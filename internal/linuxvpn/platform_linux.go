//go:build linux && !android

package linuxvpn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/user-github-me/meshway/internal/config"
	"golang.org/x/sys/unix"
)

type route struct {
	Family int      `json:"family"`
	Args   []string `json:"args"`
}
type journal struct {
	Version   int     `json:"version"`
	Config    string  `json:"config"`
	Device    string  `json:"device"`
	DNSMode   string  `json:"dns_mode"`
	DNSBackup []byte  `json:"dns_backup,omitempty"`
	DNSPerm   uint32  `json:"dns_perm,omitempty"`
	Routes    []route `json:"routes"`
	Table     string  `json:"table"`
	Created   bool    `json:"created"`
	Firewall  bool    `json:"firewall"`
	DNS       bool    `json:"dns"`
	Started   bool    `json:"started"`
}
type apiStatus struct {
	Upstream string `json:"upstream_peer"`
	Peers    []struct {
		ID        string   `json:"id"`
		Addresses []string `json:"addresses"`
	} `json:"peers"`
}
type underlay struct {
	IP        netip.Addr
	Transport string
	Port      uint16
}
type ipRoute struct {
	Dst     string `json:"dst"`
	Gateway string `json:"gateway"`
	Device  string `json:"dev"`
	Source  string `json:"prefsrc"`
	Type    string `json:"type"`
}

func command(input []byte, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w (%s)", name, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
func runtimeDir(o Options) string {
	if o.RuntimeDir != "" {
		return o.RuntimeDir
	}
	return "/run/meshway-vpn"
}
func lock(o Options) (*os.File, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("Linux full-device VPN needs administrator privileges; run sudo meshway vpn start --config /absolute/path/config.json")
	}
	dir := runtimeDir(o)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("VPN runtime directory must be a root-owned private directory")
	}
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
func save(o Options, j journal) error {
	body, err := json.Marshal(j)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(runtimeDir(o), ".journal-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(body)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(name, filepath.Join(runtimeDir(o), "journal.json")); err != nil {
		return err
	}
	dir, err := os.Open(runtimeDir(o))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func load(o Options) (journal, error) {
	var j journal
	body, err := os.ReadFile(filepath.Join(runtimeDir(o), "journal.json"))
	if err != nil {
		return j, err
	}
	if len(body) > 65536 {
		return j, errors.New("VPN journal oversized")
	}
	if err = json.Unmarshal(body, &j); err != nil {
		return j, err
	}
	if j.Version != 1 {
		return j, errors.New("VPN journal version unsupported")
	}
	if err = ValidateOptions(Options{ConfigPath: j.Config, Device: j.Device, DNSMode: j.DNSMode}); err != nil {
		return j, err
	}
	if j.Table != "meshway_vpn" || len(j.Routes) > 260 {
		return j, errors.New("invalid VPN journal")
	}
	for _, r := range j.Routes {
		if (r.Family != 4 && r.Family != 6) || len(r.Args) < 3 || len(r.Args) > 5 {
			return j, errors.New("invalid VPN route journal")
		}
		if _, err := netip.ParsePrefix(r.Args[0]); err != nil {
			return j, errors.New("invalid VPN route prefix")
		}
		if len(r.Args) == 3 {
			if r.Args[1] != "dev" || r.Args[2] == "" {
				return j, errors.New("invalid VPN route device")
			}
		}
		if len(r.Args) == 5 {
			if r.Args[1] != "via" || r.Args[3] != "dev" || r.Args[4] == "" {
				return j, errors.New("invalid VPN bypass route")
			}
			if _, err := netip.ParseAddr(r.Args[2]); err != nil {
				return j, errors.New("invalid VPN gateway")
			}
		}
	}
	return j, nil
}
func api(c config.Config, method, path string, body any, response any) error {
	var input []byte
	if body != nil {
		input, _ = json.Marshal(body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, "http://"+c.APIListen+path, bytes.NewReader(input))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIToken)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 20 * time.Second}
	defer client.CloseIdleConnections()
	r, err := client.Do(req)
	if err != nil {
		return errors.New("running Meshway client API unavailable")
	}
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, 65536))
	if err != nil {
		return err
	}
	if r.StatusCode != 200 {
		return fmt.Errorf("Meshway API refused VPN setup (%d)", r.StatusCode)
	}
	if response != nil {
		return json.Unmarshal(data, response)
	}
	return nil
}
func readConfig(path string) (config.Config, uint32, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return config.Config{}, 0, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return config.Config{}, 0, errors.New("client config must be a private regular file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return config.Config{}, 0, errors.New("cannot determine client owner")
	}
	c, err := config.Load(path)
	if err != nil {
		return c, 0, err
	}
	if c.Exit || c.Relay {
		return c, 0, errors.New("full-device setup requires a client configuration without relay/exit roles")
	}
	return c, stat.Uid, nil
}
func routes(family int, args ...string) ([]byte, error) {
	if family == 6 {
		args = append([]string{"-6"}, args...)
	}
	return command(nil, "ip", args...)
}

func Start(o Options) (err error) {
	if err = ValidateOptions(o); err != nil {
		return err
	}
	guard, err := lock(o)
	if err != nil {
		return err
	}
	defer guard.Close()
	if _, err = os.Stat(filepath.Join(runtimeDir(o), "journal.json")); err == nil {
		return errors.New("a VPN setup journal exists; run meshway vpn stop first to restore it")
	}
	c, owner, err := readConfig(o.ConfigPath)
	if err != nil {
		return err
	}
	for _, tool := range []string{"ip", "nft"} {
		if _, err = exec.LookPath(tool); err != nil {
			return fmt.Errorf("install Linux prerequisite %s", tool)
		}
	}
	if o.DNSMode == "resolved" {
		if _, err = exec.LookPath("resolvectl"); err != nil {
			return errors.New("systemd-resolved/resolvectl required; alternatively choose --dns-mode file explicitly")
		}
		if _, err = command(nil, "resolvectl", "status"); err != nil {
			return err
		}
		dnsLink, linkErr := filepath.EvalSymlinks("/etc/resolv.conf")
		if linkErr != nil || !strings.HasPrefix(dnsLink, "/run/systemd/resolve/") {
			return errors.New("system resolver must use systemd-resolved stub; otherwise choose --dns-mode file explicitly")
		}
	}
	var status apiStatus
	if err = api(c, "GET", "/v1/status", nil, &status); err != nil {
		return err
	}
	if status.Upstream == "" {
		return errors.New("select your server in the Meshway dashboard before starting device VPN")
	}
	endpoints, err := underlays(c, status)
	if err != nil {
		return err
	}
	if len(endpoints) == 0 {
		return errors.New("server/relay needs reachable literal IP invitations before full-device VPN")
	}
	if _, err = command(nil, "ip", "link", "show", "dev", o.Device); err == nil {
		return errors.New("VPN interface already exists; choose an unused device name")
	}
	if _, err = command(nil, "nft", "list", "table", "inet", "meshway_vpn"); err == nil {
		return errors.New("Meshway firewall table already exists; restore previous setup first")
	}
	for _, item := range []struct {
		family int
		prefix string
	}{{4, "0.0.0.0/1"}, {4, "128.0.0.0/1"}, {6, "::/1"}, {6, "8000::/1"}} {
		body, inspectErr := routes(item.family, "-j", "route", "show", "exact", item.prefix)
		if inspectErr != nil {
			return inspectErr
		}
		var existing []ipRoute
		if json.Unmarshal(body, &existing) != nil {
			return errors.New("cannot inspect existing VPN capture routes")
		}
		if len(existing) > 0 {
			return errors.New("an existing VPN already owns a full-traffic capture route; stop it before Meshway device VPN")
		}
	}
	j := journal{Version: 1, Config: o.ConfigPath, Device: o.Device, DNSMode: o.DNSMode, Table: "meshway_vpn"}
	if o.DNSMode == "file" {
		info, readErr := os.Lstat("/etc/resolv.conf")
		if readErr != nil {
			return readErr
		}
		if !info.Mode().IsRegular() {
			return errors.New("file DNS mode requires a regular /etc/resolv.conf; use systemd-resolved for managed/symlink DNS")
		}
		j.DNSBackup, err = os.ReadFile("/etc/resolv.conf")
		if err != nil || len(j.DNSBackup) > 65536 {
			return errors.New("cannot snapshot bounded resolver file")
		}
		j.DNSPerm = uint32(info.Mode().Perm())
	}
	if err = save(o, j); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			if restoreErr := rollback(o, &j, c); restoreErr != nil {
				err = fmt.Errorf("%w; rollback incomplete, run vpn stop: %v", err, restoreErr)
			}
		}
	}()
	// Record intention before every mutation, making partial setup recoverable.
	j.Created = true
	if err = save(o, j); err != nil {
		return err
	}
	if _, err = command(nil, "ip", "tuntap", "add", "dev", o.Device, "mode", "tun", "user", strconv.FormatUint(uint64(owner), 10)); err != nil {
		return err
	}
	for _, args := range [][]string{{"addr", "add", "10.77.0.2/32", "dev", o.Device}, {"-6", "addr", "add", "fd77::2/128", "dev", o.Device, "nodad"}, {"link", "set", "dev", o.Device, "mtu", "1280", "up"}} {
		if _, err = command(nil, "ip", args...); err != nil {
			return err
		}
	}
	j.Started = true
	if err = save(o, j); err != nil {
		return err
	}
	if err = api(c, "POST", "/v1/vpn/start", map[string]string{"mode": "exit", "device": o.Device, "peer_id": status.Upstream}, nil); err != nil {
		return err
	}
	for _, endpoint := range endpoints {
		family := 4
		prefix := endpoint.IP.String() + "/32"
		if endpoint.IP.Is6() {
			family = 6
			prefix = endpoint.IP.String() + "/128"
		}
		existing, outErr := routes(family, "-j", "route", "show", "exact", prefix)
		if outErr != nil {
			return outErr
		}
		var current []ipRoute
		if json.Unmarshal(existing, &current) != nil {
			return errors.New("cannot inspect bypass routes")
		}
		if len(current) > 0 {
			continue
		}
		found, outErr := routes(family, "-j", "route", "get", endpoint.IP.String())
		if outErr != nil {
			if endpoint.IP.Is6() {
				continue
			}
			return outErr
		}
		var best []ipRoute
		if json.Unmarshal(found, &best) != nil || len(best) == 0 || best[0].Device == "" || best[0].Device == o.Device {
			return errors.New("server underlay route unavailable")
		}
		args := []string{prefix}
		if best[0].Gateway != "" {
			args = append(args, "via", best[0].Gateway)
		}
		args = append(args, "dev", best[0].Device)
		j.Routes = append(j.Routes, route{Family: family, Args: args})
		if err = save(o, j); err != nil {
			return err
		}
		if _, err = routes(family, append([]string{"route", "add"}, args...)...); err != nil {
			return err
		}
	}
	j.Firewall = true
	if err = save(o, j); err != nil {
		return err
	}
	if _, err = command([]byte(firewall(o.Device, endpoints)), "nft", "-f", "-"); err != nil {
		return err
	}
	for _, r := range []route{{4, []string{"0.0.0.0/1", "dev", o.Device}}, {4, []string{"128.0.0.0/1", "dev", o.Device}}, {6, []string{"::/1", "dev", o.Device}}, {6, []string{"8000::/1", "dev", o.Device}}} {
		j.Routes = append(j.Routes, r)
		if err = save(o, j); err != nil {
			return err
		}
		if _, err = routes(r.Family, append([]string{"route", "add"}, r.Args...)...); err != nil {
			return err
		}
	}
	j.DNS = true
	if err = save(o, j); err != nil {
		return err
	}
	if o.DNSMode == "resolved" {
		for _, args := range [][]string{{"dns", o.Device, "10.77.0.1", "fd77::1"}, {"domain", o.Device, "~."}, {"default-route", o.Device, "yes"}, {"flush-caches"}} {
			if _, err = command(nil, "resolvectl", args...); err != nil {
				return err
			}
		}
	} else {
		if err = os.WriteFile("/etc/resolv.conf", []byte("nameserver 10.77.0.1\nnameserver fd77::1\noptions timeout:2 attempts:2\n"), os.FileMode(j.DNSPerm)); err != nil {
			return err
		}
	}
	return nil
}
func firewall(device string, endpoints []underlay) string {
	var b strings.Builder
	fmt.Fprintf(&b, "table inet meshway_vpn { chain output { type filter hook output priority -10; policy accept; oifname \"lo\" accept; oifname \"%s\" accept;\n", device)
	for _, e := range endpoints {
		family := "ip"
		if e.IP.Is6() {
			family = "ip6"
		}
		fmt.Fprintf(&b, "%s daddr %s %s dport %d accept;\n", family, e.IP.String(), e.Transport, e.Port)
	}
	b.WriteString("reject with icmpx type admin-prohibited;\n}\n}\n")
	return b.String()
}
func underlays(c config.Config, status apiStatus) ([]underlay, error) {
	texts := append([]string(nil), c.BootstrapPeers...)
	for _, p := range status.Peers {
		if p.ID == status.Upstream {
			for _, a := range p.Addresses {
				texts = append(texts, a+"/p2p/"+p.ID)
			}
		}
	}
	out := []underlay{}
	seen := map[string]bool{}
	for _, text := range texts {
		info, err := peer.AddrInfoFromString(text)
		if err != nil {
			return nil, errors.New("invalid server invitation")
		}
		for _, address := range info.Addrs {
			ipText, ipErr := address.ValueForProtocol(ma.P_IP4)
			if ipErr != nil {
				ipText, ipErr = address.ValueForProtocol(ma.P_IP6)
			}
			if ipErr != nil {
				return nil, errors.New("full-device VPN requires literal server/relay IP addresses; replace DNS invitations")
			}
			ip, err := netip.ParseAddr(ipText)
			if err != nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
				return nil, errors.New("invalid server underlay address")
			}
			transport := "tcp"
			portText, err := address.ValueForProtocol(ma.P_TCP)
			if err != nil {
				transport = "udp"
				portText, err = address.ValueForProtocol(ma.P_UDP)
			}
			if err != nil {
				continue
			}
			port, err := strconv.Atoi(portText)
			if err != nil || port < 1 || port > 65535 {
				continue
			}
			key := ip.String() + ":" + transport + ":" + portText
			if !seen[key] {
				seen[key] = true
				out = append(out, underlay{ip, transport, uint16(port)})
			}
			if len(out) > 128 {
				return nil, errors.New("too many server transport addresses")
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IP.String() < out[j].IP.String() })
	return out, nil
}

func rollback(o Options, j *journal, c config.Config) error {
	failures := []string{}
	if j.DNS {
		var err error
		if j.DNSMode == "resolved" {
			_, err = command(nil, "resolvectl", "revert", j.Device)
		} else {
			err = os.WriteFile("/etc/resolv.conf", j.DNSBackup, os.FileMode(j.DNSPerm))
		}
		if err != nil {
			failures = append(failures, "DNS restore failed")
		}
	}
	for i := len(j.Routes) - 1; i >= 0; i-- {
		r := j.Routes[i]
		if _, err := routes(r.Family, append([]string{"route", "del"}, r.Args...)...); err != nil {
			body, inspectErr := routes(r.Family, "-j", "route", "show", "exact", r.Args[0])
			var remaining []ipRoute
			if inspectErr != nil || json.Unmarshal(body, &remaining) != nil || len(remaining) > 0 {
				failures = append(failures, "owned route restore failed")
			}
		}
	}
	// Keep the blocking firewall if DNS restoration failed; the journal allows
	// an explicit retry rather than silently exposing traffic with wrong DNS.
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	if j.Started && c.APIListen != "" {
		_ = api(c, "POST", "/v1/vpn/stop", map[string]bool{}, nil)
	}
	if j.Created {
		if _, err := command(nil, "ip", "tuntap", "del", "dev", j.Device, "mode", "tun"); err != nil {
			if _, existsErr := command(nil, "ip", "link", "show", "dev", j.Device); existsErr == nil {
				return err
			}
		}
	}
	if j.Firewall {
		if _, err := command(nil, "nft", "delete", "table", "inet", j.Table); err != nil {
			if _, existsErr := command(nil, "nft", "list", "table", "inet", j.Table); existsErr == nil {
				return err
			}
		}
	}
	return os.Remove(filepath.Join(runtimeDir(o), "journal.json"))
}
func Stop(o Options) error {
	guard, err := lock(o)
	if err != nil {
		return err
	}
	defer guard.Close()
	j, err := load(o)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	c, _, _ := readConfig(j.Config)
	return rollback(o, &j, c)
}
func Status(o Options) (string, error) {
	guard, err := lock(o)
	if err != nil {
		return "", err
	}
	defer guard.Close()
	j, err := load(o)
	if os.IsNotExist(err) {
		return "Device VPN is stopped.", nil
	}
	if err != nil {
		return "", err
	}
	return "Device VPN protection is installed on " + j.Device + ". Run meshway vpn stop to restore routes and DNS.", nil
}
