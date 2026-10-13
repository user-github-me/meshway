//go:build darwin || linux

package vpn

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/sys/unix"
)

func androidLabChecksum(data []byte) uint16 {
	var sum uint32
	for len(data) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(data))
		data = data[2:]
	}
	if len(data) != 0 {
		sum += uint32(data[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

// Opt-in Android emulator lab. The host creates only a packet socket pair;
// the Android VpnService creates its own real TUN with user consent. No host
// interfaces, routes, DNS, or proxy configuration are changed.
func TestAndroidKernelTUNEncryptedPing(t *testing.T) {
	if os.Getenv("MESHWAY_ANDROID_TUN_LAB") != "1" {
		t.Skip("opt-in Android VpnService lab")
	}
	remote := os.Getenv("MESHWAY_ANDROID_PEER_ID")
	if _, err := peer.Decode(remote); err != nil {
		t.Fatal("MESHWAY_ANDROID_PEER_ID must be the emulator's device peer ID")
	}
	port := os.Getenv("MESHWAY_ANDROID_LAB_PORT")
	if port == "" {
		port = "14001"
	}
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/" + port))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	service, err := New(h, Options{MTU: 1280, AllowedPeers: []string{remote}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	device := socketDevice(t)
	if err = service.AttachFD(device[0], ""); err != nil {
		t.Fatal(err)
	}
	address := "/ip4/10.0.2.2/tcp/" + port + "/p2p/" + h.ID().String()
	fmt.Println("ANDROID_LAB_HOST_PEER=" + h.ID().String())
	fmt.Println("ANDROID_LAB_BOOTSTRAP=" + address)
	if file := os.Getenv("MESHWAY_ANDROID_LAB_INFO"); file != "" {
		if err = os.WriteFile(file, []byte(h.ID().String()+"\n"+address+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	packet := make([]byte, 44)
	packet[0], packet[8], packet[9] = 0x45, 64, 1
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
	copy(packet[12:16], []byte{10, 77, 0, 1})
	copy(packet[16:20], []byte{10, 77, 0, 2})
	binary.BigEndian.PutUint16(packet[10:12], androidLabChecksum(packet[:20]))
	packet[20], packet[24], packet[25], packet[27] = 8, 0x12, 0x34, 1
	copy(packet[28:], []byte("meshway-android!"))
	binary.BigEndian.PutUint16(packet[22:24], androidLabChecksum(packet[20:]))
	deadline := time.Now().Add(90 * time.Second)
	poll := []unix.PollFd{{Fd: int32(device[1]), Events: unix.POLLIN}}
	activeLogged := false
	for time.Now().Before(deadline) {
		service.mu.Lock()
		active := service.stream != nil
		service.mu.Unlock()
		if !active {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if !activeLogged {
			fmt.Println("ANDROID_LAB_STREAM_ACTIVE")
			activeLogged = true
		}
		if _, err = unix.Write(device[1], packet); err != nil {
			t.Fatal(err)
		}
		count, err := unix.Poll(poll, 1000)
		if err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			continue
		}
		buffer := make([]byte, 2000)
		length, err := unix.Read(device[1], buffer)
		if err != nil {
			t.Fatal(err)
		}
		reply := buffer[:length]
		if len(reply) < 28 || reply[0]>>4 != 4 || reply[9] != 1 || reply[20] != 0 {
			continue
		}
		if len(reply) != len(packet) || reply[24] != 0x12 || reply[25] != 0x34 {
			t.Fatalf("unexpected ICMP reply: %x", reply)
		}
		if androidLabChecksum(reply[:20]) != 0 || androidLabChecksum(reply[20:]) != 0 {
			t.Fatal("Android kernel reply checksum invalid")
		}
		t.Log("PASS: host packet socket -> encrypted peer stream -> actual Android VpnService TUN -> Android kernel ICMP echo -> encrypted return stream")
		return
	}
	t.Fatal("Android kernel ICMP reply not received within 90 seconds")
}
