package core

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func ownerRequest(n *Node, method, path, owner string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:7777"+path, bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+n.Config.APIToken)
	r.Header.Set("X-Meshway-VPN-Session", owner)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	n.apiHandler().ServeHTTP(w, r)
	return w
}
func ownerStart(n *Node, pin, owner string) *httptest.ResponseRecorder {
	return ownerRequest(n, "POST", "/v1/vpn/start", owner, []byte(fmt.Sprintf(`{"mode":"exit","peer_id":%q,"session_id":%q}`, pin, owner)))
}

func TestNativeHelperCannotClaimOrStopPreexistingPacketSession(t *testing.T) {
	client, server := pair(t, true, true, true)
	if err := client.StartPacketVPN(server.Host.ID().String()); err != nil {
		t.Fatal(err)
	}
	owner := strings.Repeat("a", 32)
	if w := ownerStart(client, server.Host.ID().String(), owner); w.Code != 409 {
		t.Fatalf("existing queue claimed: %d", w.Code)
	}
	if w := ownerRequest(client, "POST", "/v1/vpn/stop", owner, nil); w.Code != 409 {
		t.Fatal("helper stopped an unowned queue")
	}
	if !client.vpn.Status().Active {
		t.Fatal("foreign helper changed the original packet session")
	}
}

func TestConcurrentHelpersHaveSingleOwnerAndRejectForeignPollAndStop(t *testing.T) {
	client, server := pair(t, true, true, true)
	owners := []string{strings.Repeat("a", 32), strings.Repeat("b", 32)}
	results := make([]int, 2)
	var workers sync.WaitGroup
	for i, owner := range owners {
		workers.Add(1)
		go func(i int, owner string) {
			defer workers.Done()
			results[i] = ownerStart(client, server.Host.ID().String(), owner).Code
		}(i, owner)
	}
	workers.Wait()
	winner := ""
	for i, code := range results {
		if code == 200 {
			if winner != "" {
				t.Fatal("both helpers acquired the same queue")
			}
			winner = owners[i]
		} else if code != 409 {
			t.Fatalf("unexpected helper start status %d", code)
		}
	}
	if winner == "" {
		t.Fatal("neither helper acquired the queue")
	}
	for _, owner := range []string{"", strings.Repeat("c", 32)} {
		for _, request := range []struct{ method, path string }{{"POST", "/v1/vpn/read"}, {"GET", "/v1/vpn/status"}, {"POST", "/v1/vpn/stop"}} {
			if w := ownerRequest(client, request.method, request.path, owner, nil); w.Code != 409 {
				t.Fatalf("foreign %s status %d", request.path, w.Code)
			}
		}
	}
	if w := ownerRequest(client, "GET", "/v1/vpn/status", winner, nil); w.Code != 200 {
		t.Fatal("owner could not confirm its session")
	}
	if w := ownerRequest(client, "POST", "/v1/vpn/stop", winner, nil); w.Code != 200 {
		t.Fatal("owner could not stop its session")
	}
	if client.vpn.Status().PeerID != "" {
		t.Fatal("owner stop retained its device")
	}
}

func TestLostStartResponseCanConfirmOwnershipWithoutStoppingOtherHelper(t *testing.T) {
	client, server := pair(t, true, true, true)
	owner := strings.Repeat("d", 32)
	// The server handles a request even if its HTTP caller loses the response.
	// Ownership is committed before sending success and can be re-queried.
	_ = ownerStart(client, server.Host.ID().String(), owner)
	if w := ownerRequest(client, "GET", "/v1/vpn/status", owner, nil); w.Code != 200 {
		t.Fatal("lost response left an unidentifiable owned session")
	}
	other := strings.Repeat("e", 32)
	if w := ownerRequest(client, "POST", "/v1/vpn/stop", other, nil); w.Code != 409 {
		t.Fatal("ambiguous start cleanup could stop a different helper")
	}
	if !client.vpn.Status().Active {
		t.Fatal("foreign cleanup closed the real session")
	}
}

func TestRevokedOwnedPacketSessionRemainsProtectedUntilOwnerCleanup(t *testing.T) {
	client, server := pair(t, true, true, true)
	owner := strings.Repeat("f", 32)
	if w := ownerStart(client, server.Host.ID().String(), owner); w.Code != 200 {
		t.Fatal("helper start failed")
	}
	client.vpn.RevokePeer(server.Host.ID().String())
	if w := ownerRequest(client, "POST", "/v1/vpn/stop", strings.Repeat("a", 32), nil); w.Code != 409 {
		t.Fatal("revocation released ownership to a foreign client")
	}
	if w := ownerRequest(client, "POST", "/v1/vpn/stop", owner, nil); w.Code != 200 {
		t.Fatal("revoked helper could not clean its ownership")
	}
	if client.vpnOwner != "" {
		t.Fatal("owner cleanup retained ownership")
	}
}

func TestOwnedHelperCanceledReadReleasesOwnershipLock(t *testing.T) {
	client, server := pair(t, true, true, true)
	owner := strings.Repeat("1", 32)
	if w := ownerStart(client, server.Host.ID().String(), owner); w.Code != 200 {
		t.Fatal("helper start failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", "http://127.0.0.1:7777/v1/vpn/read", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+client.Config.APIToken)
	r.Header.Set("X-Meshway-VPN-Session", owner)
	done := make(chan struct{})
	go func() { client.apiHandler().ServeHTTP(httptest.NewRecorder(), r); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled owned poll blocked")
	}
	if w := ownerRequest(client, "POST", "/v1/vpn/stop", owner, nil); w.Code != http.StatusOK {
		t.Fatal("canceled read held the owner lock")
	}
}

func TestCaptureReadinessRequiresExactActiveOwnerAndClearsOnStop(t *testing.T) {
	client, server := pair(t, true, true, true)
	owner := strings.Repeat("c", 32)
	ready := []byte(`{"ready":true,"device":"utun99","instance":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}`)
	if w := ownerRequest(client, "POST", "/v1/vpn/capture-ready", owner, ready); w.Code != 409 {
		t.Fatal("readiness accepted before packet session")
	}
	if w := ownerStart(client, server.Host.ID().String(), owner); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := ownerRequest(client, "POST", "/v1/vpn/capture-ready", strings.Repeat("d", 32), ready); w.Code != 409 {
		t.Fatal("foreign readiness accepted")
	}
	if w := ownerRequest(client, "POST", "/v1/vpn/capture-ready", owner, []byte(`{"ready":true,"device":"en0"}`)); w.Code != 400 {
		t.Fatal("physical device accepted")
	}
	if w := ownerRequest(client, "POST", "/v1/vpn/capture-ready", owner, ready); w.Code != 200 || !strings.Contains(w.Body.String(), `"capture_ready":true`) {
		t.Fatal(w.Body.String())
	}
	if w := ownerRequest(client, "POST", "/v1/vpn/capture-ready", owner, []byte(`{"ready":false}`)); w.Code != 200 || !strings.Contains(w.Body.String(), `"capture_ready":false`) {
		t.Fatal("cleanup did not clear capture")
	}
	if w := ownerRequest(client, "POST", "/v1/vpn/capture-ready", owner, ready); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := ownerRequest(client, "POST", "/v1/vpn/stop", owner, nil); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if client.vpnCaptureReady || client.vpnCaptureDevice != "" {
		t.Fatal("stop retained native readiness")
	}
}
