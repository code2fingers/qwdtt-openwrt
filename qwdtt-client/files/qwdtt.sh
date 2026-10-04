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

# Where the client and the up-script leave a tunnel's runtime state. On tmpfs,
# written when the client starts and removed when the protocol is torn down,
# so it says what is running where the configuration only says what should be.
# Overridable so the handler's tests can look somewhere writable.
QWDTT_RUN_DIR="${QWDTT_RUN_DIR:-/var/run/qwdtt}"

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
	local section="$1" proto id disabled

	[ -z "$QWDTT_OWNER" ] && [ "$section" != "$QWDTT_SELF" ] || return 0
	config_get proto "$section" proto
	[ "$proto" = qwdtt ] || return 0
	# A disabled tunnel never connects, so it cannot flap the server, so it
	# must not reserve its device_id against one that would. The migration
	# makes this reachable: it writes device_id=openwrt for every legacy
	# section that had none, so an enabled and a disabled tunnel can share one.
	config_get_bool disabled "$section" disabled 0
	[ "$disabled" = 0 ] || return 0
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

# The same question for the relay's local port. Two relays on one port do not
# collide - SO_REUSEADDR lets the second bind and the packets are split
# between them - so nothing downstream can detect it and the configuration is
# what has to be refused.
QWDTT_PORT=

# Whether a tunnel still has state in the run directory. Written when its
# client starts and removed by teardown below, so it answers the question the
# configuration cannot: is this one running right now.
relay_running() {
	local f
	for f in "$QWDTT_RUN_DIR/$1".*; do
		[ -e "$f" ] && return 0
	done
	return 1
}

qwdtt_port_claim() {
	local section="$1" proto mode port disabled auto

	[ -z "$QWDTT_OWNER" ] && [ "$section" != "$QWDTT_SELF" ] || return 0
	config_get proto "$section" proto
	[ "$proto" = qwdtt ] || return 0
	config_get mode "$section" mode rawtun
	[ "$mode" = wireguard ] || return 0
	# A tunnel the operator has parked holds no port, so whichever of the two
	# is started first claims it and the second is refused on its way up.
	# Parking it does not stop one that is already running, though, and a
	# relay that still holds the port while the configuration says it is
	# parked is the case worth catching: SO_REUSEADDR lets the second one
	# bind and the packets are split with nothing downstream to notice.
	config_get_bool disabled "$section" disabled 0
	config_get_bool auto "$section" auto 1
	if [ "$disabled" != 0 ] || [ "$auto" = 0 ]; then
		relay_running "$section" || return 0
	fi
	config_get port "$section" listen_port 9000
	[ "$port" = "$QWDTT_PORT" ] && QWDTT_OWNER="$section"
	return 0
}

listen_port_owner() {
	QWDTT_SELF="$1"
	QWDTT_PORT="$2"
	QWDTT_OWNER=

	config_foreach qwdtt_port_claim interface
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

# What a wireguard-mode tunnel is reported up on.
#
# netifd refuses a protocol update that names no device when the interface has
# none of its own, so an interface with nothing to name cannot be reported up
# at all: it stays in setup for ever, and every page calls a relay that is
# working "connecting". Naming a device that already exists is worse than it
# sounds - with lo, firewall4 resolved the zone's network list to it and put
# loopback in a zone whose input policy is DROP, MTU mangling and all.
#
# So the interface gets a device of its own, carrying nothing. Named after the
# interface, like the TUN device of a RAW-IP tunnel, which is what keeps it
# inside the qwdtt+ firewall zone instead of landing it somewhere unexpected.
#
# A TUN device with nothing attached to it rather than a dummy, which would be
# the obvious choice: dummy needs kmod-dummy, which an OpenWrt image does not
# carry by default, while kmod-tun is already required for the RAW-IP shape.
relay_device() {
	[ -e "/sys/class/net/$1" ] || ip tuntap add mode tun "$1" || return 1
	ip link set "$1" up
}

proto_qwdtt_init_config() {
	no_device=1
	available=1

	proto_config_add_string  "mode"
	proto_config_add_string  "peer_host"
	proto_config_add_int     "peer_port"
	proto_config_add_int     "listen_port"
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
	proto_config_add_string  "config_file"
	proto_config_add_string  "vk_client_id"
	proto_config_add_string  "vk_client_secret"
	proto_config_add_int     "rate_down"
	proto_config_add_int     "rate_up"
	proto_config_add_boolean "no_dtls"
	proto_config_add_boolean "turn_tcp"
}

# The two shapes a qWDTT tunnel comes in, which differ in what the interface
# ends up owning rather than in how the transport works.
#
# rawtun is the tunnel itself: the client creates the TUN device, the server
# assigns its address, and the interface carries traffic like any other.
#
# wireguard carries no traffic of its own. The client relays a local UDP port
# through the VK call to the server, which forwards it to its own WireGuard
# port, so the interface provides an endpoint on 127.0.0.1 and nothing else -
# no device, no address, no routes. What uses it is an ordinary `proto
# wireguard` interface whose peer endpoint is that port, and everything about
# the WireGuard tunnel - keys, addresses, allowed IPs, routing - belongs to
# that interface.
#
# The ports are the server's two listeners, not its WireGuard port: that one
# is internal, reachable from the server's own loopback and nowhere else, and
# a client pointed at it is answered by nothing at all.
QWDTT_PEER_PORT_rawtun=56003
QWDTT_PEER_PORT_wireguard=56000

proto_qwdtt_setup() {
	local config="$1"
	local mode peer_host peer_port listen_port password device_id workers go_dns obfs
	local captcha_mode vk_auth vk_anon_path vk_creds_file vk_client_id vk_client_secret
	local config_file rate_down rate_up no_dtls turn_tcp hashes ip4table defaultroute owner

	json_get_vars mode peer_host peer_port listen_port password device_id workers go_dns obfs \
		captcha_mode vk_auth vk_anon_path vk_creds_file vk_client_id vk_client_secret \
		config_file rate_down rate_up no_dtls turn_tcp

	mode="${mode:-rawtun}"
	case "$mode" in
	rawtun|wireguard) ;;
	*)
		logger -t qwdtt "network.$config.mode is $mode, which is neither rawtun nor wireguard"
		proto_notify_error "$config" "INVALID_MODE"
		proto_block_restart "$config"
		return 1
		;;
	esac
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
	# Every packet is sealed with a key derived from this, so the client has
	# nothing to do without it and exits - and netifd starts it again the
	# instant it does, with no backoff. Measured on a router: 349 starts in
	# thirty seconds, with the interface page showing nothing at all.
	[ -n "$password" ] || [ -n "$config_file" ] || {
		logger -t qwdtt "network.$config.password is not set"
		proto_notify_error "$config" "MISSING_PASSWORD"
		proto_block_restart "$config"
		return 1
	}
	# Checked here rather than left to the client, which exits on an
	# unreadable one. netifd would start it again the instant it does, which
	# is the restart storm MISSING_PASSWORD above exists to prevent.
	[ -z "$config_file" ] || [ -r "$config_file" ] || {
		logger -t qwdtt "network.$config.config_file $config_file cannot be read"
		proto_notify_error "$config" "UNREADABLE_CONFIG_FILE"
		proto_block_restart "$config"
		return 1
	}
	# The interface name is also the name of its device - the TUN one a rawtun
	# tunnel carries traffic on, the placeholder a wireguard one is reported up
	# on - and the kernel takes 15 characters for either.
	[ ${#config} -le 15 ] || {
		logger -t qwdtt "network.$config: the name is longer than 15 characters, which cannot be an interface name"
		proto_notify_error "$config" "NAME_TOO_LONG"
		proto_block_restart "$config"
		return 1
	}
	# A wireguard-mode tunnel adds no route of its own, so this one is about
	# the RAW-IP shape alone.
	if [ "$mode" = rawtun ]; then
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
	fi

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
	if [ "$mode" = wireguard ]; then
		owner=$(listen_port_owner "$config" "${listen_port:-9000}")
		[ -z "$owner" ] || {
			logger -t qwdtt "network.$config: local port ${listen_port:-9000} is already used by $owner"
			proto_notify_error "$config" "DUPLICATE_LISTEN_PORT"
			proto_block_restart "$config"
			return 1
		}
	fi

	# INTERFACE is what the up-script reports against and what tags the
	# client's lines in the system log; netifd runs every protocol task under
	# its own name, so nothing else tells two tunnels apart.
	proto_export "INTERFACE=$config"
	proto_export "TZ=$(uci -q get system.@system[0].zonename)"
	# Secrets travel in the environment, not on the command line:
	# /proc/<pid>/cmdline is readable by every process on the router and
	# /proc/<pid>/environ by root alone. The password used to be in the
	# readable one, and in every `ps` an operator pasted into a bug report.
	#
	# This does not hide them completely, and config_file is the way out:
	# netifd hands the interface's own configuration to this script as an
	# argument and proto_export reaches ubus as one too, so anything kept in
	# /etc/config/network is readable for as long as the interface is coming
	# up. Only the path is, once the secrets live in the client's own file.
	[ -z "$password" ] || proto_export "QWDTT_PASSWORD=$password"
	[ -z "$vk_client_id" ] || proto_export "QWDTT_VK_CLIENT_ID=$vk_client_id"
	[ -z "$vk_client_secret" ] || proto_export "QWDTT_VK_CLIENT_SECRET=$vk_client_secret"

	# Written out rather than as a shell default, because the fallback is one
	# port per mode rather than one port. The two are named above.
	[ -n "$peer_port" ] || eval "peer_port=\$QWDTT_PEER_PORT_$mode"

	# Built with set -- rather than one expansion per option: a value that
	# happens to contain a space stays a single argument, and no shell ever
	# re-parses the password.
	set -- -netifd
	if [ "$mode" = wireguard ]; then
		set -- "$@" -mode vpn -listen "127.0.0.1:${listen_port:-9000}"
	else
		set -- "$@" -mode rawtun -tun-name "$config"
	fi
	set -- "$@" \
		-peer "${peer_host}:${peer_port}" \
		-vk "$hashes" \
		-device-id "$device_id" \
		-n "${workers:-9}" \
		-go-dns "${go_dns:-yandex}" \
		-obfs "${obfs:-audio}" \
		-captcha-mode "${captcha_mode:-auto}" \
		-vk-auth "${vk_auth:-anonymous}" \
		-vk-anon-path "${vk_anon_path:-vkcalls}"
	[ -z "$vk_creds_file" ] || set -- "$@" -vk-creds-file "$vk_creds_file"
	[ -z "$config_file" ] || set -- "$@" -config "$config_file"
	[ -z "$rate_down" ] || set -- "$@" -rate-down "$rate_down"
	[ -z "$rate_up" ] || set -- "$@" -rate-up "$rate_up"
	# Only passed when on: Go's flag package reads a bare -notls as true.
	[ "$no_dtls" != 1 ] || set -- "$@" -notls
	[ "$turn_tcp" != 1 ] || set -- "$@" -turn-tcp

	# Before the client, not after: the client is what reports a wireguard-mode
	# interface up - through /lib/netifd/qwdtt-relay-up.sh, once its relay holds
	# the local port - and netifd refuses an update naming a device that does
	# not exist yet. Failing here also costs nothing to unwind, because nothing
	# has been started.
	[ "$mode" != wireguard ] || relay_device "$config" || {
		logger -t qwdtt "network.$config: cannot create the placeholder device"
		proto_notify_error "$config" "NO_RELAY_DEVICE"
		return 1
	}

	proto_run_command "$config" "$CLIENT" "$@"

	# Neither shape is reported up from here. A rawtun tunnel waits for the
	# server to answer with an address and reports through qwdtt-up.sh; a
	# wireguard one has no address to wait for and reports as soon as its relay
	# is listening. Both are facts only the client knows.
}

proto_qwdtt_teardown() {
	local config="$1"

	proto_kill_command "$config"
	# Still a RAW-IP qWDTT tunnel: the device is kept across the teardown and
	# setup that every reconnect goes through, which is what holds its
	# interface index. A wireguard-mode tunnel has no device to keep, and the
	# TUN device left over from a tunnel that was switched to it is exactly
	# what has to go - it would otherwise sit there holding the address the
	# server last assigned until the router is rebooted.
	[ "$(uci -q get "network.$config.proto")" = qwdtt ] &&
		[ "$(uci -q get "network.$config.mode")" != wireguard ] && return 0

	drop_device "$config"
	# What the up-script and the client left for the status page to read. All
	# of it describes a tunnel that is gone: the counter baseline would be
	# subtracted from whatever took the device's name next, and the WireGuard
	# configuration the server issued holds a private key that has no reason
	# to outlive the tunnel it was issued to.
	#
	# Matched by the interface's own prefix rather than listed, because the
	# list was already a suffix out of date twice over by the time anyone
	# noticed.
	rm -f "$QWDTT_RUN_DIR/$config".*
	# The SNAT rule the up-script wrote names an address nothing answers to any
	# more, so it goes with the tunnel rather than outliving it. So does the
	# zone entry it adds for an off-pattern name, or the next interface to take
	# that name would land in this zone.
	local changed= n
	uci -q delete "firewall.${config}_snat" && changed=1
	for n in $(uci -q get firewall.qwdtt.network); do
		[ "$n" = "$config" ] && uci -q del_list "firewall.qwdtt.network=$config" && changed=1
	done
	[ -n "$changed" ] || return 0
	uci commit firewall
	[ ! -x /etc/init.d/firewall ] || /etc/init.d/firewall reload >/dev/null 2>&1
}

[ -n "$INCLUDE_ONLY" ] || add_protocol qwdtt
