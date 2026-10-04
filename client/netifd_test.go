package main

import (
	"strings"
	"testing"
	"time"
)

// The server's RAWCONF separates resolvers with commas and the up-script
// iterates DNS as a word list, so this conversion is the whole contract
// between them.
func TestNetifdUpEnvDNS(t *testing.T) {
	tests := []struct {
		name string
		csv  string
		want string
	}{
		{"one resolver", "10.70.0.1", "DNS=10.70.0.1"},
		{"several", "10.70.0.1,1.1.1.1", "DNS=10.70.0.1 1.1.1.1"},
		{"spaces after the commas", "10.70.0.1, 1.1.1.1", "DNS=10.70.0.1 1.1.1.1"},
		{"a trailing comma adds no empty resolver", "10.70.0.1,", "DNS=10.70.0.1"},
		{"none at all", "", "DNS="},
	}

	for _, tc := range tests {
		got := netifdUpEnv("qwdtt0", "10.70.0.2", tc.csv, 1300)
		if !hasEnv(got, tc.want) {
			t.Errorf("%s: got %q, want %q in it", tc.name, got, tc.want)
		}
	}
}

func TestNetifdUpEnvCarriesTheDevice(t *testing.T) {
	got := netifdUpEnv("qwdtt0", "10.70.0.2", "10.70.0.1", 1300)

	for _, want := range []string{"DEVICE=qwdtt0", "IPADDR=10.70.0.2", "MTU=1300"} {
		if !hasEnv(got, want) {
			t.Errorf("got %q, want %q in it", got, want)
		}
	}
}

// The name alone, so a test for DNS= does not pass on DNS=10.70.0.1.
func hasEnv(env []string, want string) bool {
	name := want[:strings.Index(want, "=")+1]

	for _, entry := range env {
		if strings.HasPrefix(entry, name) {
			return entry == want
		}
	}
	return false
}

// The point is which refusal is reported, not that one is: an operator reading
// "Unknown error (FATAL_AUTH)" on the interface learns nothing they could act
// on, and the three password refusals need three different corrections.
func TestNetifdErrorCode(t *testing.T) {
	tests := []struct {
		message string
		want    string
	}{
		{"FATAL_AUTH: the password is bound to another device", "QWDTT_DEVICE_MISMATCH"},
		{"FATAL_AUTH: the password has expired", "QWDTT_PASSWORD_EXPIRED"},
		{"FATAL_AUTH: wrong connection password", "QWDTT_WRONG_PASSWORD"},
		{"FATAL_AUTH: access denied (banned)", "QWDTT_AUTH_FAILED"},
		{"хеш мёртв", "QWDTT_HASH_DEAD"},
		// A dead call reaches here as a CallUnavailableError, whose text has to
		// map to the same code or the group would retry it for ever.
		{(&CallUnavailableError{Code: 951, Message: "call not found"}).Error(), "QWDTT_HASH_DEAD"},
		{(&CallUnavailableError{Code: 954}).Error(), "QWDTT_HASH_DEAD"},
		{"TURN Allocate: error 401", ""},
		{"", ""},
	}

	for _, tc := range tests {
		if got := netifdErrorCode(tc.message); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.message, got, tc.want)
		}
	}
}

// The watch takes a working tunnel down if it is wrong, so both directions
// matter: probing a tunnel that is delivering is waste, and giving up on one
// whose server never answered a probe is worse than the fault.
func TestNetifdWatchAction(t *testing.T) {
	const young = time.Minute        // inside the arming window
	const old = 10 * time.Minute     // past it
	const busy = time.Second         // traffic is arriving
	const quiet = netifdStallTimeout // nothing has arrived for a long time

	tests := []struct {
		name     string
		idle     time.Duration
		age      time.Duration
		answered bool
		probe    bool
		giveUp   bool
	}{
		// Before the server has answered, the probe goes out whatever the
		// tunnel is doing. This is the case a router got wrong: a tunnel
		// carrying traffic was never probed, so when it died there was
		// nothing to tell a dead server from one that ignores echoes.
		{"working but not yet armed, ask anyway", busy, young, false, true, false},
		{"quiet and not yet armed, ask", quiet, young, false, true, false},

		// Once the window closes with no answer, this server does not answer
		// echoes. Stop asking, and never act on silence.
		{"never answered, window closed", quiet, old, false, false, false},
		{"never answered, window closed, still quiet later", time.Hour, old, false, false, false},

		// Armed.
		{"delivering, leave it alone", busy, old, true, false, false},
		{"just short of the probe", netifdProbeAfter - time.Second, old, true, false, false},
		{"quiet enough to poke", netifdProbeAfter, old, true, true, false},
		{"quiet past the timeout, give up", quiet, old, true, true, true},
		{"long past it", time.Hour, old, true, true, true},
	}

	for _, tc := range tests {
		probe, giveUp := netifdWatchAction(tc.idle, tc.age, tc.answered)
		if probe != tc.probe || giveUp != tc.giveUp {
			t.Errorf("%s: got probe=%v giveUp=%v, want probe=%v giveUp=%v",
				tc.name, probe, giveUp, tc.probe, tc.giveUp)
		}
	}
}

// A wrong checksum is dropped by the far end in silence, which would read
// here as a server that never answers.
// The wireguard-mode watch has no probe to fall back on, so its whole safety
// rests on telling "we are sending and nothing comes back" from "nothing is
// pointed at this relay". Getting the second one wrong would restart a tunnel
// every five minutes for as long as it went unused.
func TestNetifdRelayStalled(t *testing.T) {
	const busy = time.Second
	const quiet = netifdStallTimeout
	const nearly = netifdStallTimeout - time.Second

	tests := []struct {
		name     string
		idleIn   time.Duration
		idleOut  time.Duration
		received bool
		want     bool
	}{
		// The fault: WireGuard is keepaliving through the relay and the far
		// end has stopped delivering.
		{"sending, nothing coming back", quiet, busy, true, true},
		{"long past it", time.Hour, busy, true, true},

		// No WireGuard interface pointed at the relay yet, or one that is
		// down. Nothing is crossing in either direction and there is nothing
		// to rescue.
		{"nothing pointed at the relay", quiet, quiet, true, false},
		{"neither direction, for hours", time.Hour, time.Hour, true, false},

		// Working.
		{"both directions moving", busy, busy, true, false},
		{"inbound just short of the timeout", nearly, busy, true, false},

		// Inbound stalled and outbound stopping at the same time is the
		// interface being taken down, not the tunnel failing.
		{"outbound stopped too, right at the edge", quiet, quiet - time.Second, true, true},
		{"outbound stopped first", quiet, quiet + time.Minute, true, false},

		// Never delivered anything. Measured on a router: without this gate
		// a tunnel whose relays were blocked from the start gave itself up
		// at five minutes and would have gone on doing so, rebuilding the
		// whole session fleet each time. The likeliest causes - a peer port
		// that is not the server's listener, or wrong keys on the WireGuard
		// interface above a relay that is working - are none of them fixed
		// by a restart.
		{"never received, sending hard", quiet, busy, false, false},
		{"never received, hours of it", time.Hour, busy, false, false},
	}

	for _, tc := range tests {
		got := netifdRelayStalled(tc.idleIn, tc.idleOut, tc.received)
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestICMPEcho(t *testing.T) {
	b := icmpEcho(0x1234)
	if len(b) != 8 || b[0] != icmpEchoRequest {
		t.Fatalf("not an echo request: %x", b)
	}
	// The checksum of a correct packet, checksum field included, is zero.
	if got := icmpChecksum(b); got != 0 {
		t.Errorf("checksum does not verify: got %#04x, want 0", got)
	}
}

// A raw ICMP read carries the IPv4 header on Linux and not everywhere, and
// mistaking the version nibble for an ICMP type is silent: it reads as a
// server that never answers, which is exactly the state that disarms the
// watch.
func TestICMPPayload(t *testing.T) {
	echo := icmpEcho(0x1234)
	withHeader := append([]byte{0x45, 0, 0, 28, 0, 0, 0, 0, 64, 1, 0, 0,
		10, 0, 0, 1, 10, 0, 0, 2}, echo...)

	if got := icmpPayload(withHeader); len(got) != len(echo) || got[0] != echo[0] {
		t.Errorf("header not skipped: got %x", got)
	}
	if got := icmpPayload(echo); len(got) != len(echo) {
		t.Errorf("bare message was altered: got %x", got)
	}
}
