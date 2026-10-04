package main

import (
	"golang.org/x/time/rate"
)

// What one session may pass in each direction, in bytes per second. Zero
// leaves that direction alone. Set once from -rate-up and -rate-down before
// any session starts.
//
// Per session because that is what VK allocates and meters, and because
// several sessions share one relay address here, so a per-address limit would
// describe nothing VK sees. Measured unlimited, a session sends about 90 KB/s
// where a real call's uplink is a fraction of that.
var (
	sessionRecvLimit int
	sessionSendLimit int
)

// nil when that direction is not limited, so the packet path costs nothing in
// the usual case. The dispatcher's single writeLoop waits on the first; every
// session's Writer waits on the second.
var (
	tunnelRecvLimiter *rate.Limiter
	tunnelSendLimiter *rate.Limiter
)

func initTunnelLimiters(workers int) {
	tunnelRecvLimiter = newTunnelLimiter(sessionRecvLimit, workers)
	tunnelSendLimiter = newTunnelLimiter(sessionSendLimit, workers)
}

// One bucket for the whole tunnel, at the per-session rate times the number
// of sessions. That it is not one bucket per session is the part that looks
// wrong and is not: a session paced on its own takes ~44ms to clear a packet
// at a call's rate, far longer than the dispatcher will dwell on one relay,
// so the run of consecutive packets that keeps a flow on a single relay no
// longer fits and every packet leaves by a different one. Relays differ in
// latency by tens of milliseconds, so the far side receives a shuffle and
// reads it as loss. Measured at 256 Kbit/s over 18 sessions, per session
// against shared: 502 fast retransmits in 25s against 97, six timeouts
// against none, and a quarter to a third of every second carrying nothing at
// all against none. Shared, those runs survive, the tunnel holds 93% of its
// budget instead of 85%, and each session still averages the rate it was
// given.
//
// The burst is one packet, which is both the least and the most that works.
// A bucket too small to hold a packet would never release one and the tunnel
// would stall rather than slow down; a bucket holding a second of traffic is
// full whenever the tunnel has been quiet, so it empties at line speed and
// then waits to refill, which the TCP inside reads as a link going on and off
// rather than as a slow one.
func newTunnelLimiter(perSession, workers int) *rate.Limiter {
	if perSession <= 0 || workers <= 0 {
		return nil
	}
	return rate.NewLimiter(rate.Limit(perSession*workers), pktBufSize)
}

// Follows the sessions that are actually carrying traffic, not the number
// asked for. A total fixed at the latter would let each survivor pass more
// than a call's worth whenever some of them failed to come up, which is the
// one thing the limits exist to prevent.
func setTunnelSessions(n int) {
	if n <= 0 {
		return
	}
	if tunnelRecvLimiter != nil {
		tunnelRecvLimiter.SetLimit(rate.Limit(sessionRecvLimit * n))
	}
	if tunnelSendLimiter != nil {
		tunnelSendLimiter.SetLimit(rate.Limit(sessionSendLimit * n))
	}
}

// How deep a paced queue may be. A queue has to be measured in time, not in
// packets: 128 packets is right behind a sender running at full speed and
// absurd behind one running at a couple of KB/s, where it is most of a minute
// of buffering. A TCP flow inside the tunnel reads that as a dead link and
// answers with RTO backoff rather than with a smaller window, which is worse
// than the loss it was avoiding - measured at 126 Kbit/s of 288 available,
// 178 retransmits in 25s, and large-packet latency up to a second.
//
// About a sixth of a second, and never less than one packet, so the excess is
// refused early enough for the sender to notice at once.
func queueFor(bytesPerSec, unpaced int) int {
	if bytesPerSec <= 0 {
		return unpaced
	}
	n := bytesPerSec / 6 / 1400
	if n < 1 {
		n = 1
	}
	if n > unpaced {
		n = unpaced
	}
	return n
}

// One queue per session on the way out, so it takes the per-session share and
// the queues together come to a sixth of a second at the tunnel's rate
// however many sessions there are.
func workerSendBufFor(bytesPerSec int) int {
	return queueFor(bytesPerSec, workerSendBuf)
}

// One queue for the whole tunnel on the way in, so it takes the tunnel's rate
// directly. Read after initTunnelLimiters and before the dispatcher is built.
func returnChBufFor() int {
	if tunnelRecvLimiter == nil {
		return returnChBuf
	}
	return queueFor(int(tunnelRecvLimiter.Limit()), returnChBuf)
}
