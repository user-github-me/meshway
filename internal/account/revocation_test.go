package account

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRevocationsPropagateWithoutSharingHistory(t *testing.T) {
	aDir, bDir, cDir := t.TempDir(), t.TempDir(), t.TempDir()
	aHost, bHost, cHost := testHost(t), testHost(t), testHost(t)
	a := testStore(t, aDir, aHost, false)
	backup := filepath.Join(t.TempDir(), "recovery")
	if err := ExportRecovery(aDir, backup); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{bDir, cDir} {
		if err := ImportRecovery(dir, backup, false); err != nil {
			t.Fatal(err)
		}
	}
	b := testStore(t, bDir, bHost, false)
	c := testStore(t, cDir, cHost, false)
	connect(t, aHost, bHost)
	connect(t, aHost, cHost)
	connect(t, bHost, cHost)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.AuthorizePeer(ctx, cHost.ID()); err != nil {
		t.Fatal(err)
	}
	if err := b.AuthorizePeer(ctx, cHost.ID()); err != nil {
		t.Fatal(err)
	}
	if err := a.AuthorizePeer(ctx, bHost.ID()); err != nil {
		t.Fatal(err)
	}
	if err := a.Record("connect", "private history"); err != nil {
		t.Fatal(err)
	}
	if err := a.Revoke(cHost.ID().String()); err != nil {
		t.Fatal(err)
	}
	if err := a.ExchangeRevocations(ctx, bHost.ID()); err != nil {
		t.Fatal(err)
	}
	if err := b.AuthorizePeer(ctx, cHost.ID()); err == nil {
		t.Fatal("revoked device accepted on second private server")
	}
	if len(b.History()) != 0 {
		t.Fatal("revocation propagation shared history without consent")
	}
	forged := a.revocations()
	forged[0].RevocationSignature = "invalid"
	if err := c.mergeRevocations(forged); err == nil {
		t.Fatal("forged revocation accepted")
	}
}
