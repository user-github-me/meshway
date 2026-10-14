package core

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/user-github-me/meshway/internal/account"
	"github.com/user-github-me/meshway/internal/config"
)

func TestPersonalRevocationClosesActiveUDPAndPacketSessions(t *testing.T) {
	clientConfig := testConfig(t)
	backup := filepath.Join(t.TempDir(), "recovery")
	if err := account.ExportRecovery(clientConfig.StateDir, backup); err != nil {
		t.Fatal(err)
	}
	serverConfig := testConfig(t)
	if err := account.ImportRecovery(serverConfig.StateDir, backup, false); err != nil {
		t.Fatal(err)
	}
	serverConfig.PersonalServer = true
	serverConfig.Exit = true
	serverConfig.AllowPrivateTargets = true
	server := startNode(t, serverConfig)
	clientConfig.UpstreamPeer = server.Host.ID().String()
	clientConfig.VPN = &config.VPN{Enabled: true, Internet: true, MTU: 1280}
	client := startNode(t, clientConfig)
	connect(t, client, server)
	if err := client.StartPacketVPN(server.Host.ID().String()); err != nil {
		t.Fatal(err)
	}
	control, relay := socksUDPAssociate(t, client)
	defer control.Close()
	origin := testUDPOrigin(t, "udp4", "127.0.0.1:0")
	socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	packet, _ := socksDatagram(origin.LocalAddr(), []byte("allowed before revoke"))
	socket.SetDeadline(time.Now().Add(5 * time.Second))
	socket.WriteToUDP(packet, relay)
	if _, _, err = socket.ReadFromUDP(make([]byte, 1500)); err != nil {
		t.Fatal(err)
	}
	if err = server.Account.Revoke(client.Host.ID().String()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for client.vpn.Status().Active && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if client.vpn.Status().Active {
		t.Fatal("revoked account device retained active packet exit stream")
	}
	control.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err = control.Read(make([]byte, 1)); err == nil {
		t.Fatal("UDP control remained usable after revoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if stream, err := client.openUDP(ctx, server.Host.ID().String()); err == nil {
		stream.Reset()
		t.Fatal("revoked device re-opened UDP exit")
	}
	if err = client.StartPacketVPN(server.Host.ID().String()); err == nil {
		t.Fatal("revoked device re-opened packet exit")
	}
}
