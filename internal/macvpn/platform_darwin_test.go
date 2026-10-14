//go:build darwin && !ios

package macvpn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/user-github-me/meshway/internal/config"
	"github.com/user-github-me/meshway/internal/core"
)

type probeBackend struct {
	out   []byte
	err   error
	calls []command
}

func (e *probeBackend) execute(_ context.Context, c command) ([]byte, error) {
	e.calls = append(e.calls, c)
	return e.out, e.err
}

func TestPrivateConfigurationUsesSameNoFollowDescriptor(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("app-user configuration fixture")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Default(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err = config.Save(path, c, false); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MESHWAY_STATE_KEY", "deliberately-invalid-environment-key")
	decoded, owner, err := loadPrivateConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if owner != uint32(os.Getuid()) || len(decoded.StorageKey) != 0 || decoded.APIListen != c.APIListen {
		t.Fatal("privileged config changed owner or loaded environment credentials")
	}
	link := filepath.Join(dir, "symlink.json")
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err = loadPrivateConfig(link); err == nil {
		t.Fatal("configuration symlink accepted")
	}
	parentLink := filepath.Join(dir, "alias")
	if err = os.Symlink(dir, parentLink); err != nil {
		t.Fatal(err)
	}
	if _, _, err = loadPrivateConfig(filepath.Join(parentLink, "config.json")); err == nil {
		t.Fatal("configuration ancestor symlink accepted")
	}
	hardLink := filepath.Join(dir, "hardlink.json")
	if err = os.Link(path, hardLink); err != nil {
		t.Fatal(err)
	}
	if _, _, err = loadPrivateConfig(path); err == nil {
		t.Fatal("multi-link configuration accepted")
	}
	if err = os.Remove(hardLink); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err = loadPrivateConfig(path); err == nil {
		t.Fatal("publicly readable configuration accepted")
	}
}

func TestPacketAPIRejectsRedirectWithoutForwardingCredentials(t *testing.T) {
	called := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) }))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 307) }))
	defer redirect.Close()
	a, err := newPacketAPI(config.Config{APIListen: strings.TrimPrefix(redirect.URL, "http://"), APIToken: strings.Repeat("a", 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer a.client.CloseIdleConnections()
	if _, status, err := a.request(context.Background(), "/v1/vpn/read", nil, ""); err == nil || status != 307 {
		t.Fatal("redirected API request did not fail closed")
	}
	if called {
		t.Fatal("helper followed a redirect with API credentials")
	}
	for _, address := range []string{"example.org:7777", "192.0.2.5:7777", "127.0.0.1:0", "127.0.0.1:not-a-port"} {
		if _, err := newPacketAPI(config.Config{APIListen: address}); err == nil {
			t.Fatalf("unsafe API address accepted: %s", address)
		}
	}
}

func markedRouteOutput(target, iface string, marker uint32) []byte {
	return []byte(fmt.Sprintf("   route to: %s\ndestination: %s\n    gateway: 192.0.2.1\n  interface: %s\n      flags: <UP,GATEWAY,HOST,DONE,STATIC,PROTO1,PROTO2>\n recvpipe  sendpipe  ssthresh  rtt,msec    rttvar  hopcount      mtu     expire\n       0         0         0         0         0         %d      1500         0\n", target, target, iface, marker))
}

func TestRouteInspectorUsesMarkerAndPreservesForeignReplacement(t *testing.T) {
	const marker = 1234567
	s := step{Apply: command{Name: "/sbin/route", Args: []string{"-n", "add", "-inet", "-host", "192.0.2.5", "-gateway", "192.0.2.1", "-proto1", "-proto2", "-hopcount", strconv.Itoa(marker)}}}
	p := &probeBackend{out: markedRouteOutput("192.0.2.5", "en0", marker)}
	e := systemExecutor{marker: marker, backend: p}
	if owned, err := e.inspect(context.Background(), s); err != nil || !owned {
		t.Fatalf("owned route not recognized: %v", err)
	}
	p.out = markedRouteOutput("192.0.2.5", "en0", marker+1)
	if owned, err := e.inspect(context.Background(), s); err != nil || owned {
		t.Fatal("foreign marker was accepted")
	}
	p.out = []byte("destination: default\n gateway: 192.0.2.1\n interface: en0\n flags: <UP,GATEWAY>\n")
	if owned, err := e.inspect(context.Background(), s); err != nil || owned {
		t.Fatal("fallback route was accepted as exact owned host route")
	}
	p.err = context.DeadlineExceeded
	if _, err := e.inspect(context.Background(), s); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("failed route probe was treated as absence")
	}
}

func TestDNSInspectorPreservesReplacedDictionary(t *testing.T) {
	key := "State:/Network/Service/Meshway-0123456789abcdef01234567/DNS"
	s := step{Apply: command{Name: "/usr/sbin/scutil", Input: "d.add InterfaceName utun99\n"}, Undo: command{Name: "/usr/sbin/scutil", Input: "remove " + key + "\nquit\n"}}
	owned := `<dictionary> {
 InterfaceName : utun99
 MeshwayOwner : Meshway-0123456789abcdef01234567
 ServerAddresses : <array> {
  0 : 10.77.0.1
  1 : fd77::1
 }
}`
	p := &probeBackend{out: []byte(owned)}
	e := systemExecutor{backend: p}
	if yes, err := e.inspect(context.Background(), s); err != nil || !yes {
		t.Fatalf("owned DNS entry not recognized: %v", err)
	}
	for _, foreign := range []string{strings.Replace(owned, "utun99", "utun98", 1), strings.Replace(owned, "fd77::1", "2001:db8::1", 1), strings.Replace(owned, "MeshwayOwner : Meshway-0123456789abcdef01234567", "MeshwayOwner : another-app", 1)} {
		p.out = []byte(foreign)
		if yes, err := e.inspect(context.Background(), s); err != nil || yes {
			t.Fatal("replacement DNS dictionary was accepted")
		}
	}
	p.out = []byte("  No such key\n")
	if yes, err := e.inspect(context.Background(), s); err != nil || yes {
		t.Fatal("positive absent key failed")
	}
	p.out = []byte("unexpected probe output")
	if _, err := e.inspect(context.Background(), s); err == nil {
		t.Fatal("unknown probe output was treated as absence")
	}
}

func TestStaleJournalNeverMutatesReusedInterfaceName(t *testing.T) {
	s := step{Apply: command{Name: "/sbin/ifconfig", Args: []string{"utun99", "inet6", "fd77::2", "prefixlen", "128"}}}
	p := &probeBackend{}
	if owned, err := (systemExecutor{backend: p}).inspect(context.Background(), s); err != nil || owned {
		t.Fatal("stale journal claimed a potentially reused interface")
	}
	if len(p.calls) != 0 {
		t.Fatal("stale interface inspection invoked a mutation")
	}
	if owned, err := (systemExecutor{liveDevice: "utun99", backend: p}).inspect(context.Background(), s); err != nil || !owned {
		t.Fatal("live descriptor ownership was not recognized")
	}
}

func TestReadOnlySystemRouteOutputParses(t *testing.T) {
	out, err := (systemExecutor{}).execute(context.Background(), command{Name: "/sbin/route", Args: []string{"-n", "get", "127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	r := parseRoute(out)
	if r.Destination != "127.0.0.1" || r.Interface != "lo0" || !strings.Contains(r.Flags, "HOST") {
		t.Fatal("actual macOS route-get shape was not understood")
	}
}

func TestPrivateJournalWriteAndSync(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	o := Options{RuntimeDir: dir}
	j := journal{Version: 3, PID: os.Getpid(), Device: "utun99", Marker: 1234, SessionID: strings.Repeat("a", 32), OwnerUID: uint32(os.Getuid()), ConfigPath: filepath.Join(dir, "config.json")}
	if err := saveJournal(o, j); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(dir, "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("journal was not written as a private regular file")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatal("journal write left temporary files")
	}
}

func TestRouteTableShorthandNormalization(t *testing.T) {
	for input, expected := range map[string]string{"0/1": "0.0.0.0/1", "128.0/1": "128.0.0.0/1", "128.0.0.0/1": "128.0.0.0/1", "192.0.2.5": "192.0.2.5/32", "::/1": "::/1", "8000::/1": "8000::/1"} {
		if result := normalizeRouteDestination(input); result != expected {
			t.Fatalf("%s normalized as %s", input, result)
		}
	}
}

func TestCrashRecoveryStopsOnlyJournalOwnedPacketSession(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("app-user private config fixture")
	}
	serverConfig, err := config.Default(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	serverConfig.ListenAddresses = []string{"/ip4/127.0.0.1/tcp/0"}
	serverConfig.APIListen = ""
	serverConfig.SOCKSListen = ""
	clientConfig, err := config.Default(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	clientConfig.ListenAddresses = []string{"/ip4/127.0.0.1/tcp/0"}
	clientConfig.APIListen = "127.0.0.1:0"
	clientConfig.SOCKSListen = ""
	id, err := core.IdentityID(clientConfig.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	serverConfig.Exit = true
	serverConfig.AllowedPeers = []string{id}
	server, err := core.Start(context.Background(), serverConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	client, err := core.Start(context.Background(), clientConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	if _, err = client.Connect(context.Background(), server.Status().Addresses[0]); err != nil {
		t.Fatal(err)
	}
	clientConfig.APIListen = client.Status().APIListen
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err = config.Save(path, clientConfig, false); err != nil {
		t.Fatal(err)
	}
	api, err := newPacketAPI(clientConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer api.client.CloseIdleConnections()
	api.session = strings.Repeat("a", 32)
	payload := []byte(fmt.Sprintf(`{"mode":"exit","peer_id":%q,"session_id":%q}`, server.Host.ID().String(), api.session))
	if _, _, err = api.request(context.Background(), "/v1/vpn/start", payload, "application/json"); err != nil {
		t.Fatal(err)
	}
	j := journal{Version: 3, PID: 12345, Device: "utun99", Marker: 42, ConfigPath: path, OwnerUID: uint32(os.Getuid()), SessionID: api.session}
	if err = cleanupPacketSession(j); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("GET", "http://"+clientConfig.APIListen+"/v1/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+clientConfig.APIToken)
	response, err := api.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		VPN struct {
			Active bool   `json:"active"`
			PeerID string `json:"peer_id"`
		} `json:"vpn"`
	}
	if err = json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.VPN.Active || state.VPN.PeerID != "" {
		t.Fatal("crash cleanup retained the daemon packet session")
	}
	// Idempotent recovery receives409 once the old owner is gone and never
	// removes a new native client that subsequently owns the daemon.
	api.session = strings.Repeat("b", 32)
	payload = []byte(fmt.Sprintf(`{"mode":"exit","peer_id":%q,"session_id":%q}`, server.Host.ID().String(), api.session))
	if _, _, err = api.request(context.Background(), "/v1/vpn/start", payload, "application/json"); err != nil {
		t.Fatal(err)
	}
	if err = cleanupPacketSession(j); err != nil {
		t.Fatal(err)
	}
	probe, err := http.NewRequest("GET", api.url+"/v1/vpn/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	probe.Header.Set("Authorization", "Bearer "+api.token)
	probe.Header.Set("X-Meshway-VPN-Session", api.session)
	response, err = api.client.Do(probe)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("old crash journal stopped the new owner's packet session")
	}
}

func TestCrashRecoveryRejectsChangedConfigurationOwner(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("app-user fixture")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Default(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err = config.Save(path, c, false); err != nil {
		t.Fatal(err)
	}
	j := journal{SessionID: strings.Repeat("a", 32), ConfigPath: path, OwnerUID: uint32(os.Getuid() + 1)}
	if err = cleanupPacketSession(j); err == nil || !strings.Contains(err.Error(), "owner changed") {
		t.Fatal("crash cleanup accepted another user's configuration")
	}
}

func TestCrashCleanupRetainsJournalWhenOriginalConfigMissing(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	o := Options{RuntimeDir: dir}
	j := journal{Version: 3, PID: 12345, Device: "utun99", Marker: 42, ConfigPath: filepath.Join(dir, "missing.json"), OwnerUID: uint32(os.Getuid()), SessionID: strings.Repeat("a", 32)}
	if err = saveJournal(o, j); err != nil {
		t.Fatal(err)
	}
	if err = restore(o, &j, &transactionBackend{}); err == nil {
		t.Fatal("missing original config was ignored")
	}
	if _, err = os.Stat(filepath.Join(dir, "journal.json")); err != nil {
		t.Fatal("failed packet cleanup deleted its recovery journal")
	}
}

func TestJournalCallerRejectsAnotherConfigurationWithoutTouchingSession(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("app-user fixture")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Default(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	other := filepath.Join(dir, "other.json")
	if err = config.Save(path, c, false); err != nil {
		t.Fatal(err)
	}
	if err = config.Save(other, c, false); err != nil {
		t.Fatal(err)
	}
	j := journal{ConfigPath: path, OwnerUID: uint32(os.Getuid()), SessionID: strings.Repeat("a", 32), CaptureInstance: strings.Repeat("b", 32)}
	if err = journalCaller(Options{ConfigPath: path}, j); err != nil {
		t.Fatal(err)
	}
	for _, o := range []Options{{ConfigPath: other}, {}, {ConfigPath: path + "/../config.json"}} {
		if err = journalCaller(o, j); err == nil {
			t.Fatal("foreign configuration acquired journal")
		}
	}
	if err = journalCaller(Options{ConfigPath: path, CaptureInstance: j.CaptureInstance}, j); err != nil {
		t.Fatal("original capture instance rejected")
	}
	if err = journalCaller(Options{ConfigPath: path, CaptureInstance: strings.Repeat("c", 32)}, j); err == nil {
		t.Fatal("same-configuration foreign capture instance acquired journal")
	}
	j.OwnerUID++
	if err = journalCaller(Options{ConfigPath: path}, j); err == nil {
		t.Fatal("changed owner acquired journal")
	}
}

func TestRealDarwinDefaultHalfSpaceStillRequiresExactMaskMarkerAndDevice(t *testing.T) {
	const marker = 1971273528
	s := step{Apply: command{Name: "/sbin/route", Args: []string{"-n", "add", "-inet", "-net", "0.0.0.0/1", "-interface", "utun4", "-proto1", "-proto2", "-hopcount", strconv.Itoa(marker)}}}
	real := []byte("destination: default\n mask: 128.0.0.0\n interface: utun4\n flags: <UP,DONE,STATIC,PROTO2,PROTO1,PRCLONING,GLOBAL>\n recvpipe sendpipe ssthresh rtt,msec rttvar hopcount mtu expire\n 0 0 0 0 0 1971273528 1280 0\n")
	p := &probeBackend{out: real}
	e := systemExecutor{marker: marker, backend: p, liveDevice: "utun4"}
	if yes, err := e.inspect(context.Background(), s); err != nil || !yes {
		t.Fatalf("actual Darwin /1 shape rejected: %v", err)
	}
	for _, bad := range []string{strings.Replace(string(real), "128.0.0.0", "0.0.0.0", 1), strings.Replace(string(real), "utun4", "en0", 1), strings.Replace(string(real), "1971273528", "1971273529", 1)} {
		p.out = []byte(bad)
		if yes, err := e.inspect(context.Background(), s); err != nil || yes {
			t.Fatal("foreign /0 or replaced route accepted")
		}
	}
}

func TestUnderlayCloneCannotReplaceExplicitPhysicalBypass(t *testing.T) {
	const address = "192.0.2.5"
	for _, tc := range []struct {
		name, flags string
		wantHost    bool
	}{
		{"cloned-physical-default", "<UP,GATEWAY,HOST,DONE,WASCLONED,IFSCOPE,IFREF>", true},
		{"explicit-static-host", "<UP,GATEWAY,HOST,DONE,STATIC>", false},
		{"cloned-with-static-metadata", "<UP,GATEWAY,HOST,DONE,STATIC,WASCLONED>", true},
		{"not-a-host-route", "<UP,GATEWAY,DONE,STATIC>", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &probeBackend{out: []byte("destination: " + address + "\n gateway: 192.0.2.1\n interface: en0\n flags: " + tc.flags + "\n")}
			steps, err := routePlan("utun99", []string{address}, p, 123)
			if err != nil {
				t.Fatal(err)
			}
			hasHost := len(steps) == 5 && argument(steps[0].Apply.Args, "-host") == address && argument(steps[0].Apply.Args, "-gateway") == "192.0.2.1"
			if hasHost != tc.wantHost {
				t.Fatalf("persistent bypass=%v want=%v", hasHost, tc.wantHost)
			}
		})
	}
}

func TestEffectiveUnderlayLookupMustSurviveSplitRoutes(t *testing.T) {
	p := &probeBackend{out: markedRouteOutput("192.0.2.5", "en0", 123)}
	if err := verifyUnderlays([]string{"192.0.2.5"}, p); err != nil {
		t.Fatal(err)
	}
	p.out = markedRouteOutput("192.0.2.5", "utun99", 123)
	if err := verifyUnderlays([]string{"192.0.2.5"}, p); err == nil {
		t.Fatal("recursive encrypted transport accepted as ready")
	}
	p.err = context.DeadlineExceeded
	if err := verifyUnderlays([]string{"192.0.2.5"}, p); err == nil {
		t.Fatal("failed physical route probe accepted as ready")
	}
}

func TestDeadZombieCannotOwnMarkerOnlyStop(t *testing.T) {
	for _, state := range []string{"Z", "Z+", "ZX"} {
		if start := liveProcessStart([]byte(state + " Wed Oct 14 19:49:21 2026\n")); start != "" {
			t.Fatal("dead process retained live ownership")
		}
	}
	if start := liveProcessStart([]byte("Ss+ Wed Oct 14 19:49:21 2026\n")); start != "Wed Oct 14 19:49:21 2026" {
		t.Fatal("live process identity changed")
	}
	if start := liveProcessStart([]byte("")); start != "" {
		t.Fatal("missing process retained ownership")
	}
}

func TestStaleUtunRouteStepsRequirePositiveTableAbsence(t *testing.T) {
	p := &probeBackend{out: []byte("Routing tables\nInternet:\nDestination Gateway Flags Netif Expire\ndefault 192.0.2.1 UGScg en0\n")}
	for _, target := range []string{"0.0.0.0/1", "128.0.0.0/1", "::/1", "8000::/1"} {
		family := "-inet"
		if strings.Contains(target, ":") {
			family = "-inet6"
		}
		s := step{Apply: command{Name: "/sbin/route", Args: []string{"-n", "add", family, "-net", target, "-interface", "utun99", "-proto1", "-proto2", "-hopcount", "123"}}}
		if owned, err := (systemExecutor{marker: 123, backend: p}).inspect(context.Background(), s); err != nil || owned {
			t.Fatalf("absent stale interface route claimed: %v", err)
		}
		p.err = context.DeadlineExceeded
		if _, err := (systemExecutor{marker: 123, backend: p}).inspect(context.Background(), s); err == nil {
			t.Fatal("failed enumeration treated as absence")
		}
		p.err = nil
	}
	for _, call := range p.calls {
		if call.Name != "/usr/sbin/netstat" {
			t.Fatal("positively absent route was mutated or claimed")
		}
	}
}
func TestRouteTableExactInterfaceAndPrefixAbsence(t *testing.T) {
	for _, tc := range []struct {
		table, target, iface string
		present              bool
	}{
		{"Destination Gateway Flags Netif Expire\n0/1 link#17 UCS utun99\n", "0.0.0.0/1", "utun99", true},
		{"Destination Gateway Flags Netif Expire\n128.0/1 link#17 UCS utun99\n", "128.0.0.0/1", "utun99", true},
		{"Destination Gateway Flags Netif Expire\n8000::/1 link#17 UCS utun99\n", "8000::/1", "utun99", true},
		{"Destination Gateway Flags Netif Expire\ndefault 192.0.2.1 UG en0\n", "0.0.0.0/1", "utun99", false},
		{"Destination Gateway Flags Netif Expire\n0/1 link#18 UCS utun98\n", "0.0.0.0/1", "utun99", false},
	} {
		present, err := tableHasRouteOnInterface([]byte(tc.table), tc.target, tc.iface)
		if err != nil || present != tc.present {
			t.Fatalf("exact table presence=%v want=%v err=%v", present, tc.present, err)
		}
	}
	if _, err := tableHasRouteOnInterface([]byte("unexpected result"), "::/1", "utun99"); err == nil {
		t.Fatal("unknown output treated as absence")
	}
}
