#!/bin/sh
# Run by `qwdtt-client -netifd` when the tunnel cannot come up and the reason
# is worth putting in front of somebody. netifd holds the code against the
# interface, so Network -> Interfaces says why instead of leaving it to the
# log.
#
# BLOCK decides whether the interface stops retrying with it. A refusal the
# server has stated - a password it will not take, a call hash it says is
# dead - is worth blocking on: retrying gets the same answer and asks VK for
# call credentials on the way, and correcting the interface is what starts it
# again. A silence is not: the server may simply be down, and an interface
# that blocked on that would stay down after the server came back.

[ -n "$INTERFACE" ] && [ -n "$ERROR" ] || {
	echo "qwdtt-error: INTERFACE and ERROR must be set" >&2
	exit 1
}

. /lib/functions.sh
. /lib/netifd/netifd-proto.sh

proto_notify_error "$INTERFACE" "$ERROR"
[ "${BLOCK:-1}" = 0 ] || proto_block_restart "$INTERFACE"
