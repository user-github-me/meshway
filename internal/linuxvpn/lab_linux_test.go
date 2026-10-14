//go:build linux && !android

package linuxvpn

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/user-github-me/meshway/internal/account"
	"github.com/user-github-me/meshway/internal/config"
	"github.com/user-github-me/meshway/internal/core"
)

func labConfig(t *testing.T, directory string) config.Config {
	t.Helper()
	c, err := config.Default(directory)
	if err != nil {
		t.Fatal(err)
	}
	c.ListenAddresses = []string{"/ip4/0.0.0.0/tcp/14001"}
	c.APIListen = ""
	c.SOCKSListen = ""
	return c
}

type labInvitation struct {
	Address string `json:"address"`
	Peer    string `json:"peer"`
}

func TestLinuxSetupGatewayFixture(t *testing.T) {
	if os.Getenv("MESHWAY_LINUX_HELPER_GATEWAY") != "1" {
		t.Skip("isolated root fixture")
	}
	directory := os.Getenv("MESHWAY_LAB_DIR")
	c := labConfig(t, t.TempDir())
	c.ListenAddresses = []string{"/ip4/10.89.77.2/tcp/14001"}
	c.Exit = true
	c.PersonalServer = true
	c.AllowPrivateTargets = true
	backup := filepath.Join(directory, "recovery")
	n, err := core.Start(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Stop()
	if err = account.ExportRecovery(c.StateDir, backup); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "10.98.0.2:18081")
	if err != nil {
		t.Fatal(err)
	}
	server := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "privileged Linux setup works") })}
	go server.Serve(listener)
	defer server.Close()
	body, _ := json.Marshal(labInvitation{Address: "/ip4/10.89.77.2/tcp/14001/p2p/" + n.Host.ID().String(), Peer: n.Host.ID().String()})
	os.WriteFile(filepath.Join(directory, "gateway.json"), body, 0600)
	for i := 0; i < 800; i++ {
		if _, err = os.Stat(filepath.Join(directory, "done")); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("client setup fixture timed out")
}

func TestLinuxPrivilegedSetupRollbackAndFailClosed(t *testing.T) {
	if os.Getenv("MESHWAY_LINUX_HELPER_LAB") != "1" {
		t.Skip("opt-in disposable Linux root namespace")
	}
	directory := os.Getenv("MESHWAY_LAB_DIR")
	defer os.WriteFile(filepath.Join(directory, "done"), []byte("done"), 0600)
	body, err := os.ReadFile(filepath.Join(directory, "gateway.json"))
	if err != nil {
		t.Fatal(err)
	}
	var invitation labInvitation
	if err = json.Unmarshal(body, &invitation); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	if err = account.ImportRecovery(stateDir, filepath.Join(directory, "recovery"), false); err != nil {
		t.Fatal(err)
	}
	c := labConfig(t, stateDir)
	c.ListenAddresses = []string{"/ip4/0.0.0.0/tcp/0"}
	c.APIListen = "127.0.0.1:0"
	c.UpstreamPeer = invitation.Peer
	c.BootstrapPeers = []string{invitation.Address}
	n, err := core.Start(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Stop()
	c.APIListen = n.Status().APIListen
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err = config.Save(configPath, c, false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err = n.Connect(ctx, invitation.Address); err != nil {
		t.Fatal(err)
	}
	beforeRoutes, err := command(nil, "ip", "-j", "route", "show", "table", "main")
	if err != nil {
		t.Fatal(err)
	}
	beforeDNS, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		t.Fatal(err)
	}
	options := Options{ConfigPath: configPath, Device: "meshwayhelp0", DNSMode: "file", RuntimeDir: filepath.Join(t.TempDir(), "runtime")}
	if err = Start(options); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: nil}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	defer transport.CloseIdleConnections()
	response, err := client.Get("http://10.98.0.2:18081/")
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(payload) != "privileged Linux setup works" {
		t.Fatal("privileged setup kernel egress failed")
	}
	// This address deliberately has an underlying route to the gateway origin.
	// With the owned daemon stopped, nft still rejects packets that might escape
	// through its former default route instead of exposing the user's traffic.
	n.Stop()
	transport.CloseIdleConnections()
	if _, err = client.Get("http://10.98.0.2:18081/"); err == nil {
		t.Fatal("traffic escaped after daemon crash")
	}
	if _, err = command(nil, "nft", "list", "table", "inet", "meshway_vpn"); err != nil {
		t.Fatal("kill-switch firewall vanished on daemon death")
	}
	// Simulate a moved client configuration. Restoration uses its own journal
	// and must still repair networking even when the stopped daemon is absent.
	if err = os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	if err = Stop(options); err != nil {
		t.Fatal(err)
	}
	afterRoutes, err := command(nil, "ip", "-j", "route", "show", "table", "main")
	if err != nil {
		t.Fatal(err)
	}
	afterDNS, _ := os.ReadFile("/etc/resolv.conf")
	if string(beforeRoutes) != string(afterRoutes) {
		t.Fatalf("original routes not restored\nbefore %s\nafter %s", beforeRoutes, afterRoutes)
	}
	if string(beforeDNS) != string(afterDNS) {
		t.Fatal("original DNS not restored")
	}
	if _, err = command(nil, "ip", "link", "show", "dev", options.Device); err == nil {
		t.Fatal("owned TUN remained")
	}
	if _, err = command(nil, "nft", "list", "table", "inet", "meshway_vpn"); err == nil {
		t.Fatal("owned firewall remained")
	}
	// Force a late setup failure after TUN/API/firewall and routes were created
	// by filling the helper's runtime journal with an invalid DNS target via a
	// root-only nft collision. The preflight must refuse without any mutation.
	if _, err = command([]byte("table inet meshway_vpn {}\n"), "nft", "-f", "-"); err != nil {
		t.Fatal(err)
	}
	if err = config.Save(configPath, c, false); err != nil {
		t.Fatal(err)
	}
	restarted, err := core.Start(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Stop()
	c.APIListen = restarted.Status().APIListen
	if err = config.Save(configPath, c, true); err != nil {
		t.Fatal(err)
	}
	if err = Start(options); err == nil {
		t.Fatal("conflicting firewall setup was overwritten")
	}
	if _, err = command(nil, "nft", "delete", "table", "inet", "meshway_vpn"); err != nil {
		t.Fatal(err)
	}
	// Invalid route cleanup must retain protection and its root-owned journal.
	if _, err = command([]byte("table inet meshway_vpn {}\n"), "nft", "-f", "-"); err != nil {
		t.Fatal(err)
	}
	bad := journal{Version: 1, Config: configPath, Device: options.Device, DNSMode: "file", Table: "meshway_vpn", Firewall: true, Routes: []route{{4, []string{"10.89.77.0/24", "dev", "nonexistent0"}}}}
	if err = save(options, bad); err != nil {
		t.Fatal(err)
	}
	if err = Stop(options); err == nil {
		t.Fatal("failed route cleanup silently reported success")
	}
	if _, err = command(nil, "nft", "list", "table", "inet", "meshway_vpn"); err != nil {
		t.Fatal("failed cleanup removed protection")
	}
	if _, err = os.Stat(filepath.Join(options.RuntimeDir, "journal.json")); err != nil {
		t.Fatal("failed cleanup deleted recovery journal")
	}
	bad.Routes = nil
	if err = save(options, bad); err != nil {
		t.Fatal(err)
	}
	if err = Stop(options); err != nil {
		t.Fatal(err)
	}
	t.Log("PASS automatic persistent TUN + underlay preservation + full IPv4/6 route setup + DNS + real kernel HTTP; daemon-death firewall remains; explicit stop restores exact original routes/DNS and removes owned device/firewall")
}
