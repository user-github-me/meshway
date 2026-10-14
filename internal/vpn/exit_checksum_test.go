package vpn

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"time"
)

// Independent raw-packet probes exercise the exit without a synthetic client
// stack automatically filling checksums. This catches native packet adapters
// admitting structurally valid packets whose offload checksums are unfinished.
func checksumProbe(data []byte) uint16 {
	var sum uint32
	for len(data) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(data))
		data = data[2:]
	}
	if len(data) > 0 {
		sum += uint32(data[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

func rawDNSPacket(version byte, payload []byte) []byte {
	headerLen := 20
	if version == 6 {
		headerLen = 40
	}
	packet := make([]byte, headerLen+8+len(payload))
	transport := packet[headerLen:]
	binary.BigEndian.PutUint16(transport[:2], 55001)
	binary.BigEndian.PutUint16(transport[2:4], 53)
	binary.BigEndian.PutUint16(transport[4:6], uint16(len(transport)))
	copy(transport[8:], payload)
	var pseudo []byte
	if version == 4 {
		packet[0] = 0x45
		packet[8] = 64
		packet[9] = 17
		binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
		src, dst := netip.MustParseAddr(ClientIPv4).As4(), netip.MustParseAddr(DNSIPv4).As4()
		copy(packet[12:16], src[:])
		copy(packet[16:20], dst[:])
		pseudo = append(pseudo, packet[12:20]...)
		pseudo = append(pseudo, 0, 17, byte(len(transport)>>8), byte(len(transport)))
		binary.BigEndian.PutUint16(packet[10:12], checksumProbe(packet[:20]))
	} else {
		packet[0] = 0x60
		packet[6] = 17
		packet[7] = 64
		binary.BigEndian.PutUint16(packet[4:6], uint16(len(transport)))
		src, dst := netip.MustParseAddr(ClientIPv6).As16(), netip.MustParseAddr(DNSIPv6).As16()
		copy(packet[8:24], src[:])
		copy(packet[24:40], dst[:])
		pseudo = append(pseudo, packet[8:40]...)
		pseudo = append(pseudo, 0, 0, byte(len(transport)>>8), byte(len(transport)), 0, 0, 0, 17)
	}
	pseudo = append(pseudo, transport...)
	checksum := checksumProbe(pseudo)
	if checksum == 0 {
		checksum = 0xffff
	}
	binary.BigEndian.PutUint16(transport[6:8], checksum)
	return packet
}

func TestInternetStackRawNativeChecksumAdmission(t *testing.T) {
	for _, tc := range []struct {
		name      string
		version   byte
		mutate    func([]byte)
		wantReply bool
	}{
		{"ipv4-valid", 4, nil, true},
		{"ipv4-udp-no-checksum", 4, func(p []byte) { p[26], p[27] = 0, 0 }, true},
		{"ipv4-unfinished-header-checksum", 4, func(p []byte) { p[10], p[11] = 0, 0 }, false},
		{"ipv4-unfinished-udp-checksum", 4, func(p []byte) { p[26] ^= 0x40 }, false},
		{"ipv6-valid", 6, nil, true},
		{"ipv6-unfinished-udp-checksum", 6, func(p []byte) { p[46], p[47] = 0, 0 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer resolver.Close()
			received := make(chan []byte, 1)
			go func() {
				buf := make([]byte, 512)
				n, from, err := resolver.ReadFrom(buf)
				if err != nil {
					return
				}
				received <- append([]byte(nil), buf[:n]...)
				_, _ = resolver.WriteTo(buf[:n], from)
			}()
			g, err := newInternetStack(context.Background(), Options{MTU: 1280, MaxFlowsPerSession: 4, AllowPrivateTargets: true, DNSResolver: resolver.LocalAddr().String()})
			if err != nil {
				t.Fatal(err)
			}
			defer g.Close()
			payload := []byte("meshway native checksum probe")
			packet := rawDNSPacket(tc.version, payload)
			if tc.mutate != nil {
				tc.mutate(packet)
			}
			if err = ValidatePacket(packet, 1280); err != nil {
				t.Fatal(err)
			}
			g.Inject(packet)
			ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
			defer cancel()
			reply, err := g.ReadPacket(ctx)
			if !tc.wantReply {
				if err == nil {
					t.Fatal("invalid native checksum accepted")
				}
				select {
				case <-received:
					t.Fatal("invalid packet reached UDP resolver")
				default:
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			offset := 20
			if tc.version == 6 {
				offset = 40
			}
			if len(reply) < offset+8 || !bytes.Equal(reply[offset+8:], payload) {
				t.Fatal("wrong raw packet reply")
			}
			select {
			case p := <-received:
				if !bytes.Equal(p, payload) {
					t.Fatal("resolver payload changed")
				}
			default:
				t.Fatal("valid native packet did not reach resolver")
			}
		})
	}
}
