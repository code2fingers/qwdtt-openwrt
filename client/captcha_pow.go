// SPDX-License-Identifier: Apache-2.0
//
// Ported from TurnGuard (github.com/NikKuz99/turnguard), internal/core/
// pow_extract.go, Copyright (C) 2026 NikKuz99, licensed Apache-2.0.
// Apache-2.0 permits incorporation into a GPL-3.0-or-later work, and
// requires this notice to travel with it.
//
// Finding the proof-of-work seed in VK's captcha page by structure rather
// than by pattern.
//
// The regexes this sits in front of look for `const powInput = "..."`, which
// VK stopped emitting: the seed is now an argument to an obfuscated
// self-invoking function, and the obfuscator rewrites how that call is
// written from time to time. Layers, first match wins:
//
//	L1a  Lex the inline scripts, find the string "pow_timeout", recover the
//	     argument list of the call containing it, and classify the arguments
//	     by type. Blind to quote style, spacing and hex numbers, which is
//	     what broke the regexes twice upstream.
//	L1b  The same walk without relying on that marker: a call qualifies when
//	     it carries an array of known telemetry component names, exactly one
//	     seed-shaped string and a small difficulty number. Survives the
//	     marker being renamed.
//	L1c  Both passes again over the whole page rather than the inline
//	     scripts. The seed has always been in a script, but this is free.
//	L1d  All of the above again after unescaping HTML entities.
//
// Not attempted, and worth knowing before anyone tries: running the
// obfuscated script in an embedded JS engine works under node but goja has
// no async/await and quickjs needs cgo, which the OpenWrt cross-builds do
// not have. Worth revisiting only if VK changes what the proof of work
// computes rather than how the seed is written down.

package main

import (
	"regexp"
	"strconv"
	"strings"
)

// powSeed is the classified argument list of the seed call.
type powSeed struct {
	PowInput   string
	Difficulty int    // 0 when the call carries none; the caller keeps its own
	Marker     string // the literal that identified the call, when there was one
	Source     string // which layer matched, for the log
}

// Telemetry component names seen in the obfuscator's output. A fingerprint
// rather than a schema: L1b wants several of them, so VK adding or renaming
// one does not stop extraction.
var knownPowComponents = map[string]bool{
	"frame": true, "cookie_test": true, "referrer": true, "nav_tamper": true,
	"timezone_locale": true, "plugins": true, "devtools": true, "ua": true,
	"shadow_dom_check": true, "env_viewport": true, "device_pixel_ratio": true,
	"native_integrity": true, "match_media": true, "env_ua": true,
	"max_touch_points": true, "chrome_runtime": true, "media_codecs": true,
	"ancestor_origins": true, "globals": true, "css": true,
	"sandbox_behavior": true, "origin_via_self": true, "font": true,
	"webgl": true, "canvas": true, "audio": true, "battery": true,
	"speech": true, "notification": true, "permissions": true,
}

// What VK has used as a seed: a short base62-ish token.
var powInputShape = regexp.MustCompile(`^[A-Za-z0-9_\-]{6,64}$`)

// Inline script bodies. A script with a src has an empty body and drops out.
var inlineScriptRe = regexp.MustCompile(`(?is)<script[^>]*>(.*?)</script>`)

func extractPowSeed(html string) (*powSeed, bool) {
	for _, pass := range []struct{ name, input string }{
		{"raw", html},
		{"unescaped", htmlUnescapeForPow(html)},
	} {
		var bodies []string
		for _, m := range inlineScriptRe.FindAllStringSubmatch(pass.input, -1) {
			if strings.TrimSpace(m[1]) != "" {
				bodies = append(bodies, m[1])
			}
		}
		if seed, ok := extractPowFromBodies(bodies, pass.name+"/inline"); ok {
			return seed, true
		}
		if seed, ok := extractPowFromBodies([]string{pass.input}, pass.name+"/full"); ok {
			return seed, true
		}
	}
	return nil, false
}

func extractPowFromBodies(bodies []string, label string) (*powSeed, bool) {
	for _, body := range bodies {
		toks := lexJS(body)
		for k, t := range toks {
			if (t.Kind == jsTString || (t.Kind == jsTTemplate && t.Val != "")) && t.Val == "pow_timeout" {
				if seed, ok := classifyCallAround(toks, k, label+"/tokenizer-marker", true); ok {
					return seed, true
				}
			}
		}
		for k, t := range toks {
			if t.Kind == jsTPunct && t.Val == "(" {
				if seed, ok := classifyCallAround(toks, k, label+"/tokenizer-structure", false); ok {
					return seed, true
				}
			}
		}
	}
	return nil, false
}

// classifyCallAround inspects the argument list related to token k.
//
// With markerMode, k is the "pow_timeout" string and the call opener is
// found by walking back; the marker alone is not enough, so a components
// array or a difficulty number has to corroborate it. Without markerMode, k
// is a "(" and only the shape of the arguments decides.
func classifyCallAround(toks []jsToken, k int, source string, markerMode bool) (*powSeed, bool) {
	open := k
	if markerMode {
		open = findEnclosingCallOpen(toks, k)
		if open < 0 {
			return nil, false
		}
	}

	args, closed := parseCallArgs(toks, open)
	if !closed || len(args) < 3 || len(args) > 6 {
		return nil, false
	}

	seed := &powSeed{Source: source}

	var strArgs, arrayArg []string
	hasArray := false
	difficulty := 0

	for _, arg := range args {
		switch {
		case len(arg) == 1 && arg[0].Kind == jsTString:
			strArgs = append(strArgs, arg[0].Val)
		case len(arg) == 1 && arg[0].Kind == jsTTemplate && arg[0].Val != "":
			strArgs = append(strArgs, arg[0].Val)
		case len(arg) == 1 && arg[0].Kind == jsTNumber:
			if v, err := parseJSNumber(arg[0].Val); err == nil && v >= 1 && v <= 16 {
				if difficulty == 0 {
					difficulty = v
				}
			}
		case len(arg) >= 2 && arg[0].Kind == jsTPunct && arg[0].Val == "[" &&
			arg[len(arg)-1].Kind == jsTPunct && arg[len(arg)-1].Val == "]":
			if hasArray {
				return nil, false // two arrays, so not the call we want
			}
			hasArray = true
			for _, t := range arg[1 : len(arg)-1] {
				if t.Kind == jsTString || (t.Kind == jsTTemplate && t.Val != "") {
					arrayArg = append(arrayArg, t.Val)
				}
			}
		default:
			return nil, false // a function, an object, an expression: not it
		}
	}

	var seedCandidates []string
	for _, s := range strArgs {
		low := strings.ToLower(s)
		if strings.Contains(low, "pow") || strings.Contains(low, "timeout") || strings.Contains(low, "seed") {
			if seed.Marker == "" {
				seed.Marker = s
			}
			continue
		}
		if powInputShape.MatchString(s) {
			seedCandidates = append(seedCandidates, s)
		}
	}

	known := 0
	for _, c := range arrayArg {
		if knownPowComponents[c] {
			known++
		}
	}

	if markerMode {
		// The marker is a strong signal but not a sufficient one: a call that
		// merely mentions it, f('pow_timeout'), must not qualify.
		if !hasArray && difficulty == 0 {
			return nil, false
		}
	} else if !hasArray || known < 4 {
		return nil, false
	}

	if len(seedCandidates) != 1 {
		return nil, false
	}
	// In marker mode the page is obfuscated, so there is no `const difficulty`
	// to fall back to. A call carrying an array but no number in 1..16 leaves
	// difficulty at 0, which cannot be solved; report no seed so the caller
	// tries another path rather than failing the page on a seed it cannot use.
	if markerMode && difficulty == 0 {
		return nil, false
	}
	seed.PowInput = seedCandidates[0]
	seed.Difficulty = difficulty
	return seed, true
}

// findEnclosingCallOpen walks back from k to the "(" that opens the argument
// list holding it, or -1 when the token is not a direct argument of a call:
// inside an array or object literal, or in a function body.
func findEnclosingCallOpen(toks []jsToken, k int) int {
	depth := 0
	for j := k - 1; j >= 0; j-- {
		t := toks[j]
		if t.Kind != jsTPunct {
			continue
		}
		switch t.Val {
		case ")", "]", "}":
			depth++
		case "(", "[", "{":
			if depth == 0 {
				if t.Val == "(" && j > 0 {
					// What precedes "(" separates a call from a grouping
					// paren: ")" for a wrapped function expression, "}" for a
					// bare one, or an identifier for an ordinary call.
					p := toks[j-1]
					if (p.Kind == jsTPunct && (p.Val == ")" || p.Val == "}")) || p.Kind == jsTIdent {
						return j
					}
				}
				return -1
			}
			depth--
		}
	}
	return -1
}

// parseCallArgs splits the top-level comma groups of the argument list
// opening at toks[open], and reports whether it closed properly.
func parseCallArgs(toks []jsToken, open int) ([][]jsToken, bool) {
	if open >= len(toks) || toks[open].Kind != jsTPunct || toks[open].Val != "(" {
		return nil, false
	}
	var args [][]jsToken
	var cur []jsToken
	depth := 0
	for i := open + 1; i < len(toks); i++ {
		t := toks[i]
		if t.Kind == jsTPunct {
			switch t.Val {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				if depth == 0 {
					if t.Val == ")" {
						if len(cur) > 0 || len(args) > 0 {
							args = append(args, cur)
						}
						return args, true
					}
					return nil, false // "]" or "}" closing here means malformed
				}
				depth--
			case ",":
				if depth == 0 {
					args = append(args, cur)
					cur = nil
					continue
				}
			case ";":
				if depth == 0 {
					return nil, false // a statement, not an argument list
				}
			}
		} else if t.Kind == jsTIdent && depth == 0 && len(cur) == 0 {
			switch t.Val {
			case "function":
				// allowed: the head of a wrapped function expression
			case "return", "var", "let", "const", "if", "for", "while", "do",
				"switch", "try", "typeof", "new", "delete", "void", "class",
				"import", "export", "yield", "await", "throw", "else", "case":
				return nil, false // a keyword, so this is not a literal arg list
			}
		}
		cur = append(cur, t)
	}
	return nil, false // unterminated
}

// parseJSNumber reads what JavaScript accepts as a numeric literal: 0x, 0o
// and 0b prefixes, underscores, floats, exponents and the BigInt suffix.
func parseJSNumber(s string) (int, error) {
	s = strings.ReplaceAll(s, "_", "")
	if strings.HasSuffix(s, "n") || strings.HasSuffix(s, "N") {
		s = s[:len(s)-1]
	}
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}

	var v int64
	var err error
	switch {
	case strings.HasPrefix(s, "0x"), strings.HasPrefix(s, "0X"):
		v, err = strconv.ParseInt(s[2:], 16, 64)
	case strings.HasPrefix(s, "0o"), strings.HasPrefix(s, "0O"):
		v, err = strconv.ParseInt(s[2:], 8, 64)
	case strings.HasPrefix(s, "0b"), strings.HasPrefix(s, "0B"):
		v, err = strconv.ParseInt(s[2:], 2, 64)
	case strings.ContainsAny(s, ".eE"):
		var f float64
		if f, err = strconv.ParseFloat(s, 64); err == nil {
			v = int64(f)
		}
	default:
		v, err = strconv.ParseInt(s, 10, 64)
	}
	if err != nil {
		return 0, err
	}

	if neg {
		v = -v
	}
	return int(v), nil
}

// The entities that turn up in inline scripts on an XHTML-ish captcha page.
var powHTMLUnescaper = strings.NewReplacer(
	"&quot;", `"`,
	"&#34;", `"`,
	"&#x22;", `"`,
	"&amp;", "&",
	"&#x27;", "'",
	"&#39;", "'",
)

func htmlUnescapeForPow(s string) string { return powHTMLUnescaper.Replace(s) }
