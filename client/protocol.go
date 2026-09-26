package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// The server's refusals, which both config requests answer the same way.
// FATAL_AUTH is a prefix the rest of the client keys on: reconnecting clears
// none of these, so a worker meeting one stops and the interface is told why
// rather than being left to retry.
func deniedError(resp string) error {
	reason := strings.TrimPrefix(resp, "DENIED:")
	switch reason {
	case "wrong_password":
		return fmt.Errorf("FATAL_AUTH: wrong connection password")
	case "expired":
		return fmt.Errorf("FATAL_AUTH: the password has expired")
	case "device_mismatch":
		return fmt.Errorf("FATAL_AUTH: the password is bound to another device")
	default:
		return fmt.Errorf("FATAL_AUTH: access denied (%s)", reason)
	}
}

// RequestConfig asks for the WireGuard config over the DTLS connection.
func RequestConfig(conn net.Conn, localPort, deviceID, password string) (string, error) {
	payload := fmt.Sprintf("GETCONF:%s|%s|%s", localPort, deviceID, password)
	if _, err := conn.Write([]byte(payload)); err != nil {
		return "", fmt.Errorf("sending GETCONF: %w", err)
	}

	b := make([]byte, 4096)
	if err := conn.SetReadDeadline(time.Now().Add(45 * time.Second)); err != nil {
		return "", fmt.Errorf("setting the deadline: %w", err)
	}
	n, err := conn.Read(b)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		return "", fmt.Errorf("reading the config response: %w", err)
	}

	resp := string(b[:n])
	if resp == "NOCONF" {
		return "", nil
	}

	if strings.HasPrefix(resp, "DENIED:") {
		return "", deniedError(resp)
	}

	return resp, nil
}

// SendAuth sends the authorization command so the server can tie the connection to a device
func SendAuth(conn net.Conn, deviceID, password string) error {
	payload := fmt.Sprintf("AUTH:%s|%s", deviceID, password)
	if _, err := conn.Write([]byte(payload)); err != nil {
		return fmt.Errorf("sending AUTH: %w", err)
	}

	return nil
}

// RequestRawConfig asks the server for the raw-IP mode configuration
// (no WireGuard) - the server answers "RAWCONF:ip|dns|mtu" (see server.go
// handleConnRaw). ip is empty on the first call, if the server has not assigned one yet.
func RequestRawConfig(conn net.Conn, deviceID, password string) (ip, dnsCSV string, mtu int, err error) {
	payload := fmt.Sprintf("GETCONF_RAW:%s|%s", deviceID, password)
	if _, err = conn.Write([]byte(payload)); err != nil {
		return "", "", 0, fmt.Errorf("sending GETCONF_RAW: %w", err)
	}

	b := make([]byte, 4096)
	if err = conn.SetReadDeadline(time.Now().Add(45 * time.Second)); err != nil {
		return "", "", 0, fmt.Errorf("setting the deadline: %w", err)
	}
	n, readErr := conn.Read(b)
	_ = conn.SetReadDeadline(time.Time{})
	if readErr != nil {
		return "", "", 0, fmt.Errorf("reading the RAWCONF response: %w", readErr)
	}

	resp := string(b[:n])
	if resp == "NOCONF" {
		return "", "", 0, nil
	}
	if strings.HasPrefix(resp, "DENIED:") {
		return "", "", 0, deniedError(resp)
	}
	if !strings.HasPrefix(resp, "RAWCONF:") {
		return "", "", 0, fmt.Errorf("unexpected RAWCONF response: %q", resp)
	}

	parts := strings.Split(strings.TrimPrefix(resp, "RAWCONF:"), "|")
	if len(parts) != 3 {
		return "", "", 0, fmt.Errorf("malformed RAWCONF: %q", resp)
	}
	mtuVal, convErr := strconv.Atoi(strings.TrimSpace(parts[2]))
	if convErr != nil {
		return "", "", 0, fmt.Errorf("malformed MTU in RAWCONF: %q", parts[2])
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), mtuVal, nil
}
