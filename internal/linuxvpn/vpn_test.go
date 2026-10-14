package linuxvpn

import "testing"

func TestOptionsRejectDeviceCommandCharacters(t *testing.T) {
	for _, name := range []string{"", "toolongdevice123456", "meshway0;id", "meshway0\n", "../meshway", "meshway 0"} {
		if err := ValidateOptions(Options{ConfigPath: "config.json", Device: name, DNSMode: "resolved"}); err == nil {
			t.Fatalf("invalid device %q accepted", name)
		}
	}
	if err := ValidateOptions(Options{ConfigPath: "config.json", Device: "meshway0", DNSMode: "resolved"}); err != nil {
		t.Fatal(err)
	}
}
