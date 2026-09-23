// SPDX-License-Identifier: GPL-3.0-or-later
'use strict';
'require view';
'require dom';
'require poll';
'require rpc';
'require uci';
'require network';

/* Status -> qWDTT: one row per tunnel and nothing else. Most of it comes off
   the interface, because netifd owns it and the counters it keeps are free.
   The rest comes from /var/run/qwdtt through one backend call: where the byte
   counters stood when the tunnel last came up - the TUN device outlives the
   interface on purpose, so its totals would otherwise be shown against an
   uptime that starts at the restart - how the client's sessions are doing, and
   which VK relays they are on. */

var callRuntime = rpc.declare({
	object: 'luci.qwdtt',
	method: 'getRuntime',
	expect: { '': {} }
});

/* The flush is not optional. getNetworks() hands back a state that netifd was
   asked about once and memoised, so without it the uptime and the byte
   counters freeze at whatever they were when the page opened, while the
   figures this page fetches for itself keep moving - and the two drift apart
   by exactly as long as the tab has been left open. */
function tunnels() {
	return network.flushCache().then(function() {
		return Promise.all([ network.getNetworks(), callRuntime() ]);
	}).then(function(res) {
		var runtime = res[1] || {};

		return res[0].filter(function(net) {
			return net.getProtocol() == 'qwdtt';
		}).map(function(net) {
			net.qwdttRuntime = runtime[net.getName()] || {};
			return net;
		});
	});
}

function peerOf(net) {
	var host = net._get('peer_host');
	var port = net._get('peer_port');

	if (!host)
		return '-';
	return port ? host + ':' + port : host;
}

/* What the server knows this tunnel as. Two tunnels sharing one are a single
   device to it and disconnect each other in turn, which is not a thing any
   other column would show. A tunnel written before this was required can still
   have none, and the protocol handler refuses such a tunnel rather than
   supplying one, so a blank here is the honest answer. */
function deviceIdOf(net) {
	return uci.get('network', net.getName(), 'device_id') || '-';
}

/* The client selects a hash modulo the list length, so how many there are is
   what decides how the load is spread - and one left in the list by mistake is
   invisible anywhere else on this page. */
function hashesOf(net) {
	var v = uci.get('network', net.getName(), 'hash');

	if (Array.isArray(v))
		return v.length;
	return v ? 1 : 0;
}

/* Workers arrive one at a time and drop out on their own, so the count is the
   most direct sign of how the tunnel is doing: the total is what it was asked
   for, and the gap is what VK or the relays would not give. */
function workersOf(net) {
	var r = net.qwdttRuntime || {};

	if (r.workers_total == null)
		return '-';
	return '%d / %d'.format(r.workers_active || 0, r.workers_total);
}

/* The VK relays the tunnel's sessions are on, one per line so the column stays
   one address wide however many there are. These are the relays in use, not
   the ones the credentials offered: a relay that never answered is exactly
   what a reader is trying to tell apart from one that did. */
function relaysOf(net) {
	var list = (net.qwdttRuntime || {}).relays || [];

	if (!list.length)
		return '-';
	return E('div', {}, list.map(function(addr) {
		return E('div', {}, [ addr ]);
	}));
}

/* Counted from the last time the interface came up. A device recreated since
   then counts from zero and so reads below the baseline, in which case the raw
   total is already the figure that is wanted. */
function since(now, base) {
	return (base != null && now >= base) ? now - base : now;
}

/* Shaped like the Status cell on Network -> Interfaces: L.itemlist lays out the
   label and value pairs and drops every pair whose value is null, which is how
   the counters disappear for a tunnel that has not come up yet.

   Up means the tunnel is carrying traffic, not that the client is running: the
   client creates its device and reports an address only once the server has
   answered, so everything before that is "connecting". */
function stateOf(net) {
	var device = net.getL3Device() || net.getDevice();

	if (!net.isUp()) {
		return L.itemlist(E('span'), [
			null, E('em', net.isDynamic() || net.getUptime() > 0
				? _('connecting') : _('down'))
		]);
	}

	var base = net.qwdttRuntime || {};

	return L.itemlist(E('span'), [
		_('Uptime'), '%t'.format(net.getUptime()),
		/* Named against the uptime above, because the pair is the point:
		   netifd keeps the interface up through an outage its sessions did not
		   survive, so a session uptime far shorter than the interface's is a
		   tunnel that dropped and came back without anything else noticing. */
		_('Session uptime'), base.connected_for != null
			? '%t'.format(base.connected_for) : null,
		/* As a string, so that a tunnel which has not dropped once reads "0"
		   rather than dropping out of the list and leaving no way to tell a
		   steady tunnel from one the backend said nothing about. */
		_('Reconnects'), base.reconnects != null ? String(base.reconnects) : null,
		_('IPv4'), (net.getIPAddrs() || [])[0] || null,
		_('RX'), device ? '%.2mB (%d %s)'.format(
			since(device.getRXBytes(), base.rx_bytes),
			since(device.getRXPackets(), base.rx_packets), _('Pkts.')) : null,
		_('TX'), device ? '%.2mB (%d %s)'.format(
			since(device.getTXBytes(), base.tx_bytes),
			since(device.getTXPackets(), base.tx_packets), _('Pkts.')) : null
	]);
}

/* Nothing here is editable, and a page of read-only rows gives no clue where
   the settings behind them live. One msgid with a placeholder rather than
   three fragments around the link, so a translator keeps the word order. */
function whereToConfigure() {
	var parts = _('Tunnels are configured on %s: add an interface whose protocol is qWDTT, or open one that already exists to change it. This page only reports.')
		.split('%s');

	return E('div', { 'class': 'cbi-section-descr' }, [
		parts[0],
		E('a', { 'href': L.url('admin/network/network') },
			[ _('Network -> Interfaces') ]),
		parts[1]
	]);
}

function renderTable(nets) {
	var table = new L.ui.Table(
		[ _('Tunnel'), _('Peer'), _('Device ID'), _('Hashes'),
		  _('Active Workers'), _('TURN relays'), _('Status') ],
		{ id: 'qwdtt-tunnels' },
		E('em', [ _('No qWDTT interfaces are configured.') ])
	);

	table.update(nets.map(function(net) {
		return [
			net.getName(),
			peerOf(net),
			deviceIdOf(net),
			hashesOf(net),
			workersOf(net),
			relaysOf(net),
			stateOf(net)
		];
	}));

	return table.render();
}

return view.extend({
	/* network.getNetworks() reads the system feature probe synchronously and
	   throws if it has not resolved, which on a router with no wifi is where a
	   view that asks for networks first thing lands. Waiting for it here costs
	   one cached call and is what keeps this page working where the stock
	   Interfaces page does not. */
	load: function() {
		return L.probeSystemFeatures().then(tunnels);
	},

	render: function(nets) {
		poll.add(function() {
			return tunnels().then(function(fresh) {
				var into = document.getElementById('qwdtt-table');
				if (into)
					dom.content(into, renderTable(fresh));
			});
		}, 5);

		return E([], [
			E('h2', [ _('qWDTT') ]),
			whereToConfigure(),
			E('div', { 'id': 'qwdtt-table' }, [ renderTable(nets) ])
		]);
	},

	handleReset: null,
	handleSaveApply: null,
	handleSave: null
});
