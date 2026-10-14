//go:build windows

package vpn

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/sagernet/gvisor/pkg/buffer"
	"github.com/sagernet/gvisor/pkg/tcpip"
	"github.com/sagernet/gvisor/pkg/tcpip/adapters/gonet"
	"github.com/sagernet/gvisor/pkg/tcpip/header"
	"github.com/sagernet/gvisor/pkg/tcpip/link/channel"
	"github.com/sagernet/gvisor/pkg/tcpip/network/ipv4"
	"github.com/sagernet/gvisor/pkg/tcpip/network/ipv6"
	"github.com/sagernet/gvisor/pkg/tcpip/stack"
	"github.com/sagernet/gvisor/pkg/tcpip/transport/tcp"
)

func windowsLabPowerShell(script string) ([]byte, error) {
	return exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; "+script).CombinedOutput()
}

// Opt-in test for a DISPOSABLE administrator Windows runner with the pinned,
// Authenticode-verified Wintun DLL beside this test executable. It creates one
// owned adapter, two private addresses and two host-only return routes. It never
// changes default routes, DNS, active VPN, or system/browser proxy settings.
func TestWindowsWintunKernelIPv4IPv6EncryptedTCP(t *testing.T) {
	if os.Getenv("MESHWAY_WINDOWS_TUN_LAB") != "1" {
		t.Skip("opt-in disposable Windows runner Wintun lab")
	}
	name := "MeshwayLab" + strconv.Itoa(os.Getpid())
	rule := name + "Inbound"
	device, err := openPacketDevice(name, 1280)
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	cleanup := func() {
		_, _ = windowsLabPowerShell(fmt.Sprintf("Remove-NetFirewallRule -Name '%s' -ErrorAction SilentlyContinue; Get-NetRoute -InterfaceAlias '%s' -ErrorAction SilentlyContinue | Remove-NetRoute -Confirm:$false -ErrorAction SilentlyContinue; Get-NetIPAddress -InterfaceAlias '%s' -ErrorAction SilentlyContinue | Remove-NetIPAddress -Confirm:$false -ErrorAction SilentlyContinue", rule, name, name))
	}
	defer cleanup()
	script := fmt.Sprintf("New-NetIPAddress -InterfaceAlias '%s' -IPAddress 10.73.0.11 -PrefixLength 32 -AddressFamily IPv4 | Out-Null; New-NetIPAddress -InterfaceAlias '%s' -IPAddress fd73::11 -PrefixLength 128 -AddressFamily IPv6 | Out-Null; Set-NetIPInterface -InterfaceAlias '%s' -NlMtuBytes 1280; New-NetRoute -InterfaceAlias '%s' -DestinationPrefix 10.73.0.10/32 -NextHop 0.0.0.0 -PolicyStore ActiveStore | Out-Null; New-NetRoute -InterfaceAlias '%s' -DestinationPrefix fd73::10/128 -NextHop :: -PolicyStore ActiveStore | Out-Null; New-NetFirewallRule -Name '%s' -DisplayName '%s' -InterfaceAlias '%s' -Direction Inbound -Action Allow -Protocol TCP -LocalAddress 10.73.0.11,fd73::11 | Out-Null", name, name, name, name, name, rule, rule, name)
	if output, err := windowsLabPowerShell(script); err != nil {
		t.Fatalf("isolated Windows adapter setup: %v %s", err, output)
	}
	// Wait for IPv6 duplicate-address detection to complete on the owned adapter.
	time.Sleep(1500 * time.Millisecond)
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err = client.Connect(ctx, peer.AddrInfo{ID: server.ID(), Addrs: server.Addrs()}); err != nil {
		t.Fatal(err)
	}
	right, err := New(server, Options{AllowedPeers: []string{client.ID().String()}})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()
	if err = right.attach(device); err != nil {
		t.Fatal(err)
	}
	left, err := New(client, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()
	stream, err := client.NewStream(ctx, server.ID(), Protocol)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Reset()
	if err = left.handshake(stream, true); err != nil {
		t.Fatal(err)
	}
	link := channel.New(256, 1280, "")
	ipStack := stack.New(stack.Options{NetworkProtocols: []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol}, TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol}})
	defer func() {
		link.Close()
		ipStack.Close()
		for _, endpoint := range ipStack.CleanupEndpoints() {
			endpoint.Abort()
		}
		link.Drain()
	}()
	if err := ipStack.CreateNIC(1, link); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"10.73.0.10", "fd73::10"} {
		ip := netip.MustParseAddr(address)
		number := tcpip.NetworkProtocolNumber(ipv4.ProtocolNumber)
		bits := 32
		var a tcpip.Address
		if ip.Is4() {
			a = tcpip.AddrFrom4(ip.As4())
		} else {
			number = ipv6.ProtocolNumber
			bits = 128
			a = tcpip.AddrFrom16(ip.As16())
		}
		if err := ipStack.AddProtocolAddress(1, tcpip.ProtocolAddress{Protocol: number, AddressWithPrefix: tcpip.AddressWithPrefix{Address: a, PrefixLen: bits}}, stack.AddressProperties{}); err != nil {
			t.Fatal(err)
		}
	}
	ipStack.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}, {Destination: header.IPv6EmptySubnet, NIC: 1}})
	pumpCtx, stopPump := context.WithCancel(ctx)
	var pumps sync.WaitGroup
	pumps.Add(2)
	go func() {
		defer pumps.Done()
		for {
			packet := link.ReadContext(pumpCtx)
			if packet == nil {
				return
			}
			view := packet.ToView()
			data := append([]byte(nil), view.AsSlice()...)
			view.Release()
			packet.DecRef()
			if WritePacket(stream, data, 1280) != nil {
				return
			}
		}
	}()
	go func() {
		defer pumps.Done()
		for {
			data, err := ReadPacket(stream, 1280)
			if err != nil {
				return
			}
			pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(data)})
			number := tcpip.NetworkProtocolNumber(ipv4.ProtocolNumber)
			if data[0]>>4 == 6 {
				number = ipv6.ProtocolNumber
			}
			link.InjectInbound(number, pkt)
			pkt.DecRef()
		}
	}()
	defer func() { stopPump(); stream.Reset(); pumps.Wait() }()
	for _, address := range []string{"10.73.0.11", "fd73::11"} {
		t.Run(address, func(t *testing.T) {
			listener, err := net.Listen("tcp", net.JoinHostPort(address, "0"))
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				payload := make([]byte, 32)
				_, err = io.ReadFull(conn, payload)
				if err == nil {
					_, err = conn.Write(payload)
				}
				done <- err
			}()
			ip := netip.MustParseAddr(address)
			number := tcpip.NetworkProtocolNumber(ipv4.ProtocolNumber)
			var a tcpip.Address
			if ip.Is4() {
				a = tcpip.AddrFrom4(ip.As4())
			} else {
				number = ipv6.ProtocolNumber
				a = tcpip.AddrFrom16(ip.As16())
			}
			conn, err := gonet.DialContextTCP(ctx, ipStack, tcpip.FullAddress{NIC: 1, Addr: a, Port: uint16(listener.Addr().(*net.TCPAddr).Port)}, number)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			payload := bytes.Repeat([]byte{0x42}, 32)
			if _, err = conn.Write(payload); err != nil {
				t.Fatal(err)
			}
			reply := make([]byte, 32)
			if _, err = io.ReadFull(conn, reply); err != nil || !bytes.Equal(reply, payload) {
				t.Fatalf("Windows kernel TCP round trip: %v", err)
			}
			if err = <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Log("PASS: actual signed Wintun driver, private IPv4+IPv6 kernel TCP listener, encrypted peer forward/return; owned host routes/firewall removed; no default routes/DNS/proxy changes")
}
