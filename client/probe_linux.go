//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os"
	"sync/atomic"
	"syscall"
	"time"
)

// An ICMP echo sent through the tunnel to the server's own address, so that a
// tunnel nobody is using still has something to prove it is alive with.
//
// The server answers it itself: the packet leaves through the tunnel device,
// crosses a TURN relay, and arrives at the address the client was configured
// with, which is the server's. Nothing outside the connection the operator
// already set up is involved, and the reply comes back down the same path, so
// what this tests is the whole data path rather than any part of it.
//
// Bound to the device, and sourced from its address. The destination is the
// server's public address, which the main table reaches over the WAN, so
// SO_BINDTODEVICE is what keeps the probe from leaving by the WAN and proving
// nothing. That alone is not enough: the tunnel's default route lives in the
// interface's own table, and what directs a locally generated packet into
// that table is the rule netifd writes for the interface's source address. A
// socket left on 0.0.0.0 matches no such rule and sendto answers ENETUNREACH.
// Both together are what `ping -I <device>` does.
type tunnelProbe struct {
	conn     *net.IPConn
	id       uint16
	answered atomic.Bool
}

func newTunnelProbe(ctx context.Context, device string) (*tunnelProbe, error) {
	source, err := deviceIPv4(device)
	if err != nil {
		return nil, err
	}

	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			var serr error
			if err := c.Control(func(fd uintptr) {
				serr = syscall.SetsockoptString(int(fd),
					syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, device)
			}); err != nil {
				return err
			}
			return serr
		},
	}

	pc, err := lc.ListenPacket(ctx, "ip4:icmp", source.String())
	if err != nil {
		return nil, err
	}

	p := &tunnelProbe{conn: pc.(*net.IPConn), id: uint16(os.Getpid())}
	go p.drain(ctx)
	return p, nil
}

func deviceIPv4(device string) (net.IP, error) {
	iface, err := net.InterfaceByName(device)
	if err != nil {
		return nil, err
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			if v4 := n.IP.To4(); v4 != nil {
				return v4, nil
			}
		}
	}
	return nil, fmt.Errorf("%s has no IPv4 address yet", device)
}

func (p *tunnelProbe) send(ip net.IP) error {
	_, err := p.conn.WriteTo(icmpEcho(p.id), &net.IPAddr{IP: ip})
	return err
}

// Whether the server has ever answered. Silence is only worth acting on from
// a server that was answering: one that never has may simply not reply to
// this, and taking a working tunnel down every couple of minutes over that
// would be worse than the fault being looked for.
func (p *tunnelProbe) everAnswered() bool { return p.answered.Load() }

func (p *tunnelProbe) close() { _ = p.conn.Close() }

// A raw ICMP socket is handed every ICMP packet that arrives on the device, so
// it has to be read or the receive buffer fills and the kernel drops what is
// in it. Reading is also how a reply is noticed.
func (p *tunnelProbe) drain(ctx context.Context) {
	buf := make([]byte, 128)
	for {
		if ctx.Err() != nil {
			return
		}
		_ = p.conn.SetReadDeadline(time.Now().Add(time.Second))
		n, err := p.conn.Read(buf)
		if err != nil {
			continue
		}
		msg := icmpPayload(buf[:n])
		if len(msg) >= 8 && msg[0] == icmpEchoReply &&
			binary.BigEndian.Uint16(msg[4:6]) == p.id {
			if p.answered.CompareAndSwap(false, true) {
				log.Printf("[NETIFD] the server answered a probe through the tunnel")
			}
		}
	}
}

const (
	icmpEchoRequest = 8
	icmpEchoReply   = 0
)

// A read from a raw ICMP socket carries the IPv4 header on Linux and does not
// on every platform, and the difference is silent: the version nibble read as
// an ICMP type is 4, which is neither of the types here, so a reply would
// simply never be recognised and the tunnel would look like a server that
// does not answer. So the header is detected rather than assumed.
func icmpPayload(b []byte) []byte {
	if len(b) < 20 || b[0]>>4 != 4 {
		return b
	}
	ihl := int(b[0]&0x0f) * 4
	if ihl < 20 || ihl > len(b) {
		return nil
	}
	return b[ihl:]
}

// The sequence number is left at zero: nothing here pairs a reply with the
// request that caused it, only with this client, which the identifier does.
func icmpEcho(id uint16) []byte {
	b := make([]byte, 8)
	b[0] = icmpEchoRequest
	binary.BigEndian.PutUint16(b[4:6], id)
	binary.BigEndian.PutUint16(b[2:4], icmpChecksum(b))
	return b
}

// The internet checksum of RFC 1071: the one's complement of the one's
// complement sum of the 16-bit words.
func icmpChecksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i : i+2]))
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}
