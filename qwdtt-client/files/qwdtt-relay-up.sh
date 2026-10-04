#!/bin/sh
# Run by `qwdtt-client -netifd` in wireguard mode once the relay is listening
# on its local port. That endpoint is the whole of what the interface provides,
# so this reports it up and nothing else: there is no address to carry, and
# never will be.
#
# Separate from qwdtt-up.sh, whose job is the address, the routes, the
# resolvers and the SNAT a RAW-IP tunnel gets from the server - none of which
# exist here. The device is the placeholder the protocol handler made before
# starting the client, because netifd refuses an update naming a device that
# is not there.

[ -n "$INTERFACE" ] && [ -n "$DEVICE" ] || {
	echo "qwdtt-relay-up: INTERFACE and DEVICE must be set" >&2
	exit 1
}

. /lib/functions.sh
. /lib/netifd/netifd-proto.sh

proto_init_update "$DEVICE" 1
proto_send_update "$INTERFACE"
