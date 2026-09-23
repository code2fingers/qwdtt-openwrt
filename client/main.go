package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// CaptchaResultChan carries the captcha token from an external solver (WebView)
var CaptchaResultChan = make(chan string, 1)

var captchaModeValue atomic.Value

func init() {
	captchaModeValue.Store("auto")
}

func normalizeCaptchaMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "auto", "rjs", "wv":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "auto"
	}
}

func setCaptchaMode(mode string) string {
	normalized := normalizeCaptchaMode(mode)
	captchaModeValue.Store(normalized)
	return normalized
}

func getCaptchaMode() string {
	mode, _ := captchaModeValue.Load().(string)
	if mode == "" {
		return "auto"
	}
	return mode
}

// drainCaptchaResult removes a stale captcha result from the channel
func drainCaptchaResult() {
	select {
	case <-CaptchaResultChan:
	default:
	}
}

func runHashChecks(ctx context.Context, hashes []string) {
	log.Printf("[CHECK] Checking VK hashes: %d", len(hashes))
	for i, hash := range hashes {
		fmt.Printf("HASH_CHECK_START|%d|%s\n", i+1, hash)
		checkCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		_, _, turnURLs, err := GetCreds(checkCtx, hash, 9000+i)
		cancel()

		status, message := classifyHashCheckError(err)
		if err == nil {
			status = "ok"
			message = fmt.Sprintf("TURN urls=%d", len(turnURLs))
		}
		fmt.Printf("HASH_CHECK|%d|%s|%s|%s\n", i+1, hash, status, sanitizeHashCheckMessage(message))
	}
}

func classifyHashCheckError(err error) (string, string) {
	if err == nil {
		return "ok", ""
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "captcha_required") || strings.Contains(text, "captcha_wait_required"):
		return "captcha", "VK is asking for a captcha"
	case strings.Contains(text, "call not found") ||
		strings.Contains(text, "joinconversationbylink") ||
		strings.Contains(text, "missing turn_server") ||
		strings.Contains(text, "9000") ||
		strings.Contains(text, "callunavailable"):
		return "dead", "the call was not found, or is closed"
	case strings.Contains(text, "flood") || strings.Contains(text, "rate limit") || strings.Contains(text, "error_code:29"):
		return "limited", "VK is rate-limiting requests"
	case strings.Contains(text, "timeout") || strings.Contains(text, "deadline") || strings.Contains(text, "lookup") || strings.Contains(text, "network"):
		return "network", "network error"
	default:
		return "error", err.Error()
	}
}

func sanitizeHashCheckMessage(message string) string {
	message = strings.ReplaceAll(message, "\n", " ")
	message = strings.ReplaceAll(message, "\r", " ")
	message = strings.ReplaceAll(message, "|", "/")
	if len(message) > 180 {
		return message[:180]
	}
	return message
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	configPath := configPathFromArgs(os.Args[1:])
	fileConfig, err := loadClientFileConfig(configPath)
	if err != nil {
		log.Fatalf("[CLIENT] Config error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signals
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case s := <-sig:
			log.Printf("[CLIENT] Signal %v, shutting down...", s)
			cancel()
		case <-ctx.Done():
			return
		}
		select {
		case s := <-sig:
			log.Printf("[CLIENT] %v again, forcing an exit", s)
			os.Exit(1)
		case <-ctx.Done():
		}
	}()

	var pauseFlag int32

	// STDIN for PAUSE/RESUME/STOP and CAPTCHA_RESULT
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.Contains(line, "error:tunnel stopped") {
				log.Printf("[STDIN] %s", line)
			}
			switch {
			case line == "PAUSE":
				atomic.StoreInt32(&pauseFlag, 1)
			case line == "RESUME":
				atomic.StoreInt32(&pauseFlag, 0)
			case line == "STOP":
				cancel()
				return
			case strings.HasPrefix(line, "CAPTCHA_RESULT|"):
				result := strings.TrimPrefix(line, "CAPTCHA_RESULT|")
				drainCaptchaResult()
				CaptchaResultChan <- result
				log.Printf("[CAPTCHA] The result from Kotlin was written to the channel")
			case strings.HasPrefix(line, "TURN_CREDS|"):
				handleTurnCredsStdinLine(line)
			}
		}
	}()

	flag.String("config", configPath, "client JSON config")
	host := flag.String("turn", "", "override the TURN IP")
	port := flag.String("port", "", "override the TURN port")
	listen := flag.String("listen", "127.0.0.1:9000", "local address")
	vkHash := flag.String("vk", fileConfig.hashesCSV(), "VK call hashes (comma-separated)")
	peerAddr := flag.String("peer", fileConfig.Peer, "address:port of the VPS server")
	workersDefault := fileConfig.Workers
	if workersDefault == 0 {
		workersDefault = 9
	}
	numW := flag.Int("n", workersDefault, "number of workers")
	pingOnly := flag.Bool("ping-only", false, "only measure latency, then exit")

	deviceIDDefault := fileConfig.DeviceID
	if deviceIDDefault == "" {
		deviceIDDefault = "unknown"
		if configPath != "" {
			deviceIDDefault = "openwrt"
		}
	}
	deviceID := flag.String("device-id", deviceIDDefault, "unique device ID")
	connPassword := flag.String("password", fileConfig.Password, "connection password")
	captchaModeDefault := fileConfig.CaptchaMode
	if captchaModeDefault == "" {
		captchaModeDefault = "auto"
	}
	captchaMode := flag.String("captcha-mode", captchaModeDefault, "captcha bypass mode (auto/wv/rjs)")
	vkAuthDefault := fileConfig.VKAuth
	if vkAuthDefault == "" {
		vkAuthDefault = "anonymous"
	}
	vkAuthMode := flag.String("vk-auth", vkAuthDefault, "VK authorization mode (account/anonymous)")
	vkAnonPathDefault := fileConfig.VKAnonPath
	if vkAnonPathDefault == "" {
		vkAnonPathDefault = "vkcalls"
	}
	vkAnonPath := flag.String("vk-anon-path", vkAnonPathDefault, "anonymous VK TURN path (vkcalls/legacy)")
	vkCredsFile := flag.String("vk-creds-file", "", "file with TURN credentials from a VK account")
	dnsDefault := fileConfig.DNS
	if dnsDefault == "" {
		dnsDefault = "yandex"
	}
	goDNS := flag.String("go-dns", dnsDefault, "DNS for VK (yandex/cloudflare/google, doh-yandex/doh-cloudflare/doh-google, custom:IP or doh:URL)")
	obfsDefault := fileConfig.Obfs
	if obfsDefault == "" {
		obfsDefault = "audio"
	}
	obfsMode := flag.String("obfs", obfsDefault, "obfuscation mode (audio/video)")
	checkHashes := flag.Bool("check-hashes", false, "check the VK hashes, then exit")
	modeDefault := "vpn"
	if configPath != "" {
		modeDefault = "rawtun"
	}
	connMode := flag.String("mode", modeDefault, "client mode (vpn|socks|rawtun)")
	socksAddr := flag.String("socks", "127.0.0.1:1080", "local SOCKS5 (only with -mode socks)")
	socksAuth := flag.Bool("socks-auth", false, "require a SOCKS5 username and password")
	socksUser := flag.String("socks-user", "", "SOCKS5 username")
	socksPass := flag.String("socks-pass", "", "SOCKS5 password")
	noDTLS := flag.Bool("notls", fileConfig.NoDTLS, "direct mode: RTP-obfs AEAD without DTLS over TURN (needs a server with -listen-direct)")
	turnTCP := flag.Bool("turn-tcp", fileConfig.TurnTCP, "connect to the TURN relay over TCP instead of UDP (works around UDP throttling on some networks, e.g. Rostelecom)")
	tunFdSock := flag.String("tun-fd-sock", "", "unix socket for receiving the TUN fd from Android (only with -mode rawtun)")
	tunName := flag.String("tun-name", fileConfig.TunName, "Linux/OpenWrt TUN interface name")
	netifd := flag.Bool("netifd", false, "hand the RAW config to netifd through "+netifdUpScript+" instead of configuring the TUN directly")
	rawTunSelfTest := flag.String("rawtun-self-test", "", "create a temporary OpenWrt RAW TUN with this IPv4 address")
	rawTunSelfTestDuration := flag.Duration("rawtun-self-test-duration", 5*time.Second, "temporary RAW TUN self-test duration")

	flag.Parse()
	// netifd already prefixes each line of a protocol task with the interface
	// it belongs to, and syslog already dates it. Repeating either is what put
	// two clocks on one line, the second of them in UTC while syslog's is
	// local.
	if *netifd {
		log.SetFlags(0)
		netifdManaged = true
	}
	if *rawTunSelfTest != "" {
		tun, testErr := createNativeRawTUN(*tunName)
		if testErr != nil {
			log.Fatalf("[RAW SELF-TEST] %v", testErr)
		}
		// The device persists, so a failure here has to take it away as well:
		// the point of the self-test is that it leaves the router unchanged.
		if testErr = tun.configure(*rawTunSelfTest, 1300); testErr != nil {
			tun.destroy()
			log.Fatalf("[RAW SELF-TEST] %v", testErr)
		}
		log.Printf("[RAW SELF-TEST] TUN %s is up on %s", tun.name, *rawTunSelfTest)
		select {
		case <-ctx.Done():
		case <-time.After(*rawTunSelfTestDuration):
		}
		tun.destroy()
		log.Printf("[RAW SELF-TEST] success")
		return
	}
	activeConnMode := strings.ToLower(strings.TrimSpace(*connMode))
	if activeConnMode != "socks" && activeConnMode != "rawtun" {
		activeConnMode = "vpn"
	}
	if activeConnMode == "socks" && *socksAuth {
		if *socksUser == "" || *socksPass == "" {
			log.Fatal("[SOCKS] Authorization needs a username and a password")
		}
		if len([]byte(*socksUser)) > 255 || len([]byte(*socksPass)) > 255 {
			log.Fatal("[SOCKS] The username and password must be at most 255 bytes")
		}
	}
	setupGlobalResolver(*goDNS)
	activeCaptchaMode := setCaptchaMode(*captchaMode)
	activeVkAuthMode := setVkAuthMode(*vkAuthMode)
	activeVkAnonPath := setVkAnonPath(*vkAnonPath)

	if err := loadVkCredsFile(*vkCredsFile); err != nil {
		log.Fatalf("[CLIENT] Error reading vk-creds-file: %v", err)
	}

	hashes := ParseHashes(*vkHash)
	if *checkHashes {
		if len(hashes) == 0 {
			log.Fatal("[CHECK] -vk with a list of hashes is required")
		}
		log.Printf("[CLIENT] VK auth mode: %s (hash-check)", activeVkAuthMode)
		if activeVkAuthMode == "anonymous" {
			log.Printf("[CLIENT] VK anon path: %s", activeVkAnonPath)
		}
		log.Printf("[CLIENT] Captcha mode: %s", activeCaptchaMode)
		runHashChecks(ctx, hashes)
		return
	}

	log.Printf("[CLIENT] VK auth mode: %s", activeVkAuthMode)
	if activeVkAuthMode == "anonymous" {
		log.Printf("[CLIENT] VK anon path: %s", activeVkAnonPath)
	}

	if *peerAddr == "" || *vkHash == "" {
		log.Fatal("[CLIENT] -peer and -vk are required")
	}

	peer, err := net.ResolveUDPAddr("udp", *peerAddr)
	if err != nil {
		log.Fatalf("[CLIENT] Error parsing the peer: %v", err)
	}

	if len(hashes) == 0 {
		log.Fatal("[CLIENT] No VK hashes")
	}

	if *connPassword == "" {
		log.Fatal("[CLIENT] -password is required: the WRAP key is now derived from the connection password")
	}

	// WRAP key
	wrapKey, err := deriveWrapKey(*connPassword)
	if err != nil {
		log.Fatalf("[CLIENT] WRAP key derive: %v", err)
	}

	// Worker limit
	maxWorkers := 108
	if *numW > maxWorkers {
		*numW = maxWorkers
	}
	if getVkAuthMode() == "account" {
		const accountMaxWorkers = 4
		if *numW > accountMaxWorkers {
			log.Printf("[CLIENT] VK account: TURN quota ~%d relays per session, streams %d -> %d", accountMaxWorkers, *numW, accountMaxWorkers)
			*numW = accountMaxWorkers
		}
		if *numW < 1 {
			*numW = 1
		}
	} else {
		if *numW < workersPerGroup {
			*numW = workersPerGroup
		}
		*numW = (*numW / workersPerGroup) * workersPerGroup
	}

	netifdWorkerSlots = *numW

	tp := &TurnParams{
		Host:         *host,
		Port:         *port,
		Hashes:       hashes,
		WrapKey:      wrapKey,
		ObfsMode:     normalizeObfsMode(*obfsMode),
		NoDTLS:       *noDTLS,
		RawMode:      activeConnMode == "rawtun",
		TCPTransport: *turnTCP,
	}

	if *pingOnly {
		var lastErr error
		for i, hash := range hashes {
			user, pass, turnURLs, err := GetCreds(ctx, hash, 999)
			if err != nil {
				lastErr = fmt.Errorf("GetCreds hash %d: %v", i, err)
				continue
			}
			creds := &Credentials{User: user, Pass: pass, TurnURLs: turnURLs, CacheStreamID: 999}
			rtt, err := RunPing(ctx, tp, peer, creds)
			if err != nil {
				lastErr = fmt.Errorf("RunPing hash %d: %v", i, err)
				continue
			}
			fmt.Printf("PING_RESULT|%d\n", rtt)
			os.Exit(0)
		}
		// If every hash failed
		fmt.Printf("PING_ERROR|All hashes failed. Last error: %v\n", lastErr)
		os.Exit(1)
	}

	// Listen locally (SO_REUSEADDR - a quick restart without "address already in use")
	localConn, err := listenUDP(*listen)
	if err != nil {
		log.Fatalf("[CLIENT] Listener error %s: %v", *listen, err)
	}
	if uc, ok := localConn.(*net.UDPConn); ok {
		_ = uc.SetReadBuffer(socketBufSize)
		_ = uc.SetWriteBuffer(socketBufSize)
	}
	stopLocalConn := context.AfterFunc(ctx, func() { _ = localConn.Close() })
	defer stopLocalConn()

	_, localPort, _ := net.SplitHostPort(*listen)
	if localPort == "" {
		localPort = "9000"
	}

	numGroups := (*numW + workersPerGroup - 1) / workersPerGroup

	wrapStatus := "OFF"
	if len(wrapKey) == wrapKeyLen {
		wrapStatus = "ON (password HKDF + RTP AEAD)"
	}

	captchaStatus := "AUTO: Go v2 x2 -> WBV Auto x2 -> Go v2 x1 -> Manual WBV"
	switch activeCaptchaMode {
	case "wv":
		captchaStatus = "WBV selected in Android"
	case "rjs":
		captchaStatus = "RJS Go v2 with WBV Auto fallback"
	}

	log.Println("[CLIENT] ═══════════════════════════════════════")
	log.Printf("[CLIENT] VK Creds: 2 stable app_id with a rotating fallback")
	log.Printf("[CLIENT] TLS: Chrome 146 fingerprint")
	log.Printf("[CLIENT] Workers: %d (groups: %d, %d each)", *numW, numGroups, workersPerGroup)
	log.Printf("[CLIENT] Hashes: %d", len(hashes))
	log.Printf("[CLIENT] Listening: %s | Peer: %s", *listen, *peerAddr)
	if *turnTCP {
		log.Printf("[CLIENT] TURN transport: TCP")
	} else {
		log.Printf("[CLIENT] TURN transport: UDP")
	}
	log.Printf("[CLIENT] Mode: %s", activeConnMode)
	if activeConnMode == "socks" {
		log.Printf("[CLIENT] SOCKS5: %s", *socksAddr)
		if *socksAuth {
			log.Printf("[CLIENT] SOCKS5: username/password authorization enabled")
		}
	}
	log.Printf("[CLIENT] WRAP: %s", wrapStatus)
	log.Printf("[WRAP] The key is derived from the password, RTP AEAD mode is active")
	log.Printf("[CLIENT] Device ID: %s", *deviceID)
	log.Printf("[CLIENT] Captcha: %s", captchaStatus)
	log.Println("[CLIENT] ═══════════════════════════════════════")

	stats := NewStats()

	var disp *Dispatcher
	if activeConnMode == "rawtun" {
		disp = NewDispatcherPendingTUN(ctx, stats)
	} else {
		disp = NewDispatcher(ctx, localConn, stats)
	}
	defer disp.Shutdown()

	configCh := make(chan string, 1)
	configDone := make(chan struct{})
	go func() {
		defer close(configDone)
		select {
		case rawConf, ok := <-configCh:
			if !ok || rawConf == "" {
				return
			}

			if strings.HasPrefix(rawConf, "RAWCONF:") {
				parts := strings.Split(strings.TrimPrefix(rawConf, "RAWCONF:"), "|")
				if len(parts) != 3 {
					log.Printf("[RAW] Malformed RAWCONF: %q", rawConf)
					return
				}
				ip, dnsCSV, mtuStr := parts[0], parts[1], parts[2]
				fmt.Println()
				fmt.Println("╔══════════════ RAW config ══════════════╗")
				fmt.Printf("║ %-40s ║\n", fmt.Sprintf("IP = %s", ip))
				fmt.Printf("║ %-40s ║\n", fmt.Sprintf("DNS = %s", dnsCSV))
				fmt.Printf("║ %-40s ║\n", fmt.Sprintf("MTU = %s", mtuStr))
				fmt.Println("╚══════════════════════════════════════╝")

				var tunFile *os.File
				if *tunFdSock != "" {
					log.Println("[RAW] Waiting for the TUN fd from Android...")
					var fdErr error
					attempt := 0
					for {
						attempt++
						tunFile, fdErr = recvTunFD(*tunFdSock)
						if fdErr == nil {
							break
						}
						rawDiagf("recvTunFD attempt #%d failed: %v (retry in 200ms)", attempt, fdErr)
						select {
						case <-ctx.Done():
							return
						case <-time.After(200 * time.Millisecond):
						}
					}
					rawDiagf("recvTunFD succeeded on attempt #%d, fd=%v", attempt, tunFile.Fd())
				} else {
					mtu, mtuErr := strconv.Atoi(strings.TrimSpace(mtuStr))
					if mtuErr != nil {
						log.Printf("[RAW] Malformed MTU: %q", mtuStr)
						cancel()
						return
					}
					nativeTun, nativeErr := createNativeRawTUN(*tunName)
					if nativeErr == nil {
						if *netifd {
							nativeErr = notifyNetifd(nativeTun.name, ip, dnsCSV, mtu)
						} else {
							nativeErr = nativeTun.configure(ip, mtu)
						}
						if nativeErr != nil {
							nativeTun.cleanup()
						}
					}
					if nativeErr != nil {
						log.Printf("[RAW] Linux/OpenWrt TUN error: %v", nativeErr)
						cancel()
						return
					}
					context.AfterFunc(ctx, nativeTun.cleanup)
					tunFile = nativeTun.file
					log.Printf("[RAW] OpenWrt TUN %s is up on %s", nativeTun.name, ip)
				}
				disp.AttachTUN(tunFile)
				log.Println("[RAW] TUN attached, traffic is flowing")
				return
			}

			finalConf := rawConf
			if !strings.Contains(finalConf, "MTU =") {
				lines := strings.Split(finalConf, "\n")
				var newLines []string
				for _, line := range lines {
					newLines = append(newLines, line)
					if strings.TrimSpace(line) == "[Interface]" {
						newLines = append(newLines, "MTU = 1280")
					}
				}
				finalConf = strings.Join(newLines, "\n")
			}
			fmt.Println()
			fmt.Println("╔══════════════ WireGuard config ══════════════╗")
			for _, line := range strings.Split(finalConf, "\n") {
				fmt.Printf("║ %-44s ║\n", line)
			}
			fmt.Println("╚══════════════════════════════════════════════╝")
			if err := os.WriteFile("wg-turn.conf", []byte(finalConf+"\n"), 0600); err != nil {
				log.Printf("[CONFIG] Error saving: %v", err)
			} else {
				log.Println("[CONFIG] Saved to wg-turn.conf")
			}

			if activeConnMode == "socks" {
				dev, tnet, err := startUserspaceWireGuard(finalConf)
				if err != nil {
					log.Printf("[SOCKS] Userspace WG error: %v", err)
					return
				}
				defer dev.Close()
				if err := runSocks5Server(ctx, *socksAddr, tnet, *socksAuth, *socksUser, *socksPass); err != nil {
					log.Printf("[SOCKS] Server stopped: %v", err)
				}
			}
		case <-ctx.Done():
		}
	}()

	var wg sync.WaitGroup
	workerIDCounter := 1

	var prevWaitReady <-chan struct{}

	for g := 0; g < numGroups; g++ {
		isFirst := (g == 0)

		var myWaitReady <-chan struct{}
		var mySignalReady chan<- struct{}

		if g > 0 {
			myWaitReady = prevWaitReady
		}
		if g < numGroups-1 {
			ch := make(chan struct{})
			mySignalReady = ch
			prevWaitReady = ch
		}

		startIdx := g * workersPerGroup
		endIdx := startIdx + workersPerGroup
		if endIdx > *numW {
			endIdx = *numW
		}
		groupSize := endIdx - startIdx
		if groupSize <= 0 {
			continue
		}

		ids := make([]int, groupSize)
		for i := range ids {
			ids[i] = workerIDCounter
			workerIDCounter++
		}

		gID := g + 1
		var cc chan<- string
		if isFirst {
			cc = configCh
		}

		wg.Add(1)
		go func(groupID int, isFirstGroup bool, configChan chan<- string, workerIds []int, startHashIndex int, waitR <-chan struct{}, sigR chan<- struct{}) {
			defer wg.Done()
			WorkerGroup(ctx, groupID, startHashIndex, tp, peer, disp, localPort,
				isFirstGroup, configChan, workerIds, &pauseFlag, *deviceID, *connPassword, stats, waitR, sigR)
		}(gID, isFirst, cc, ids, g, myWaitReady, mySignalReady)
	}

	wg.Wait()
	cancel()
	close(configCh)
	<-configDone
	log.Println("[CLIENT] All workers have finished")
}
