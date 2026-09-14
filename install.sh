#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
#
# One-command installer for qWDTT on OpenWrt.
#
#   wget -qO- https://raw.githubusercontent.com/code2fingers/qwdtt-openwrt/main/install.sh | sh
#
# It adds the signed feed and its trust key, then installs the control script,
# the LuCI page and the client. Both package managers are served: 25.12 and
# later read an apk index, 24.10 an opkg one, from separate paths under the
# same site.
#
# It configures nothing and starts nothing: the peer, password and call hashes
# are secrets the operator supplies afterwards, over LuCI or UCI.

set -eu

REPO_OWNER=code2fingers
REPO_NAME=qwdtt-openwrt
FEED_BASE="https://${REPO_OWNER}.github.io/${REPO_NAME}"

# A file of our own rather than a line appended to customfeeds: re-running is
# then a write instead of a duplicate, and removing us is a single rm.
APK_KEY_DEST=/etc/apk/keys/qwdtt.pem
APK_FEED_LIST=/etc/apk/repositories.d/qwdtt.list
OPKG_FEED_CONF=/etc/opkg/qwdtt.conf

# The three qWDTT packages, plus ip-full: the client policy-routes with
# `ip rule`/`ip route ... table`, which BusyBox ip does not fully implement, and
# qwdtt-client does not depend on it (kmod-tun and ca-bundle it does).
CORE_PKGS="ip-full qwdtt luci-app-qwdtt qwdtt-client"
I18N_PKG="luci-i18n-qwdtt-ru"

WANT_I18N=1

msg()  { printf '\033[1;32m[qwdtt]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[qwdtt]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[qwdtt]\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
	cat <<EOF
Usage: install.sh [-e] [-b URL] [-h]

  -e        do not install the Russian LuCI translation ($I18N_PKG)
  -b URL    override the feed base URL (for testing against a sandbox feed)
  -h        show this help

Run as root on the router:
  wget -qO- https://raw.githubusercontent.com/$REPO_OWNER/$REPO_NAME/main/install.sh | sh
EOF
	exit 0
}

while getopts "eb:h" opt; do
	case "$opt" in
	e) WANT_I18N=0 ;;
	b) FEED_BASE=$OPTARG ;;
	h) usage ;;
	*) usage ;;
	esac
done

[ "$(id -u)" = 0 ] || die "run this as root."

fetch() { # fetch URL DEST
	if command -v wget >/dev/null 2>&1; then
		wget -qO "$2" "$1"
	elif command -v curl >/dev/null 2>&1; then
		curl -fsSL -o "$2" "$1"
	else
		die "neither wget nor curl is available."
	fi
}

# The router's package architecture, e.g. aarch64_cortex-a53. apk knows it
# first-hand; the rest are fallbacks in case apk cannot answer.
detect_pkgarch() {
	arch=""
	if command -v apk >/dev/null 2>&1; then
		arch=$(apk --print-arch 2>/dev/null || true)
	fi
	if [ -z "$arch" ]; then
		arch=$(ubus call system board 2>/dev/null |
			jsonfilter -e '@.release.arch' 2>/dev/null || true)
	fi
	if [ -z "$arch" ] && [ -f /etc/openwrt_release ]; then
		arch=$(sed -n "s/^DISTRIB_ARCH='\(.*\)'/\1/p" /etc/openwrt_release || true)
	fi
	if [ -z "$arch" ] && command -v opkg >/dev/null 2>&1; then
		arch=$(opkg print-architecture 2>/dev/null |
			awk 'BEGIN{m=0}{if($3>m){m=$3;a=$2}}END{print a}' || true)
	fi
	[ -n "$arch" ] || arch=$(uname -m)
	printf '%s\n' "$arch"
}

install_via_feed() {
	arch=$(detect_pkgarch)
	[ -n "$arch" ] || die "could not determine the package architecture."

	key_url="$FEED_BASE/qwdtt.pem"
	feed_url="$FEED_BASE/releases/25.12/$arch/packages.adb"

	msg "architecture: $arch"

	# The key has to be in place before `apk update`, so the feed's signed index
	# verifies and the packages install trusted (no --allow-untrusted).
	msg "trust key:    $key_url"
	mkdir -p /etc/apk/keys /etc/apk/repositories.d
	fetch "$key_url" "$APK_KEY_DEST" ||
		die "could not download the trust key from $key_url"
	grep -q "BEGIN PUBLIC KEY" "$APK_KEY_DEST" 2>/dev/null ||
		die "the downloaded trust key is not a PEM public key -- is the feed published?"

	msg "feed:         $feed_url"
	printf '%s\n' "$feed_url" >"$APK_FEED_LIST"

	msg "apk update"
	apk update

	# --upgrade, because `apk add` is a no-op on a package that is already
	# installed: without it, re-running this to pick up a new release leaves
	# the old version in place and says nothing.
	msg "installing: $CORE_PKGS"
	# shellcheck disable=SC2086
	apk add --upgrade $CORE_PKGS

	if [ "$WANT_I18N" = 1 ]; then
		apk add --upgrade "$I18N_PKG" ||
			warn "could not install $I18N_PKG (translation only; skipping)"
	fi

	msg "done."
	post_install_hint
}

install_via_opkg() {
	arch=$(detect_pkgarch)
	[ -n "$arch" ] || die "could not determine the package architecture."

	key_url="$FEED_BASE/qwdtt-usign.pub"
	feed_url="$FEED_BASE/releases/24.10/$arch"

	msg "architecture: $arch"

	command -v usign >/dev/null 2>&1 ||
		die "usign is missing, so opkg cannot verify a signed feed. Install it first."

	msg "trust key:    $key_url"
	mkdir -p /etc/opkg/keys
	fetch "$key_url" /tmp/qwdtt-usign.pub ||
		die "could not download the trust key from $key_url"

	# The file name under /etc/opkg/keys has to be the key's own id: opkg reads
	# the signer id out of the signature and looks for a file of that name.
	keyid=$(usign -F -p /tmp/qwdtt-usign.pub 2>/dev/null) ||
		die "the downloaded trust key is not a usign public key -- is the feed published?"
	[ -n "$keyid" ] ||
		die "the downloaded trust key is not a usign public key -- is the feed published?"
	mv /tmp/qwdtt-usign.pub "/etc/opkg/keys/$keyid"
	msg "key id:       $keyid"

	msg "feed:         $feed_url"
	printf 'src/gz qwdtt %s\n' "$feed_url" >"$OPKG_FEED_CONF"

	msg "opkg update"
	opkg update

	msg "installing: $CORE_PKGS"
	# shellcheck disable=SC2086
	opkg install $CORE_PKGS
	# `opkg install` is a no-op on a package that is already installed, so
	# without this a re-run to pick up a new release leaves the old version
	# in place. It runs second because `opkg upgrade` needs them installed.
	# shellcheck disable=SC2086
	opkg upgrade $CORE_PKGS ||
		warn "opkg upgrade reported a problem; see its output above"

	if [ "$WANT_I18N" = 1 ]; then
		opkg install "$I18N_PKG" ||
			warn "could not install $I18N_PKG (translation only; skipping)"
		opkg upgrade "$I18N_PKG" ||
			warn "could not upgrade $I18N_PKG (translation only; skipping)"
	fi

	msg "done."
	post_install_hint
}

post_install_hint() {
	cat <<'EOF'

Next steps
  1. Set the peer, password and call hashes -- in LuCI (Services -> qWDTT ->
     Settings), or over UCI:

       uci set qwdtt.main.peer_host='SERVER_IP'
       uci set qwdtt.main.peer_port='56003'
       uci set qwdtt.main.password='CONNECTION_PASSWORD'
       uci add_list qwdtt.main.hash='VK_CALL_HASH'
       uci commit qwdtt

  2. Enable at boot and start now:

       /etc/init.d/qwdtt enable
       /usr/bin/qwdtt start

  The password and hashes are secrets -- do not publish or share them.
EOF
}

if command -v apk >/dev/null 2>&1; then
	install_via_feed
elif command -v opkg >/dev/null 2>&1; then
	install_via_opkg
else
	die "no supported package manager (apk/opkg) found."
fi
