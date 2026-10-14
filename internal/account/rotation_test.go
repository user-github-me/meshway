package account

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	statevault "github.com/user-github-me/meshway/internal/state"
)

func TestRotationRequiresSavedBackupAndRecoversInterruptedTransition(t *testing.T) {
	dir := t.TempDir()
	h := testHost(t)
	key := bytes.Repeat([]byte{13}, 32)
	s, err := New(dir, h, Options{StorageKey: key})
	if err != nil {
		t.Fatal(err)
	}
	oldID := s.Status().AccountID
	s.Close()
	backup := filepath.Join(t.TempDir(), "new-recovery")
	if err := RotateRecovery(dir, backup, key); err != nil {
		t.Fatal(err)
	}
	rotated, err := New(dir, h, Options{StorageKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Status().AccountID == oldID {
		t.Fatal("root did not rotate")
	}
	rotated.Close()
	if err := RotateRecovery(dir, backup, key); err == nil {
		t.Fatal("overwrote recovery backup")
	}
	newID := rotated.Status().AccountID
	seed := bytes.Repeat([]byte{31}, 32)
	journal, _ := json.Marshal(accountTransition{Seed: seed})
	if err := statevault.SavePrivate(filepath.Join(dir, "account.transition"), journal, key); err != nil {
		t.Fatal(err)
	}
	recovered, err := New(dir, h, Options{StorageKey: key})
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if recovered.Status().AccountID == newID {
		t.Fatal("staged transition not recovered")
	}
	if _, err := os.Stat(filepath.Join(dir, "account.transition")); !os.IsNotExist(err) {
		t.Fatal("journal retained")
	}
}

func TestSignedHistoryDeletionCannotBeUndoneByOldSync(t *testing.T) {
	s := testStore(t, t.TempDir(), testHost(t), true)
	if err := s.Record("connect", "private"); err != nil {
		t.Fatal(err)
	}
	old := s.data
	if err := s.ClearHistory(true); err != nil {
		t.Fatal(err)
	}
	if err := s.merge(old, s.certificate); err != nil {
		t.Fatal(err)
	}
	if len(s.History()) != 0 {
		t.Fatal("old sync restored deleted history")
	}
	forged := s.data
	bad := *forged.Deletion
	bad.Signature = "invalid"
	forged.Deletion = &bad
	if s.validateState(forged) == nil {
		t.Fatal("forged history deletion accepted")
	}
}

func TestRotatedRecoveryBackupCanEnrollAnotherDevice(t *testing.T) {
	directory := t.TempDir()
	host := testHost(t)
	store := testStore(t, directory, host, false)
	store.Close()
	backup := filepath.Join(t.TempDir(), "rotated-recovery")
	if err := RotateRecovery(directory, backup, nil); err != nil {
		t.Fatal(err)
	}
	joinedDir := t.TempDir()
	if err := ImportRecoveryWithKey(joinedDir, backup, false, nil); err != nil {
		t.Fatalf("rotated recovery cannot be imported: %v", err)
	}
	joined, err := New(joinedDir, testHost(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer joined.Close()
	rotated, err := New(directory, host, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer rotated.Close()
	if joined.Status().AccountID != rotated.Status().AccountID {
		t.Fatal("rotated account membership mismatch")
	}
}
