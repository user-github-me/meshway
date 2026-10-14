package core

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
)

// This opt-in deadline experiment keeps endpoints on loopback and simulates
// bounded authenticated-server work before its target-connect acknowledgement.
// It diagnoses the end-to-end budget without delaying the normal test suite.
func TestDelayedGatewayHandshakeBudget(t *testing.T) {
	if os.Getenv("MESHWAY_HANDSHAKE_LAB") != "1" {
		t.Skip("bounded delayed handshake experiment")
	}
	client, server := pair(t, true, true, true)
	server.Host.SetStreamHandler(TunnelProtocol, func(s network.Stream) {
		defer s.Close()
		var req request
		if readFrame(s, &req) != nil {
			return
		}
		time.Sleep(10500 * time.Millisecond)
		_ = writeFrame(s, response{OK: true})
	})
	ctx, cancel := context.WithTimeout(context.Background(), 13*time.Second)
	defer cancel()
	s, err := client.openTunnel(ctx, server.Host.ID().String(), request{Target: "127.0.0.1:12345"})
	if err != nil {
		t.Fatalf("bounded server work exceeded the end-to-end budget: %v", err)
	}
	s.Close()
	t.Log("PASS delayed 10.5-second gateway acknowledgement inside bounded end-to-end peer budget; target and account limits unchanged")
}

func TestGatewayHandshakeHonorsCallerCancellation(t *testing.T) {
	client, server := pair(t, true, true, true)
	requested := make(chan struct{})
	closed := make(chan struct{})
	server.Host.SetStreamHandler(TunnelProtocol, func(s network.Stream) {
		defer s.Close()
		defer close(closed)
		var req request
		if readFrame(s, &req) != nil {
			return
		}
		close(requested)
		var data [1]byte
		_, _ = s.Read(data[:])
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := client.openTunnel(ctx, server.Host.ID().String(), request{Target: "127.0.0.1:12345"})
		result <- err
	}()
	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("gateway request did not reach peer")
	}
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("canceled handshake succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled handshake retained pending peer stream")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("gateway stream was not reset on caller cancellation")
	}
}
