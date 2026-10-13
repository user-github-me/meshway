//go:build linux || android || darwin || freebsd || openbsd || netbsd

package vpn

import "golang.org/x/sys/unix"

func duplicateFD(fd int) (int, error) {
	copyFD, err := unix.Dup(fd)
	if err != nil {
		return -1, err
	}
	unix.CloseOnExec(copyFD)
	// Go's poller must own blocking waits so closing the file wakes a pending read.
	// The duplicate shares file status flags with the original TUN descriptor.
	if err = unix.SetNonblock(copyFD, true); err != nil {
		unix.Close(copyFD)
		return -1, err
	}
	return copyFD, nil
}
func closeFD(fd int) error { return unix.Close(fd) }
