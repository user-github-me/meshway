package transportssh

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestAliasAndCommandBoundary(t *testing.T) {
	for _, alias := range []string{"-oProxyCommand=evil", "user@host", "host;id", "host\n", "../host"} {
		if Validate(Options{Alias: alias, LocalPort: 44001, RemotePort: 44001}) == nil {
			t.Fatal("unsafe alias accepted")
		}
	}
	command := strings.Join(args(Options{Alias: "personal", LocalPort: 44001, RemotePort: 44001}), " ")
	for _, required := range []string{"BatchMode=yes", "StrictHostKeyChecking=yes", "ExitOnForwardFailure=yes", "PermitLocalCommand=no", "127.0.0.1:44001:127.0.0.1:44001"} {
		if !strings.Contains(command, required) {
			t.Fatal("missing safe command boundary")
		}
	}
	if strings.Contains(command, "ClearAllForwardings=yes") {
		t.Fatal("explicit local forwarding would be cleared")
	}
}

// This opt-in fixture uses a temporary OpenSSH client config and a real Go SSH
// server with a pinned host key. All TCP endpoints are loopback; no host SSH
// config, routes, DNS or proxy settings are changed.
func TestActualOpenSSHForwardAuthenticationAndCleanup(t *testing.T) {
	if os.Getenv("MESHWAY_SSH_LAB") != "1" {
		t.Skip("opt-in system OpenSSH loopback fixture")
	}
	sshPath, err := executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key")
	generated := exec.Command("/usr/bin/ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", keyPath)
	generated.Stdout, generated.Stderr = nil, nil
	if err = generated.Run(); err != nil {
		t.Fatal("fixture key generation failed")
	}
	body, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(body)
	if err != nil {
		t.Fatal(err)
	}
	serverConfig := &ssh.ServerConfig{PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if string(key.Marshal()) != string(signer.PublicKey().Marshal()) {
			return nil, errors.New("wrong fixture key")
		}
		return nil, nil
	}}
	serverConfig.AddHostKey(signer)
	server, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connections := make(chan net.Conn, 16)
	go func() {
		for {
			c, err := server.Accept()
			if err != nil {
				return
			}
			connections <- c
			go func() {
				defer c.Close()
				sshConn, channels, requests, err := ssh.NewServerConn(c, serverConfig)
				if err != nil {
					return
				}
				defer sshConn.Close()
				go ssh.DiscardRequests(requests)
				for channel := range channels {
					if channel.ChannelType() != "direct-tcpip" {
						channel.Reject(ssh.UnknownChannelType, "not supported")
						continue
					}
					ch, req, err := channel.Accept()
					if err != nil {
						continue
					}
					go ssh.DiscardRequests(req)
					go func() { defer ch.Close(); buffer := make([]byte, 32); n, _ := ch.Read(buffer); ch.Write(buffer[:n]) }()
				}
			}()
		}
	}()
	defer func() {
		close(connections)
		for c := range connections {
			c.Close()
		}
	}()
	port := server.Addr().(*net.TCPAddr).Port
	known := filepath.Join(dir, "known_hosts")
	os.WriteFile(known, []byte(fmt.Sprintf("[127.0.0.1]:%d %s", port, ssh.MarshalAuthorizedKey(signer.PublicKey()))), 0600)
	configPath := filepath.Join(dir, "ssh_config")
	config := fmt.Sprintf("Host lab\n HostName 127.0.0.1\n Port %d\n User lab\n IdentityFile %s\n UserKnownHostsFile %s\n IdentitiesOnly yes\n", port, keyPath, known)
	os.WriteFile(configPath, []byte(config), 0600)
	local, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	localPort := local.Addr().(*net.TCPAddr).Port
	local.Close()
	m, err := New(Options{Alias: "lab", LocalPort: localPort, RemotePort: 44001, PeerID: "test-pin"})
	if err != nil {
		t.Fatal(err)
	}
	arguments := append([]string{"-F", configPath}, args(m.options)...)
	var peerHealthy atomic.Bool
	var allowAuthentication atomic.Bool
	var authenticationAttempts atomic.Int32
	allowAuthentication.Store(true)
	m.SetReadyCheck(peerHealthy.Load)
	authenticate := func(ctx context.Context, address string) error {
		authenticationAttempts.Add(1)
		if !allowAuthentication.Load() {
			return errors.New("fixture peer is temporarily unavailable")
		}
		d := net.Dialer{}
		conn, err := d.DialContext(ctx, "tcp", address)
		if err != nil {
			return err
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(time.Second))
		if _, err = conn.Write([]byte("pinned-meshway-auth")); err != nil {
			return err
		}
		buffer := make([]byte, 32)
		n, err := conn.Read(buffer)
		if err != nil || string(buffer[:n]) != "pinned-meshway-auth" {
			return errors.New("fixture peer authentication failed")
		}
		peerHealthy.Store(true)
		return nil
	}
	if err = m.start(ctx, sshPath, arguments, authenticate); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	if err = m.start(ctx, sshPath, arguments, authenticate); err == nil {
		t.Fatal("concurrent start accepted")
	}
	deadline := time.Now().Add(8 * time.Second)
	for !m.Status().Authenticated && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !m.Status().Authenticated {
		t.Fatalf("actual SSH forwarding did not authenticate: %+v", m.Status())
	}
	m.mu.Lock()
	ownedPID := m.cmd.Process.Pid
	m.mu.Unlock()
	attemptsBeforeLoss := authenticationAttempts.Load()
	allowAuthentication.Store(false)
	peerHealthy.Store(false)
	deadline = time.Now().Add(5 * time.Second)
	for m.Status().Authenticated && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if state := m.Status(); state.Authenticated || state.Ready || !state.Running {
		t.Fatalf("lost peer retained authentication or killed live SSH: %+v", state)
	}
	if authenticationAttempts.Load() <= attemptsBeforeLoss {
		t.Fatal("live SSH did not retry pinned peer authentication")
	}
	allowAuthentication.Store(true)
	deadline = time.Now().Add(5 * time.Second)
	for !m.Status().Authenticated && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !m.Status().Authenticated {
		t.Fatal("live SSH did not recover after pinned peer returned")
	}
	m.mu.Lock()
	recoveredPID := m.cmd.Process.Pid
	m.mu.Unlock()
	if recoveredPID != ownedPID {
		t.Fatal("peer reconnect unnecessarily replaced the SSH transport")
	}
	if err = m.Stop(); err != nil {
		t.Fatal(err)
	}
	if m.Status().Running || m.Status().Ready {
		t.Fatal("owned SSH stayed active")
	}
	if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort)), 100*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("forward listener remained")
	}
	t.Log("PASS actual OpenSSH strict-host-key loopback forwarding, authentication gate, live-child peer-loss reauthentication, duplicate-start rejection and owned child/listener cleanup")
}
