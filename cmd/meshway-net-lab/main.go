// meshway-net-lab is an opt-in validation harness, not an installed client.
// It observes real connection transports and requests explicit relay reservations.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	"github.com/user-github-me/meshway/internal/config"
	"github.com/user-github-me/meshway/internal/core"
	"golang.org/x/net/proxy"
)

func main() {
	path := flag.String("config", "", "private temporary configuration")
	relayAddress := flag.String("reserve-relay", "", "public relay multiaddress to explicitly reserve")
	operation := flag.String("operation", "", "status|connect|https|shutdown on an existing lab node")
	target := flag.String("target", "", "peer multiaddress for connect or HTTPS URL")
	flag.Parse()
	var err error
	if *operation != "" {
		err = control(*path, *operation, *target)
	} else {
		err = run(*path, *relayAddress)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func control(path, operation, target string) error {
	c, err := config.Load(path)
	if err != nil {
		return err
	}
	if operation == "https" {
		dialer, err := proxy.SOCKS5("tcp", c.SOCKSListen, nil, &net.Dialer{Timeout: 15 * time.Second})
		if err != nil {
			return err
		}
		transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialer.(proxy.ContextDialer).DialContext(ctx, network, address)
		}}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 40 * time.Second}
		response, err := client.Get(target)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("HTTPS returned %d", response.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
		if err != nil {
			return err
		}
		if len(body) == 0 {
			return fmt.Errorf("empty HTTPS response")
		}
		fmt.Printf("{\"result\":\"PASS\",\"response_bytes\":%d}\n", len(body))
		return nil
	}
	method := "GET"
	var payload []byte
	if operation == "connect" {
		method = "POST"
		payload, _ = json.Marshal(map[string]string{"address": target})
	}
	if operation == "shutdown" {
		method = "POST"
		payload = []byte("{}")
	}
	if operation != "connect" && operation != "shutdown" && operation != "status" {
		return fmt.Errorf("invalid lab operation")
	}
	request, err := http.NewRequest(method, "http://"+c.APIListen+"/v1/"+operation, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.APIToken)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 20 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("lab API returned %d", response.StatusCode)
	}
	_, err = io.Copy(os.Stdout, io.LimitReader(response.Body, 1<<20))
	return err
}

func run(path, address string) error {
	configuration, err := config.Load(path)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	node, err := core.Start(ctx, configuration)
	if err != nil {
		return err
	}
	defer node.Stop()
	var relay *peer.AddrInfo
	if address != "" {
		relay, err = peer.AddrInfoFromString(address)
		if err != nil {
			return err
		}
	}
	previous := ""
	reserveAfter := time.Time{}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if relay != nil && time.Now().After(reserveAfter) {
			attempt, stop := context.WithTimeout(ctx, 12*time.Second)
			_, reserveErr := client.Reserve(attempt, node.Host, *relay)
			stop()
			if reserveErr != nil {
				message, _ := json.Marshal(map[string]any{"event": "reservation", "success": false, "error": reserveErr.Error()})
				fmt.Println(string(message))
				reserveAfter = time.Now().Add(3 * time.Second)
			} else {
				fmt.Println(`{"event":"reservation","success":true}`)
				reserveAfter = time.Now().Add(20 * time.Second)
			}
		}
		connections := []map[string]any{}
		for _, connection := range node.Host.Network().Conns() {
			connections = append(connections, map[string]any{"peer": connection.RemotePeer().String(), "limited": connection.Stat().Limited, "transport": connection.RemoteMultiaddr().String()})
		}
		payload, _ := json.Marshal(map[string]any{"event": "connections", "connections": connections})
		if string(payload) != previous {
			fmt.Println(string(payload))
			previous = string(payload)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-node.Done():
			return nil
		case <-ticker.C:
		}
	}
}
