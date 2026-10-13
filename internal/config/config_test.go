package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecureDefaultsAndSave(t *testing.T) {
	c, err := Default(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.Exit || c.Relay || c.AllowPrivateTargets || len(c.AllowedPeers) > 0 {
		t.Fatal("unsafe defaults")
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if err = Save(p, c, false); err != nil {
		t.Fatal(err)
	}
	if err = Save(p, c, false); err == nil {
		t.Fatal("overwrote config")
	}
	loaded, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.APIToken != loaded.APIToken {
		t.Fatal("token changed")
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0600 {
		t.Fatal("configuration not private")
	}
}
func TestRejectPublicLocalListenersAndUnknownJSON(t *testing.T) {
	c, _ := Default(t.TempDir())
	for _, addr := range []string{"0.0.0.0:7777", "localhost:7777", "[::]:7777", "8.8.8.8:7777"} {
		c.APIListen = addr
		if c.Validate() == nil {
			t.Errorf("accepted %s", addr)
		}
	}
	if _, err := Decode(strings.NewReader(`{"unknown":true}`)); err == nil {
		t.Fatal("accepted unknown field")
	}
	c, _ = Default(t.TempDir())
	c.APIToken = "REPLACE_BY_RUNNING_meshway_init_FOR_RANDOM_TOKEN"
	if c.Validate() == nil {
		t.Fatal("accepted documented placeholder token")
	}
}
