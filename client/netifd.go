package main

import (
	"fmt"
	"log"
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
