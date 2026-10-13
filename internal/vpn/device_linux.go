//go:build linux && !android

package vpn

import (
	"errors"
	"golang.org/x/sys/unix"
)

// OpenDevice creates or attaches a Linux TUN interface without changing routes.
// The returned descriptor belongs to the caller.
func OpenDevice(name string) (int, error) {
	if name == "" || len(name) >= unix.IFNAMSIZ {
		return -1, errors.New("TUN name must contain 1 to 15 characters")
	}
	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	req, err := unix.NewIfreq(name)
	if err != nil {
		unix.Close(fd)
		return -1, err
	}
	req.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI)
	if err = unix.IoctlIfreq(fd, unix.TUNSETIFF, req); err != nil {
		unix.Close(fd)
		return -1, err
	}
	return fd, nil
}
