// Exercises the routing options in protocol/qwdtt.js away from a browser.
//
// These two flags write ordinary sections of /etc/config/network that the
// operator is then invited to edit on Network -> Routing, and every way that
// can go wrong is silent: a rule re-broadened on the next save still routes,
// a kill switch removed along with the rule still leaves the tunnel up, and
// both read back as if nothing happened. The interface editor cannot be opened
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

function load(uci, formvalues) {
	const opts = {};
	const section = {
		section: 'qwdtt0',
		tab() {},
		formvalue(sid, name) { return formvalues[name]; },
		taboption(tab, type, name, title, desc) {
			const o = {
				enabled: '1', disabled: '0', section,
				optName: name, title, description: desc,
				value() {}, depends() {}
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
		registerErrorCode() {},
		registerProtocol(name, proto) { return proto; }
	};

	const fn = new Function('form', 'network', 'uci', 'ui', 'L', '_', 'E',
		fs.readFileSync(SRC, 'utf8'));
	const proto = fn(form, network, uci, {}, { resource: () => '' },
		s => s, () => ({}));

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
	check('a new tunnel starts with a kill switch',
		uci.get('network', 'qwdtt0_killswitch', 'type'), 'unreachable');
	check('and the kill switch flag reads back on',
		opts._killswitch.cfgvalue('qwdtt0'), '1');
	check('a new tunnel starts carrying the lan',
		[ uci.get('network', 'qwdtt0_rule', 'in'),
		  uci.get('network', 'qwdtt0_rule', 'lookup') ], [ 'lan', '51820' ]);
	check('and the lan flag reads back on',
		opts._lanroute.cfgvalue('qwdtt0'), '1');
	check('both point at the table the tunnel was seeded with',
		uci.get('network', 'qwdtt0_killswitch', 'table'), '51820');
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

// --- the two flags are independent -----------------------------------------
{
	const uci = makeUci();
	uci.add('network', 'interface', 'qwdtt0');
	uci.set('network', 'qwdtt0', 'ip4table', '51820');
	const opts = load(uci, { defaultroute: '1', ip4table: '51820' });

	opts._lanroute.write('qwdtt0', '1');
	opts._killswitch.write('qwdtt0', '1');
	opts._lanroute.write('qwdtt0', '0');
	check('turning the rule off leaves the kill switch',
		[ uci.get('network', 'qwdtt0_rule'),
		  uci.get('network', 'qwdtt0_killswitch', 'type') ],
		[ null, 'unreachable' ]);

	opts._lanroute.write('qwdtt0', '1');
	opts._killswitch.write('qwdtt0', '0');
	check('and dropping the kill switch leaves the rule',
		[ uci.get('network', 'qwdtt0_killswitch'),
		  uci.get('network', 'qwdtt0_rule', 'in') ], [ null, 'lan' ]);
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
		const opts = load(uci, { hash: H(hashCount), vk_auth: auth || 'anonymous' });
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
	const uci = makeUci();
	uci.add('network', 'interface', 'qwdtt0');
	uci.set('network', 'qwdtt0', 'ip4table', '51820');
	const opts = load(uci, {});
	let m, seen = 0;

	while ((m = re.exec(handler)) !== null) {
		const [ , name, value ] = m;
		const o = opts[name];

		if (o == null) {
			console.log(`the handler falls back to ${name}=${value}, which no field offers`);
			failed = 1;
			continue;
		}
		seen++;
		const want = `Default: ${value}.`;
		if (!String(o.description || '').startsWith(want)) {
			console.log(`${name}: hint does not open with ${JSON.stringify(want)}\n  ${o.description}`);
			failed = 1;
		}
	}
	check('every handler fallback was checked', seen > 0, true);
}

if (failed)
	process.exit(1);
console.log('qwdtt.js routing options: ok');
