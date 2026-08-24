package main

import "strings"

// workerErrorHint returns a short user-facing hint for a worker error.
func workerErrorHint(err error) string {
	if err == nil {
		return ""
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "wrap_auth_timeout"):
		return "the server did not answer WRAP/DTLS — check the password, the IP/port, and that wdtt-server is running"
	case strings.Contains(text, "context canceled"):
		return "the connection broke before the handshake — often the server is unreachable, the carrier throttles UDP, or the network changed"
	case strings.Contains(text, "context deadline exceeded"):
		return "handshake timeout — the server is not answering; check the VPS, the firewall and the WRAP password"
	case strings.Contains(text, "deadline exceeded") || strings.Contains(text, "timeout") || strings.Contains(text, "i/o timeout"):
		return "timeout — the server is not answering; check that the VPS is reachable, and check the password"
	case strings.Contains(text, "connection refused"):
		return "the server refused the connection — check the IP, the DTLS port, and that wdtt-server is running"
	case strings.Contains(text, "connection reset"):
		return "the server reset the connection — possibly a wrong WRAP password, or a server restart"
	case strings.Contains(text, "no route") || strings.Contains(text, "network is unreachable"):
		return "no route to the server — check your internet connection; turn off other VPNs/proxies"
	case strings.Contains(text, "lookup") || strings.Contains(text, "no such host"):
		return "DNS is not resolving the address — change the DNS under ⚙️ → Network"
	case strings.Contains(text, "turn quota") || strings.Contains(text, "quota") || strings.Contains(text, "486"):
		return "VK is out of TURN slots — lower the number of streams, or change the VK hash/account"
	case strings.Contains(text, "turn allocate"):
		return "TURN relay error — VK may be throttling UDP; try another hash or captcha mode"
	case strings.Contains(text, "rate limit") || strings.Contains(text, "flood") || strings.Contains(text, "error 29"):
		return "VK is rate-limiting requests — wait, or change the IP/hash"
	case strings.Contains(text, "rtp aead") || strings.Contains(text, "auth failed") || strings.Contains(text, "tag mismatch"):
		return "WRAP/RTP error — wrong password, or an incompatible server version"
	case strings.Contains(text, "fatal_auth") || strings.Contains(text, "wrong connection password"):
		return "wrong connection password, or it has expired"
	case strings.Contains(text, "cannot create socket"):
		return "could not open a UDP socket — a firmware restriction, or no network"
	default:
		return ""
	}
}
