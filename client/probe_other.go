//go:build !linux

package main

import (
	"context"
	"fmt"
	"net"
)

// The probe binds a raw ICMP socket to an interface, which is a Linux call.
// Nothing outside the netifd protocol handler uses it, and that only runs on
// OpenWrt, so elsewhere it simply is not available.
type tunnelProbe struct{}

func newTunnelProbe(context.Context, string) (*tunnelProbe, error) {
	return nil, fmt.Errorf("the tunnel probe needs Linux")
}

func (p *tunnelProbe) send(net.IP) error  { return nil }
func (p *tunnelProbe) everAnswered() bool { return false }
func (p *tunnelProbe) close()             {}
