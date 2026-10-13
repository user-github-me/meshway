package vpn

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func ipPacket(version byte, payload string) []byte {
	if version == 4 {
		b := make([]byte, 20+len(payload))
		b[0] = 0x45
		binary.BigEndian.PutUint16(b[2:], uint16(len(b)))
		copy(b[20:], payload)
		return b
	}
	b := make([]byte, 40+len(payload))
	b[0] = 0x60
	binary.BigEndian.PutUint16(b[4:], uint16(len(payload)))
	copy(b[40:], payload)
	return b
}

func TestPacketFraming(t *testing.T) {
	for _, version := range []byte{4, 6} {
		packet := ipPacket(version, "real IP payload")
		var stream bytes.Buffer
		if err := WritePacket(&stream, packet, 1280); err != nil {
			t.Fatal(err)
		}
		got, err := ReadPacket(&stream, 1280)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(packet, got) {
			t.Fatal("packet changed")
		}
	}
}

func TestMalformedFrames(t *testing.T) {
	valid := ipPacket(4, "")
	for _, packet := range [][]byte{nil, {0x70}, make([]byte, 1281), append([]byte{0x45}, make([]byte, 19)...), append([]byte{0x60}, make([]byte, 19)...)} {
		if err := ValidatePacket(packet, 1280); err == nil {
			t.Fatalf("accepted malformed %v", packet)
		}
	}
	valid[0] = 0x44
	if err := ValidatePacket(valid, 1280); err == nil {
		t.Fatal("accepted invalid IHL")
	}
	for _, frame := range [][]byte{{0, 0}, {255, 255}, {0, 20, 0x45}} {
		if _, err := ReadPacket(bytes.NewReader(frame), 1280); err == nil {
			t.Fatal("accepted invalid frame")
		}
	}
}
