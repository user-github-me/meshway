package account

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// Revocations propagate independently of opt-in history synchronization.
const RevocationProtocol protocol.ID = "/meshway/account-revoke/1.0.0"

func (s *Store) revocations() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Device{}
	for _, d := range s.data.Devices {
		if d.Revoked {
			out = append(out, d)
		}
	}
	return out
}

func (s *Store) mergeRevocations(incoming []Device) error {
	if len(incoming) > maxDevices {
		return errors.New("too many revocations")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("account closed")
	}
	accountID := s.certificate.AccountID
	for _, d := range incoming {
		if err := verifyCertificate(d.Certificate, accountID); err != nil {
			s.mu.Unlock()
			return err
		}
		signature, err := decode(d.RevocationSignature)
		if err != nil || d.PeerID != d.Certificate.PeerID || !d.Revoked || d.RevokedAt == nil || d.RevokedAt.After(time.Now().Add(5*time.Minute)) || !ed25519.Verify(s.root.Public().(ed25519.PublicKey), revocationMessage(d), signature) {
			s.mu.Unlock()
			return errors.New("invalid signed revocation")
		}
	}
	previous := append([]Device(nil), s.data.Devices...)
	changed := []string{}
	for _, d := range incoming {
		found := false
		for i, old := range s.data.Devices {
			if old.PeerID != d.PeerID {
				continue
			}
			found = true
			if old.Certificate.Signature != d.Certificate.Signature {
				s.data.Devices = previous
				s.mu.Unlock()
				return errors.New("conflicting revoked certificate")
			}
			if !old.Revoked {
				d.LastSeen = old.LastSeen
				s.data.Devices[i] = d
				changed = append(changed, d.PeerID)
			}
			break
		}
		if !found {
			if len(s.data.Devices) == maxDevices {
				s.data.Devices = previous
				s.mu.Unlock()
				return errors.New("device registry full")
			}
			d.Label = d.Certificate.Label
			d.CreatedAt = d.Certificate.CreatedAt
			d.LastSeen = time.Time{}
			s.data.Devices = append(s.data.Devices, d)
			changed = append(changed, d.PeerID)
		}
	}
	if len(changed) != 0 {
		if err := s.save(); err != nil {
			s.data.Devices = previous
			s.mu.Unlock()
			return err
		}
	}
	s.mu.Unlock()
	if s.onRevoked != nil {
		for _, id := range changed {
			go s.onRevoked(id)
		}
	}
	return nil
}

func (s *Store) handleRevocations(stream network.Stream) {
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
	stream.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err := s.exchangeAuthCertificate(stream, false); err != nil {
		stream.Reset()
		return
	}
	b, err := readFrame(stream, 128<<10)
	var incoming []Device
	if err != nil || json.Unmarshal(b, &incoming) != nil || s.mergeRevocations(incoming) != nil {
		stream.Reset()
		return
	}
	b, _ = json.Marshal(s.revocations())
	if writeFrame(stream, b) != nil {
		stream.Reset()
	}
}

func (s *Store) ExchangeRevocations(ctx context.Context, id peer.ID) error {
	if !s.beginAuth() {
		return errors.New("account busy or closed")
	}
	defer s.endAuth()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	stream, err := s.host.NewStream(network.WithAllowLimitedConn(ctx, "account revocations"), id, RevocationProtocol)
	if err != nil {
		return err
	}
	if !s.trackStream(stream) {
		stream.Reset()
		return errors.New("account closed")
	}
	defer s.untrackStream(stream)
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(8 * time.Second))
	stop := context.AfterFunc(ctx, func() { stream.Reset() })
	defer stop()
	if _, err = s.exchangeAuthCertificate(stream, true); err != nil {
		return err
	}
	b, _ := json.Marshal(s.revocations())
	if err = writeFrame(stream, b); err != nil {
		return err
	}
	b, err = readFrame(stream, 128<<10)
	if err != nil {
		return err
	}
	var incoming []Device
	if err = json.Unmarshal(b, &incoming); err != nil {
		return err
	}
	return s.mergeRevocations(incoming)
}

func (s *Store) broadcastRevocations() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, d := range s.Devices() {
		if d.Revoked || d.PeerID == s.host.ID().String() {
			continue
		}
		id, err := peer.Decode(d.PeerID)
		if err == nil && s.host.Network().Connectedness(id) != network.NotConnected {
			_ = s.ExchangeRevocations(ctx, id)
		}
	}
}
