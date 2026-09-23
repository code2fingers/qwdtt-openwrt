package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cbeuw/connutil"
	"github.com/pion/dtls/v3"
	"github.com/pion/dtls/v3/pkg/crypto/selfsign"
	"github.com/pion/logging"
	"github.com/pion/transport/v4/stdnet"
	"github.com/pion/turn/v5"
)

const (
	workerSendBuf      = 128
	sessionReadTimeout = 30 * time.Minute // Increased from 60s to 30min
	readBufSize        = 1600
	socketBufSize      = 625 * 1024
	keepaliveByte      = 0xFF // keepalive marker (DTLS-level or a direct obfs frame)
	// keepaliveInterval: 1s (as in the reference client) - keeps the TURN
	// permission/NAT mapping "warm" on each of the session's 18-108 relay
	// sockets more aggressively than the previous 15s/5s.
	keepaliveInterval = 10 * time.Second
	// keepaliveMinSize/keepaliveMaxSize: the keepalive packet no longer has a
	// fixed size (it used to be 1 byte every time) - a random length of
	// 25-44 bytes imitates the "silence" of OPUS in a real call, while a
	// constant size at even intervals is an easily recognisable pattern for DPI.
	keepaliveMinSize = 25
	keepaliveMaxSize = 20 // range added on top of keepaliveMinSize (rand.Intn(20))
)

// obfsDirectConn is a net.Conn over the TURN relay WITHOUT DTLS.
//
// RTP-obfs (ChaCha20-Poly1305/AES-GCM AEAD, obfs.go) already gives every
// packet full encryption and authentication. DTLS on top of it was pure
// overhead: a self-signed certificate with InsecureSkipVerify added no real
// protection, while doubling the AEAD work per packet and requiring a
// separate handshake (see handshakeSem) for each of the 9 worker sessions.
// Used only when tp.NoDTLS=true AND useWrap=true (otherwise there is no
// encryption here at all - and then DTLS is mandatory, see the branch below).
type obfsDirectConn struct {
	relay      net.PacketConn
	peer       net.Addr
	wrapKey    []byte
	cfg        *ObfsConfig
	writeState *ObfsState
}

func (c *obfsDirectConn) Read(b []byte) (int, error) {
	wire := make([]byte, len(b)+80) // RTP header(12) + AEAD tag + padding
	for {
		n, _, err := c.relay.ReadFrom(wire)
		if err != nil {
			return 0, err
		}
		if !obfsIsRTPPacket(wire[:n]) {
			continue
		}
		m, unwrapErr := obfsUnwrapPacket(c.wrapKey, wire[:n], b)
		if unwrapErr != nil {
			continue
		}
		return m, nil
	}
}

func (c *obfsDirectConn) Write(b []byte) (int, error) {
	wrapped, err := obfsWrapPacket(c.wrapKey, b, c.cfg, c.writeState)
	if err != nil {
		return 0, err
	}
	if _, err := c.relay.WriteTo(wrapped, c.peer); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (c *obfsDirectConn) Close() error                       { return nil }
func (c *obfsDirectConn) LocalAddr() net.Addr                { return c.relay.LocalAddr() }
func (c *obfsDirectConn) RemoteAddr() net.Addr               { return c.peer }
func (c *obfsDirectConn) SetDeadline(t time.Time) error      { return c.relay.SetDeadline(t) }
func (c *obfsDirectConn) SetReadDeadline(t time.Time) error  { return c.relay.SetReadDeadline(t) }
func (c *obfsDirectConn) SetWriteDeadline(t time.Time) error { return c.relay.SetWriteDeadline(t) }

// Handshake semaphore: limit to 3 concurrent DTLS handshakes
var handshakeSem = make(chan struct{}, 3)

// NullLoggerFactory suppresses pion's logs
type NullLoggerFactory struct{}

func (n *NullLoggerFactory) NewLogger(_ string) logging.LeveledLogger { return &NullLogger{} }

type NullLogger struct{}

func (n *NullLogger) Trace(_ string)                    {}
func (n *NullLogger) Tracef(_ string, _ ...interface{}) {}
func (n *NullLogger) Debug(_ string)                    {}
func (n *NullLogger) Debugf(_ string, _ ...interface{}) {}
func (n *NullLogger) Info(_ string)                     {}
func (n *NullLogger) Infof(_ string, _ ...interface{})  {}
func (n *NullLogger) Warn(_ string)                     {}
func (n *NullLogger) Warnf(_ string, _ ...interface{})  {}
func (n *NullLogger) Error(_ string)                    {}
func (n *NullLogger) Errorf(_ string, _ ...interface{}) {}

// connectedUDPConn wraps a connected UDP socket as a PacketConn
type connectedUDPConn struct{ *net.UDPConn }

func (c *connectedUDPConn) WriteTo(p []byte, _ net.Addr) (int, error) { return c.Write(p) }

// dialTURNConn opens a socket to the TURN server and wraps it in the
// net.PacketConn that turn.ClientConfig.Conn expects. UDP by default (as
// before). If tcp=true, it opens an ordinary TCP connection and wraps it
// with turn.NewSTUNConn - a regular, documented pion/turn feature (see
// examples/turn-client/tcp), not a hand-rolled protocol: NewSTUNConn parses
// the STUN/ChannelData framing over streaming TCP itself. Needed on networks
// (seen on Rostelecom) where UDP to the TURN relay is throttled/dropped
// while TCP to the same relay gets through - compare
// github.com/anton48/vk-turn-proxy-ios, which reaches the same VK/OK TURN
// infrastructure (calls.okcdn.ru) over TCP by default.
func dialTURNConn(turnAddr string, tcp bool) (net.PacketConn, io.Closer, error) {
	if !tcp {
		resolved, err := net.ResolveUDPAddr("udp", turnAddr)
		if err != nil {
			return nil, nil, fmt.Errorf("resolving TURN: %w", err)
		}
		c, err := net.DialUDP("udp", nil, resolved)
		if err != nil {
			return nil, nil, fmt.Errorf("connecting to TURN over UDP: %w", err)
		}
		_ = c.SetReadBuffer(socketBufSize)
		_ = c.SetWriteBuffer(socketBufSize)
		return &connectedUDPConn{c}, c, nil
	}

	d := net.Dialer{Timeout: 10 * time.Second}
	c, err := d.Dial("tcp", turnAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to TURN over TCP: %w", err)
	}
	return turn.NewSTUNConn(c), c, nil
}

func RunSession(
	ctx context.Context,
	tp *TurnParams,
	peer *net.UDPAddr,
	d *Dispatcher,
	localPort string,
	getConfig bool,
	configCh chan<- string,
	sessionID int,
	creds *Credentials,
	deviceID, password string,
	stats *Stats,
	allocateGate <-chan time.Time,
) (bool, error) {
	configDelivered := false
	var firstWrapUp uint32
	var firstWrapDown uint32
	var firstWireWrite uint32
	var firstWireRead uint32

	if len(creds.TurnURLs) == 0 {
		return false, fmt.Errorf("no TURN URL in the credentials")
	}
	selectedURL := creds.TurnURLs[sessionID%len(creds.TurnURLs)]

	urlhost, urlport, err := net.SplitHostPort(selectedURL)
	if err != nil {
		return false, fmt.Errorf("parsing TURN URL %q: %w", selectedURL, err)
	}
	if tp.Host != "" {
		urlhost = tp.Host
	}
	if tp.Port != "" {
		urlport = tp.Port
	}
	turnAddr := net.JoinHostPort(urlhost, urlport)

	turnConn, turnConnCloser, err := dialTURNConn(turnAddr, tp.TCPTransport)
	if err != nil {
		return false, err
	}
	defer turnConnCloser.Close()

	reportNetifdRelay(turnAddr, 1)
	defer reportNetifdRelay(turnAddr, -1)

	if tp.TCPTransport {
		log.Printf("[SESSION #%d] TURN TCP (%s)", sessionID, turnAddr)
	} else {
		log.Printf("[SESSION #%d] TURN UDP (%s)", sessionID, turnAddr)
	}

	// RequestedAddressFamily
	var addrFamily turn.RequestedAddressFamily
	if peer.IP.To4() != nil {
		addrFamily = turn.RequestedAddressFamilyIPv4
	} else {
		addrFamily = turn.RequestedAddressFamilyIPv6
	}

	// Pion's default stdnet.NewNet enumerates network interfaces through
	// NETLINK_ROUTE. Some Huawei/Honor ROMs deny that operation to apps.
	// The zero-value stdnet.Net provides everything this UDP client uses
	// without performing the unnecessary interface enumeration.
	tc, err := turn.NewClient(&turn.ClientConfig{
		STUNServerAddr:         turnAddr,
		TURNServerAddr:         turnAddr,
		Conn:                   turnConn,
		Net:                    new(stdnet.Net),
		Username:               creds.User,
		Password:               creds.Pass,
		RequestedAddressFamily: addrFamily,
		LoggerFactory:          &NullLoggerFactory{},
	})
	if err != nil {
		return false, fmt.Errorf("TURN client: %w", err)
	}
	defer tc.Close()

	if err = tc.Listen(); err != nil {
		return false, fmt.Errorf("TURN Listen: %w", err)
	}

	// A global rate limit on the moment of TURN Allocate itself (not just on
	// the start of the worker goroutine, see workerDelay in group.go) - no more
	// than one new TURN allocation per tick across the whole group, no matter
	// how many workers are ready to make one. Without this the start stagger
	// does not save us: on an unstable network (Allocate retries/delays) several
	// workers still overlap and together burn through the VK quota (error 486)
	// faster than they should. free-turn-proxy uses the same trick
	// (internal/proxy/udprelay/loop.go, one shared 200ms ticker).
	if allocateGate != nil {
		select {
		case <-allocateGate:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}

	relay, err := tc.Allocate()
	if err != nil {
		if isAuthError(err) {
			handleAuthError(creds.CacheStreamID)
		}
		errStr := err.Error()
		if strings.Contains(errStr, "Quota") || strings.Contains(errStr, "486") {
			return false, fmt.Errorf("TURN quota: %w", err)
		}
		return false, fmt.Errorf("TURN Allocate: %w", err)
	}
	defer relay.Close()

	// Reset error count on successful allocation
	getStreamCache(creds.CacheStreamID).errorCount.Store(0)

	log.Printf("[SESSION #%d] Relay: %s", sessionID, relay.LocalAddr())

	sessCtx, sessCancel := context.WithCancel(ctx)
	defer sessCancel()

	// Keepalive goroutine (TURN binding request)
	var sessionWg sync.WaitGroup
	sessionWg.Add(1)
	go func() {
		defer sessionWg.Done()
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-sessCtx.Done():
				return
			case <-t.C:
				tc.SendBindingRequest()
			}
		}
	}()

	useWrap := len(tp.WrapKey) == wrapKeyLen

	var activeConn net.Conn
	var relayWg sync.WaitGroup

	if useWrap && (tp.NoDTLS || tp.RawMode) {
		// ─── Direct mode: RTP-obfs AEAD straight over the TURN relay, no DTLS ───
		obfsCfg := NewObfsConfig(tp.ObfsMode)
		obfsWriteState := NewObfsState()
		activeConn = &obfsDirectConn{
			relay:      relay,
			peer:       peer,
			wrapKey:    tp.WrapKey,
			cfg:        obfsCfg,
			writeState: obfsWriteState,
		}
		log.Printf("[WORKER #%d] [DIRECT] No DTLS, RTP-obfs AEAD only ✓", sessionID)
	} else {
		// ─── Classic mode: DTLS over RTP-obfs (backwards compatibility) ───
		pipeA, pipeB := connutil.AsyncPacketPipe()
		defer pipeA.Close()
		defer pipeB.Close()

		var dtlsObfsCfg *ObfsConfig
		var obfsWriteState *ObfsState
		if useWrap {
			dtlsObfsCfg = NewObfsConfig(tp.ObfsMode)
			obfsWriteState = NewObfsState()
		}

		relayWg.Add(2)
		stopRelay := context.AfterFunc(sessCtx, func() {
			_ = relay.SetDeadline(time.Now())
			_ = pipeA.SetDeadline(time.Now())
		})
		defer stopRelay()

		// relay → pipeA (UNWRAP: strip RTP header + decrypt)
		go func() {
			defer relayWg.Done()
			defer sessCancel()
			// Max incoming: RTP header (12) + AEAD tag (16) + padding.
			readBufLen := readBufSize + 80
			buf := make([]byte, readBufLen)
			plain := make([]byte, readBufSize)
			for {
				n, _, readErr := relay.ReadFrom(buf)
				if readErr != nil {
					return
				}
				payload := buf[:n]
				if useWrap {
					if !obfsIsRTPPacket(payload) {
						log.Printf("[SESSION #%d] OBFS unwrap: unexpected packet (n=%d)", sessionID, n)
						continue
					}
					m, wrapErr := obfsUnwrapPacket(tp.WrapKey, payload, plain)
					if wrapErr != nil {
						log.Printf("[SESSION #%d] OBFS unwrap: %v (n=%d)", sessionID, wrapErr, n)
						continue
					}
					payload = plain[:m]
				}
				if atomic.CompareAndSwapUint32(&firstWrapUp, 0, 1) {
					log.Printf("[SESSION #%d] [DEBUG] Successfully decrypted/received the FIRST packet from the TURN relay (%d bytes)", sessionID, len(payload))
				}
				if _, writeErr := pipeA.WriteTo(payload, peer); writeErr != nil {
					return
				}
			}
		}()

		// pipeA → relay (WRAP: add RTP header + encrypt)
		go func() {
			defer relayWg.Done()
			defer sessCancel()
			b := make([]byte, readBufSize)
			for {
				n, _, readErr := pipeA.ReadFrom(b)
				if readErr != nil {
					return
				}
				out := b[:n]
				if useWrap {
					if dtlsObfsCfg != nil && obfsWriteState != nil {
						wrapped, wrapErr := obfsWrapPacket(tp.WrapKey, out, dtlsObfsCfg, obfsWriteState)
						if wrapErr != nil {
							log.Printf("[SESSION #%d] OBFS wrap: %v", sessionID, wrapErr)
							return
						}
						out = wrapped
					}
				}
				if atomic.CompareAndSwapUint32(&firstWrapDown, 0, 1) {
					log.Printf("[SESSION #%d] [DEBUG] Successfully encrypted/sent the FIRST packet to the TURN relay (%d bytes)", sessionID, len(out))
				}
				if _, writeErr := relay.WriteTo(out, peer); writeErr != nil {
					return
				}
			}
		}()

		// DTLS with Connection ID support (no SNI)
		cert, err := selfsign.GenerateSelfSigned()
		if err != nil {
			return false, fmt.Errorf("generating the certificate: %w", err)
		}

		// Acquire handshake semaphore
		select {
		case handshakeSem <- struct{}{}:
		case <-sessCtx.Done():
			return false, sessCtx.Err()
		}

		dtlsCfg := &dtls.Config{
			Certificates:          []tls.Certificate{cert},
			InsecureSkipVerify:    true,
			ExtendedMasterSecret:  dtls.RequireExtendedMasterSecret,
			CipherSuites:          []dtls.CipherSuiteID{dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256},
			ConnectionIDGenerator: dtls.OnlySendCIDGenerator(),
			MTU:                   1100,
			// No ServerName (SNI) - less detectable by DPI
		}

		dtlsConn, err := dtls.Client(pipeB, peer, dtlsCfg)
		if err != nil {
			<-handshakeSem
			return false, fmt.Errorf("DTLS client: %w", err)
		}

		hctx, hcancel := context.WithTimeout(sessCtx, 50*time.Second)
		log.Printf("[WORKER #%d] [DTLS] Handshake...", sessionID)
		err = dtlsConn.HandshakeContext(hctx)
		hcancel()
		<-handshakeSem // RELEASE SEMAPHORE IMMEDIATELY AFTER HANDSHAKE

		if err != nil {
			dtlsConn.Close()
			if useWrap {
				errStr := strings.ToLower(err.Error())
				if strings.Contains(errStr, "deadline") || strings.Contains(errStr, "timeout") {
					return false, fmt.Errorf("WRAP_AUTH_TIMEOUT: DTLS timeout, the password/WRAP was not confirmed")
				}
			}
			return false, fmt.Errorf("DTLS handshake: %w", err)
		}
		log.Printf("[WORKER #%d] [DTLS] Connection established ✓", sessionID)
		activeConn = dtlsConn
	}
	defer activeConn.Close()

	stats.ActiveConnections.Add(1)
	defer stats.ActiveConnections.Add(-1)

	// Config request
	if getConfig && configCh != nil && tp.RawMode {
		ip, dnsCSV, mtu, confErr := RequestRawConfig(activeConn, deviceID, password)
		if confErr != nil {
			errStr := confErr.Error()
			if strings.Contains(errStr, "FATAL_AUTH") {
				return false, confErr
			}
			log.Printf("[WORKER #%d] RAW config error: %v", sessionID, confErr)
		} else if ip != "" {
			conf := fmt.Sprintf("RAWCONF:%s|%s|%d", ip, dnsCSV, mtu)
			select {
			case configCh <- conf:
				configDelivered = true
				log.Printf("[WORKER #%d] RAW config received (ip=%s)", sessionID, ip)
			default:
				configDelivered = true
				log.Printf("[WORKER #%d] The RAW config was already delivered by another worker", sessionID)
			}
		} else {
			log.Printf("[WORKER #%d] The server has not assigned a raw IP yet, retrying later", sessionID)
		}
	} else if getConfig && configCh != nil {
		conf, confErr := RequestConfig(activeConn, localPort, deviceID, password)
		if confErr != nil {
			errStr := confErr.Error()
			if strings.Contains(errStr, "FATAL_AUTH") {
				return false, confErr
			}
			log.Printf("[WORKER #%d] Config error: %v", sessionID, confErr)
		} else if conf != "" {
			select {
			case configCh <- conf:
				configDelivered = true
				log.Printf("[WORKER #%d] Config received", sessionID)
			default:
				configDelivered = true
				log.Printf("[WORKER #%d] The config was already delivered by another worker", sessionID)
			}
		} else {
			log.Printf("[WORKER #%d] The server has not issued a WireGuard config yet, retrying later", sessionID)
		}
	} else {
		if authErr := SendAuth(activeConn, deviceID, password); authErr != nil {
			log.Printf("[WORKER #%d] Authorization error: %v", sessionID, authErr)
		}
	}

	log.Printf("[WORKER #%d] [READY] The tunnel is ready ✓", sessionID)

	// Register with the dispatcher
	slot := &WorkerSlot{
		ID:     sessionID,
		SendCh: make(chan []byte, workerSendBuf),
		PrioCh: make(chan []byte, prioBuf),
	}
	d.Register(slot)
	defer d.Unregister(slot)

	// Proxy activeConn ↔ Dispatcher
	var proxyWg sync.WaitGroup
	proxyWg.Add(3) // +1 for keepalive goroutine

	stopConn := context.AfterFunc(sessCtx, func() {
		_ = activeConn.SetDeadline(time.Now())
	})
	defer stopConn()

	if tp.RawMode {
		// The transport is UDP over TURN, so the server has no way of learning
		// that the connection broke other than a timeout (see handleConnRaw). Say
		// explicitly that the disconnect was deliberate, so the server frees the
		// slot in rawRouter at once instead of waiting for idleness - otherwise
		// "dead" connections from the previous session clutter the round-robin in
		// downlinkLoop when the same device reconnects quickly.
		go func() {
			select {
			case <-ctx.Done():
				_ = activeConn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
				_, _ = activeConn.Write([]byte("DISCONNECT_RAW:" + deviceID))
			case <-sessCtx.Done():
			}
		}()
	}

	// Keepalive: prevents TURN allocation timeout and idle disconnect.
	// The packet is not written straight to activeConn (that would be a second
	// goroutine competing for the conn with the main Writer below) - it goes
	// into slot.PrioCh without blocking, by the same path as small ACK packets,
	// and leaves through the single writer goroutine.
	go func() {
		defer proxyWg.Done()
		t := time.NewTicker(keepaliveInterval)
		defer t.Stop()

		didBytes := make([]byte, 16)
		copy(didBytes, deviceID)

		for {
			select {
			case <-sessCtx.Done():
				return
			case <-t.C:
				size := keepaliveMinSize + rand.Intn(keepaliveMaxSize)
				pkt := getPktBuf(size)
				pkt[0] = keepaliveByte
				copy(pkt[1:17], didBytes)
				for i := 17; i < size; i++ {
					pkt[i] = keepaliveByte
				}
				select {
				case slot.PrioCh <- pkt:
				default:
					putPktBuf(pkt)
				}
			}
		}
	}()

	// Writer: dispatcher → activeConn. PrioCh (small packets/ACKs) is always
	// checked first, ahead of SendCh with ordinary data - otherwise an ACK can
	// sit in the queue behind a large chunk of data.
	go func() {
		defer proxyWg.Done()
		defer sessCancel()
		defer func() {
			for {
				select {
				case p := <-slot.PrioCh:
					putPktBuf(p)
				default:
					goto drainSend
				}
			}
		drainSend:
			for {
				select {
				case p := <-slot.SendCh:
					putPktBuf(p)
				default:
					return
				}
			}
		}()
		for {
			var pkt []byte
			var ok bool
			select {
			case pkt, ok = <-slot.PrioCh:
			default:
				select {
				case <-sessCtx.Done():
					return
				case pkt, ok = <-slot.PrioCh:
				case pkt, ok = <-slot.SendCh:
				}
			}
			if !ok {
				return
			}
			// 3s, not sessionReadTimeout (30 min). The write goes into a
			// UDP socket to the TURN relay rather than reading a long-lived
			// connection; a 30-minute deadline meant a stuck write (a full OS
			// socket buffer, a bad network) would hold the Writer for half an hour
			// while this worker's SendCh/PrioCh queue piles up and/or is dropped
			// by the dispatcher above (see readLoop in dispatcher.go).
			_ = activeConn.SetWriteDeadline(time.Now().Add(3 * time.Second))
			if atomic.CompareAndSwapUint32(&firstWireWrite, 0, 1) {
				log.Printf("[WORKER #%d] [DEBUG] Sent the FIRST packet into the connection (%d bytes)", sessionID, len(pkt))
			}
			_, writeErr := activeConn.Write(pkt)
			putPktBuf(pkt)
			if writeErr != nil {
				log.Printf("[WORKER #%d] Writer error: %v", sessionID, writeErr)
				return
			}
		}
	}()

	// Reader: activeConn → dispatcher
	go func() {
		defer proxyWg.Done()
		defer sessCancel()
		b := make([]byte, 2000)
		for {
			_ = activeConn.SetReadDeadline(time.Now().Add(sessionReadTimeout))
			n, readErr := activeConn.Read(b)
			if readErr != nil {
				if sessCtx.Err() != nil {
					return
				}
				if ne, ok := readErr.(net.Error); ok && ne.Timeout() {
					continue
				}
				log.Printf("[WORKER #%d] Reader error: %v", sessionID, readErr)
				return
			}

			// Skip keepalive pong from server
			if n == 1 && b[0] == keepaliveByte {
				continue
			}

			if atomic.CompareAndSwapUint32(&firstWireRead, 0, 1) {
				log.Printf("[WORKER #%d] [DEBUG] Received the FIRST packet from the connection (%d bytes)", sessionID, n)
			}

			pkt := getPktBuf(n)
			copy(pkt, b[:n])
			select {
			case d.ReturnCh <- pkt:
			case <-sessCtx.Done():
				putPktBuf(pkt)
				return
			default:
				// ReturnCh is full - the packet is dropped, but this goroutine
				// does not block. There used to be no default here: if writeLoop
				// could not drain ReturnCh fast enough (say TUN.Write is slow on
				// a particular device), the Reader would wedge on this select
				// and stop reading activeConn.Read() altogether - that is, new
				// packets from the server (including real answers to user
				// traffic) stopped being drained from the OS UDP socket and were
				// lost there, not here. Once the channel filled up it killed all
				// further receive for that worker - the select has to be
				// non-blocking.
				putPktBuf(pkt)
			}
		}
	}()

	proxyWg.Wait()
	sessCancel()
	relayWg.Wait()
	sessionWg.Wait()
	log.Printf("[SESSION #%d] Finished", sessionID)
	return configDelivered, nil
}

func RunPing(
	ctx context.Context,
	tp *TurnParams,
	peer *net.UDPAddr,
	creds *Credentials,
) (int64, error) {
	startPing := time.Now()

	if len(creds.TurnURLs) == 0 {
		return 0, fmt.Errorf("no TURN URL")
	}
	selectedURL := creds.TurnURLs[0]

	urlhost, urlport, err := net.SplitHostPort(selectedURL)
	if err != nil {
		return 0, err
	}
	if tp.Host != "" {
		urlhost = tp.Host
	}
	if tp.Port != "" {
		urlport = tp.Port
	}
	turnAddr := net.JoinHostPort(urlhost, urlport)

	turnConn, turnConnCloser, err := dialTURNConn(turnAddr, tp.TCPTransport)
	if err != nil {
		return 0, err
	}
	defer turnConnCloser.Close()

	var addrFamily turn.RequestedAddressFamily
	if peer.IP.To4() != nil {
		addrFamily = turn.RequestedAddressFamilyIPv4
	} else {
		addrFamily = turn.RequestedAddressFamilyIPv6
	}

	tc, err := turn.NewClient(&turn.ClientConfig{
		STUNServerAddr:         turnAddr,
		TURNServerAddr:         turnAddr,
		Conn:                   turnConn,
		Net:                    new(stdnet.Net),
		Username:               creds.User,
		Password:               creds.Pass,
		RequestedAddressFamily: addrFamily,
		LoggerFactory:          &NullLoggerFactory{},
	})
	if err != nil {
		return 0, err
	}
	defer tc.Close()

	if err = tc.Listen(); err != nil {
		return 0, err
	}

	relay, err := tc.Allocate()
	if err != nil {
		return 0, err
	}
	defer relay.Close()

	pipeA, pipeB := connutil.AsyncPacketPipe()
	defer pipeA.Close()
	defer pipeB.Close()

	sessCtx, sessCancel := context.WithCancel(ctx)
	defer sessCancel()

	var relayWg sync.WaitGroup
	relayWg.Add(2)

	useWrap := len(tp.WrapKey) == wrapKeyLen
	var obfsCfg *ObfsConfig
	var obfsWriteState *ObfsState
	if useWrap {
		obfsCfg = NewObfsConfig(tp.ObfsMode)
		obfsWriteState = NewObfsState()
	}

	// relay → pipeA
	go func() {
		defer relayWg.Done()
		defer sessCancel()
		buf := make([]byte, readBufSize+80)
		plain := make([]byte, readBufSize)
		for {
			n, _, err := relay.ReadFrom(buf)
			if err != nil {
				return
			}
			payload := buf[:n]
			if useWrap {
				if !obfsIsRTPPacket(payload) {
					continue
				}
				m, err := obfsUnwrapPacket(tp.WrapKey, payload, plain)
				if err != nil {
					continue
				}
				payload = plain[:m]
			}
			_, _ = pipeA.WriteTo(payload, peer)
		}
	}()

	// pipeA → relay
	go func() {
		defer relayWg.Done()
		defer sessCancel()
		b := make([]byte, readBufSize)
		for {
			n, _, err := pipeA.ReadFrom(b)
			if err != nil {
				return
			}
			out := b[:n]
			if useWrap {
				wrapped, err := obfsWrapPacket(tp.WrapKey, out, obfsCfg, obfsWriteState)
				if err != nil {
					return
				}
				out = wrapped
			}
			_, _ = relay.WriteTo(out, peer)
		}
	}()

	cert, err := selfsign.GenerateSelfSigned()
	if err != nil {
		return 0, err
	}

	dtlsCfg := &dtls.Config{
		Certificates:          []tls.Certificate{cert},
		InsecureSkipVerify:    true,
		ExtendedMasterSecret:  dtls.RequireExtendedMasterSecret,
		CipherSuites:          []dtls.CipherSuiteID{dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256},
		ConnectionIDGenerator: dtls.OnlySendCIDGenerator(),
		MTU:                   1100,
	}

	dtlsConn, err := dtls.Client(pipeB, peer, dtlsCfg)
	if err != nil {
		return 0, err
	}
	defer dtlsConn.Close()

	hctx, hcancel := context.WithTimeout(sessCtx, 15*time.Second)
	defer hcancel()

	err = dtlsConn.HandshakeContext(hctx)
	if err != nil {
		return 0, err
	}

	rtt := time.Since(startPing).Milliseconds()
	// Handshake completes -> we have a successful round trip!
	return rtt, nil
}
