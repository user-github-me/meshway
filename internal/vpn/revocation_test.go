package vpn

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestRevocationCancelsAuthorizationRacingWithPacketHandshake(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	left, right := exitPair(t, Options{Exit: true, Authorize: func(context.Context, peer.ID) bool { close(entered); <-release; return true }}, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := left.host.NewStream(ctx, right.host.ID(), ExitProtocol)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Reset()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("authorization did not start")
	}
	right.RevokePeer(left.host.ID().String())
	close(release)
	if err = exitHandshake(stream, 1280, true); err == nil {
		t.Fatal("revoked pending handshake was accepted")
	}
	if err = left.StartPacketVPN(right.host.ID().String()); err == nil {
		t.Fatal("revoked peer reopened packet exit")
	}
}

func TestRevocationStopsClientReconnectAndCancelsPacketPoll(t *testing.T) {
	left, right := exitPair(t, Options{Exit: true}, true)
	if err := left.StartPacketVPN(right.host.ID().String()); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := left.ReadPacketContext(context.Background()); result <- err }()
	left.RevokePeer(right.host.ID().String())
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("poll survived peer revocation")
		}
	case <-time.After(time.Second):
		t.Fatal("revocation did not cancel blocked packet poll")
	}
	if left.Status().Active {
		t.Fatal("revoked client retained active session")
	}
	if err := left.StartPacketVPN(right.host.ID().String()); err == nil {
		t.Fatal("revoked client reopened exit")
	}
}

func TestCanceledPacketPollDoesNotConsumeFollowingPacket(t *testing.T) {
	left, right := exitPair(t, Options{Exit: true}, true)
	if err := left.StartPacketVPN(right.host.ID().String()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := left.ReadPacketContext(ctx); result <- err }()
	cancel()
	select {
	case <-result:
	case <-time.After(time.Second):
		t.Fatal("canceled packet poll remained blocked")
	}
	left.exit.mu.Lock()
	client := left.exit.client
	left.exit.mu.Unlock()
	client.queue.Write([]byte("next-packet"))
	readCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	packet, err := left.ReadPacketContext(readCtx)
	if err != nil || string(packet) != "next-packet" {
		t.Fatal("canceled poll stole the next packet")
	}
}
