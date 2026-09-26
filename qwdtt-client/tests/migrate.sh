#!/bin/sh
# Exercises the /etc/config/qwdtt -> /etc/config/network conversion away from a
# router. It runs once, on a router that already carries traffic, and there is
# no second chance at it: a tunnel that comes back without its routing rule
# carries nothing, and one that comes back without its unreachable route
# releases everything to the WAN the moment it drops. So the whole resulting
# config is asserted, key by key, against a uci stubbed out over a text file.
#
# Run from the repository root: sh qwdtt-client/tests/migrate.sh
set -u

WORK=${TMPDIR:-/tmp}/qwdtt-migrate-test.$$
mkdir -p "$WORK"
trap 'rm -rf "$WORK"' EXIT

# One tunnel taking everything from the LAN and one selected by mark, which is
# the only shape the old configuration allowed. work is switched off and names a
# LAN that is not br-lan, so it also covers the two things the conversion must
# not do quietly.
QWDTT="qwdtt.qwdtt0=qwdtt
qwdtt.qwdtt0.enabled=1
qwdtt.qwdtt0.mode=rawtun
qwdtt.qwdtt0.peer_host=vpn1.example
qwdtt.qwdtt0.peer_port=56003
qwdtt.qwdtt0.password=p1
qwdtt.qwdtt0.hash=aaa bbb
qwdtt.qwdtt0.workers=18
qwdtt.qwdtt0.dns=cloudflare
qwdtt.qwdtt0.route_table=51820
qwdtt.qwdtt0.rule_priority=10000
qwdtt.qwdtt0.lan_interface=br-lan
qwdtt.work=qwdtt
qwdtt.work.enabled=0
qwdtt.work.peer_host=vpn2.example
qwdtt.work.password=p2
qwdtt.work.hash=ccc
qwdtt.work.device_id=openwrt-work
qwdtt.work.route_table=51821
qwdtt.work.rule_priority=9000
qwdtt.work.fwmark=0x100/0xff00
qwdtt.work.lan_interface=br-guest
qwdtt.work.turn_tcp=1"

# A file rather than a variable: the script ends in exit, so the test has to
# source it in a subshell, and nothing it assigned would come back.
NETWORK="$WORK/network"
: > "$NETWORK"

value_of() {
	awk -v k="$1" 'index($0, k "=") == 1 { v = substr($0, length(k) + 2) } END { print v }' "$2"
}

# The store is append-only, so the last write to a key wins - which is what uci
# would show, and what add_list leaves behind after growing a list.
dump() {
	awk -F= '{ k = $1; sub("^[^=]*=", ""); if (!(k in v)) order[++n] = k; v[k] = $0 }
	         END { for (i = 1; i <= n; i++) print order[i] "=" v[order[i]] }' "$NETWORK" | sort
}

uci() {
	[ "$1" = -q ] && shift

	case "$1" in
	get)
		case "$2" in
		qwdtt.*) got=$(printf '%s\n' "$QWDTT" | awk -v k="$2" 'index($0, k "=") == 1 { v = substr($0, length(k) + 2) } END { print v }') ;;
		*) got=$(value_of "$2" "$NETWORK") ;;
		esac
		[ -n "$got" ] || return 1
		printf '%s\n' "$got" ;;
	set)
		printf '%s\n' "$2" >> "$NETWORK" ;;
	add_list)
		# key=value, appended space-separated the way uci renders a list back.
		key=${2%%=*}
		got=$(value_of "$key" "$NETWORK")
		printf '%s\n' "$key=${got:+$got }${2#*=}" >> "$NETWORK" ;;
	show)
		printf '%s\n' "$QWDTT" ;;
	commit)
		: ;;
	esac
}

logger() { shift 2; echo "log: $*"; }
mv() { echo "moved: $1 -> $2"; }

printf 'placeholder\n' > "$WORK/qwdtt"
QWDTT_CONFIG="$WORK/qwdtt"
export QWDTT_CONFIG

log=$(. ./qwdtt-client/files/qwdtt.migrate)

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

# The whole resulting config, so an option that is quietly not carried over
# shows up here rather than on somebody's router. qwdtt0 names no device_id and
# so gets the constant the retired client used, which is the identity it has
# been reaching the server under; work named one and keeps it.
got=$(dump)
want='network.qwdtt0.device_id=openwrt
network.qwdtt0.hash=aaa bbb
network.qwdtt0.ip4table=51820
network.qwdtt0.password=p1
network.qwdtt0.peer_host=vpn1.example
network.qwdtt0.peer_port=56003
network.qwdtt0.proto=qwdtt
network.qwdtt0.workers=18
network.qwdtt0=interface
network.qwdtt0_killswitch.interface=loopback
network.qwdtt0_killswitch.metric=1000000
network.qwdtt0_killswitch.table=51820
network.qwdtt0_killswitch.target=0.0.0.0/0
network.qwdtt0_killswitch.type=unreachable
network.qwdtt0_killswitch=route
network.qwdtt0_rule.in=lan
network.qwdtt0_rule.lookup=51820
network.qwdtt0_rule.priority=9999
network.qwdtt0_rule=rule
network.qwdtt0.go_dns=cloudflare
network.work.device_id=openwrt-work
network.work.disabled=1
network.work.hash=ccc
network.work.ip4table=51821
network.work.password=p2
network.work.peer_host=vpn2.example
network.work.proto=qwdtt
network.work.turn_tcp=1
network.work=interface
network.work_killswitch.interface=loopback
network.work_killswitch.metric=1000000
network.work_killswitch.table=51821
network.work_killswitch.target=0.0.0.0/0
network.work_killswitch.type=unreachable
network.work_killswitch=route
network.work_rule.lookup=51821
network.work_rule.mark=0x100/0xff00
network.work_rule.priority=9000
network.work_rule=rule'
check "the converted config" "$got" "$(printf '%s\n' "$want" | sort)"

# dns is renamed because netifd already defines dns on every interface as the
# list of resolvers to install, so a protocol option of that name would collide
# with a core one.
if grep -q '^network\.qwdtt0\.dns=' "$NETWORK"; then
	echo "dns was copied under its old name, which collides with netifd's own"
	fail=1
fi

# Nothing may be dropped quietly. lan_interface named a device where a rule
# names a logical interface, and only the operator knows whether br-guest is
# the lan.
case $log in
*'lan_interface was br-guest'*) ;;
*)
	echo "a lan_interface that is not br-lan was converted without saying so:"
	echo "$log"
	fail=1 ;;
esac
case $log in
*'lan_interface was br-lan'*)
	echo "the default lan_interface was reported as a change:"
	echo "$log"
	fail=1 ;;
esac
case $log in
*'set aside as '*.migrated*) ;;
*)
	echo "the old config was not set aside:"
	echo "$log"
	fail=1 ;;
esac

# --- run again --------------------------------------------------------------

# A package can be reinstalled, and uci-defaults run again when it is. What
# stops a second pass is the file being gone; an interface that already exists
# is the belt to that brace, and neither may overwrite a tunnel somebody has
# edited since.
printf 'network.qwdtt0=interface\nnetwork.qwdtt0.proto=qwdtt\nnetwork.qwdtt0.peer_host=edited.example\n' > "$NETWORK"
log=$(. ./qwdtt-client/files/qwdtt.migrate)
check "an interface that already exists is left alone" \
	"$(value_of network.qwdtt0.peer_host "$NETWORK")" "edited.example"
case $log in
*'network.qwdtt0 exists already'*) ;;
*)
	echo "the collision was not reported:"
	echo "$log"
	fail=1 ;;
esac

[ "$fail" = 0 ] || exit 1
echo "qwdtt.migrate: ok"
