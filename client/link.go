package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode"
)

// What an imported server link sets on an interface. Read by the LuCI import
// dialog and by setup.sh, which is why it is printed rather than applied here.
type linkSettings struct {
	PeerHost   string   `json:"peer_host"`
	PeerPort   string   `json:"peer_port"` // empty: the mode's own default
	ListenPort string   `json:"listen_port,omitempty"`
	Password   string   `json:"password"`
	Hashes     []string `json:"hashes"`
	Name       string   `json:"name,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

// The upstream server's admin API port. The Android app's admin screen writes
// it into the links it shares, where it is not a tunnel listener at all.
const linkAdminPort = "56002"

// The no-DTLS listener's port, which the link never names: the app keeps it in
// its own settings and replaces the link's port with it, as it does for RAW.
const linkDirectPort = "56002"

// runParseLink reads a link from stdin, so that it stays out of the process
// list, and prints its settings as JSON for the mode given.
func runParseLink(mode string, noDTLS bool) int {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 64<<10))
	if err == nil {
		var s linkSettings
		if s, err = parseLink(string(raw), mode, noDTLS); err == nil {
			err = json.NewEncoder(os.Stdout).Encode(s)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// A link names the server and, at best, its DTLS port: the mode is the
// user's choice, and the RAW and no-DTLS listeners have ports of their own.
func parseLink(raw, mode string, noDTLS bool) (linkSettings, error) {
	l, port, err := readLink(raw)
	if err != nil {
		return l, err
	}
	switch {
	case mode == "rawtun":
	case noDTLS:
		l.PeerPort = linkDirectPort
	case port == linkAdminPort:
		l.Warnings = append(l.Warnings, "admin_port")
	default:
		l.PeerPort = port
	}
	return l, nil
}

// readLink returns the settings and the server port as the link wrote it.
func readLink(raw string) (linkSettings, string, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, string(rune(0xFEFF))))
	lower := strings.ToLower(raw)

	var l linkSettings
	var peer, port, hashes string
	switch {
	case strings.HasPrefix(lower, "qwdtt:"):
		_, query, _ := strings.Cut(raw, "?")
		q, err := url.ParseQuery(query)
		if err != nil {
			return l, "", fmt.Errorf("unreadable qwdtt link: %w", err)
		}
		peer = q.Get("peer")
		port = first(q.Get("dtls_port"), q.Get("server_port"))
		l.Password = first(q.Get("pass"), q.Get("password"))
		hashes = first(q.Get("hashes"), q.Get("vk"), q.Get("vkHashes"), q.Get("hash"))
		l.ListenPort = first(q.Get("port"), q.Get("listenPort"))
		l.Name = q.Get("name")

	case strings.HasPrefix(lower, "wdtt://"):
		body := raw[len("wdtt://"):]
		body, l.Name, _ = strings.Cut(body, "#")
		if m, err := profileJSON(body); err == nil {
			peer, l.Password, hashes, l.ListenPort, l.Name = fromProfile(m, l.Name)
			break
		}
		// host:dtls_port:wg_port:local_port:password[:hash]; the server's
		// WireGuard port is internal to it and of no use to the client.
		parts := strings.Split(body, ":")
		if len(parts) < 5 {
			return l, "", errors.New("unreadable wdtt link: expected host:port:port:port:password[:hash]")
		}
		peer = net.JoinHostPort(parts[0], parts[1])
		l.ListenPort, l.Password = parts[3], parts[4]
		hashes = strings.Join(parts[5:], ":")

	case strings.Contains(lower, "/call/join/"):
		return l, "", errors.New("a VK call link carries a hash, not a server: add it to the hash list instead")

	default:
		m, err := profileJSON(raw)
		if err != nil {
			return l, "", errors.New("not a qwdtt://, wdtt:// or profile link")
		}
		peer, l.Password, hashes, l.ListenPort, l.Name = fromProfile(m, "")
	}

	host, p, err := net.SplitHostPort(strings.TrimSpace(peer))
	if err != nil {
		host = strings.Trim(strings.TrimSpace(peer), "[]")
	} else if p != "" {
		port = p
	}
	l.PeerHost = host
	l.Hashes = ParseHashes(hashes)
	if l.Hashes == nil {
		l.Hashes = []string{}
	}

	switch {
	case l.PeerHost == "":
		return l, "", errors.New("the link names no server")
	case l.Password == "":
		return l, "", errors.New("the link carries no password")
	case !isPort(port) || (l.ListenPort != "" && !isPort(l.ListenPort)):
		return l, "", errors.New("the link has a port that is not a number")
	// setup.sh hands the password to uci batch, which takes a newline as the
	// end of one command and the start of the next.
	case strings.ContainsFunc(l.PeerHost+l.Password, unicode.IsControl):
		return l, "", errors.New("the link has a control character in its server or password")
	}
	return l, port, nil
}

// A profile the Android app exports, as JSON or base64 of it.
func profileJSON(s string) (map[string]any, error) {
	s = strings.TrimSpace(s)
	data := []byte(s)
	if !strings.HasPrefix(s, "{") {
		var err error
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding,
			base64.URLEncoding, base64.RawURLEncoding} {
			if data, err = enc.DecodeString(s); err == nil {
				break
			}
		}
		if err != nil {
			return nil, err
		}
	}
	var m map[string]any
	return m, json.Unmarshal(data, &m)
}

func fromProfile(m map[string]any, name string) (peer, pass, hashes, listen, outName string) {
	return field(m, "peer"), first(field(m, "password"), field(m, "pass")),
		first(field(m, "hashes"), field(m, "vkHashes")),
		first(field(m, "port"), field(m, "listenPort")),
		first(field(m, "name"), name)
}

// A JSON value as text, whether the exporter wrote it as a string, a number or
// a list.
func field(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case []any:
		parts := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ",")
	}
	return ""
}

func first(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func isPort(s string) bool {
	if s == "" {
		return true
	}
	n, err := strconv.Atoi(s)
	return err == nil && n > 0 && n < 65536
}
