#!/bin/sh
# Exercises the sweep in files/qwdtt.defaults that removes routing a deleted
# tunnel left behind.
#
# It runs on every install and upgrade, unattended, and it deletes sections of
# /etc/config/network - so the two ways it can be wrong are both expensive. Too
# eager and it takes a route the operator wrote; too shy and the router keeps
# refusing whatever the orphaned rule matches, which is the fault it exists to
# clear. Both directions are asserted here against a uci stubbed over a file.
#
# Run from the repository root: sh qwdtt-client/tests/defaults.sh
set -u

WORK=${TMPDIR:-/tmp}/qwdtt-defaults-test.$$
mkdir -p "$WORK"
trap 'rm -rf "$WORK"' EXIT

STORE="$WORK/store"

# network.<section>=<type> for a section, network.<section>.<option>=<value>
# for an option, which is the shape `uci show` prints and the shape the sweep
# reads back.
cat > "$STORE" <<'EOF'
network.loopback=interface
network.qwdtt0=interface
network.qwdtt0.proto=qwdtt
network.qwdtt0_rule=rule
network.qwdtt0_rule.lookup=51820
network.qwdtt0_killswitch=route
network.qwdtt0_killswitch.interface=loopback
network.qwdtt0_killswitch.type=unreachable
network.qwdtt0_killswitch.table=51820
network.qwdtt1_rule=rule
network.qwdtt1_rule.lookup=51821
network.qwdtt1_killswitch=route
network.qwdtt1_killswitch.interface=loopback
network.qwdtt1_killswitch.type=unreachable
network.qwdtt1_killswitch.table=51821
network.qwdtt2_killswitch=route
network.qwdtt2_killswitch.interface=loopback
network.qwdtt2_killswitch.type=unreachable
network.qwdtt2_killswitch.table=51822
network.mine_killswitch=route
network.mine_killswitch.interface=wan
network.mine_killswitch.type=unreachable
EOF

value_of() {
	awk -v k="$1" 'index($0, k "=") == 1 { print substr($0, length(k) + 2); exit }' "$STORE"
}

uci() {
	[ "$1" = -q ] && shift

	case "$1" in
	get)
		got=$(value_of "$2")
		[ -n "$got" ] || return 1
		printf '%s\n' "$got" ;;
	delete)
		# A section takes its options with it, the way uci does.
		grep -v "^$2=" "$STORE" | grep -v "^$2\." > "$STORE.new"
		mv "$STORE.new" "$STORE" ;;
	set|add_list)
		printf '%s\n' "$2" >> "$STORE" ;;
	show)
		grep "^$2\." "$STORE" ;;
	batch)
		cat >/dev/null ;;
	commit)
		: ;;
	esac
	return 0
}

logger() { :; }

# The script ends in exit, so it has to run in a subshell; the store is a file
# for the same reason. Absolute paths to /etc/init.d are not stubbed and simply
# fail, which is what the sweep does when the network is not up either.
( . ./qwdtt-client/files/qwdtt.defaults ) >/dev/null 2>&1

fail=0
gone() {
	if [ -n "$(value_of "$2")" ]; then
		echo "$1: $2 is still there"
		fail=1
	fi
}
kept() {
	if [ -z "$(value_of "$2")" ]; then
		echo "$1: $2 was removed"
		fail=1
	fi
}

# network.qwdtt1 does not exist, so both sections name nothing. Left alone, the
# rule looks up a table whose only route refuses everything.
gone "an orphaned kill switch" network.qwdtt1_killswitch
gone "an orphaned rule" network.qwdtt1_rule

# The same with no rule beside it: the kill switch alone is what does the harm.
gone "an orphaned kill switch with no rule" network.qwdtt2_killswitch

# network.qwdtt0 is a live interface, so its routing is in use.
kept "a live tunnel's kill switch" network.qwdtt0_killswitch
kept "a live tunnel's rule" network.qwdtt0_rule

# Named like ours and orphaned, but not the shape this package writes, so it
# belongs to whoever did write it.
kept "an unreachable route that is not ours" network.mine_killswitch

[ "$fail" = 0 ] || exit 1
echo "qwdtt.defaults: ok"
