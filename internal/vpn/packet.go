// Package vpn transports IPv4 and IPv6 packets through authenticated peer streams.
// It supplies a point-to-point link; operating-system routes and NAT remain explicit.
package vpn

import (
	"encoding/binary"
	"errors"
	"io"
)

const DefaultMTU = 1280

func ValidatePacket(packet []byte, mtu int) error {
	if len(packet) == 0 || len(packet) > mtu {
		return errors.New("packet outside MTU")
	}
	switch packet[0] >> 4 {
	case 4:
		if len(packet) < 20 {
			return errors.New("truncated IPv4 header")
		}
		header := int(packet[0]&15) * 4
		if header < 20 || header > len(packet) {
			return errors.New("invalid IPv4 header length")
		}
		if int(binary.BigEndian.Uint16(packet[2:4])) != len(packet) {
			return errors.New("invalid IPv4 total length")
		}
	case 6:
		if len(packet) < 40 {
			return errors.New("truncated IPv6 header")
		}
		if int(binary.BigEndian.Uint16(packet[4:6]))+40 != len(packet) {
			return errors.New("invalid IPv6 payload length")
		}
	default:
		return errors.New("only IPv4 and IPv6 are supported")
	}
	return nil
}

func ReadPacket(r io.Reader, mtu int) ([]byte, error) {
	var size [2]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(size[:]))
	if n < 20 || n > mtu {
		return nil, errors.New("invalid framed packet size")
	}
	packet := make([]byte, n)
	if _, err := io.ReadFull(r, packet); err != nil {
		return nil, err
	}
	if err := ValidatePacket(packet, mtu); err != nil {
		return nil, err
	}
	return packet, nil
}

func WritePacket(w io.Writer, packet []byte, mtu int) error {
	if err := ValidatePacket(packet, mtu); err != nil {
		return err
	}
	frame := make([]byte, 2+len(packet))
	binary.BigEndian.PutUint16(frame[:2], uint16(len(packet)))
	copy(frame[2:], packet)
	for len(frame) > 0 {
		n, err := w.Write(frame)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}
