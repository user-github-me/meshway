//go:build darwin && !ios

package macvpn

import (
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"strings"
	"sync"
	"time"
)

// utunDevice keeps its interface alive until owned-route cleanup finishes.
// Packet cancellation must not close the descriptor and allow its name reuse.
type utunDevice struct {
	fd       int
	stopped  chan struct{}
	stopOnce sync.Once
	mu       sync.RWMutex
	closed   bool
}

func newUTUNDevice(fd int) (*utunDevice, error) {
	unix.CloseOnExec(fd)
	if err := unix.SetNonblock(fd, true); err != nil {
		return nil, err
	}
	return &utunDevice{fd: fd, stopped: make(chan struct{})}, nil
}

func openUTUN() (*utunDevice, string, error) {
	fd, err := unix.Socket(unix.AF_SYSTEM, unix.SOCK_DGRAM, 2)
	if err != nil {
		return nil, "", err
	}
	fail := func(err error) (*utunDevice, string, error) { _ = unix.Close(fd); return nil, "", err }
	unix.CloseOnExec(fd)
	info := unix.CtlInfo{}
	copy(info.Name[:], "com.apple.net.utun_control")
	if err = unix.IoctlCtlInfo(fd, &info); err != nil {
		return fail(err)
	}
	if err = unix.Connect(fd, &unix.SockaddrCtl{ID: info.Id, Unit: 0}); err != nil {
		return fail(err)
	}
	name, err := unix.GetsockoptString(fd, 2, 2)
	if err != nil {
		return fail(err)
	}
	name = strings.TrimRight(name, "\x00")
	if !validDevice(name) {
		return fail(errors.New("invalid utun name"))
	}
	device, err := newUTUNDevice(fd)
	if err != nil {
		return fail(err)
	}
	return device, name, nil
}

// cancelReaders stops both packet directions without destroying the interface.
func (d *utunDevice) cancelReaders() {
	d.stopOnce.Do(func() { close(d.stopped) })
}

func (d *utunDevice) wait(events int16) error {
	for {
		select {
		case <-d.stopped:
			return io.ErrClosedPipe
		default:
		}
		poll := []unix.PollFd{{Fd: int32(d.fd), Events: events}}
		_, err := unix.Poll(poll, 100)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if poll[0].Revents&unix.POLLNVAL != 0 {
			return io.ErrClosedPipe
		}
		if poll[0].Revents&(events|unix.POLLERR|unix.POLLHUP) != 0 {
			return nil
		}
	}
}

func (d *utunDevice) Read(p []byte) (int, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for {
		if err := d.wait(unix.POLLIN); err != nil {
			return 0, err
		}
		n, err := unix.Read(d.fd, p)
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			continue
		}
		if err == nil && n == 0 {
			return 0, io.EOF
		}
		return n, err
	}
}

func (d *utunDevice) Write(p []byte) (int, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for {
		if err := d.wait(unix.POLLOUT); err != nil {
			return 0, err
		}
		n, err := unix.Write(d.fd, p)
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			continue
		}
		// Darwin can report ENOBUFS while a datagram descriptor still polls
		// writable. Retry with a cancellable delay instead of spinning or
		// treating temporary packet-ring pressure as a permanent failure.
		if errors.Is(err, unix.ENOBUFS) {
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-d.stopped:
				timer.Stop()
				return 0, io.ErrClosedPipe
			case <-timer.C:
			}
			continue
		}
		if err == nil && n != len(p) {
			return n, io.ErrShortWrite
		}
		return n, err
	}
}

func (d *utunDevice) Close() error {
	d.cancelReaders()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	return unix.Close(d.fd)
}

func validDevice(name string) bool {
	if !strings.HasPrefix(name, "utun") || len(name) < 5 || len(name) > 15 {
		return false
	}
	for _, c := range name[4:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

var _ io.ReadWriteCloser = (*utunDevice)(nil)
