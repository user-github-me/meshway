package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/user-github-me/meshway/internal/linuxvpn"
	"github.com/user-github-me/meshway/internal/macvpn"
)

func vpnCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: meshway vpn start|stop|status --config PATH [--device meshway0] [--dns-mode resolved]")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("vpn "+args[0], flag.ContinueOnError)
	config := flags.String("config", filepath.Join(home, ".meshway", "config.json"), "running client configuration path")
	device := flags.String("device", "meshway0", "dedicated persistent TUN name")
	dns := flags.String("dns-mode", "resolved", "resolved or explicit file (overwrites /etc/resolv.conf while active)")
	peer := flags.String("peer", "", "selected Meshway peer ID for macOS device VPN")
	underlays := flags.String("underlay-ips", "", "comma-separated resolved physical transport IPs for macOS")
	parent := flags.Int("parent-pid", 0, "owning macOS app process; helper restores networking if it exits")
	instance := flags.String("capture-instance", "", "public opaque macOS capture readiness instance")
	if err = flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	path, err := filepath.Abs(*config)
	if err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		var ips []string
		if *underlays != "" {
			ips = strings.Split(*underlays, ",")
		}
		options := macvpn.Options{ConfigPath: path, PeerID: *peer, UnderlayIPs: ips, ParentPID: *parent, CaptureInstance: *instance}
		switch args[0] {
		case "start":
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			return macvpn.Run(ctx, options)
		case "stop":
			return macvpn.Stop(options)
		case "status":
			value, err := macvpn.Status(options)
			if err == nil {
				fmt.Println(value)
			}
			return err
		default:
			return errors.New("VPN command must be start, stop or status")
		}
	}
	options := linuxvpn.Options{ConfigPath: path, Device: *device, DNSMode: *dns}
	switch args[0] {
	case "start":
		err = linuxvpn.Start(options)
		if err == nil {
			fmt.Println("Device VPN connected. DNS and IPv4/IPv6 traffic use the selected server; firewall protection remains until explicit stop.")
		}
		return err
	case "stop":
		err = linuxvpn.Stop(options)
		if err == nil {
			fmt.Println("Device VPN stopped. Original routes and DNS restored.")
		}
		return err
	case "status":
		status, err := linuxvpn.Status(options)
		if err == nil {
			fmt.Println(status)
		}
		return err
	default:
		return errors.New("VPN command must be start, stop or status")
	}
}
