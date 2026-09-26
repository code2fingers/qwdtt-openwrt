#!/bin/sh
# Exercises /lib/netifd/proto/qwdtt.sh away from a router. The handler decides
# what the client is started with and which configurations are refused, and
# every way it can be wrong is quiet: a tunnel that carries nothing, or a
# default route into the tunnel that swallows the client's own traffic to VK
# and takes the router's WAN with it. So the checks it makes are asserted here
# against stubs for the netifd and uci helpers, with the command line printed
# instead of started.
#
# Run from the repository root: sh qwdtt-client/tests/proto.sh
set -u

SECTIONS="qwdtt0 work"
SECTION=qwdtt0

# section.option=value, in the shape /etc/config/network holds. qwdtt0 is a
# complete tunnel; work is a valid second one.
CFG='
qwdtt0.proto=qwdtt
qwdtt0.ip4table=51820
qwdtt0.peer_host=vpn1.example
qwdtt0.peer_port=56003
qwdtt0.password=p1
qwdtt0.device_id=openwrt-qwdtt0
qwdtt0.hash=aaa bbb
work.proto=qwdtt
work.ip4table=51821
work.peer_host=vpn2.example
work.password=p2
work.device_id=openwrt-work
work.hash=ccc
'

cfg() { printf '%s\n' "$CFG" | sed -n "s/^$1\\.$2=//p"; }

set_cfg() { CFG="$CFG
$1=$2"; }

config_load() { :; }

config_get() {
	eval "$1=\"\$(cfg \"\$2\" \"\$3\")\""
	eval "[ -n \"\$$1\" ] || $1=\${4:-}"
}

config_get_bool() { config_get "$@"; }

config_foreach() {
	_fn=$1
	shift 2
	for _sec in $SECTIONS; do
		"$_fn" "$_sec" "$@"
	done
}

json_get_vars() {
	for _name in "$@"; do
		eval "$_name=\"\$(cfg \"\$SECTION\" \"\$_name\")\""
	done
}

json_get_values() { eval "$1=\"\$(cfg \"\$SECTION\" \"\$2\")\""; }

logger() { shift 2; echo "log: $*"; }

uci() {
	[ "${1:-}" = -q ] && shift
	case "${1:-}" in
	get)
		case "$2" in
		'system.@system[0].zonename') echo "Europe/Moscow" ;;
		network.*.proto) _s=${2#network.}; cfg "${_s%.proto}" proto ;;
		esac ;;
	delete) echo "deleted: $2" ;;
	esac
	return 0
}

proto_config_add_string() { :; }
proto_config_add_int() { :; }
proto_config_add_boolean() { :; }
proto_config_add_array() { :; }
proto_export() { echo "export: $1"; }
proto_notify_error() { echo "refused: $2"; }
proto_block_restart() { :; }
proto_run_command() { shift; echo "run: $*"; }
proto_kill_command() { echo "killed: $1"; }
rm() { echo "removed: $*"; }

INCLUDE_ONLY=1
. ./qwdtt-client/files/qwdtt.sh

# There is no /sys here, and what the checks below are about is when the device
# is dropped rather than how it is recognised.
drop_device() { echo "dropped: $1"; }

fail=0

check() {
	local what="$1" got="$2" want="$3"

	[ "$got" = "$want" ] && return 0
	echo "$what:"
	echo "--- got"
	echo "$got"
	echo "--- want"
	echo "$want"
	fail=1
}

# --- the command line -------------------------------------------------------

got=$(proto_qwdtt_setup qwdtt0 2>&1)
want='export: INTERFACE=qwdtt0
export: TZ=Europe/Moscow
run: /usr/bin/qwdtt-client -netifd -mode rawtun -tun-name qwdtt0 -peer vpn1.example:56003 -vk aaa,bbb -password p1 -device-id openwrt-qwdtt0 -n 9 -go-dns yandex -obfs audio -captcha-mode auto -vk-auth anonymous -vk-anon-path vkcalls'
check "a complete tunnel" "$got" "$want"

# Every default here is one the handler supplies rather than the client, so a
# tunnel that sets none of them still runs with the values the documentation
# names.
SECTION=work
got=$(proto_qwdtt_setup work 2>&1)
want='export: INTERFACE=work
export: TZ=Europe/Moscow
run: /usr/bin/qwdtt-client -netifd -mode rawtun -tun-name work -peer vpn2.example:56003 -vk ccc -password p2 -device-id openwrt-work -n 9 -go-dns yandex -obfs audio -captcha-mode auto -vk-auth anonymous -vk-anon-path vkcalls'
check "no peer_port falls back to 56003, and every optional value left unset" "$got" "$want"

# The three that are appended rather than always passed. -notls and -turn-tcp
# are bare flags, which Go's flag package reads as true, so passing them with a
# 0 would switch them on.
set_cfg work.vk_creds_file '/etc/qwdtt/creds with a space.json'
set_cfg work.no_dtls 1
set_cfg work.turn_tcp 0
got=$(proto_qwdtt_setup work 2>&1 | sed -n 's/^run: //p')
case $got in
*"-vk-anon-path vkcalls -vk-creds-file /etc/qwdtt/creds with a space.json -notls") ;;
*)
	echo "the optional flags:"
	echo "--- got"
	echo "$got"
	fail=1 ;;
esac

# --- what is refused --------------------------------------------------------

refusal() {
	local what="$1" section="$2" want="$3" got

	SECTION=$section
	got=$(proto_qwdtt_setup "$section" 2>&1 | sed -n 's/^refused: //p')
	[ "$got" = "$want" ] && return 0
	echo "$what: got refusal ${got:-none}, want $want"
	fail=1
}

SECTIONS="$SECTIONS noserver nohash waytoolongfortun mainte twin loose nodevice"

set_cfg noserver.proto qwdtt
set_cfg noserver.ip4table 51822
set_cfg noserver.hash ddd
refusal "a tunnel with no peer_host" noserver MISSING_PEER_HOST

set_cfg nohash.proto qwdtt
set_cfg nohash.ip4table 51823
set_cfg nohash.peer_host vpn3.example
refusal "a tunnel with no hashes" nohash MISSING_HASH

set_cfg waytoolongfortun.proto qwdtt
set_cfg waytoolongfortun.ip4table 51824
set_cfg waytoolongfortun.peer_host vpn4.example
set_cfg waytoolongfortun.hash eee
refusal "a name no interface can have" waytoolongfortun NAME_TOO_LONG

# The one that would otherwise fail silently and in the wrong direction: the
# client reaches VK over the WAN, so its own transport would be routed into
# the tunnel it is carrying.
set_cfg mainte.proto qwdtt
set_cfg mainte.peer_host vpn5.example
set_cfg mainte.hash fff
refusal "a default route with no table to put it in" mainte MISSING_IP4TABLE

# Unless there is no default route to misplace, which is the split-tunnel case:
# the operator writes the routes and the table is theirs to choose.
set_cfg loose.proto qwdtt
set_cfg loose.peer_host vpn6.example
set_cfg loose.device_id openwrt-loose
set_cfg loose.hash ggg
set_cfg loose.defaultroute 0
SECTION=loose
got=$(proto_qwdtt_setup loose 2>&1 | sed -n 's/^run: //p')
case $got in
/usr/bin/qwdtt-client*) ;;
*)
	echo "defaultroute 0 without ip4table was refused: ${got:-nothing ran}"
	fail=1 ;;
esac

# The server knows a tunnel by its device_id and nothing else, so an absent one
# is refused rather than derived: a value invented here would reach the server
# as a device the operator never named.
set_cfg nodevice.proto qwdtt
set_cfg nodevice.ip4table 51826
set_cfg nodevice.peer_host vpn8.example
set_cfg nodevice.hash iii
refusal "a tunnel with no device_id" nodevice MISSING_DEVICE_ID

# Quieter and worse than a table collision: the server takes two tunnels that
# share a device_id for one device and disconnects them in turn, which reads as
# a flapping link rather than as a configuration mistake.
set_cfg twin.proto qwdtt
set_cfg twin.ip4table 51825
set_cfg twin.peer_host vpn7.example
set_cfg twin.hash hhh
set_cfg twin.device_id openwrt-qwdtt0
refusal "two tunnels with one device_id" twin DUPLICATE_DEVICE_ID

# --- teardown ---------------------------------------------------------------

# netifd tears the protocol down and sets it up again every time the client
# exits, so a teardown that took the device away would give the tunnel a new
# interface index on every reconnect - and a socket bound to it with
# SO_BINDTODEVICE could never send again.
got=$(proto_qwdtt_teardown qwdtt0 2>&1)
check "a teardown of a tunnel that still exists" "$got" "killed: qwdtt0"

# The SNAT rule names the address the server assigned, so it is worth exactly
# as much as the tunnel is: left behind, it would rewrite the source of
# whatever took the device's name next.
got=$(proto_qwdtt_teardown gone 2>&1)
check "a teardown of a section that has been deleted" "$got" 'killed: gone
dropped: gone
removed: -f /var/run/qwdtt/gone.counters /var/run/qwdtt/gone.workers /var/run/qwdtt/gone.relays
deleted: firewall.gone_snat'

# --- the option list the uci-defaults script reads --------------------------

# /etc/uci-defaults/99-qwdtt decides whether netifd has to be restarted by
# comparing the options declared below against the ones netifd registered, and
# it reads them out of this file with a sed of its own. A declaration written
# in a shape that sed does not match would leave it comparing a shorter list
# and skipping the restart - which is the silent case it exists to catch, so
# the two readings of the same list are asserted to agree.
DECLARED=
proto_config_add_string()  { DECLARED="$DECLARED $1"; }
proto_config_add_int()     { DECLARED="$DECLARED $1"; }
proto_config_add_boolean() { DECLARED="$DECLARED $1"; }
proto_config_add_array()   { DECLARED="$DECLARED $1"; }
proto_qwdtt_init_config

scanned=$(sed -n 's/^[[:space:]]*proto_config_add_[a-z]*[[:space:]]*"\([a-z_]*\)".*/\1/p' \
	./qwdtt-client/files/qwdtt.sh)
check "the options 99-qwdtt scans for" "$(echo $scanned)" "$(echo $DECLARED)"

[ "$fail" = 0 ] || exit 1
echo "qwdtt.sh: ok"
