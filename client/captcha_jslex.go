// SPDX-License-Identifier: Apache-2.0
//
// Ported from TurnGuard (github.com/NikKuz99/turnguard), internal/core/
// jstoken.go, Copyright (C) 2026 NikKuz99, licensed Apache-2.0. Apache-2.0
// permits incorporation into a GPL-3.0-or-later work, and requires this
// notice to travel with it.
//
// A minimal JavaScript lexer, here to read one thing out of VK's captcha
// page: the proof-of-work seed.
//
// VK serves the seed as arguments to an obfuscated self-invoking function in
// an inline script. The obfuscator changes how that call is WRITTEN - quote
// style, spacing, hex against decimal numbers - while what it MEANS stays
// put:
//
//	(function(_0xa,_0xb,_0xc,_0xd){ ... })(
//	    "<powInput>", <difficulty>, "pow_timeout", ["frame","cookie_test",...]
//	);
//
// Two rewrites of that kind broke regex extraction upstream, once when the
// seed moved into the call arguments and once when the quotes flipped from
// double to single. A regex encodes the writing; a token stream encodes the
// structure, and the structure is what has held.
//
// Best-effort by design: it never fails, it runs out of input. Only strings,
// numbers and punctuation have to be right, which keeps it on the standard
// library with no cgo - the client cross-compiles to every OpenWrt target.

package main

// jsTokenKind classifies tokens produced by lexJS.
type jsTokenKind int

const (
	jsTString   jsTokenKind = iota // '...' or "..." with escapes resolved
	jsTNumber                      // 12, 0x2, 3.5, 1e3, 0n
	jsTIdent                       // identifier or keyword
	jsTPunct                       // operator / punctuation
	jsTTemplate                    // `...`
	jsTRegex                       // /.../ flags
)

// jsToken is one lexical token. Pos is the byte offset of the token start in
// the source, for diagnostics. For strings Val holds the escape-resolved
// value; for everything else it is the literal source text.
type jsToken struct {
	Kind jsTokenKind
	Val  string
	Pos  int
}

// Multi-character operators, longest first, so the lexer takes the longest
// match rather than the first.
var jsPunctOps = []string{
	">>>=", "...", "===", "!==", "**=", "<<=", ">>=", ">>>", "&&=", "||=", "??=",
	"=>", "==", "!=", "<=", ">=", "&&", "||", "??", "?.", "++", "--",
	"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "<<", ">>",
}

// Keywords after which a '/' opens a regular expression rather than dividing.
// The standard lexer heuristic, and the only way to tell the two apart
// without parsing.
var jsRegexPrecKw = map[string]bool{
	"return": true, "typeof": true, "instanceof": true, "in": true, "of": true,
	"new": true, "delete": true, "void": true, "case": true, "do": true,
	"else": true, "throw": true, "yield": true, "await": true,
}

// lexJS tokenizes src, skipping comments and whitespace. Anything unexpected
// becomes a single-character punctuation token, so a malformed region turns
// into junk tokens rather than ending the stream.
func lexJS(src string) []jsToken {
	var toks []jsToken
	i, n := 0, len(src)

	prevIsRegexPos := func() bool {
		if len(toks) == 0 {
			return true // start of input
		}
		last := toks[len(toks)-1]
		switch last.Kind {
		case jsTIdent:
			// A keyword after a dot is a property name. Minified code is full
			// of short ones, and reading x.of as the keyword puts this in
			// regex position, where the division that follows swallows the
			// rest of the line - and any call in it - into one regex token.
			if len(toks) >= 2 {
				if p := toks[len(toks)-2]; p.Kind == jsTPunct && (p.Val == "." || p.Val == "?.") {
					return false
				}
			}
			// "of" is the one entry that can also be a variable name, so it
			// is the keyword only after a binding: a name, or the close of a
			// destructuring pattern.
			if last.Val == "of" {
				if len(toks) < 2 {
					return false
				}
				p := toks[len(toks)-2]
				return p.Kind == jsTIdent ||
					(p.Kind == jsTPunct && (p.Val == "]" || p.Val == "}"))
			}
			return jsRegexPrecKw[last.Val]
		case jsTNumber, jsTString, jsTTemplate, jsTRegex:
			return false
		case jsTPunct:
			switch last.Val {
			case ")", "]", "}", "++", "--":
				return false
			default:
				return true
			}
		}
		return true
	}

	for i < n {
		c := src[i]

		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f' {
			i++
			continue
		}

		if c == '/' && i+1 < n {
			if src[i+1] == '/' {
				for i < n && src[i] != '\n' {
					i++
				}
				continue
			}
			if src[i+1] == '*' {
				i += 2
				for i+1 < n && !(src[i] == '*' && src[i+1] == '/') {
					i++
				}
				if i+1 < n {
					i += 2
				} else {
					i = n
				}
				continue
			}
		}

		start := i

		if c == '\'' || c == '"' {
			quote := c
			i++
			var b []byte
			for i < n {
				ch := src[i]
				if ch == quote {
					i++
					break
				}
				if ch == '\\' && i+1 < n {
					i++
					switch esc := src[i]; esc {
					case 'n':
						b = append(b, '\n')
					case 't':
						b = append(b, '\t')
					case 'r':
						b = append(b, '\r')
					case 'b':
						b = append(b, '\b')
					case 'f':
						b = append(b, '\f')
					case 'v':
						b = append(b, '\v')
					case '0':
						b = append(b, 0)
					case 'x':
						if i+2 < n {
							if v, ok := parseHexPair(src[i+1], src[i+2]); ok {
								b = append(b, v)
								i += 2
							} else {
								b = append(b, 'x')
							}
						} else {
							b = append(b, 'x')
						}
					case 'u':
						if i+4 < n && isHexDigit(src[i+1]) && isHexDigit(src[i+2]) &&
							isHexDigit(src[i+3]) && isHexDigit(src[i+4]) {
							v := 0
							for _, d := range []byte{src[i+1], src[i+2], src[i+3], src[i+4]} {
								v = v*16 + hexVal(d)
							}
							b = append(b, []byte(string(rune(v)))...)
							i += 4
						} else if i+1 < n && src[i+1] == '{' {
							j := i + 2
							v := 0
							for j < n && isHexDigit(src[j]) {
								v = v*16 + hexVal(src[j])
								j++
							}
							if j < n && src[j] == '}' && v < 0x110000 {
								b = append(b, []byte(string(rune(v)))...)
								i = j
							} else {
								b = append(b, 'u')
							}
						} else {
							b = append(b, 'u')
						}
					case '\n':
						// line continuation: produces nothing
					case '\r':
						// CRLF is one line terminator. Taken as two, the \r landed in
						// the value and the \n after it ended the string as though it
						// were unterminated.
						if i+1 < n && src[i+1] == '\n' {
							i++
						}
					case 0xE2:
						// U+2028 and U+2029 terminate a line as well, and are three
						// bytes each in UTF-8. Left alone they put a stray two bytes
						// into the value.
						if i+2 < n && src[i+1] == 0x80 &&
							(src[i+2] == 0xA8 || src[i+2] == 0xA9) {
							i += 2
						} else {
							b = append(b, esc)
						}
					default:
						b = append(b, esc)
					}
					i++
					continue
				}
				if ch == '\n' {
					break // unterminated at a newline, tolerate and stop
				}
				b = append(b, ch)
				i++
			}
			toks = append(toks, jsToken{Kind: jsTString, Val: string(b), Pos: start})
			continue
		}

		// Template literal. With no ${...} substitution its text is carried in
		// Val so the extractor can treat it as a string; with one, Val holds
		// only the literal parts, which will not match a seed shape.
		if c == '`' {
			i++
			depth := 0
			var b []byte
			for i < n {
				ch := src[i]
				if ch == '\\' && i+1 < n {
					if depth == 0 && src[i+1] != '`' {
						b = append(b, src[i+1])
					}
					i += 2
					continue
				}
				if depth == 0 && ch == '`' {
					i++
					break
				}
				if ch == '$' && i+1 < n && src[i+1] == '{' {
					depth++
					i += 2
					continue
				}
				if depth > 0 && ch == '{' {
					depth++
				}
				if depth > 0 && ch == '}' {
					depth--
				}
				if depth == 0 {
					b = append(b, ch)
				}
				i++
			}
			toks = append(toks, jsToken{Kind: jsTTemplate, Val: string(b), Pos: start})
			continue
		}

		if isDecDigit(c) || (c == '.' && i+1 < n && isDecDigit(src[i+1])) {
			j := i
			if c == '0' && i+1 < n {
				switch src[i+1] {
				case 'x', 'X':
					j = i + 2
					for j < n && (isHexDigit(src[j]) || src[j] == '_') {
						j++
					}
				case 'o', 'O':
					j = i + 2
					for j < n && (isOctDigit(src[j]) || src[j] == '_') {
						j++
					}
				case 'b', 'B':
					j = i + 2
					for j < n && (src[j] == '0' || src[j] == '1' || src[j] == '_') {
						j++
					}
				}
			}
			if j == i { // decimal
				for j < n && (isDecDigit(src[j]) || src[j] == '_') {
					j++
				}
				if j < n && src[j] == '.' {
					j++
					for j < n && (isDecDigit(src[j]) || src[j] == '_') {
						j++
					}
				}
				if j < n && (src[j] == 'e' || src[j] == 'E') {
					k := j + 1
					if k < n && (src[k] == '+' || src[k] == '-') {
						k++
					}
					if k < n && isDecDigit(src[k]) {
						j = k
						for j < n && isDecDigit(src[j]) {
							j++
						}
					}
				}
			}
			if j < n && (src[j] == 'n' || src[j] == 'N') { // BigInt suffix
				j++
			}
			toks = append(toks, jsToken{Kind: jsTNumber, Val: src[i:j], Pos: start})
			i = j
			continue
		}

		if isIdentStart(rune(c)) || c >= 0x80 {
			j := i
			for j < n && (src[j] >= 0x80 || isIdentPart(rune(src[j]))) {
				j++
			}
			toks = append(toks, jsToken{Kind: jsTIdent, Val: src[i:j], Pos: start})
			i = j
			continue
		}

		if c == '/' {
			if prevIsRegexPos() {
				j := i + 1
				inClass := false
				okRegex := false
				for j < n {
					ch := src[j]
					if ch == '\\' && j+1 < n {
						j += 2
						continue
					}
					if ch == '\n' {
						break // a regex cannot span lines, so this was division
					}
					if inClass {
						if ch == ']' {
							inClass = false
						}
					} else if ch == '[' {
						inClass = true
					} else if ch == '/' {
						j++
						for j < n && isIdentPart(rune(src[j])) { // flags
							j++
						}
						okRegex = true
						break
					}
					j++
				}
				if okRegex {
					toks = append(toks, jsToken{Kind: jsTRegex, Val: src[i:j], Pos: start})
					i = j
					continue
				}
			}
			toks = append(toks, jsToken{Kind: jsTPunct, Val: "/", Pos: start})
			i++
			continue
		}

		matched := false
		for _, op := range jsPunctOps {
			if i+len(op) <= n && src[i:i+len(op)] == op {
				toks = append(toks, jsToken{Kind: jsTPunct, Val: op, Pos: start})
				i += len(op)
				matched = true
				break
			}
		}
		if matched {
			continue
		}

		toks = append(toks, jsToken{Kind: jsTPunct, Val: string(c), Pos: start})
		i++
	}

	return toks
}

func isDecDigit(b byte) bool { return b >= '0' && b <= '9' }
func isOctDigit(b byte) bool { return b >= '0' && b <= '7' }

func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func hexVal(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	default:
		return int(b-'A') + 10
	}
}

func parseHexPair(a, b byte) (byte, bool) {
	if !isHexDigit(a) || !isHexDigit(b) {
		return 0, false
	}
	return byte(hexVal(a)*16 + hexVal(b)), true
}

func isIdentStart(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || r == '$'
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || (r >= '0' && r <= '9')
}
