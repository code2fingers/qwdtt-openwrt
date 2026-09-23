package main

import (
	"context"
	"log"
	"net"
	"time"
)

const (
	uplinkPollInterval = 2 * time.Second
	uplinkStableFor    = 10 * time.Second
	uplinkSettleMax    = 60 * time.Second
)

// currentSource returns the address the kernel would pick for packets to addr
// right now. Nothing is sent: for UDP, net.Dial only performs connect(2), after
// which LocalAddr reports the chosen source.
func currentSource(addr string) string {
	c, err := net.Dial("udp", addr)
	if err != nil {
		return ""
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}
	return ""
}

// waitUplinkSettled waits until the source address towards addr stops moving,
// so that the reconnect spends one TURN allocation rather than one per
// intermediate address.
func waitUplinkSettled(ctx context.Context, addr string, wid int) {
	deadline := time.Now().Add(uplinkSettleMax)
	last := currentSource(addr)
	stableSince := time.Now()

	for {
		if ctx.Err() != nil {
			return
		}
		if time.Now().After(deadline) {
			log.Printf("[WORKER #%d] Address never settled within %s, connecting as is",
				wid, uplinkSettleMax)
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(uplinkPollInterval):
		}

		now := currentSource(addr)
		if now != last {
			last = now
			stableSince = time.Now()
			continue
		}
		// An empty address means no route to addr at all: that is not settled,
		// that is no uplink.
		if now != "" && time.Since(stableSince) >= uplinkStableFor {
			log.Printf("[WORKER #%d] Address settled (%s), reconnecting", wid, now)
			return
		}
	}
}
