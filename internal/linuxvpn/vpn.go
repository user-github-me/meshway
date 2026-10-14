// Package linuxvpn manages explicitly privileged, reversible Linux VPN setup.
package linuxvpn

import "errors"

type Options struct {
	ConfigPath string
	Device     string
	DNSMode    string
	RuntimeDir string
}

func ValidateOptions(o Options) error {
	if o.ConfigPath == "" {
		return errors.New("configuration path required")
	}
	if o.Device == "" {
		return errors.New("VPN device required")
	}
	if len(o.Device) > 15 {
		return errors.New("VPN device name exceeds 15 characters")
	}
	for _, r := range o.Device {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return errors.New("VPN device name must contain ASCII letters, digits, underscore or hyphen")
		}
	}
	if o.DNSMode != "resolved" && o.DNSMode != "file" {
		return errors.New("DNS mode must be resolved or file")
	}
	return nil
}
