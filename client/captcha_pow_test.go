package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The seed call as VK wrote it in the captured page, and what the extractor
// has to find in it.
const (
	powFixtureCall  = `'bC2NroTIH2mShJWH',2,'pow_timeout'`
	powFixtureInput = "bC2NroTIH2mShJWH"
	powFixtureDiff  = 2
)

func powFixture(t testing.TB) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "captcha_page.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if !strings.Contains(string(b), powFixtureCall) {
		t.Fatalf("fixture no longer contains the seed call %s", powFixtureCall)
	}
	return string(b)
}

// The page is real, captured from VK, and is the only evidence that any of
// this works on what the server actually serves. The variants are the ways
// the obfuscator has rewritten that one call, applied to the same page: the
// point of a tokenizer over a regex is that none of them should matter.
func TestExtractPowSeed(t *testing.T) {
	page := powFixture(t)

	cases := []struct {
		name string
		call string
	}{
		{"as captured, single quotes", powFixtureCall},
		{"double quotes", `"bC2NroTIH2mShJWH",2,"pow_timeout"`},
		{"hex difficulty and loose spacing", `"bC2NroTIH2mShJWH" , 0x2 , "pow_timeout"`},
		{"marker first", `'pow_timeout','bC2NroTIH2mShJWH',2`},
		{"template literals", "`bC2NroTIH2mShJWH`,2,`pow_timeout`"},
		{"underscored difficulty", `'bC2NroTIH2mShJWH',0b10,'pow_timeout'`},
		// The exact literal is gone, so the marker layer cannot fire and the
		// structural one has to recognise the call by its components array
		// and the shape of its arguments alone.
		{"marker renamed", `'bC2NroTIH2mShJWH',2,'x7_powkey_v2'`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			html := strings.Replace(page, powFixtureCall, tc.call, 1)
			if tc.call != powFixtureCall && strings.Contains(html, powFixtureCall) {
				t.Fatal("variant was not applied")
			}

			seed, ok := extractPowSeed(html)
			if !ok {
				t.Fatal("no seed found")
			}
			if seed.PowInput != powFixtureInput {
				t.Errorf("powInput = %q, want %q (via %s)", seed.PowInput, powFixtureInput, seed.Source)
			}
			if seed.Difficulty != powFixtureDiff {
				t.Errorf("difficulty = %d, want %d (via %s)", seed.Difficulty, powFixtureDiff, seed.Source)
			}
			t.Logf("matched by %s, marker %q", seed.Source, seed.Marker)
		})
	}
}

// Where this stops working, written down because it is not obvious and
// because the day it matters nobody will want to rediscover it.
//
// The seed is told apart from the other strings in the call by elimination:
// anything mentioning pow, timeout or seed is taken for the marker, and what
// is left has to be exactly one token of seed shape. Rename the marker to
// something that mentions none of those and is itself six to sixty-four
// base62 characters, and there are two candidates with nothing to choose
// between them, so extraction refuses rather than guess.
//
// Refusing is the right answer - a wrong seed means a wrong proof of work
// sent to VK on every attempt - but it is a real hole, and the layer that
// was meant to survive a marker rename only survives one that keeps a hint.
func TestExtractPowSeedGivesUpOnAnUnhintedMarker(t *testing.T) {
	page := powFixture(t)
	html := strings.Replace(page, powFixtureCall, `'bC2NroTIH2mShJWH',2,'tmo_9f'`, 1)

	if seed, ok := extractPowSeed(html); ok {
		t.Errorf("extraction now succeeds (%+v) where it used to give up; if that"+
			" was deliberate, this test should record the new behaviour", seed)
	}
}

// Escaped quotes in an inline script are the L1d layer's reason to exist.
func TestExtractPowSeedHTMLEscaped(t *testing.T) {
	page := powFixture(t)
	escaped := strings.Replace(page, powFixtureCall,
		`&quot;bC2NroTIH2mShJWH&quot;,2,&quot;pow_timeout&quot;`, 1)

	seed, ok := extractPowSeed(escaped)
	if !ok {
		t.Fatal("no seed found in the escaped page")
	}
	if seed.PowInput != powFixtureInput {
		t.Errorf("powInput = %q, want %q", seed.PowInput, powFixtureInput)
	}
}

// Finding nothing has to stay cheap and quiet: an ordinary page is the
// common case on every code path that is not a captcha.
func TestExtractPowSeedFindsNothing(t *testing.T) {
	for name, html := range map[string]string{
		"empty":        "",
		"no script":    "<html><body><p>hello</p></body></html>",
		"plain script": "<html><script>var a = 1; f('x');</script></html>",
	} {
		if seed, ok := extractPowSeed(html); ok {
			t.Errorf("%s: found %+v, want nothing", name, seed)
		}
	}
}

// A settings object that merely mentions the marker is not a seed call. This
// is the false positive the structural layer could most easily produce, and
// acting on it would send VK a wrong proof of work every time.
func TestExtractPowSeedIgnoresSettingsObject(t *testing.T) {
	html := `<html><body><script>
	var settings = {"pow_timeout": 30000, "difficulty": 3, "powInput": "notaseed"};
	window.init(settings);
	</script></body></html>`

	if seed, ok := extractPowSeed(html); ok {
		t.Errorf("false positive: %+v", seed)
	}
}

// The extractor is only worth anything if the page parser reaches for it, so
// this drives parseCaptchaV2Page rather than extractPowSeed.
//
// The captured page cannot be used whole: it has neither the window.init
// blob nor the script tag the parser still requires, which is why a real
// captcha does not get this far today. Both are supplied here so the one
// thing under test is whether the seed arrives.
func TestParseCaptchaV2PageUsesTheTokenizer(t *testing.T) {
	page := powFixture(t)
	page = strings.Replace(page, "</head>", `<script>window.init = {"data":{"show_captcha_type":"pow"}};</script>`+
		`<script src="https://st.vk.ru/vkid/1.0/not_robot_captcha.js"></script></head>`, 1)

	parsed, err := parseCaptchaV2Page(page)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.PowInput != powFixtureInput {
		t.Errorf("PowInput = %q, want %q", parsed.PowInput, powFixtureInput)
	}
	if parsed.PowDifficulty != powFixtureDiff {
		t.Errorf("PowDifficulty = %d, want %d", parsed.PowDifficulty, powFixtureDiff)
	}
}

// The page VK actually serves, parsed whole and unaltered. This is the one
// that matters: before this work it could not be parsed at all, because the
// parser insisted on a window.init blob and a script tag the page no longer
// has, and gave up two steps before the proof of work.
func TestParseCaptchaV2PageOnTheRealPage(t *testing.T) {
	parsed, err := parseCaptchaV2Page(powFixture(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.PowInput != powFixtureInput || parsed.PowDifficulty != powFixtureDiff {
		t.Errorf("seed = %q/%d, want %q/%d",
			parsed.PowInput, parsed.PowDifficulty, powFixtureInput, powFixtureDiff)
	}
	if parsed.DebugInfo != "da6ed55c-041c-48dc-be66-f4021da07442" {
		t.Errorf("debugInfo = %q, want the uuid from window.vk", parsed.DebugInfo)
	}
	// Absent from this page, and no longer required: the show type and the
	// slider key come from initSession now.
	if parsed.Init != nil {
		t.Errorf("Init = %+v, want nil on a page that carries no window.init", parsed.Init)
	}
}

// debug_info is the one value here that is neither computed nor constant: it
// is read off the page, under a name the obfuscator picks.
func TestExtractCaptchaDebugInfo(t *testing.T) {
	const uuid = "da6ed55c-041c-48dc-be66-f4021da07442"

	t.Run("by the name VK uses today", func(t *testing.T) {
		got, ok := extractCaptchaDebugInfo(powFixture(t))
		if !ok || got != uuid {
			t.Errorf("got %q/%v, want %q", got, ok, uuid)
		}
	})

	// The name is obfuscator output and will change. The shape will not.
	t.Run("by shape when the name has changed", func(t *testing.T) {
		page := strings.Replace(powFixture(t), "brlefapmjnpg:", "qzxkwrtynopq:", 1)
		got, ok := extractCaptchaDebugInfo(page)
		if !ok || got != uuid {
			t.Errorf("got %q/%v, want %q", got, ok, uuid)
		}
	})

	t.Run("gives up rather than guess between two", func(t *testing.T) {
		page := strings.Replace(powFixture(t),
			"brlefapmjnpg: \""+uuid+"\"",
			"aaa: \""+uuid+"\",\n bbb: \"11111111-2222-3333-4444-555555555555\"", 1)
		if got, ok := extractCaptchaDebugInfo(page); ok {
			t.Errorf("picked %q out of two candidates", got)
		}
	})

	t.Run("absent", func(t *testing.T) {
		if got, ok := extractCaptchaDebugInfo("<html><body>nothing</body></html>"); ok {
			t.Errorf("found %q on a page with none", got)
		}
	})
}

// The slider key moved between two field names, and a server that has not
// moved yet still answers with the old one.
func TestCaptchaV2SliderKey(t *testing.T) {
	cases := map[string]struct {
		raw  any
		want string
	}{
		"settings_key, as VK sends now": {
			[]any{map[string]any{"type": "slider", "settings_key": "abc"}}, "abc"},
		"settings, as it was": {
			[]any{map[string]any{"type": "slider", "settings": "old"}}, "old"},
		"another component's settings are not the slider's": {
			[]any{map[string]any{"type": "checkbox", "settings_key": "no"}}, ""},
		"absent":     {nil, ""},
		"wrong type": {"a string", ""},
	}

	for name, tc := range cases {
		if got := captchaV2SliderKey(tc.raw); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

// The old pages are not extinct, and the regexes behind the tokenizer are
// what still reads them.
func TestParseCaptchaV2PageStillReadsThePlainForm(t *testing.T) {
	html := `<html><head>` +
		`<script>window.init = {"data":{"show_captcha_type":"pow"}};</script>` +
		`<script src="https://st.vk.ru/vkid/1.0/not_robot_captcha.js"></script></head>` +
		`<body><script>const powInput = "plainSeedValue123"; const difficulty = 3;</script></body></html>`

	parsed, err := parseCaptchaV2Page(html)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.PowInput != "plainSeedValue123" || parsed.PowDifficulty != 3 {
		t.Errorf("got %q/%d, want plainSeedValue123/3", parsed.PowInput, parsed.PowDifficulty)
	}
}

func TestLexJSStrings(t *testing.T) {
	toks := lexJS(`var a = 'it\'s'; var b = "x\"y"; var c = '\x41B'; var d = "line\
cont"; // comment "with quotes"
/* block // comment */ var e = 'pow_timeout';`)

	var got []string
	for _, tok := range toks {
		if tok.Kind == jsTString {
			got = append(got, tok.Val)
		}
	}

	want := []string{"it's", `x"y`, "AB", "linecont", "pow_timeout"}
	if len(got) != len(want) {
		t.Fatalf("strings = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("string %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// Telling a regex literal from a division is the one place a lexer this
// small can go wrong and silently swallow the rest of the script.
func TestLexJSTemplatesAndRegex(t *testing.T) {
	toks := lexJS("var s = `a ${'inner'} b`; var r = /a\\/b/g; var q = s / 2 / 1;")

	tpl, rgx, div := 0, 0, 0
	for _, tok := range toks {
		switch {
		case tok.Kind == jsTTemplate:
			tpl++
		case tok.Kind == jsTRegex:
			rgx++
		case tok.Kind == jsTPunct && tok.Val == "/":
			div++
		}
	}

	if tpl != 1 || rgx != 1 || div != 2 {
		t.Errorf("templates=%d regexes=%d divisions=%d, want 1, 1 and 2", tpl, rgx, div)
	}
}

// A keyword after a dot is a property name, not a keyword. Minified VK code
// is full of short property names, and reading one as a keyword puts the
// lexer in regex position: the division that follows then swallows the rest
// of the line into one regex token, and whatever call was in it disappears.
func TestLexJSKeywordAfterADotIsAPropertyName(t *testing.T) {
	for _, src := range []string{"x.of / 2 / y", "x.in / 2 / y", "x.do / 2 / y", "x?.of / 2 / y"} {
		rgx, div := 0, 0
		for _, tok := range lexJS(src) {
			switch {
			case tok.Kind == jsTRegex:
				rgx++
			case tok.Kind == jsTPunct && tok.Val == "/":
				div++
			}
		}
		if rgx != 0 || div != 2 {
			t.Errorf("%s: regexes=%d divisions=%d, want 0 and 2", src, rgx, div)
		}
	}

	// The contextual keyword still introduces a regex where it really is one.
	rgx := 0
	for _, tok := range lexJS("for (const m of /a+/g.exec(s)) {}") {
		if tok.Kind == jsTRegex {
			rgx++
		}
	}
	if rgx != 1 {
		t.Errorf("for-of over a literal regex: regexes=%d, want 1", rgx)
	}
}

// "of" is the one entry in the keyword set that can also be a variable name,
// which a minifier may well produce. It opens a regex only where the for-of
// that gives it that meaning puts it, straight after the loop variable.
func TestLexJSOfIsAKeywordOnlyInAForOf(t *testing.T) {
	regexes := func(src string) int {
		n := 0
		for _, tok := range lexJS(src) {
			if tok.Kind == jsTRegex {
				n++
			}
		}
		return n
	}

	for _, src := range []string{"of / 2 / y", "var of = 1; of / 2 / y", "a = of / 2 / y"} {
		if n := regexes(src); n != 0 {
			t.Errorf("%s: %d regexes, want 0 - of is a variable here", src, n)
		}
	}
	// A binding can also be a destructuring pattern, which ends in a bracket
	// rather than a name.
	for _, src := range []string{
		"for (const m of /a+/g.exec(s)) {}",
		"for (const [m] of /a+/g.exec(s)) {}",
		"for (const {m} of /a+/g.exec(s)) {}",
	} {
		if n := regexes(src); n != 1 {
			t.Errorf("%s: %d regexes, want 1", src, n)
		}
	}
}

// A backslash before any line terminator is a continuation and produces
// nothing. Only the newline was known, so CRLF put a stray carriage return
// in the value and then ended the string early, and U+2028 or U+2029 left
// two bytes of their UTF-8 behind.
func TestLexJSLineContinuations(t *testing.T) {
	for name, term := range map[string]string{
		"lf":    "\n",
		"crlf":  "\r\n",
		"u2028": "\u2028",
		"u2029": "\u2029",
	} {
		src := "var s = 'ab\\" + term + "cd';"
		var got string
		for _, tok := range lexJS(src) {
			if tok.Kind == jsTString {
				got = tok.Val
			}
		}
		if got != "abcd" {
			t.Errorf("%s: string is %q, want abcd", name, got)
		}
	}
}

func TestParseJSNumber(t *testing.T) {
	for in, want := range map[string]int{
		"2": 2, "0x2": 2, "0X10": 16, "0o7": 7, "0b101": 5,
		"1_0": 10, "2.0": 2, "1e1": 10, "-4": -4, "3n": 3,
	} {
		got, err := parseJSNumber(in)
		if err != nil || got != want {
			t.Errorf("parseJSNumber(%q) = %d, %v; want %d", in, got, err, want)
		}
	}

	for _, in := range []string{"", "abc", "0xzz"} {
		if _, err := parseJSNumber(in); err == nil {
			t.Errorf("parseJSNumber(%q) accepted a non-number", in)
		}
	}
}
