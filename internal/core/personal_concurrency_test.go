package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/user-github-me/meshway/internal/account"
)

func TestPersonalServerFiftyParallelSOCKSRequests(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "parallel authenticated personal exit")
	}))
	defer origin.Close()
	clientConfig := testConfig(t)
	backup := filepath.Join(t.TempDir(), "recovery.txt")
	if err := account.ExportRecovery(clientConfig.StateDir, backup); err != nil {
		t.Fatal(err)
	}
	serverConfig := testConfig(t)
	if err := account.ImportRecovery(serverConfig.StateDir, backup, false); err != nil {
		t.Fatal(err)
	}
	serverConfig.PersonalServer, serverConfig.Exit, serverConfig.AllowPrivateTargets = true, true, true
	server := startNode(t, serverConfig)
	clientConfig.UpstreamPeer = server.Host.ID().String()
	client := startNode(t, clientConfig)
	connect(t, client, server)
	target, port, err := net.SplitHostPort(strings.TrimPrefix(origin.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := strconv.Atoi(port)
	start := make(chan struct{})
	failures := make(chan error, 50)
	var workers sync.WaitGroup
	for i := 0; i < 50; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			conn, err := net.DialTimeout("tcp", client.Status().SOCKSListen, 5*time.Second)
			if err != nil {
				failures <- err
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(8 * time.Second))
			if _, err = conn.Write([]byte{5, 1, 0}); err != nil {
				failures <- err
				return
			}
			var greeting [2]byte
			if _, err = io.ReadFull(conn, greeting[:]); err != nil || greeting != [2]byte{5, 0} {
				failures <- errors.New("SOCKS greeting failed")
				return
			}
			r := append([]byte{5, 1, 0, 3, byte(len(target))}, []byte(target)...)
			r = append(r, byte(p>>8), byte(p))
			if _, err = conn.Write(r); err != nil {
				failures <- err
				return
			}
			var reply [10]byte
			if _, err = io.ReadFull(conn, reply[:]); err != nil || reply[1] != 0 {
				failures <- fmt.Errorf("personal SOCKS rejected concurrent request with code %d", reply[1])
				return
			}
			if _, err = io.WriteString(conn, "GET / HTTP/1.0\r\nHost: loopback-origin\r\n\r\n"); err != nil {
				failures <- err
				return
			}
			data, err := io.ReadAll(conn)
			if err != nil || !bytes.Contains(data, []byte("parallel authenticated personal exit")) {
				failures <- errors.New("parallel encrypted HTTP response failed")
			}
		}()
	}
	close(start)
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if t.Failed() {
		return
	}
	if err := server.Account.Revoke(client.Host.ID().String()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := client.openTunnel(ctx, server.Host.ID().String(), request{Target: strings.TrimPrefix(origin.URL, "http://")}); err == nil {
		t.Fatal("cached personal proof survived revocation")
	}
	t.Log("PASS 50 simultaneous SOCKS handshakes and real HTTP payloads through the same-account encrypted exit; immediate revocation denied subsequent streams")
}
