package account

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const AuthProtocol protocol.ID = "/meshway/account-auth/1.0.0"

type authFlight struct {
	done chan struct{}
	err  error
}
type authProof struct {
	connectionID string
	expires      time.Time
}

func (s *Store) cachedAuthLocked(id peer.ID) bool {
	proof, ok := s.authCache[id]
	if !ok || time.Now().After(proof.expires) {
		delete(s.authCache, id)
		return false
	}
	validDevice := false
	for _, d := range s.data.Devices {
		if d.PeerID == id.String() {
			validDevice = !d.Revoked
			break
		}
	}
	if !validDevice {
		delete(s.authCache, id)
		return false
	}
	for _, conn := range s.host.Network().ConnsToPeer(id) {
		if !conn.IsClosed() && conn.ID() == proof.connectionID {
			return true
		}
	}
	delete(s.authCache, id)
	return false
}

func (s *Store) beginAuth() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.authPending >= 8 {
		return false
	}
	s.authPending++
	return true
}
func (s *Store) endAuth() { s.mu.Lock(); s.authPending--; s.mu.Unlock() }
func (s *Store) handleAuth(stream network.Stream) {
	if !s.beginAuth() {
		stream.Reset()
		return
	}
	defer s.endAuth()
	if !s.trackStream(stream) {
		stream.Reset()
		return
	}
	defer s.untrackStream(stream)
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(5 * time.Second))
	remote, err := s.exchangeAuthCertificate(stream, false)
	if err != nil {
		stream.Reset()
		return
	}
	if err = s.rememberDevice(remote); err != nil {
		stream.Reset()
	}
}
func (s *Store) exchangeAuthCertificate(stream network.Stream, outbound bool) (Certificate, error) {
	var remote Certificate
	local, _ := json.Marshal(s.certificate)
	if outbound {
		if err := writeFrame(stream, local); err != nil {
			return remote, err
		}
	}
	b, err := readFrame(stream, 8192)
	if err != nil {
		return remote, err
	}
	if err = json.Unmarshal(b, &remote); err != nil {
		return remote, err
	}
	if err = s.verifyRemote(remote, stream.Conn().RemotePeer()); err != nil {
		return remote, err
	}
	if !outbound {
		if err = writeFrame(stream, local); err != nil {
			return remote, err
		}
	}
	return remote, nil
}
func (s *Store) rememberDevice(c Certificate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("account is closed")
	}
	for i, d := range s.data.Devices {
		if d.PeerID == c.PeerID {
			if d.Revoked {
				return errors.New("device revoked")
			}
			if d.Certificate.Signature != c.Signature {
				return errors.New("conflicting certificate")
			}
			s.data.Devices[i].LastSeen = time.Now().UTC()
			return s.save()
		}
	}
	if len(s.data.Devices) >= maxDevices {
		return errors.New("device registry full")
	}
	s.data.Devices = append(s.data.Devices, Device{Certificate: c, PeerID: c.PeerID, Label: c.Label, CreatedAt: c.CreatedAt, LastSeen: time.Now().UTC()})
	return s.save()
}

// AuthorizePeer proves account membership using a root-signed device certificate
// bound to the peer's encrypted transport identity, without enabling history sync.
// Concurrent requests share one exchange. A successful proof is reused for at
// most 30 seconds on that same live encrypted connection; revocations are checked
// on every call and disconnected/replaced connections require a new exchange.
func (s *Store) AuthorizePeer(ctx context.Context, id peer.ID) error {
	if id == s.host.ID() {
		return errors.New("cannot authorize self as remote")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("account is closed")
	}
	for _, d := range s.data.Devices {
		if d.PeerID == id.String() && d.Revoked {
			s.mu.Unlock()
			return errors.New("device is revoked")
		}
	}
	if s.cachedAuthLocked(id) {
		s.mu.Unlock()
		return nil
	}
	flight := s.authFlights[id]
	if flight == nil {
		if s.authPending >= 8 {
			s.mu.Unlock()
			return errors.New("account authentication busy or closed")
		}
		flight = &authFlight{done: make(chan struct{})}
		s.authFlights[id] = flight
		s.authPending++
		go func() {
			connectionID, err := s.authorizeExchange(context.Background(), id)
			s.mu.Lock()
			if err == nil {
				if s.closed {
					err = errors.New("account is closed")
				} else {
					for _, d := range s.data.Devices {
						if d.PeerID == id.String() && d.Revoked {
							err = errors.New("device is revoked")
							break
						}
					}
				}
			}
			if err == nil {
				s.authCache[id] = authProof{connectionID: connectionID, expires: time.Now().Add(30 * time.Second)}
			}
			flight.err = err
			delete(s.authFlights, id)
			s.authPending--
			close(flight.done)
			s.mu.Unlock()
		}()
	}
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-flight.done:
		if flight.err != nil {
			return flight.err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			return errors.New("account is closed")
		}
		if !s.cachedAuthLocked(id) {
			return errors.New("account proof expired or peer disconnected")
		}
		return nil
	}
}

func (s *Store) authorizeExchange(ctx context.Context, id peer.ID) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stream, err := s.host.NewStream(network.WithAllowLimitedConn(ctx, "personal server authentication"), id, AuthProtocol)
	if err != nil {
		return "", err
	}
	if !s.trackStream(stream) {
		stream.Reset()
		return "", errors.New("account is closed")
	}
	defer s.untrackStream(stream)
	defer stream.Close()
	stopCancel := context.AfterFunc(ctx, func() { stream.Reset() })
	defer stopCancel()
	stream.SetDeadline(time.Now().Add(5 * time.Second))
	c, err := s.exchangeAuthCertificate(stream, true)
	if err != nil {
		stream.Reset()
		return "", err
	}
	if err = s.rememberDevice(c); err != nil {
		return "", err
	}
	// Best-effort versioned exchange preserves compatibility with older peers.
	// Offline devices receive signed revocations on their next authenticated link.
	go func() { _ = s.ExchangeRevocations(context.Background(), id) }()
	return stream.Conn().ID(), nil
}
