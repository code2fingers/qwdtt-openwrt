// Checks that the status page's backend knows about every file the rest of
// the project leaves in /var/run/qwdtt.
//
// The backend finds a tunnel by stripping a known suffix off the filenames it
// finds there, so the list of suffixes is what decides which tunnels the page
// can see at all. It used to look for one file in particular, .counters, which
// only the up-script writes and only for a RAW-IP tunnel - so a wireguard-mode
// one was invisible to the page: no workers, no relays, no session clock, an
// empty answer for a tunnel that was carrying traffic.
//
// Nothing at runtime would report the two drifting apart again. A file written
// by a name the backend does not list is simply never read, and the page shows
// a dash where the figure should be.
//
// Run from the repository root: node luci-proto-qwdtt/tests/runtime.js

const fs = require('fs');

const BACKEND = 'luci-proto-qwdtt/root/usr/share/rpcd/ucode/luci.qwdtt';
// Where the files are written: the client for the ones it counts itself, and
// the up-script for the byte totals it records as the interface comes up.
const WRITERS = [
	[ 'client/netifd.go', /writeNetifdRunFile\("([a-z]+)"/g ],
	[ 'client/netifd.go', /iface\+"\.([a-z]+)"/g ],
	[ 'qwdtt-client/files/qwdtt-up.sh', /\$INTERFACE\.([a-z]+)"/g ]
];

let failed = 0;

const written = new Set();
for (const [ path, re ] of WRITERS) {
	const src = fs.readFileSync(path, 'utf8');
	let m;
	while ((m = re.exec(src)) !== null)
		written.add(m[1]);
}

if (!written.size) {
	console.log('found no run files being written at all, so nothing was checked');
	process.exit(1);
}

// The alternation out of the backend's own pattern, read loosely: what this
// has to stay in step with is the list of suffixes, not the regexp syntax
// around it.
const backend = fs.readFileSync(BACKEND, 'utf8');
const line = (backend.match(/^const RUNFILE = .*$/m) || [])[0] || '';
const listed = new Set(
	((line.match(/\(([a-z]+(?:\|[a-z]+)+)\)/) || [])[1] || '')
		.split('|').filter(Boolean));

if (!listed.size) {
	console.log(`could not read the suffix list out of ${BACKEND}`);
	process.exit(1);
}

const missing = [...written].filter(s => !listed.has(s)).sort();
if (missing.length) {
	console.log('written under /var/run/qwdtt but not known to the status backend:');
	for (const s of missing)
		console.log(`  .${s}`);
	failed = 1;
}

// The other direction is worth saying but not worth failing on: a suffix the
// backend still lists after the thing that wrote it is gone costs one stat()
// per poll and nothing else.
const stale = [...listed].filter(s => !written.has(s)).sort();
if (stale.length)
	console.log(`the backend also lists ${stale.map(s => '.' + s).join(', ')}, which nothing writes`);

// The teardown has to clear them when a tunnel stops being one. A file left
// behind is read against whatever takes the name next - the counter baseline
// subtracted from a different device's totals - and the WireGuard one holds a
// private key. A glob covers every suffix at once and is what this wants to
// find; a list is what drifted.
const handler = fs.readFileSync('qwdtt-client/files/qwdtt.sh', 'utf8');
// Either spelling of the run directory: the handler names it through a
// variable so its own tests can point it somewhere writable.
const sweeps = /rm -f "(?:\$QWDTT_RUN_DIR|\/var\/run\/qwdtt)\/\$config"\.\*/.test(handler);

if (!sweeps) {
	const removed = new Set();
	const rm = /(?:\$QWDTT_RUN_DIR|\/var\/run\/qwdtt)\/\$config"?\.([a-z]+)/g;
	let r;
	while ((r = rm.exec(handler)) !== null)
		removed.add(r[1]);

	const kept = [...written].filter(s => !removed.has(s)).sort();
	if (kept.length) {
		console.log('left behind when the tunnel is torn down:');
		for (const s of kept)
			console.log(`  .${s}`);
		failed = 1;
	}
}

if (failed)
	process.exit(1);
console.log('status backend run files: ok');
