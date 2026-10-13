package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	"github.com/meshway/meshway/internal/account"
	"github.com/meshway/meshway/internal/config"
	"github.com/meshway/meshway/internal/discovery"
	"github.com/meshway/meshway/internal/state"
	"github.com/meshway/meshway/internal/vpn"
)

const Version = "0.1.0-dev"

type Node struct {
	Config           config.Config
	Host             host.Host
	ctx              context.Context
	cancel           context.CancelFunc
	started          time.Time
	mu               sync.Mutex
	listeners        []net.Listener
	connections      map[net.Conn]struct{}
	server           *http.Server
	relay            *relay.Relay
	vpn              *vpn.Service
	Account          *account.Store
	stateLock        *state.Lock
	Discovery        *discovery.Service
	stopOnce         sync.Once
	stopErr          error
	done             chan struct{}
	bytesSent        atomic.Uint64
	bytesReceived    atomic.Uint64
	sessions         chan struct{}
	socksAddress     string
	apiAddress       string
	upstreamPeer     string
	outbound         map[network.Stream]struct{}
	tunnelGeneration uint64
}

type PeerInfo struct {
	ID        string   `json:"id"`
	Addresses []string `json:"addresses"`
}
type Roles struct {
	Relay bool `json:"relay"`
	Exit  bool `json:"exit"`
}
type Status struct {
	Version           string     `json:"version"`
	PeerID            string     `json:"peer_id"`
	Addresses         []string   `json:"addresses"`
	Peers             []PeerInfo `json:"peers"`
	Roles             Roles      `json:"roles"`
	SOCKSListen       string     `json:"socks_listen"`
	APIListen         string     `json:"api_listen"`
	UpstreamPeer      string     `json:"upstream_peer"`
	UpstreamConnected bool       `json:"upstream_connected"`
	StartedAt         time.Time  `json:"started_at"`
	BytesSent         uint64     `json:"bytes_sent"`
	BytesReceived     uint64     `json:"bytes_received"`
}

func Start(parent context.Context, c config.Config) (*Node, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	lock, err := state.Acquire(c.StateDir)
	if err != nil {
		return nil, err
	}
	key, err := Identity(c.StateDir)
	if err != nil {
		lock.Close()
		return nil, err
	}
	opts := []libp2p.Option{libp2p.Identity(key), libp2p.ListenAddrStrings(c.ListenAddresses...), libp2p.EnableRelay(), libp2p.EnableHolePunching(), libp2p.NATPortMap()}
	// Relay reservations are requested only from configured bootstrap peers.
	var bootstrap []peer.AddrInfo
	for _, a := range c.BootstrapPeers {
		p, err := peer.AddrInfoFromString(a)
		if err != nil {
			lock.Close()
			return nil, err
		}
		bootstrap = append(bootstrap, *p)
	}
	if len(bootstrap) > 0 {
		opts = append(opts, libp2p.EnableAutoRelayWithStaticRelays(bootstrap))
	}
	h, err := libp2p.New(opts...)
	if err != nil {
		lock.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	n := &Node{Config: c, Host: h, ctx: ctx, cancel: cancel, started: time.Now().UTC(), connections: make(map[net.Conn]struct{}), done: make(chan struct{}), sessions: make(chan struct{}, 128), stateLock: lock, upstreamPeer: c.UpstreamPeer, outbound: map[network.Stream]struct{}{}}
	n.Account, err = account.New(c.StateDir, h, account.Options{SyncEnabled: c.AccountSync, Label: c.DeviceLabel})
	if err != nil {
		n.Stop()
		return nil, err
	}
	n.Discovery, err = discovery.New(h, discovery.Options{Advertise: c.Advertise, Label: c.ServerLabel, Country: c.ServerCountry, Relay: c.Relay, Exit: c.Exit, AllowedPeers: c.AllowedPeers, Personal: c.PersonalServer, Authorize: func(ctx context.Context, id peer.ID) bool { return n.authorized(ctx, id) }})
	if err != nil {
		n.Stop()
		return nil, err
	}
	if c.Relay {
		n.relay, err = relay.New(h, relay.WithResources(relay.Resources{MaxReservations: 128, MaxCircuits: 16, BufferSize: 2048, ReservationTTL: time.Hour, MaxReservationsPerPeer: 1, MaxReservationsPerIP: 8, MaxReservationsPerASN: 32, Limit: &relay.RelayLimit{Duration: 2 * time.Minute, Data: 64 << 20}}))
		if err != nil {
			n.Stop()
			return nil, err
		}
	}
	h.SetStreamHandler(TunnelProtocol, n.handleTunnel)
	if c.VPN != nil && c.VPN.Enabled {
		vpnOptions := vpn.Options{AllowedPeers: c.AllowedPeers, MTU: c.VPN.MTU}
		if c.PersonalServer {
			vpnOptions.Authorize = func(ctx context.Context, id peer.ID) bool { return n.authorized(ctx, id) }
		}
		n.vpn, err = vpn.New(h, vpnOptions)
		if err != nil {
			n.Stop()
			return nil, err
		}
	}
	if c.SOCKSListen != "" {
		l, err := n.listen(c.SOCKSListen)
		if err != nil {
			n.Stop()
			return nil, fmt.Errorf("SOCKS listener: %w", err)
		}
		n.socksAddress = l.Addr().String()
		go n.accept(l, n.handleSOCKS)
	}
	for _, f := range c.Forwards {
		l, err := n.listen(f.Listen)
		if err != nil {
			n.Stop()
			return nil, fmt.Errorf("forward %s: %w", f.Name, err)
		}
		forward := f
		go n.accept(l, func(c net.Conn) {
			defer c.Close()
			p := forward.Peer
			if p == "" {
				p = n.UpstreamPeer()
			}
			s, err := n.openTunnel(n.ctx, p, request{Target: forward.Target, Service: forward.Service})
			if err != nil {
				return
			}
			defer s.Close()
			n.bridge(c, s)
		})
	}
	if c.APIListen != "" {
		l, err := n.listen(c.APIListen)
		if err != nil {
			n.Stop()
			return nil, fmt.Errorf("API listener: %w", err)
		}
		n.apiAddress = l.Addr().String()
		n.server = &http.Server{Handler: n.apiHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
		go func() { _ = n.server.Serve(l) }()
	}
	for _, p := range bootstrap {
		p := p
		go func() {
			n.maintainPeer(p, 30*time.Second)
		}()
	}
	if c.VPN != nil && c.VPN.Enabled && c.VPN.Device != "" {
		fd, err := vpn.OpenDevice(c.VPN.Device)
		if err != nil {
			n.Stop()
			return nil, fmt.Errorf("open VPN device: %w", err)
		}
		original := os.NewFile(uintptr(fd), "meshway-original-tun")
		err = n.vpn.AttachFD(fd, c.VPN.Peer)
		if original != nil {
			_ = original.Close()
		}
		if err != nil {
			n.Stop()
			return nil, err
		}
	}
	go func() { <-ctx.Done(); n.Stop() }()
	_ = n.Account.Record("daemon_started", "")
	return n, nil
}

func (n *Node) maintainPeer(p peer.AddrInfo, interval time.Duration) {
	for {
		if !n.peerConnected(p.ID) {
			ctx, cancel := context.WithTimeout(n.ctx, 15*time.Second)
			_ = n.Host.Connect(ctx, p)
			cancel()
		}
		timer := time.NewTimer(interval)
		select {
		case <-n.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (n *Node) peerConnected(id peer.ID) bool {
	for _, c := range n.Host.Network().ConnsToPeer(id) {
		if !c.IsClosed() {
			return true
		}
	}
	return false
}

func (n *Node) listen(addr string) (net.Listener, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	n.listeners = append(n.listeners, l)
	n.mu.Unlock()
	return l, nil
}

func (n *Node) accept(l net.Listener, handler func(net.Conn)) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		select {
		case n.sessions <- struct{}{}:
		default:
			c.Close()
			continue
		}
		n.mu.Lock()
		if n.ctx.Err() != nil {
			n.mu.Unlock()
			<-n.sessions
			c.Close()
			return
		}
		n.connections[c] = struct{}{}
		n.mu.Unlock()
		go func() {
			defer func() { c.Close(); n.mu.Lock(); delete(n.connections, c); n.mu.Unlock(); <-n.sessions }()
			handler(c)
		}()
	}
}

func (n *Node) Stop() error {
	n.stopOnce.Do(func() {
		n.cancel()
		if n.Account != nil {
			_ = n.Account.Record("daemon_stopped", "")
			_ = n.Account.Close()
		}
		if n.Discovery != nil {
			_ = n.Discovery.Close()
		}
		n.mu.Lock()
		for _, l := range n.listeners {
			_ = l.Close()
		}
		for c := range n.connections {
			_ = c.Close()
		}
		n.mu.Unlock()
		if n.server != nil {
			_ = n.server.Close()
		}
		if n.relay != nil {
			_ = n.relay.Close()
		}
		if n.vpn != nil {
			_ = n.vpn.Close()
		}
		n.stopErr = n.Host.Close()
		if n.stateLock != nil {
			_ = n.stateLock.Close()
		}
		close(n.done)
	})
	return n.stopErr
}
func (n *Node) Done() <-chan struct{} { return n.done }

func (n *Node) StartVPN(fd int, peerID string) error {
	if n.vpn == nil {
		return errors.New("VPN is disabled; enable vpn.enabled in configuration")
	}
	return n.vpn.AttachFD(fd, peerID)
}

func (n *Node) Connect(ctx context.Context, address string) (PeerInfo, error) {
	p, err := peer.AddrInfoFromString(address)
	if err != nil {
		return PeerInfo{}, err
	}
	if err = n.Host.Connect(ctx, *p); err != nil {
		return PeerInfo{}, err
	}
	if n.Account != nil {
		_ = n.Account.Record("peer_connected", p.ID.String())
	}
	return n.peerInfo(p.ID), nil
}
func (n *Node) peerInfo(id peer.ID) PeerInfo {
	addresses := []string{}
	for _, a := range n.Host.Peerstore().Addrs(id) {
		addresses = append(addresses, a.String())
	}
	sort.Strings(addresses)
	return PeerInfo{ID: id.String(), Addresses: addresses}
}
func (n *Node) Status() Status {
	addresses := []string{}
	for _, a := range n.Host.Addrs() {
		addresses = append(addresses, fmt.Sprintf("%s/p2p/%s", a, n.Host.ID()))
	}
	sort.Strings(addresses)
	peers := []PeerInfo{}
	for _, p := range n.Host.Network().Peers() {
		peers = append(peers, n.peerInfo(p))
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].ID < peers[j].ID })
	upstream := n.UpstreamPeer()
	connected := false
	if id, err := peer.Decode(upstream); err == nil {
		connected = n.peerConnected(id)
	}
	return Status{Version: Version, PeerID: n.Host.ID().String(), Addresses: addresses, Peers: peers, Roles: Roles{Relay: n.Config.Relay, Exit: n.Config.Exit}, SOCKSListen: n.socksAddress, APIListen: n.apiAddress, UpstreamPeer: upstream, UpstreamConnected: connected, StartedAt: n.started, BytesSent: n.bytesSent.Load(), BytesReceived: n.bytesReceived.Load()}
}

func (n *Node) UpstreamPeer() string { n.mu.Lock(); defer n.mu.Unlock(); return n.upstreamPeer }

func (n *Node) SelectPeer(ctx context.Context, peerID, address string) error {
	p, err := peer.Decode(peerID)
	if err != nil {
		return err
	}
	if address != "" {
		if err = discovery.ValidateAddress(address, p); err != nil {
			return err
		}
		if _, err = n.Connect(ctx, address); err != nil {
			return err
		}
	}
	server, err := n.Discovery.Server(ctx, p)
	if err != nil {
		return fmt.Errorf("server unavailable: %w", err)
	}
	if !server.Roles.Exit {
		return errors.New("this peer does not provide internet exit connections")
	}
	if !server.CanConnect {
		return errors.New("server approval required; ask its operator to allow your peer ID")
	}
	n.mu.Lock()
	streams := n.takeOutgoing()
	n.upstreamPeer = p.String()
	n.mu.Unlock()
	for _, stream := range streams {
		_ = stream.Reset()
	}
	_ = n.Account.Record("server_selected", p.String())
	return nil
}

// Disconnect ends outgoing TCP proxy sessions while leaving this node's
// independently enabled server and relay roles running.
func (n *Node) Disconnect() {
	n.mu.Lock()
	n.upstreamPeer = ""
	streams := n.takeOutgoing()
	n.mu.Unlock()
	for _, stream := range streams {
		_ = stream.Reset()
	}
	_ = n.Account.Record("server_disconnected", "")
}

// takeOutgoing must be called while mu is held.
func (n *Node) takeOutgoing() []network.Stream {
	n.tunnelGeneration++
	streams := make([]network.Stream, 0, len(n.outbound))
	for stream := range n.outbound {
		streams = append(streams, stream)
	}
	n.outbound = map[network.Stream]struct{}{}
	return streams
}

type outgoingStream struct {
	network.Stream
	node *Node
}

func (s *outgoingStream) forget()      { s.node.mu.Lock(); delete(s.node.outbound, s); s.node.mu.Unlock() }
func (s *outgoingStream) Close() error { s.forget(); return s.Stream.Close() }
func (s *outgoingStream) Reset() error { s.forget(); return s.Stream.Reset() }

func (n *Node) authorized(ctx context.Context, id peer.ID) bool {
	if n.Config.PersonalServer {
		return n.Account.AuthorizePeer(ctx, id) == nil
	}
	for _, allowed := range n.Config.AllowedPeers {
		if allowed == id.String() {
			return true
		}
	}
	return false
}
func (n *Node) handleTunnel(s network.Stream) {
	select {
	case n.sessions <- struct{}{}:
	default:
		_ = s.Reset()
		return
	}
	defer func() { <-n.sessions }()
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(handshakeTimeout))
	if !n.authorized(n.ctx, s.Conn().RemotePeer()) {
		_ = writeFrame(s, response{Error: "peer is not allowed"})
		return
	}
	var req request
	if err := readFrame(s, &req); err != nil {
		_ = s.Reset()
		return
	}
	if (req.Target == "") == (req.Service == "") {
		_ = writeFrame(s, response{Error: "provide one target or service"})
		return
	}
	target := req.Target
	allowPrivate := n.Config.AllowPrivateTargets
	if req.Service != "" {
		target = ""
		for _, service := range n.Config.Services {
			if service.Name == req.Service {
				target = service.Target
				allowPrivate = true
				break
			}
		}
		if target == "" {
			_ = writeFrame(s, response{Error: "unknown service"})
			return
		}
	} else if !n.Config.Exit {
		_ = writeFrame(s, response{Error: "internet exit is disabled"})
		return
	}
	ctx, cancel := context.WithTimeout(n.ctx, handshakeTimeout)
	defer cancel()
	c, err := dialTarget(ctx, target, allowPrivate)
	if err != nil {
		_ = writeFrame(s, response{Error: err.Error()})
		return
	}
	defer c.Close()
	if err = writeFrame(s, response{OK: true}); err != nil {
		return
	}
	_ = s.SetDeadline(time.Time{})
	n.bridge(c, s)
}

func (n *Node) openTunnel(ctx context.Context, peerID string, req request) (network.Stream, error) {
	n.mu.Lock()
	generation := n.tunnelGeneration
	n.mu.Unlock()
	if peerID == "" {
		return nil, errors.New("no upstream peer configured")
	}
	p, err := peer.Decode(peerID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	s, err := n.Host.NewStream(network.WithAllowLimitedConn(ctx, "configured tunnel peer"), p, TunnelProtocol)
	if err != nil {
		return nil, err
	}
	_ = s.SetDeadline(time.Now().Add(handshakeTimeout))
	if err = writeFrame(s, req); err != nil {
		_ = s.Reset()
		return nil, err
	}
	var resp response
	if err = readFrame(s, &resp); err != nil {
		_ = s.Reset()
		return nil, err
	}
	if !resp.OK {
		_ = s.Close()
		return nil, fmt.Errorf("gateway: %s", resp.Error)
	}
	_ = s.SetDeadline(time.Time{})
	n.mu.Lock()
	if n.ctx.Err() != nil || generation != n.tunnelGeneration {
		n.mu.Unlock()
		_ = s.Reset()
		return nil, errors.New("connection selection changed or node stopped")
	}
	outgoing := &outgoingStream{Stream: s, node: n}
	n.outbound[outgoing] = struct{}{}
	n.mu.Unlock()
	return outgoing, nil
}

func (n *Node) bridge(c net.Conn, s network.Stream) {
	defer func() { n.mu.Lock(); delete(n.outbound, s); n.mu.Unlock() }()
	stop := context.AfterFunc(n.ctx, func() { _ = c.Close(); _ = s.Reset() })
	defer stop()
	done := make(chan struct{})
	go func() {
		_, err := io.Copy(countingWriter{Writer: activityConn{s}, counter: &n.bytesSent}, activityConn{c})
		if err != nil {
			_ = c.Close()
			_ = s.Reset()
		}
		_ = s.CloseWrite()
		if v, ok := c.(interface{ CloseRead() error }); ok {
			_ = v.CloseRead()
		}
		close(done)
	}()
	_, err := io.Copy(countingWriter{Writer: activityConn{c}, counter: &n.bytesReceived}, activityConn{s})
	if err != nil {
		_ = c.Close()
		_ = s.Reset()
	}
	if v, ok := c.(interface{ CloseWrite() error }); ok {
		_ = v.CloseWrite()
	}
	_ = s.CloseRead()
	<-done
}
