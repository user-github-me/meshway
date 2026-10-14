//go:build !linux || android

package linuxvpn

import "errors"

func Start(o Options) error { return errors.New("automatic Linux VPN setup requires Linux") }
func Stop(o Options) error  { return errors.New("automatic Linux VPN setup requires Linux") }
func Status(o Options) (string, error) {
	return "", errors.New("automatic Linux VPN setup requires Linux")
}
