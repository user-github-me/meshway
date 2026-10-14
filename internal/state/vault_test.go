package state

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestVaultMigrationAuthenticationAndFileBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "account.seed")
	plain := bytes.Repeat([]byte{7}, 32)
	key := bytes.Repeat([]byte{9}, 32)
	if err := os.WriteFile(path, plain, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadPrivate(path, 32, key)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatal("migration failed", err)
	}
	encrypted, _ := os.ReadFile(path)
	if bytes.Contains(encrypted, plain) {
		t.Fatal("plaintext remains in state")
	}
	if _, err := ReadPrivate(path, 32, nil); err == nil {
		t.Fatal("encrypted state accepted without key")
	}
	if _, err := ReadPrivate(path, 32, bytes.Repeat([]byte{1}, 32)); err == nil {
		t.Fatal("wrong key accepted")
	}
	copyPath := filepath.Join(filepath.Dir(path), "identity.key")
	if err := os.WriteFile(copyPath, encrypted, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPrivate(copyPath, 4096, key); err == nil {
		t.Fatal("ciphertext substitution accepted")
	}
	encrypted[len(encrypted)-1] ^= 1
	if err := os.WriteFile(path, encrypted, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPrivate(path, 32, key); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}
