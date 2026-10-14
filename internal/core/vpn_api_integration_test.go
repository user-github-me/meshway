package core

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
	"strconv"
	"sync"
	"testing"
	"time"

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
	"github.com/user-github-me/meshway/internal/config"
	"github.com/user-github-me/meshway/internal/vpn"
)

// This exercises the actual HTTP packet-bridge contract used by a native
// helper. The client IP stack stays in memory; endpoints and peers are all
// loopback. No kernel interface, default route, DNS or proxy is changed.
func TestPacketHelperHTTPBridgeCarriesTLSAndRemoteDNS(t *testing.T) {
	dns, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dns.Close()
	go func() {
		data := make([]byte, 1500)
		for {
			n, from, err := dns.ReadFrom(data)
			if err != nil {
				return
			}
			if n < 12 {
				continue
			}
			answer := append([]byte(nil), data[:n]...)
			answer[2] |= 0x80
			_, _ = dns.WriteTo(answer, from)
		}
	}()
	clientConfig := testConfig(t)
	clientID, err := IdentityID(clientConfig.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	serverConfig := testConfig(t)
	serverConfig.Exit, serverConfig.AllowPrivateTargets = true, true
	serverConfig.AllowedPeers = []string{clientID}
	serverConfig.VPN = &config.VPN{MTU: 1280, DNSResolver: dns.LocalAddr().String()}
	server := startNode(t, serverConfig)
	client := startNode(t, clientConfig)
	connect(t, client, server)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	apiHTTP := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 4 * time.Second}
	defer apiHTTP.CloseIdleConnections()
	request := func(requestCtx context.Context, path string, body []byte, content string) ([]byte, int, error) {
		r, err := http.NewRequestWithContext(requestCtx, "POST", "http://"+client.Status().APIListen+path, bytes.NewReader(body))
		if err != nil {
			return nil, 0, err
		}
		r.Header.Set("Authorization", "Bearer "+client.Config.APIToken)
		if content != "" {
			r.Header.Set("Content-Type", content)
		}
		response, err := apiHTTP.Do(r)
		if err != nil {
			return nil, 0, err
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, 9001))
		if response.StatusCode >= 300 {
			return nil, response.StatusCode, fmt.Errorf("packet API HTTP %d", response.StatusCode)
		}
		return data, response.StatusCode, err
	}
	payload := []byte(fmt.Sprintf(`{"mode":"exit","peer_id":%q}`, server.Host.ID().String()))
	if _, _, err = request(ctx, "/v1/vpn/start", payload, "application/json"); err != nil {
		t.Fatal(err)
	}
	link := channel.New(256, 1280, "")
	ipStack := stack.New(stack.Options{NetworkProtocols: []stack.NetworkProtocolFactory{ipv4.NewProtocolWithOptions(ipv4.Options{AllowExternalLoopbackTraffic: true}), ipv6.NewProtocolWithOptions(ipv6.Options{AllowExternalLoopbackTraffic: true})}, TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol}})
	if err := ipStack.CreateNIC(1, link); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{vpn.ClientIPv4, vpn.ClientIPv6} {
		ip := netip.MustParseAddr(value)
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
		if err := ipStack.AddProtocolAddress(1, tcpip.ProtocolAddress{Protocol: number, AddressWithPrefix: tcpip.AddressWithPrefix{Address: a, PrefixLen: bits}}, stack.AddressProperties{}); err != nil {
			t.Fatal(err)
		}
	}
	ipStack.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}, {Destination: header.IPv6EmptySubnet, NIC: 1}})
	var workers sync.WaitGroup
	failures := make(chan error, 2)
	workers.Add(2)
	go func() {
		defer workers.Done()
		for {
			packet := link.ReadContext(ctx)
			if packet == nil {
				return
			}
			view := packet.ToView()
			data := append([]byte(nil), view.AsSlice()...)
			view.Release()
			packet.DecRef()
			if _, _, err := request(ctx, "/v1/vpn/write", data, "application/octet-stream"); err != nil {
				if ctx.Err() == nil {
					failures <- err
				}
				return
			}
		}
	}()
	go func() {
		defer workers.Done()
		for {
			packet, status, err := request(ctx, "/v1/vpn/read", nil, "")
			if err != nil {
				if ctx.Err() == nil {
					failures <- err
				}
				return
			}
			if status == 204 {
				continue
			}
			p := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(packet)})
			number := tcpip.NetworkProtocolNumber(ipv4.ProtocolNumber)
			if packet[0]>>4 == 6 {
				number = ipv6.ProtocolNumber
			}
			link.InjectInbound(number, p)
			p.DecRef()
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = client.StopPacketVPN()
		link.Close()
		ipStack.Close()
		for _, endpoint := range ipStack.CleanupEndpoints() {
			endpoint.Abort()
		}
		workers.Wait()
		link.Drain()
	})
	dialTCP := func(ctx context.Context, network, target string) (net.Conn, error) {
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
		var address tcpip.Address
		if ip.Is4() {
			address = tcpip.AddrFrom4(ip.As4())
		} else {
			number = ipv6.ProtocolNumber
			address = tcpip.AddrFrom16(ip.As16())
		}
		return gonet.DialContextTCP(ctx, ipStack, tcpip.FullAddress{NIC: 1, Addr: address, Port: uint16(p)}, number)
	}
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "TLS through authenticated HTTP packet queue")
	}))
	defer origin.Close()
	tlsTransport := &http.Transport{DialContext: dialTCP, TLSClientConfig: &tls.Config{RootCAs: origin.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}}
	defer tlsTransport.CloseIdleConnections()
	tlsClient := &http.Client{Transport: tlsTransport, Timeout: 5 * time.Second}
	response, err := tlsClient.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(data) != "TLS through authenticated HTTP packet queue" {
		t.Fatal("packet bridge TLS response failed")
	}
	for _, value := range []string{vpn.DNSIPv4, vpn.DNSIPv6} {
		ip := netip.MustParseAddr(value)
		number := tcpip.NetworkProtocolNumber(ipv4.ProtocolNumber)
		var address tcpip.Address
		if ip.Is4() {
			address = tcpip.AddrFrom4(ip.As4())
		} else {
			number = ipv6.ProtocolNumber
			address = tcpip.AddrFrom16(ip.As16())
		}
		conn, err := gonet.DialUDP(ipStack, nil, &tcpip.FullAddress{NIC: 1, Addr: address, Port: 53}, number)
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		question := []byte{0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'o', 'r', 'g', 0, 0, 1, 0, 1}
		if _, err = conn.Write(question); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		answer := make([]byte, 1500)
		n, err := conn.Read(answer)
		conn.Close()
		if err != nil || n != len(question) || binary.BigEndian.Uint16(answer[:2]) != 0x1234 || answer[2]&0x80 == 0 {
			t.Fatalf("remote DNS %s through packet API failed: %v", ip, err)
		}
	}
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
	cancel()
	workers.Wait()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	if _, _, err := request(stopCtx, "/v1/vpn/stop", []byte("{}"), "application/json"); err != nil {
		t.Fatal(err)
	}
	if client.vpn.Status().Active || len(client.sessions) != 0 {
		t.Fatal("HTTP bridge stop retained the packet session or admission slots")
	}
	t.Log("PASS real authenticated HTTP packet start/read/write/stop, loopback encrypted peer exit, trusted TLS and exit-side IPv4/IPv6 DNS; host networking unchanged")
}
