package macvpn

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func frameTestIPv4(size int) []byte {
	p := make([]byte, size)
	if size >= 20 {
		p[0] = 0x45
		binary.BigEndian.PutUint16(p[2:4], uint16(size))
		p[8] = 64
		p[9] = 17
		copy(p[12:16], []byte{10, 77, 0, 2})
		copy(p[16:20], []byte{1, 1, 1, 1})
	}
	return p
}
func frameTestIPv6(size int) []byte {
	p := make([]byte, size)
	if size >= 40 {
		p[0] = 0x60
		binary.BigEndian.PutUint16(p[4:6], uint16(size-40))
		p[6] = 17
		p[7] = 64
		p[8] = 0xfd
		p[24] = 0x26
	}
	return p
}

func TestUTUNFramesIPv4AndIPv6(t *testing.T) {
	for _, tc := range []struct {
		name   string
		packet []byte
		family uint32
	}{{"ipv4", frameTestIPv4(1280), 2}, {"ipv6", frameTestIPv6(1280), 30}} {
		t.Run(tc.name, func(t *testing.T) {
			frame, err := encodeFrame(tc.packet, 1280)
			if err != nil {
				t.Fatal(err)
			}
			if len(frame) != len(tc.packet)+4 || binary.BigEndian.Uint32(frame[:4]) != tc.family {
				t.Fatalf("wrong Darwin utun header: %x", frame[:4])
			}
			decoded, err := decodeFrame(frame, 1280)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(decoded, tc.packet) {
				t.Fatal("IP payload changed")
			}
			frame[4] ^= 0x80
			if _, err = decodeFrame(frame, 1280); err == nil {
				t.Fatal("accepted mismatched family/IP version")
			}
		})
	}
}

func TestUTUNFramesRejectInvalidInput(t *testing.T) {
	ipv4 := frameTestIPv4(20)
	good4, _ := encodeFrame(ipv4, 1280)
	wrongFamily := bytes.Clone(good4)
	binary.BigEndian.PutUint32(wrongFamily[:4], unixFamilyUnsupported)
	wrongEndian := bytes.Clone(good4)
	binary.LittleEndian.PutUint32(wrongEndian[:4], 2)
	badLength := bytes.Clone(good4)
	badLength[7] = 19
	invalidHeader := bytes.Clone(good4)
	invalidHeader[4] = 0x44
	cases := [][]byte{nil, {0, 0, 0, 2}, {0, 0, 0, 2, 0x45}, wrongFamily, wrongEndian, badLength, invalidHeader}
	for i, frame := range cases {
		if _, err := decodeFrame(frame, 1280); err == nil {
			t.Fatalf("accepted malformed frame %d", i)
		}
	}
	oversized, _ := encodeFrame(frameTestIPv6(1281), 1281)
	if _, err := decodeFrame(oversized, 1280); err == nil {
		t.Fatal("accepted oversized frame")
	}
	if _, err := encodeFrame(nil, 1280); err == nil {
		t.Fatal("accepted empty packet")
	}
	if _, err := encodeFrame(frameTestIPv4(1281), 1280); err == nil {
		t.Fatal("accepted oversized packet")
	}
}

const unixFamilyUnsupported = 123

func FuzzUTUNFrame(f *testing.F) {
	frame4, _ := encodeFrame(frameTestIPv4(20), 1280)
	frame6, _ := encodeFrame(frameTestIPv6(40), 1280)
	f.Add(frame4)
	f.Add(frame6)
	f.Add([]byte{0, 0, 0, 2})
	f.Fuzz(func(t *testing.T, frame []byte) {
		packet, err := decodeFrame(frame, 1280)
		if err != nil {
			return
		}
		encoded, err := encodeFrame(packet, 1280)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(encoded, frame) {
			t.Fatal("accepted a noncanonical utun frame")
		}
	})
}
