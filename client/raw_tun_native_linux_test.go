//go:build linux && !android

package main

import "testing"

// Rejected before /dev/net/tun is opened, so this runs unprivileged. The name
// reaches `ip` as an argument, which is why a shell metacharacter matters as
// much as a name the kernel would not take.
func TestCreateNativeRawTUNRejectsBadNames(t *testing.T) {
	names := []string{
		"qwdtt 0",
		"qwdtt0; reboot",
		"qwdtt/0",
		"waytoolongfortunname",
	}

	for _, name := range names {
		if _, err := createNativeRawTUN(name); err == nil {
			t.Errorf("%q: accepted", name)
		}
	}
}

// Both are checked before ip runs, so nothing here touches the router.
func TestConfigureRejectsBadAddressAndMTU(t *testing.T) {
	tests := []struct {
		name    string
		address string
		mtu     int
	}{
		{"not an address", "not-an-address", 1300},
		{"IPv6, and this tunnel carries IPv4", "fd00::1", 1300},
		{"MTU below the IPv4 minimum", "10.70.0.2", 100},
		{"MTU past any link that exists", "10.70.0.2", 100000},
	}

	tun := &nativeRawTUN{name: "qwdtt0"}
	for _, tc := range tests {
		if err := tun.configure(tc.address, tc.mtu); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
}
