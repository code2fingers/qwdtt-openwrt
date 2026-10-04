// Exercises the routing options in protocol/qwdtt.js away from a browser.
//
// The LAN-route flag writes an ordinary section of /etc/config/network that the
// operator is then invited to edit on Network -> Routing, and every way that
// can go wrong is silent: a rule re-broadened on the next save still routes and
// reads back as if nothing happened. The interface editor cannot be opened
// here, so LuCI's wrapper is reproduced instead - it wraps each resource in a
// function and injects its requires - and the uci store is read back after.
//
// Run from the repository root: node luci-proto-qwdtt/tests/options.js

const fs = require('fs');

const SRC = 'luci-proto-qwdtt/htdocs/luci-static/resources/protocol/qwdtt.js';

// luci.js adds this to String, and the descriptions use it.
if (!String.prototype.format)
	String.prototype.format = function() {
		const args = arguments;
		let i = 0;
		return this.replace(/%[sd]/g, () => String(args[i++]));
	};

function makeUci() {
	const store = {};
	return {
		store,
		get(cfg, sid, opt) {
			const s = store[sid];
			if (s == null) return null;
			return opt == null ? s['.type'] : (s[opt] != null ? s[opt] : null);
		},
		set(cfg, sid, opt, val) {
			store[sid] = store[sid] || { '.type': 'unknown' };
			store[sid][opt] = String(val);
		},
		add(cfg, type, sid) { store[sid] = { '.type': type }; },
		remove(cfg, sid) { delete store[sid]; },
		sections(cfg, type, cb) {
			Object.keys(store).forEach(k => {
				if (store[k]['.type'] === type) cb(Object.assign({ '.name': k }, store[k]));
			});
		}
	};
}

// The object registerProtocol was handed, kept because not every hook has a
// widget to reach it by: deleteConfiguration is called by the interface
// editor's Delete button and by nothing else.
let lastProto = null;

// Every error code the protocol registers, with its message, kept so the
// messages can be held to the width of the box they are shown in.
const registeredErrors = {};

function load(uci, formvalues) {
	const opts = {};
	const section = {
		section: 'qwdtt0',
		// As form.js does it: a second declaration of the same tab throws,
		// which is what the interface editor hit when one qWDTT interface was
		// opened after another. A no-op stub here could not have caught it.
		tabs: null,
		tab(name, title) {
			if (this.tabs && this.tabs[name])
				throw 'Tab already declared';
			this.tabs = this.tabs || {};
			this.tabs[name] = { name, title };
		},
		formvalue(sid, name) { return formvalues[name]; },
		taboption(tab, type, name, title, desc) {
			const o = {
				enabled: '1', disabled: '0', section,
				optName: name, title, description: desc, labels: {},
				value(key, label) { this.labels[key] = label; }, depends() {}
			};
			opts[name] = o;
			return o;
		}
	};

	const form = {};
	[ 'Flag', 'Value', 'ListValue', 'DynamicList' ].forEach(k => {
		form[k] = function() {};
		form[k].prototype = { write() {}, renderWidget() { return {}; } };
	});

	const network = {
		registerErrorCode(code, text) { registeredErrors[code] = text; },
		registerProtocol(name, proto) { return proto; }
	};

	const fn = new Function('form', 'network', 'uci', 'ui', 'L', '_', 'E',
		fs.readFileSync(SRC, 'utf8'));
	const proto = fn(form, network, uci, {}, { resource: () => '' },
		s => s, () => ({}));

	lastProto = proto;
	proto.renderFormOptions.call({ sid: 'qwdtt0' }, section);
	return opts;
}

let failed = 0;
function check(what, got, want) {
	const a = JSON.stringify(got), b = JSON.stringify(want);
	if (a === b) return;
	console.log(`${what}:\n  got  ${a}\n  want ${b}`);
	failed = 1;
}

// --- a tunnel being created ------------------------------------------------
{
	const uci = makeUci();
	uci.add('network', 'interface', 'qwdtt0');
	uci.set('network', 'qwdtt0', 'proto', 'qwdtt');
	const opts = load(uci, { defaultroute: '1', ip4table: null });

	check('a new tunnel is seeded with a table',
		uci.get('network', 'qwdtt0', 'ip4table'), '51820');
	check('a new tunnel starts carrying the lan',
		[ uci.get('network', 'qwdtt0_rule', 'in'),
		  uci.get('network', 'qwdtt0_rule', 'lookup') ], [ 'lan', '51820' ]);
	check('and the lan flag reads back on',
		opts._lanroute.cfgvalue('qwdtt0'), '1');
	check('the rule points at the table the tunnel was seeded with',
		uci.get('network', 'qwdtt0_rule', 'lookup'), '51820');

	// It writes a section of its own rather than a value, so it goes last
	// rather than among the plain settings.
	check('the lan-route flag comes last on the tab',
		Object.keys(opts).slice(-1), [ '_lanroute' ]);

	check('the two pacing limits are adjacent, download first',
		Object.keys(opts).filter(k => k == 'rate_up' || k == 'rate_down'),
		[ 'rate_down', 'rate_up' ]);
}

// --- deleting a tunnel takes its routing with it ---------------------------
{
	const uci = makeUci();
	uci.add('network', 'interface', 'qwdtt0');
	uci.set('network', 'qwdtt0', 'proto', 'qwdtt');
	load(uci, { defaultroute: '1', ip4table: null });

	// Left behind, the rule looks up a table this tunnel no longer fills.
	lastProto.deleteConfiguration.call({ sid: 'qwdtt0' });
	check('deleting a tunnel removes its rule',
		uci.get('network', 'qwdtt0_rule'), null);
}

// --- the rule's priority stays clear of netifd's ---------------------------
// netifd gives an interface with an ip4table a source rule at 10000, so a
// first tunnel handed the same number leaves two rules whose order is decided
// by insertion and written down nowhere.
{
	const uci = makeUci();
	uci.add('network', 'interface', 'qwdtt0');
	uci.set('network', 'qwdtt0', 'proto', 'qwdtt');
	load(uci, { defaultroute: '1', ip4table: null });
	check('the first tunnel does not land on the priority netifd uses',
		uci.get('network', 'qwdtt0_rule', 'priority'), '9999');

	uci.remove('network', 'qwdtt0_rule');
	uci.add('network', 'rule', 'other');
	uci.set('network', 'other', 'priority', '10001');
	const again = load(uci, { defaultroute: '1', ip4table: '51820' });
	again._lanroute.write('qwdtt0', '1');
	check('nor does it when the rule below would have put it there',
		uci.get('network', 'qwdtt0_rule', 'priority'), '9999');
}

// --- the rule is written once, then left alone -----------------------------
{
	const uci = makeUci();
	uci.add('network', 'interface', 'qwdtt0');
	uci.set('network', 'qwdtt0', 'ip4table', '51820');
	const opts = load(uci, { defaultroute: '1', ip4table: '51820' });

	opts._lanroute.write('qwdtt0', '1');
	check('the rule is created for the lan',
		[ uci.get('network', 'qwdtt0_rule', 'in'),
		  uci.get('network', 'qwdtt0_rule', 'lookup') ], [ 'lan', '51820' ]);

	// what an operator does on Network -> Routing to reach one client only
	uci.set('network', 'qwdtt0_rule', 'in', 'guest');
	uci.set('network', 'qwdtt0_rule', 'src', '192.168.1.50/32');
	opts._lanroute.write('qwdtt0', '1');
	check('a narrowed rule survives a later save',
		[ uci.get('network', 'qwdtt0_rule', 'in'),
		  uci.get('network', 'qwdtt0_rule', 'src') ],
		[ 'guest', '192.168.1.50/32' ]);

	// but the table must follow the interface
	const moved = load(uci, { defaultroute: '1', ip4table: '51999' });
	moved._lanroute.write('qwdtt0', '1');
	check('a changed table is carried into the rule',
		uci.get('network', 'qwdtt0_rule', 'lookup'), '51999');
}

// --- turning the rule off removes its section -------------------------------
{
	const uci = makeUci();
	uci.add('network', 'interface', 'qwdtt0');
	uci.set('network', 'qwdtt0', 'ip4table', '51820');
	const opts = load(uci, { defaultroute: '1', ip4table: '51820' });

	opts._lanroute.write('qwdtt0', '1');
	check('turning it on writes the rule',
		uci.get('network', 'qwdtt0_rule', 'in'), 'lan');
	opts._lanroute.write('qwdtt0', '0');
	check('turning it off removes the rule',
		uci.get('network', 'qwdtt0_rule'), null);
}

// --- the guard against the one broken combination --------------------------
{
	const uci = makeUci();
	uci.add('network', 'interface', 'qwdtt0');
	uci.set('network', 'qwdtt0', 'ip4table', '51820');

	let opts = load(uci, { defaultroute: '0', ip4table: '51820' });
	check('the rule is refused without a default route',
		typeof opts._lanroute.validate('qwdtt0', '1'), 'string');
	check('but turning it off is always allowed',
		opts._lanroute.validate('qwdtt0', '0'), true);

	opts = load(uci, { defaultroute: '1', ip4table: '51820' });
	check('and it is accepted with one',
		opts._lanroute.validate('qwdtt0', '1'), true);

	// the option lives on another tab and may not be instantiated yet
	opts = load(uci, { defaultroute: undefined, ip4table: '51820' });
	check('an unreadable gateway field does not block the save',
		opts._lanroute.validate('qwdtt0', '1'), true);
}

// --- how many workers a tunnel may ask for ---------------------------------
// The ceiling is VK's relay quota rather than the client's: one call sustains
// three groups of nine, so hashes are what buy workers. The client rounds and
// caps silently, which is the thing being refused here instead.
{
	const H = n => Array.from({ length: n },
		(_, i) => String(i).padStart(43, 'a' + i));
	const at = (workers, hashCount, auth) => {
		const uci = makeUci();
		uci.add('network', 'interface', 'qwdtt0');
		uci.set('network', 'qwdtt0', 'ip4table', '51820');
		// account mode is not on the tab any more, so the ceiling reads it from
		// uci: a tunnel set that way by hand is still held to four
		uci.set('network', 'qwdtt0', 'vk_auth', auth || 'anonymous');
		const opts = load(uci, { hash: H(hashCount) });
		return opts.workers.validate('qwdtt0', workers);
	};
	const ok = (what, v, hashes, auth) => check(what, at(v, hashes, auth), true);
	const no = (what, v, hashes, auth) =>
		check(what, typeof at(v, hashes, auth), 'string');

	ok('one hash allows a full 27', '27', 1);
	no('one hash refuses 36', '36', 1);
	ok('two hashes allow 54', '54', 2);
	no('two hashes refuse 63', '63', 2);
	ok('four hashes allow the whole 108', '108', 4);
	no('a fifth hash buys nothing', '117', 5);

	no('a value between groups is refused', '10', 1);
	no('fewer than one group is refused', '5', 1);
	no('zero is refused', '0', 1);
	ok('the default of 9 passes with a single hash', '9', 1);
	ok('an empty value is left to the handler default', '', 1);

	ok('a VK account allows 4', '4', 4, 'account');
	no('a VK account refuses 9', '9', 4, 'account');
	ok('and is not held to whole groups', '3', 4, 'account');

	// The hash list and the workers field share a tab, so the hash formvalue
	// is normally present - but when that widget is not instantiated it is
	// undefined, and the ceiling was then taken from one hash, refusing a
	// tunnel whose saved list allows more. It falls back to the saved hashes.
	{
		const uci = makeUci();
		uci.add('network', 'interface', 'qwdtt0');
		uci.set('network', 'qwdtt0', 'ip4table', '51820');
		uci.set('network', 'qwdtt0', 'hash', H(4));
		const opts = load(uci, {});
		check('the worker ceiling falls back to the saved hashes when the field is absent',
			opts.workers.validate('qwdtt0', '108'), true);
	}
}

// --- the editor may render the same section twice -------------------------
// Open one qWDTT interface, close it, open another: the editor comes back
// with a section that already carries the tab, and form.js throws on a
// repeat declaration. What the operator saw was "Tab already declared" and
// an editor that would not open.
{
	const uci = makeUci();
	uci.add('network', 'interface', 'qwdtt0');
	uci.set('network', 'qwdtt0', 'proto', 'qwdtt');

	const opts = {};
	const section = {
		section: 'qwdtt0',
		tabs: null,
		tab(name, title) {
			if (this.tabs && this.tabs[name])
				throw 'Tab already declared';
			this.tabs = this.tabs || {};
			this.tabs[name] = { name, title };
		},
		formvalue() { return null; },
		taboption(tab, type, name, title, desc) {
			const o = { enabled: '1', disabled: '0', section, optName: name,
			            title, description: desc, value() {}, depends() {} };
			opts[name] = o;
			return o;
		}
	};
	const form = {};
	[ 'Flag', 'Value', 'ListValue', 'DynamicList' ].forEach(k => {
		form[k] = function() {};
		form[k].prototype = { write() {}, renderWidget() { return {}; } };
	});
	const network = { registerErrorCode() {}, registerProtocol(name, proto) { return proto; } };
	const fn = new Function('form', 'network', 'uci', 'ui', 'L', '_', 'E',
		fs.readFileSync(SRC, 'utf8'));
	const proto = fn(form, network, uci, {}, { resource: () => '' }, s => s, () => ({}));

	let err = null;
	try {
		proto.renderFormOptions.call({ sid: 'qwdtt0' }, section);
		proto.renderFormOptions.call({ sid: 'qwdtt0' }, section);
	} catch (e) {
		err = String(e);
	}
	check('rendering the same section twice does not throw', err, null);
}

// --- nothing here opens a dialog -------------------------------------------
// The interface editor is itself a LuCI modal and there is only one: showModal
// calls dom.content on it, so a second dialog replaces the editor rather than
// stacking on it, and the form being edited is gone. Anything this file wants
// to show has to go inside the form.
{
	const src = fs.readFileSync(
		'luci-proto-qwdtt/htdocs/luci-static/resources/protocol/qwdtt.js', 'utf8');
	// the explanation of why is allowed to name them; a call is not
	const calls = src.replace(/\/\*[\s\S]*?\*\//g, '')
		.match(/\b(showModal|hideModal)\s*\(/g) || [];

	check('the protocol page opens no modal of its own', calls, []);
}

// --- the stated defaults are the handler's ---------------------------------
// Each hint opens with the value the tunnel runs when the field is left alone,
// which is a copy of a fallback in the protocol handler. A copy drifts, and a
// hint naming a default nothing uses is worse than no hint, so the two are
// compared rather than trusted. English only: the value is substituted before
// translation, and _() is the identity here.
{
	const handler = fs.readFileSync('qwdtt-client/files/qwdtt.sh', 'utf8');
	const re = /\$\{([a-z_]+):-([^}]+)\}/g;
	// Deliberately absent from the tab. Account mode wants a supervising
	// process handing it fresh TURN credentials every few minutes, which a
	// router does not have, so the page does not offer a choice that cannot
	// work. The handler still passes it, for a config set with uci.
	const NOT_OFFERED = { vk_auth: 1, vk_creds_file: 1 };
	const uci = makeUci();
	uci.add('network', 'interface', 'qwdtt0');
	uci.set('network', 'qwdtt0', 'ip4table', '51820');
	const opts = load(uci, {});
	let m, seen = 0;

	while ((m = re.exec(handler)) !== null) {
		const [ , name, value ] = m;
		const o = opts[name];

		if (NOT_OFFERED[name])
			continue;
		if (o == null) {
			console.log(`the handler falls back to ${name}=${value}, which no field offers`);
			failed = 1;
			continue;
		}
		seen++;
		// A dropdown names its default by the label it shows.
		const want = `Default: ${o.labels[value] ?? value}.`;
		if (!String(o.description || '').startsWith(want)) {
			console.log(`${name}: hint does not open with ${JSON.stringify(want)}\n  ${o.description}`);
			failed = 1;
		}
	}
	check('every handler fallback was checked', seen > 0, true);
}

// --- the ports that depend on the mode, in three places --------------------
// The peer port has no single default any more: the handler picks one by mode,
// the protocol page names both in the hint for the field, and the status page
// fills one in so the Peer column says where the tunnel actually goes. Three
// copies, and nothing at runtime would notice them disagreeing - a tunnel sent
// at a port nobody mentioned looks exactly like a server that is not there.
{
	const handler = fs.readFileSync('qwdtt-client/files/qwdtt.sh', 'utf8');
	const PAGES = [
		'luci-proto-qwdtt/htdocs/luci-static/resources/protocol/qwdtt.js',
		'luci-proto-qwdtt/htdocs/luci-static/resources/view/qwdtt/status.js'
	];

	const ports = {};
	let m;
	const re = /^QWDTT_PEER_PORT_([a-z]+)=(\d+)$/gm;
	while ((m = re.exec(handler)) !== null)
		ports[m[1]] = m[2];
	check('the handler names a peer port for each mode',
		Object.keys(ports).sort(), [ 'rawtun', 'wireguard' ]);

	// The one default the handler still writes as a plain fallback, and the
	// address the status page tells the operator to point WireGuard at.
	const relay = (handler.match(/\$\{listen_port:-(\d+)\}/) || [])[1];
	check('the handler has a default relay port', relay != null, true);

	PAGES.forEach(path => {
		const src = fs.readFileSync(path, 'utf8');
		const decl = (src.match(/var PEER_PORT = \{([^}]*)\}/) || [])[1] || '';
		const seen = {};
		const one = /([a-z]+):\s*'(\d+)'/g;
		let d;

		while ((d = one.exec(decl)) !== null)
			seen[d[1]] = d[2];

		check(`${path} agrees with the handler on the peer ports`, seen, ports);
		check(`${path} agrees with the handler on the relay port`,
			(src.match(/var RELAY_PORT = '(\d+)'/) || [])[1], relay);
	});
}

// --- opening a WireGuard tunnel does not give it routing ------------------
// A wireguard-mode tunnel made over uci has no ip4table, because the handler
// asks for one only in rawtun mode. Seeding on sight staged a table and a lan
// rule for it; the flag depends on rawtun so it sits inactive and does not
// take the rule away, and the mode write handler only runs when the mode
// changes. Saving then pointed the lan at a table that routes nowhere.
{
	const uci = makeUci();
	uci.add('network', 'interface', 'qwdtt0');
	uci.set('network', 'qwdtt0', 'proto', 'qwdtt');
	uci.set('network', 'qwdtt0', 'mode', 'wireguard');

	load(uci, { defaultroute: '1', ip4table: null });

	check('opening a wireguard tunnel seeds no rule',
		uci.get('network', 'qwdtt0_rule'), null);
	// The table is seeded even so: inert for a tunnel that routes nothing,
	// and wanted the moment the mode changes, where nothing else would add
	// it in time - an ip4table widget that renders empty is parsed away.
	check('and seeds the table anyway, for the mode it may become',
		uci.get('network', 'qwdtt0', 'ip4table') != null, true);
}

// And switching it back finds the table already there, which is the point of
// seeding it: the handler refuses a rawtun tunnel without one.
{
	const uci = makeUci();
	uci.add('network', 'interface', 'qwdtt0');
	uci.set('network', 'qwdtt0', 'proto', 'qwdtt');
	uci.set('network', 'qwdtt0', 'mode', 'wireguard');

	const opts = load(uci, { defaultroute: '1', ip4table: null });
	opts.mode.write('qwdtt0', 'rawtun');

	check('switching a wireguard tunnel to rawtun leaves it with a table',
		uci.get('network', 'qwdtt0', 'ip4table') != null, true);
}

// --- a WireGuard tunnel writes no routing ----------------------------------
// It adds no route of its own, so a rule steering the lan at its table would
// find nothing there. The flag goes inactive on the tab, and an inactive
// option is not removed unless it has rmempty, which it clears; the mode field
// takes it instead.
{
	const uci = makeUci();
	uci.add('network', 'interface', 'qwdtt0');
	uci.set('network', 'qwdtt0', 'proto', 'qwdtt');

	// created as a RAW-IP tunnel, so the rule exists to begin with
	const opts = load(uci, { defaultroute: '1', ip4table: null });
	check('the tunnel starts with routing to lose',
		uci.get('network', 'qwdtt0_rule', 'in'), 'lan');

	opts.mode.write('qwdtt0', 'wireguard');
	check('switching to wireguard takes the rule with it',
		uci.get('network', 'qwdtt0_rule'), null);

	// and the other way round leaves what is there alone
	const back = makeUci();
	back.add('network', 'interface', 'qwdtt0');
	back.set('network', 'qwdtt0', 'proto', 'qwdtt');
	const again = load(back, { defaultroute: '1', ip4table: null });
	again.mode.write('qwdtt0', 'rawtun');
	check('staying on rawtun keeps it',
		back.get('network', 'qwdtt0_rule', 'in'), 'lan');
}

// --- the messages the interface page shows ---------------------------------
// Each one is a single line in the status box on Network -> Interfaces and in
// the Status column of Status -> qWDTT, which wrap past about seventy
// characters into something nobody reads. The Russian is what most users see
// there, so it is held to the same width, read straight out of the po file
// rather than trusted.
{
	const LIMIT = 70;
	const codes = Object.keys(registeredErrors);
	check('the error codes were seen at all', codes.length > 0, true);

	const ru = {};
	let msgid = null;
	for (const line of fs.readFileSync('luci-proto-qwdtt/po/ru/qwdtt.po', 'utf8').split('\n')) {
		let m;
		if ((m = line.match(/^msgid "(.*)"$/)))
			msgid = m[1].replace(/\\"/g, '"');
		else if ((m = line.match(/^msgstr "(.*)"$/)) && msgid !== null)
			ru[msgid] = m[1].replace(/\\"/g, '"');
	}

	const long = [];
	for (const code of codes) {
		const en = registeredErrors[code];
		if (en.length > LIMIT)
			long.push(`${code} en (${en.length})`);
		const tr = ru[en];
		if (tr && [...tr].length > LIMIT)
			long.push(`${code} ru (${[...tr].length})`);
	}
	check('every error message fits on one line of the interface page', long, []);
}

if (failed)
	process.exit(1);
console.log('qwdtt.js routing options: ok');
