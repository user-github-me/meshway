package discovery

import (
	"context"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestAuthenticatedServerCatalogAndPermissions(t *testing.T) {
	client, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = client.Connect(ctx, peer.AddrInfo{ID: server.ID(), Addrs: server.Addrs()}); err != nil {
		t.Fatal(err)
	}
	a, _ := New(client, Options{})
	defer a.Close()
	b, _ := New(server, Options{Advertise: true, Label: "My VPS", Country: "DE", Exit: true, AllowedPeers: []string{client.ID().String()}})
	defer b.Close()
	servers := a.Servers(ctx)
	if len(servers) != 1 || servers[0].Label != "My VPS" || !servers[0].CanConnect || servers[0].Country != "DE" {
		t.Fatalf("wrong catalog: %+v", servers)
	}
	if _, err = b.Server(ctx, client.ID()); err == nil {
		t.Fatal("private device advertised")
	}
	if err = ValidateAddress(servers[0].Address, client.ID()); err == nil {
		t.Fatal("mismatched address accepted")
	}
}
