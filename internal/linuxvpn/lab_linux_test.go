//go:build linux && !android

package linuxvpn

import (
	"context"
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
	c.ListenAddresses = []string{"/ip6/fd89:77::2/tcp/14001"}
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
	body, _ := json.Marshal(labInvitation{Address: "/ip6/fd89:77::2/tcp/14001/p2p/" + n.Host.ID().String(), Peer: n.Host.ID().String()})
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
	runtime, err := os.MkdirTemp("/run", "meshway-helper-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(runtime)
	options := Options{ConfigPath: configPath, Device: "meshwayhelp0", DNSMode: "file", RuntimeDir: filepath.Join(runtime, "runtime")}
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
	// Force actual IPv6 neighbor discovery after the firewall is active. A
	// blanket ICMPv6 block can look correct until this underlay cache expires.
	transport.CloseIdleConnections()
	if _, err = command(nil, "ip", "-6", "neigh", "flush", "dev", "eth0"); err != nil {
		t.Fatal(err)
	}
	response, err = client.Get("http://10.98.0.2:18081/")
	if err != nil {
		t.Fatalf("IPv6 underlay failed after neighbor-cache expiry: %v", err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	neighbors, err := command(nil, "ip", "-6", "neigh", "show", "dev", "eth0")
	if err != nil || !strings.Contains(string(neighbors), "fd89:77::2") {
		t.Fatalf("IPv6 neighbor was not rediscovered: %s %v", neighbors, err)
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
	bad := journal{Version: 2, Metric: 123456, Config: configPath, Device: options.Device, DNSMode: "file", Table: "meshway_vpn", Firewall: true, Routes: []route{{4, []string{"10.88.77.0/24", "dev", "eth0"}}}}
	if _, err = routes(4, append([]string{"route", "add"}, ownedRouteArgs(&bad, bad.Routes[0])...)...); err != nil {
		t.Fatal(err)
	}
	if err = save(options, bad); err != nil {
		t.Fatal(err)
	}
	ipPath, err := exec.LookPath("ip")
	if err != nil {
		t.Fatal(err)
	}
	shimDir := t.TempDir()
	originalPath := os.Getenv("PATH")
	defer os.Setenv("PATH", originalPath)
	shim := fmt.Sprintf("#!/bin/sh\ncase \" $* \" in *\" route del \"*) exit 42;; esac\nexec %q \"$@\"\n", ipPath)
	if err = os.WriteFile(filepath.Join(shimDir, "ip"), []byte(shim), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Setenv("PATH", shimDir+":"+originalPath); err != nil {
		t.Fatal(err)
	}
	if err = Stop(options); err == nil {
		t.Fatal("failed route cleanup silently reported success")
	}
	if err = os.Setenv("PATH", originalPath); err != nil {
		t.Fatal(err)
	}
	if _, err = command(nil, "nft", "list", "table", "inet", "meshway_vpn"); err != nil {
		t.Fatal("failed cleanup removed protection")
	}
	if _, err = os.Stat(filepath.Join(options.RuntimeDir, "journal.json")); err != nil {
		t.Fatal("failed cleanup deleted recovery journal")
	}
	if err = Stop(options); err != nil {
		t.Fatal(err)
	}
	// An intent recorded before a failed add must never delete a concurrently
	// created foreign route at the same prefix. Ownership includes protocol and
	// the random journal metric, rather than just destination and device.
	foreign := []string{"10.88.77.0/24", "dev", "eth0", "proto", "static", "metric", "7"}
	if _, err = routes(4, append([]string{"route", "add"}, foreign...)...); err != nil {
		t.Fatal(err)
	}
	bad.Firewall = false
	if err = save(options, bad); err != nil {
		t.Fatal(err)
	}
	if err = Stop(options); err != nil {
		t.Fatal(err)
	}
	foreignRoutes, err := routes(4, "-j", "route", "show", "exact", "10.88.77.0/24")
	if err != nil || !strings.Contains(string(foreignRoutes), "\"metric\":7") {
		t.Fatalf("foreign route changed: %s %v", foreignRoutes, err)
	}
	if _, err = routes(4, append([]string{"route", "del"}, foreign...)...); err != nil {
		t.Fatal(err)
	}
	// A failed deletion followed by a failed inspection is uncertainty, never
	// evidence that an owned TUN has disappeared. Keep journal and firewall.
	bad.Routes, bad.Created, bad.Firewall = nil, true, true
	if _, err = command(nil, "ip", "tuntap", "add", "dev", bad.Device, "mode", "tun"); err != nil {
		t.Fatal(err)
	}
	if _, err = command([]byte("table inet meshway_vpn {}\n"), "nft", "-f", "-"); err != nil {
		t.Fatal(err)
	}
	if err = save(options, bad); err != nil {
		t.Fatal(err)
	}
	shim = fmt.Sprintf("#!/bin/sh\ncase \" $* \" in *\" tuntap del \"*|*\" -j link show \"*) exit 42;; esac\nexec %q \"$@\"\n", ipPath)
	if err = os.WriteFile(filepath.Join(shimDir, "ip"), []byte(shim), 0700); err != nil {
		t.Fatal(err)
	}
	os.Setenv("PATH", shimDir+":"+originalPath)
	if err = Stop(options); err == nil {
		t.Fatal("failed TUN probe was treated as absent")
	}
	os.Setenv("PATH", originalPath)
	if exists, inspectErr := linkExists(bad.Device); inspectErr != nil || !exists {
		t.Fatal("owned TUN unexpectedly removed")
	}
	if exists, inspectErr := tableExists(bad.Table); inspectErr != nil || !exists {
		t.Fatal("uncertain cleanup removed firewall")
	}
	if _, err = os.Stat(filepath.Join(options.RuntimeDir, "journal.json")); err != nil {
		t.Fatal("uncertain cleanup deleted journal")
	}
	if err = Stop(options); err != nil {
		t.Fatal(err)
	}
	// The daemon can retain the TUN FD even when its configuration disappears.
	// Stop must remove the owned interface before dropping protection.
	if err = Start(options); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	if err = Stop(options); err != nil {
		t.Fatal(err)
	}
	if exists, inspectErr := linkExists(options.Device); inspectErr != nil || exists {
		t.Fatal("live-daemon TUN remained after missing-config stop")
	}
	if err = config.Save(configPath, c, false); err != nil {
		t.Fatal(err)
	}
	// Failed firewall deletion and failed inspection must retain recovery state.
	bad.Created = false
	if _, err = command([]byte("table inet meshway_vpn {}\n"), "nft", "-f", "-"); err != nil {
		t.Fatal(err)
	}
	if err = save(options, bad); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(shimDir, "nft"), []byte("#!/bin/sh\nexit 42\n"), 0700); err != nil {
		t.Fatal(err)
	}
	os.Setenv("PATH", shimDir+":"+originalPath)
	if err = Stop(options); err == nil {
		t.Fatal("failed firewall probe was treated as absent")
	}
	os.Setenv("PATH", originalPath)
	if exists, inspectErr := tableExists(bad.Table); inspectErr != nil || !exists {
		t.Fatal("uncertain firewall cleanup removed table")
	}
	if _, err = os.Stat(filepath.Join(options.RuntimeDir, "journal.json")); err != nil {
		t.Fatal("uncertain firewall cleanup deleted journal")
	}
	if err = Stop(options); err != nil {
		t.Fatal(err)
	}
	unsafeParent := t.TempDir()
	if err = os.Chmod(unsafeParent, 0777); err != nil {
		t.Fatal(err)
	}
	unsafe := Options{RuntimeDir: filepath.Join(unsafeParent, "runtime")}
	if guard, lockErr := lock(unsafe); lockErr == nil {
		guard.Close()
		t.Fatal("user-writable runtime ancestor accepted")
	}
	if _, err = os.Stat(unsafe.RuntimeDir); !os.IsNotExist(err) {
		t.Fatal("unsafe runtime directory was created")
	}
	t.Log("PASS automatic TUN + full IPv4/6 routes + DNS + real kernel HTTP over IPv6 underlay after neighbor-cache expiry; daemon-death protection; exact restoration; failed cleanup retains journal/firewall; foreign routes preserved")
}
