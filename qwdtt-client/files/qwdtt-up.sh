#!/bin/sh
# Run by `qwdtt-client -netifd` once the server has answered with the tunnel's
# address, resolvers and MTU. netifd cannot be given them when the interface is
# brought up, because they arrive over the tunnel's own control connection -
# the same shape as a DHCP lease, and told to netifd the same way.

[ -n "$INTERFACE" ] && [ -n "$DEVICE" ] && [ -n "$IPADDR" ] || {
	echo "qwdtt-up: INTERFACE, DEVICE and IPADDR must be set" >&2
	exit 1
}

. /lib/functions.sh
. /lib/netifd/netifd-proto.sh

config_load network
config_get_bool defaultroute "$INTERFACE" defaultroute 1
config_get_bool peerdns "$INTERFACE" peerdns 1

[ -z "$MTU" ] || ip link set dev "$DEVICE" mtu "$MTU"

proto_init_update "$DEVICE" 1
# /16, not /32: the server hands every peer an address out of one 10.x.0.0/16
# and expects them to reach each other without going back through it.
proto_add_ipv4_address "$IPADDR" 16

if [ "$defaultroute" = 1 ]; then
	proto_add_ipv4_route 0.0.0.0 0
fi
if [ "$peerdns" = 1 ]; then
	for server in $DNS; do
		proto_add_dns_server "$server"
	done
fi

# What the firewall zone would otherwise get from masquerading, without the
# notifier that comes with it - see the reasoning in the uci-defaults script
# that writes the zone. Rewritten only when the server hands out a different
# address, because reloading the firewall on every reconnect would be a cost
# paid for nothing.
snat="firewall.${INTERFACE}_snat"
if [ "$(uci -q get "$snat.snat_ip")" != "$IPADDR" ]; then
	uci -q batch <<-EOF
		set $snat=nat
		set $snat.name='${INTERFACE}-snat'
		set $snat.family='ipv4'
		set $snat.src='qwdtt'
		set $snat.device='$DEVICE'
		set $snat.target='SNAT'
		set $snat.snat_ip='$IPADDR'
	EOF
	uci commit firewall
	[ ! -x /etc/init.d/firewall ] || /etc/init.d/firewall reload >/dev/null 2>&1
fi

# The zone matches qwdtt-named devices by its device='qwdtt+' wildcard, so a
# conventionally named tunnel needs nothing here. One named off that pattern
# reaches the zone only through its network list, which files/qwdtt.defaults
# fills at install; a tunnel created later would otherwise sit in no zone, its
# LAN forwarding and SNAT both silently missing.
case "$INTERFACE" in
qwdtt*) ;;
*)
	in_zone=0
	for n in $(uci -q get firewall.qwdtt.network 2>/dev/null); do
		[ "$n" = "$INTERFACE" ] && in_zone=1
	done
	if [ "$in_zone" = 0 ]; then
		uci -q add_list "firewall.qwdtt.network=$INTERFACE"
		uci commit firewall
		[ ! -x /etc/init.d/firewall ] || /etc/init.d/firewall reload >/dev/null 2>&1
	fi ;;
esac

# The device outlives the interface, so its counters carry on across a restart
# while netifd starts the uptime again from this update - the two then describe
# different spans and read as a contradiction. Recording the counters at the
# same moment is what lets the status page show both since the same instant.
mkdir -p /var/run/qwdtt
stats="/sys/class/net/$DEVICE/statistics"
echo "$(cat "$stats/rx_bytes") $(cat "$stats/rx_packets")" \
     "$(cat "$stats/tx_bytes") $(cat "$stats/tx_packets")" \
	> "/var/run/qwdtt/$INTERFACE.counters"

proto_send_update "$INTERFACE"
