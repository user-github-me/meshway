//go:build !windows

package vpn

import (
	"io"
	"os"
)

func openPacketDevice(name string, mtu int) (io.ReadWriteCloser, error) {
	fd, err := OpenDevice(name)
	if err != nil {
		return nil, err
	}
	copyFD, err := duplicateFD(fd)
	_ = closeFD(fd)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(copyFD), "meshway-packet-tun"), nil
}
