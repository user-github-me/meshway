package vpn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const Protocol protocol.ID = "/meshway/ip/1.0.0"

type Options struct {
	AllowedPeers []string
	MTU          int
	Authorize    func(context.Context, peer.ID) bool
	// Exit explicitly permits approved peers to use userspace Internet egress.
	Exit                bool
	AllowPrivateTargets bool
	DNSResolver         string
	MaxExitSessions     int
	MaxFlowsPerSession  int
	BytesPerSecond      int
	AccountBytes        func(sent, received uint64)
}

// Service connects one local packet device to one authorized peer at a time.
type Service struct {
	host      host.Host
	mtu       int
	allowed   map[peer.ID]bool
	authorize func(context.Context, peer.ID) bool
	mu        sync.Mutex
	device    io.ReadWriteCloser
	stream    network.Stream
	pending   map[network.Stream]struct{}
	closed    bool
	cancel    context.CancelFunc
	ctx       context.Context
	wg        sync.WaitGroup
	exit      *exitService
}

func New(h host.Host, options Options) (*Service, error) {
	if h == nil {
		return nil, errors.New("host is required")
	}
	if options.MTU == 0 {
		options.MTU = DefaultMTU
	}
	if options.MTU < 576 || options.MTU > 9000 {
		return nil, errors.New("MTU must be between 576 and 9000")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{host: h, mtu: options.MTU, allowed: make(map[peer.ID]bool), authorize: options.Authorize, pending: make(map[network.Stream]struct{}), ctx: ctx, cancel: cancel}
	for _, id := range options.AllowedPeers {
		p, err := peer.Decode(id)
		if err != nil {
			cancel()
			return nil, err
		}
		s.allowed[p] = true
	}
	h.SetStreamHandler(Protocol, s.accept)
	var err error
	s.exit, err = newExitService(h, options)
	if err != nil {
		h.RemoveStreamHandler(Protocol)
		cancel()
		return nil, err
	}
	return s, nil
}

// AttachFD duplicates fd. The caller retains ownership of the original FD.
// An empty peerID waits for an inbound stream from the configured peer allowlist.
// A nonempty peerID creates an outbound stream after attaching the device.
func (s *Service) AttachFD(fd int, peerID string) error {
	var remote peer.ID
	var err error
	if peerID != "" {
		remote, err = peer.Decode(peerID)
		if err != nil {
			return err
		}
	}
	copyFD, err := duplicateFD(fd)
	if err != nil {
		return err
	}
	device := os.NewFile(uintptr(copyFD), "meshway-tun")
	if device == nil {
		closeFD(copyFD)
		return errors.New("invalid device file descriptor")
	}
	if err = s.attach(device); err != nil {
		device.Close()
		return err
	}
	if remote == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	stream, err := s.host.NewStream(network.WithAllowLimitedConn(ctx, "configured VPN peer"), remote, Protocol)
	if err != nil {
		s.detach(device)
		return fmt.Errorf("VPN peer: %w", err)
	}
	if !s.trackHandshake(stream) {
		stream.Reset()
		s.detach(device)
		return errors.New("VPN session unavailable")
	}
	defer s.finishHandshake(stream)
	// Both ends exchange a fixed version/MTU handshake before packet forwarding.
	if err = s.handshake(stream, true); err != nil {
		stream.Reset()
		s.detach(device)
		return err
	}
	if err = s.activate(stream, device); err != nil {
		stream.Reset()
		s.detach(device)
		return err
	}
	return nil
}

func (s *Service) attach(device io.ReadWriteCloser) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("VPN is closed")
	}
	if s.device != nil {
		return errors.New("VPN already has an attached device")
	}
	s.device = device
	return nil
}

func (s *Service) detach(device io.ReadWriteCloser) {
	s.mu.Lock()
	if s.device == device {
		s.device = nil
	}
	s.mu.Unlock()
	device.Close()
}

func (s *Service) accept(stream network.Stream) {
	approved := s.allowed[stream.Conn().RemotePeer()]
	if s.authorize != nil {
		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
		approved = s.authorize(ctx, stream.Conn().RemotePeer())
		cancel()
	}
	if !approved {
		stream.Reset()
		return
	}
	s.mu.Lock()
	device := s.device
	available := !s.closed && device != nil && s.stream == nil
	s.mu.Unlock()
	if !available {
		stream.Reset()
		return
	}
	if !s.trackHandshake(stream) {
		stream.Reset()
		return
	}
	defer s.finishHandshake(stream)
	if err := s.handshake(stream, false); err != nil {
		stream.Reset()
		return
	}
	if err := s.activate(stream, device); err != nil {
		stream.Reset()
	}
}

func (s *Service) trackHandshake(stream network.Stream) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.stream != nil || len(s.pending) > 0 {
		return false
	}
	s.pending[stream] = struct{}{}
	return true
}

func (s *Service) finishHandshake(stream network.Stream) {
	s.mu.Lock()
	delete(s.pending, stream)
	s.mu.Unlock()
}

func (s *Service) handshake(stream network.Stream, outbound bool) error {
	stream.SetDeadline(time.Now().Add(10 * time.Second))
	defer stream.SetDeadline(time.Time{})
	magic := []byte{'M', 'W', 1, byte(s.mtu >> 8), byte(s.mtu)}
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
		return errors.New("VPN version or MTU mismatch")
	}
	return nil
}

func (s *Service) activate(stream network.Stream, device io.ReadWriteCloser) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.device != device || s.stream != nil {
		return errors.New("VPN session unavailable")
	}
	s.stream = stream
	s.wg.Add(1)
	go s.pump(stream, device)
	return nil
}

func (s *Service) pump(stream network.Stream, device io.ReadWriteCloser) {
	defer s.wg.Done()
	done := make(chan error, 2)
	go func() {
		buffer := make([]byte, s.mtu+1)
		for {
			n, err := device.Read(buffer)
			if err != nil {
				done <- err
				return
			}
			if err = WritePacket(stream, buffer[:n], s.mtu); err != nil {
				done <- err
				return
			}
		}
	}()
	go func() {
		for {
			packet, err := ReadPacket(stream, s.mtu)
			if err != nil {
				done <- err
				return
			}
			n, err := device.Write(packet)
			if err != nil {
				done <- err
				return
			}
			if n != len(packet) {
				done <- io.ErrShortWrite
				return
			}
		}
	}()
	<-done
	// A broken stream closes its packet device to unblock the opposite direction.
	stream.Reset()
	device.Close()
	<-done
	s.mu.Lock()
	if s.stream == stream {
		s.stream = nil
	}
	if s.device == device {
		s.device = nil
	}
	s.mu.Unlock()
}

func (s *Service) Close() error {
	if s.exit != nil {
		_ = s.exit.close()
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.cancel()
	stream, device := s.stream, s.device
	for pending := range s.pending {
		pending.Reset()
	}
	s.mu.Unlock()
	s.host.RemoveStreamHandler(Protocol)
	if stream != nil {
		stream.Reset()
	}
	if device != nil {
		device.Close()
	}
	s.wg.Wait()
	return nil
}
