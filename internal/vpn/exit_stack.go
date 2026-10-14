package vpn

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/sagernet/gvisor/pkg/buffer"
	"github.com/sagernet/gvisor/pkg/tcpip"
	"github.com/sagernet/gvisor/pkg/tcpip/adapters/gonet"
	"github.com/sagernet/gvisor/pkg/tcpip/header"
	"github.com/sagernet/gvisor/pkg/tcpip/link/channel"
	"github.com/sagernet/gvisor/pkg/tcpip/network/ipv4"
	"github.com/sagernet/gvisor/pkg/tcpip/network/ipv6"
	"github.com/sagernet/gvisor/pkg/tcpip/stack"
	"github.com/sagernet/gvisor/pkg/tcpip/transport/icmp"
	"github.com/sagernet/gvisor/pkg/tcpip/transport/tcp"
	"github.com/sagernet/gvisor/pkg/tcpip/transport/udp"
	"github.com/sagernet/gvisor/pkg/waiter"
)

// Each authenticated device receives its own IP stack. A peer cannot inject
// packets into another peer's flow, impersonate its source, or use IP spoofing
// at the exit: remote TCP/UDP is re-originated through ordinary OS sockets.
type internetStack struct {
	options Options
	ctx     context.Context
	cancel  context.CancelFunc
	stack   *stack.Stack
	link    *channel.Endpoint
	mu      sync.Mutex
	closed  bool
	flows   map[net.Conn]struct{}
	slots   chan struct{}
	wg      sync.WaitGroup
	once    sync.Once
}

func newInternetStack(parent context.Context, options Options) (*internetStack, error) {
	ctx, cancel := context.WithCancel(parent)
	g := &internetStack{ctx: ctx, cancel: cancel, options: options, flows: map[net.Conn]struct{}{}, slots: make(chan struct{}, options.MaxFlowsPerSession)}
	g.link = channel.New(256, uint32(options.MTU), "")
	g.stack = stack.New(stack.Options{NetworkProtocols: []stack.NetworkProtocolFactory{ipv4.NewProtocolWithOptions(ipv4.Options{AllowExternalLoopbackTraffic: options.AllowPrivateTargets}), ipv6.NewProtocolWithOptions(ipv6.Options{AllowExternalLoopbackTraffic: options.AllowPrivateTargets})}, TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol, icmp.NewProtocol4, icmp.NewProtocol6}})
	if err := g.stack.CreateNIC(1, g.link); err != nil {
		g.Close()
		return nil, errors.New(err.String())
	}
	if err := g.stack.SetPromiscuousMode(1, true); err != nil {
		g.Close()
		return nil, errors.New(err.String())
	}
	if err := g.stack.SetSpoofing(1, true); err != nil {
		g.Close()
		return nil, errors.New(err.String())
	}
	g.stack.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}, {Destination: header.IPv6EmptySubnet, NIC: 1}})
	// Small bounded buffers keep a large set of approved users from consuming
	// hundreds of megabytes for each idle connection.
	rx := tcpip.TCPReceiveBufferSizeRangeOption{Min: 4096, Default: 64 << 10, Max: 256 << 10}
	tx := tcpip.TCPSendBufferSizeRangeOption{Min: 4096, Default: 64 << 10, Max: 256 << 10}
	_ = g.stack.SetTransportProtocolOption(tcp.ProtocolNumber, &rx)
	_ = g.stack.SetTransportProtocolOption(tcp.ProtocolNumber, &tx)
	sack := tcpip.TCPSACKEnabled(true)
	_ = g.stack.SetTransportProtocolOption(tcp.ProtocolNumber, &sack)
	g.stack.SetTransportProtocolHandler(tcp.ProtocolNumber, tcp.NewForwarder(g.stack, 64<<10, options.MaxFlowsPerSession, g.forwardTCP).HandlePacket)
	// Use a synchronous request against the existing packet reference. gVisor's
	// standard UDP forwarder clones a request packet without a release API.
	g.stack.SetTransportProtocolHandler(udp.ProtocolNumber, g.forwardUDP)
	return g, nil
}
func (g *internetStack) Inject(packet []byte) {
	if ValidatePacket(packet, g.options.MTU) != nil || !validClientSource(packetIP(packet, true)) {
		return
	}
	// Reject fragmented/extension-header traffic instead of letting it bypass
	// the early destination/protocol check. TCP segmentation and MTU 1280 avoid
	// fragmentation; native clients must honor the advertised MTU.
	if packet[0]>>4 == 4 && (packet[6]&0x3f != 0 || packet[7] != 0) {
		return
	}
	proto := packetProtocol(packet)
	if proto != 6 && proto != 17 {
		return
	}
	dst := packetIP(packet, false)
	dns := dst == netip.MustParseAddr(DNSIPv4) || dst == netip.MustParseAddr(DNSIPv6)
	if dns {
		if packetPort(packet) != 53 {
			return
		}
	} else if !g.options.AllowPrivateTargets && !publicDestination(dst) {
		return
	}
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(packet)})
	number := header.IPv4ProtocolNumber
	if packet[0]>>4 == 6 {
		number = header.IPv6ProtocolNumber
	}
	g.link.InjectInbound(tcpip.NetworkProtocolNumber(number), pkt)
	pkt.DecRef()
}
func (g *internetStack) ReadPacket(ctx context.Context) ([]byte, error) {
	pkt := g.link.ReadContext(ctx)
	if pkt == nil {
		return nil, io.EOF
	}
	defer pkt.DecRef()
	view := pkt.ToView()
	defer view.Release()
	return append([]byte(nil), view.AsSlice()...), nil
}
func addressIP(a tcpip.Address) netip.Addr {
	if a.Len() == 4 {
		return netip.AddrFrom4(a.As4())
	}
	if a.Len() == 16 {
		return netip.AddrFrom16(a.As16())
	}
	return netip.Addr{}
}
func (g *internetStack) target(id stack.TransportEndpointID) (string, error) {
	dst := addressIP(id.LocalAddress)
	if !dst.IsValid() || id.LocalPort == 0 {
		return "", errors.New("invalid destination")
	}
	if dst == netip.MustParseAddr(DNSIPv4) || dst == netip.MustParseAddr(DNSIPv6) {
		if id.LocalPort != 53 {
			return "", errors.New("DNS gateway only accepts port 53")
		}
		return g.options.DNSResolver, nil
	}
	if !g.options.AllowPrivateTargets && !publicDestination(dst) {
		return "", errors.New("private or reserved destination is disabled")
	}
	return net.JoinHostPort(dst.String(), strconv.Itoa(int(id.LocalPort))), nil
}
func (g *internetStack) acquire() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	select {
	case g.slots <- struct{}{}:
		g.wg.Add(1)
		return true
	default:
		return false
	}
}
func (g *internetStack) release() { <-g.slots; g.wg.Done() }
func (g *internetStack) track(c net.Conn) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		c.Close()
		return false
	}
	g.flows[c] = struct{}{}
	return true
}
func (g *internetStack) forget(c net.Conn) { c.Close(); g.mu.Lock(); delete(g.flows, c); g.mu.Unlock() }
func (g *internetStack) dial(network, target string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(g.ctx, 10*time.Second)
	defer cancel()
	return (&net.Dialer{}).DialContext(ctx, network, target)
}
func (g *internetStack) forwardTCP(r *tcp.ForwarderRequest) {
	if !g.acquire() {
		r.Complete(true)
		return
	}
	defer g.release()
	target, err := g.target(r.ID())
	if err != nil {
		r.Complete(true)
		return
	}
	remote, err := g.dial("tcp", target)
	if err != nil {
		r.Complete(true)
		return
	}
	if !g.track(remote) {
		r.Complete(true)
		return
	}
	defer g.forget(remote)
	var queue waiter.Queue
	endpoint, tcpErr := r.CreateEndpoint(&queue)
	if tcpErr != nil {
		r.Complete(true)
		return
	}
	r.Complete(false)
	local := gonet.NewTCPConn(&queue, endpoint)
	if !g.track(local) {
		return
	}
	defer g.forget(local)
	copyTCP(local, remote)
}
func (g *internetStack) forwardUDP(id stack.TransportEndpointID, pkt *stack.PacketBuffer) bool {
	if !g.acquire() {
		return true
	}
	target, err := g.target(id)
	if err != nil {
		g.release()
		return true
	}
	var queue waiter.Queue
	endpoint, tcpErr := udp.NewForwarderRequest(g.stack, id, pkt).CreateEndpoint(&queue)
	if tcpErr != nil {
		g.release()
		return true
	}
	local := gonet.NewUDPConn(&queue, endpoint)
	if !g.track(local) {
		g.release()
		return true
	}
	go func() {
		defer g.release()
		defer g.forget(local)
		remote, err := g.dial("udp", target)
		if err != nil {
			return
		}
		if !g.track(remote) {
			return
		}
		defer g.forget(remote)
		copyUDP(local, remote)
	}()
	return true
}

// Rolling idle timeouts release abandoned flows while allowing active transfers.
type idleConn struct {
	net.Conn
	idle time.Duration
}

func (c idleConn) Read(p []byte) (int, error) {
	_ = c.Conn.SetReadDeadline(time.Now().Add(c.idle))
	return c.Conn.Read(p)
}
func (c idleConn) Write(p []byte) (int, error) {
	_ = c.Conn.SetWriteDeadline(time.Now().Add(c.idle))
	return c.Conn.Write(p)
}
func copyTCP(left, right net.Conn) {
	done := make(chan struct{}, 2)
	copyOne := func(dst, src net.Conn) {
		_, _ = io.Copy(idleConn{dst, 2 * time.Minute}, idleConn{src, 2 * time.Minute})
		if c, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = c.CloseWrite()
		}
		done <- struct{}{}
	}
	go copyOne(left, right)
	go copyOne(right, left)
	<-done
	_ = left.SetDeadline(time.Now().Add(5 * time.Second))
	_ = right.SetDeadline(time.Now().Add(5 * time.Second))
	<-done
}
func copyUDP(left, right net.Conn) {
	done := make(chan struct{}, 2)
	copyOne := func(dst, src net.Conn) {
		buf := make([]byte, 65535)
		for {
			_ = src.SetReadDeadline(time.Now().Add(60 * time.Second))
			n, err := src.Read(buf)
			if err != nil {
				done <- struct{}{}
				return
			}
			_ = dst.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err = dst.Write(buf[:n]); err != nil {
				done <- struct{}{}
				return
			}
		}
	}
	go copyOne(left, right)
	go copyOne(right, left)
	<-done
	left.Close()
	right.Close()
	<-done
}
func (g *internetStack) Close() {
	g.once.Do(func() {
		g.cancel()
		g.mu.Lock()
		g.closed = true
		for c := range g.flows {
			c.Close()
		}
		g.mu.Unlock()
		g.link.Close()
		g.stack.Close()
		for _, endpoint := range g.stack.CleanupEndpoints() {
			endpoint.Abort()
		}
		g.wg.Wait()
		g.link.Drain()
	})
}
