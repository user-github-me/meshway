package core

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/time/rate"
)

const UDPProtocol = "/meshway/udp/1.0.0"
const maxDatagram = 65507
const maxUDPFlows = 32

// SOCKS UDP packets retain their RFC1928 address so domain resolution is done
// only at the authenticated exit. Fragmented SOCKS messages are unsupported.
func parseSOCKSDatagram(packet []byte) (target string, payload []byte, err error) {
	if len(packet) < 4 || packet[0] != 0 || packet[1] != 0 || packet[2] != 0 {
		return "", nil, errors.New("invalid or fragmented SOCKS datagram")
	}
	offset := 4
	var host string
	switch packet[3] {
	case 1:
		if len(packet) < offset+4 {
			return "", nil, io.ErrUnexpectedEOF
		}
		host = net.IP(packet[offset : offset+4]).String()
		offset += 4
	case 4:
		if len(packet) < offset+16 {
			return "", nil, io.ErrUnexpectedEOF
		}
		host = net.IP(packet[offset : offset+16]).String()
		offset += 16
	case 3:
		if len(packet) < offset+1 || packet[offset] == 0 {
			return "", nil, errors.New("invalid UDP domain")
		}
		size := int(packet[offset])
		offset++
		if len(packet) < offset+size {
			return "", nil, io.ErrUnexpectedEOF
		}
		host = string(packet[offset : offset+size])
		offset += size
	default:
		return "", nil, errors.New("unsupported UDP address")
	}
	if len(packet) < offset+2 {
		return "", nil, io.ErrUnexpectedEOF
	}
	port := binary.BigEndian.Uint16(packet[offset : offset+2])
	offset += 2
	if port == 0 {
		return "", nil, errors.New("invalid UDP port")
	}
	if len(packet)-offset > maxDatagram {
		return "", nil, errors.New("UDP payload too large")
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port))), packet[offset:], nil
}
func socksDatagram(address net.Addr, payload []byte) ([]byte, error) {
	udp, ok := address.(*net.UDPAddr)
	if !ok {
		return nil, errors.New("UDP address required")
	}
	ip := udp.IP.To4()
	kind := byte(1)
	if ip == nil {
		ip = udp.IP.To16()
		kind = 4
	}
	if ip == nil || udp.Port < 1 || udp.Port > 65535 {
		return nil, errors.New("invalid UDP response address")
	}
	packet := make([]byte, 4+len(ip)+2+len(payload))
	packet[3] = kind
	copy(packet[4:], ip)
	binary.BigEndian.PutUint16(packet[4+len(ip):], uint16(udp.Port))
	copy(packet[6+len(ip):], payload)
	return packet, nil
}
func readUDPFrame(r io.Reader) ([]byte, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(size[:])
	if n < 8 || n > maxDatagram+262 {
		return nil, errors.New("invalid UDP frame size")
	}
	packet := make([]byte, int(n))
	_, err := io.ReadFull(r, packet)
	return packet, err
}
func writeUDPFrame(w io.Writer, packet []byte) error {
	if len(packet) < 8 || len(packet) > maxDatagram+262 {
		return errors.New("invalid UDP frame size")
	}
	frame := make([]byte, 4+len(packet))
	binary.BigEndian.PutUint32(frame, uint32(len(packet)))
	copy(frame[4:], packet)
	for len(frame) > 0 {
		n, err := w.Write(frame)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}

func (n *Node) openUDP(ctx context.Context, peerID string) (network.Stream, error) {
	n.mu.Lock()
	generation := n.tunnelGeneration
	n.mu.Unlock()
	p, err := peer.Decode(peerID)
	if err != nil {
		return nil, errors.New("no valid upstream UDP exit selected")
	}
	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	stream, err := n.Host.NewStream(network.WithAllowLimitedConn(ctx, "SOCKS UDP exit"), p, UDPProtocol)
	if err != nil {
		return nil, err
	}
	stream.SetDeadline(time.Now().Add(handshakeTimeout))
	if err = writeFrame(stream, request{Service: "udp-associate"}); err != nil {
		stream.Reset()
		return nil, err
	}
	var response response
	if err = readFrame(stream, &response); err != nil {
		stream.Reset()
		return nil, err
	}
	if !response.OK {
		stream.Close()
		return nil, fmt.Errorf("UDP exit: %s", response.Error)
	}
	stream.SetDeadline(time.Time{})
	n.mu.Lock()
	if n.ctx.Err() != nil || generation != n.tunnelGeneration {
		n.mu.Unlock()
		stream.Reset()
		return nil, errors.New("selection changed or node stopped")
	}
	outgoing := &outgoingStream{Stream: stream, node: n}
	n.outbound[outgoing] = struct{}{}
	n.mu.Unlock()
	return outgoing, nil
}

func (n *Node) handleSOCKSUDP(control net.Conn, requestedHost string, requestedPort uint16) {
	// The local relay only accepts the TCP client's loopback address and either
	// its requested UDP source port or the first sender's observed source port.
	localHost, _, _ := net.SplitHostPort(control.LocalAddr().String())
	remoteHost, _, _ := net.SplitHostPort(control.RemoteAddr().String())
	remoteIP := net.ParseIP(remoteHost)
	requestedIP := net.ParseIP(requestedHost)
	if requestedIP == nil || (!requestedIP.IsUnspecified() && !requestedIP.Equal(remoteIP)) {
		socksReply(control, 2)
		return
	}
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(localHost)})
	if err != nil {
		socksReply(control, 1)
		return
	}
	defer udp.Close()
	upstream := n.UpstreamPeer()
	stream, err := n.openUDP(n.ctx, upstream)
	if err != nil {
		socksReply(control, 2)
		return
	}
	defer stream.Reset()
	upstreamID, _ := peer.Decode(upstream)
	limiter := n.bandwidth(upstreamID)
	bound := udp.LocalAddr().(*net.UDPAddr)
	reply, _ := socksDatagram(bound, nil)
	reply[0], reply[1], reply[2] = 5, 0, 0
	if _, err = control.Write(reply); err != nil {
		return
	}
	control.SetDeadline(time.Time{})
	associationCtx, cancelAssociation := context.WithCancel(n.ctx)
	defer cancelAssociation()
	done := make(chan struct{}, 3)
	var sourceMu sync.Mutex
	var source *net.UDPAddr
	go func() { _, _ = io.Copy(io.Discard, control); done <- struct{}{} }()
	go func() {
		buffer := make([]byte, maxDatagram+263)
		for {
			udp.SetReadDeadline(time.Now().Add(idleTimeout))
			size, address, err := udp.ReadFromUDP(buffer)
			if err != nil {
				done <- struct{}{}
				return
			}
			if !address.IP.Equal(remoteIP) {
				continue
			}
			sourceMu.Lock()
			if source == nil && (requestedPort == 0 || address.Port == int(requestedPort)) {
				copyAddress := *address
				source = &copyAddress
			}
			approved := source != nil && source.Port == address.Port && source.IP.Equal(address.IP)
			sourceMu.Unlock()
			if !approved {
				continue
			}
			packet := buffer[:size]
			if _, _, err = parseSOCKSDatagram(packet); err != nil {
				continue
			}
			if err = waitBandwidth(associationCtx, limiter, len(packet)); err != nil {
				done <- struct{}{}
				return
			}
			if err = writeUDPFrame(activityConn{stream}, packet); err != nil {
				done <- struct{}{}
				return
			}
			n.bytesSent.Add(uint64(size))
		}
	}()
	go func() {
		for {
			packet, err := readUDPFrame(activityConn{stream})
			if err != nil {
				done <- struct{}{}
				return
			}
			if _, _, err = parseSOCKSDatagram(packet); err != nil {
				done <- struct{}{}
				return
			}
			sourceMu.Lock()
			address := source
			sourceMu.Unlock()
			if address == nil {
				continue
			}
			if err = waitBandwidth(associationCtx, limiter, len(packet)); err != nil {
				done <- struct{}{}
				return
			}
			udp.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err = udp.WriteToUDP(packet, address); err != nil {
				done <- struct{}{}
				return
			}
			n.bytesReceived.Add(uint64(len(packet)))
		}
	}()
	<-done
	cancelAssociation()
	udp.Close()
	stream.Reset()
	control.Close()
	<-done
	<-done
}

type udpFlow struct {
	connection net.Conn
	target     string
}
type udpExitSession struct {
	node    *Node
	stream  network.Stream
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	writeMu sync.Mutex
	closed  bool
	flows   map[string]*udpFlow
	wg      sync.WaitGroup
	limiter *rate.Limiter
}

func (n *Node) handleUDP(stream network.Stream) {
	select {
	case n.sessions <- struct{}{}:
	default:
		stream.Reset()
		return
	}
	defer func() { <-n.sessions }()
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(handshakeTimeout))
	if !n.Config.Exit || !n.authorized(n.ctx, stream.Conn().RemotePeer()) {
		writeFrame(stream, response{Error: "UDP exit requires explicit consent and peer approval"})
		return
	}
	var req request
	if err := readFrame(stream, &req); err != nil || req.Service != "udp-associate" || req.Target != "" {
		stream.Reset()
		return
	}
	if err := writeFrame(stream, response{OK: true}); err != nil {
		return
	}
	stream.SetDeadline(time.Time{})
	ctx, cancel := context.WithCancel(n.ctx)
	session := &udpExitSession{node: n, stream: stream, ctx: ctx, cancel: cancel, flows: map[string]*udpFlow{}, limiter: n.bandwidth(stream.Conn().RemotePeer())}
	defer session.close()
	for {
		packet, err := readUDPFrame(activityConn{stream})
		if err != nil {
			return
		}
		target, payload, err := parseSOCKSDatagram(packet)
		if err != nil {
			continue
		}
		flow, err := session.flow(target)
		if err != nil {
			continue
		}
		if err = waitBandwidth(ctx, session.limiter, len(payload)); err != nil {
			return
		}
		flow.connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err = flow.connection.Write(payload); err != nil {
			session.remove(flow)
		}
		n.bytesReceived.Add(uint64(len(payload)))
	}
}
func dialUDPTarget(ctx context.Context, target string, allowPrivate bool) (net.Conn, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return nil, errors.New("invalid UDP port")
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(addresses) == 0 || len(addresses) > 32 {
		return nil, errors.New("invalid UDP DNS answer")
	}
	for _, ip := range addresses {
		if !allowPrivate && !publicIP(ip) {
			return nil, errors.New("private UDP target denied")
		}
	}
	var last error
	for _, ip := range addresses {
		var c net.Conn
		c, last = (&net.Dialer{}).DialContext(ctx, "udp", net.JoinHostPort(ip.Unmap().String(), port))
		if last == nil {
			return c, nil
		}
	}
	return nil, last
}
func (s *udpExitSession) flow(target string) (*udpFlow, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, io.EOF
	}
	if f := s.flows[target]; f != nil {
		s.mu.Unlock()
		return f, nil
	}
	if len(s.flows) >= maxUDPFlows {
		s.mu.Unlock()
		return nil, errors.New("UDP flow limit reached")
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(s.ctx, handshakeTimeout)
	defer cancel()
	conn, err := dialUDPTarget(ctx, target, s.node.Config.AllowPrivateTargets)
	if err != nil {
		return nil, err
	}
	flow := &udpFlow{connection: conn, target: target}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		conn.Close()
		return nil, io.EOF
	}
	s.flows[target] = flow
	s.wg.Add(1)
	s.mu.Unlock()
	go s.read(flow)
	return flow, nil
}
func (s *udpExitSession) read(flow *udpFlow) {
	defer s.wg.Done()
	defer s.remove(flow)
	buffer := make([]byte, maxDatagram)
	for {
		flow.connection.SetReadDeadline(time.Now().Add(60 * time.Second))
		n, err := flow.connection.Read(buffer)
		if err != nil {
			return
		}
		packet, err := socksDatagram(flow.connection.RemoteAddr(), buffer[:n])
		if err != nil {
			return
		}
		if err = waitBandwidth(s.ctx, s.limiter, n); err != nil {
			return
		}
		s.writeMu.Lock()
		err = writeUDPFrame(activityConn{s.stream}, packet)
		s.writeMu.Unlock()
		if err != nil {
			s.cancel()
			s.stream.Reset()
			return
		}
		s.node.bytesSent.Add(uint64(n))
	}
}
func (s *udpExitSession) remove(flow *udpFlow) {
	flow.connection.Close()
	s.mu.Lock()
	if s.flows[flow.target] == flow {
		delete(s.flows, flow.target)
	}
	s.mu.Unlock()
}
func (s *udpExitSession) close() {
	s.cancel()
	s.mu.Lock()
	s.closed = true
	for _, flow := range s.flows {
		flow.connection.Close()
	}
	s.mu.Unlock()
	s.stream.Reset()
	s.wg.Wait()
}
