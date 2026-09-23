#!/bin/sh
# The netifd protocol handler for qWDTT. netifd owns the interface and
# supervises the client; the client answers with the tunnel's address through
# /lib/netifd/qwdtt-up.sh once the server has assigned one, because none of it
# is known before then.

[ -n "$INCLUDE_ONLY" ] || {
	. /lib/functions.sh
	. ../netifd-proto.sh
	init_proto "$@"
}

CLIENT=/usr/bin/qwdtt-client

# Which other qWDTT interface already answers to this device_id, if any. The
# server takes two tunnels that share one for a single device and disconnects
# them in turn, so the symptom is a flapping link rather than a configuration
# error, and it is worth refusing up front.
#
# Written against globals because config_foreach takes no context and netifd
# brings interfaces up one at a time - there is no moment at which one run of
# this script sees them all, so each asks about itself.
QWDTT_ID=
QWDTT_SELF=
QWDTT_OWNER=

qwdtt_claim() {
	local section="$1" proto id

	[ -z "$QWDTT_OWNER" ] && [ "$section" != "$QWDTT_SELF" ] || return 0
	config_get proto "$section" proto
	[ "$proto" = qwdtt ] || return 0
	config_get id "$section" device_id
	[ "$id" = "$QWDTT_ID" ] && QWDTT_OWNER="$section"
	return 0
}

device_id_owner() {
	QWDTT_SELF="$1"
	QWDTT_ID="$2"
	QWDTT_OWNER=

	config_foreach qwdtt_claim interface
	echo "$QWDTT_OWNER"
}

# The device outlives the client on purpose - see TUNSETPERSIST in the client's
# raw_tun_native_linux.go - and netifd tears the protocol down and sets it up
# again every time the client exits. Deleting the device there would give the
# tunnel a new interface index on every reconnect, which is the one thing
# persistence exists to prevent, so it is left until the interface stops being
# a qWDTT tunnel at all.
#
# tun_flags exists only on a TUN device, which is what keeps this from deleting
# an unrelated interface that happens to share the name.
drop_device() {
	[ -e "/sys/class/net/$1/tun_flags" ] || return 0
	ip link del "$1" 2>/dev/null
}

proto_qwdtt_init_config() {
	no_device=1
	available=1

	proto_config_add_string  "peer_host"
	proto_config_add_int     "peer_port"
	proto_config_add_string  "password"
	proto_config_add_string  "device_id"
	proto_config_add_array   "hash"
	proto_config_add_int     "workers"
	proto_config_add_string  "go_dns"
	proto_config_add_string  "obfs"
	proto_config_add_string  "captcha_mode"
	proto_config_add_string  "vk_auth"
	proto_config_add_string  "vk_anon_path"
	proto_config_add_string  "vk_creds_file"
	proto_config_add_boolean "no_dtls"
	proto_config_add_boolean "turn_tcp"
}

proto_qwdtt_setup() {
	local config="$1"
	local peer_host peer_port password device_id workers go_dns obfs
	local captcha_mode vk_auth vk_anon_path vk_creds_file no_dtls turn_tcp
	local hashes ip4table defaultroute owner

	json_get_vars peer_host peer_port password device_id workers go_dns obfs \
		captcha_mode vk_auth vk_anon_path vk_creds_file no_dtls turn_tcp
	# The client wants one comma-separated -vk value; the list arrives
	# space-separated and a VK hash contains no spaces.
	json_get_values hashes "hash"
	hashes=$(echo "$hashes" | tr -s ' ' ',' | sed -e 's/^,//' -e 's/,$//')

	config_load network
	config_get ip4table "$config" ip4table
	config_get_bool defaultroute "$config" defaultroute 1

	[ -n "$peer_host" ] || {
		logger -t qwdtt "network.$config.peer_host is not set"
		proto_notify_error "$config" "MISSING_PEER_HOST"
		proto_block_restart "$config"
		return 1
	}
	[ -n "$hashes" ] || {
		logger -t qwdtt "network.$config.hash is empty"
		proto_notify_error "$config" "MISSING_HASH"
		proto_block_restart "$config"
		return 1
	}
	# The interface name is the TUN device, and the kernel takes 15 characters.
	[ ${#config} -le 15 ] || {
		logger -t qwdtt "network.$config: the name is longer than 15 characters, which cannot be an interface name"
		proto_notify_error "$config" "NAME_TOO_LONG"
		proto_block_restart "$config"
		return 1
	}
	# The client reaches its VK TURN relays over the WAN, so a default route
	# into the tunnel in the main table would send the tunnel's own transport
	# through the tunnel. Refusing is the kinder failure: the alternative takes
	# the router's WAN with it and leaves nothing to diagnose from.
	[ "$defaultroute" = 0 ] || [ -n "$ip4table" ] || {
		logger -t qwdtt "network.$config: set ip4table, or the default route into the tunnel would carry the client's own traffic to VK"
		proto_notify_error "$config" "MISSING_IP4TABLE"
		proto_block_restart "$config"
		return 1
	}

	# The server knows the tunnel by this and nothing else, so there is no
	# value to fall back to: a derived one would quietly reach the server as
	# a different device than the operator named.
	[ -n "$device_id" ] || {
		logger -t qwdtt "network.$config.device_id is not set"
		proto_notify_error "$config" "MISSING_DEVICE_ID"
		proto_block_restart "$config"
		return 1
	}
	owner=$(device_id_owner "$config" "$device_id")
	[ -z "$owner" ] || {
		logger -t qwdtt "network.$config: device_id $device_id is already used by $owner"
		proto_notify_error "$config" "DUPLICATE_DEVICE_ID"
		proto_block_restart "$config"
		return 1
	}

	# INTERFACE is what the up-script reports against and what tags the
	# client's lines in the system log; netifd runs every protocol task under
	# its own name, so nothing else tells two tunnels apart.
	proto_export "INTERFACE=$config"
	proto_export "TZ=$(uci -q get system.@system[0].zonename)"

	# Built with set -- rather than one expansion per option: a value that
	# happens to contain a space stays a single argument, and no shell ever
	# re-parses the password.
	set -- -netifd \
		-mode rawtun \
		-tun-name "$config" \
		-peer "${peer_host}:${peer_port:-56003}" \
		-vk "$hashes" \
		-password "$password" \
		-device-id "$device_id" \
		-n "${workers:-9}" \
		-go-dns "${go_dns:-yandex}" \
		-obfs "${obfs:-audio}" \
		-captcha-mode "${captcha_mode:-auto}" \
		-vk-auth "${vk_auth:-anonymous}" \
		-vk-anon-path "${vk_anon_path:-vkcalls}"
	[ -z "$vk_creds_file" ] || set -- "$@" -vk-creds-file "$vk_creds_file"
	# Only passed when on: Go's flag package reads a bare -notls as true.
	[ "$no_dtls" != 1 ] || set -- "$@" -notls
	[ "$turn_tcp" != 1 ] || set -- "$@" -turn-tcp

	proto_run_command "$config" "$CLIENT" "$@"
}

proto_qwdtt_teardown() {
	local config="$1"

	proto_kill_command "$config"
	[ "$(uci -q get "network.$config.proto")" = qwdtt ] && return 0

	drop_device "$config"
	# What the up-script and the client left for the status page to read. Both
	# describe a tunnel that is gone, and the counter baseline would otherwise
	# be subtracted from whatever took the device's name next.
	rm -f "/var/run/qwdtt/$config".counters "/var/run/qwdtt/$config".workers "/var/run/qwdtt/$config".relays
	# The SNAT rule the up-script wrote names an address nothing answers to any
	# more, so it goes with the tunnel rather than outliving it.
	uci -q delete "firewall.${config}_snat" || return 0
	uci commit firewall
	[ ! -x /etc/init.d/firewall ] || /etc/init.d/firewall reload >/dev/null 2>&1
}

[ -n "$INCLUDE_ONLY" ] || add_protocol qwdtt
