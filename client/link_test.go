package main

import (
	"encoding/base64"
	"reflect"
	"testing"
)

// The forms the Android app and its server's bot actually write, and the
// profile JSON the app exports. The port the link names is applied only where
// the app applies it.
func TestParseLink(t *testing.T) {
	profile := `{"name":"P","peer":"203.0.113.7:56000","pass":"secret","hashes":["hashA","hashB"],"port":9001,"workers":18}`
	b64 := base64.StdEncoding.EncodeToString([]byte(profile))

	cases := []struct {
		name   string
		link   string
		mode   string
		noDTLS bool
		want   linkSettings
	}{
		{"qwdtt, WireGuard takes the link's port",
			"qwdtt://config?name=Home&peer=203.0.113.7%3A56000&hashes=hashA&workers=18&port=9000&pass=secret",
			"vpn", false,
			linkSettings{PeerHost: "203.0.113.7", PeerPort: "56000", ListenPort: "9000", Password: "secret", Hashes: []string{"hashA"}, Name: "Home"}},
		{"qwdtt, RAW leaves the port to the mode",
			"qwdtt://config?peer=203.0.113.7%3A56000&hashes=hashA&port=9000&pass=secret",
			"rawtun", false,
			linkSettings{PeerHost: "203.0.113.7", ListenPort: "9000", Password: "secret", Hashes: []string{"hashA"}}},
		{"qwdtt without DTLS uses the direct listener's port",
			"qwdtt://config?peer=203.0.113.7%3A56000&hashes=hashA&pass=secret",
			"vpn", true,
			linkSettings{PeerHost: "203.0.113.7", PeerPort: "56002", Password: "secret", Hashes: []string{"hashA"}}},
		{"the admin screen's port is not used, and said so",
			"qwdtt://config?peer=203.0.113.7%3A56002&hashes=hashA&port=9000&pass=secret",
			"vpn", false,
			linkSettings{PeerHost: "203.0.113.7", ListenPort: "9000", Password: "secret", Hashes: []string{"hashA"}, Warnings: []string{"admin_port"}}},
		{"qwdtt:config with a bare host and dtls_port, the bot's shape",
			"qwdtt:config?name=Main&peer=203.0.113.7&dtls_port=56010&hashes=hashA%2ChashB&workers=9&port=9000&pass=secret",
			"vpn", false,
			linkSettings{PeerHost: "203.0.113.7", PeerPort: "56010", ListenPort: "9000", Password: "secret", Hashes: []string{"hashA", "hashB"}, Name: "Main"}},
		{"wdtt colon form",
			"wdtt://203.0.113.7:56000:56001:9000:secret:hashA#Office",
			"vpn", false,
			linkSettings{PeerHost: "203.0.113.7", PeerPort: "56000", ListenPort: "9000", Password: "secret", Hashes: []string{"hashA"}, Name: "Office"}},
		{"wdtt colon form without a hash",
			"wdtt://203.0.113.7:56000:56001:9000:secret",
			"vpn", false,
			linkSettings{PeerHost: "203.0.113.7", PeerPort: "56000", ListenPort: "9000", Password: "secret", Hashes: []string{}}},
		{"wdtt with base64 profile JSON",
			"wdtt://" + b64,
			"vpn", false,
			linkSettings{PeerHost: "203.0.113.7", PeerPort: "56000", ListenPort: "9001", Password: "secret", Hashes: []string{"hashA", "hashB"}, Name: "P"}},
		{"plain profile JSON",
			profile,
			"rawtun", false,
			linkSettings{PeerHost: "203.0.113.7", ListenPort: "9001", Password: "secret", Hashes: []string{"hashA", "hashB"}, Name: "P"}},
		{"VK call links among the hashes reduce to their tokens",
			"qwdtt://config?peer=203.0.113.7&hashes=https%3A%2F%2Fvk.com%2Fcall%2Fjoin%2FhashA%2ChashA&pass=secret",
			"rawtun", false,
			linkSettings{PeerHost: "203.0.113.7", Password: "secret", Hashes: []string{"hashA"}}},
	}
	for _, tc := range cases {
		got, err := parseLink(tc.link, tc.mode, tc.noDTLS)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got  %+v\n want %+v", tc.name, got, tc.want)
		}
	}
}

// Each of these is refused rather than half-imported.
func TestParseLinkRefuses(t *testing.T) {
	for _, link := range []string{
		"",
		"https://vk.com/call/join/hashA",
		"qwdtt://config?peer=203.0.113.7&hashes=hashA",
		"qwdtt://config?pass=secret&hashes=hashA",
		"wdtt://203.0.113.7:56000:secret",
		"wdtt://203.0.113.7:port:56001:9000:secret:hashA",
		"qwdtt://config?peer=203.0.113.7&hashes=hashA&pass=secret%0Acommit",
		"qwdtt://config?peer=203.0.113%0D.7&hashes=hashA&pass=secret",
		"vless://whatever",
	} {
		if got, err := parseLink(link, "vpn", false); err == nil {
			t.Errorf("%q: accepted as %+v", link, got)
		}
	}
}
