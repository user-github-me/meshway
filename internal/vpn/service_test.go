//go:build linux || darwin

package vpn

import (
	"bytes"
	"context"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/sys/unix"
)

func socketDevice(t *testing.T) [2]int {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unix.Close(fds[0]); unix.Close(fds[1]) })
	return fds
}

func receiveDevice(t *testing.T, fd int) []byte {
	t.Helper()
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	if n, err := unix.Poll(poll, 5000); err != nil || n == 0 {
		t.Fatalf("packet timeout: %v", err)
	}
	b := make([]byte, 2000)
	n, err := unix.Read(fd, b)
	if err != nil {
		t.Fatal(err)
	}
	return b[:n]
}

func TestEncryptedIPSession(t *testing.T) {
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
	left, err := New(client, Options{MTU: 1280})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()
	right, err := New(server, Options{MTU: 1280, AllowedPeers: []string{client.ID().String()}})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()
	a, b := socketDevice(t), socketDevice(t)
	if err = right.AttachFD(b[0], ""); err != nil {
		t.Fatal(err)
	}
	if err = left.AttachFD(a[0], server.ID().String()); err != nil {
		t.Fatal(err)
	}
	packet := ipPacket(4, "IPv4 over authenticated encrypted peer")
	if _, err = unix.Write(a[1], packet); err != nil {
		t.Fatal(err)
	}
	if got := receiveDevice(t, b[1]); !bytes.Equal(got, packet) {
		t.Fatal("forward packet changed")
	}
	packet = ipPacket(6, "IPv6 return path")
	if _, err = unix.Write(b[1], packet); err != nil {
		t.Fatal(err)
	}
	if got := receiveDevice(t, a[1]); !bytes.Equal(got, packet) {
		t.Fatal("return packet changed")
	}
	done := make(chan struct{})
	go func() { left.Close(); right.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("VPN shutdown blocked")
	}
}

func TestUnauthorizedIPPeer(t *testing.T) {
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
	left, _ := New(client, Options{})
	defer left.Close()
	right, _ := New(server, Options{})
	defer right.Close()
	a, b := socketDevice(t), socketDevice(t)
	if err = right.AttachFD(b[0], ""); err != nil {
		t.Fatal(err)
	}
	if err = left.AttachFD(a[0], server.ID().String()); err == nil {
		t.Fatal("peer without authorization was accepted")
	}
}
