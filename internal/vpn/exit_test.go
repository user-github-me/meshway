package vpn

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	"github.com/sagernet/gvisor/pkg/tcpip/transport/udp"
	"golang.org/x/net/dns/dnsmessage"
)

type testPacketClient struct {
	service *Service
	stack   *stack.Stack
	link    *channel.Endpoint
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func newTestPacketClient(t *testing.T, service *Service) *testPacketClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c := &testPacketClient{service: service, ctx: ctx, cancel: cancel, link: channel.New(256, 1280, "")}
	c.stack = stack.New(stack.Options{NetworkProtocols: []stack.NetworkProtocolFactory{ipv4.NewProtocolWithOptions(ipv4.Options{AllowExternalLoopbackTraffic: true}), ipv6.NewProtocolWithOptions(ipv6.Options{AllowExternalLoopbackTraffic: true})}, TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol}})
	if err := c.stack.CreateNIC(1, c.link); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{ClientIPv4, ClientIPv6} {
		ip := netip.MustParseAddr(address)
		number := tcpip.NetworkProtocolNumber(ipv4.ProtocolNumber)
		bits := 32
		var a tcpip.Address
		if ip.Is4() {
			a = tcpip.AddrFrom4(ip.As4())
		}
		if ip.Is6() {
			number = ipv6.ProtocolNumber
			bits = 128
			a = tcpip.AddrFrom16(ip.As16())
		}
		if err := c.stack.AddProtocolAddress(1, tcpip.ProtocolAddress{Protocol: number, AddressWithPrefix: tcpip.AddressWithPrefix{Address: a, PrefixLen: bits}}, stack.AddressProperties{}); err != nil {
			t.Fatal(err)
		}
	}
	c.stack.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}, {Destination: header.IPv6EmptySubnet, NIC: 1}})
	c.wg.Add(2)
	go func() {
		defer c.wg.Done()
		for {
			pkt := c.link.ReadContext(ctx)
			if pkt == nil {
				return
			}
			v := pkt.ToView()
			packet := append([]byte(nil), v.AsSlice()...)
			v.Release()
			pkt.DecRef()
			if err := service.WritePacket(packet); err != nil && ctx.Err() == nil {
				continue
			}
		}
	}()
	go func() {
		defer c.wg.Done()
		for {
			packet, err := service.ReadPacket()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				time.Sleep(10 * time.Millisecond)
				continue
			}
			pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(packet)})
			number := tcpip.NetworkProtocolNumber(ipv4.ProtocolNumber)
			if packet[0]>>4 == 6 {
				number = ipv6.ProtocolNumber
			}
			c.link.InjectInbound(number, pkt)
			pkt.DecRef()
		}
	}()
	t.Cleanup(func() {
		cancel()
		service.StopPacketVPN()
		c.link.Close()
		c.stack.Close()
		for _, ep := range c.stack.CleanupEndpoints() {
			ep.Abort()
		}
		c.wg.Wait()
		c.link.Drain()
	})
	return c
}
func (c *testPacketClient) tcp(ctx context.Context, target string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return nil, err
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return nil, err
	}
	number := tcpip.NetworkProtocolNumber(ipv4.ProtocolNumber)
	var a tcpip.Address
	if ip.Is4() {
		a = tcpip.AddrFrom4(ip.As4())
	} else {
		number = ipv6.ProtocolNumber
		a = tcpip.AddrFrom16(ip.As16())
	}
	return gonet.DialContextTCP(ctx, c.stack, tcpip.FullAddress{NIC: 1, Addr: a, Port: uint16(p)}, number)
}
func (c *testPacketClient) udp(target string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return nil, err
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return nil, err
	}
	number := tcpip.NetworkProtocolNumber(ipv4.ProtocolNumber)
	var a tcpip.Address
	if ip.Is4() {
		a = tcpip.AddrFrom4(ip.As4())
	} else {
		number = ipv6.ProtocolNumber
		a = tcpip.AddrFrom16(ip.As16())
	}
	return gonet.DialUDP(c.stack, nil, &tcpip.FullAddress{NIC: 1, Addr: a, Port: uint16(p)}, number)
}
func exitPair(t *testing.T, options Options, approved bool) (*Service, *Service) {
	t.Helper()
	client, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	server, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = client.Connect(ctx, peer.AddrInfo{ID: server.ID(), Addrs: server.Addrs()}); err != nil {
		t.Fatal(err)
	}
	left, err := New(client, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { left.Close() })
	if approved {
		options.AllowedPeers = []string{client.ID().String()}
	}
	right, err := New(server, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { right.Close() })
	return left, right
}

func TestInternetVPNEncryptedTCPUDPAndDNS(t *testing.T) {
	var bytesSent, bytesReceived atomic.Uint64
	dns, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dns.Close()
	go func() {
		buf := make([]byte, 2000)
		for {
			n, a, e := dns.ReadFrom(buf)
			if e != nil {
				return
			}
			answer := append([]byte(nil), buf[:n]...)
			answer[2] |= 0x80
			dns.WriteTo(answer, a)
		}
	}()
	left, right := exitPair(t, Options{Exit: true, AllowPrivateTargets: true, DNSResolver: dns.LocalAddr().String(), AccountBytes: func(sent, received uint64) { bytesSent.Add(sent); bytesReceived.Add(received) }}, true)
	if err = left.StartPacketVPN(right.host.ID().String()); err != nil {
		t.Fatal(err)
	}
	client := newTestPacketClient(t, left)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "encrypted userspace Internet TCP works")
	}))
	defer origin.Close()
	transport := &http.Transport{DialContext: func(ctx context.Context, network, target string) (net.Conn, error) { return client.tcp(ctx, target) }}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := httpClient.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "encrypted userspace Internet TCP works" {
		t.Fatalf("TCP response mismatch: %q %v", body, err)
	}
	for _, address := range []string{net.JoinHostPort(DNSIPv4, "53"), net.JoinHostPort(DNSIPv6, "53")} {
		t.Run(address, func(t *testing.T) {
			conn, err := client.udp(address)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			question := []byte{0x42, 0x11, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'o', 'r', 'g', 0, 0, 1, 0, 1}
			if _, err = conn.Write(question); err != nil {
				t.Fatal(err)
			}
			answer := make([]byte, 1500)
			n, err := conn.Read(answer)
			if err != nil {
				t.Fatal(err)
			}
			if n != len(question) || answer[0] != 0x42 || answer[2]&0x80 == 0 {
				t.Fatal("remote tunneled DNS response invalid")
			}
		})
	}
	echo, err := net.ListenPacket("udp6", "[::1]:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		p := make([]byte, 1500)
		for {
			n, a, e := echo.ReadFrom(p)
			if e != nil {
				return
			}
			echo.WriteTo(p[:n], a)
		}
	}()
	conn, err := client.udp(echo.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	conn.Write([]byte("IPv6 datagram"))
	answer := make([]byte, 1500)
	n, err := conn.Read(answer)
	if err != nil || string(answer[:n]) != "IPv6 datagram" {
		t.Fatalf("IPv6 UDP failed %v %q", err, answer[:n])
	}
	if bytesSent.Load() == 0 || bytesReceived.Load() == 0 {
		t.Fatal("packet byte accounting did not update")
	}
}

func TestInternetVPNAuthorizationAndExplicitExit(t *testing.T) {
	for _, tc := range []struct {
		name           string
		exit, approved bool
	}{{"disabled", false, true}, {"unauthorized", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			left, right := exitPair(t, Options{Exit: tc.exit}, tc.approved)
			if err := left.StartPacketVPN(right.host.ID().String()); err == nil {
				t.Fatal("unauthorized or disabled exit accepted packet session")
			}
			if left.Status().Active {
				t.Fatal("failed session active")
			}
		})
	}
}
func TestInternetVPNPrivateDNSAndSourcesPolicy(t *testing.T) {
	for _, resolver := range []string{"localhost:53", "127.0.0.1:53", "192.168.1.1:53", "[::1]:53"} {
		t.Run(resolver, func(t *testing.T) {
			left, _ := exitPair(t, Options{}, true)
			if _, err := newExitService(left.host, Options{DNSResolver: resolver}); err == nil {
				t.Fatal("private or hostname DNS resolver accepted")
			}
		})
	}
	options := Options{MTU: 1280, MaxFlowsPerSession: 4, DNSResolver: "1.1.1.1:53"}
	g, err := newInternetStack(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	for _, dst := range []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("192.168.0.1"), netip.MustParseAddr("100.64.0.1"), netip.MustParseAddr("fd00::1")} {
		a := tcpip.AddrFrom16(dst.As16())
		if dst.Is4() {
			a = tcpip.AddrFrom4(dst.As4())
		}
		if _, err := g.target(stack.TransportEndpointID{LocalAddress: a, LocalPort: 80}); err == nil {
			t.Fatalf("private destination %s allowed", dst)
		}
	}
	packet := ipPacket(4, "bad source")
	copy(packet[12:16], []byte{192, 0, 2, 2})
	g.Inject(packet)
	if g.link.NumQueued() != 0 {
		t.Fatal("spoofed source produced reply")
	}
}
func TestInternetVPNTCPIPv6(t *testing.T) {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "IPv6 TCP") }))
	origin.Listener = listener
	origin.Start()
	defer origin.Close()
	left, right := exitPair(t, Options{Exit: true, AllowPrivateTargets: true}, true)
	if err = left.StartPacketVPN(right.host.ID().String()); err != nil {
		t.Fatal(err)
	}
	client := newTestPacketClient(t, left)
	transport := &http.Transport{DialContext: func(ctx context.Context, network, target string) (net.Conn, error) { return client.tcp(ctx, target) }}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if string(body) != "IPv6 TCP" {
		t.Fatal("IPv6 TCP failed")
	}
}

// This opt-in test sends real UDP DNS and HTTPS over synthetic kernel-shaped
// packets and encrypted libp2p peers. It never changes host interfaces or routes.
func TestInternetVPNPublicHTTPSAndDNS(t *testing.T) {
	if os.Getenv("MESHWAY_INTERNET_VPN_LAB") != "1" {
		t.Skip("opt-in external packet Internet lab")
	}
	left, right := exitPair(t, Options{Exit: true}, true)
	if err := left.StartPacketVPN(right.host.ID().String()); err != nil {
		t.Fatal(err)
	}
	client := newTestPacketClient(t, left)
	dns, err := client.udp(net.JoinHostPort(DNSIPv4, "53"))
	if err != nil {
		t.Fatal(err)
	}
	defer dns.Close()
	dns.SetDeadline(time.Now().Add(15 * time.Second))
	question := []byte{0x42, 0x21, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1}
	dns.Write(question)
	answer := make([]byte, 1500)
	n, err := dns.Read(answer)
	if err != nil {
		t.Fatal(err)
	}
	if n < 12 || answer[0] != 0x42 || answer[2]&0x80 == 0 || binary.BigEndian.Uint16(answer[6:8]) == 0 {
		t.Fatal("real public DNS did not answer")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var message dnsmessage.Message
	if err = message.Unpack(answer[:n]); err != nil {
		t.Fatal(err)
	}
	var address netip.Addr
	for _, resource := range message.Answers {
		if a, ok := resource.Body.(*dnsmessage.AResource); ok {
			address = netip.AddrFrom4(a.A)
			break
		}
	}
	if !address.IsValid() {
		t.Fatal("remote DNS response did not contain an IPv4 address")
	}
	target := net.JoinHostPort(address.String(), "443")
	t.Log("remote DNS supplied public destination", target)
	if !publicDestination(address) {
		// Some development hosts run a fake-IP VPN. Preserve it and use a
		// caller-supplied literal address for the real HTTPS part of this lab.
		literal := os.Getenv("MESHWAY_LAB_HTTPS_IP")
		if literal == "" {
			t.Fatal("underlay DNS returned a reserved/fake address; set MESHWAY_LAB_HTTPS_IP to a real example.com address")
		}
		address, err = netip.ParseAddr(literal)
		if err != nil || !publicDestination(address) {
			t.Fatal("HTTPS lab IP must be a public literal address")
		}
		target = net.JoinHostPort(address.String(), "443")
	}
	probe, probeErr := net.DialTimeout("tcp", target, 5*time.Second)
	if probeErr != nil {
		t.Fatalf("host underlay cannot dial the real public destination: %v", probeErr)
	}
	probe.Close()
	raw, err := client.tcp(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	secure := tls.Client(raw, &tls.Config{ServerName: "example.com", MinVersion: tls.VersionTLS12})
	secure.SetDeadline(time.Now().Add(20 * time.Second))
	if err = secure.HandshakeContext(ctx); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(secure, "GET / HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n")
	response, err := io.ReadAll(secure)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(response, []byte("Example Domain")) || !strings.HasPrefix(string(response), "HTTP/1.1 200") {
		t.Fatalf("public HTTPS unexpected response %s", string(response[:min(128, len(response))]))
	}
	t.Log("real public UDP DNS and verified TLS HTTPS passed through IP packets and encrypted peer exit")
}

func TestInternetVPNMultiplePeersAndSessionLimits(t *testing.T) {
	left, right := exitPair(t, Options{Exit: true, MaxExitSessions: 1}, true)
	if err := left.StartPacketVPN(right.host.ID().String()); err != nil {
		t.Fatal(err)
	}
	secondHost, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer secondHost.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = secondHost.Connect(ctx, peer.AddrInfo{ID: right.host.ID(), Addrs: right.host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	second, err := New(secondHost, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err = second.StartPacketVPN(right.host.ID().String()); err == nil {
		t.Fatal("bounded session limit was bypassed")
	}
	if !left.Status().Active {
		t.Fatal("other peer's rejection disrupted established session")
	}
	done := make(chan struct{})
	go func() { left.StopPacketVPN(); right.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("packet VPN bounded shutdown blocked")
	}
}

func TestInternetVPNReconnectRetainsPacketDevice(t *testing.T) {
	left, right := exitPair(t, Options{Exit: true, AllowPrivateTargets: true}, true)
	if err := left.StartPacketVPN(right.host.ID().String()); err != nil {
		t.Fatal(err)
	}
	client := newTestPacketClient(t, left)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "reconnected packet VPN") }))
	defer origin.Close()
	left.exit.mu.Lock()
	original := left.exit.client
	left.exit.mu.Unlock()
	// Interrupt only the encrypted packet stream while preserving the peers and
	// local native packet device. The core re-establishes it itself.
	right.exit.mu.Lock()
	for _, session := range right.exit.inbound {
		session.stream.Reset()
	}
	right.exit.mu.Unlock()
	deadline := time.Now().Add(8 * time.Second)
	sawDisconnect := false
	for time.Now().Before(deadline) {
		status := left.Status()
		if !status.Active {
			sawDisconnect = true
		}
		if sawDisconnect && status.Active {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !sawDisconnect || !left.Status().Active {
		t.Fatal("packet VPN did not reconnect automatically")
	}
	left.exit.mu.Lock()
	retained := left.exit.client
	left.exit.mu.Unlock()
	if retained != original {
		t.Fatal("reconnect replaced native packet device")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, network, target string) (net.Conn, error) { return client.tcp(ctx, target) }}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if string(body) != "reconnected packet VPN" {
		t.Fatal("reconnected device cannot forward actual traffic")
	}
}
