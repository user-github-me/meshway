//go:build darwin && !ios

package macvpn

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This diagnostic owns one new interface and one exact private host route. It
// never changes defaults, resolver settings, proxies, or existing interfaces.
func TestHostedUTUNKernelUDPMetadata(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("MESHWAY_DISPOSABLE_MACOS_VM") != "1" || os.Getenv("MESHWAY_UTUN_HEADER_LAB") != "1" {
		t.Skip("explicit disposable hosted macOS kernel diagnostic")
	}
	if os.Geteuid() != 0 {
		t.Fatal("run the isolated test binary with explicit runner sudo")
	}
	directory := os.Getenv("MESHWAY_UTUN_HEADER_OUTPUT")
	if directory == "" || !filepath.IsAbs(directory) {
		t.Fatal("explicit absolute evidence directory required")
	}
	metadata := map[string]any{"status": "FAIL", "scope": "one disposable utun and private host route only", "default_routes_dns_proxy_modified": false}
	defer func() {
		data, _ := json.MarshalIndent(metadata, "", "  ")
		if err := os.WriteFile(filepath.Join(directory, "utun-header.json"), append(data, '\n'), 0644); err != nil {
			t.Error(err)
		}
	}()
	before := kernelLabBaseline(t)
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	probe := exec.CommandContext(probeCtx, "/sbin/route", "-n", "get", "-inet", "-host", "10.77.0.1")
	defer probeCancel()
	if existing, probeErr := probe.Output(); probeErr == nil && parseRoute(existing).Destination == "10.77.0.1" {
		t.Fatal("refusing an existing exact virtual DNS host route")
	}
	device, name, err := openUTUN()
	if err != nil {
		t.Fatal(err)
	}
	routeAdded := false
	defer func() {
		cleanupOK := true
		if routeAdded {
			if err := kernelLabCommand("/sbin/route", "-n", "delete", "-inet", "-host", "10.77.0.1", "-interface", name); err != nil {
				cleanupOK = false
				t.Error(err)
			}
		}
		if err := device.Close(); err != nil {
			cleanupOK = false
			t.Error(err)
		}
		metadata["baseline_preserved"] = before == kernelLabBaseline(t)
		metadata["owned_interface_closed"] = cleanupOK
		postCtx, postCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer postCancel()
		post := exec.CommandContext(postCtx, "/sbin/route", "-n", "get", "-inet", "-host", "10.77.0.1")
		observed, probeErr := post.Output()
		removed := probeErr == nil && parseRoute(observed).Interface != name
		if probeErr != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			out, tableErr := exec.CommandContext(ctx, "/usr/sbin/netstat", "-rn", "-f", "inet").Output()
			cancel()
			if tableErr == nil && strings.Contains(string(out), "Destination") {
				removed = true
				for _, line := range strings.Split(string(out), "\n") {
					fields := strings.Fields(line)
					if len(fields) > 1 && fields[0] == "10.77.0.1" && strings.Contains(line, name) {
						removed = false
					}
				}
			}
		}
		metadata["owned_host_route_removed"] = removed && cleanupOK
		if !metadata["baseline_preserved"].(bool) {
			cleanupOK = false
			t.Error("default DNS/proxy/routes baseline changed")
		}
		if !removed {
			cleanupOK = false
			t.Error("owned host route cleanup not confirmed")
		}
		if !cleanupOK {
			metadata["status"] = "FAIL"
			metadata["cleanup_error"] = "owned cleanup could not be positively confirmed"
		}
	}()
	if err = kernelLabCommand("/sbin/ifconfig", name, "inet", "10.77.0.2", "10.77.0.2", "netmask", "255.255.255.255", "mtu", "1280", "up"); err != nil {
		t.Fatal(err)
	}
	if err = kernelLabCommand("/sbin/route", "-n", "add", "-inet", "-host", "10.77.0.1", "-interface", name); err != nil {
		t.Fatal(err)
	}
	routeAdded = true
	for _, mode := range []string{"bound", "unbound"} {
		var local *net.UDPAddr
		if mode == "bound" {
			local = &net.UDPAddr{IP: net.ParseIP("10.77.0.2")}
		}
		conn, err := net.DialUDP("udp4", local, &net.UDPAddr{IP: net.ParseIP("10.77.0.1"), Port: 53})
		if err != nil {
			t.Fatal(err)
		}
		dummy := []byte{0x49, 0x23, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1}
		if _, err = conn.Write(dummy); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		stop := time.AfterFunc(5*time.Second, device.cancelReaders)
		for {
			raw := make([]byte, 1284)
			n, readErr := device.Read(raw)
			if readErr != nil {
				stop.Stop()
				conn.Close()
				t.Fatal(readErr)
			}
			raw = raw[:n]
			packet, decodeErr := decodeFrame(raw, 1280)
			if decodeErr != nil {
				continue
			}
			if packet[0]>>4 != 4 || len(packet) < 28 || packet[9] != 17 || net.IP(packet[16:20]).String() != "10.77.0.1" {
				continue
			}
			h := int(packet[0]&15) * 4
			if len(packet) < h+8 {
				continue
			}
			udp := packet[h:]
			if binary.BigEndian.Uint16(udp[2:4]) != 53 {
				continue
			}
			pseudo := append([]byte{}, packet[12:20]...)
			pseudo = append(pseudo, 0, 17, byte(len(udp)>>8), byte(len(udp)))
			pseudo = append(pseudo, udp...)
			metadata[mode+"_packet"] = map[string]any{"utun_family": binary.BigEndian.Uint32(raw[:4]), "version": 4, "source_is_virtual_client": net.IP(packet[12:16]).String() == "10.77.0.2", "destination": "10.77.0.1", "protocol": 17, "ip_total_length": binary.BigEndian.Uint16(packet[2:4]), "frame_payload_length": len(packet), "ipv4_header_checksum": binary.BigEndian.Uint16(packet[10:12]), "ipv4_header_checksum_valid": kernelLabSum(packet[:h]) == 0xffff, "udp_length": binary.BigEndian.Uint16(udp[4:6]), "udp_checksum": binary.BigEndian.Uint16(udp[6:8]), "udp_checksum_valid": kernelLabSum(pseudo) == 0xffff, "udp_checksum_omitted": binary.BigEndian.Uint16(udp[6:8]) == 0}
			stop.Stop()
			conn.Close()
			break
		}
	}
	metadata["status"] = "PASS"
	t.Log("captured actual bound and unbound Darwin UDP header metadata without defaults/DNS changes")

}

func kernelLabSum(data []byte) uint16 {
	var sum uint32
	for len(data) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(data))
		data = data[2:]
	}
	if len(data) == 1 {
		sum += uint32(data[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return uint16(sum)
}
func kernelLabCommand(binary string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LC_ALL=C"}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %s", filepath.Base(binary), strings.TrimSpace(string(out)))
	}
	return nil
}
func kernelLabBaseline(t *testing.T) string {
	t.Helper()
	var result []string
	for _, a := range [][]string{{"/sbin/route", "-n", "get", "default"}, {"/sbin/route", "-n", "get", "-inet6", "default"}, {"/usr/sbin/scutil", "--dns"}, {"/usr/sbin/scutil", "--proxy"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		out, err := exec.CommandContext(ctx, a[0], a[1:]...).CombinedOutput()
		cancel()
		value := string(out)
		if a[0] == "/sbin/route" {
			var stable []string
			for _, l := range strings.Split(value, "\n") {
				l = strings.TrimSpace(l)
				if strings.HasPrefix(l, "destination:") || strings.HasPrefix(l, "gateway:") || strings.HasPrefix(l, "interface:") || strings.HasPrefix(l, "flags:") || strings.HasPrefix(l, "mask:") {
					stable = append(stable, l)
				}
			}
			value = strings.Join(stable, "\n")
		}
		result = append(result, fmt.Sprint(err)+value)
	}
	return strings.Join(result, "\n--\n")
}
