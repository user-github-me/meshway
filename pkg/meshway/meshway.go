// Package meshway exposes a small, JSON-based API usable by gomobile and other hosts.
package meshway

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/meshway/meshway/internal/account"
	"github.com/meshway/meshway/internal/config"
	"github.com/meshway/meshway/internal/core"
	"github.com/meshway/meshway/internal/state"
)

type Node struct{ node *core.Node }

func Start(configJSON string) (*Node, error) {
	c, err := config.Decode(strings.NewReader(configJSON))
	if err != nil {
		return nil, err
	}
	n, err := core.Start(context.Background(), c)
	if err != nil {
		return nil, err
	}
	return &Node{node: n}, nil
}
func DefaultConfig(stateDir string) (string, error) {
	c, err := config.Default(stateDir)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(c)
	return string(b), err
}
func LoadConfig(path string) (string, error) {
	c, err := config.Load(path)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(c)
	return string(b), err
}
func (n *Node) Status() (string, error) {
	b, err := json.Marshal(n.node.Status())
	return string(b), err
}
func (n *Node) Connect(address string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := n.node.Connect(ctx, address)
	return err
}
func (n *Node) Stop() error { return n.node.Stop() }

func (n *Node) AccountStatus() (string, error) {
	b, err := json.Marshal(n.node.Account.Status())
	return string(b), err
}
func (n *Node) Devices() (string, error) {
	b, err := json.Marshal(n.node.Account.Devices())
	return string(b), err
}
func (n *Node) History() (string, error) {
	b, err := json.Marshal(n.node.Account.History())
	return string(b), err
}
func (n *Node) SyncAccount(peerID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return n.node.Account.Sync(ctx, peerID)
}
func (n *Node) RevokeDevice(peerID string) error { return n.node.Account.Revoke(peerID) }

func (n *Node) Servers() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	b, err := json.Marshal(n.node.Discovery.Servers(ctx))
	return string(b), err
}
func (n *Node) SelectPeer(peerID, address string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return n.node.SelectPeer(ctx, peerID, address)
}

func (n *Node) Disconnect() { n.node.Disconnect() }

// StartVPN attaches a TUN/VpnService descriptor. The core duplicates fd; the
// caller retains ownership of the original descriptor. An empty peerID waits
// for an inbound packet stream from an approved peer.
func (n *Node) StartVPN(fd int, peerID string) error { return n.node.StartVPN(fd, peerID) }

func Identity(stateDir string) (string, error) { return core.IdentityID(stateDir) }
func Version() string                          { return core.Version }

// ExportRecovery saves the account recovery secret to a new private file.
func ExportRecovery(stateDir, output string) error {
	lock, err := state.Acquire(stateDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	return account.ExportRecovery(stateDir, output)
}

// ImportRecovery requires the node stopped and never changes its peer key.
func ImportRecovery(stateDir, input string, force bool) error {
	lock, err := state.Acquire(stateDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	return account.ImportRecovery(stateDir, input, force)
}
