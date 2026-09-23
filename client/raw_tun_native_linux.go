//go:build linux && !android

package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

var interfaceNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.:-]+$`)

type nativeRawTUN struct {
	file *os.File
	name string
}

func createNativeRawTUN(name string) (*nativeRawTUN, error) {
	if name == "" {
		name = "qwdtt0"
	}
	if !validInterfaceName(name) {
		return nil, fmt.Errorf("invalid interface name")
	}

	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/net/tun: %w", err)
	}
	ifr, err := unix.NewIfreq(name)
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("create ifreq: %w", err)
	}
	ifr.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI)
	if err := unix.IoctlIfreq(fd, unix.TUNSETIFF, ifr); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("TUNSETIFF: %w", err)
	}
	// The device outlives this process, and TUNSETIFF attaches to it again on
	// the next start. Without that every restart destroys it, and a new one
	// gets a new interface index: a socket bound to the tunnel with
	// SO_BINDTODEVICE - curl --interface, iperf3 --bind-dev - then points at
	// an index that no longer exists and can never send again. Taking the
	// device away is proto_qwdtt_teardown's job alone.
	if err := unix.IoctlSetInt(fd, unix.TUNSETPERSIST, 1); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("TUNSETPERSIST: %w", err)
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("set blocking TUN: %w", err)
	}

	return &nativeRawTUN{
		file: os.NewFile(uintptr(fd), "/dev/net/tun"),
		name: name,
	}, nil
}

func validInterfaceName(name string) bool {
	return len(name) > 0 && len(name) < unix.IFNAMSIZ && interfaceNamePattern.MatchString(name)
}

// Only for a client running on its own. Under the netifd protocol handler the
// address, the MTU and every route belong to netifd, which is told about them
// through /lib/netifd/qwdtt-up.sh instead.
//
// /16 rather than /32: the server hands out addresses from one 10.x.0.0/16 and
// expects its peers to reach each other directly.
func (t *nativeRawTUN) configure(address string, mtu int) error {
	if net.ParseIP(address).To4() == nil {
		return fmt.Errorf("invalid raw IPv4 address %q", address)
	}
	if mtu < 576 || mtu > 9000 {
		return fmt.Errorf("invalid MTU %d", mtu)
	}
	if err := runNativeCommand("ip", "addr", "replace", address+"/16", "dev", t.name); err != nil {
		return err
	}
	return runNativeCommand("ip", "link", "set", "dev", t.name, "mtu", strconv.Itoa(mtu), "up")
}

// Detaching only. The device stays behind on purpose - see TUNSETPERSIST above
// - so the next client attaches to the same interface index.
func (t *nativeRawTUN) cleanup() {
	_ = t.file.Close()
}

// Everything cleanup deliberately leaves. Only the self-test wants this: it
// creates a tunnel nobody asked for and has to leave the router as it found it.
func (t *nativeRawTUN) destroy() {
	t.cleanup()
	_ = runNativeCommand("ip", "link", "del", t.name)
}

func runNativeCommand(args ...string) error {
	return runNativeCommandEnv(nil, args...)
}

func runNativeCommandEnv(env []string, args ...string) error {
	cmd := exec.Command(args[0], args[1:]...)
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("%s: %s", strings.Join(args, " "), message)
	}
	return nil
}
