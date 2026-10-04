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
		firewall.qwdtt.network) echo "${ZONE_NETWORKS:-}" ;;
		esac ;;
	delete) echo "deleted: $2" ;;
	del_list) echo "del_list: $2" ;;
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
# The handler asks this directory which tunnels are running. Pointed at a
# scratch copy so the tests can say one is without a router under them.
QWDTT_RUN_DIR=$(mktemp -d)
# command, because rm is stubbed below to report rather than delete.
trap 'command rm -rf "$QWDTT_RUN_DIR"' EXIT
export QWDTT_RUN_DIR
. ./qwdtt-client/files/qwdtt.sh

# There is no /sys here, and what the checks below are about is when the device
# is dropped rather than how it is recognised.
drop_device() { echo "dropped: $1"; }

# Nor any netlink for the placeholder a wireguard-mode tunnel is reported up
# on: what matters here is whether it is asked for, what is done when it
# cannot be made, and that the interface is only reported up after it exists.
relay_device() { echo "device: $1"; }
proto_init_update() { echo "init_update: $1"; }
proto_send_update() { echo "send_update: $1"; }

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

# The password is in the environment and nowhere on the command line:
# /proc/<pid>/cmdline is world-readable and /proc/<pid>/environ is not.
got=$(proto_qwdtt_setup qwdtt0 2>&1)
want='export: INTERFACE=qwdtt0
export: TZ=Europe/Moscow
export: QWDTT_PASSWORD=p1
run: /usr/bin/qwdtt-client -netifd -mode rawtun -tun-name qwdtt0 -peer vpn1.example:56003 -vk aaa,bbb -device-id openwrt-qwdtt0 -n 9 -go-dns yandex -obfs audio -captcha-mode auto -vk-auth anonymous -vk-anon-path vkcalls'
check "a complete tunnel" "$got" "$want"

# Every default here is one the handler supplies rather than the client, so a
# tunnel that sets none of them still runs with the values the documentation
# names.
SECTION=work
got=$(proto_qwdtt_setup work 2>&1)
want='export: INTERFACE=work
export: TZ=Europe/Moscow
export: QWDTT_PASSWORD=p2
run: /usr/bin/qwdtt-client -netifd -mode rawtun -tun-name work -peer vpn2.example:56003 -vk ccc -device-id openwrt-work -n 9 -go-dns yandex -obfs audio -captcha-mode auto -vk-auth anonymous -vk-anon-path vkcalls'
check "no peer_port falls back to 56003, and every optional value left unset" "$got" "$want"

# The three that are appended rather than always passed. -notls and -turn-tcp
# are bare flags, which Go's flag package reads as true, so passing them with a
# 0 would switch them on. The VK application pair goes the way the password
# does, and for the same reason.
set_cfg work.vk_creds_file '/etc/qwdtt/creds with a space.json'
set_cfg work.no_dtls 1
set_cfg work.turn_tcp 0
set_cfg work.vk_client_id 123456
set_cfg work.vk_client_secret 'sekret with a space'
got=$(proto_qwdtt_setup work 2>&1)
case $got in
*"-vk-anon-path vkcalls -vk-creds-file /etc/qwdtt/creds with a space.json -notls") ;;
*)
	echo "the optional flags:"
	echo "--- got"
	echo "$got"
	fail=1 ;;
esac
case $got in
*"export: QWDTT_VK_CLIENT_ID=123456"*"export: QWDTT_VK_CLIENT_SECRET=sekret with a space"*) ;;
*)
	echo "the VK application pair was not exported:"
	echo "$got"
	fail=1 ;;
esac
# The run line alone: the exports above it are allowed to carry them.
run=$(echo "$got" | sed -n 's/^run: //p')
case $run in
*-password*|*-vk-client*|*sekret*)
	echo "a secret reached the command line:"
	echo "$run"
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

SECTIONS="$SECTIONS noserver nohash nopass waytoolongfortun mainte twin loose nodevice sleeper waker"

set_cfg noserver.proto qwdtt
set_cfg noserver.ip4table 51822
set_cfg noserver.hash ddd
refusal "a tunnel with no peer_host" noserver MISSING_PEER_HOST

set_cfg nohash.proto qwdtt
set_cfg nohash.ip4table 51823
set_cfg nohash.peer_host vpn3.example
refusal "a tunnel with no hashes" nohash MISSING_HASH

# Without this the client exits at once and netifd starts it again at once,
# with no backoff and nothing on the interface page: measured at 349 starts
# in thirty seconds.
set_cfg nopass.proto qwdtt
set_cfg nopass.ip4table 51827
set_cfg nopass.peer_host vpn9.example
set_cfg nopass.hash jjj
set_cfg nopass.device_id openwrt-nopass
refusal "a tunnel with no password" nopass MISSING_PASSWORD

set_cfg waytoolongfortun.proto qwdtt
set_cfg waytoolongfortun.ip4table 51824
set_cfg waytoolongfortun.peer_host vpn4.example
set_cfg waytoolongfortun.password p4
set_cfg waytoolongfortun.hash eee
refusal "a name no interface can have" waytoolongfortun NAME_TOO_LONG

# The one that would otherwise fail silently and in the wrong direction: the
# client reaches VK over the WAN, so its own transport would be routed into
# the tunnel it is carrying.
set_cfg mainte.proto qwdtt
set_cfg mainte.peer_host vpn5.example
set_cfg mainte.password p5
set_cfg mainte.hash fff
refusal "a default route with no table to put it in" mainte MISSING_IP4TABLE

# Unless there is no default route to misplace, which is the split-tunnel case:
# the operator writes the routes and the table is theirs to choose.
set_cfg loose.proto qwdtt
set_cfg loose.peer_host vpn6.example
set_cfg loose.password p6
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
set_cfg nodevice.password p8
set_cfg nodevice.hash iii
refusal "a tunnel with no device_id" nodevice MISSING_DEVICE_ID

# Quieter and worse than a table collision: the server takes two tunnels that
# share a device_id for one device and disconnects them in turn, which reads as
# a flapping link rather than as a configuration mistake.
set_cfg twin.proto qwdtt
set_cfg twin.ip4table 51825
set_cfg twin.peer_host vpn7.example
set_cfg twin.password p7
set_cfg twin.hash hhh
set_cfg twin.device_id openwrt-qwdtt0
refusal "two tunnels with one device_id" twin DUPLICATE_DEVICE_ID

# A disabled tunnel never connects, so its device_id must not block a new one.
# The migration makes this reachable: it writes device_id=openwrt for every
# legacy section that had none, so an enabled and a disabled tunnel can end up
# sharing one, and then the enabled one has to come up regardless.
set_cfg sleeper.proto qwdtt
set_cfg sleeper.ip4table 51828
set_cfg sleeper.peer_host vpnA.example
set_cfg sleeper.password pA
set_cfg sleeper.hash kkk
set_cfg sleeper.device_id openwrt-shared
set_cfg sleeper.disabled 1
set_cfg waker.proto qwdtt
set_cfg waker.ip4table 51829
set_cfg waker.peer_host vpnB.example
set_cfg waker.password pB
set_cfg waker.hash lll
set_cfg waker.device_id openwrt-shared
SECTION=waker
got=$(proto_qwdtt_setup waker 2>&1 | sed -n 's/^run: //p')
case $got in
/usr/bin/qwdtt-client*) ;;
*)
	echo "an enabled tunnel was refused for a device_id only a disabled one holds: ${got:-nothing ran}"
	fail=1 ;;
esac

# Absent unless set: an empty -rate-down or -rate-up would be a limit of
# nothing rather than no limit.
set_cfg work.rate_down 400
set_cfg work.rate_up 150
SECTION=work
case $(proto_qwdtt_setup work 2>&1 | sed -n 's/^run: //p') in
*"-rate-down 400"*"-rate-up 150"*) ;;
*)
	echo "the per-session limits were not passed, or not in that order:"
	proto_qwdtt_setup work 2>&1 | sed -n 's/^run: //p'
	fail=1 ;;
esac
saved=$CFG
CFG=$(echo "$CFG" | grep -v '^work\.rate_')
case $(proto_qwdtt_setup work 2>&1 | sed -n 's/^run: //p') in
*-rate-*) echo "an unset limit was passed as a limit of nothing"; fail=1 ;;
esac
CFG=$saved

# --- wireguard mode ---------------------------------------------------------

# The other shape entirely: no TUN device of its own, a local endpoint instead,
# and the server's main listener rather than the RAW one.
SECTIONS="$SECTIONS wgt wgtwin"
set_cfg wgt.proto qwdtt
set_cfg wgt.mode wireguard
set_cfg wgt.peer_host vpnW.example
set_cfg wgt.password pW
set_cfg wgt.device_id openwrt-wgt
set_cfg wgt.hash www
SECTION=wgt
got=$(proto_qwdtt_setup wgt 2>&1)
case $got in
*"-mode vpn -listen 127.0.0.1:9000 -peer vpnW.example:56000"*) ;;
*)
	echo "the wireguard command line:"
	echo "$got" | sed -n 's/^run: //p'
	fail=1 ;;
esac
# In that order: the client reports the interface up once its relay is
# listening, and netifd refuses an update naming a device that does not exist,
# so the placeholder has to be made before the client is started.
case $got in
*"device: wgt"*"run: /usr/bin/qwdtt-client"*) ;;
*)
	echo "the placeholder was not made before the client was started:"
	echo "$got"
	fail=1 ;;
esac

# And the handler reports nothing up itself: it returns as soon as the client
# is started, which says nothing about whether the relay took its port.
case $got in
*init_update*|*send_update*)
	echo "the handler reported the interface up instead of leaving it to the client:"
	echo "$got"
	fail=1 ;;
esac

# A placeholder that cannot be made stops the setup before the client is
# started, so there is nothing running to unwind.
relay_device() { return 1; }
got=$(proto_qwdtt_setup wgt 2>&1)
relay_device() { echo "device: $1"; }
case $got in
*"refused: NO_RELAY_DEVICE"*) ;;
*)
	echo "a placeholder that could not be made was not reported: $got"
	fail=1 ;;
esac
case $got in
*"run: "*)
	echo "the client was started despite having no placeholder to report up on:"
	echo "$got"
	fail=1 ;;
esac

# Two relays on one local port do not collide: SO_REUSEADDR lets the second
# bind the address the first holds, both tunnels come up, and the packets are
# split between them. Nothing at runtime can see that, so the configuration is
# what has to be refused.
set_cfg wgtwin.proto qwdtt
set_cfg wgtwin.mode wireguard
set_cfg wgtwin.peer_host vpnX.example
set_cfg wgtwin.password pX
set_cfg wgtwin.device_id openwrt-wgtwin
set_cfg wgtwin.hash xxx
refusal "two relays on one local port" wgtwin DUPLICATE_LISTEN_PORT

# But a tunnel the operator has parked holds no port, so it must not keep a
# running one off it: whichever starts first claims the port, and the other is
# refused on its way up.
set_cfg wgt.auto 0
SECTION=wgtwin
got=$(proto_qwdtt_setup wgtwin 2>&1 | sed -n 's/^run: //p')
case $got in
/usr/bin/qwdtt-client*) ;;
*)
	echo "a parked tunnel reserved the port against a running one: ${got:-nothing ran}"
	fail=1 ;;
esac

# Parking one in the configuration does not stop a relay that is already
# running, and that one does still hold the port. Taking the configuration at
# its word let the second bind the same address - SO_REUSEADDR allows it - and
# split the packets with nothing downstream able to see it.
: > "$QWDTT_RUN_DIR/wgt.wg"
refusal "a parked but still running relay keeps its port" wgtwin DUPLICATE_LISTEN_PORT
command rm -f "$QWDTT_RUN_DIR/wgt.wg"

# And the same for one switched off rather than parked.
set_cfg wgt.auto 1
set_cfg wgt.disabled 1
: > "$QWDTT_RUN_DIR/wgt.workers"
refusal "a disabled but still running relay keeps its port" wgtwin DUPLICATE_LISTEN_PORT
command rm -f "$QWDTT_RUN_DIR/wgt.workers"
set_cfg wgt.disabled 0

set_cfg wgt.auto 1

# A password in /etc/config/network reaches the client through netifd's own
# argv and through ubus, both readable while the interface comes up. Pointing
# at the client's own file instead leaves only the path in those places, so
# config_file has to be accepted in place of a password rather than alongside
# one - and refused clearly when it names a file that cannot be read, since
# the client exits on that and netifd would restart it at once.
set_cfg nopass.config_file "$QWDTT_RUN_DIR/creds.json"
: > "$QWDTT_RUN_DIR/creds.json"
SECTION=nopass
got=$(proto_qwdtt_setup nopass 2>&1)
case $got in
*"run: "*"-config $QWDTT_RUN_DIR/creds.json"*) ;;
*)
	echo "a tunnel with a config file instead of a password did not start:"
	echo "$got"
	fail=1 ;;
esac
case $got in
*QWDTT_PASSWORD*)
	echo "the password was still exported when the secrets live in a file:"
	echo "$got"
	fail=1 ;;
esac
command rm -f "$QWDTT_RUN_DIR/creds.json"
refusal "a config file that cannot be read" nopass UNREADABLE_CONFIG_FILE

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
#
# The run files go by glob rather than by name. Nothing here has created any,
# so it reaches rm unexpanded, which -f makes a no-op; what is being checked
# is that the teardown asks for all of them rather than the three it used to
# list, two of which had since been joined by others - including the one
# holding the WireGuard private key the server issued.
got=$(ZONE_NETWORKS="lan2 wan" proto_qwdtt_teardown gone 2>&1)
check "a teardown of a section that has been deleted" "$got" "killed: gone
dropped: gone
removed: -f $QWDTT_RUN_DIR/gone.*
deleted: firewall.gone_snat"

# An off-pattern name reaches the zone through its network list, so a deleted
# tunnel takes its entry with it and leaves the others.
got=$(ZONE_NETWORKS="lan2 gone wan" proto_qwdtt_teardown gone 2>&1)
check "a deleted tunnel leaves the zone's network list" "$got" "killed: gone
dropped: gone
removed: -f $QWDTT_RUN_DIR/gone.*
deleted: firewall.gone_snat
del_list: firewall.qwdtt.network=gone"

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
