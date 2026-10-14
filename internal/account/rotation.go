package account

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	statevault "github.com/user-github-me/meshway/internal/state"
)

type accountTransition struct {
	Seed []byte `json:"seed"`
}

// A durable encrypted journal makes interrupted account replacement recoverable.
// Once staged, the next account open finishes the transition before reading it.
func finishTransition(dir string, key []byte) error {
	path := filepath.Join(dir, "account.transition")
	b, err := statevault.ReadPrivate(path, 1024, key)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var transition accountTransition
	if json.Unmarshal(b, &transition) != nil || len(transition.Seed) != 32 {
		return errors.New("invalid account transition journal")
	}
	if err = os.Remove(filepath.Join(dir, "account.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err = statevault.SavePrivate(filepath.Join(dir, "account.seed"), transition.Seed, key); err != nil {
		return err
	}
	if err = os.Remove(path); err != nil {
		return err
	}
	return statevault.SyncDirectory(dir)
}

func replaceRoot(dir string, seed, key []byte) error {
	b, err := json.Marshal(accountTransition{Seed: seed})
	if err != nil {
		return err
	}
	if err = statevault.SavePrivate(filepath.Join(dir, "account.transition"), b, key); err != nil {
		return err
	}
	return finishTransition(dir, key)
}

// RotateRecovery requires an offline state lock. A new private backup must be
// saved successfully before changing accounts. Other devices/servers must import
// it; the old root cannot enroll members in the new account.
func RotateRecovery(dir, output string, key []byte) error {
	if output == "" {
		return errors.New("new recovery backup output is required")
	}
	if _, err := rootKeyWithStorage(dir, key); err != nil {
		return err
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return err
	}
	f, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.WriteString("meshway-recovery-v1:" + encode(seed) + "\n")
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		os.Remove(output)
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = statevault.SyncDirectory(filepath.Dir(output)); err != nil {
		return err
	}
	return replaceRoot(dir, seed, key)
}
