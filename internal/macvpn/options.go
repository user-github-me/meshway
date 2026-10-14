// Package macvpn supplies the optional administrator-owned macOS utun bridge.
// The secure daemon remains owned by the user; this process owns only its
// packet interface, narrowly scoped routes and dynamic DNS entry.
package macvpn

import (
	"context"
	"encoding/binary"
	"errors"

	"github.com/user-github-me/meshway/internal/vpn"
)

type Options struct {
	ConfigPath      string
	PeerID          string
	UnderlayIPs     []string
	ParentPID       int
	RuntimeDir      string
	CaptureInstance string
}

func decodeFrame(frame []byte, mtu int) ([]byte, error) {
	if len(frame) < 5 {
		return nil, errors.New("short utun frame")
	}
	family := binary.BigEndian.Uint32(frame[:4])
	packet := frame[4:]
	if (family != 2 || packet[0]>>4 != 4) && (family != 30 || packet[0]>>4 != 6) {
		return nil, errors.New("utun address family mismatch")
	}
	if err := vpn.ValidatePacket(packet, mtu); err != nil {
		return nil, err
	}
	return packet, nil
}
func encodeFrame(packet []byte, mtu int) ([]byte, error) {
	if err := vpn.ValidatePacket(packet, mtu); err != nil {
		return nil, err
	}
	frame := make([]byte, len(packet)+4)
	family := uint32(2)
	if packet[0]>>4 == 6 {
		family = 30
	}
	binary.BigEndian.PutUint32(frame, family)
	copy(frame[4:], packet)
	return frame, nil
}

// Command and journal records contain no account keys or API credentials.
type command struct {
	Name  string   `json:"name"`
	Args  []string `json:"args"`
	Input string   `json:"input,omitempty"`
}
type step struct {
	Apply   command `json:"apply"`
	Undo    command `json:"undo"`
	Applied bool    `json:"applied"`
}
type executor interface {
	execute(context.Context, command) ([]byte, error)
}
type stepInspector interface {
	inspect(context.Context, step) (bool, error)
}

func apply(ctx context.Context, e executor, steps []step, record func(int, step) error) error {
	for i, s := range steps {
		// Journal before mutation so a process failure cannot lose the cleanup plan.
		s.Applied = false
		if err := record(i, s); err != nil {
			return err
		}
		if _, err := e.execute(ctx, s.Apply); err != nil {
			return err
		}
		s.Applied = true
		if err := record(i, s); err != nil {
			return err
		}
	}
	return nil
}

// rollback checks current ownership even for successful steps: another process
// may replace a route, and a timed-out add may have succeeded after the caller
// stopped waiting. Absence must be positively confirmed by the platform probe.
// Failed cleanup never prevents attempts to restore the other owned resources.
func rollback(ctx context.Context, e executor, steps []step, record func([]step) error) error {
	inspector, ok := e.(stepInspector)
	if !ok {
		return errors.New("network cleanup requires an ownership inspector")
	}
	pending := append([]step(nil), steps...)
	var failures []error
	for i := len(pending) - 1; i >= 0; i-- {
		s := pending[i]
		owned, err := inspector.inspect(ctx, s)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if owned {
			if _, err = e.execute(ctx, s.Undo); err != nil {
				failures = append(failures, err)
				continue
			}
		}
		next := make([]step, 0, len(pending)-1)
		next = append(next, pending[:i]...)
		next = append(next, pending[i+1:]...)
		if err = record(next); err != nil {
			failures = append(failures, err)
			continue
		}
		pending = next
	}
	return errors.Join(failures...)
}
