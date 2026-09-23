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

// rotationGate serialises the session rotations that follow a change of uplink,
// one worker at a time. Nine at once exhaust VK's per-account allocation quota.
var rotationGate = make(chan struct{}, 1)

// rotationSpacing is how long a rotating worker holds the gate: long enough to
// cover its replacement's TURN handshake, short enough that migrating the whole
// fleet stays in the tens of seconds.
const rotationSpacing = 2 * time.Second

// acquireRotation blocks until this worker may rotate, or the session ends.
func acquireRotation(ctx context.Context) bool {
	select {
	case rotationGate <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// addressIsLocal reports whether ip is still assigned to an interface here. An
// unreadable interface list answers true: not being able to tell is no reason
// to tear a working session down.
func addressIsLocal(ip string) bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return true
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.String() == ip {
			return true
		}
	}
	return false
}

// watchSessionPath cancels the session when the path it was built on is gone:
// either the bound address vanished, or the kernel now prefers a different
// source, which raises no error and is not survivable.
func watchSessionPath(ctx context.Context, cancel context.CancelFunc, peerAddr, boundIP string, sessionID int) {
	startSrc := currentSource(peerAddr)

	t := time.NewTicker(uplinkPollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		// No gate here: there is no working allocation left to protect.
		if boundIP != "" && !addressIsLocal(boundIP) {
			log.Printf("[WORKER #%d] Bound address %s is gone, recreating session",
				sessionID, boundIP)
			cancel()
			return
		}

		now := currentSource(peerAddr)
		if now == "" || startSrc == "" || now == startSrc {
			continue
		}
		if !acquireRotation(ctx) {
			return
		}
		log.Printf("[WORKER #%d] Preferred address changed (%s -> %s), recreating session",
			sessionID, startSrc, now)
		cancel()
		time.AfterFunc(rotationSpacing, func() { <-rotationGate })
		return
	}
}
