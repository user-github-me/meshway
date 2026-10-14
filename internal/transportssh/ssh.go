// Package transportssh owns a foreground OpenSSH child for reaching a private
// Meshway server. The SSH tunnel carries the existing authenticated protocol.
package transportssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"
)

type Options struct {
	Alias      string
	LocalPort  int
	RemotePort int
	PeerID     string
}
type Status struct {
	Kind          string `json:"kind"`
	Configured    bool   `json:"configured"`
	Running       bool   `json:"running"`
	Ready         bool   `json:"ready"`
	Authenticated bool   `json:"authenticated"`
	LocalAddress  string `json:"local_address,omitempty"`
	PeerID        string `json:"peer_id,omitempty"`
	Error         string `json:"error,omitempty"`
}
type Manager struct {
	options    Options
	mu         sync.Mutex
	status     Status
	cancel     context.CancelFunc
	done       chan struct{}
	cmd        *exec.Cmd
	readyCheck func() bool
}

func Validate(o Options) error {
	if len(o.Alias) < 1 || len(o.Alias) > 120 || o.Alias[0] == '-' {
		return errors.New("personal transport needs a valid SSH config alias")
	}
	for _, r := range o.Alias {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return errors.New("SSH alias accepts letters, digits, dots, underscores and hyphens")
		}
	}
	if o.LocalPort < 1024 || o.LocalPort > 65535 || o.RemotePort < 1 || o.RemotePort > 65535 {
		return errors.New("SSH local port must be 1024–65535 and remote port 1–65535")
	}
	return nil
}
func executable() (string, error) {
	if runtime.GOOS == "android" || runtime.GOOS == "ios" {
		return "", errors.New("managed SSH transport requires desktop OpenSSH")
	}
	path := "/usr/bin/ssh"
	if runtime.GOOS == "windows" {
		systemRoot := os.Getenv("SystemRoot")
		if systemRoot == "" {
			systemRoot = `C:\Windows`
		}
		path = filepath.Join(systemRoot, "System32", "OpenSSH", "ssh.exe")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("install the system OpenSSH client to use personal SSH transport")
	}
	return path, nil
}
func args(o Options) []string {
	return []string{"-N", "-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ExitOnForwardFailure=yes", "-o", "ConnectTimeout=10", "-o", "ConnectionAttempts=1", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "PermitLocalCommand=no", "-o", "ForwardAgent=no", "-o", "ForwardX11=no", "-L", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", o.LocalPort, o.RemotePort), o.Alias}
}
func New(o Options) (*Manager, error) {
	if err := Validate(o); err != nil {
		return nil, err
	}
	return &Manager{options: o, status: Status{Kind: "ssh", Configured: true, LocalAddress: net.JoinHostPort("127.0.0.1", strconv.Itoa(o.LocalPort)), PeerID: o.PeerID}}, nil
}
func (m *Manager) Status() Status { m.mu.Lock(); defer m.mu.Unlock(); return m.status }

// SetReadyCheck lets the owning node invalidate readiness when the pinned
// authenticated peer connection disappears while OpenSSH itself remains alive.
func (m *Manager) SetReadyCheck(check func() bool) { m.mu.Lock(); m.readyCheck = check; m.mu.Unlock() }

func (m *Manager) Start(parent context.Context, authenticate func(context.Context, string) error) error {
	path, err := executable()
	if err != nil {
		return err
	}
	return m.start(parent, path, args(m.options), authenticate)
}
func (m *Manager) start(parent context.Context, path string, arguments []string, authenticate func(context.Context, string) error) error {
	if authenticate == nil {
		return errors.New("personal SSH transport requires pinned peer authentication")
	}
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		return errors.New("personal SSH transport is already running")
	}
	reserve, err := net.Listen("tcp", m.status.LocalAddress)
	if err != nil {
		m.mu.Unlock()
		return errors.New("personal SSH local port is already occupied")
	}
	reserve.Close()
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	m.cancel, m.done = cancel, done
	m.status.Error = ""
	m.mu.Unlock()
	go func() {
		defer close(done)
		defer func() {
			m.mu.Lock()
			m.cmd, m.cancel = nil, nil
			m.status.Running, m.status.Ready, m.status.Authenticated = false, false, false
			m.mu.Unlock()
		}()
		for ctx.Err() == nil {
			cmd := exec.CommandContext(ctx, path, arguments...)
			cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
			cmd.WaitDelay = 2 * time.Second
			if cmd.Start() != nil {
				m.mu.Lock()
				m.status.Error = "OpenSSH could not start"
				m.mu.Unlock()
			} else {
				m.mu.Lock()
				m.cmd = cmd
				m.status.Running = true
				m.status.Error = ""
				m.mu.Unlock()
				exited := make(chan struct{})
				go func() { cmd.Wait(); close(exited) }()
				ticker := time.NewTicker(2 * time.Second)
			wait:
				for {
					select {
					case <-ctx.Done():
						<-exited
						break wait
					case <-exited:
						break wait
					case <-ticker.C:
						m.mu.Lock()
						authenticated := m.status.Authenticated
						check := m.readyCheck
						m.mu.Unlock()
						if authenticated && check != nil && !check() {
							m.mu.Lock()
							m.status.Ready, m.status.Authenticated = false, false
							m.mu.Unlock()
							authenticated = false
						}
						if !authenticated {
							authCtx, authCancel := context.WithTimeout(ctx, 2*time.Second)
							err := authenticate(authCtx, m.status.LocalAddress)
							authCancel()
							if err == nil {
								m.mu.Lock()
								m.status.Ready, m.status.Authenticated = true, true
								m.status.Error = ""
								m.mu.Unlock()
							} else {
								m.mu.Lock()
								m.status.Error = "Waiting for SSH and pinned Meshway peer authentication; verify your SSH alias, existing agent and known-host entry"
								m.mu.Unlock()
							}
						}
					}
				}
				ticker.Stop()
				m.mu.Lock()
				m.cmd = nil
				m.status.Running, m.status.Ready, m.status.Authenticated = false, false, false
				if ctx.Err() == nil {
					m.status.Error = "SSH disconnected; retrying with known-host and peer authentication"
				}
				m.mu.Unlock()
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
	return nil
}
func (m *Manager) Stop() error {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("owned SSH process did not stop within its deadline")
	}
}

// Invitation pins the existing Meshway identity instead of trusting whichever
// process happens to accept connections on the forwarded loopback port.
func Invitation(o Options) string {
	return "/ip4/127.0.0.1/tcp/" + strconv.Itoa(o.LocalPort) + "/p2p/" + o.PeerID
}
