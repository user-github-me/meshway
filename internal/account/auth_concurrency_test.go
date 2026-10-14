package account

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
)

func TestConcurrentPersonalAuthenticationSharesVerifiedMembership(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	start := make(chan struct{})
	failures := make(chan error, 50)
	var workers sync.WaitGroup
	for i := 0; i < 50; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			if err := a.AuthorizePeer(ctx, bHost.ID()); err != nil {
				failures <- err
			}
		}()
	}
	close(start)
	workers.Wait()
	close(failures)
	var count int
	for err := range failures {
		count++
		if count < 4 {
			t.Logf("authorization failure: %v", err)
		}
	}
	if count != 0 {
		t.Fatalf("%d valid same-account concurrent requests rejected", count)
	}
	// Reusing a proof must depend on the exact still-live encrypted connection,
	// rather than an account registry entry retained across a reconnect.
	bHost.RemoveStreamHandler(AuthProtocol)
	if err := aHost.Network().ClosePeer(bHost.ID()); err != nil {
		t.Fatal(err)
	}
	connect(t, aHost, bHost)
	if err := a.AuthorizePeer(ctx, bHost.ID()); err == nil {
		t.Fatal("new transport connection reused the old account proof")
	}
	bHost.SetStreamHandler(AuthProtocol, b.handleAuth)
	if err := a.AuthorizePeer(ctx, bHost.ID()); err != nil {
		t.Fatal(err)
	}
	if err := a.Revoke(bHost.ID().String()); err != nil {
		t.Fatal(err)
	}
	if err := a.AuthorizePeer(ctx, bHost.ID()); err == nil {
		t.Fatal("cached certificate allowed a revoked device")
	}
	if err := b.AuthorizePeer(ctx, aHost.ID()); err == nil {
		t.Fatal("remote revocation was ignored")
	}
	t.Log("PASS 50 concurrent root-certificate authorizations; reconnect requires a new proof; immediate local and remote revocation denial")
}

func TestCanceledAuthWaiterDoesNotCancelSharedProof(t *testing.T) {
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
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	bHost.SetStreamHandler(AuthProtocol, func(stream network.Stream) { once.Do(func() { close(entered) }); <-release; b.handleAuth(stream) })
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { first <- a.AuthorizePeer(firstCtx, bHost.ID()) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("authentication exchange did not start")
	}
	secondCtx, cancelSecond := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelSecond()
	second := make(chan error, 1)
	go func() { second <- a.AuthorizePeer(secondCtx, bHost.ID()) }()
	cancelFirst()
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("waiter cancellation missing")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled auth waiter remained blocked")
	}
	close(release)
	if err := <-second; err != nil {
		t.Fatalf("one canceled caller aborted other valid caller: %v", err)
	}
}

func TestDistinctUntrustedAuthenticationKeepsAdmissionBound(t *testing.T) {
	h := testHost(t)
	s := testStore(t, t.TempDir(), h, false)
	for i := 0; i < 8; i++ {
		if !s.beginAuth() {
			t.Fatal("fixture failed to reserve bounded authentication slots")
		}
	}
	stranger := testHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.AuthorizePeer(ctx, stranger.ID()); err == nil {
		t.Fatal("new untrusted peer exceeded exchange admission bound")
	}
	s.mu.Lock()
	pending, flights := s.authPending, len(s.authFlights)
	s.mu.Unlock()
	if pending != 8 || flights != 0 {
		t.Fatal("rejected distinct peer allocated an unbounded auth flight")
	}
	for i := 0; i < 8; i++ {
		s.endAuth()
	}
}
