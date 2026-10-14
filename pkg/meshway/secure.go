package meshway

import (
	"context"
	"errors"
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/user-github-me/meshway/internal/account"
	"github.com/user-github-me/meshway/internal/config"
	"github.com/user-github-me/meshway/internal/core"
	"github.com/user-github-me/meshway/internal/state"
)

func validateStorageKey(key []byte) error {
	if len(key) != 32 {
		return errors.New("device storage key must have 32 bytes")
	}
	return nil
}

// StartSecure keeps state encrypted using a key supplied by the native secure
// store. The key is never serialized into the configuration or control API.
func StartSecure(configJSON string, key []byte) (*Node, error) {
	if err := validateStorageKey(key); err != nil {
		return nil, err
	}
	c, err := config.Decode(strings.NewReader(configJSON))
	if err != nil {
		return nil, err
	}
	c.StorageKey = append([]byte(nil), key...)
	n, err := core.Start(context.Background(), c)
	if err != nil {
		return nil, err
	}
	return &Node{node: n}, nil
}

func IdentitySecure(stateDir string, key []byte) (string, error) {
	if err := validateStorageKey(key); err != nil {
		return "", err
	}
	lock, err := state.Acquire(stateDir)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	identity, err := core.IdentityWithKey(stateDir, key)
	if err != nil {
		return "", err
	}
	id, err := peer.IDFromPrivateKey(identity)
	return id.String(), err
}

func ExportRecoverySecure(stateDir, output string, key []byte) error {
	if err := validateStorageKey(key); err != nil {
		return err
	}
	lock, err := state.Acquire(stateDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	return account.ExportRecoveryWithKey(stateDir, output, key)
}

func ImportRecoverySecure(stateDir, input string, force bool, key []byte) error {
	if err := validateStorageKey(key); err != nil {
		return err
	}
	lock, err := state.Acquire(stateDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	return account.ImportRecoveryWithKey(stateDir, input, force, key)
}

func RotateRecoverySecure(stateDir, output string, key []byte) error {
	if err := validateStorageKey(key); err != nil {
		return err
	}
	lock, err := state.Acquire(stateDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	return account.RotateRecovery(stateDir, output, key)
}
