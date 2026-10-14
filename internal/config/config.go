package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/user-github-me/meshway/internal/state"
	ma "github.com/multiformats/go-multiaddr"
)

type Forward struct {
	Name    string `json:"name"`
	Listen  string `json:"listen"`
	Target  string `json:"target,omitempty"`
	Peer    string `json:"peer,omitempty"`
	Service string `json:"service,omitempty"`
}

type Service struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

type VPN struct {
	Enabled     bool   `json:"enabled"`
	MTU         int    `json:"mtu"`
	Device      string `json:"device,omitempty"`
	Peer        string `json:"peer,omitempty"`
	Internet    bool   `json:"internet"`
	DNSResolver string `json:"dns_resolver,omitempty"`
}

type Limits struct {
	MaxSessions    int `json:"max_sessions,omitempty"`
	BytesPerSecond int `json:"bytes_per_second,omitempty"`
	MaxVPNSessions int `json:"max_vpn_sessions,omitempty"`
	MaxFlowsPerVPN int `json:"max_flows_per_vpn,omitempty"`
}

type Config struct {
	AutoDiscovery       bool      `json:"auto_discovery"`
	Limits              *Limits   `json:"limits,omitempty"`
	StorageKey          []byte    `json:"-"`
	StateDir            string    `json:"state_dir"`
	ListenAddresses     []string  `json:"listen_addresses"`
	BootstrapPeers      []string  `json:"bootstrap_peers"`
	UpstreamPeer        string    `json:"upstream_peer"`
	SOCKSListen         string    `json:"socks_listen"`
	APIListen           string    `json:"api_listen"`
	APIToken            string    `json:"api_token"`
	AllowedOrigins      []string  `json:"allowed_origins"`
	Relay               bool      `json:"relay"`
	Exit                bool      `json:"exit"`
	AllowPrivateTargets bool      `json:"allow_private_targets"`
	AllowedPeers        []string  `json:"allowed_peers"`
	Forwards            []Forward `json:"forwards"`
	Services            []Service `json:"services"`
	VPN                 *VPN      `json:"vpn,omitempty"`
	AccountSync         bool      `json:"account_sync"`
	DeviceLabel         string    `json:"device_label,omitempty"`
	Advertise           bool      `json:"advertise"`
	ServerLabel         string    `json:"server_label,omitempty"`
	ServerCountry       string    `json:"server_country,omitempty"`
	PersonalServer      bool      `json:"personal_server"`
}

func Default(stateDir string) (Config, error) {
	key, err := state.KeyFromEnvironment()
	if err != nil {
		return Config{}, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return Config{}, err
	}
	return Config{StorageKey: key, StateDir: stateDir, ListenAddresses: []string{"/ip4/0.0.0.0/tcp/4001", "/ip4/0.0.0.0/udp/4001/quic-v1"}, BootstrapPeers: []string{}, SOCKSListen: "127.0.0.1:1080", APIListen: "127.0.0.1:7777", APIToken: base64.RawURLEncoding.EncodeToString(b), AllowedOrigins: []string{"http://localhost:5173", "http://127.0.0.1:5173"}, AllowedPeers: []string{}, Forwards: []Forward{}, Services: []Service{}}, nil
}

func Decode(r io.Reader) (Config, error) {
	var c Config
	d := json.NewDecoder(io.LimitReader(r, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return c, errors.New("configuration must contain one JSON object")
	}
	return c, c.Validate()
}

func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	c, err := Decode(f)
	if err != nil {
		return c, err
	}
	c.StorageKey, err = state.KeyFromEnvironment()
	return c, err
}

func Save(path string, c Config, force bool) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	e := json.NewEncoder(f)
	e.SetIndent("", "  ")
	return e.Encode(c)
}

func LoopbackAddress(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("listener must use an explicit loopback IP address")
	}
	if port == "" {
		return errors.New("missing port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return errors.New("invalid listener port")
	}
	return nil
}

func (c Config) Validate() error {
	if c.Limits != nil {
		if c.Limits.MaxSessions < 0 || c.Limits.MaxSessions > 1024 || c.Limits.BytesPerSecond < 0 || c.Limits.BytesPerSecond > 1<<30 || c.Limits.MaxVPNSessions < 0 || c.Limits.MaxVPNSessions > 128 || c.Limits.MaxFlowsPerVPN < 0 || c.Limits.MaxFlowsPerVPN > 1024 {
			return errors.New("limits exceed permitted session, flow, or bandwidth bounds")
		}
	}
	if len(c.StorageKey) != 0 && len(c.StorageKey) != 32 {
		return errors.New("storage key must have 32 bytes")
	}
	if len(c.DeviceLabel) > 80 {
		return errors.New("device_label must be at most 80 characters")
	}
	if len(c.ServerLabel) > 80 || len(c.ServerCountry) > 2 {
		return errors.New("server_label max 80 characters; server_country must be an optional two-letter country code")
	}
	if c.VPN != nil && c.VPN.Enabled {
		if c.VPN.MTU != 0 && (c.VPN.MTU < 576 || c.VPN.MTU > 9000) {
			return errors.New("vpn.mtu must be between 576 and 9000")
		}
		if c.VPN.Peer != "" {
			if _, err := peer.Decode(c.VPN.Peer); err != nil {
				return fmt.Errorf("vpn.peer: %w", err)
			}
		}
		if c.VPN.DNSResolver != "" {
			host, port, err := net.SplitHostPort(c.VPN.DNSResolver)
			if err != nil || net.ParseIP(host) == nil || port != "53" {
				return errors.New("vpn.dns_resolver must be an explicit IP address on port 53")
			}
		}
	}
	if c.StateDir == "" {
		return errors.New("state_dir is required")
	}
	if len(c.APIToken) < 32 {
		return errors.New("api_token must contain at least 32 characters")
	}
	if strings.HasPrefix(c.APIToken, "REPLACE_") {
		return errors.New("placeholder api_token is not usable; generate a private token with meshway init")
	}
	if c.APIListen != "" {
		if err := LoopbackAddress(c.APIListen); err != nil {
			return fmt.Errorf("api_listen: %w", err)
		}
	}
	if c.SOCKSListen != "" {
		if err := LoopbackAddress(c.SOCKSListen); err != nil {
			return fmt.Errorf("socks_listen: %w", err)
		}
	}
	for _, a := range c.ListenAddresses {
		if _, err := ma.NewMultiaddr(a); err != nil {
			return fmt.Errorf("listen_addresses: %w", err)
		}
	}
	for _, a := range c.BootstrapPeers {
		if _, err := peer.AddrInfoFromString(a); err != nil {
			return fmt.Errorf("bootstrap_peers: %w", err)
		}
	}
	for _, id := range append(append([]string{}, c.AllowedPeers...), c.UpstreamPeer) {
		if id != "" {
			if _, err := peer.Decode(id); err != nil {
				return fmt.Errorf("invalid peer ID %q: %w", id, err)
			}
		}
	}
	names := map[string]bool{}
	for _, s := range c.Services {
		if s.Name == "" || names[s.Name] {
			return errors.New("service names must be unique and nonempty")
		}
		names[s.Name] = true
		if _, _, err := net.SplitHostPort(s.Target); err != nil {
			return fmt.Errorf("service target: %w", err)
		}
	}
	for _, f := range c.Forwards {
		if err := LoopbackAddress(f.Listen); err != nil {
			return fmt.Errorf("forward listen: %w", err)
		}
		if f.Peer != "" {
			if _, err := peer.Decode(f.Peer); err != nil {
				return err
			}
		}
		if (f.Target == "") == (f.Service == "") {
			return errors.New("forward must have exactly one target or service")
		}
		if f.Target != "" {
			if _, _, err := net.SplitHostPort(f.Target); err != nil {
				return err
			}
		}
	}
	for _, origin := range c.AllowedOrigins {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.Contains(origin, "*") {
			return errors.New("allowed_origins must be exact http or https origins without path, credentials, query, or wildcard")
		}
	}
	return nil
}
