//go:build windows

package vpn

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Holds one owned adapter for the separate native-firewall CI lab. Only private
// addresses and host-only routes on that adapter are changed. Packet probes
// observe real Windows UDP output at Wintun; no internet packets are generated.
func TestWindowsFirewallAdapterFixture(t *testing.T) {
	directory := os.Getenv("MESHWAY_WINDOWS_FIREWALL_LAB_DIR")
	if directory == "" {
		t.Skip("opt-in disposable Windows firewall fixture")
	}
	name := "MeshwayFirewallLab" + strconv.Itoa(os.Getpid())
	device, err := openPacketDevice(name, 1280)
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	defer func() {
		_, _ = windowsLabPowerShell(fmt.Sprintf("Get-NetRoute -InterfaceAlias '%s' -ErrorAction SilentlyContinue | Remove-NetRoute -Confirm:$false -ErrorAction SilentlyContinue; Get-NetIPAddress -InterfaceAlias '%s' -ErrorAction SilentlyContinue | Remove-NetIPAddress -Confirm:$false -ErrorAction SilentlyContinue", name, name))
	}()
	setup := fmt.Sprintf("New-NetIPAddress -InterfaceAlias '%s' -IPAddress 10.74.0.11 -PrefixLength 32 -AddressFamily IPv4 -PolicyStore ActiveStore | Out-Null; New-NetIPAddress -InterfaceAlias '%s' -IPAddress fd74::11 -PrefixLength 128 -AddressFamily IPv6 -PolicyStore ActiveStore | Out-Null; foreach($p in @('10.74.0.10/32','10.74.0.12/32','fd74::10/128','fd74::12/128')) { $n=if($p.Contains(':')) {'::'} else {'0.0.0.0'}; New-NetRoute -InterfaceAlias '%s' -DestinationPrefix $p -NextHop $n -PolicyStore ActiveStore | Out-Null }; $deadline=[DateTime]::UtcNow.AddSeconds(15); do { $a=@(Get-NetIPAddress -InterfaceAlias '%s' | Where-Object { $_.IPAddress -in @('10.74.0.11','fd74::11') -and $_.AddressState -eq 'Preferred' }); if($a.Count -eq 2){break}; if([DateTime]::UtcNow -ge $deadline){throw 'Fixture addresses not ready'}; Start-Sleep -Milliseconds 200 } while($true)", name, name, name, name)
	if output, setupErr := windowsLabPowerShell(setup); setupErr != nil {
		t.Fatalf("fixture setup: %v %s", setupErr, output)
	}
	packets := make(chan []byte, 256)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		data := make([]byte, 1280)
		for {
			n, err := device.Read(data)
			if err != nil {
				return
			}
			packet := append([]byte(nil), data[:n]...)
			select {
			case packets <- packet:
			default:
			}
		}
	}()
	defer func() { device.Close(); <-readDone }()
	body, _ := json.Marshal(map[string]string{"adapter": name})
	if err = os.WriteFile(filepath.Join(directory, "ready.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(160 * time.Second)
	for time.Now().Before(deadline) {
		if _, err = os.Stat(filepath.Join(directory, "stop")); err == nil {
			return
		}
		body, readErr := os.ReadFile(filepath.Join(directory, "request.json"))
		if os.IsNotExist(readErr) {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		os.Remove(filepath.Join(directory, "request.json"))
		var request struct {
			Family int    `json:"family"`
			Peer   bool   `json:"peer"`
			Tag    string `json:"tag"`
		}
		if err = json.Unmarshal(body, &request); err != nil || len(request.Tag) < 8 || len(request.Tag) > 128 {
			t.Fatal("invalid fixture request")
		}
		for len(packets) > 0 {
			<-packets
		}
		source, destination, network := "10.74.0.11:0", "10.74.0.10:4444", "udp4"
		if request.Peer {
			destination = "10.74.0.12:4444"
		}
		if request.Family == 6 {
			source, destination, network = "[fd74::11]:0", "[fd74::10]:4444", "udp6"
			if request.Peer {
				destination = "[fd74::12]:4444"
			}
		}
		// Bind the explicit owned source address instead of any physical NIC.
		local, addressErr := net.ResolveUDPAddr(network, source)
		remote, remoteErr := net.ResolveUDPAddr(network, destination)
		var writeErr error
		if addressErr == nil && remoteErr == nil {
			udp, bindErr := net.ListenUDP(network, local)
			if bindErr == nil {
				_, writeErr = udp.WriteToUDP([]byte(request.Tag), remote)
				udp.Close()
			} else {
				writeErr = bindErr
			}
		} else {
			writeErr = fmt.Errorf("invalid fixture addresses")
		}
		observed := false
		timer := time.NewTimer(800 * time.Millisecond)
	observe:
		for {
			select {
			case packet := <-packets:
				if bytes.Contains(packet, []byte(request.Tag)) {
					observed = true
					break observe
				}
			case <-timer.C:
				break observe
			}
		}
		timer.Stop()
		result := map[string]any{"observed": observed, "write_error": writeErr != nil}
		body, _ = json.Marshal(result)
		if err = os.WriteFile(filepath.Join(directory, "response.tmp"), body, 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.Rename(filepath.Join(directory, "response.tmp"), filepath.Join(directory, "response.json")); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("Windows firewall fixture timed out")
}
