// Package copycheck is the engine of the copycheck gate: it decides whether a
// repository's source resembles the source of references it declared reading,
// in a way its declared roles and their licences do not allow.
//
// ⛔ THE RULE THIS TURNS INTO A GATE: copyleft reference code (ffmpeg, FAAD2...)
// may be READ to understand, never copied or translated, and its tables never
// copied verbatim. A rule nobody has to remember is a rule a program checks.
//
// Two measurements, because each is blind where the other sees:
//
//   - numeric tables: runs of numeric literals, normalised so that 0x10, 16 and
//     16.0f are one value, compared by winnowing. A copied or re-typed table
//     survives any change of language, formatting or variable name.
//   - code shape: token streams normalised across C and Go, compared by
//     winnowing (Schleimer, Wilkerson, Aiken, "Winnowing: Local Algorithms for
//     Document Fingerprinting", SIGMOD 2003, the algorithm behind MOSS). A C to
//     Go translation keeps the shape of the control flow and the spelling of
//     the names, and loses the punctuation.
//
// The lexer below is shared by both, and reads untrusted bytes: it never
// panics, never loops without consuming input, and is linear.
package copycheck

import (
	"math"
	"strconv"
	"strings"
)

// Kind is the class of a Token.
type Kind uint8

const (
	KIdent Kind = iota + 1
	KNum
	KStr
	KChar
	KPunct
)

// Token is one lexeme with the line it started on (1-based).
type Token struct {
	Kind Kind
	Text string
	Line int
}

var ops3 = map[string]bool{"<<=": true, ">>=": true, "...": true, "&^=": true}
var ops2 = map[string]bool{
	"->": true, "++": true, "--": true, "&&": true, "||": true, "==": true, "!=": true,
	"<=": true, ">=": true, "<<": true, ">>": true, ":=": true, "+=": true, "-=": true,
	"*=": true, "/=": true, "%=": true, "&=": true, "|=": true, "^=": true, "<-": true,
	"&^": true, "::": true,
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isIdentPart(c byte) bool {
	return isIdentStart(c) || isDigit(c)
}

// Lex splits C, C++ or Go source into tokens. Comments are dropped. Of the
// preprocessor only `#define` is kept (its body may hold a table); every other
// directive line is dropped. Strings and chars become one token each. Anything
// unterminated ends at the end of its line (strings, chars) or of the input
// (comments, raw strings), which bounds the damage of malformed input.
func Lex(src []byte) []Token {
	var out []Token
	line := 1
	bol := true // only whitespace so far on this line
	n := len(src)
	i := 0
	for i < n {
		c := src[i]
		switch {
		case c == '\n':
			line++
			bol = true
			i++
		case c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '\\' && i+1 < n && src[i+1] == '\n':
			line++
			i += 2
		case c == '\\' && i+2 < n && src[i+1] == '\r' && src[i+2] == '\n':
			line++
			i += 3
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				if src[i] == '\\' && i+1 < n && src[i+1] == '\n' { // continued comment
					line++
					i++
				}
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			i += 2
			for i < n && !(src[i] == '*' && i+1 < n && src[i+1] == '/') {
				if src[i] == '\n' {
					line++
				}
				i++
			}
			if i < n {
				i += 2
			}
			bol = false
		case c == '#' && bol:
			j := i + 1
			for j < n && (src[j] == ' ' || src[j] == '\t') {
				j++
			}
			k := j
			for k < n && isIdentPart(src[k]) {
				k++
			}
			if string(src[j:k]) == "define" {
				i = k // keep the rest of the line as ordinary tokens
				bol = false
				break
			}
			for i < n && src[i] != '\n' { // skip the directive, honouring continuations
				if src[i] == '\\' && i+1 < n && src[i+1] == '\n' {
					line++
					i++
				}
				i++
			}
		case c == '"':
			start := line
			i++
			for i < n && src[i] != '"' && src[i] != '\n' {
				if src[i] == '\\' && i+1 < n && src[i+1] != '\n' {
					i++
				}
				i++
			}
			if i < n && src[i] == '"' {
				i++
			}
			out = append(out, Token{KStr, "\"\"", start})
			bol = false
		case c == '`':
			start := line
			i++
			for i < n && src[i] != '`' {
				if src[i] == '\n' {
					line++
				}
				i++
			}
			if i < n {
				i++
			}
			out = append(out, Token{KStr, "``", start})
			bol = false
		case c == '\'':
			start := line
			i++
			for i < n && src[i] != '\'' && src[i] != '\n' {
				if src[i] == '\\' && i+1 < n && src[i+1] != '\n' {
					i++
				}
				i++
			}
			if i < n && src[i] == '\'' {
				i++
			}
			out = append(out, Token{KChar, "''", start})
			bol = false
		case isDigit(c) || (c == '.' && i+1 < n && isDigit(src[i+1])):
			start := i
			hex := c == '0' && i+1 < n && (src[i+1] == 'x' || src[i+1] == 'X')
			i++
			for i < n {
				d := src[i]
				if isIdentPart(d) && d < 0x80 || d == '.' {
					i++
					continue
				}
				if (d == '+' || d == '-') && i > start {
					p := src[i-1]
					if (!hex && (p == 'e' || p == 'E')) || (hex && (p == 'p' || p == 'P')) {
						i++
						continue
					}
				}
				break
			}
			out = append(out, Token{KNum, string(src[start:i]), line})
			bol = false
		case isIdentStart(c):
			start := i
			for i < n && isIdentPart(src[i]) {
				i++
			}
			out = append(out, Token{KIdent, string(src[start:i]), line})
			bol = false
		default:
			if i+3 <= n && ops3[string(src[i:i+3])] {
				out = append(out, Token{KPunct, string(src[i : i+3]), line})
				i += 3
			} else if i+2 <= n && ops2[string(src[i:i+2])] {
				out = append(out, Token{KPunct, string(src[i : i+2]), line})
				i += 2
			} else {
				out = append(out, Token{KPunct, string(src[i : i+1]), line})
				i++
			}
			bol = false
		}
	}
	return out
}

// Canon normalises a numeric literal so that the same VALUE has the same
// spelling whatever the language wrote: 0x10, 16, 020 (C octal), 0b10000,
// 16.0f, 1.6e1 and 1_6 (Go) are all "16". Integral floats collapse to the
// integer, so a table written with ints in C and floats in Go still matches.
// Other floats are rounded to 7 significant digits, the precision of a C
// `float`, so 0.70710678f and 0.70710678118654752 are the same number.
//
// ok is false for what is not a plain number (a Go imaginary, a malformed
// literal): the caller treats that as the end of a run, never as a value.
func Canon(text string) (canon string, ok bool) {
	s := strings.ToLower(strings.ReplaceAll(text, "_", ""))
	if s == "" {
		return "", false
	}
	hex := strings.HasPrefix(s, "0x")
	bin := strings.HasPrefix(s, "0b")
	oct := strings.HasPrefix(s, "0o")
	isFloat := strings.ContainsAny(s, ".") || (!hex && !bin && !oct && strings.Contains(s, "e")) || (hex && strings.Contains(s, "p"))
	if !isFloat {
		t := strings.TrimRight(s, "ul") // C integer suffixes; no digit of any base is u or l
		if t == "" {
			return "", false
		}
		if v, err := strconv.ParseUint(t, 0, 64); err == nil {
			return strconv.FormatUint(v, 10), true
		}
		// Wider than 64 bits is still a number: keep its spelling. Anything
		// else that failed (019 is not octal, 1f is not C) is not a number.
		if ne, ok := err2range(t); ok {
			return "big:" + ne, true
		}
		return "", false
	}
	t := s
	if !hex { // 'f' is a digit in hex, a suffix otherwise
		t = strings.TrimRight(t, "fl")
	} else {
		t = strings.TrimRight(t, "l")
	}
	v, err := strconv.ParseFloat(t, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return "", false
	}
	if v == math.Trunc(v) && math.Abs(v) < 1<<53 {
		return strconv.FormatInt(int64(v), 10), true
	}
	return strconv.FormatFloat(v, 'g', 7, 64), true
}

// err2range reports whether t is a well-formed integer literal that merely does
// not fit 64 bits, and returns it unchanged.
func err2range(t string) (string, bool) {
	_, err := strconv.ParseUint(t, 0, 64)
	if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
		return t, true
	}
	return "", false
}
