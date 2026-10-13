//go:build windows

package vpn

import "errors"

func duplicateFD(fd int) (int, error) {
	return -1, errors.New("Windows packet VPN requires a native driver integration")
}
func closeFD(fd int) error { return nil }
