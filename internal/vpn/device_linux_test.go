//go:build linux && !android

package vpn

import (
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/sys/unix"
)

func internetChecksum(data []byte) uint16 {
	var sum uint32
	for len(data) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(data))
		data = data[2:]
	}
	if len(data) > 0 {
		sum += uint32(data[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

// This test changes only its Linux namespace. Run in a disposable container,
// never the developer host. It requires CAP_NET_ADMIN and /dev/net/tun.
func TestLinuxKernelTUNEncryptedPing(t *testing.T) {
	if os.Getenv("MESHWAY_LINUX_TUN_LAB") != "1" {
		t.Skip("opt-in disposable Linux network namespace only")
	}
	fd, err := OpenDevice("meshwaylab0")
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	for _, args := range [][]string{{"addr", "add", "10.73.0.1/24", "dev", "meshwaylab0"}, {"link", "set", "meshwaylab0", "mtu", "1280", "up"}} {
		if output, err := exec.Command("ip", args...).CombinedOutput(); err != nil {
			t.Fatalf("ip: %v: %s", err, output)
		}
	}
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
	a := socketDevice(t)
	if err = right.AttachFD(fd, ""); err != nil {
		t.Fatal(err)
	}
	if err = left.AttachFD(a[0], server.ID().String()); err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 20+8+16)
	packet[0] = 0x45
	packet[8] = 64
	packet[9] = 1
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
	copy(packet[12:16], []byte{10, 73, 0, 2})
	copy(packet[16:20], []byte{10, 73, 0, 1})
	binary.BigEndian.PutUint16(packet[10:12], internetChecksum(packet[:20]))
	packet[20] = 8
	packet[24] = 0x12
	packet[25] = 0x34
	packet[27] = 1
	copy(packet[28:], []byte("meshway-real-tun!"))
	binary.BigEndian.PutUint16(packet[22:24], internetChecksum(packet[20:]))
	if _, err = unix.Write(a[1], packet); err != nil {
		t.Fatal(err)
	}
	var reply []byte
	for attempt := 0; attempt < 20; attempt++ {
		reply = receiveDevice(t, a[1])
		// Kernel IPv6 router solicitations may precede the requested IPv4 response.
		if len(reply) >= 28 && reply[0]>>4 == 4 && reply[9] == 1 && reply[20] == 0 {
			break
		}
	}
	if len(reply) != len(packet) || reply[9] != 1 || reply[20] != 0 || reply[24] != 0x12 || reply[25] != 0x34 {
		t.Fatalf("not a kernel ICMP echo reply: %x", reply)
	}
	if internetChecksum(reply[:20]) != 0 || internetChecksum(reply[20:]) != 0 {
		t.Fatal("kernel reply checksum invalid")
	}
	t.Log("PASS: simulated peer IP -> encrypted stream -> actual Linux TUN -> kernel ICMP reply -> encrypted return stream")
}
