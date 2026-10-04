package main

import (
	"context"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestTunnelLimitersAreOnlyBuiltWhenAsked(t *testing.T) {
	defer restoreLimits(t)

	initTunnelLimiters(18)
	if tunnelRecvLimiter != nil || tunnelSendLimiter != nil {
		t.Error("no limit configured, but a limiter was built")
	}

	// One direction limited must not pace the other.
	sessionRecvLimit, sessionSendLimit = 32000, 0
	initTunnelLimiters(18)
	if tunnelRecvLimiter == nil {
		t.Error("a download limit was configured but no limiter was built")
	}
	if tunnelSendLimiter != nil {
		t.Error("only the download was limited, but the upload was paced too")
	}

	restoreLimits(t)
	sessionRecvLimit, sessionSendLimit = 0, 32000
	initTunnelLimiters(18)
	if tunnelSendLimiter == nil {
		t.Error("an upload limit was configured but no limiter was built")
	}
	if tunnelRecvLimiter != nil {
		t.Error("only the upload was limited, but the download was paced too")
	}

	// No sessions is not a rate of nothing: it is a tunnel that carries
	// nothing yet, and pacing it to zero would wedge every writer.
	restoreLimits(t)
	sessionRecvLimit, sessionSendLimit = 32000, 32000
	initTunnelLimiters(0)
	if tunnelRecvLimiter != nil || tunnelSendLimiter != nil {
		t.Error("zero sessions built a limiter that could never release a packet")
	}
}

// The budget is written per session, so the tunnel's is that times the number
// of sessions - the figure the interface page promises and the one a user
// sets the limit against.
func TestTunnelLimitersSpendEverySessionsShare(t *testing.T) {
	defer restoreLimits(t)

	sessionRecvLimit, sessionSendLimit = 96000, 32000 // 768 and 256 Kbit/s
	initTunnelLimiters(18)

	if got, want := int(tunnelRecvLimiter.Limit()), 18*96000; got != want {
		t.Errorf("download paced to %d B/s, want %d", got, want)
	}
	if got, want := int(tunnelSendLimiter.Limit()), 18*32000; got != want {
		t.Errorf("upload paced to %d B/s, want %d", got, want)
	}
}

// Sessions come and go, and the tunnel's share has to follow them in both
// directions. The limit is written per session, so a tunnel that lost half of
// them must pass half as much rather than let the survivors take up the
// slack.
func TestTunnelLimitersFollowTheLiveSessionCount(t *testing.T) {
	defer restoreLimits(t)

	sessionRecvLimit, sessionSendLimit = 96000, 32000
	initTunnelLimiters(18)

	setTunnelSessions(9)
	if got, want := int(tunnelRecvLimiter.Limit()), 9*96000; got != want {
		t.Errorf("9 sessions left, download paced to %d B/s, want %d", got, want)
	}
	if got, want := int(tunnelSendLimiter.Limit()), 9*32000; got != want {
		t.Errorf("9 sessions left, upload paced to %d B/s, want %d", got, want)
	}

	// The last one going must not leave a bucket that releases nothing: a
	// writer waiting on a rate of zero never comes back.
	setTunnelSessions(0)
	if int(tunnelRecvLimiter.Limit()) == 0 || int(tunnelSendLimiter.Limit()) == 0 {
		t.Error("paced to zero, which a writer would wait on for ever")
	}

	// Unpaced stays unpaced, whatever the sessions do.
	restoreLimits(t)
	setTunnelSessions(18)
	if tunnelRecvLimiter != nil || tunnelSendLimiter != nil {
		t.Error("no limit configured, but registering a session built one")
	}
}

// A bucket is full when the tunnel is idle, so its burst is what leaves at
// line speed the moment there is something to pass. A burst of one whole
// second meant twenty-odd packets went out back to back and then nothing
// until it refilled. The TCP inside reads that as an on/off link rather than
// a slow one: it lost a window to tail drop at every pause and spent whole
// seconds in RTO backoff, so the transfer reported zero bytes for one second
// in seven.
func TestTunnelLimitersDoNotStoreUpASecond(t *testing.T) {
	defer restoreLimits(t)

	sessionRecvLimit, sessionSendLimit = 32000, 32000
	initTunnelLimiters(18)

	for _, c := range []struct {
		dir string
		lim *rate.Limiter
	}{{"download", tunnelRecvLimiter}, {"upload", tunnelSendLimiter}} {
		// One instant, so this measures the standing burst and not the rate.
		var atOnce int
		for now := time.Now(); c.lim.AllowN(now, 1400); {
			atOnce += 1400
		}
		if atOnce > 2*1400 {
			t.Errorf("%s released %d B at once, %.2fs of traffic at %d B/s: a paced tunnel should start at its rate, not empty a stored-up bucket",
				c.dir, atOnce, float64(atOnce)/float64(18*32000), 18*32000)
		}
	}
}

// A limit under one packet a second used to be a deadlock rather than a slow
// link: a bucket that cannot hold a packet never releases one.
func TestTunnelLimiterPacesAndNeverStalls(t *testing.T) {
	defer restoreLimits(t)

	sessionSendLimit = 100
	initTunnelLimiters(1)
	if tunnelSendLimiter.Burst() < pktBufSize {
		t.Errorf("burst %d cannot hold one packet (%d): the tunnel would stall, not slow down",
			tunnelSendLimiter.Burst(), pktBufSize)
	}

	// 16 KB/s: eight 2 KB packets past the initial burst should take about a
	// second, and certainly not arrive at once.
	sessionSendLimit = 16 * 1024
	initTunnelLimiters(1)
	ctx := context.Background()
	if err := tunnelSendLimiter.WaitN(ctx, tunnelSendLimiter.Burst()); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for i := 0; i < 8; i++ {
		if err := tunnelSendLimiter.WaitN(ctx, 2048); err != nil {
			t.Fatal(err)
		}
	}
	if el := time.Since(start); el < 700*time.Millisecond {
		t.Errorf("16 KB at 16 KB/s took %v: not paced", el)
	}
}

// The queues behind a paced sender, which is the part that made a tight limit
// behave like a broken link rather than a slow one.
func TestPacedQueuesAreSizedInTime(t *testing.T) {
	defer restoreLimits(t)

	if got := workerSendBufFor(0); got != workerSendBuf {
		t.Errorf("unpaced queue is %d, want the full %d", got, workerSendBuf)
	}
	// 2000 B/s is the case that failed: one packet, not 128, so a sender
	// learns it is over the limit in a packet's time instead of a minute's.
	if got := workerSendBufFor(2000); got != 1 {
		t.Errorf("at 2000 B/s the queue is %d packets, want 1", got)
	}
	// Never zero, or nothing could ever be queued at all.
	if got := workerSendBufFor(1); got < 1 {
		t.Errorf("queue of %d would take nothing", got)
	}
	// A fast limit is still capped at the unpaced depth.
	if got := workerSendBufFor(100 << 20); got != workerSendBuf {
		t.Errorf("a high limit gave %d, want it capped at %d", got, workerSendBuf)
	}
	// Roughly a sixth of a second at the configured rate.
	if got := workerSendBufFor(84000); got != 10 {
		t.Errorf("at 84000 B/s the queue is %d packets, want 10", got)
	}

	// The return queue is one for the whole tunnel, so it is sized from the
	// tunnel's rate and not from a session's share. Left full depth it is
	// over a second of buffering at a call-sized limit, which is the
	// bufferbloat the send side was already fixed for.
	if got := returnChBufFor(); got != returnChBuf {
		t.Errorf("unpaced return queue is %d, want the full %d", got, returnChBuf)
	}
	sessionRecvLimit = 32000
	initTunnelLimiters(18)
	if got, want := returnChBufFor(), 18*32000/6/1400; got != want {
		t.Errorf("at 256 Kbit/s over 18 sessions the return queue is %d packets, want %d", got, want)
	}
}

func restoreLimits(t *testing.T) {
	t.Helper()
	sessionRecvLimit, sessionSendLimit = 0, 0
	tunnelRecvLimiter, tunnelSendLimiter = nil, nil
}
