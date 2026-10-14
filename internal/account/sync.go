package account

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const SyncProtocol protocol.ID = "/meshway/account-sync/1.0.0"

func writeFrame(w io.Writer, data []byte) error {
	if len(data) == 0 || len(data) > maxStateSize+64 {
		return errors.New("sync frame size limit")
	}
	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame, uint32(len(data)))
	copy(frame[4:], data)
	for len(frame) > 0 {
		n, err := w.Write(frame)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}
func readFrame(r io.Reader, limit int) ([]byte, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint32(size[:]))
	if n == 0 || n > limit {
		return nil, errors.New("sync frame size limit")
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}
func (s *Store) aead() (cipher.AEAD, error) {
	// A domain-separated SHA256 KDF is sufficient for a uniformly random 256-bit seed.
	// Recovery secrets are never sent; AES-GCM adds account-level encryption to libp2p.
	key := sha256.Sum256(append([]byte("meshway-account-sync-aes256-v1\x00"), s.root.Seed()...))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func (s *Store) writeState(w io.Writer) error {
	s.mu.Lock()
	b, err := json.Marshal(s.data)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	aead, err := s.aead()
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	sealed := aead.Seal(nonce, nonce, b, []byte(s.certificate.AccountID))
	return writeFrame(w, sealed)
}
func (s *Store) readState(r io.Reader) (state, error) {
	var data state
	sealed, err := readFrame(r, maxStateSize+64)
	if err != nil {
		return data, err
	}
	aead, err := s.aead()
	if err != nil {
		return data, err
	}
	if len(sealed) < aead.NonceSize()+aead.Overhead() {
		return data, errors.New("truncated encrypted account state")
	}
	b, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(s.certificate.AccountID))
	if err != nil {
		return data, errors.New("account state authentication failed")
	}
	if err = json.Unmarshal(b, &data); err != nil {
		return data, err
	}
	if err = s.validateState(data); err != nil {
		return data, err
	}
	return data, nil
}
func (s *Store) verifyRemote(c Certificate, remote peer.ID) error {
	if err := verifyCertificate(c, s.certificate.AccountID); err != nil {
		return err
	}
	if c.PeerID != remote.String() {
		return errors.New("certificate does not match authenticated transport peer")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("account is closed")
	}
	for _, d := range s.data.Devices {
		if d.PeerID == c.PeerID {
			if d.Revoked {
				return errors.New("device is revoked")
			}
			if d.Certificate.Signature != c.Signature {
				return errors.New("conflicting device certificate")
			}
			return nil
		}
	}
	if len(s.data.Devices) >= maxDevices {
		return errors.New("device registry full")
	}
	return nil
}
func (s *Store) exchangeCertificate(stream network.Stream, outbound bool) (Certificate, error) {
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
func (s *Store) beginSync() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.syncEnabled || s.pending >= 4 {
		return false
	}
	s.pending++
	return true
}
func (s *Store) endSync() { s.mu.Lock(); s.pending--; s.mu.Unlock() }
func (s *Store) trackStream(stream network.Stream) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.streams[stream] = struct{}{}
	return true
}
func (s *Store) untrackStream(stream network.Stream) {
	s.mu.Lock()
	delete(s.streams, stream)
	s.mu.Unlock()
}
func (s *Store) handleSync(stream network.Stream) {
	if !s.beginSync() {
		stream.Reset()
		return
	}
	defer s.endSync()
	if !s.trackStream(stream) {
		stream.Reset()
		return
	}
	defer s.untrackStream(stream)
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(20 * time.Second))
	remote, err := s.exchangeCertificate(stream, false)
	if err != nil {
		stream.Reset()
		return
	}
	data, err := s.readState(stream)
	if err != nil {
		stream.Reset()
		return
	}
	if err = s.merge(data, remote); err != nil {
		stream.Reset()
		return
	}
	if err = s.writeState(stream); err != nil {
		stream.Reset()
	}
}
func (s *Store) Sync(ctx context.Context, peerID string) error {
	if !s.beginSync() {
		return errors.New("enable account_sync on both devices before synchronizing")
	}
	defer s.endSync()
	remote, err := peer.Decode(peerID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	stream, err := s.host.NewStream(network.WithAllowLimitedConn(ctx, "account synchronization"), remote, SyncProtocol)
	if err != nil {
		return err
	}
	if !s.trackStream(stream) {
		stream.Reset()
		return errors.New("account closed")
	}
	defer s.untrackStream(stream)
	defer stream.Close()
	stopCancel := context.AfterFunc(ctx, func() { stream.Reset() })
	defer stopCancel()
	stream.SetDeadline(time.Now().Add(20 * time.Second))
	c, err := s.exchangeCertificate(stream, true)
	if err != nil {
		stream.Reset()
		return err
	}
	if err = s.writeState(stream); err != nil {
		stream.Reset()
		return err
	}
	data, err := s.readState(stream)
	if err != nil {
		stream.Reset()
		return err
	}
	return s.merge(data, c)
}
func (s *Store) merge(incoming state, remote Certificate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("account is closed")
	}
	// Revalidate after acquiring the lock because local revocations may have changed.
	if err := s.validateState(incoming); err != nil {
		return err
	}
	devices := map[string]Device{}
	for _, d := range s.data.Devices {
		devices[d.PeerID] = d
	}
	for _, d := range incoming.Devices {
		if old, ok := devices[d.PeerID]; ok {
			if old.Certificate.Signature != d.Certificate.Signature {
				return errors.New("conflicting device certificate")
			}
			// Revocation is monotonic; an old snapshot never unrevoke a device.
			if old.Revoked {
				d.Revoked = true
				d.RevokedAt = old.RevokedAt
				d.RevocationSignature = old.RevocationSignature
			}
			d.LastSeen = old.LastSeen
		} else {
			d.LastSeen = time.Time{}
		}
		devices[d.PeerID] = d
	}
	if current, ok := devices[remote.PeerID]; ok {
		if current.Revoked {
			return errors.New("syncing device revoked")
		}
		current.LastSeen = time.Now().UTC()
		devices[remote.PeerID] = current
	} else {
		devices[remote.PeerID] = Device{Certificate: remote, PeerID: remote.PeerID, Label: remote.Label, CreatedAt: remote.CreatedAt, LastSeen: time.Now().UTC()}
	}
	if len(devices) > maxDevices {
		return errors.New("device registry full")
	}
	events := map[string]Event{}
	for _, e := range s.data.Events {
		events[e.ID] = e
	}
	for _, e := range incoming.Events {
		if old, exists := events[e.ID]; exists {
			if old.Signature != e.Signature {
				return errors.New("conflicting event ID")
			}
			continue
		}
		d := devices[e.PeerID]
		if d.Revoked {
			continue
		}
		events[e.ID] = e
	}
	merged := state{Devices: make([]Device, 0, len(devices)), Events: make([]Event, 0, len(events))}
	merged.Deletion = s.data.Deletion
	if incoming.Deletion != nil && (merged.Deletion == nil || incoming.Deletion.Before.After(merged.Deletion.Before)) {
		merged.Deletion = incoming.Deletion
	}
	for _, d := range devices {
		merged.Devices = append(merged.Devices, d)
	}
	for _, e := range events {
		if merged.Deletion != nil && !e.Time.After(merged.Deletion.Before) {
			continue
		}
		merged.Events = append(merged.Events, e)
	}
	sort.Slice(merged.Devices, func(i, j int) bool { return merged.Devices[i].PeerID < merged.Devices[j].PeerID })
	sort.Slice(merged.Events, func(i, j int) bool {
		if merged.Events[i].Time.Equal(merged.Events[j].Time) {
			return merged.Events[i].ID < merged.Events[j].ID
		}
		return merged.Events[i].Time.Before(merged.Events[j].Time)
	})
	if len(merged.Events) > maxEvents {
		merged.Events = merged.Events[len(merged.Events)-maxEvents:]
	}
	previous := s.data
	s.data = merged
	if err := s.save(); err != nil {
		s.data = previous
		return err
	}
	if s.onRevoked != nil {
		known := map[string]bool{}
		for _, d := range previous.Devices {
			if d.Revoked {
				known[d.PeerID] = true
			}
		}
		for _, d := range merged.Devices {
			if d.Revoked && !known[d.PeerID] {
				go s.onRevoked(d.PeerID)
			}
		}
	}
	return nil
}
