// SPDX-License-Identifier: GPL-3.0-or-later
'use strict';
'require form';
'require network';
'require uci';
'require ui';

/* The qWDTT protocol class. A tunnel is a section of /etc/config/network with
   option proto 'qwdtt', so it is created, edited, started and stopped from
   Network -> Interfaces with the standard controls, and this file only has to
   describe the fields the protocol handler reads.

   What needs code is the hash list: the client accepts a full VK call link and
   reduces it to a hash itself (ParseHashes over the -vk flag), so storing a
   link would work -- but it would leave a link in the config and make the count
   on the status page misleading. Links are therefore reduced here, on save.

   That reduction is a translation of ParseHashes and normalizeVKJoinHash from
   the client's client/group.go, which is why this file is GPL-3.0-or-later and
   cannot be relicensed by one contributor alone: group.go carries no SPDX
   header of its own, inherits its repository's GPL-3.0, and has more than one
   author. Keeping the two in step also matters behaviourally -- if the
   client's rule changes, this must follow. */

/* ---- hash handling -------------------------------------------------------
   Ports of normalizeVKJoinHash and ParseHashes from the client's group.go. */

function trimChars(s, chars) {
	var start = 0, end = s.length;
	while (start < end && chars.indexOf(s.charAt(start)) !== -1)
		start++;
	while (end > start && chars.indexOf(s.charAt(end - 1)) !== -1)
		end--;
	return s.slice(start, end);
}

function firstIndexOfAny(s, chars) {
	var best = -1;
	for (var i = 0; i < chars.length; i++) {
		var at = s.indexOf(chars.charAt(i));
		if (at !== -1 && (best === -1 || at < best))
			best = at;
	}
	return best;
}

/* A full VK join link reduces to its trailing token; the "j-" such links carry
   is PART of the hash and is deliberately not stripped. A URL that is not a
   join link is rejected outright, exactly as the client does. */
function normalizeVKJoinHash(input) {
	var s = trimChars(String(input == null ? '' : input).trim(), '<>"\'');
	if (!s)
		return '';

	var lower = s.toLowerCase();
	var marker = '/call/join/';
	var idx = lower.indexOf(marker);

	if (idx >= 0)
		s = s.slice(idx + marker.length);
	else if (lower.indexOf('http://') === 0 ||
	         lower.indexOf('https://') === 0)
		return '';

	var cut = firstIndexOfAny(s, '?#/');
	if (cut !== -1)
		s = s.slice(0, cut);

	return trimChars(s.trim(), '/');
}

/* The client splits on comma, semicolon, whitespace and newlines, then
   deduplicates. One pasted field may therefore expand into several hashes. */
function splitHashTokens(raw) {
	var seps = ',;\n\r\t ';
	var out = [], cur = '';
	for (var i = 0; i < raw.length; i++) {
		var ch = raw.charAt(i);
		if (seps.indexOf(ch) !== -1) {
			if (cur)
				out.push(cur);
			cur = '';
		}
		else {
			cur += ch;
		}
	}
	if (cur)
		out.push(cur);
	return out;
}

/* Every hash VK has issued so far is 43 characters from the base64url
   alphabet -- unpadded base64 of a 32-byte token. The CLIENT enforces nothing
   of the sort: it only refuses an empty list. So this is a check against
   typos and truncated pastes, not a mirror of a rule the daemon applies, and
   the field description says as much so a future format change is
   diagnosable rather than mysterious. */
var HASH_LEN = 43;
var B64URL = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ' +
             'abcdefghijklmnopqrstuvwxyz' + '0123456789-_';

/* ---- how many workers a tunnel may ask for -------------------------------
   The client starts them in groups of workersPerGroup and applies two ceilings
   of its own in main.go: 108 in all, and 4 when the credentials come from a VK
   account, which is about as many relays as one account is given.

   The third ceiling is VK's rather than the client's, and the client does not
   apply it: one call sustains three groups before the relay quota starts
   refusing allocations with error 486, so the real limit is 27 per hash and
   more hashes are what buys more workers. The Android client of this protocol
   computes it the same way, in SettingsStore.maxAnonymousWorkers. */

var WORKERS_PER_GROUP = 9;
var GROUPS_PER_HASH = 3;
var MAX_HASHES = 4;
var ACCOUNT_MAX_WORKERS = 4;

function workerCeiling(hashCount, accountMode) {
	if (accountMode)
		return ACCOUNT_MAX_WORKERS;

	return Math.min(Math.max(hashCount, 1), MAX_HASHES) *
		WORKERS_PER_GROUP * GROUPS_PER_HASH;
}

/* ---- descriptions --------------------------------------------------------
   A field left alone is written nowhere, so the page cannot show what it will
   run with: the value is whatever the protocol handler falls back to, and that
   is only visible by reading the handler. Saying it first, in the same words
   every time, is what lets a reader scan the tab for what has been changed and
   put a field back.

   The value is not translated when it is a literal the operator would type, and
   is when the control shows it translated - the reader is matching this against
   what is in front of them, not against the config file. */
function withDefault(value, text) {
	return '%s %s'.format(_('Default: %s.').format(value), text);
}

/* null when the hash looks right, otherwise why not. */
function hashProblem(h) {
	if (!h)
		return _('not a VK call link or a hash');
	if (h.length !== HASH_LEN)
		return _('expected %d characters, got %d').format(HASH_LEN, h.length);
	for (var i = 0; i < h.length; i++)
		if (B64URL.indexOf(h.charAt(i)) === -1)
			return _('unexpected character "%s"').format(h.charAt(i));
	return null;
}

function parseHashes(entries) {
	var seen = {}, out = [];
	entries.forEach(function(entry) {
		splitHashTokens(String(entry == null ? '' : entry)).forEach(function(token) {
			var h = normalizeVKJoinHash(token);
			if (h && !seen[h]) {
				seen[h] = true;
				out.push(h);
			}
		});
	});
	return out;
}

/* ---- routing -------------------------------------------------------------
   The tunnel needs a routing table of its own, and that is not a preference.
   The client reaches its VK TURN relays over the WAN, so a default route into
   the tunnel in the main table would send the tunnel's own transport through
   the tunnel; the protocol handler refuses to come up rather than take the
   router's WAN down that way. The field for it belongs to the interface editor,
   so all this file can do is choose the number - see renderFormOptions.

   What decides which traffic enters the table is an ordinary `config rule`,
   and what stops traffic leaking to the WAN while the tunnel is down is an
   ordinary unreachable route in the same table. Neither is a qWDTT option --
   both are written here because every value they need is known here, and
   both are then editable on Network -> Routing like any other.

   One flag each, rather than one flag writing both: they answer different
   questions, and a single control could neither drop the kill switch on its
   own nor be turned off without taking a kill switch somebody wanted with
   it. */

var KILL_METRIC = '4096';

function tableOf(section_id) {
	return uci.get('network', section_id, 'ip4table') || '';
}

/* The lowest table nothing else has claimed. 51820 is where the client's own
   default sat, so a router with one tunnel keeps the number it already had. */
function freeTable() {
	var used = {};

	uci.sections('network', 'interface', function(section) {
		var table = section.ip4table;
		if (table)
			used[table] = true;
	});

	for (var table = 51820; table < 52820; table++)
		if (!used[String(table)])
			return String(table);

	return '51820';
}

/* Below every rule that exists, so a tunnel added second is consulted first.
   The one added first is usually the catch-all, and a rule that matches
   everything has to be asked last or the others never see a packet. */
function freePriority() {
	var lowest = null;

	uci.sections('network', 'rule', function(section) {
		var priority = parseInt(section.priority, 10);
		if (!isNaN(priority) && (lowest == null || priority < lowest))
			lowest = priority;
	});

	return String(lowest == null ? 10000 : Math.max(1, lowest - 1));
}

/* Everything but the table is written once, at creation. The description sends
   the reader to Network -> Routing to narrow this to one client or one
   destination, and a value re-asserted on every save would quietly undo that
   the next time anyone opened the interface. The table is not theirs to keep:
   it is whichever one the interface uses, and a rule left on the old number
   looks up an empty table. */
function addLanRule(section_id, table) {
	var rule = section_id + '_rule';

	if (uci.get('network', rule) == null) {
		uci.add('network', 'rule', rule);
		uci.set('network', rule, 'in', 'lan');
		uci.set('network', rule, 'priority', freePriority());
	}
	uci.set('network', rule, 'lookup', table);
}

/* Attached to loopback so that it outlives the tunnel: a route attached to the
   tunnel itself would disappear exactly when it is needed. The metric is
   beaten by the tunnel's own default, so it only decides what happens once
   that one is gone. */
function addKillswitch(section_id, table) {
	var kill = section_id + '_killswitch';

	if (uci.get('network', kill) == null) {
		uci.add('network', 'route', kill);
		uci.set('network', kill, 'interface', 'loopback');
		uci.set('network', kill, 'target', '0.0.0.0/0');
		uci.set('network', kill, 'type', 'unreachable');
		uci.set('network', kill, 'metric', KILL_METRIC);
	}
	uci.set('network', kill, 'table', table);
}

/* Only what is there. Removing a section that does not exist still marks the
   map as changed, which would show an unsaved change on every visit to a
   tunnel that never had it. */
function dropSection(name) {
	if (uci.get('network', name) != null)
		uci.remove('network', name);
}

/* ---- what the interface says when it will not come up --------------------
   netifd carries a code, and Network -> Interfaces prints "Unknown error
   (CODE)" for anything it has not been told about. The first six are the
   protocol handler refusing a configuration it can read; the rest are the
   client reporting an answer from the server that reconnecting will not
   change. Both reach the same place, which is the one the operator is looking
   at when a tunnel is down. */
[
	[ 'MISSING_PEER_HOST',      _('No server address is set') ],
	[ 'MISSING_HASH',           _('No VK call hash is set') ],
	[ 'NAME_TOO_LONG',          _('The interface name is longer than the 15 characters a TUN device may have') ],
	[ 'MISSING_IP4TABLE',       _('No routing table is set, and the tunnel cannot use the main one: its default route would carry the client\'s own traffic to VK') ],
	[ 'MISSING_DEVICE_ID',      _('No device ID is set') ],
	[ 'DUPLICATE_DEVICE_ID',    _('Another qWDTT interface already uses this device ID') ],
	[ 'QWDTT_WRONG_PASSWORD',   _('The server rejected the connection password') ],
	[ 'QWDTT_PASSWORD_EXPIRED', _('The connection password has expired') ],
	[ 'QWDTT_DEVICE_MISMATCH',  _('The connection password belongs to another device ID. One password is bound to one device, so a second router needs its own.') ],
	[ 'QWDTT_AUTH_FAILED',      _('The server refused this tunnel. Check the password and the device ID.') ],
	[ 'QWDTT_HASH_DEAD',        _('The VK call behind this hash is closed. Replace the hash.') ]
].forEach(function(e) {
	network.registerErrorCode(e[0], e[1]);
});

return network.registerProtocol('qwdtt', {
	getI18n: function() {
		return _('qWDTT');
	},

	getIfname: function() {
		return this._ubus('l3_device') || this.sid;
	},

	getOpkgPackage: function() {
		return 'qwdtt-client';
	},

	getIcon: function() {
		return L.resource('icons/tunnel%s.png').format(this.isUp() ? '' : '_disabled');
	},

	isFloating: function() {
		return true;
	},

	isVirtual: function() {
		return true;
	},

	getDevices: function() {
		return null;
	},

	containsDevice: function(ifname) {
		return (network.getIfnameOf(ifname) == this.getIfname());
	},

	renderFormOptions: function(s) {
		var o;

		/* Everything this protocol adds goes on one tab of its own. The
		   interface editor's own tabs are declared before this runs, so it
		   lands last, after DHCP Server. */
		s.tab('qwdtt', _('qWDTT'));

		/* The section name is the TUN device the client creates, so it has to
		   be a name the kernel takes. Nothing else in the editor says so, and
		   the protocol handler's refusal arrives only after Save & Apply. */
		if (s.section.length > 15)
			s.description = _('This interface name is longer than 15 characters, so the tunnel cannot come up: the name is also the TUN device, and the kernel takes 15.');

		/* ip4table is netifd's own option and the editor adds its field for it
		   after this function returns - through replaceOption, which finds an
		   option by name anywhere in the section and rebuilds it on Advanced
		   Settings. An option declared here is therefore thrown away, default
		   and all, which is how a tunnel came to be created with no table and
		   refused for the want of one. The value is seeded instead, as a
		   staged change the editor shows like any other. */
		if (!uci.get('network', s.section, 'ip4table')) {
			var seeded = freeTable();

			uci.set('network', s.section, 'ip4table', seeded);
			/* The same visit is the only one at which a tunnel is known to have
			   no routing of its own yet, so it is where both routing flags get
			   to start on. Carrying the LAN is what a tunnel is added for, and a
			   tunnel that releases it to the WAN the moment it drops is a
			   surprise rather than a convenience. Either is one click off. */
			addLanRule(s.section, seeded);
			addKillswitch(s.section, seeded);
		}

		/* host(1) rather than host(): the protocol handler builds the client's
		   -peer as host:port with no brackets, so an IPv6 literal would not
		   survive the concatenation. */
		o = s.taboption('qwdtt', form.Value, 'peer_host', _('Peer host'),
			_('Server hostname or IPv4 address.'));
		o.datatype = 'host(1)';
		o.rmempty = false;

		o = s.taboption('qwdtt', form.Value, 'peer_port', _('Peer port'),
			withDefault('56003', _('UDP port of the server RAW listener.')));
		o.datatype = 'port';
		o.placeholder = '56003';

		o = s.taboption('qwdtt', form.Value, 'device_id', _('Device ID'),
			_('Identifies this tunnel to the server, which knows it by this and nothing else. Two tunnels that share one are a single device to it and it disconnects them in turn, so each needs its own.'));
		o.rmempty = false;

		/* ---- routing ------------------------------------------------------ */

		o = s.taboption('qwdtt', form.Value, 'password', _('Password'),
			_('Connection password, as set on the server.'));
		o.password = true;
		o.rmempty = false;

		o = s.taboption('qwdtt', form.DynamicList, 'hash', _('Hashes'),
			_('Added one at a time with the button below, which takes either the %d-character hash or a whole VK call link and reduces the link to the hash it denotes. At least one is required -- the client selects a hash modulo the list length, so an empty list cannot work.').format(HASH_LEN));

		/* DynamicList passes `optional: this.optional || this.rmempty` to its
		   widget, so clearing rmempty is what routes an empty list through
		   LuCI's own "non-empty value" rejection rather than a check of our
		   own. */
		o.rmempty = false;

		/* Judged per field. A link passes because it reduces to a valid hash,
		   which is what the client would do with it anyway. */
		o.validate = function(section_id, value) {
			if (value == null || value === '')
				return true;
			return hashProblem(normalizeVKJoinHash(value)) || true;
		};

		/* Normalise on the way to UCI so a pasted link is stored as the hash
		   it denotes and duplicates collapse. */
		o.write = function(section_id, formvalue) {
			var list = Array.isArray(formvalue) ? formvalue
			         : (formvalue ? [ formvalue ] : []);
			return form.DynamicList.prototype.write.call(this, section_id,
				parseHashes(list));
		};

		/* The stock widget for what is already in the list, and a field of our
		   own to put things into it. Delegating to the parent keeps the
		   standard remove and reorder controls, and the field goes in a wrapper
		   rather than inside the dynlist node, whose children are its items.
		   Wrapping is safe for getUIElement, which resolves the widget by
		   element id. */
		o.renderWidget = function(section_id, option_index, cfgvalue) {
			var self = this;
			var node = form.DynamicList.prototype.renderWidget.apply(this, arguments);

			/* ui.DynamicList ends with a row of its own for adding items, and
			   this field is not free-form: a link has to be reduced to the hash
			   it denotes before it can go in the list, and anything that is
			   neither has to be refused. That row is hidden rather than taken
			   out, because addItem() finds it with querySelector to insert
			   before and dereferences the result without checking - removing it
			   throws on the next setValue(). */
			var addRow = node.querySelector('.add-item');
			if (addRow)
				addRow.style.display = 'none';

			/* An id because a form field without one is flagged by every
			   accessibility check, and its own rather than the widget's:
			   getUIElement resolves the list by "widget." + cbid, and a second
			   element answering to that would be found instead of the list.

			   No width of its own either. The theme gives every input 210px,
			   which is what the fields above this one are, and anything set
			   here would leave this one the odd width on the tab. */
			var field = E('input', {
				'id': 'qwdtt.%s.addhash'.format(section_id),
				'type': 'text',
				'class': 'cbi-input-text',
				'aria-label': _('Hash or VK call link'),
				'placeholder': _('Hash or VK call link')
			});

			var problem = E('div', {
				'class': 'cbi-value-description',
				'style': 'display:none'
			});

			function complain(text) {
				problem.textContent = text;
				problem.style.display = '';
				field.classList.add('cbi-input-invalid');
			}

			function accept() {
				problem.style.display = 'none';
				field.classList.remove('cbi-input-invalid');
			}

			/* Staged, not saved: what is added has to join what the user is
			   looking at, including edits not yet written. */
			function add() {
				var el = self.getUIElement(section_id);
				var hash = normalizeVKJoinHash(field.value);
				var trouble = hashProblem(hash);
				var list = el ? el.getValue() : null;

				if (trouble)
					return complain(trouble);

				list = (Array.isArray(list) ? list : []).filter(function(h) {
					return h != null && h !== '';
				});
				if (list.indexOf(hash) !== -1)
					return complain(_('This hash is already in the list.'));

				list.push(hash);
				if (el)
					el.setValue(list);
				field.value = '';
				accept();
			}

			field.addEventListener('input', accept);

			/* Inline rather than a flex row: both are inline-block already, so
			   they line up beside each other on their own, and the input keeps
			   the width the theme gave it instead of being stretched to fill. */
			return E('div', {}, [
				node,
				E('div', { 'style': 'margin-top:.5em' }, [
					field,
					' ',
					E('button', {
						'class': 'cbi-button cbi-button-add',
						'click': function(ev) { ev.preventDefault(); add(); }
					}, [ _('Add hash') ])
				]),
				problem
			]);
		};

		o = s.taboption('qwdtt', form.Value, 'workers', _('Workers'),
			withDefault(String(WORKERS_PER_GROUP),
				_('Parallel sessions, started in groups of %d. One VK call sustains three groups before its relay quota starts refusing, so the ceiling is %d per hash and up to %d hashes count towards it: %d, %d, %d, %d. With a VK account it is %d in all, which is about what one account is given.')
					.format(WORKERS_PER_GROUP, WORKERS_PER_GROUP * GROUPS_PER_HASH,
						MAX_HASHES,
						workerCeiling(1, false), workerCeiling(2, false),
						workerCeiling(3, false), workerCeiling(4, false),
						ACCOUNT_MAX_WORKERS)));
		o.datatype = 'uinteger';
		o.default = String(WORKERS_PER_GROUP);

		/* The client silently rounds a value it cannot use down to a group and
		   caps it, so a tunnel asked for 200 quietly runs 108 and one asked for
		   10 quietly runs 9. Refusing here is what makes the number on the page
		   the number the tunnel runs. */
		o.validate = function(section_id, value) {
			var account, hashes, limit, n;

			if (value == null || value === '')
				return true;

			n = parseInt(value, 10);
			if (isNaN(n) || n < 1)
				return _('At least one worker is needed.');

			hashes = this.section.formvalue(section_id, 'hash');
			hashes = parseHashes(Array.isArray(hashes) ? hashes : [ hashes ]);
			account = this.section.formvalue(section_id, 'vk_auth') == 'account';
			limit = workerCeiling(hashes.length, account);

			if (account)
				return n <= limit ? true
					: _('A VK account is given about %d relays, so at most %d workers.')
						.format(limit, limit);

			if (n % WORKERS_PER_GROUP != 0)
				return _('Workers are started in groups of %d, so this has to be a multiple of %d.')
					.format(WORKERS_PER_GROUP, WORKERS_PER_GROUP);

			if (n > limit)
				return _('%d hash(es) sustain %d workers. Add a hash for %d more, up to %d of them.')
					.format(Math.max(hashes.length, 1), limit,
						WORKERS_PER_GROUP * GROUPS_PER_HASH, MAX_HASHES);

			return true;
		};

		o = s.taboption('qwdtt', form.Flag, '_lanroute',
			_('Route LAN client traffic through this tunnel'),
			withDefault(_('on'), _('Writes an ordinary routing rule sending traffic from the lan interface to the routing table of this tunnel. Edit it afterwards on Network -> Routing - to send one client or one destination instead of the whole LAN, narrow it there and it stays narrowed; only the table it looks up is kept in step from here.')));
		o.rmempty = false;
		/* So that a table changed under Advanced Settings is carried into the
		   rule, which is otherwise left pointing at the old one. */
		o.forcewrite = true;

		/* Not a uci option of its own: the section it writes is what it reads
		   back. */
		o.cfgvalue = function(section_id) {
			return uci.get('network', section_id + '_rule') != null
				? this.enabled : this.disabled;
		};

		/* The rule steers traffic at a table whose only default route is the
		   one "Use default gateway" asks for. Without it the table holds
		   nothing the rule can use, and the LAN loses the internet instead of
		   gaining a tunnel - which looks like the tunnel failing rather than
		   like a box left unticked on another tab. */
		o.validate = function(section_id, value) {
			var gateway;

			if (value != this.enabled)
				return true;
			gateway = this.section.formvalue(section_id, 'defaultroute');
			if (gateway != null && gateway != '1')
				return _('This needs "Use default gateway" under Advanced Settings: without it the tunnel adds no default route for the rule to find, and the LAN would reach nothing.');
			return true;
		};

		o.write = function(section_id, value) {
			if (value != this.enabled)
				return dropSection(section_id + '_rule');

			addLanRule(section_id,
				this.section.formvalue(section_id, 'ip4table') ||
				tableOf(section_id) || freeTable());
		};

		o.remove = function(section_id) {
			dropSection(section_id + '_rule');
		};

		o = s.taboption('qwdtt', form.Flag, '_killswitch',
			_('Do not allow traffic if the tunnel is down (kill switch).'),
			withDefault(_('on'), _('Writes an unreachable default route into the routing table of this tunnel, so traffic sent there is refused rather than released to the WAN whenever the tunnel is not up. Independent of the rule above: it covers whatever looks up that table, including a rule written by hand.')));
		o.rmempty = false;
		o.forcewrite = true;

		o.cfgvalue = function(section_id) {
			return uci.get('network', section_id + '_killswitch') != null
				? this.enabled : this.disabled;
		};

		o.write = function(section_id, value) {
			if (value != this.enabled)
				return dropSection(section_id + '_killswitch');

			addKillswitch(section_id,
				this.section.formvalue(section_id, 'ip4table') ||
				tableOf(section_id) || freeTable());
		};

		o.remove = function(section_id) {
			dropSection(section_id + '_killswitch');
		};

		/* Every default below is the one the protocol handler falls back to,
		   so a new tunnel opens showing what it will actually run with instead
		   of a row of empty fields. LuCI writes nothing for a field still equal
		   to its default, which is what keeps the section free of options the
		   handler would have supplied anyway. */

		o = s.taboption('qwdtt', form.Value, 'go_dns', _('DNS for VK'),
			withDefault('yandex', _('Resolver the client uses to reach VK, which is not the resolver the tunnel hands out: yandex, cloudflare or google, their doh- variants, or custom:IP and doh:URL.')));
		o.default = 'yandex';

		o = s.taboption('qwdtt', form.ListValue, 'obfs', _('Obfuscation'),
			withDefault(_('audio'), _('What the tunnel is disguised as inside the VK call.')));
		o.value('audio', _('audio'));
		o.value('video', _('video'));
		o.default = 'audio';

		o = s.taboption('qwdtt', form.ListValue, 'captcha_mode', _('Captcha mode'),
			withDefault('auto', _('How a VK captcha is answered. auto tries the built-in solver and falls back.')));
		o.value('auto', 'auto');
		o.value('rjs', 'rjs');
		o.value('wv', 'wv');
		o.default = 'auto';

		o = s.taboption('qwdtt', form.ListValue, 'vk_auth', _('VK authorization'),
			withDefault(_('anonymous'), _('anonymous joins the call without an account. account uses TURN credentials from a VK account, read from the file below.')));
		o.value('anonymous', _('anonymous'));
		o.value('account', _('account'));
		o.default = 'anonymous';

		o = s.taboption('qwdtt', form.ListValue, 'vk_anon_path', _('Anonymous path'),
			withDefault('vkcalls', _('Which VK endpoint an anonymous join goes through.')));
		o.value('vkcalls', 'vkcalls');
		o.value('legacy', 'legacy');
		o.default = 'vkcalls';
		o.depends('vk_auth', 'anonymous');

		o = s.taboption('qwdtt', form.Value, 'vk_creds_file', _('VK credentials file'),
			_('File holding the TURN credentials of a VK account. Without it, account authorization has nothing to authorize with.'));
		o.placeholder = '/etc/qwdtt/vk-creds.json';
		o.depends('vk_auth', 'account');

		o = s.taboption('qwdtt', form.Flag, 'no_dtls', _('Disable DTLS'),
			withDefault(_('off'), _('Direct mode: RTP-obfs AEAD over TURN without DTLS. The server has to be started with -listen-direct, or the tunnel will not come up.')));

		o = s.taboption('qwdtt', form.Flag, 'turn_tcp', _('TURN over TCP'),
			withDefault(_('off'), _('Reach the TURN relay over TCP instead of UDP. Works around UDP throttling on some networks, for example Rostelecom.')));

	}
});
