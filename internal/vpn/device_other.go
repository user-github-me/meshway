//go:build !linux || android

package vpn

import "errors"

func OpenDevice(name string) (int, error) {
	return -1, errors.New("automatic TUN creation is available on Linux; Android must supply its VpnService descriptor")
}
