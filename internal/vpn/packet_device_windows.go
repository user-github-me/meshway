//go:build windows

package vpn

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wintun"
)

// Wintun owns only an interface and bounded packet rings. Native clients own
// explicit address, route, DNS and firewall setup and restoration. The DLL must
// be the signed architecture-specific Wintun release next to meshway-daemon.exe.
type windowsPacketDevice struct {
	adapter *wintun.Adapter
	session wintun.Session
	stop    windows.Handle
	mu      sync.RWMutex
	once    sync.Once
	closed  bool
}

func openPacketDevice(name string, mtu int) (io.ReadWriteCloser, error) {
	if name == "" || len(name) > 80 {
		return nil, errors.New("Wintun adapter name must be 1..80 characters")
	}
	adapter, err := wintun.CreateAdapter(name, "Wintun", nil)
	if err != nil {
		return nil, fmt.Errorf("create Wintun adapter (administrator and signed wintun.dll required): %w", err)
	}
	session, err := adapter.StartSession(1 << 20)
	if err != nil {
		adapter.Close()
		return nil, err
	}
	stop, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		session.End()
		adapter.Close()
		return nil, err
	}
	return &windowsPacketDevice{adapter: adapter, session: session, stop: stop}, nil
}
func (d *windowsPacketDevice) Read(p []byte) (int, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return 0, io.EOF
	}
	for {
		selectResult, _ := windows.WaitForSingleObject(d.stop, 0)
		if selectResult == windows.WAIT_OBJECT_0 {
			return 0, io.EOF
		}
		packet, err := d.session.ReceivePacket()
		if err == nil {
			n := copy(p, packet)
			d.session.ReleaseReceivePacket(packet)
			return n, nil
		}
		if !errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
			return 0, err
		}
		result, err := windows.WaitForMultipleObjects([]windows.Handle{d.stop, d.session.ReadWaitEvent()}, false, windows.INFINITE)
		if err != nil {
			return 0, err
		}
		if result == windows.WAIT_OBJECT_0 {
			return 0, io.EOF
		}
	}
}
func (d *windowsPacketDevice) Write(packet []byte) (int, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return 0, io.EOF
	}
	result, _ := windows.WaitForSingleObject(d.stop, 0)
	if result == windows.WAIT_OBJECT_0 {
		return 0, io.EOF
	}
	buffer, err := d.session.AllocateSendPacket(len(packet))
	if err != nil {
		if errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			return len(packet), nil
		}
		return 0, err
	}
	copy(buffer, packet)
	d.session.SendPacket(buffer)
	return len(packet), nil
}
func (d *windowsPacketDevice) Close() error {
	d.once.Do(func() {
		windows.SetEvent(d.stop)
		d.mu.Lock()
		defer d.mu.Unlock()
		d.closed = true
		d.session.End()
		d.adapter.Close()
		windows.CloseHandle(d.stop)
	})
	return nil
}
