package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"
	"time"
)

func socksUDPAssociate(t *testing.T, client *Node) (net.Conn, *net.UDPAddr) {
	t.Helper()
	control, err := net.DialTimeout("tcp", client.Status().SOCKSListen, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	control.SetDeadline(time.Now().Add(5 * time.Second))
	control.Write([]byte{5, 1, 0})
	var method [2]byte
	if _, err = io.ReadFull(control, method[:]); err != nil || method != [2]byte{5, 0} {
		t.Fatal("SOCKS handshake failed")
	}
	control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	var reply [10]byte
	if _, err = io.ReadFull(control, reply[:]); err != nil {
		t.Fatal(err)
	}
	if reply[1] != 0 {
		control.Close()
		t.Fatalf("UDP associate refused code%d", reply[1])
	}
	control.SetDeadline(time.Time{})
	return control, &net.UDPAddr{IP: net.IP(reply[4:8]), Port: int(binary.BigEndian.Uint16(reply[8:]))}
}
func testUDPOrigin(t *testing.T, network, address string) *net.UDPConn {
	t.Helper()
	a, err := net.ResolveUDPAddr(network, address)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.ListenUDP(network, a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buffer := make([]byte, 65535)
		for {
			n, a, err := conn.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			conn.WriteToUDP(buffer[:n], a)
		}
	}()
	return conn
}
func TestSOCKSUDPEncryptedIPv4IPv6AndControlLifecycle(t *testing.T) {
	client, server := pair(t, true, true, true)
	server.Host.SetStreamHandler(UDPProtocol, server.handleUDP)
	origin4 := testUDPOrigin(t, "udp4", "127.0.0.1:0")
	origin6 := testUDPOrigin(t, "udp6", "[::1]:0")
	control, relay := socksUDPAssociate(t, client)
	defer control.Close()
	socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	for _, origin := range []*net.UDPConn{origin4, origin6} {
		packet, err := socksDatagram(origin.LocalAddr(), []byte("authenticated UDP round trip"))
		if err != nil {
			t.Fatal(err)
		}
		socket.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err = socket.WriteToUDP(packet, relay); err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, 1500)
		n, _, err := socket.ReadFromUDP(buffer)
		if err != nil {
			t.Fatal(err)
		}
		target, payload, err := parseSOCKSDatagram(buffer[:n])
		if err != nil || target != origin.LocalAddr().String() || string(payload) != "authenticated UDP round trip" {
			t.Fatalf("UDP response mismatch target%s payload%q err%v", target, payload, err)
		}
	}
	// Another loopback process cannot inject into the observed source port.
	impostor, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer impostor.Close()
	packet, _ := socksDatagram(origin4.LocalAddr(), []byte("unpaired source"))
	impostor.SetDeadline(time.Now().Add(100 * time.Millisecond))
	impostor.WriteToUDP(packet, relay)
	if _, _, err = impostor.ReadFromUDP(make([]byte, 1500)); err == nil {
		t.Fatal("unpaired source received response")
	}
	// FRAG is rejected and never forwarded.
	packet[2] = 1
	socket.SetDeadline(time.Now().Add(100 * time.Millisecond))
	socket.WriteToUDP(packet, relay)
	if _, _, err = socket.ReadFromUDP(make([]byte, 1500)); err == nil {
		t.Fatal("fragmented SOCKS packet accepted")
	}
	control.Close()
	time.Sleep(80 * time.Millisecond)
	packet[2] = 0
	socket.SetDeadline(time.Now().Add(150 * time.Millisecond))
	socket.WriteToUDP(packet, relay)
	if _, _, err = socket.ReadFromUDP(make([]byte, 1500)); err == nil {
		t.Fatal("UDP association survived TCP control EOF")
	}
}
func TestSOCKSUDPPrivateTargetsAndExitPermissions(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		exit, private, approved bool
	}{{"private", true, false, true}, {"no-consent", false, true, true}, {"no-approval", true, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := pair(t, tc.exit, tc.private, tc.approved)
			server.Host.SetStreamHandler(UDPProtocol, server.handleUDP)
			if tc.exit && tc.approved {
				control, relay := socksUDPAssociate(t, client)
				defer control.Close()
				origin := testUDPOrigin(t, "udp4", "127.0.0.1:0")
				socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
				if err != nil {
					t.Fatal(err)
				}
				defer socket.Close()
				packet, _ := socksDatagram(origin.LocalAddr(), []byte("private denied"))
				socket.SetDeadline(time.Now().Add(150 * time.Millisecond))
				socket.WriteToUDP(packet, relay)
				if _, _, err = socket.ReadFromUDP(make([]byte, 1500)); err == nil {
					t.Fatal("private UDP escaped")
				}
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if stream, err := client.openUDP(ctx, server.Host.ID().String()); err == nil {
					stream.Reset()
					t.Fatal("unapproved UDP exit accepted")
				}
			}
		})
	}
}
func TestSOCKSUDPDomainResolvesAtExit(t *testing.T) {
	client, server := pair(t, true, true, true)
	server.Host.SetStreamHandler(UDPProtocol, server.handleUDP)
	origin := testUDPOrigin(t, "udp", "[::]:0")
	control, relay := socksUDPAssociate(t, client)
	defer control.Close()
	socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	port := origin.LocalAddr().(*net.UDPAddr).Port
	host := "localhost"
	packet := append([]byte{0, 0, 0, 3, byte(len(host))}, []byte(host)...)
	packet = append(packet, byte(port>>8), byte(port))
	packet = append(packet, []byte("exit DNS")...)
	socket.SetDeadline(time.Now().Add(5 * time.Second))
	socket.WriteToUDP(packet, relay)
	buffer := make([]byte, 1500)
	n, _, err := socket.ReadFromUDP(buffer)
	if err != nil {
		t.Fatal(err)
	}
	target, payload, err := parseSOCKSDatagram(buffer[:n])
	_, returnedPort, _ := net.SplitHostPort(target)
	if err != nil || returnedPort != strconv.Itoa(port) || !bytes.Equal(payload, []byte("exit DNS")) {
		t.Fatal("exit domain UDP failed")
	}
}
func TestUDPMalformedFraming(t *testing.T) {
	for _, packet := range [][]byte{nil, {0, 0, 1, 1, 127, 0, 0, 1, 0, 53}, {0, 0, 0, 3, 0}, {0, 0, 0, 1, 127, 0, 0, 1, 0, 0}} {
		if _, _, err := parseSOCKSDatagram(packet); err == nil {
			t.Fatal("malformed datagram accepted")
		}
	}
	if _, err := readUDPFrame(bytes.NewReader([]byte{0, 1, 255, 255})); err == nil {
		t.Fatal("oversized UDPframe accepted")
	}
}
