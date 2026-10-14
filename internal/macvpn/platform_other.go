//go:build !darwin || ios

package macvpn

import (
	"context"
	"errors"
)

func Run(context.Context, Options) error {
	return errors.New("macOS administrator utun helper requires macOS")
}
func Stop(Options) error { return errors.New("macOS administrator utun helper requires macOS") }
func Status(Options) (string, error) {
	return "", errors.New("macOS administrator utun helper requires macOS")
}
