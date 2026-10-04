// SPDX-License-Identifier: GPL-3.0-or-later
'use strict';
'require view';
'require dom';
'require poll';
'require rpc';
'require uci';
'require fs';
'require ui';
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

		var all = res[0];

		return all.filter(function(net) {
			return net.getProtocol() == 'qwdtt';
		}).map(function(net) {
			net.qwdttRuntime = runtime[net.getName()] || {};
			/* The WireGuard interface a wireguard-mode tunnel is carried by,
			   kept here because it is where that tunnel's address and its byte
			   counters actually live. Taken from the list already fetched
			   rather than asked for again. */
			net.qwdttCarrier = null;
			if (modeOf(net) == 'wireguard') {
				var name = checkDeviceOf(net);
				if (name)
					net.qwdttCarrier = all.filter(function(n) {
						return n.getName() == name;
					})[0] || null;
			}
			return net;
		});
	});
}

/* Kept in step with QWDTT_PEER_PORT_* in the protocol handler, which is what
   applies them, and with PEER_PORT in protocol/qwdtt.js, which offers them. */
var PEER_PORT = { rawtun: '56003', wireguard: '56000' };
var RELAY_PORT = '9000';

function modeOf(net) {
	return uci.get('network', net.getName(), 'mode') || 'rawtun';
}

/* The port is filled in when the tunnel leaves it unset, because the column is
   there to say where this tunnel goes and the handler's fallback is part of
   that - and it is not one number but one per mode. */
function peerOf(net) {
	var host = net._get('peer_host');
	var port = net._get('peer_port') || PEER_PORT[modeOf(net)];

	if (!host)
		return '-';
	return host + ':' + port;
}

/* What a WireGuard-mode tunnel exists to provide: the address a WireGuard
   interface is pointed at as its peer. Nothing else on the router says what it
   ended up being, and the tunnel carries no traffic of its own to show
   instead. */
function endpointOf(net) {
	if (modeOf(net) != 'wireguard')
		return null;
	return '127.0.0.1:%s'.format(
		uci.get('network', net.getName(), 'listen_port') || RELAY_PORT);
}

/* The address the tunnel answers on. A RAW-IP tunnel holds it itself; a
   wireguard-mode one never will - the placeholder its interface is reported up
   on carries nothing - so the address is the one the server issued to the
   WireGuard interface above it, which is what the far end actually sees. */
function addressOf(net) {
	if (modeOf(net) != 'wireguard')
		return (net.getIPAddrs() || [])[0] || null;

	var carrier = checkDeviceOf(net);
	if (!carrier)
		return null;

	var addrs = uci.get('network', carrier, 'addresses');
	if (Array.isArray(addrs))
		addrs = addrs[0];
	return addrs || null;
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

/* VK puts a captcha in front of the client now and then, and the solver answers
   it without anything on the router saying so: a solved one leaves the tunnel
   working, and one it cannot answer looks from here like credentials that never
   arrived. Null until the first is asked for, so the line stays out of the way
   on a tunnel that has never met one. */
function captchasOf(net) {
	var r = net.qwdttRuntime || {};

	if (r.captcha_faced == null)
		return null;
	return '%d / %d'.format(r.captcha_solved || 0, r.captcha_faced);
}

/* The one figure on this page taken from traffic rather than from a session
   being established, and from the inbound direction alone: what the reader
   wants to know is whether the far end is still delivering, and bytes this
   router sent say nothing about that. Everything above it counts from a worker
   reporting ready, so a server that accepts the sessions and forwards nothing
   leaves the worker count full and the session clock rising: a tunnel that
   reads as eight hours healthy while nothing has crossed it. This is what
   tells those apart.

   It used to be unreadable on a quiet tunnel, because an idle one and a dead
   one both showed the figure climbing. It no longer is: after half a minute
   of nothing arriving the client sends an echo through the tunnel to the
   server and the answer lands here like any other traffic, so on a tunnel
   that is merely unused this now sits under a minute. Climbing past that
   means the tunnel is not answering, and the client gives it up at five
   minutes and has netifd rebuild it - so a reading much above that is the
   page having caught it mid-rebuild. */
function idleOf(net) {
	var idle = (net.qwdttRuntime || {}).idle_for;

	if (idle == null)
		return null;
	if (idle < 0)
		return _('nothing yet');
	return _('%s ago').format('%t'.format(idle));
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

   For a RAW-IP tunnel, up means it is carrying traffic rather than that the
   client is running: the client creates its device and reports an address only
   once the server has answered, so everything before that is "connecting". A
   WireGuard-mode one has no address to wait for and is reported up as soon as
   it starts, so there "up" says the relay is listening and the rows below -
   the sessions, what last arrived, the worker count beside them - are what say
   whether anything is getting through. */
function stateOf(net) {
	/* Whichever device actually carries the traffic. For a RAW-IP tunnel that
	   is the interface itself; for a wireguard-mode one the placeholder it is
	   reported up on carries nothing, and the WireGuard interface above it
	   does. Reading the placeholder would show a steady zero beside a relay
	   that is working. */
	var carrier = net.qwdttCarrier;
	var device = modeOf(net) == 'rawtun'
		? (net.getL3Device() || net.getDevice())
		: (carrier ? (carrier.getL3Device() || carrier.getDevice()) : null);

	if (!net.isUp()) {
		/* Whatever netifd was told about why. Without this the column said
		   "connecting" and nothing else while a tunnel sat refused, and the
		   reason - which the interface page already had - was a page away
		   from the page somebody opens to ask what is wrong. */
		var errors = net.getErrors() || [];
		if (errors.length) {
			return E('span', {}, errors.map(function(err) {
				return E('div', { 'class': 'qwdtt-error' }, [
					E('strong', {}, [ _('Error') + ': ' ]), err
				]);
			}));
		}
		return L.itemlist(E('span'), [
			null, E('em', net.isDynamic() || net.getUptime() > 0
				? _('connecting') : _('down'))
		]);
	}

	var base = net.qwdttRuntime || {};

	return L.itemlist(E('span'), [
		/* Not "uptime": this counts from the moment the tunnel last had no
		   session at all, which is a fact about sessions being established and
		   nothing more. Read as uptime it says the tunnel has been working that
		   long, and it will say so just as readily while the server accepts
		   every session and forwards nothing. The interface uptime that used to
		   sit above it only doubled the claim, so it is gone; netifd shows it
		   on Network -> Interfaces for anyone who wants it. */
		_('Sessions up for'), base.connected_for != null
			? '%t'.format(base.connected_for) : null,
		/* Directly beneath, because the pair is the point: sessions long
		   established with nothing arriving is the state neither figure shows
		   on its own. */
		_('Last received'), idleOf(net),
		/* As a string, so that a tunnel which has not dropped once reads "0"
		   rather than dropping out of the list and leaving no way to tell a
		   steady tunnel from one the backend said nothing about. */
		_('Reconnects'), base.reconnects != null ? String(base.reconnects) : null,
		/* Only once VK has asked for one. A tunnel that has never seen a
		   captcha has nothing to say here, and a nought would read as a
		   reassurance rather than as silence. Solved over faced, because the
		   pair is what matters: the same count on both sides is the solver
		   keeping up, and a gap is what leaves the tunnel waiting. */
		_('Captchas'), captchasOf(net),
		_('IPv4'), addressOf(net),
		_('Local endpoint'), endpointOf(net),
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

/* ---- checking a tunnel carries traffic -----------------------------------
   Below the table rather than in it, laid out the way Network -> Diagnostics
   lays out the same question, because it is the same question asked of one
   interface instead of the router. Keeping it out of the table also keeps it
   out of the way of the poll, which rebuilds those rows from scratch every few
   seconds and would take the address being typed and the output being read
   with the old ones.

   Folded away until asked for: the page is read far more often than a tunnel
   is tested, and an empty box with a button is worth less than the room it
   takes. <details> rather than a widget of LuCI's, which has none for this.
   The heading is left at the size of the other headings and keeps the
   disclosure triangle, which is the part that says it opens.

   Drawn as a panel of its own, because cbi-section carries no background in
   these themes and a bare heading under a table reads as part of the table.
   The colours come from the theme's own variables rather than a literal white:
   the same names resolve to white on the light theme and to the dark panel
   colour on the dark one, where white would glare. Full width and the table's
   own bottom margin, so it lines up with what it sits under.

   The tunnel is chosen rather than assumed: a page with two of them has two
   answers, and -I is the whole point of asking here instead of over ssh. */
var CHECK_TARGETS = {
	'1.1.1.1': '1.1.1.1 (Cloudflare)',
	'8.8.8.8': '8.8.8.8 (Google)',
	'77.88.8.8': '77.88.8.8 (Yandex)'
};

var WG_PEER = 'wireguard_';

/* Which interface a tunnel is checked through. A RAW-IP one carries the
   traffic itself, so it is its own answer. A wireguard-mode one carries none:
   it is a relay on 127.0.0.1, and what crosses it belongs to the WireGuard
   interface pointed at that port. Asking the relay would prove nothing - its
   device is the placeholder netifd needs to call the interface up, with no
   address and no routes - so the question is passed to the interface above
   it.

   Which one that is does not have to be guessed at. A WireGuard peer section
   is typed wireguard_<interface>, so a peer whose endpoint is this tunnel's
   own local port names the interface sitting on top of it. */
function checkDeviceOf(net) {
	if (modeOf(net) == 'rawtun')
		return net.getName();

	var port = uci.get('network', net.getName(), 'listen_port') || RELAY_PORT;
	var found = null;

	uci.sections('network', null, function(section) {
		var type = section['.type'] || '';

		/* A disabled peer is not configured at all, so matching one would
		   name an interface that carries nothing and hide the real match. */
		if (found || section.disabled == '1' || type.indexOf(WG_PEER) !== 0)
			return;
		if (section.endpoint_host != '127.0.0.1')
			return;
		if (String(section.endpoint_port || '') != String(port))
			return;
		found = type.substring(WG_PEER.length);
	});

	return found;
}

function checkSection(nets) {
	if (!nets.length)
		return E([]);

	/* A wireguard-mode tunnel with nothing on it yet has nothing to ask, and
	   it is the one case where saying so beats offering a control that cannot
	   answer. */
	var checkable = [];
	nets.forEach(function(net) {
		var device = checkDeviceOf(net);
		if (device)
			checkable.push({ net: net, device: device });
	});

	if (!checkable.length)
		return E('div', { 'class': 'cbi-section' }, [
			E('h3', {}, [ _('Check tunnel') ]),
			E('div', { 'class': 'cbi-section-descr' }, [
				_('A WireGuard-mode tunnel is checked through the WireGuard interface that uses it, and none is configured yet: add one whose peer endpoint is this tunnel local endpoint, and it will be offered here.')
			])
		]);

	var picker = E('select', {
			'id': 'qwdtt-check-iface',
			'class': 'cbi-input-select',
			'style': 'margin:5px 0'
		},
		checkable.map(function(entry) {
			var name = entry.net.getName();
			/* The interface name, not the device's. The TUN device is named
			   after the section - the handler refuses a name over fifteen
			   characters for exactly that reason - so they are the same thing
			   while the tunnel is up. While it is down there is no L3 device,
			   and LuCI stands in a placeholder called qwdtt-<name>, which is
			   what ping was being asked to use: "bad address 'qwdtt-qwdtt0'"
			   on a tunnel somebody was trying to find out about.

			   Both names are shown where they differ, because the reply then
			   comes back through an interface the reader did not name and the
			   figure is only worth as much as knowing what it crossed. */
			return E('option', { 'value': entry.device }, [
				entry.device == name ? name
					: '%s (%s)'.format(name, entry.device)
			]);
		}));

	/* The widget the firewall pages use for an address: the resolvers worth
	   trying are one click away, and anything else can still be typed. It takes
	   the datatype itself and wires the validator, so a custom entry is judged
	   the way the rest of LuCI judges an address. */
	var target = new ui.Combobox('1.1.1.1', CHECK_TARGETS, {
		datatype: 'host',
		optional: false,
		select_placeholder: _('IP address or hostname'),
		custom_placeholder: _('IP address or hostname')
	});

	/* Kept inside the panel two ways. A textarea's width is its content box, so
	   a plain 100% puts its border and padding outside the room there is and
	   the box overhangs the frame by those few pixels; border-box spends the
	   100% on the whole thing. And a textarea may be dragged by its corner,
	   which is worth keeping for the height - the panel grows with it - but not
	   for the width, where it would be dragged straight over the edge. */
	var out = E('textarea', {
		'id': 'qwdtt-check-output',
		'style': 'width:100%; max-width:100%; box-sizing:border-box; ' +
			'resize:vertical; font-family:monospace; white-space:pre',
		'readonly': true,
		'wrap': 'off',
		'rows': '20'
	});

	/* Whitespace and backslashes escaped the way fs.exec_direct escapes them,
	   because this posts to the same cgi-exec endpoint it does. */
	function quoteArg(value) {
		return String(value).replace(/\\/g, '\\\\').replace(/(\s)/g, '\\$1');
	}

	/* Streamed rather than waited for. ping flushes a line per reply even when
	   its output is a pipe, so the replies can appear over the five seconds they
	   take instead of arriving together at the end; fs.exec would hand back the
	   whole thing once the command had finished, so the request is made here and
	   the body read as it comes.

	   The command is printed above the output: what was run is half of what the
	   reader needs, and the interface it was bound to is the rest. */
	function run() {
		var address = (target.getValue() || '').trim();

		/* isValid() is true until the validator has run once, so the empty
		   case is checked here rather than left to it. */
		if (!address || !target.isValid())
			return;

		var args = [ '-I', picker.value, '-c', '5', '-W', '2', address ];
		var command = [ '/bin/ping' ].concat(args).map(quoteArg).join(' ');

		out.value = '# ping %s\n\n'.format(args.join(' '));

		return fetch(L.env.cgi_base + '/cgi-exec', {
			method: 'POST',
			headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
			body: 'sessionid=%s&command=%s'.format(
				encodeURIComponent(L.env.sessionid), encodeURIComponent(command))
		}).then(function(res) {
			/* Not a throw: that would skip straight to .catch and never reach
			   the fs.exec fallback below, leaving the box showing only
			   "Error: Forbidden" when the cgi-exec rule does not match.
			   Returning lets the next step run the same ping through rpcd. */
			if (!res.ok)
				return;

			/* A browser without a readable body stream still gets the output,
			   just all at once, which is what this replaced. */
			if (!res.body || typeof res.body.getReader != 'function')
				return res.text().then(function(text) { out.value += text; });

			var reader = res.body.getReader();
			var decoder = new TextDecoder();

			return (function pump() {
				return reader.read().then(function(chunk) {
					if (chunk.done)
						return;
					out.value += decoder.decode(chunk.value, { stream: true });
					out.scrollTop = out.scrollHeight;
					return pump();
				});
			})();
		}).then(function() {
			/* Both of ping's refusals - a device it does not know, an interface
			   with no route - go to stderr, and the stream carries stdout only,
			   so the box was left holding the command line and nothing else.
			   When the run ends without ping's own summary line, ask rpcd for
			   the same command once more: that returns stderr and the exit
			   status, and a ping that failed this way fails again at once. */
			if (/packets transmitted/.test(out.value))
				return;
			return fs.exec('/bin/ping', args).then(function(res) {
				if (res.stderr)
					out.value += res.stderr;
				if (res.code)
					out.value += _('exit status %d').format(res.code) + '\n';
			});
		}).catch(function(err) {
			out.value += '\n' + err;
		});
	}

	return E('details', {
		'class': 'cbi-section',
		'style': 'background:var(--background-color-high); ' +
			'border:1px solid var(--border-color-medium); ' +
			'border-radius:3px; box-sizing:border-box; ' +
			'padding:10px; margin-bottom:18px'
	}, [
		/* The marker is drawn in the summary's own font, not the heading's, so
		   with the heading at 18px over a 13px summary it came out small and
		   sitting low against the C. The summary carries the heading's metrics
		   instead and the heading inherits them back, which leaves the marker
		   the same size as the text and on the same baseline. The numbers are
		   the theme's own for h3, so this tracks the other headings. */
		E('summary', {
			'style': 'cursor:pointer; margin-bottom:.5em; ' +
				'font-size:18px; line-height:36px'
		}, [
			E('h3', {
				'style': 'display:inline; font-size:inherit; ' +
					'line-height:inherit; margin:0'
			}, [ _('Check tunnel') ])
		]),
		E('div', { 'class': 'cbi-section-descr' }, [
			_('Pings an address through the tunnel. A WireGuard-mode tunnel is pinged through the WireGuard interface that uses it.')
		]),
		E('table', { 'class': 'table' }, [
			E('tr', { 'class': 'tr' }, [
				E('td', { 'class': 'td left', 'style': 'overflow:initial' }, [
					/* One line, each label kept against the control it names:
					   the gap inside a pair is smaller than the gap between
					   them, so the eye groups them without a separator. Wraps
					   rather than overflows on a narrow window. */
					E('div', {
						'style': 'display:flex; align-items:center; ' +
							'flex-wrap:wrap; gap:1em'
					}, [
						E('span', { 'style': 'display:flex; align-items:center; gap:.4em' }, [
							/* Only this one can point at what it labels. The
							   host beside it is a LuCI dropdown, a div with an
							   input somewhere inside, and a for that named the
							   wrapper would be a claim that is not true. */
							E('label', { 'for': 'qwdtt-check-iface', 'style': 'margin:0' },
								[ '%s:'.format(_('Tunnel')) ]),
							picker
						]),
						E('span', { 'style': 'display:flex; align-items:center; gap:.4em' }, [
							E('label', { 'style': 'margin:0' }, [ '%s:'.format(_('Host')) ]),
							target.render()
						]),
						E('span', { 'class': 'diag-action' }, [
							E('button', {
								'class': 'cbi-button cbi-button-action',
								'click': ui.createHandlerFn(this, run)
							}, [ _('Ping') ])
						])
					])
				])
			])
		]),
		out
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
			E('div', { 'id': 'qwdtt-table' }, [ renderTable(nets) ]),
			checkSection(nets)
		]);
	},

	handleReset: null,
	handleSaveApply: null,
	handleSave: null
});
