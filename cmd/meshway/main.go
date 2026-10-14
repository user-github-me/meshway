package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/user-github-me/meshway/internal/account"
	"github.com/user-github-me/meshway/internal/config"
	"github.com/user-github-me/meshway/internal/core"
	"github.com/user-github-me/meshway/internal/state"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "meshway:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) > 0 && args[0] == "vpn" {
		return vpnCommand(args[1:])
	}
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		fmt.Println("Meshway: encrypted peer VPN, SOCKS and tunnels\n\nCommands: init, setup-server --personal, identity, daemon, status, version\nAccount: create, export --output FILE, import --input FILE [--force], rotate --output NEW_FILE, status, devices, history, sync --peer ID, revoke --peer ID\nLinux device VPN: sudo meshway vpn start|stop|status --config ABSOLUTE_PATH [--dns-mode resolved|file]\nAll commands accept --config PATH. Default: ~/.meshway/config.json\nStart: meshway init && meshway daemon\nDocumentation: https://github.com/user-github-me/meshway")
		return nil
	}
	if len(args) == 0 {
		return errors.New("usage: meshway init|identity|daemon|status|version [--config path]")
	}
	if args[0] == "version" {
		fmt.Println("Meshway", core.Version)
		return nil
	}
	if args[0] == "account" {
		return accountCommand(args[1:])
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	defaultPath := filepath.Join(home, ".meshway", "config.json")
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	path := fs.String("config", defaultPath, "configuration path")
	force := fs.Bool("force", false, "replace existing configuration (init only)")
	personal := fs.Bool("personal", false, "same-account private server (setup-server)")
	if err = fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if args[0] == "init" || args[0] == "setup-server" {
		absolute, err := filepath.Abs(*path)
		if err != nil {
			return err
		}
		c, err := config.Default(filepath.Join(filepath.Dir(absolute), "state"))
		if err != nil {
			return err
		}
		if args[0] == "setup-server" {
			if !*personal {
				return errors.New("setup-server requires --personal for same-account private access")
			}
			c.PersonalServer = true
			c.Exit = true
			c.Advertise = true
			c.SOCKSListen = ""
			c.ServerLabel = "My personal server"
		}
		lock, err := state.Acquire(c.StateDir)
		if err != nil {
			return err
		}
		defer lock.Close()
		if err = config.Save(absolute, c, *force); err != nil {
			return err
		}
		id, err := core.IdentityID(c.StateDir)
		if err != nil {
			return err
		}
		fmt.Printf("Meshway configuration: %s\nPeer ID: %s\nAPI token stored privately in configuration. Start with: meshway daemon --config %s\n", absolute, id, absolute)
		return nil
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	switch args[0] {
	case "identity":
		id, err := core.IdentityID(c.StateDir)
		if err != nil {
			return err
		}
		fmt.Println(id)
		return nil
	case "daemon":
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		n, err := core.Start(ctx, c)
		if err != nil {
			return err
		}
		defer n.Stop()
		b, _ := json.MarshalIndent(n.Status(), "", "  ")
		fmt.Println(string(b))
		<-n.Done()
		return nil
	case "status":
		if c.APIListen == "" {
			return errors.New("API is disabled")
		}
		req, err := http.NewRequest("GET", "http://"+c.APIListen+"/v1/status", nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.APIToken)
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("API returned %s", resp.Status)
		}
		_, err = io.Copy(os.Stdout, io.LimitReader(resp.Body, 1<<20))
		return err
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func accountCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: meshway account create|export|import|status|devices|history|sync|revoke --config path")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("account "+args[0], flag.ContinueOnError)
	path := fs.String("config", filepath.Join(home, ".meshway", "config.json"), "configuration path")
	output := fs.String("output", "", "new recovery backup file (export)")
	input := fs.String("input", "", "recovery file (import)")
	force := fs.Bool("force", false, "replace existing account (import)")
	peerID := fs.String("peer", "", "peer ID (sync/revoke)")
	if err = fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	if args[0] == "export" || args[0] == "import" || args[0] == "create" || args[0] == "rotate" {
		lock, err := state.Acquire(c.StateDir)
		if err != nil {
			return err
		}
		defer lock.Close()
	}
	switch args[0] {
	case "rotate":
		if err := account.RotateRecovery(c.StateDir, *output, c.StorageKey); err != nil {
			return err
		}
		fmt.Println("Recovery key rotated and saved privately. Import the new backup on every trusted device and private server before reconnecting.")
		return nil
	case "export":
		if *output == "" {
			return errors.New("--output is required; recovery secrets are never printed")
		}
		if err = account.ExportRecovery(c.StateDir, *output); err != nil {
			return err
		}
		fmt.Println("Recovery backup written to", *output)
		return nil
	case "import":
		if *input == "" {
			return errors.New("--input recovery file is required")
		}
		if err = account.ImportRecovery(c.StateDir, *input, *force); err != nil {
			return err
		}
		fmt.Println("Account imported. Start the daemon to enroll this device.")
		return nil
	case "create":
		key, err := core.Identity(c.StateDir)
		if err != nil {
			return err
		}
		h, err := libp2p.New(libp2p.Identity(key), libp2p.NoListenAddrs)
		if err != nil {
			return err
		}
		defer h.Close()
		store, err := account.New(c.StateDir, h, account.Options{Label: c.DeviceLabel, SyncEnabled: c.AccountSync, StorageKey: c.StorageKey})
		if err != nil {
			return err
		}
		defer store.Close()
		b, _ := json.MarshalIndent(store.Status(), "", "  ")
		fmt.Println(string(b))
		return nil
	case "status", "devices", "history":
		return accountAPICall(c, "GET", "/v1/account/"+args[0], nil)
	case "sync", "revoke":
		if *peerID == "" {
			return errors.New("--peer is required")
		}
		b, _ := json.Marshal(map[string]string{"peer_id": *peerID})
		return accountAPICall(c, "POST", "/v1/account/"+args[0], b)
	default:
		return fmt.Errorf("unknown account command %q", args[0])
	}
}

func accountAPICall(c config.Config, method, path string, body []byte) error {
	if c.APIListen == "" {
		return errors.New("API is disabled")
	}
	req, err := http.NewRequest(method, "http://"+c.APIListen+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 6<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("API %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	_, err = os.Stdout.Write(b)
	return err
}
