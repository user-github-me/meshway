package core

import (
	"bytes"
	"context"
	"github.com/user-github-me/meshway/internal/config"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPacketHelperAPIRejectsUnauthenticatedAndMalformedInput(t *testing.T) {
	n := startNode(t, testConfig(t))
	for _, path := range []string{"/v1/vpn/read", "/v1/vpn/write"} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:7777"+path, nil)
		w := httptest.NewRecorder()
		n.apiHandler().ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s: unauthenticated status %d", path, w.Code)
		}
	}
	for _, tc := range []struct {
		method, contentType string
		size, code          int
	}{
		{"GET", "application/octet-stream", 20, 405},
		{"POST", "application/json", 20, 415},
		{"POST", "application/octet-stream", 9001, 400},
		{"POST", "application/octet-stream", 0, 400},
		{"POST", "application/octet-stream", 20, 400},
	} {
		r := httptest.NewRequest(tc.method, "http://127.0.0.1:7777/v1/vpn/write", bytes.NewReader(make([]byte, tc.size)))
		r.Header.Set("Authorization", "Bearer "+n.Config.APIToken)
		r.Header.Set("Content-Type", tc.contentType)
		w := httptest.NewRecorder()
		n.apiHandler().ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("%+v: status %d body %s", tc, w.Code, w.Body.String())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := n.ReadPacketContext(ctx); err == nil {
		t.Fatal("unstarted packet session must fail")
	}
}

func TestPacketHelperBoundedPollAndCancellation(t *testing.T) {
	client, server := pair(t, true, true, true)
	if err := client.StartPacketVPN(server.Host.ID().String()); err != nil {
		t.Fatal(err)
	}
	// A canceled authenticated HTTP poll exits promptly without spawning a
	// goroutine that could steal the following packet from the queue.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	r := httptest.NewRequest("POST", "http://127.0.0.1:7777/v1/vpn/read", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+client.Config.APIToken)
	w := httptest.NewRecorder()
	go func() { client.apiHandler().ServeHTTP(w, r); close(done) }()
	cancel()
	select {
	case <-done:
		if w.Code != 204 {
			t.Fatal("canceled poll failed")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled helper HTTP poll remained blocked")
	}
	if len(client.sessions) != 0 {
		t.Fatal("packet helper admission slot leaked")
	}
	limited := testConfig(t)
	limited.Limits = &config.Limits{MaxSessions: 1}
	node := startNode(t, limited)
	node.sessions <- struct{}{}
	for _, path := range []string{"/v1/vpn/read", "/v1/vpn/write"} {
		request := httptest.NewRequest("POST", "http://127.0.0.1:7777"+path, nil)
		request.Header.Set("Authorization", "Bearer "+node.Config.APIToken)
		response := httptest.NewRecorder()
		node.apiHandler().ServeHTTP(response, request)
		if response.Code != 429 {
			t.Fatal("packet helper exceeded admission limit")
		}
	}
	<-node.sessions
}
