//go:build linux && !android

package vpn

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/sys/unix"
)

type linuxExitInfo struct {
	PeerID  string `json:"peer_id"`
	Address string `json:"address"`
}

// Gateway is a separate disposable Docker network namespace. All test routing
// changes occur in the client namespace; the development host stays untouched.
func TestLinuxInternetVPNGateway(t *testing.T) {
	if os.Getenv("MESHWAY_LINUX_EXIT_GATEWAY") != "1" {
		t.Skip("opt-in isolated gateway fixture")
	}
	lab := os.Getenv("MESHWAY_LAB_DIR")
	gateway, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/14001"))
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	dns, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dns.Close()
	go func() {
		p := make([]byte, 1500)
		for {
			n, a, err := dns.ReadFrom(p)
			if err != nil {
				return
			}
			var q dnsmessage.Message
			if q.Unpack(p[:n]) != nil {
				continue
			}
			q.Header.Response = true
			q.Header.Authoritative = true
			if len(q.Questions) > 0 {
				q.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 1}, Body: &dnsmessage.AResource{A: [4]byte{10, 99, 0, 2}}}}
			}
			answer, err := q.Pack()
			if err == nil {
				dns.WriteTo(answer, a)
			}
		}
	}()
	resolver := dns.LocalAddr().String()
	if os.Getenv("MESHWAY_LAB_INTERNET") == "1" {
		resolver = "1.1.1.1:53"
	}
	s, err := New(gateway, Options{Exit: true, AllowPrivateTargets: true, DNSResolver: resolver, Authorize: func(ctx context.Context, p peer.ID) bool {
		body, err := os.ReadFile(filepath.Join(lab, "client-peer"))
		return err == nil && strings.TrimSpace(string(body)) == p.String()
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	servers := []*http.Server{}
	for _, address := range []string{"10.99.0.2:18080", "[fd99::2]:18080"} {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "real Linux kernel IP exit works") }), ReadHeaderTimeout: time.Second}
		servers = append(servers, srv)
		go srv.Serve(listener)
		defer srv.Close()
	}
	body, _ := json.Marshal(linuxExitInfo{PeerID: gateway.ID().String(), Address: "/ip4/10.88.77.2/tcp/14001/p2p/" + gateway.ID().String()})
	if err = os.WriteFile(filepath.Join(lab, "gateway.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(100 * time.Second)
	stopped := false
	for time.Now().Before(deadline) {
		if _, err = os.Stat(filepath.Join(lab, "stop-exit")); err == nil && !stopped {
			s.Close()
			stopped = true
			os.WriteFile(filepath.Join(lab, "exit-stopped"), []byte("ok"), 0600)
		}
		if _, err = os.Stat(filepath.Join(lab, "done")); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("client never completed isolated lab")
}

func labIP(t *testing.T, args ...string) {
	t.Helper()
	output, err := exec.Command("ip", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("ip failed: %v %s", err, output)
	}
}
func labGet(t *testing.T, url string) string {
	t.Helper()
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	c := &http.Client{Transport: transport, Timeout: 8 * time.Second}
	r, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestLinuxKernelInternetVPN(t *testing.T) {
	if os.Getenv("MESHWAY_LINUX_IP_EXIT_LAB") != "1" {
		t.Skip("opt-in disposable Linux namespace only")
	}
	lab := os.Getenv("MESHWAY_LAB_DIR")
	defer os.WriteFile(filepath.Join(lab, "done"), []byte("ok"), 0600)
	body, err := os.ReadFile(filepath.Join(lab, "gateway.json"))
	if err != nil {
		t.Fatal(err)
	}
	var gateway linuxExitInfo
	if err = json.Unmarshal(body, &gateway); err != nil {
		t.Fatal(err)
	}
	client, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	os.WriteFile(filepath.Join(lab, "client-peer"), []byte(client.ID().String()), 0600)
	info, err := peer.AddrInfoFromString(gateway.Address)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = client.Connect(ctx, *info); err != nil {
		t.Fatal(err)
	}
	s, err := New(client, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fd, err := OpenDevice("meshwaylab1")
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if err = s.AttachExitFD(fd, gateway.PeerID); err != nil {
		t.Fatal(err)
	}
	// The connected underlay remains reachable via eth0 while every other
	// public IPv4/IPv6 destination and DNS uses this namespace's real TUN.
	labIP(t, "link", "set", "meshwaylab1", "mtu", "1280", "up")
	labIP(t, "addr", "add", "10.77.0.2/32", "dev", "meshwaylab1")
	labIP(t, "-6", "addr", "add", "fd77::2/128", "dev", "meshwaylab1", "nodad")
	labIP(t, "route", "add", "10.88.77.2/32", "dev", "eth0")
	labIP(t, "route", "add", "0.0.0.0/1", "dev", "meshwaylab1")
	labIP(t, "route", "add", "128.0.0.0/1", "dev", "meshwaylab1")
	labIP(t, "-6", "route", "add", "::/1", "dev", "meshwaylab1")
	labIP(t, "-6", "route", "add", "8000::/1", "dev", "meshwaylab1")
	if got := labGet(t, "http://10.99.0.2:18080/"); got != "real Linux kernel IP exit works" {
		t.Fatal("kernel IPv4 response mismatch")
	}
	if got := labGet(t, "http://[fd99::2]:18080/"); got != "real Linux kernel IP exit works" {
		t.Fatal("kernel IPv6 response mismatch")
	}
	resolver := &net.Resolver{PreferGo: true}
	domain := "meshway.test"
	if os.Getenv("MESHWAY_LAB_INTERNET") == "1" {
		domain = "example.com"
	}
	dnsCtx, dnsCancel := context.WithTimeout(context.Background(), 8*time.Second)
	addresses, err := resolver.LookupNetIP(dnsCtx, "ip4", domain)
	dnsCancel()
	if err != nil || len(addresses) == 0 {
		t.Fatalf("kernel DNS failed: %v", err)
	}
	if os.Getenv("MESHWAY_LAB_INTERNET") == "1" {
		raw, err := net.DialTimeout("tcp", net.JoinHostPort(addresses[0].String(), "443"), 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		secure := tls.Client(raw, &tls.Config{ServerName: "example.com", MinVersion: tls.VersionTLS12})
		secure.SetDeadline(time.Now().Add(15 * time.Second))
		if err = secure.Handshake(); err != nil {
			t.Fatal(err)
		}
		fmt.Fprint(secure, "GET / HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n")
		response, err := io.ReadAll(secure)
		if err != nil || !strings.Contains(string(response), "Example Domain") {
			t.Fatalf("real kernel HTTPS failed: %v", err)
		}
	}
	if err = os.WriteFile(filepath.Join(lab, "stop-exit"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if _, err = os.Stat(filepath.Join(lab, "exit-stopped")); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for i := 0; i < 100 && s.Status().Active; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if s.Status().Active {
		t.Fatal("exit did not report disconnect")
	}
	if _, err = net.InterfaceByName("meshwaylab1"); err != nil {
		t.Fatal("disconnection removed fail-closed TUN")
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	leakClient := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	if r, err := leakClient.Get("http://10.99.0.2:18080/"); err == nil {
		r.Body.Close()
		t.Fatal("TCP escaped after exit disconnected")
	}
	dnsCtx, dnsCancel = context.WithTimeout(context.Background(), 2*time.Second)
	_, err = resolver.LookupNetIP(dnsCtx, "ip4", domain)
	dnsCancel()
	if err == nil {
		t.Fatal("DNS escaped after exit disconnected")
	}
	t.Log("PASS: actual Linux TUN, kernel TCP IPv4+IPv6, tunneled kernel DNS, disconnected interface retained, TCP+DNS fail closed; development host network unchanged")
}
