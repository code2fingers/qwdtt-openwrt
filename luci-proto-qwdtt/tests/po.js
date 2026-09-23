// Compares the _( ) strings in each view against the msgids in its catalog.
//
// The installation side of this project is Russian, so an untranslated string
// is a real defect and an invisible one: LuCI falls back to the English source
// rather than failing, so the page still renders and nobody notices until a
// user does. A msgid left behind after its string was reworded is the same
// defect wearing the opposite hat - the translation is there and unreachable.
//
// Run from the repository root: node luci-proto-qwdtt/tests/po.js

const fs = require('fs');

const PAIRS = [
	[ 'luci-proto-qwdtt/htdocs/luci-static/resources/protocol/qwdtt.js',
	  'luci-proto-qwdtt/po/ru/qwdtt.po' ],
	[ 'luci-proto-qwdtt/htdocs/luci-static/resources/view/qwdtt/status.js',
	  'luci-proto-qwdtt/po/ru/qwdtt-status.po' ],
];

function sources(path) {
	const text = fs.readFileSync(path, 'utf8');
	const re = /_\(\s*'((?:[^'\\]|\\.)*)'/g;
	const out = new Set();
	let m;

	while ((m = re.exec(text)) !== null)
		out.add(m[1].replace(/\\'/g, "'"));

	return out;
}

function msgids(path) {
	const text = fs.readFileSync(path, 'utf8');
	const re = /^msgid "((?:[^"\\]|\\.)*)"/gm;
	const out = new Set();
	let m;

	while ((m = re.exec(text)) !== null)
		if (m[1])
			out.add(m[1].replace(/\\"/g, '"'));

	return out;
}

let failed = false;

for (const [ js, po ] of PAIRS) {
	const src = sources(js);
	const cat = msgids(po);
	const missing = [ ...src ].filter(s => !cat.has(s));
	const orphaned = [ ...cat ].filter(s => !src.has(s));

	for (const s of missing) {
		console.log(`${po}: no translation for ${JSON.stringify(s)}`);
		failed = true;
	}
	for (const s of orphaned) {
		console.log(`${po}: ${JSON.stringify(s)} is translated but no longer used`);
		failed = true;
	}
}

if (failed)
	process.exit(1);

console.log('luci-proto-qwdtt translations: ok');
