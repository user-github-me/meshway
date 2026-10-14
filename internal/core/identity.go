package core

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/user-github-me/meshway/internal/state"
)

func Identity(stateDir string) (crypto.PrivKey, error) {
	storageKey, err := state.KeyFromEnvironment()
	if err != nil {
		return nil, err
	}
	return IdentityWithKey(stateDir, storageKey)
}

func IdentityWithKey(stateDir string, storageKey []byte) (crypto.PrivKey, error) {
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(stateDir, "identity.key")
	b, err := state.ReadPrivate(path, 4096, storageKey)
	if err == nil {
		return crypto.UnmarshalPrivateKey(b)
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	k, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return nil, err
	}
	b, err = crypto.MarshalPrivateKey(k)
	if err != nil {
		return nil, err
	}
	b, err = state.Seal(path, b, storageKey)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		b, err = state.ReadPrivate(path, 4096, storageKey)
		if err != nil {
			return nil, err
		}
		return crypto.UnmarshalPrivateKey(b)
	}
	if err != nil {
		return nil, err
	}
	if _, err = f.Write(b); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	return k, nil
}

func readIdentity(path string) ([]byte, error) {
	return state.ReadPrivate(path, 4096, nil)
}

func IdentityID(stateDir string) (string, error) {
	k, err := Identity(stateDir)
	if err != nil {
		return "", err
	}
	id, err := peer.IDFromPrivateKey(k)
	if err != nil {
		return "", fmt.Errorf("identity: %w", err)
	}
	return id.String(), nil
}
