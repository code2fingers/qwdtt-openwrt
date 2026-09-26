package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A qWDTT tunnel's address, resolvers and MTU are not known until the server
// answers, so netifd cannot be given them when the interface is brought up. It
// is told afterwards, the way udhcpc tells it about a lease: the protocol
// handler starts this client, and this client runs the up-script once RAWCONF
// arrives. Shelling out rather than speaking ubus keeps the client free of a
// ubus dependency and puts the netifd calls where every other OpenWrt daemon
// protocol puts them.
const netifdUpScript = "/lib/netifd/qwdtt-up.sh"

const netifdErrorScript = "/lib/netifd/qwdtt-error.sh"

// Shared with /lib/netifd/qwdtt-up.sh, which writes the counter baseline
// beside these files. On tmpfs, so everything in it lasts exactly one boot,
// which is as long as the tunnel device does.
const netifdRunDir = "/var/run/qwdtt"

// How many worker slots the client settled on after its own clamping, so that
// the status page can say "4 of 9" rather than a bare count that means nothing
// without the total beside it.
var netifdWorkerSlots int

// Set when -netifd is given, because the workers that hit a fatal condition
// are several call frames away from the flag and have no other reason to know
// how the client was started.
var netifdManaged bool

// Refusals a reconnect cannot clear. Told to netifd, they appear against the
// interface on Network -> Interfaces; left in the log, they are a line the
// operator has to go looking for while the tunnel retries for ever.
//
// Matched on the message rather than plumbed through from where the server's
// DENIED reason is parsed, so that protocol.go stays free of netifd: it is
// shared with a build that has none.
var netifdErrors = []struct{ contains, code string }{
	{"the password is bound to another device", "QWDTT_DEVICE_MISMATCH"},
	{"the password has expired", "QWDTT_PASSWORD_EXPIRED"},
	{"wrong connection password", "QWDTT_WRONG_PASSWORD"},
	{"хеш мёртв", "QWDTT_HASH_DEAD"},
	{"FATAL_AUTH", "QWDTT_AUTH_FAILED"},
}

func netifdErrorCode(message string) string {
	for _, e := range netifdErrors {
		if strings.Contains(message, e.contains) {
			return e.code
		}
	}
	return ""
}

// Reported once. Every worker meets the same refusal, and netifd wants to know
// what is wrong with the interface, not how many streams noticed.
var netifdErrorOnce sync.Once

func notifyNetifdError(message string) {
	code := netifdErrorCode(message)
	if !netifdManaged || code == "" {
		return
	}
	netifdErrorOnce.Do(func() {
		if err := runNativeCommandEnv([]string{"ERROR=" + code}, netifdErrorScript); err != nil {
			log.Printf("[NETIFD] reporting %s: %v", code, err)
		}
	})
}

func notifyNetifd(device, address, dnsCSV string, mtu int) error {
	if os.Getenv("INTERFACE") == "" {
		return fmt.Errorf("INTERFACE is unset: -netifd only works under the qwdtt protocol handler")
	}
	return runNativeCommandEnv(netifdUpEnv(device, address, dnsCSV, mtu), netifdUpScript)
}

// The up-script iterates DNS unquoted, so the separator has to be whitespace.
// Splitting rather than replacing drops the empty fields a trailing comma would
// otherwise turn into an empty resolver.
func netifdUpEnv(device, address, dnsCSV string, mtu int) []string {
	servers := strings.FieldsFunc(dnsCSV, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	return []string{
		"DEVICE=" + device,
		"IPADDR=" + address,
		"DNS=" + strings.Join(servers, " "),
		"MTU=" + strconv.Itoa(mtu),
	}
}

func writeNetifdRunFile(suffix, content string) {
	iface := os.Getenv("INTERFACE")
	if !netifdManaged || iface == "" {
		return
	}
	if err := os.MkdirAll(netifdRunDir, 0755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(netifdRunDir, iface+"."+suffix), []byte(content), 0644)
}

// How the tunnel's sessions are doing, written where the status page can read
// it. The dispatcher is the only thing that knows, and it learns by workers
// arriving and leaving rather than at any one moment, so this is rewritten on
// each change rather than sampled.
//
// Three facts come out of the same transitions. How many workers are carrying
// traffic is the obvious one. How many have had to be re-established since the
// client started is the one that shows a tunnel flapping: a worker that drops
// is rebuilt, so the count climbing is churn nothing else reports. And the
// moment the tunnel last had no session at all is what the page counts
// "connected for" from - distinct from the interface uptime, which netifd
// keeps running through an outage the sessions did not survive.
var (
	netifdSessionMu   sync.Mutex
	netifdPrevActive  int
	netifdReconnects  int
	netifdConnectedAt int64
)

func reportNetifdWorkers(active int) {
	netifdSessionMu.Lock()
	if active < netifdPrevActive {
		netifdReconnects += netifdPrevActive - active
	}
	netifdPrevActive = active
	if active == 0 {
		netifdConnectedAt = 0
	} else if netifdConnectedAt == 0 {
		netifdConnectedAt = time.Now().Unix()
	}
	line := fmt.Sprintf("%d %d %d %d\n", active, netifdWorkerSlots,
		netifdReconnects, netifdConnectedAt)
	netifdSessionMu.Unlock()

	writeNetifdRunFile("workers", line)
}

// When the tunnel last carried a byte, which is the one thing on the status
// page that answers "is it working".
//
// Everything else there is counted from a session being established: a worker
// registers when its session reports ready, so the worker count and the clock
// started from it both go on rising while the server accepts the sessions and
// forwards nothing. That state has been seen for hours at a time, reading as a
// tunnel eight hours healthy.
//
// Inbound only. What this has to answer is whether the far end is still
// delivering, and bytes this client sent prove nothing about that: a curl
// through a tunnel that carries nothing still fills the outbound counter, and
// counting it put "last traffic: 2 seconds ago" on a tunnel that had delivered
// nothing for hours - the very reading this exists to stop.
//
// Sampled rather than stamped per packet: the dispatcher already counts the
// bytes on the data path, so a ticker comparing the total costs one comparison
// every few seconds instead of a write per packet.
const netifdTrafficTick = 5 * time.Second

// How long the tunnel may deliver nothing before the client pokes it, and how
// long before it stops believing in it.
//
// Nothing in the client notices a tunnel that has stopped delivering. Every
// session can be established and every worker registered while the far end
// forwards none of it: the Reader re-arms its read deadline on every timeout
// rather than giving up, the relay reader has no deadline at all, and the
// result of the periodic TURN binding refresh is discarded. Measured on a
// router, the tunnel sat in that state for seven and a half hours, showing
// thirty-six of thirty-six workers, no reconnects and a session clock at
// fourteen hours, and one ifdown/ifup put it right in twenty seconds.
//
// Silence alone cannot be the trigger. The server answers nothing on an idle
// tunnel - measured, the only thing coming back is one STUN binding response
// per session per ten seconds, from the relay rather than from the server -
// so a watchdog on silence restarts a tunnel nobody is using. An earlier
// attempt inferred it instead, from the LAN sending while nothing came back,
// and that missed the outage above entirely: the router was not sending, so
// there was nothing to infer from.
//
// So the client makes the traffic itself. After netifdProbeAfter of nothing
// arriving it sends an echo through the tunnel to the server every tick, and
// a reply is inbound traffic like any other, which puts the question beyond
// inference: either the tunnel delivers the answer or it delivers nothing.
const (
	netifdProbeAfter   = 30 * time.Second
	netifdStallTimeout = 2 * time.Minute
)

// What the watch does at a given moment. Pulled out of the loop and given a
// test because getting it wrong takes down a working tunnel.
//
// idle is how long since anything at all arrived. answered says the server has
// replied to a probe at least once: a server that never has may simply not
// answer them, and taking its tunnel down every couple of minutes over that
// would be worse than the fault being looked for.
func netifdWatchAction(idle time.Duration, answered bool) (probe, giveUp bool) {
	if idle < netifdProbeAfter {
		return false, false
	}
	return true, answered && idle >= netifdStallTimeout
}

func startNetifdTrafficWatch(ctx context.Context, cancel context.CancelFunc, stats *Stats, device string, server net.IP) {
	if !netifdManaged || stats == nil {
		return
	}

	go func() {
		t := time.NewTicker(netifdTrafficTick)
		defer t.Stop()

		var last int64
		var seenAt int64
		var probe *tunnelProbe
		var probeErr bool
		defer func() {
			if probe != nil {
				probe.close()
			}
		}()

		// Counted from the client starting rather than from the first byte, so
		// a tunnel that never delivers anything is still probed. Escalating on
		// one is a separate question, and netifdWatchAction answers it.
		lastSeen := time.Now()

		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}

			now := time.Now()
			total := stats.TotalBytesDown.Load()
			if total != last {
				last = total
				seenAt = now.Unix()
				lastSeen = now
			}
			// Written every tick rather than only on change, so a page reading
			// it can tell "nothing yet" from a file nobody has updated.
			writeNetifdRunFile("traffic", fmt.Sprintf("%d %d\n", seenAt, total))

			idle := now.Sub(lastSeen)
			shouldProbe, giveUp := netifdWatchAction(idle, probe != nil && probe.everAnswered())

			if giveUp {
				log.Printf("[NETIFD] nothing has come back through the tunnel for %v, and the server has stopped answering it, giving the interface up so it is rebuilt",
					idle.Truncate(time.Second))
				cancel()
				return
			}
			if !shouldProbe {
				continue
			}

			// Opened late on purpose: the device does not exist until the
			// server has answered with an address and the up-script has run,
			// so there is nothing to bind to before then.
			if probe == nil {
				p, err := newTunnelProbe(ctx, device)
				if err != nil {
					// Tried again on the next tick rather than given up on:
					// the usual reason is a device that has no address yet.
					// Said once, or it is said every five seconds until it
					// has one.
					if !probeErr {
						probeErr = true
						log.Printf("[NETIFD] cannot probe the tunnel yet: %v", err)
					}
					continue
				}
				probe, probeErr = p, false
			}
			if err := probe.send(server); err != nil {
				log.Printf("[NETIFD] probing the tunnel: %v", err)
				// The address it is bound to has most likely moved. A fresh
				// socket picks the new one up, so drop this one and let the
				// next tick build it again.
				probe.close()
				probe = nil
			}
		}
	}()
}

// Which VK relays the tunnel is actually on. A session picks one out of the
// list its credentials came with and stays on it, and that list is the wrong
// thing to report: it names relays that were offered and never answered. So
// the sessions are counted per address, and an address is listed for as long
// as one is holding it.
var (
	netifdRelayMu    sync.Mutex
	netifdRelaySlots = map[string]int{}
)

func reportNetifdRelay(addr string, delta int) {
	if !netifdManaged {
		return
	}

	netifdRelayMu.Lock()
	netifdRelaySlots[addr] += delta
	if netifdRelaySlots[addr] < 1 {
		delete(netifdRelaySlots, addr)
	}
	addrs := make([]string, 0, len(netifdRelaySlots))
	for a := range netifdRelaySlots {
		addrs = append(addrs, a)
	}
	netifdRelayMu.Unlock()

	// Sorted, or the page reshuffles the list on every poll for want of an
	// order of its own.
	sort.Strings(addrs)
	writeNetifdRunFile("relays", strings.Join(addrs, " ")+"\n")
}

// How often VK has put a captcha in front of this tunnel, and how often the
// client got past it. Nothing else says so: a captcha the solver answers leaves
// the tunnel working and shows up nowhere, and a captcha it cannot answer looks
// from the outside like credentials that will not come, so the two are worth
// telling apart on the page.
//
// Counted per challenge rather than per attempt. The solver is called again for
// the same captcha up to three times, and VK identifies the challenge by its
// sid, so a repeat of a sid already seen is the same captcha being retried.
var (
	netifdCaptchaMu     sync.Mutex
	netifdCaptchaSeen   = map[string]bool{}
	netifdCaptchaFaced  int
	netifdCaptchaSolved int
)

func reportNetifdCaptcha(sid string, solved bool) {
	if !netifdManaged {
		return
	}

	netifdCaptchaMu.Lock()
	// An empty sid cannot be told apart from the last one, so it counts as its
	// own challenge rather than silently folding into another.
	if sid == "" || !netifdCaptchaSeen[sid] {
		if sid != "" {
			netifdCaptchaSeen[sid] = true
		}
		netifdCaptchaFaced++
	}
	if solved {
		netifdCaptchaSolved++
	}
	line := fmt.Sprintf("%d %d\n", netifdCaptchaSolved, netifdCaptchaFaced)
	netifdCaptchaMu.Unlock()

	writeNetifdRunFile("captcha", line)
}
