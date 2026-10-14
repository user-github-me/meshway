package vpn

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"golang.org/x/time/rate"
)

// ExitProtocol is distinct from the backwards-compatible point-to-point link.
const ExitProtocol protocol.ID = "/meshway/ip-exit/1.0.0"
const DNSIPv4 = "10.77.0.1"
const DNSIPv6 = "fd77::1"
const ClientIPv4 = "10.77.0.2"
const ClientIPv6 = "fd77::2"

type Status struct {
	Active bool   `json:"active"`
	PeerID string `json:"peer_id,omitempty"`
	Mode   string `json:"mode,omitempty"`
	Device string `json:"device,omitempty"`
	Error  string `json:"error,omitempty"`
}

type exitService struct {
	host    host.Host
	options Options
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	closed  bool
	inbound map[peer.ID]*exitSession
	pending map[network.Stream]struct{}
	client  *exitClient
	wg      sync.WaitGroup
}

type exitSession struct {
	stream network.Stream
	cancel context.CancelFunc
}

type exitClient struct {
	service    *exitService
	ctx        context.Context
	cancel     context.CancelFunc
	peer       peer.ID
	device     io.ReadWriteCloser
	queue      *packetQueue
	deviceName string
	mu         sync.Mutex
	stream     network.Stream
	err        string
	connecting bool
	wg         sync.WaitGroup
}

type queuedPacket struct {
	packet []byte
	err    error
}
type packetQueue struct {
	input  chan []byte
	output chan queuedPacket
	done   chan struct{}
	once   sync.Once
}

func newPacketQueue() *packetQueue {
	return &packetQueue{input: make(chan []byte, 256), output: make(chan queuedPacket, 256), done: make(chan struct{})}
}
func (q *packetQueue) Read(p []byte) (int, error) {
	select {
	case <-q.done:
		return 0, io.EOF
	case packet := <-q.input:
		return copy(p, packet), nil
	}
}
func (q *packetQueue) Write(p []byte) (int, error) {
	packet := append([]byte(nil), p...)
	select {
	case <-q.done:
		return 0, io.EOF
	case q.output <- queuedPacket{packet: packet}:
		return len(p), nil
	default:
		return len(p), nil /* bounded packet loss is preferable to blocking network progress */
	}
}
func (q *packetQueue) Close() error { q.once.Do(func() { close(q.done) }); return nil }
func (q *packetQueue) failure(err error) {
	select {
	case q.output <- queuedPacket{err: err}:
	default:
	}
}

func newExitService(h host.Host, options Options) (*exitService, error) {
	if options.MTU == 0 {
		options.MTU = DefaultMTU
	}
	if options.DNSResolver == "" {
		options.DNSResolver = "1.1.1.1:53"
	}
	dns, err := netip.ParseAddrPort(options.DNSResolver)
	if err != nil || dns.Port() == 0 {
		return nil, errors.New("VPN DNS resolver must be a literal IP:port")
	}
	if !options.AllowPrivateTargets && !publicDestination(dns.Addr()) {
		return nil, errors.New("private VPN DNS resolver requires allow_private_targets")
	}
	if options.MaxExitSessions == 0 {
		options.MaxExitSessions = 16
	}
	if options.MaxExitSessions < 1 || options.MaxExitSessions > 128 {
		return nil, errors.New("VPN max sessions must be 1..128")
	}
	if options.MaxFlowsPerSession == 0 {
		options.MaxFlowsPerSession = 128
	}
	if options.MaxFlowsPerSession < 1 || options.MaxFlowsPerSession > 1024 {
		return nil, errors.New("VPN max flows must be 1..1024")
	}
	if options.BytesPerSecond < 0 {
		return nil, errors.New("VPN byte rate cannot be negative")
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &exitService{host: h, options: options, ctx: ctx, cancel: cancel, inbound: map[peer.ID]*exitSession{}, pending: map[network.Stream]struct{}{}}
	if options.Exit {
		h.SetStreamHandler(ExitProtocol, e.accept)
	}
	return e, nil
}

func (e *exitService) approved(ctx context.Context, p peer.ID) bool {
	if e.options.Authorize != nil {
		return e.options.Authorize(ctx, p)
	}
	for _, id := range e.options.AllowedPeers {
		if id == p.String() {
			return true
		}
	}
	return false
}

// The handshake is bounded, has a separate version, and agrees on a packet MTU.
func exitHandshake(stream network.Stream, mtu int, outbound bool) error {
	_ = stream.SetDeadline(time.Now().Add(10 * time.Second))
	defer stream.SetDeadline(time.Time{})
	magic := []byte{'M', 'E', 1, byte(mtu >> 8), byte(mtu)}
	other := make([]byte, 5)
	if outbound {
		if _, err := stream.Write(magic); err != nil {
			return err
		}
		if _, err := io.ReadFull(stream, other); err != nil {
			return err
		}
	} else {
		if _, err := io.ReadFull(stream, other); err != nil {
			return err
		}
		if _, err := stream.Write(magic); err != nil {
			return err
		}
	}
	if string(magic) != string(other) {
		return errors.New("Internet VPN version or MTU mismatch")
	}
	return nil
}

func (e *exitService) accept(stream network.Stream) {
	p := stream.Conn().RemotePeer()
	e.mu.Lock()
	if e.closed || len(e.inbound)+len(e.pending) >= e.options.MaxExitSessions || e.inbound[p] != nil {
		e.mu.Unlock()
		stream.Reset()
		return
	}
	e.pending[stream] = struct{}{}
	e.wg.Add(1)
	e.mu.Unlock()
	defer e.wg.Done()
	defer func() { e.mu.Lock(); delete(e.pending, stream); e.mu.Unlock(); stream.Reset() }()
	ctx, cancel := context.WithTimeout(e.ctx, 5*time.Second)
	approved := e.approved(ctx, p)
	cancel()
	if !approved {
		return
	}
	if err := exitHandshake(stream, e.options.MTU, false); err != nil {
		return
	}
	ctx, cancel = context.WithCancel(e.ctx)
	defer cancel()
	session := &exitSession{stream: stream, cancel: cancel}
	e.mu.Lock()
	delete(e.pending, stream)
	if e.closed || e.inbound[p] != nil {
		e.mu.Unlock()
		return
	}
	e.inbound[p] = session
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		if e.inbound[p] == session {
			delete(e.inbound, p)
		}
		e.mu.Unlock()
	}()
	stack, err := newInternetStack(ctx, e.options)
	if err != nil {
		return
	}
	defer stack.Close()
	done := make(chan error, 2)
	var limiter *rate.Limiter
	if e.options.BytesPerSecond > 0 {
		limiter = rate.NewLimiter(rate.Limit(e.options.BytesPerSecond), max(e.options.BytesPerSecond, e.options.MTU))
	}
	go func() {
		for {
			packet, err := ReadPacket(stream, e.options.MTU)
			if err != nil {
				done <- err
				return
			}
			if limiter != nil {
				if err = limiter.WaitN(ctx, len(packet)); err != nil {
					done <- err
					return
				}
			}
			if e.options.AccountBytes != nil {
				e.options.AccountBytes(0, uint64(len(packet)))
			}
			stack.Inject(packet)
		}
	}()
	go func() {
		for {
			packet, err := stack.ReadPacket(ctx)
			if err != nil {
				done <- err
				return
			}
			if limiter != nil {
				if err = limiter.WaitN(ctx, len(packet)); err != nil {
					done <- err
					return
				}
			}
			if err = WritePacket(stream, packet, e.options.MTU); err != nil {
				done <- err
				return
			}
			if e.options.AccountBytes != nil {
				e.options.AccountBytes(uint64(len(packet)), 0)
			}
		}
	}()
	<-done
	cancel()
	stream.Reset()
	stack.Close()
	<-done
}

func (e *exitService) start(device io.ReadWriteCloser, queue *packetQueue, name, peerID string) error {
	p, err := peer.Decode(peerID)
	if err != nil {
		return err
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return errors.New("VPN is closed")
	}
	if e.client != nil {
		existing := e.client
		e.mu.Unlock()
		if queue != nil && existing.queue != nil && existing.peer == p {
			_ = queue.Close()
			return existing.connect()
		}
		return errors.New("an Internet VPN device is already attached")
	}
	ctx, cancel := context.WithCancel(e.ctx)
	client := &exitClient{service: e, ctx: ctx, cancel: cancel, peer: p, device: device, queue: queue, deviceName: name}
	client.wg.Add(2)
	e.client = client
	e.mu.Unlock()
	if err = client.connect(); err != nil {
		e.mu.Lock()
		if e.client == client {
			e.client = nil
		}
		e.mu.Unlock()
		cancel()
		device.Close()
		client.wg.Done()
		client.wg.Done()
		return err
	}
	go client.readDevice()
	go client.reconnect()
	return nil
}
func (c *exitClient) connect() error {
	c.mu.Lock()
	if c.ctx.Err() != nil {
		c.mu.Unlock()
		return errors.New("VPN is stopped")
	}
	if c.stream != nil {
		c.mu.Unlock()
		return nil
	}
	if c.connecting {
		c.mu.Unlock()
		return nil
	}
	c.connecting = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.connecting = false; c.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(c.ctx, 15*time.Second)
	defer cancel()
	stream, err := c.service.host.NewStream(network.WithAllowLimitedConn(ctx, "Internet VPN exit"), c.peer, ExitProtocol)
	if err == nil {
		err = exitHandshake(stream, c.service.options.MTU, true)
	}
	if err != nil {
		if stream != nil {
			stream.Reset()
		}
		c.mu.Lock()
		c.err = err.Error()
		c.mu.Unlock()
		return fmt.Errorf("Internet VPN exit: %w", err)
	}
	c.mu.Lock()
	if c.ctx.Err() != nil {
		c.mu.Unlock()
		stream.Reset()
		return errors.New("VPN is stopped")
	}
	c.stream = stream
	c.err = ""
	c.wg.Add(1)
	c.mu.Unlock()
	go c.readStream(stream)
	return nil
}
func (c *exitClient) failed(stream network.Stream, err error) {
	stream.Reset()
	c.mu.Lock()
	if c.stream == stream {
		c.stream = nil
		c.err = err.Error()
		if c.queue != nil {
			c.queue.failure(errors.New("Internet VPN disconnected; reconnecting"))
		}
	}
	c.mu.Unlock()
}
func (c *exitClient) readStream(stream network.Stream) {
	defer c.wg.Done()
	for {
		packet, err := ReadPacket(stream, c.service.options.MTU)
		if err != nil {
			c.failed(stream, err)
			return
		}
		if c.service.options.AccountBytes != nil {
			c.service.options.AccountBytes(0, uint64(len(packet)))
		}
		if _, err = c.device.Write(packet); err != nil {
			c.failed(stream, err)
			return
		}
	}
}
func (c *exitClient) readDevice() {
	defer c.wg.Done()
	buffer := make([]byte, c.service.options.MTU+1)
	for {
		n, err := c.device.Read(buffer)
		if err != nil {
			return
		}
		if ValidatePacket(buffer[:n], c.service.options.MTU) != nil {
			continue
		}
		c.mu.Lock()
		stream := c.stream
		c.mu.Unlock()
		if stream == nil {
			continue
		}
		if err = WritePacket(stream, buffer[:n], c.service.options.MTU); err != nil {
			c.failed(stream, err)
			continue
		}
		if c.service.options.AccountBytes != nil {
			c.service.options.AccountBytes(uint64(n), 0)
		}
	}
}
func (c *exitClient) reconnect() {
	defer c.wg.Done()
	timer := time.NewTicker(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-timer.C:
			_ = c.connect()
		}
	}
}
func (c *exitClient) close() {
	c.cancel()
	c.mu.Lock()
	stream := c.stream
	c.stream = nil
	c.mu.Unlock()
	if stream != nil {
		stream.Reset()
	}
	c.device.Close()
	c.wg.Wait()
}
func (e *exitService) close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	e.cancel()
	client := e.client
	e.client = nil
	for stream := range e.pending {
		stream.Reset()
	}
	for _, s := range e.inbound {
		s.cancel()
		s.stream.Reset()
	}
	e.mu.Unlock()
	e.host.RemoveStreamHandler(ExitProtocol)
	if client != nil {
		client.close()
	}
	e.wg.Wait()
	return nil
}

func (s *Service) StartPacketVPN(peerID string) error {
	q := newPacketQueue()
	err := s.exit.start(q, q, "packet-flow", peerID)
	if err != nil {
		q.Close()
	}
	return err
}
func (s *Service) ReadPacket() ([]byte, error) {
	s.exit.mu.Lock()
	c := s.exit.client
	s.exit.mu.Unlock()
	if c == nil || c.queue == nil {
		return nil, errors.New("packet VPN is not started")
	}
	select {
	case <-c.queue.done:
		return nil, io.EOF
	case r := <-c.queue.output:
		return r.packet, r.err
	}
}
func (s *Service) WritePacket(packet []byte) error {
	if err := ValidatePacket(packet, s.mtu); err != nil {
		return err
	}
	s.exit.mu.Lock()
	c := s.exit.client
	s.exit.mu.Unlock()
	if c == nil || c.queue == nil {
		return errors.New("packet VPN is not started")
	}
	select {
	case <-c.queue.done:
		return io.EOF
	case c.queue.input <- append([]byte(nil), packet...):
		return nil
	default:
		return errors.New("packet VPN queue is full")
	}
}
func (s *Service) StopPacketVPN() error {
	s.exit.mu.Lock()
	c := s.exit.client
	s.exit.client = nil
	s.exit.mu.Unlock()
	if c != nil {
		c.close()
	}
	return nil
}
func (s *Service) AttachExitFD(fd int, peerID string) error {
	copyFD, err := duplicateFD(fd)
	if err != nil {
		return err
	}
	device := os.NewFile(uintptr(copyFD), "meshway-internet-tun")
	if device == nil {
		closeFD(copyFD)
		return errors.New("invalid VPN descriptor")
	}
	if err = s.exit.start(device, nil, "external-fd", peerID); err != nil {
		device.Close()
	}
	return err
}
func (s *Service) StartDeviceExit(name, peerID string) error {
	device, err := openPacketDevice(name, s.mtu)
	if err != nil {
		return err
	}
	if err = s.exit.start(device, nil, name, peerID); err != nil {
		device.Close()
	}
	return err
}
func (s *Service) Status() Status {
	s.exit.mu.Lock()
	c := s.exit.client
	s.exit.mu.Unlock()
	if c == nil {
		return Status{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return Status{Active: c.stream != nil, PeerID: c.peer.String(), Mode: "exit", Device: c.deviceName, Error: c.err}
}

var reservedDestinations = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001:db8::/32")}

func publicDestination(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	for _, p := range reservedDestinations {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}
func packetIP(packet []byte, source bool) netip.Addr {
	offset := 16
	if source {
		offset = 12
	}
	if packet[0]>>4 == 4 {
		return netip.AddrFrom4([4]byte(packet[offset : offset+4]))
	}
	offset = 24
	if source {
		offset = 8
	}
	return netip.AddrFrom16([16]byte(packet[offset : offset+16]))
}
func packetProtocol(packet []byte) byte {
	if packet[0]>>4 == 4 {
		return packet[9]
	}
	return packet[6]
}
func packetPort(packet []byte) uint16 {
	offset := 40
	if packet[0]>>4 == 4 {
		offset = int(packet[0]&15) * 4
	}
	if len(packet) < offset+4 {
		return 0
	}
	return binary.BigEndian.Uint16(packet[offset+2 : offset+4])
}
func validClientSource(ip netip.Addr) bool {
	return ip == netip.MustParseAddr(ClientIPv4) || ip == netip.MustParseAddr(ClientIPv6)
}
