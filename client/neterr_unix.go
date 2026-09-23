//go:build unix

package main

import (
	"errors"
	"syscall"
)

// isStaleBindingError reports that a socket can no longer send, because its
// local address stopped being usable after an uplink change. Reconnecting is
// the only fix: net.DialUDP pins the source address for the socket's lifetime.
func isStaleBindingError(err error) bool {
	if err == nil {
		return false
	}
	for _, target := range []error{
		syscall.EPERM,
		syscall.EACCES,
		syscall.EADDRNOTAVAIL,
		syscall.ENETUNREACH,
		syscall.EHOSTUNREACH,
		syscall.ENETDOWN,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}
