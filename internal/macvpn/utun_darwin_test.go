//go:build darwin && !ios

package macvpn

import (
	"bytes"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"testing"
	"time"
)

func testDatagramDevice(t *testing.T) (*utunDevice, int) {
	t.Helper()
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	device, err := newUTUNDevice(pair[0])
	if err != nil {
		unix.Close(pair[0])
		unix.Close(pair[1])
		t.Fatal(err)
	}
	t.Cleanup(func() { device.Close(); unix.Close(pair[1]) })
	return device, pair[1]
}

func TestUTUNDevicePacketIO(t *testing.T) {
	device, other := testDatagramDevice(t)
	packet := []byte("one whole datagram")
	if _, err := unix.Write(other, packet); err != nil {
		t.Fatal(err)
	}
	received := make([]byte, 1284)
	n, err := device.Read(received)
	if err != nil || !bytes.Equal(received[:n], packet) {
		t.Fatalf("read datagram: n=%d err=%v", n, err)
	}
	if n, err = device.Write(packet); err != nil || n != len(packet) {
		t.Fatalf("write datagram: n=%d err=%v", n, err)
	}
	n, err = unix.Read(other, received)
	if err != nil || !bytes.Equal(received[:n], packet) {
		t.Fatalf("peer datagram: n=%d err=%v", n, err)
	}
	flags, err := unix.FcntlInt(uintptr(device.fd), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.O_NONBLOCK == 0 {
		t.Fatal("descriptor remained blocking")
	}
	descriptorFlags, err := unix.FcntlInt(uintptr(device.fd), unix.F_GETFD, 0)
	if err != nil {
		t.Fatal(err)
	}
	if descriptorFlags&unix.FD_CLOEXEC == 0 {
		t.Fatal("descriptor can leak into child process")
	}
}

func TestUTUNDeviceCancelPreservesDescriptorForCleanup(t *testing.T) {
	device, other := testDatagramDevice(t)
	result := make(chan error, 1)
	go func() { _, err := device.Read(make([]byte, 1284)); result <- err }()
	device.cancelReaders()
	select {
	case err := <-result:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("read cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("packet reader did not stop within one second")
	}
	if _, err := unix.FcntlInt(uintptr(device.fd), unix.F_GETFD, 0); err != nil {
		t.Fatalf("cancellation prematurely destroyed descriptor: %v", err)
	}
	// The kernel socket remains usable by cleanup until the explicit Close.
	if _, err := unix.Write(other, []byte("alive")); err != nil {
		t.Fatal(err)
	}
	received := make([]byte, 32)
	n, err := unix.Read(device.fd, received)
	if err != nil || string(received[:n]) != "alive" {
		t.Fatalf("descriptor did not survive: n=%d err=%v", n, err)
	}
	if _, err = device.Write([]byte("stopped")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("writer continued after cancellation: %v", err)
	}
	if err = device.Close(); err != nil {
		t.Fatal(err)
	}
	if err = device.Close(); err != nil {
		t.Fatal("Close is not idempotent", err)
	}
	if _, err = unix.FcntlInt(uintptr(device.fd), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("Close left descriptor open: %v", err)
	}
}

func TestUTUNDeviceCloseUnblocksReader(t *testing.T) {
	device, _ := testDatagramDevice(t)
	result := make(chan error, 1)
	go func() { _, err := device.Read(make([]byte, 1284)); result <- err }()
	closed := make(chan error, 1)
	go func() { closed <- device.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close blocked on packet read")
	}
	select {
	case err := <-result:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("read result: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("reader leaked after Close")
	}
}

func TestUTUNDeviceCancelUnblocksBackpressuredWriter(t *testing.T) {
	device, _ := testDatagramDevice(t)
	if err := unix.SetsockoptInt(device.fd, unix.SOL_SOCKET, unix.SO_SNDBUF, 4096); err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 1284)
	full := false
	for i := 0; i < 1024; i++ {
		_, err := unix.Write(device.fd, packet)
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.ENOBUFS) {
			full = true
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if !full {
		t.Fatal("could not create bounded datagram backpressure")
	}
	result := make(chan error, 1)
	go func() { _, err := device.Write(packet); result <- err }()
	device.cancelReaders()
	select {
	case err := <-result:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("write cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("backpressured writer did not stop within one second")
	}
	if _, err := unix.FcntlInt(uintptr(device.fd), unix.F_GETFD, 0); err != nil {
		t.Fatalf("write cancellation destroyed the descriptor: %v", err)
	}
}

func TestValidUTUNDevice(t *testing.T) {
	for _, name := range []string{"utun0", "utun123"} {
		if !validDevice(name) {
			t.Fatalf("rejected %q", name)
		}
	}
	for _, name := range []string{"utun", "utun-1", "utun0;", "en0", "utun999999999999"} {
		if validDevice(name) {
			t.Fatalf("accepted %q", name)
		}
	}
}
