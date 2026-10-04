package main

import (
	"context"
	"net"
	"syscall"
)

// listenUDP binds a UDP socket with SO_REUSEADDR so a quick restart can reclaim
// 127.0.0.1:port after the previous client process exits.
//
// The same option also lets an unrelated socket share the port, so a bind
// succeeding is no evidence that this relay has the port to itself. Two
// relays on one port is therefore refused by the protocol handler, which can
// see the configuration, rather than detected here, which cannot.
func listenUDP(addr string) (net.PacketConn, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var setErr error
			if err := c.Control(func(fd uintptr) {
				setErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
			}); err != nil {
				return err
			}
			return setErr
		},
	}
	return lc.ListenPacket(context.Background(), "udp", addr)
}
