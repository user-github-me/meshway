// Package account implements local-first, recovery-key accounts and signed
// device identities. Account recovery keys are never transmitted over sync.
package account

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	statevault "github.com/user-github-me/meshway/internal/state"
)

const maxEvents = 2000
const maxDevices = 128
const maxStateSize = 6 << 20

type Certificate struct {
	AccountID    string    `json:"account_id"`
	RootPublic   string    `json:"root_public"`
	PeerID       string    `json:"peer_id"`
	DevicePublic string    `json:"device_public"`
	Label        string    `json:"label"`
	CreatedAt    time.Time `json:"created_at"`
	Signature    string    `json:"signature"`
}
type Device struct {
	Certificate         Certificate `json:"certificate"`
	PeerID              string      `json:"peer_id"`
	Label               string      `json:"label"`
	CreatedAt           time.Time   `json:"created_at"`
	LastSeen            time.Time   `json:"last_seen"`
	Revoked             bool        `json:"revoked"`
	RevokedAt           *time.Time  `json:"revoked_at,omitempty"`
	RevocationSignature string      `json:"revocation_signature,omitempty"`
}
type Event struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	PeerID    string    `json:"peer_id"`
	Time      time.Time `json:"time"`
	Detail    string    `json:"detail"`
	Signature string    `json:"signature"`
}
type Status struct {
	AccountID   string    `json:"account_id"`
	DeviceID    string    `json:"device_id"`
	CreatedAt   time.Time `json:"created_at"`
	SyncEnabled bool      `json:"sync_enabled"`
}
type state struct {
	Deletion *HistoryDeletion `json:"history_deletion,omitempty"`
	Devices  []Device         `json:"devices"`
	Events   []Event          `json:"events"`
}
type Options struct {
	StorageKey  []byte
	OnRevoked   func(string)
	SyncEnabled bool
	Label       string
}
type Store struct {
	storageKey  []byte
	onRevoked   func(string)
	mu          sync.Mutex
	dir         string
	root        ed25519.PrivateKey
	host        host.Host
	certificate Certificate
	data        state
	syncEnabled bool
	closed      bool
	pending     int
	authPending int
	streams     map[network.Stream]struct{}
}

func rootID(public []byte) string {
	sum := sha256.Sum256(public)
	return "mw_" + hex.EncodeToString(sum[:])
}
func encode(data []byte) string           { return base64.RawURLEncoding.EncodeToString(data) }
func decode(value string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(value) }
func certificateMessage(c Certificate) []byte {
	c.Signature = ""
	b, _ := json.Marshal(c)
	return append([]byte("meshway-device-certificate-v1\x00"), b...)
}
func eventMessage(e Event) []byte {
	e.Signature = ""
	b, _ := json.Marshal(e)
	return append([]byte("meshway-history-event-v1\x00"), b...)
}
func revocationMessage(d Device) []byte {
	b, _ := json.Marshal(struct {
		PeerID string
		At     *time.Time
	}{d.PeerID, d.RevokedAt})
	return append([]byte("meshway-device-revocation-v1\x00"), b...)
}

func privateRead(path string, limit int) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return nil, errors.New("private file is not a regular bounded file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("private file permissions must be 0600 (chmod 600)")
	}
	return os.ReadFile(path)
}
func atomicSave(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".meshway-state-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
func rootKey(dir string) (ed25519.PrivateKey, error) {
	key, err := statevault.KeyFromEnvironment()
	if err != nil {
		return nil, err
	}
	return rootKeyWithStorage(dir, key)
}

func rootKeyWithStorage(dir string, storageKey []byte) (ed25519.PrivateKey, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := finishTransition(dir, storageKey); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "account.seed")
	seed, err := statevault.ReadPrivate(path, 32, storageKey)
	if err == nil {
		if len(seed) != 32 {
			return nil, errors.New("invalid account seed")
		}
		return ed25519.NewKeyFromSeed(seed), nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	seed = make([]byte, 32)
	if _, err = rand.Read(seed); err != nil {
		return nil, err
	}
	sealed, err := statevault.Seal(path, seed, storageKey)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(err) {
		return rootKeyWithStorage(dir, storageKey)
	}
	if err != nil {
		return nil, err
	}
	if _, err = f.Write(sealed); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func New(dir string, h host.Host, options Options) (*Store, error) {
	if h == nil {
		return nil, errors.New("account requires a device host")
	}
	root, err := rootKeyWithStorage(dir, options.StorageKey)
	if err != nil {
		return nil, err
	}
	s := &Store{onRevoked: options.OnRevoked, storageKey: append([]byte(nil), options.StorageKey...), dir: dir, root: root, host: h, syncEnabled: options.SyncEnabled, streams: make(map[network.Stream]struct{}), data: state{Devices: []Device{}, Events: []Event{}}}
	path := filepath.Join(dir, "account.json")
	b, err := statevault.ReadPrivate(path, maxStateSize, s.storageKey)
	if err == nil {
		if err = json.Unmarshal(b, &s.data); err != nil {
			return nil, err
		}
		if err = s.validateState(s.data); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	for _, d := range s.data.Devices {
		if d.PeerID == h.ID().String() {
			if d.Revoked {
				return nil, errors.New("this device is revoked")
			}
			s.certificate = d.Certificate
			break
		}
	}
	if s.certificate.PeerID == "" {
		public, err := crypto.MarshalPublicKey(h.Peerstore().PubKey(h.ID()))
		if err != nil {
			return nil, err
		}
		label := options.Label
		if label == "" {
			label = "This device"
		}
		if len(label) > 80 {
			return nil, errors.New("device label is too long")
		}
		s.certificate = Certificate{AccountID: rootID(root.Public().(ed25519.PublicKey)), RootPublic: encode(root.Public().(ed25519.PublicKey)), PeerID: h.ID().String(), DevicePublic: encode(public), Label: label, CreatedAt: time.Now().UTC()}
		s.certificate.Signature = encode(ed25519.Sign(root, certificateMessage(s.certificate)))
		s.data.Devices = append(s.data.Devices, Device{Certificate: s.certificate, PeerID: s.certificate.PeerID, Label: label, CreatedAt: s.certificate.CreatedAt, LastSeen: time.Now().UTC()})
	}
	if len(s.data.Devices) > maxDevices {
		return nil, errors.New("device registry limit reached")
	}
	if err = s.save(); err != nil {
		return nil, err
	}
	h.SetStreamHandler(SyncProtocol, s.handleSync)
	h.SetStreamHandler(AuthProtocol, s.handleAuth)
	h.SetStreamHandler(RevocationProtocol, s.handleRevocations)
	return s, nil
}
func (s *Store) save() error {
	b, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	if len(b) > maxStateSize {
		return errors.New("account state size limit reached")
	}
	return statevault.SavePrivate(filepath.Join(s.dir, "account.json"), b, s.storageKey)
}
func (s *Store) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{AccountID: s.certificate.AccountID, DeviceID: s.certificate.PeerID, CreatedAt: s.certificate.CreatedAt, SyncEnabled: s.syncEnabled}
}
func (s *Store) Devices() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Device{}, s.data.Devices...)
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}
func (s *Store) History() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event{}, s.data.Events...)
}

// Record records minimal local connection metadata, never destination URLs or keys.
func (s *Store) Record(kind, detail string) error {
	if len(kind) == 0 || len(kind) > 64 || len(detail) > 256 {
		return errors.New("event fields exceed limits")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("account is closed")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	e := Event{ID: hex.EncodeToString(nonce), Type: kind, PeerID: s.host.ID().String(), Time: time.Now().UTC(), Detail: detail}
	signature, err := s.host.Peerstore().PrivKey(s.host.ID()).Sign(eventMessage(e))
	if err != nil {
		return err
	}
	e.Signature = encode(signature)
	s.data.Events = append(s.data.Events, e)
	if len(s.data.Events) > maxEvents {
		s.data.Events = s.data.Events[len(s.data.Events)-maxEvents:]
	}
	return s.save()
}
func (s *Store) Revoke(peerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("account is closed")
	}
	if peerID == s.host.ID().String() {
		return errors.New("cannot revoke this running device")
	}
	for i := range s.data.Devices {
		d := &s.data.Devices[i]
		if d.PeerID == peerID {
			old := *d
			now := time.Now().UTC()
			d.Revoked = true
			d.RevokedAt = &now
			d.RevocationSignature = encode(ed25519.Sign(s.root, revocationMessage(*d)))
			if err := s.save(); err != nil {
				*d = old
				return err
			}
			if s.onRevoked != nil {
				go s.onRevoked(peerID)
			}
			go s.broadcastRevocations()
			return nil
		}
	}
	return errors.New("unknown device")
}
func (s *Store) Close() error {
	s.mu.Lock()
	s.closed = true
	streams := make([]network.Stream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.mu.Unlock()
	s.host.RemoveStreamHandler(SyncProtocol)
	s.host.RemoveStreamHandler(AuthProtocol)
	s.host.RemoveStreamHandler(RevocationProtocol)
	for _, stream := range streams {
		stream.Reset()
	}
	return nil
}

// ExportRecovery writes an account recovery secret to a new private file.
func ExportRecovery(dir, output string) error {
	key, err := statevault.KeyFromEnvironment()
	if err != nil {
		return err
	}
	return ExportRecoveryWithKey(dir, output, key)
}

func ExportRecoveryWithKey(dir, output string, key []byte) error {
	root, err := rootKeyWithStorage(dir, key)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "meshway-recovery-v1:%s\n", encode(root.Seed()))
	return err
}

// ImportRecovery must run with the daemon stopped. It preserves the device peer
// key and reissues its account certificate, while clearing the old local account.
func ImportRecovery(dir, input string, force bool) error {
	key, err := statevault.KeyFromEnvironment()
	if err != nil {
		return err
	}
	return ImportRecoveryWithKey(dir, input, force, key)
}

func ImportRecoveryWithKey(dir, input string, force bool, key []byte) error {
	info, err := os.Lstat(input)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 512 {
		return errors.New("recovery input must be a small regular file")
	}
	b, err := os.ReadFile(input)
	if err != nil {
		return err
	}
	value := strings.TrimSpace(string(b))
	if !strings.HasPrefix(value, "meshway-recovery-v1:") {
		return errors.New("invalid recovery file format")
	}
	seed, err := decode(strings.TrimPrefix(value, "meshway-recovery-v1:"))
	if err != nil || len(seed) != 32 {
		return errors.New("invalid recovery key")
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, "account.seed")
	if previous, readErr := statevault.ReadPrivate(path, 32, key); readErr == nil {
		// Restoring the same account preserves its signed device certificates and
		// local history; reissuing a different certificate causes sync conflicts.
		if subtle.ConstantTimeCompare(previous, seed) == 1 {
			return nil
		}
		if !force {
			return errors.New("account exists; explicit --force required to replace it")
		}
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	return replaceRoot(dir, seed, key)
}

func verifyCertificate(c Certificate, accountID string) error {
	root, err := decode(c.RootPublic)
	if err != nil || len(root) != 32 {
		return errors.New("invalid root public key")
	}
	if c.AccountID != accountID || rootID(root) != accountID {
		return errors.New("different account")
	}
	if len(c.Label) > 80 || c.CreatedAt.IsZero() {
		return errors.New("invalid certificate metadata")
	}
	signature, err := decode(c.Signature)
	if err != nil || !ed25519.Verify(root, certificateMessage(c), signature) {
		return errors.New("invalid device certificate signature")
	}
	device, err := decode(c.DevicePublic)
	if err != nil {
		return err
	}
	public, err := crypto.UnmarshalPublicKey(device)
	if err != nil {
		return err
	}
	id, err := peer.IDFromPublicKey(public)
	if err != nil || id.String() != c.PeerID {
		return errors.New("device key does not match peer ID")
	}
	return nil
}
func (s *Store) validateState(data state) error {
	if data.Deletion != nil {
		if err := s.verifyDeletion(*data.Deletion); err != nil {
			return err
		}
	}
	if len(data.Devices) > maxDevices || len(data.Events) > maxEvents {
		return errors.New("account dataset exceeds limits")
	}
	devices := map[string]Device{}
	accountID := rootID(s.root.Public().(ed25519.PublicKey))
	for _, d := range data.Devices {
		if err := verifyCertificate(d.Certificate, accountID); err != nil {
			return err
		}
		if d.PeerID != d.Certificate.PeerID || d.Label != d.Certificate.Label || !d.CreatedAt.Equal(d.Certificate.CreatedAt) {
			return errors.New("device metadata differs from certificate")
		}
		if _, ok := devices[d.PeerID]; ok {
			return errors.New("duplicate device")
		}
		devices[d.PeerID] = d
		if d.Revoked {
			signature, err := decode(d.RevocationSignature)
			if err != nil || d.RevokedAt == nil || !ed25519.Verify(s.root.Public().(ed25519.PublicKey), revocationMessage(d), signature) {
				return errors.New("invalid revocation signature")
			}
		}
	}
	ids := map[string]bool{}
	for _, e := range data.Events {
		if len(e.ID) != 32 || len(e.Type) == 0 || len(e.Type) > 64 || len(e.Detail) > 256 || e.Time.IsZero() || e.Time.After(time.Now().Add(5*time.Minute)) {
			return errors.New("invalid event fields")
		}
		if _, err := hex.DecodeString(e.ID); err != nil {
			return err
		}
		if ids[e.ID] {
			return errors.New("duplicate event")
		}
		ids[e.ID] = true
		d, ok := devices[e.PeerID]
		if !ok {
			return errors.New("event device is unknown")
		}
		publicBytes, err := decode(d.Certificate.DevicePublic)
		if err != nil {
			return err
		}
		public, err := crypto.UnmarshalPublicKey(publicBytes)
		if err != nil {
			return err
		}
		signature, err := decode(e.Signature)
		if err != nil {
			return err
		}
		valid, err := public.Verify(eventMessage(e), signature)
		if err != nil || !valid {
			return errors.New("invalid event signature")
		}
	}
	return nil
}
