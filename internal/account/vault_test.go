package account

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptedAccountRecoveryPreservesSignedState(t *testing.T) {
	dir := t.TempDir()
	h := testHost(t)
	key := bytes.Repeat([]byte{12}, 32)
	s, err := New(dir, h, Options{StorageKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Record("connect", "test peer"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "account.json"))
	seed, _ := os.ReadFile(filepath.Join(dir, "account.seed"))
	if bytes.HasPrefix(before, []byte("{")) || len(seed) == 32 {
		t.Fatal("account stored in plaintext")
	}
	backup := filepath.Join(t.TempDir(), "backup.txt")
	if err := ExportRecoveryWithKey(dir, backup, key); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := ImportRecoveryWithKey(dir, backup, false, key); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "account.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("same root import changed signed state")
	}
	reopened, err := New(dir, h, Options{StorageKey: key})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.Status().AccountID != s.Status().AccountID || len(reopened.History()) != 1 {
		t.Fatal("encrypted recovery lost account")
	}
}
