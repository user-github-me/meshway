package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	"github.com/user-github-me/meshway/internal/account"
	"github.com/user-github-me/meshway/internal/config"
)

func testConfig(t *testing.T) config.Config {
	t.Helper()
	c, err := config.Default(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c.ListenAddresses = []string{"/ip4/127.0.0.1/tcp/0"}
	c.SOCKSListen = "127.0.0.1:0"
	c.APIListen = "127.0.0.1:0"
	return c
}
func startNode(t *testing.T, c config.Config) *Node {
	t.Helper()
	n, err := Start(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.Stop() })
	return n
}
func connect(t *testing.T, a, b *Node) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.Connect(ctx, b.Status().Addresses[0]); err != nil {
		t.Fatal(err)
	}
}

func pair(t *testing.T, exit, private, authorized bool) (*Node, *Node) {
	t.Helper()
	cc := testConfig(t)
	id, err := IdentityID(cc.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	sc := testConfig(t)
	sc.Exit = exit
	sc.AllowPrivateTargets = private
	if authorized {
		sc.AllowedPeers = []string{id}
	}
	server := startNode(t, sc)
	cc.UpstreamPeer = server.Host.ID().String()
	client := startNode(t, cc)
	connect(t, client, server)
	return client, server
}

func socksDial(t *testing.T, address, target string) (net.Conn, byte) {
	t.Helper()
	c, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err = c.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	var greet [2]byte
	if _, err = io.ReadFull(c, greet[:]); err != nil {
		t.Fatal(err)
	}
	if greet != [2]byte{5, 0} {
		t.Fatalf("bad greeting %v", greet)
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := strconv.Atoi(port)
	request := append([]byte{5, 1, 0, 3, byte(len(host))}, []byte(host)...)
	request = append(request, byte(p>>8), byte(p))
	if _, err = c.Write(request); err != nil {
		t.Fatal(err)
	}
	var reply [10]byte
	if _, err = io.ReadFull(c, reply[:]); err != nil {
		t.Fatal(err)
	}
	return c, reply[1]
}

func TestEncryptedSOCKSHTTP(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/encrypted" {
			t.Errorf("path %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, "through encrypted peers")
	}))
	defer origin.Close()
	client, server := pair(t, true, true, true)
	c, code := socksDial(t, client.Status().SOCKSListen, strings.TrimPrefix(origin.URL, "http://"))
	defer c.Close()
	if code != 0 {
		t.Fatalf("SOCKS denied %d", code)
	}
	_, _ = io.WriteString(c, "GET /encrypted HTTP/1.0\r\nHost: test\r\n\r\n")
	b, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("through encrypted peers")) {
		t.Fatalf("response %q", b)
	}
	conns := server.Host.Network().ConnsToPeer(client.Host.ID())
	if len(conns) == 0 || conns[0].ConnState().Security == "" {
		t.Fatal("missing authenticated encryption")
	}
	if client.Host.ID() == server.Host.ID() {
		t.Fatal("same identity")
	}
}

func TestGatewayDenials(t *testing.T) {
	for _, tc := range []struct {
		name                string
		exit, private, auth bool
	}{{"private", true, false, true}, {"disabled-exit", false, true, true}, {"unapproved-peer", true, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := pair(t, tc.exit, tc.private, tc.auth)
			c, code := socksDial(t, client.Status().SOCKSListen, "127.0.0.1:80")
			c.Close()
			if code == 0 {
				t.Fatal("forbidden request succeeded")
			}
		})
	}
}

func TestNamedServiceForward(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "explicit private service") }))
	defer origin.Close()
	cc := testConfig(t)
	id, _ := IdentityID(cc.StateDir)
	sc := testConfig(t)
	sc.AllowedPeers = []string{id}
	sc.Services = []config.Service{{Name: "web", Target: strings.TrimPrefix(origin.URL, "http://")}}
	server := startNode(t, sc)
	cc.UpstreamPeer = server.Host.ID().String()
	cc.Forwards = []config.Forward{{Name: "web", Listen: "127.0.0.1:0", Service: "web"}}
	client := startNode(t, cc)
	connect(t, client, server)
	var address string
	client.mu.Lock()
	address = client.listeners[1].Addr().String()
	client.mu.Unlock()
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get("http://" + address)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "explicit private service" {
		t.Fatalf("body %q", b)
	}
	if server.Config.Exit || server.Config.AllowPrivateTargets {
		t.Fatal("service unexpectedly enabled broad exit")
	}
}

func TestAPIAuthOriginAndHost(t *testing.T) {
	n := startNode(t, testConfig(t))
	url := "http://" + n.Status().APIListen + "/v1/status"
	for _, tc := range []struct {
		name, token, origin, host string
		want                      int
	}{{"missing-token", "", "", "", 401}, {"wrong-token", "incorrect", "", "", 401}, {"foreign-origin", n.Config.APIToken, "https://attacker.example", "", 403}, {"rebound-host", n.Config.APIToken, "", "attacker.example:7777", 403}, {"allowed", n.Config.APIToken, "http://localhost:5173", "", 200}} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", url, nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.host != "" {
				req.Host = tc.host
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status %d want %d", resp.StatusCode, tc.want)
			}
			if tc.want == 200 {
				var s Status
				if err = json.NewDecoder(resp.Body).Decode(&s); err != nil {
					t.Fatal(err)
				}
				if s.PeerID != n.Host.ID().String() {
					t.Fatal("wrong node")
				}
			}
		})
	}
}

func TestFramesAndIPPolicy(t *testing.T) {
	for _, n := range []uint32{0, maxHeader + 1, 0xffffffff} {
		var b bytes.Buffer
		_ = binary.Write(&b, binary.BigEndian, n)
		var r request
		if readFrame(&b, &r) == nil {
			t.Errorf("accepted length %d", n)
		}
	}
	var r request
	if readFrame(bytes.NewReader([]byte{0, 0, 0, 3, '{'}), &r) == nil {
		t.Fatal("accepted truncated frame")
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "::1", "::ffff:127.0.0.1", "100.64.0.1", "169.254.169.254", "192.0.2.1", "2001:db8::1"} {
		if publicIP(netip.MustParseAddr(ip)) {
			t.Errorf("accepted reserved %s", ip)
		}
	}
	if !publicIP(netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("public IP denied")
	}
}

func TestMalformedSOCKSAndShutdown(t *testing.T) {
	n := startNode(t, testConfig(t))
	c, err := net.Dial("tcp", n.Status().SOCKSListen)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Write([]byte{4, 1, 0})
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err = c.Read(b[:]); err == nil {
		t.Fatal("malformed greeting accepted")
	}
	c.Close()
	if err = n.Stop(); err != nil {
		t.Fatal(err)
	}
	if err = n.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-n.Done():
	case <-time.After(time.Second):
		t.Fatal("stop did not close done")
	}
}

func TestPersistentIdentity(t *testing.T) {
	dir := t.TempDir()
	first, err := IdentityID(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := IdentityID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("identity changed")
	}
	if runtime.GOOS != "windows" {
		if err = os.Chmod(filepath.Join(dir, "identity.key"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err = IdentityID(dir); err == nil {
			t.Fatal("accepted publicly readable identity")
		}
	}
}

func TestCircuitRelayEncryptedTunnel(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "encrypted through circuit relay")
	}))
	defer origin.Close()
	rc := testConfig(t)
	rc.Relay = true
	relayNode := startNode(t, rc)
	cc := testConfig(t)
	cid, _ := IdentityID(cc.StateDir)
	sc := testConfig(t)
	sc.AllowedPeers = []string{cid}
	sc.Services = []config.Service{{Name: "echo", Target: strings.TrimPrefix(origin.URL, "http://")}}
	server := startNode(t, sc)
	cl := startNode(t, cc)
	connect(t, server, relayNode)
	connect(t, cl, relayNode)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := client.Reserve(ctx, server.Host, peer.AddrInfo{ID: relayNode.Host.ID(), Addrs: relayNode.Host.Addrs()})
	if err != nil {
		t.Fatal(err)
	}
	address := relayNode.Status().Addresses[0] + "/p2p-circuit/p2p/" + server.Host.ID().String()
	if _, err = cl.Connect(ctx, address); err != nil {
		t.Fatal(err)
	}
	conns := cl.Host.Network().ConnsToPeer(server.Host.ID())
	found := false
	for _, c := range conns {
		if c.Stat().Limited {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a circuit connection")
	}
	s, err := cl.openTunnel(ctx, server.Host.ID().String(), request{Service: "echo"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = io.WriteString(s, "GET / HTTP/1.0\r\nHost: relay-test\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(s)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(response, []byte("encrypted through circuit relay")) {
		t.Fatalf("relay HTTP response %q", response)
	}
}

func TestServerSelectionAndAPI(t *testing.T) {
	cc := testConfig(t)
	id, err := IdentityID(cc.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	sc := testConfig(t)
	sc.Advertise = true
	sc.ServerLabel = "Test gateway"
	sc.ServerCountry = "DE"
	sc.Exit = true
	sc.AllowedPeers = []string{id}
	server := startNode(t, sc)
	clientNode := startNode(t, cc)
	connect(t, clientNode, server)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	catalog := clientNode.Discovery.Servers(ctx)
	if len(catalog) != 1 || catalog[0].Label != "Test gateway" || !catalog[0].CanConnect {
		t.Fatalf("catalog %+v", catalog)
	}
	if err = clientNode.SelectPeer(ctx, server.Host.ID().String(), clientNode.Status().Addresses[0]); err == nil {
		t.Fatal("accepted address with wrong peer ID")
	}
	body, _ := json.Marshal(map[string]string{"peer_id": server.Host.ID().String(), "address": server.Status().Addresses[0]})
	req, _ := http.NewRequest("POST", "http://"+clientNode.Status().APIListen+"/v1/select", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+clientNode.Config.APIToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("selection %d %s", resp.StatusCode, b)
	}
	if clientNode.Status().UpstreamPeer != server.Host.ID().String() {
		t.Fatal("selected peer not reflected in status")
	}
	if clientNode.Config.UpstreamPeer != "" {
		t.Fatal("mutated immutable config")
	}
}

func TestAccountAPIAndStateLock(t *testing.T) {
	c := testConfig(t)
	n := startNode(t, c)
	if _, err := Start(context.Background(), c); err == nil {
		t.Fatal("started two daemons sharing state")
	}
	for _, path := range []string{"status", "devices", "history"} {
		req, _ := http.NewRequest("GET", "http://"+n.Status().APIListen+"/v1/account/"+path, nil)
		req.Header.Set("Authorization", "Bearer "+n.Config.APIToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("account %s response %d", path, resp.StatusCode)
		}
		if bytes.Contains(b, []byte("meshway-recovery-v1:")) {
			t.Fatal("recovery leaked into API")
		}
	}
	accountID := n.Account.Status().AccountID
	n.Stop()
	restart := startNode(t, c)
	if restart.Account.Status().AccountID != accountID {
		t.Fatal("account changed on restart")
	}
}

func TestPersonalServerSameAccountOnlyWithoutSync(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "my personal encrypted server") }))
	defer origin.Close()
	cc := testConfig(t)
	recovery := filepath.Join(t.TempDir(), "backup.txt")
	if err := account.ExportRecovery(cc.StateDir, recovery); err != nil {
		t.Fatal(err)
	}
	sc := testConfig(t)
	if err := account.ImportRecovery(sc.StateDir, recovery, false); err != nil {
		t.Fatal(err)
	}
	sc.PersonalServer = true
	sc.Advertise = true
	sc.Exit = true
	sc.AllowPrivateTargets = true
	sc.ServerLabel = "My VPS"
	server := startNode(t, sc)
	cc.UpstreamPeer = server.Host.ID().String()
	owner := startNode(t, cc)
	connect(t, owner, server)
	stranger := startNode(t, testConfig(t))
	connect(t, stranger, server)
	if owner.Account.Status().AccountID != server.Account.Status().AccountID || owner.Host.ID() == server.Host.ID() {
		t.Fatal("account/device identity separation failed")
	}
	if owner.Config.AccountSync || server.Config.AccountSync {
		t.Fatal("sync enabled unexpectedly")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	catalog := owner.Discovery.Servers(ctx)
	if len(catalog) != 1 || !catalog[0].CanConnect || !catalog[0].Personal {
		t.Fatalf("personal catalog %+v", catalog)
	}
	if len(stranger.Discovery.Servers(ctx)) != 0 {
		t.Fatal("personal server advertised to foreign account")
	}
	c, code := socksDial(t, owner.Status().SOCKSListen, strings.TrimPrefix(origin.URL, "http://"))
	defer c.Close()
	if code != 0 {
		t.Fatalf("same-account SOCKS denied %d", code)
	}
	_, _ = io.WriteString(c, "GET / HTTP/1.0\r\nHost: personal\r\n\r\n")
	b, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("my personal encrypted server")) {
		t.Fatalf("response %q", b)
	}
	if _, err = stranger.openTunnel(ctx, server.Host.ID().String(), request{Target: strings.TrimPrefix(origin.URL, "http://")}); err == nil {
		t.Fatal("foreign account used personal exit")
	}
	if err = server.Account.Revoke(owner.Host.ID().String()); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.openTunnel(ctx, server.Host.ID().String(), request{Target: strings.TrimPrefix(origin.URL, "http://")}); err == nil {
		t.Fatal("revoked device used personal exit")
	}
}

func TestDisconnectClosesOutgoingTCP(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	connections := make(chan net.Conn, 1)
	go func() {
		c, err := listener.Accept()
		if err == nil {
			connections <- c
		}
	}()
	clientNode, _ := pair(t, true, true, true)
	c, code := socksDial(t, clientNode.Status().SOCKSListen, listener.Addr().String())
	defer c.Close()
	if code != 0 {
		t.Fatalf("SOCKS %d", code)
	}
	select {
	case origin := <-connections:
		defer origin.Close()
	case <-time.After(time.Second):
		t.Fatal("origin not connected")
	}
	clientNode.Disconnect()
	if clientNode.UpstreamPeer() != "" {
		t.Fatal("upstream not cleared")
	}
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err = c.Read(b[:]); err == nil {
		t.Fatal("outgoing proxy survived disconnect")
	}
	clientNode.mu.Lock()
	count := len(clientNode.outbound)
	clientNode.mu.Unlock()
	if count != 0 {
		t.Fatal("outgoing streams retained")
	}
	c2, code := socksDial(t, clientNode.Status().SOCKSListen, listener.Addr().String())
	c2.Close()
	if code == 0 {
		t.Fatal("SOCKS used cleared upstream")
	}
}

func TestValidatedIPFallbackDoesNotWaitForIPv6Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	v6Canceled := make(chan struct{})
	c, remote := net.Pipe()
	defer remote.Close()
	started := time.Now()
	result, err := dialAddresses(ctx, []netip.Addr{netip.MustParseAddr("2001:4860:4860::8888"), netip.MustParseAddr("8.8.8.8")}, "443", func(ctx context.Context, network, address string) (net.Conn, error) {
		if strings.HasPrefix(address, "[") {
			<-ctx.Done()
			close(v6Canceled)
			return nil, ctx.Err()
		}
		if address != "8.8.8.8:443" {
			t.Errorf("unexpected literal dial %s", address)
		}
		return c, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	if time.Since(started) > time.Second {
		t.Fatal("IPv4 fallback waited for IPv6 timeout")
	}
	select {
	case <-v6Canceled:
	case <-time.After(time.Second):
		t.Fatal("losing dial not canceled")
	}
}

func TestPayloadCountersUpdateBeforeConnectionClose(t *testing.T) {
	var counter atomic.Uint64
	var buffer bytes.Buffer
	w := countingWriter{Writer: &buffer, counter: &counter}
	if _, err := w.Write([]byte("streaming")); err != nil {
		t.Fatal(err)
	}
	if counter.Load() != 9 {
		t.Fatal("counter not live")
	}
}

func TestGatewayRestartKeepsIdentityAndRedials(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "gateway restart works") }))
	defer origin.Close()
	cc := testConfig(t)
	id, err := IdentityID(cc.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	portListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := portListener.Addr().(*net.TCPAddr).Port
	portListener.Close()
	sc := testConfig(t)
	sc.ListenAddresses = []string{"/ip4/127.0.0.1/tcp/" + strconv.Itoa(port)}
	sc.Exit = true
	sc.AllowPrivateTargets = true
	sc.AllowedPeers = []string{id}
	server := startNode(t, sc)
	cc.UpstreamPeer = server.Host.ID().String()
	clientNode := startNode(t, cc)
	connect(t, clientNode, server)
	if !clientNode.Status().UpstreamConnected {
		t.Fatal("live server reported disconnected")
	}
	oldID := server.Host.ID()
	server.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for clientNode.Status().UpstreamConnected && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if clientNode.Status().UpstreamConnected {
		t.Fatal("stopped server still reported connected")
	}
	if clientNode.Status().UpstreamPeer != oldID.String() {
		t.Fatal("lost selected peer after restart")
	}
	restarted := startNode(t, sc)
	if restarted.Host.ID() != oldID {
		t.Fatal("restart changed persistent peer identity")
	}
	c, code := socksDial(t, clientNode.Status().SOCKSListen, strings.TrimPrefix(origin.URL, "http://"))
	defer c.Close()
	if code != 0 {
		t.Fatalf("restart SOCKS reply %d", code)
	}
	_, _ = io.WriteString(c, "GET / HTTP/1.0\r\nHost: restarted\r\n\r\n")
	b, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("gateway restart works")) {
		t.Fatalf("restart response %q", b)
	}
	if !clientNode.Status().UpstreamConnected {
		t.Fatal("redialed server reported disconnected")
	}
}

func TestBootstrapMaintenanceReconnects(t *testing.T) {
	a := startNode(t, testConfig(t))
	b := startNode(t, testConfig(t))
	p := peer.AddrInfo{ID: b.Host.ID(), Addrs: b.Host.Addrs()}
	go a.maintainPeer(p, 20*time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for !a.peerConnected(b.Host.ID()) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !a.peerConnected(b.Host.ID()) {
		t.Fatal("bootstrap initial connection failed")
	}
	if err := a.Host.Network().ClosePeer(b.Host.ID()); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for !a.peerConnected(b.Host.ID()) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !a.peerConnected(b.Host.ID()) {
		t.Fatal("bootstrap did not reconnect")
	}
}

func FuzzTunnelFrame(f *testing.F) {
	f.Add([]byte{0, 0, 0, 2, '{', '}'})
	f.Add([]byte{0, 0, 16, 1})
	f.Add([]byte{255, 255, 255, 255})
	f.Add([]byte{0, 0, 0, 18, '{', '"', 's', 'e', 'r', 'v', 'i', 'c', 'e', '"', ':', '"', 'w', 'e', 'b', '"', '}'})
	f.Fuzz(func(t *testing.T, data []byte) { var req request; _ = readFrame(bytes.NewReader(data), &req) })
}
