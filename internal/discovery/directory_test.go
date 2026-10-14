package discovery

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
)

func directoryHost(t *testing.T) host.Host {
	t.Helper()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	return h
}
func connectDirectoryHost(t *testing.T, client, server host.Host) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Connect(ctx, peer.AddrInfo{ID: server.ID(), Addrs: server.Addrs()}); err != nil {
		t.Fatal(err)
	}
}
func hintServer(h host.Host) directoryHint {
	return directoryHint{PeerID: h.ID().String(), Addresses: []string{h.Addrs()[0].String() + "/p2p/" + h.ID().String()}, Expires: time.Now().Add(time.Minute)}
}
func serveDirectoryHints(h host.Host, hints []directoryHint) {
	h.SetStreamHandler(DirectoryProtocol, func(stream network.Stream) {
		defer stream.Close()
		body, _ := json.Marshal(directoryResponse{Servers: hints})
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(body)))
		stream.Write(size[:])
		stream.Write(body)
	})
}

func TestDirectoryOwnerValidationAndPrivateDialPolicy(t *testing.T) {
	for _, known := range []bool{false, true} {
		name := "reject-private"
		if known {
			name = "explicit-private-invitation"
		}
		t.Run(name, func(t *testing.T) {
			client, source, server := directoryHost(t), directoryHost(t), directoryHost(t)
			connectDirectoryHost(t, client, source)
			discovery, err := New(client, Options{AutoDiscover: true})
			if err != nil {
				t.Fatal(err)
			}
			defer discovery.Close()
			discovery.TrustPeer(source.ID())
			advertised, err := New(server, Options{Advertise: true, Label: "Owner supplied label", Exit: true, AllowedPeers: []string{client.ID().String()}})
			if err != nil {
				t.Fatal(err)
			}
			defer advertised.Close()
			serveDirectoryHints(source, []directoryHint{hintServer(server)})
			if known {
				client.Peerstore().AddAddrs(server.ID(), server.Addrs(), peerstore.PermanentAddrTTL)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			discovery.RefreshDiscovery(ctx)
			discovery.mu.Lock()
			cached, exists := discovery.cache[server.ID()]
			origin := discovery.learned[server.ID()]
			discovery.mu.Unlock()
			if !known {
				if exists || client.Network().Connectedness(server.ID()) == network.Connected {
					t.Fatal("unknown private destination dialed through directory")
				}
			} else {
				if !exists || cached.Label != "Owner supplied label" || !cached.Available || !cached.CanConnect || origin.Hops != 1 {
					t.Fatal("actual owner advertisement was not authenticated")
				}
			}
		})
	}
}
func TestDirectoryNeverForwardsPersonalAdvertisements(t *testing.T) {
	client, source, personal := directoryHost(t), directoryHost(t), directoryHost(t)
	connectDirectoryHost(t, source, personal)
	connectDirectoryHost(t, client, source)
	p, err := New(personal, Options{Advertise: true, Personal: true, Exit: true, Authorize: func(context.Context, peer.ID) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	directory, err := New(source, Options{AutoDiscover: true})
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err = directory.Server(ctx, personal.ID()); err != nil {
		t.Fatal(err)
	}
	reader, err := New(client, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	hints, err := reader.queryDirectory(ctx, source.ID())
	if err != nil {
		t.Fatal(err)
	}
	for _, hint := range hints {
		if hint.PeerID == personal.ID().String() {
			t.Fatal("private personal-server advertisement leaked")
		}
	}
}
func TestDirectoryExpiryHopAndLiteralAddressBounds(t *testing.T) {
	client, source, server := directoryHost(t), directoryHost(t), directoryHost(t)
	connectDirectoryHost(t, client, source)
	service, err := New(client, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	valid := hintServer(server)
	old := valid
	old.Expires = time.Now().Add(-time.Second)
	future := valid
	future.Expires = time.Now().Add(time.Hour)
	hops := valid
	hops.Hops = 2
	serveDirectoryHints(source, []directoryHint{old, future, hops})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hints, err := service.queryDirectory(ctx, source.ID())
	if err != nil {
		t.Fatal(err)
	}
	if len(hints) != 0 {
		t.Fatal("expired, excessive-hop or future hint accepted")
	}
	for _, address := range []string{"/dns4/example.com/tcp/4001/p2p/" + server.ID().String(), "/ip4/127.0.0.1/tcp/4001/p2p/" + client.ID().String()} {
		if _, err = safeDirectoryAddress(address, server.ID()); err == nil {
			t.Fatal("DNS or mismatched directory address accepted")
		}
	}
	for _, address := range []string{"/ip4/127.0.0.1/tcp/4001/p2p/", "/ip4/10.1.2.3/tcp/4001/p2p/", "/ip4/198.18.0.1/tcp/4001/p2p/", "/ip6/fd00::1/tcp/4001/p2p/"} {
		a, err := safeDirectoryAddress(address+server.ID().String(), server.ID())
		if err != nil {
			t.Fatal(err)
		}
		if isPublicMultiaddr(a) {
			t.Fatal("private literal considered public")
		}
	}
}
