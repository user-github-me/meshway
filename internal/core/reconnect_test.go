package core

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/p2p/net/swarm"
)

// Explicit user retries should not inherit libp2p's cooldown after a transient
// transport failure. Each cycle still authenticates the peer and its exit role.
func TestExplicitReconnectClearsBackoffAndRestoresTraffic(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "reconnected encrypted exit")
	}))
	defer origin.Close()
	clientConfig := testConfig(t)
	clientID, err := IdentityID(clientConfig.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	serverConfig := testConfig(t)
	serverConfig.Exit, serverConfig.AllowPrivateTargets, serverConfig.Advertise = true, true, true
	serverConfig.AllowedPeers = []string{clientID}
	server := startNode(t, serverConfig)
	client := startNode(t, clientConfig)
	connect(t, client, server)
	dialer, ok := client.Host.Network().(*swarm.Swarm)
	if !ok {
		t.Fatal("fixture does not use the production libp2p dialer")
	}
	for cycle := 0; cycle < 10; cycle++ {
		client.Disconnect()
		if status := client.Status(); status.UpstreamPeer != "" || status.UpstreamConnected {
			t.Fatal("disconnect retained the selected upstream")
		}
		if err := client.Host.Network().ClosePeer(server.Host.ID()); err != nil {
			t.Fatal(err)
		}
		for _, address := range server.Host.Addrs() {
			dialer.Backoff().AddBackoff(server.Host.ID(), address)
			if !dialer.Backoff().Backoff(server.Host.ID(), address) {
				t.Fatal("fixture did not establish a real dial cooldown")
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if cycle%2 == 0 {
			if _, err := client.Connect(ctx, server.Status().Addresses[0]); err != nil {
				cancel()
				t.Fatalf("explicit connect cycle %d: %v", cycle, err)
			}
		}
		err := client.SelectPeer(ctx, server.Host.ID().String(), "")
		cancel()
		if err != nil {
			t.Fatalf("explicit select cycle %d: %v", cycle, err)
		}
		conn, code := socksDial(t, client.Status().SOCKSListen, strings.TrimPrefix(origin.URL, "http://"))
		if code != 0 {
			conn.Close()
			t.Fatalf("reconnected SOCKS reply %d", code)
		}
		_, _ = io.WriteString(conn, "GET / HTTP/1.0\r\nHost: loopback-fixture\r\n\r\n")
		body, err := io.ReadAll(conn)
		conn.Close()
		if err != nil || !bytes.Contains(body, []byte("reconnected encrypted exit")) {
			t.Fatalf("reconnected traffic cycle %d failed: %v", cycle, err)
		}
		if !client.Status().UpstreamConnected {
			t.Fatal("selected authenticated upstream reported disconnected")
		}
	}
}
