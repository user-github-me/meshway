package core

import (
	"context"
	"testing"

	"github.com/user-github-me/meshway/internal/config"
)

func TestServerInvitationSurvivesClientRestart(t *testing.T) {
	exitConfig, _ := config.Default(t.TempDir())
	exitConfig.ListenAddresses = []string{"/ip4/127.0.0.1/tcp/0"}
	exitConfig.APIListen = ""
	exitConfig.SOCKSListen = ""
	exit, err := Start(context.Background(), exitConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer exit.Stop()
	c, _ := config.Default(t.TempDir())
	c.ListenAddresses = []string{"/ip4/127.0.0.1/tcp/0"}
	c.APIListen = ""
	c.SOCKSListen = ""
	c.StorageKey = make([]byte, 32)
	client, err := Start(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	address := exit.Status().Addresses[0]
	if _, err := client.Connect(context.Background(), address); err != nil {
		t.Fatal(err)
	}
	client.Stop()
	restarted, err := Start(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Stop()
	found := false
	for _, saved := range restarted.Config.BootstrapPeers {
		if saved == address {
			found = true
		}
	}
	if !found {
		t.Fatal("server invitation lost on restart")
	}
}
