package account

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

func testHost(t *testing.T) host.Host {
	t.Helper()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	return h
}
func testStore(t *testing.T, dir string, h host.Host, enabled bool) *Store {
	t.Helper()
	s, err := New(dir, h, Options{SyncEnabled: enabled})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func connect(t *testing.T, a, b host.Host) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Connect(ctx, peer.AddrInfo{ID: b.ID(), Addrs: b.Addrs()}); err != nil {
		t.Fatal(err)
	}
}
func TestRecoveryImportPreservesAccountWithDistinctDeviceIdentities(t *testing.T) {
	aDir, bDir := t.TempDir(), t.TempDir()
	aHost, bHost := testHost(t), testHost(t)
	a := testStore(t, aDir, aHost, true)
	backup := filepath.Join(t.TempDir(), "recovery.txt")
	if err := ExportRecovery(aDir, backup); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(backup)
	if info.Mode().Perm() != 0600 {
		t.Fatal("recovery permissions are not private")
	}
	if err := ExportRecovery(aDir, backup); err == nil {
		t.Fatal("overwrote recovery")
	}
	if err := ImportRecovery(bDir, backup, false); err != nil {
		t.Fatal(err)
	}
	b := testStore(t, bDir, bHost, true)
	if a.Status().AccountID != b.Status().AccountID {
		t.Fatal("recovery changed account")
	}
	if a.Status().DeviceID == b.Status().DeviceID {
		t.Fatal("devices share transport identity")
	}
	if err := ImportRecovery(bDir, backup, false); err != nil {
		t.Fatal("restoring the same account should preserve existing data:", err)
	}
}

func TestPersonalAuthenticationWithoutHistorySync(t *testing.T) {
	aDir, bDir := t.TempDir(), t.TempDir()
	aHost, bHost := testHost(t), testHost(t)
	a := testStore(t, aDir, aHost, false)
	backup := filepath.Join(t.TempDir(), "recovery.txt")
	if err := ExportRecovery(aDir, backup); err != nil {
		t.Fatal(err)
	}
	if err := ImportRecovery(bDir, backup, false); err != nil {
		t.Fatal(err)
	}
	b := testStore(t, bDir, bHost, false)
	connect(t, aHost, bHost)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.AuthorizePeer(ctx, bHost.ID()); err != nil {
		t.Fatal(err)
	}
	if len(a.Devices()) != 2 || len(b.Devices()) != 2 {
		t.Fatal("authenticated device not registered")
	}
	strangerHost := testHost(t)
	stranger := testStore(t, t.TempDir(), strangerHost, false)
	connect(t, aHost, strangerHost)
	if err := a.AuthorizePeer(ctx, strangerHost.ID()); err == nil {
		t.Fatal("different root authorized")
	}
	if err := stranger.AuthorizePeer(ctx, aHost.ID()); err == nil {
		t.Fatal("different root inbound authorized")
	}
	if err := a.Revoke(bHost.ID().String()); err != nil {
		t.Fatal(err)
	}
	if err := a.AuthorizePeer(ctx, bHost.ID()); err == nil {
		t.Fatal("revoked device authorized")
	}
}
func TestEncryptedSyncDeduplicatesHistoryAndListsDevices(t *testing.T) {
	aDir, bDir := t.TempDir(), t.TempDir()
	aHost, bHost := testHost(t), testHost(t)
	a := testStore(t, aDir, aHost, true)
	backup := filepath.Join(t.TempDir(), "recovery.txt")
	if err := ExportRecovery(aDir, backup); err != nil {
		t.Fatal(err)
	}
	if err := ImportRecovery(bDir, backup, false); err != nil {
		t.Fatal(err)
	}
	b := testStore(t, bDir, bHost, true)
	if err := a.Record("connected", "peer connection established"); err != nil {
		t.Fatal(err)
	}
	if err := b.Record("connected", "another local connection"); err != nil {
		t.Fatal(err)
	}
	connect(t, aHost, bHost)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for range 2 {
		if err := a.Sync(ctx, bHost.ID().String()); err != nil {
			t.Fatal(err)
		}
	}
	if len(a.History()) != 2 || len(b.History()) != 2 {
		t.Fatal("history did not merge/deduplicate")
	}
	if len(a.Devices()) != 2 || len(b.Devices()) != 2 {
		t.Fatal("device registry did not merge")
	}
	if err := a.Revoke(bHost.ID().String()); err != nil {
		t.Fatal(err)
	}
	if err := a.Sync(ctx, bHost.ID().String()); err == nil {
		t.Fatal("revoked peer accepted")
	}
	if err := b.Sync(ctx, aHost.ID().String()); err == nil {
		t.Fatal("revoked inbound peer accepted")
	}
}
func TestDifferentAccountAndDisabledSyncRejected(t *testing.T) {
	aHost, bHost := testHost(t), testHost(t)
	a := testStore(t, t.TempDir(), aHost, true)
	b := testStore(t, t.TempDir(), bHost, true)
	connect(t, aHost, bHost)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Sync(ctx, bHost.ID().String()); err == nil {
		t.Fatal("different account sync accepted")
	}
	b.mu.Lock()
	b.syncEnabled = false
	b.mu.Unlock()
	if err := b.Sync(ctx, aHost.ID().String()); err == nil {
		t.Fatal("disabled sync accepted")
	}
}
func TestTamperedEventsAndCertificatesRejected(t *testing.T) {
	s := testStore(t, t.TempDir(), testHost(t), true)
	if err := s.Record("connected", "metadata"); err != nil {
		t.Fatal(err)
	}
	copyState := state{Devices: s.Devices(), Events: s.History()}
	copyState.Events[0].Detail = "forged"
	if err := s.validateState(copyState); err == nil {
		t.Fatal("forged history accepted")
	}
	copyState.Events = s.History()
	copyState.Devices[0].Certificate.Label = "forged"
	if err := s.validateState(copyState); err == nil {
		t.Fatal("forged certificate accepted")
	}
}
func TestLocalHistoryPersistsAndPrivateSeedPermissionsEnforced(t *testing.T) {
	dir := t.TempDir()
	h := testHost(t)
	s := testStore(t, dir, h, false)
	if err := s.Record("connected", "local only"); err != nil {
		t.Fatal(err)
	}
	id := s.Status().AccountID
	s.Close()
	s2 := testStore(t, dir, h, false)
	if s2.Status().AccountID != id || len(s2.History()) != 1 {
		t.Fatal("state not preserved")
	}
	s2.Close()
	if err := os.Chmod(filepath.Join(dir, "account.seed"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir, h, Options{}); err == nil {
		t.Fatal("world-readable recovery secret accepted")
	}
}
