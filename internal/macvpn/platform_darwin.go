//go:build darwin && !ios

package macvpn

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/user-github-me/meshway/internal/config"
	"golang.org/x/sys/unix"
)

const runtimeDefault = "/private/var/db/meshway-macvpn"

type journal struct {
	Version         int    `json:"version"`
	PID             int    `json:"pid"`
	ProcessStart    string `json:"process_start"`
	Device          string `json:"device"`
	PeerID          string `json:"peer_id"`
	Active          bool   `json:"active"`
	Steps           []step `json:"steps"`
	Marker          uint32 `json:"marker"`
	SessionID       string `json:"session_id"`
	ConfigPath      string `json:"config_path"`
	OwnerUID        uint32 `json:"owner_uid"`
	CaptureInstance string `json:"capture_instance,omitempty"`
}
type systemExecutor struct {
	liveDevice string
	marker     uint32
	backend    executor
}

func (e systemExecutor) execute(ctx context.Context, c command) ([]byte, error) {
	if e.backend != nil {
		return e.backend.execute(ctx, c)
	}
	if c.Name != "/sbin/route" && c.Name != "/sbin/ifconfig" && c.Name != "/usr/sbin/scutil" && c.Name != "/usr/sbin/netstat" && c.Name != "/bin/ps" {
		return nil, errors.New("unsupported helper command")
	}
	limited, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(limited, c.Name, c.Args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C", "LC_ALL=C"}
	cmd.WaitDelay = time.Second
	if c.Input != "" {
		cmd.Stdin = strings.NewReader(c.Input)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s failed: %w", filepath.Base(c.Name), err)
	}
	return out, nil
}
func runtimeDir(o Options) string {
	if o.RuntimeDir != "" {
		return o.RuntimeDir
	}
	return runtimeDefault
}
func protectedDir(o Options) error {
	if os.Geteuid() != 0 {
		return errors.New("macOS device VPN needs explicit administrator authorization")
	}
	dir := runtimeDir(o)
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return errors.New("runtime directory must be an absolute clean path")
	}
	for p := filepath.Dir(dir); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
			return errors.New("runtime ancestors must be protected root directories")
		}
		if p == "/" {
			break
		}
	}
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("runtime directory must be root-owned and private")
	}
	return nil
}

// Every path component is opened without following symlinks. The same final
// descriptor is checked and decoded, so an app-owned parent replacement cannot
// redirect the privileged helper to another user's private configuration.
func loadPrivateConfig(path string) (config.Config, uint32, error) {
	var zero config.Config
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return zero, 0, errors.New("use an absolute clean private configuration path")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return zero, 0, err
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for _, part := range parts[:len(parts)-1] {
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if err != nil {
			return zero, 0, errors.New("configuration ancestors must be actual directories without symlinks")
		}
		fd = next
	}
	fileFD, err := unix.Openat(fd, parts[len(parts)-1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	unix.Close(fd)
	if err != nil {
		return zero, 0, err
	}
	f := os.NewFile(uintptr(fileFD), "private-config")
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return zero, 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || st.Uid == 0 || st.Nlink != 1 || info.Size() > 1<<20 {
		return zero, 0, errors.New("configuration must be a single-link app-user-owned private regular file")
	}
	c, err := config.Decode(f)
	return c, st.Uid, err
}
func lock(o Options) (*os.File, error) {
	if err := protectedDir(o); err != nil {
		return nil, err
	}
	fd, err := unix.Open(filepath.Join(runtimeDir(o), "lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "macvpn-lock")
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || st.Uid != 0 || info.Mode().Perm()&0077 != 0 || st.Nlink != 1 {
		f.Close()
		return nil, errors.New("invalid private helper lock")
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("macOS VPN helper already running")
	}
	return f, nil
}
func saveJournal(o Options, j journal) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	path := filepath.Join(runtimeDir(o), "journal.json")
	f, err := os.CreateTemp(runtimeDir(o), "journal-*.tmp")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(temp, path); err != nil {
		return err
	}
	return syncRuntime(o)
}
func syncRuntime(o Options) error {
	f, err := os.Open(runtimeDir(o))
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func processOwner(pid int) (uint32, error) {
	out, err := systemExecutor{}.execute(context.Background(), command{Name: "/bin/ps", Args: []string{"-p", strconv.Itoa(pid), "-o", "uid="}})
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 32)
	return uint32(n), err
}
func processStart(pid int) string {
	out, err := systemExecutor{}.execute(context.Background(), command{Name: "/bin/ps", Args: []string{"-p", strconv.Itoa(pid), "-o", "lstart="}})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
func readJournal(o Options) (journal, error) {
	var j journal
	fd, err := unix.Open(filepath.Join(runtimeDir(o), "journal.json"), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return j, err
	}
	f := os.NewFile(uintptr(fd), "vpn-journal")
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return j, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || st.Uid != 0 || info.Mode().Perm()&0077 != 0 || st.Nlink != 1 {
		return j, errors.New("invalid private VPN journal")
	}
	data, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil {
		return j, err
	}
	if len(data) > 64*1024 {
		return j, errors.New("oversized VPN journal")
	}
	err = json.Unmarshal(data, &j)
	if err == nil && (j.Version != 3 || !validDevice(j.Device) || j.PID <= 1 || j.Marker == 0 || len(j.Steps) > 128 || !validSessionID(j.SessionID) || !filepath.IsAbs(j.ConfigPath) || filepath.Clean(j.ConfigPath) != j.ConfigPath || j.OwnerUID == 0) {
		err = errors.New("invalid VPN journal")
	}
	return j, err
}
func Stop(o Options) error {
	if err := protectedDir(o); err != nil {
		return err
	}
	j, err := readJournal(o)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = journalCaller(o, j); err != nil {
		return err
	}
	owner, ownerErr := processOwner(j.PID)
	if j.PID > 1 && j.ProcessStart != "" && ownerErr == nil && owner == 0 && processStart(j.PID) == j.ProcessStart {
		fd, err := unix.Open(filepath.Join(runtimeDir(o), "stop"), unix.O_CREAT|unix.O_WRONLY|unix.O_TRUNC|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
		if err != nil {
			return err
		}
		f := os.NewFile(uintptr(fd), "stop-marker")
		_, err = f.Write([]byte("stop"))
		if err == nil {
			err = f.Sync()
		}
		f.Close()
		return err
	}
	f, err := lock(o)
	if err != nil {
		return err
	}
	defer f.Close()
	return restore(o, &j, systemExecutor{marker: j.Marker})
}
func Status(o Options) (string, error) {
	if err := protectedDir(o); err != nil {
		return "", err
	}
	j, err := readJournal(o)
	if os.IsNotExist(err) {
		return `{"active":false}`, nil
	}
	if err != nil {
		return "", err
	}
	if err = journalCaller(o, j); err != nil {
		return "", err
	}
	running := j.ProcessStart != "" && processStart(j.PID) == j.ProcessStart
	data, _ := json.Marshal(map[string]any{"active": j.Active && running, "running": running, "device": j.Device, "peer_id": j.PeerID, "network_cleanup_pending": !running && (len(j.Steps) > 0 || j.SessionID != "")})
	return string(data), nil
}

func journalCaller(o Options, j journal) error {
	if o.ConfigPath == "" || o.ConfigPath != j.ConfigPath || !filepath.IsAbs(o.ConfigPath) || filepath.Clean(o.ConfigPath) != o.ConfigPath {
		return errors.New("VPN recovery belongs to a different configuration")
	}
	_, uid, err := loadPrivateConfig(o.ConfigPath)
	if err != nil {
		return errors.New("VPN recovery requires its original private configuration")
	}
	if uid != j.OwnerUID {
		return errors.New("VPN recovery configuration owner changed")
	}
	if o.CaptureInstance != "" && (!validSessionID(o.CaptureInstance) || o.CaptureInstance != j.CaptureInstance) {
		return errors.New("VPN recovery belongs to a different capture instance")
	}
	return nil
}

type routeInfo struct {
	Destination, Gateway, Interface, Mask, Flags string
	Marker                                       uint32
}

func parseRoute(out []byte) routeInfo {
	r := routeInfo{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(fields) != 2 {
			continue
		}
		value := strings.TrimSpace(fields[1])
		switch fields[0] {
		case "destination":
			r.Destination = value
		case "gateway":
			r.Gateway = value
		case "interface":
			r.Interface = value
		case "mask":
			r.Mask = value
		case "flags":
			r.Flags = value
		}
	}
	lines := strings.Split(string(out), "\n")
	for i, line := range lines {
		fields := strings.Fields(line)
		for column, value := range fields {
			if value == "hopcount" && i+1 < len(lines) {
				metrics := strings.Fields(lines[i+1])
				if column < len(metrics) {
					v, _ := strconv.ParseUint(metrics[column], 10, 32)
					r.Marker = uint32(v)
				}
			}
		}
	}
	return r
}
func routeGet(e executor, ip string) (routeInfo, error) {
	out, err := e.execute(context.Background(), command{Name: "/sbin/route", Args: []string{"-n", "get", ip}})
	if err != nil {
		return routeInfo{}, err
	}
	r := parseRoute(out)
	if r.Interface == "" {
		return r, errors.New("underlay has no existing route")
	}
	return r, nil
}
func routeFlag(flags, flag string) bool {
	for _, value := range strings.Split(strings.Trim(flags, "<>"), ",") {
		if strings.TrimSpace(value) == flag {
			return true
		}
	}
	return false
}

// A persistent bypass must win the effective lookup after split routes are
// installed. Darwin invalidates clones when those routes change.
func verifyUnderlays(underlays []string, e executor) error {
	for _, underlay := range underlays {
		route, err := routeGet(e, underlay)
		if err != nil || strings.HasPrefix(route.Interface, "utun") {
			return errors.New("encrypted peer underlay does not bypass the VPN capture; restoring owned changes")
		}
	}
	return nil
}

func routePlan(device string, underlays []string, e executor, marker uint32) ([]step, error) {
	if !validDevice(device) {
		return nil, errors.New("invalid owned utun device")
	}
	if len(underlays) == 0 || len(underlays) > 32 {
		return nil, errors.New("explicit transport underlay IPs are required")
	}
	steps := []step{}
	seen := map[string]bool{}
	for _, value := range underlays {
		ip, err := netip.ParseAddr(value)
		if err != nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
			return nil, errors.New("underlay must be an explicit unicast transport IP")
		}
		value = ip.Unmap().String()
		if seen[value] {
			continue
		}
		seen[value] = true
		old, err := routeGet(e, value)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(old.Interface, "utun") {
			return nil, errors.New("underlay already routes through another VPN tunnel")
		}
		// Darwin route-get returns an exact destination for a transient route
		// cloned from the physical default. Adding /1 routes discards that
		// clone, so it cannot serve as a persistent transport bypass.
		if old.Destination == value && routeFlag(old.Flags, "HOST") && routeFlag(old.Flags, "STATIC") && !routeFlag(old.Flags, "WASCLONED") {
			continue
		} // Preserve an existing explicit static host route.
		family := "-inet"
		if ip.Is6() {
			family = "-inet6"
		}
		suffix := []string{"-interface", old.Interface}
		if gateway, err := netip.ParseAddr(strings.Split(old.Gateway, "%")[0]); err == nil && !gateway.IsUnspecified() {
			suffix = []string{"-gateway", old.Gateway}
		}
		args := append([]string{"-n", "add", family, "-host", value}, suffix...)
		args = append(args, "-proto1", "-proto2", "-hopcount", strconv.FormatUint(uint64(marker), 10))
		undo := append([]string{"-n", "delete", family, "-host", value}, suffix...)
		steps = append(steps, step{Apply: command{Name: "/sbin/route", Args: args}, Undo: command{Name: "/sbin/route", Args: undo}})
	}
	for _, entry := range []struct{ family, prefix string }{{"-inet", "0.0.0.0/1"}, {"-inet", "128.0.0.0/1"}, {"-inet6", "::/1"}, {"-inet6", "8000::/1"}} {
		steps = append(steps, step{Apply: command{Name: "/sbin/route", Args: []string{"-n", "add", entry.family, "-net", entry.prefix, "-interface", device, "-proto1", "-proto2", "-hopcount", strconv.FormatUint(uint64(marker), 10)}}, Undo: command{Name: "/sbin/route", Args: []string{"-n", "delete", entry.family, "-net", entry.prefix, "-interface", device}}})
	}
	return steps, nil
}

func argument(args []string, key string) string {
	for i, a := range args {
		if a == key && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
func (e systemExecutor) inspect(ctx context.Context, s step) (bool, error) {
	switch s.Apply.Name {
	case "/sbin/ifconfig":
		if len(s.Apply.Args) == 0 || !validDevice(s.Apply.Args[0]) {
			return false, errors.New("invalid journal interface")
		}
		// A live descriptor cannot be reused. Once the owner died, its utun
		// interface is gone; any interface with the same name belongs elsewhere.
		return e.liveDevice == s.Apply.Args[0], nil
	case "/usr/sbin/scutil":
		fields := strings.Fields(s.Undo.Input)
		if len(fields) != 3 || fields[0] != "remove" || fields[2] != "quit" || !strings.HasPrefix(fields[1], "State:/Network/Service/Meshway-") || !strings.HasSuffix(fields[1], "/DNS") {
			return false, errors.New("invalid journal DNS key")
		}
		out, err := e.execute(ctx, command{Name: "/usr/sbin/scutil", Input: "show " + fields[1] + "\nquit\n"})
		if err != nil {
			return false, err
		}
		if strings.Contains(string(out), "No such key") {
			return false, nil
		}
		if !strings.Contains(string(out), "<dictionary>") {
			return false, errors.New("unexpected resolver ownership probe")
		}
		keyName := strings.TrimSuffix(strings.TrimPrefix(fields[1], "State:/Network/Service/"), "/DNS")
		if scutilValue(out, "MeshwayOwner") != keyName {
			return false, nil
		}
		if iface := scutilValue(out, "InterfaceName"); !validDevice(iface) {
			return false, nil
		}
		servers := regexp.MustCompile(`(?s)ServerAddresses\s*:\s*<array>\s*\{([^}]*)\}`).FindSubmatch(out)
		if len(servers) != 2 {
			return false, nil
		}
		values := regexp.MustCompile(`\d+\s*:\s*(\S+)`).FindAllSubmatch(servers[1], -1)
		if len(values) != 2 || string(values[0][1]) != "10.77.0.1" || string(values[1][1]) != "fd77::1" {
			return false, nil
		}
		if expected := scutilSetupValue(s.Apply.Input, "InterfaceName"); expected == "" || scutilValue(out, "InterfaceName") != expected {
			return false, nil
		}
		return true, nil
	case "/sbin/route":
		args := s.Apply.Args
		if len(args) < 7 || args[0] != "-n" || args[1] != "add" || (args[2] != "-inet" && args[2] != "-inet6") || (args[3] != "-host" && args[3] != "-net") {
			return false, errors.New("invalid journal route")
		}
		marker, err := strconv.ParseUint(argument(args, "-hopcount"), 10, 32)
		if err != nil || marker == 0 || uint32(marker) != e.marker {
			return false, errors.New("invalid route ownership marker")
		}
		out, err := e.execute(ctx, command{Name: "/sbin/route", Args: []string{"-n", "get", args[2], args[3], args[4]}})
		if err != nil {
			// A missing family/default route can make route-get fail even after
			// this exact route was already removed. Positive table enumeration
			// distinguishes absence from command failure; an existing matching
			// entry with an unreadable marker stays journaled for a safe retry.
			family := "inet"
			if args[2] == "-inet6" {
				family = "inet6"
			}
			table, probeErr := e.execute(ctx, command{Name: "/usr/sbin/netstat", Args: []string{"-rn", "-f", family}})
			if probeErr != nil {
				return false, errors.Join(err, probeErr)
			}
			if !strings.Contains(string(table), "Destination") || !strings.Contains(string(table), "Gateway") {
				return false, errors.Join(err, errors.New("unexpected route table ownership probe"))
			}
			target := args[4]
			if args[3] == "-host" {
				if ip, parseErr := netip.ParseAddr(target); parseErr == nil {
					target = netip.PrefixFrom(ip, ip.BitLen()).String()
				}
			}
			for _, line := range strings.Split(string(table), "\n") {
				fields := strings.Fields(line)
				if len(fields) > 0 && normalizeRouteDestination(fields[0]) == target {
					return false, err
				}
			}
			return false, nil
		}
		r := parseRoute(out)
		if r.Destination == "" || r.Interface == "" || r.Flags == "" {
			return false, errors.New("unexpected route ownership probe")
		}
		target := strings.Split(args[4], "/")[0]
		destination := r.Destination
		// route-get names the zero half-space "default" even when its mask
		// is /1. Marker, exact mask and interface still distinguish it from
		// the system /0 default; no broader route is accepted as our own.
		if destination == "default" && args[3] == "-net" {
			if args[2] == "-inet" {
				destination = "0.0.0.0"
			} else {
				destination = "::"
			}
		}
		if destination != target {
			return false, nil
		}
		if args[3] == "-host" && !strings.Contains(r.Flags, "HOST") {
			return false, nil
		}
		if args[3] == "-net" {
			mask := "128.0.0.0"
			if args[2] == "-inet6" {
				mask = "8000::"
			}
			if r.Mask != mask {
				return false, nil
			}
		}
		if r.Marker != e.marker || !strings.Contains(r.Flags, "PROTO1") || !strings.Contains(r.Flags, "PROTO2") {
			return false, nil
		}
		if iface := argument(args, "-interface"); iface != "" && r.Interface != iface {
			return false, nil
		}
		if gateway := argument(args, "-gateway"); gateway != "" && r.Gateway != gateway {
			return false, nil
		}
		return true, nil
	default:
		return false, errors.New("unsupported journal resource")
	}
}

func normalizeRouteDestination(value string) string {
	if ip, err := netip.ParseAddr(value); err == nil {
		return netip.PrefixFrom(ip, ip.BitLen()).String()
	}
	if p, err := netip.ParsePrefix(value); err == nil {
		return p.Masked().String()
	}
	parts := strings.Split(value, "/")
	if len(parts) != 2 || strings.Contains(parts[0], ":") {
		return value
	}
	fields := strings.Split(parts[0], ".")
	if len(fields) > 4 {
		return value
	}
	for len(fields) < 4 {
		fields = append(fields, "0")
	}
	if p, err := netip.ParsePrefix(strings.Join(fields, ".") + "/" + parts[1]); err == nil {
		return p.Masked().String()
	}
	return value
}

func scutilValue(out []byte, key string) string {
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(fields) == 2 && strings.TrimSpace(fields[0]) == key {
			return strings.TrimSpace(fields[1])
		}
	}
	return ""
}
func scutilSetupValue(input, key string) string {
	for _, line := range strings.Split(input, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "d.add" && fields[1] == key {
			return fields[2]
		}
	}
	return ""
}
func restore(o Options, j *journal, e executor) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	networkErr := rollback(ctx, e, j.Steps, func(remaining []step) error {
		next := *j
		next.Steps = remaining
		next.Active = false
		if err := saveJournal(o, next); err != nil {
			return err
		}
		*j = next
		return nil
	})
	packetErr := cleanupPacketSession(*j)
	if err := errors.Join(networkErr, packetErr); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(runtimeDir(o), "journal.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(filepath.Join(runtimeDir(o), "stop")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncRuntime(o)
}

func validSessionID(value string) bool {
	if len(value) != 32 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// Session ownership is recovered from the protected journal after a helper
// crash. Configuration credentials are read only from the journal's original
// user's private file; a caller-supplied path cannot redirect the recovery API.
func cleanupPacketSession(j journal) error {
	if !validSessionID(j.SessionID) || j.OwnerUID == 0 || j.ConfigPath == "" {
		return errors.New("packet session recovery journal is invalid")
	}
	c, owner, err := loadPrivateConfig(j.ConfigPath)
	if err != nil {
		return errors.New("packet session cleanup needs the original private configuration")
	}
	if owner != j.OwnerUID {
		return errors.New("packet session cleanup configuration owner changed")
	}
	api, err := newPacketAPI(c)
	if err != nil {
		return err
	}
	api.session = j.SessionID
	defer api.client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	_, status, err := api.request(ctx, "/v1/vpn/stop", []byte("{}"), "application/json")
	// Conflict means the old owner has already been removed, or a newer native
	// client owns the daemon. The endpoint refuses to stop that other session.
	if status == 409 {
		return nil
	}
	if err != nil {
		return errors.New("owned daemon packet session cleanup needs retry")
	}
	return nil
}

type packetAPI struct {
	url, token string
	session    string
	client     *http.Client
}

func newPacketAPI(c config.Config) (packetAPI, error) {
	host, port, err := net.SplitHostPort(c.APIListen)
	ip, parseErr := netip.ParseAddr(host)
	if err != nil || parseErr != nil || !ip.IsLoopback() {
		return packetAPI{}, errors.New("daemon API must use an explicit loopback IP")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return packetAPI{}, errors.New("daemon API requires a fixed port")
	}
	return packetAPI{url: "http://" + c.APIListen, token: c.APIToken, client: &http.Client{Timeout: 6 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (a *packetAPI) request(ctx context.Context, path string, body []byte, content string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", a.url+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	if a.session != "" {
		req.Header.Set("X-Meshway-VPN-Session", a.session)
	}
	if content != "" {
		req.Header.Set("Content-Type", content)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 9001))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if len(data) > 9000 {
		return nil, resp.StatusCode, errors.New("oversized packet API response")
	}
	if resp.StatusCode >= 300 {
		return nil, resp.StatusCode, fmt.Errorf("packet API HTTP %d", resp.StatusCode)
	}
	return data, resp.StatusCode, nil
}
func preflightRoutes(e executor) error {
	for _, family := range []string{"inet", "inet6"} {
		out, err := e.execute(context.Background(), command{Name: "/usr/sbin/netstat", Args: []string{"-rn", "-f", family}})
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) > 0 && (f[0] == "0/1" || f[0] == "0.0.0.0/1" || f[0] == "128.0/1" || f[0] == "128.0.0.0/1" || f[0] == "::/1" || f[0] == "8000::/1") {
				return errors.New("an existing VPN owns split default routes; disconnect it before using Meshway utun")
			}
		}
	}
	return nil
}
func Run(ctx context.Context, o Options) (result error) {
	c, ownerUID, err := loadPrivateConfig(o.ConfigPath)
	if err != nil {
		return err
	}
	f, err := lock(o)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = readJournal(o); err == nil {
		return errors.New("existing VPN journal requires explicit stop/cleanup before starting")
	} else if !os.IsNotExist(err) {
		return err
	}
	if o.PeerID == "" {
		o.PeerID = c.UpstreamPeer
	}
	if _, err = peer.Decode(o.PeerID); err != nil {
		return errors.New("choose a valid pinned exit peer")
	}
	if c.APIListen == "" {
		return errors.New("start the user-owned Meshway daemon with a loopback API first")
	}
	parentUID, parentErr := processOwner(o.ParentPID)
	if o.ParentPID <= 1 || processStart(o.ParentPID) == "" || parentErr != nil || parentUID != ownerUID {
		return errors.New("a live owning application PID from the configuration's user is required")
	}
	parentStart := processStart(o.ParentPID)
	e := systemExecutor{}
	if err = preflightRoutes(e); err != nil {
		return err
	}
	api, err := newPacketAPI(c)
	if err != nil {
		return err
	}
	defer api.client.CloseIdleConnections()
	sessionBytes := make([]byte, 16)
	if _, err = rand.Read(sessionBytes); err != nil {
		return err
	}
	api.session = hex.EncodeToString(sessionBytes)
	payload, _ := json.Marshal(map[string]string{"mode": "exit", "peer_id": o.PeerID, "session_id": api.session})
	ownedSession := false
	defer func() {
		if !ownedSession {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, _, err := api.request(cleanup, "/v1/vpn/stop", []byte("{}"), "application/json"); err != nil {
			result = errors.Join(result, errors.New("daemon packet session cleanup failed"))
		}
	}()
	if _, _, err = api.request(ctx, "/v1/vpn/start", payload, "application/json"); err != nil {
		probeCtx, probeCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer probeCancel()
		r, probeErr := http.NewRequestWithContext(probeCtx, "GET", api.url+"/v1/vpn/status", nil)
		if probeErr == nil {
			r.Header.Set("Authorization", "Bearer "+api.token)
			r.Header.Set("X-Meshway-VPN-Session", api.session)
			probeClient := *api.client
			probeClient.Timeout = 20 * time.Second
			response, probeErr := probeClient.Do(r)
			if probeErr == nil {
				ownedSession = response.StatusCode == 200
				response.Body.Close()
			}
		}
		return err
	}
	ownedSession = true
	device, name, err := openUTUN()
	if err != nil {
		return err
	}
	defer device.Close()
	id := make([]byte, 12)
	if _, err = rand.Read(id); err != nil {
		return err
	}
	dnsKey := "State:/Network/Service/Meshway-" + hex.EncodeToString(id) + "/DNS"
	marker := uint32(id[0])<<24 | uint32(id[1])<<16 | uint32(id[2])<<8 | uint32(id[3])
	marker &= 0x7fffffff
	if marker == 0 {
		marker = 1
	}
	instance := o.CaptureInstance
	if instance == "" {
		instance = hex.EncodeToString(id) + hex.EncodeToString(id[:4])
	}
	if !validSessionID(instance) {
		return errors.New("capture readiness instance must have 32 hex characters")
	}
	j := journal{Version: 3, PID: os.Getpid(), ProcessStart: processStart(os.Getpid()), Device: name, PeerID: o.PeerID, Marker: marker, SessionID: api.session, ConfigPath: o.ConfigPath, OwnerUID: ownerUID, CaptureInstance: instance}
	e.liveDevice, e.marker = name, marker
	if err = saveJournal(o, j); err != nil {
		return err
	}
	defer func() {
		if err := restore(o, &j, e); err != nil {
			result = errors.Join(result, fmt.Errorf("network cleanup needs retry: %w", err))
		}
		// restore recovered the exact owned session, or retained its durable
		// journal. The earlier fallback is only for failures before a journal.
		ownedSession = false
	}()
	plan, err := routePlan(name, o.UnderlayIPs, e, marker)
	if err != nil {
		return err
	}
	setup := []step{{Apply: command{Name: "/sbin/ifconfig", Args: []string{name, "inet", "10.77.0.2", "10.77.0.2", "netmask", "255.255.255.255", "mtu", "1280", "up"}}, Undo: command{Name: "/sbin/ifconfig", Args: []string{name, "down"}}}, {Apply: command{Name: "/sbin/ifconfig", Args: []string{name, "inet6", "fd77::2", "prefixlen", "128"}}, Undo: command{Name: "/sbin/ifconfig", Args: []string{name, "inet6", "fd77::2", "-alias"}}}}
	setup = append(setup, plan...)
	setup = append(setup, step{Apply: command{Name: "/usr/sbin/scutil", Input: "d.init\nd.add ServerAddresses * 10.77.0.1 fd77::1\nd.add SupplementalMatchDomains * \"\"\nd.add SupplementalMatchOrders * 1\nd.add InterfaceName " + name + "\nd.add MeshwayOwner Meshway-" + hex.EncodeToString(id) + "\nset " + dnsKey + "\nquit\n"}, Undo: command{Name: "/usr/sbin/scutil", Input: "remove " + dnsKey + "\nquit\n"}})
	if err = apply(ctx, e, setup, func(i int, s step) error {
		next := j
		next.Steps = append([]step(nil), j.Steps...)
		if i == len(next.Steps) {
			next.Steps = append(next.Steps, s)
		} else {
			next.Steps[i] = s
		}
		if err := saveJournal(o, next); err != nil {
			return err
		}
		j = next
		return nil
	}); err != nil {
		return err
	}
	for _, s := range j.Steps {
		owned, err := e.inspect(ctx, s)
		if err != nil {
			return err
		}
		if !owned {
			detail := filepath.Base(s.Apply.Name)
			if s.Apply.Name == "/sbin/route" && len(s.Apply.Args) >= 5 {
				out, probeErr := e.execute(ctx, command{Name: "/sbin/route", Args: []string{"-n", "get", s.Apply.Args[2], s.Apply.Args[3], s.Apply.Args[4]}})
				if probeErr == nil {
					r := parseRoute(out)
					detail = fmt.Sprintf("route family=%s target=%s destination=%s mask=%s interface=%s flags=%s marker=%d expected_marker=%d", s.Apply.Args[2], s.Apply.Args[4], r.Destination, r.Mask, r.Interface, r.Flags, r.Marker, e.marker)
				}
			}
			return fmt.Errorf("network setup ownership could not be verified (%s); restoring the owned changes", detail)
		}
	}
	if err = verifyUnderlays(o.UnderlayIPs, e); err != nil {
		return err
	}
	j.Active = true
	if err = saveJournal(o, j); err != nil {
		return err
	}
	readyPayload, _ := json.Marshal(map[string]any{"ready": true, "device": name, "instance": instance})
	if _, _, err = api.request(ctx, "/v1/vpn/capture-ready", readyPayload, "application/json"); err != nil {
		return errors.New("daemon could not confirm verified device capture")
	}
	defer func() {
		clearCtx, clearCancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer clearCancel()
		_, _, _ = api.request(clearCtx, "/v1/vpn/capture-ready", []byte(`{"ready":false}`), "application/json")
	}()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	fail := make(chan error, 2)
	workers.Add(2)
	go func() {
		defer workers.Done()
		buffer := make([]byte, 9004)
		diagnostic := os.Getenv("GITHUB_ACTIONS") == "true" && os.Getenv("MESHWAY_DISPOSABLE_MACOS_VM") == "1" && os.Getenv("MESHWAY_UTUN_PACKET_DIAGNOSTICS") == "1"
		seen := map[byte]bool{}
		writeFailureReported := false
		for {
			n, err := device.Read(buffer)
			if err != nil {
				if runCtx.Err() == nil {
					fail <- err
				}
				return
			}
			packet, err := decodeFrame(buffer[:n], 1280)
			if err != nil {
				continue
			}
			if diagnostic && !seen[packet[0]>>4] {
				family := packet[0] >> 4
				ipHeader, proto, source, destination := 20, packet[9], packet[12:16], packet[16:20]
				if family == 6 {
					ipHeader, proto, source, destination = 40, packet[6], packet[8:24], packet[24:40]
				} else {
					ipHeader = int(packet[0]&15) * 4
				}
				if proto == 17 && len(packet) >= ipHeader+8 && binary.BigEndian.Uint16(packet[ipHeader+2:ipHeader+4]) == 53 {
					dns := net.IP(destination).String()
					if dns == "10.77.0.1" || dns == "fd77::1" {
						seen[family] = true
						udp := packet[ipHeader:]
						pseudo := append([]byte{}, source...)
						pseudo = append(pseudo, destination...)
						if family == 4 {
							pseudo = append(pseudo, 0, 17, byte(len(udp)>>8), byte(len(udp)))
						} else {
							pseudo = append(pseudo, 0, 0, byte(len(udp)>>8), byte(len(udp)), 0, 0, 0, 17)
						}
						pseudo = append(pseudo, udp...)
						data, _ := json.Marshal(map[string]any{"meshway_packet_diagnostic": true, "family": family, "source_is_virtual_client": net.IP(source).String() == "10.77.0.2" || net.IP(source).String() == "fd77::2", "udp_checksum_valid": packetChecksum(pseudo) == 0xffff, "udp_checksum": binary.BigEndian.Uint16(udp[6:8]), "ipv4_header_checksum_valid": family != 4 || packetChecksum(packet[:ipHeader]) == 0xffff, "packet_length": len(packet), "udp_length": binary.BigEndian.Uint16(udp[4:6])})
						fmt.Fprintln(os.Stderr, string(data))
					}
				}
			}
			if _, _, err = api.request(runCtx, "/v1/vpn/write", packet, "application/octet-stream"); err != nil && runCtx.Err() == nil {
				if diagnostic && !writeFailureReported {
					writeFailureReported = true
					fmt.Fprintln(os.Stderr, `{"meshway_packet_diagnostic":true,"write_failed":true}`)
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()
	go func() {
		defer workers.Done()
		readFailureReported := false
		diagnostic := os.Getenv("GITHUB_ACTIONS") == "true" && os.Getenv("MESHWAY_DISPOSABLE_MACOS_VM") == "1" && os.Getenv("MESHWAY_UTUN_PACKET_DIAGNOSTICS") == "1"
		for {
			packet, status, err := api.request(runCtx, "/v1/vpn/read", nil, "")
			if runCtx.Err() != nil {
				return
			}
			if err != nil {
				if diagnostic && !readFailureReported {
					readFailureReported = true
					fmt.Fprintln(os.Stderr, `{"meshway_packet_diagnostic":true,"read_failed":true}`)
				}
				time.Sleep(200 * time.Millisecond)
				continue
			}
			if status == 204 {
				continue
			}
			frame, err := encodeFrame(packet, 1280)
			if err != nil {
				continue
			}
			if _, err = device.Write(frame); err != nil {
				fail <- err
				return
			}
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer func() { cancel(); device.cancelReaders(); workers.Wait() }()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-fail:
			return err
		case <-ticker.C:
			if _, err = os.Stat(filepath.Join(runtimeDir(o), "stop")); err == nil {
				return nil
			}
			currentUID, uidErr := processOwner(o.ParentPID)
			if uidErr != nil || currentUID != ownerUID || processStart(o.ParentPID) != parentStart {
				return errors.New("owning application stopped; restoring network")
			}
		}
	}
}

func packetChecksum(data []byte) uint16 {
	var sum uint32
	for len(data) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(data))
		data = data[2:]
	}
	if len(data) == 1 {
		sum += uint32(data[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return uint16(sum)
}
