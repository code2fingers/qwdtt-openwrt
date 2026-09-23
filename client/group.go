package main

import (
	"context"
	"log"
	"math/rand"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const workersPerGroup = 9

// allocateGateInterval is the minimum interval between TURN Allocate requests
// within one worker group (see the comment on allocateTicker in
// WorkerGroup). The same order of magnitude as free-turn-proxy (200ms).
const allocateGateInterval = 200 * time.Millisecond

// WorkerGroup:
// Starts 9 streams on one set of credentials. There is no rotation - it runs until the workers die.
func WorkerGroup(
	ctx context.Context,
	groupID int,
	hashIndex int,
	tp *TurnParams,
	peer *net.UDPAddr,
	d *Dispatcher,
	localPort string,
	getConfig bool,
	configCh chan<- string,
	workerIDs []int,
	pauseFlag *int32,
	deviceID, password string,
	stats *Stats,
	waitReady <-chan struct{},
	signalReady chan<- struct{},
) {
	// Cascaded start: wait for our turn
	if waitReady != nil {
		log.Printf("[GROUP #%d] Waiting for the signal from the previous group...", groupID)
		select {
		case <-waitReady:
		case <-ctx.Done():
			return
		}
	}

	var configSent int32
	if !getConfig {
		configSent = 1
	}

	// Doze-mode pause
	for atomic.LoadInt32(pauseFlag) != 0 {
		if ctx.Err() != nil {
			return
		}
		time.Sleep(1 * time.Second)
	}

	hash := tp.Hashes[hashIndex%len(tp.Hashes)]
	shortHash := hash
	if len(shortHash) > 8 {
		shortHash = shortHash[:8]
	}
	log.Printf("[GROUP #%d] Requesting credentials (hash: %s...)", groupID, shortHash)

	// The next group waits on this, so it has to be released whatever happens
	// below. A group that returned without handing the baton on left every
	// later group blocked for ever, and main's wg.Wait() with them: the client
	// stayed alive doing nothing, which is also why netifd never restarted it.
	var handedOver sync.Once
	handOver := func() bool {
		released := false
		if signalReady != nil {
			handedOver.Do(func() {
				close(signalReady)
				released = true
			})
		}
		return released
	}
	defer handOver()

	credStreamID := groupID * 100
	var creds *Credentials
	for attempt := 1; ; attempt++ {
		user, pass, turnURLs, err := GetCreds(ctx, hash, credStreamID)
		if err == nil {
			creds = &Credentials{User: user, Pass: pass, TurnURLs: turnURLs, CacheStreamID: credStreamID}
			break
		}
		if ctx.Err() != nil {
			return
		}

		// Asking again will not revive a dead hash, and the interface should be
		// told rather than left waiting behind a tunnel that is not coming up.
		errStr := err.Error()
		if strings.Contains(errStr, "хеш мёртв") ||
			strings.Contains(errStr, "FATAL_AUTH") {
			log.Printf("[GROUP #%d] Fatal credentials error: %v", groupID, err)
			notifyNetifdError(errStr)
			return
		}

		// Anything else is worth asking again. At boot netifd brings the
		// interface up before the WAN has a route, so the first fetch fails for
		// want of a network rather than for want of credentials, and giving up
		// there left the tunnel down until somebody restarted it by hand.
		log.Printf("[GROUP #%d] Credentials error (attempt %d): %v", groupID, attempt, err)
		select {
		case <-time.After(time.Duration(5+rand.Intn(11)) * time.Second):
		case <-ctx.Done():
			return
		}
	}

	log.Printf("[GROUP #%d] Credentials OK, TURN: %v, %d workers", groupID, creds.TurnURLs, len(workerIDs))

	var configRequestInFlight int32
	var wg sync.WaitGroup
	var credsMu sync.RWMutex
	var refreshMu sync.Mutex
	var lastCredRefresh atomic.Int64

	refreshCreds := func(reason string) bool {
		refreshMu.Lock()
		defer refreshMu.Unlock()

		now := time.Now().Unix()
		last := lastCredRefresh.Load()
		if last > 0 && now-last < 15 {
			log.Printf("[TURN] Credentials were already refreshed %d s ago, waiting for the next retry (%s)", now-last, reason)
			return true
		}

		getStreamCache(credStreamID).invalidate(credStreamID)
		if getVkAuthMode() == "account" {
			invalidateInjectedTurnCreds(hash)
		}
		u, p, urls, refreshErr := GetCreds(ctx, hash, credStreamID)
		if refreshErr != nil {
			log.Printf("[TURN] Could not refresh credentials after %s: %v", reason, refreshErr)
			return false
		}

		credsMu.Lock()
		creds = &Credentials{User: u, Pass: p, TurnURLs: urls, CacheStreamID: credStreamID}
		credsMu.Unlock()
		lastCredRefresh.Store(time.Now().Unix())
		log.Printf("[TURN] Credentials refreshed after %s, TURN urls=%d", reason, len(urls))
		return true
	}

	// Signal the next group that we started successfully (credentials in hand + a head start)
	if signalReady != nil {
		go func() {
			delayMs := 1000 + rand.Intn(500)
			time.Sleep(time.Duration(delayMs) * time.Millisecond)
			if handOver() {
				log.Printf("[GROUP #%d] Started successfully! Handing the baton to the next group...", groupID)
			}
		}()
	}

	// A group-wide rate limit on TURN Allocate: no more than one new
	// allocation per tick, no matter how many workers are ready to make one
	// (the start stagger is a separate thing, see workerDelay below - it
	// spreads out the start of the goroutines, but not the Allocate retries
	// inside ones already running). Without this, on an unstable network
	// several workers still overlap and together burn through the VK quota
	// (error 486) faster than they should. See RunSession(allocateGate) in
	// session.go and the comment there about free-turn-proxy - the same trick.
	allocateTicker := time.NewTicker(allocateGateInterval)
	defer allocateTicker.Stop()

	for i, wid := range workerIDs {
		wg.Add(1)

		// Stagger: 200ms between workers
		workerDelay := time.Duration(i) * 200 * time.Millisecond

		go func(wid int, delay time.Duration) {
			defer wg.Done()

			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return
				}
			}

			shouldGetConfig := getConfig
			attempt := 0

			for {
				if ctx.Err() != nil {
					return
				}

				getConf := false
				if shouldGetConfig && atomic.LoadInt32(&configSent) == 0 {
					getConf = atomic.CompareAndSwapInt32(&configRequestInFlight, 0, 1)
				}
				var cc chan<- string
				if getConf {
					cc = configCh
				}

				credsMu.RLock()
				credsSnapshot := *creds
				credsSnapshot.TurnURLs = cloneStringSlice(creds.TurnURLs)
				credsMu.RUnlock()

				configDelivered, sessErr := RunSession(ctx, tp, peer, d, localPort,
					getConf, cc, wid, &credsSnapshot, deviceID, password, stats, allocateTicker.C)

				quotaRetry := false
				if getConf {
					if configDelivered {
						atomic.StoreInt32(&configSent, 1)
					} else {
						atomic.StoreInt32(&configRequestInFlight, 0)
					}
				}

				// Reconnect at once rather than after the 5-15s backoff below:
				// the failure is local, not VK's, so waiting only extends the
				// outage.
				if isStaleBindingError(sessErr) {
					log.Printf("[WORKER #%d] Uplink changed (%v), waiting for the address to settle", wid, sessErr)
					waitUplinkSettled(ctx, peer.String(), wid)
					// Every worker waits on the same address and so returns at
					// the same instant. Ungated, all nine Allocate together and
					// VK refuses the lot (error 486).
					if !acquireRotation(ctx) {
						return
					}
					time.AfterFunc(rotationSpacing, func() { <-rotationGate })
					continue
				}

				if sessErr != nil {
					if ctx.Err() != nil {
						return
					}
					errStr := sessErr.Error()
					errStrLower := strings.ToLower(errStr)

					turnAllocAttrMissing := strings.Contains(errStrLower, "turn allocate") &&
						strings.Contains(errStrLower, "attribute not found")
					isTurnQuota := strings.Contains(errStrLower, "quota") || strings.Contains(errStr, "486")
					quotaRetry = isTurnQuota
					turnCredRefreshNeeded := !isTurnQuota && (turnAllocAttrMissing ||
						strings.Contains(errStrLower, "turn allocate auth") ||
						strings.Contains(errStrLower, "invalid credential") ||
						strings.Contains(errStrLower, "stale nonce") ||
						strings.Contains(errStrLower, "allocation mismatch") ||
						strings.Contains(errStrLower, "error 508"))

					if hint := workerErrorHint(sessErr); hint != "" {
						errStr += " | " + hint
					} else if strings.Contains(errStrLower, "rate limit") ||
						strings.Contains(errStrLower, "flood control") ||
						strings.Contains(errStrLower, "ip mismatch") ||
						strings.Contains(errStrLower, "error 29") {
						errStr += " (VK-side error)"
					}

					// Stays Russian: nothing in this repo produces this text, it
					// arrives from the server, so translating it here would
					// silently stop dead-hash detection.
					if strings.Contains(errStr, "хеш мёртв") ||
						strings.Contains(errStr, "FATAL_AUTH") {
						log.Printf("[WORKER #%d] Fatal error: %s", wid, errStr)
						notifyNetifdError(errStr)
						return
					}

					attempt++
					if isTurnQuota {
						log.Printf("[WORKER #%d] [TURN] Relay quota exhausted (one VK account = few slots), waiting: %s", wid, errStr)
					} else if turnAllocAttrMissing {
						log.Printf("[WORKER #%d] [TURN] Allocate returned an incomplete response, refreshing TURN credentials and retrying (attempt %d): %s", wid, attempt, errStr)
						refreshCreds("TURN Allocate attribute-not-found")
					} else if turnCredRefreshNeeded {
						log.Printf("[WORKER #%d] [TURN] Allocation/credentials error, refreshing TURN credentials and retrying (attempt %d): %s", wid, attempt, errStr)
						refreshCreds("TURN allocation error")
					} else {
						log.Printf("[WORKER #%d] Error (attempt %d): %s", wid, attempt, errStr)
					}

					// On a STUN error (credentials invalid) the worker cannot reconnect. Stop it.
					isStunDeath := strings.Contains(errStrLower, "error 29") ||
						strings.Contains(errStrLower, "cannot create socket")

					if isStunDeath {
						log.Printf("[WORKER #%d] Unrecoverable TURN/STUN error, stopping: %s", wid, errStr)
						return
					}
				}

				if ctx.Err() != nil {
					return
				}

				retryDelay := time.Duration(5+rand.Intn(11)) * time.Second
				if quotaRetry {
					retryDelay = time.Duration(30+rand.Intn(31)) * time.Second
				}
				select {
				case <-time.After(retryDelay):
				case <-ctx.Done():
					return
				}
			}
		}(wid, workerDelay)
	}

	wg.Wait()
	log.Printf("[GROUP #%d] All workers in the group have finished.", groupID)
}

// ParseHashes parses a comma-separated string of hashes
func ParseHashes(raw string) []string {
	var result []string
	seen := make(map[string]struct{})
	for _, h := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	}) {
		h = normalizeVKJoinHash(h)
		if h != "" {
			if _, exists := seen[h]; exists {
				continue
			}
			seen[h] = struct{}{}
			result = append(result, h)
		}
	}
	return result
}

func normalizeVKJoinHash(input string) string {
	s := strings.Trim(strings.TrimSpace(input), "<>\"'")
	if s == "" {
		return ""
	}

	lower := strings.ToLower(s)
	if idx := strings.Index(lower, "/call/join/"); idx >= 0 {
		s = s[idx+len("/call/join/"):]
	} else if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return ""
	}

	if idx := strings.IndexAny(s, "?#/"); idx != -1 {
		s = s[:idx]
	}
	return strings.Trim(strings.TrimSpace(s), "/")
}

// TurnParams is the TURN configuration
type TurnParams struct {
	Host     string
	Port     string
	Hashes   []string
	WrapKey  []byte // Password-derived WRAP key (32 bytes), nil = disabled
	ObfsMode string // "audio" or "video" - RTP masking mode
	// NoDTLS: skip DTLS and run RTP-obfs AEAD directly over the TURN relay.
	// Requires a server that can accept direct (DTLS-less) sessions on a
	// separate port/listener - see server.go -listen-direct.
	NoDTLS bool
	// RawMode: raw-IP without WireGuard (see server.go -listen-raw, handleConnRaw).
	// Implies NoDTLS - the server on -listen-raw does not speak DTLS.
	RawMode bool
	// TCPTransport: connect to the TURN relay over TCP instead of UDP (see
	// dialTURNConn in session.go). On some networks (seen on Rostelecom)
	// UDP to TURN is throttled/dropped by the ISP more aggressively than
	// TCP to the same relay - this flag works around exactly that.
	TCPTransport bool
}

// Credentials holds the TURN credentials
type Credentials struct {
	User          string
	Pass          string
	TurnURLs      []string
	CacheStreamID int
}
