#!/bin/sh
# Run by `qwdtt-client -netifd` when it meets a refusal that reconnecting
# cannot clear - a password the server will not take, or a call hash it says is
# dead. netifd puts the code against the interface, so Network -> Interfaces
# says why the tunnel is not up instead of leaving it to the log.
#
# The restart is blocked with it: retrying a rejected password gets the same
# answer and keeps asking VK for call credentials on the way. Correcting the
# interface and applying is what starts it again.

[ -n "$INTERFACE" ] && [ -n "$ERROR" ] || {
	echo "qwdtt-error: INTERFACE and ERROR must be set" >&2
	exit 1
}

. /lib/functions.sh
. /lib/netifd/netifd-proto.sh

proto_notify_error "$INTERFACE" "$ERROR"
proto_block_restart "$INTERFACE"
