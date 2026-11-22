// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import "strings"

// maxTargets bounds how many redirect targets one command can contribute.
// A command line is attacker-adjacent only in the sense that it is MY OWN
// text, but a hook that opens an unbounded number of files is a hook that
// times out, and a timed-out hook reports nothing — the failure mode this
// whole tool exists to refuse.
var maxTargets = 16

// maxCommandBytes bounds the command text the parser will walk.
var maxCommandBytes = 1 << 16

// redirectTargets returns the files a shell command redirects output into, in
// order and without duplicates.
//
// It recognises `>`, `>>`, `1>`, `1>>`, `2>`, `2>>`, `&>`, `&>>` and `>|`,
// and deliberately does NOT treat `2>&1`, `>&2` or `<` as a file: the first
// two name a descriptor and the third reads.
//
// ⛔ This is a reader of my own command lines, not a shell. It does not expand
// `$VAR`, `~` or globs, so a target spelled with one is reported as unresolved
// rather than silently dropped — see unresolved().
func redirectTargets(command string) []string {
	if len(command) > maxCommandBytes {
		command = command[:maxCommandBytes]
	}
	toks := tokens(command)
	var out []string
	seen := map[string]bool{}
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if !isRedirect(t.text) || t.quoted {
			continue
		}
		// `2>&1` and `>&2` arrive as one token; descriptor, not a file.
		if strings.Contains(t.text, "&") && strings.HasSuffix(t.text, "&") {
			continue
		}
		if rest := afterOperator(t.text); rest != "" {
			out = appendTarget(out, seen, rest)
			continue
		}
		if i+1 < len(toks) {
			nxt := toks[i+1]
			// A QUOTED word is a file name whatever it looks like: `> ">x"`
			// names a file, `> >x` is syntax.
			if nxt.text == "" || (!nxt.quoted && (isRedirect(nxt.text) || isSeparator(nxt.text))) {
				continue
			}
			// ⛔ An unterminated quote means this reader GUESSED where the word
			// ended, and a shell would have refused the command outright — so
			// there is no output file to read. Fuzz finding, from `> 2">`,
			// which otherwise named a file `2>`.
			if nxt.broken {
				continue
			}
			out = appendTarget(out, seen, nxt.text)
			i++
		}
		// ⛔ No `if len(out) >= maxTargets { return }` fast path here:
		// appendTarget already refuses past the bound, so the early return was
		// a branch no input could distinguish from its absence — a mutation
		// survivor by construction, and a line that would read as the bound
		// while the real bound lived elsewhere. maxCommandBytes bounds the walk.
	}
	return out
}

// appendTarget adds a target once, dropping descriptor words and anything that
// is itself only shell syntax.
//
// ⛔ The fuzz finding behind this (`cmd >>>> x` reporting the string `>>` as a
// FILE NAME) is fixed in afterOperator, which strips the operator characters it
// did not consume. A second `strings.Trim(name, ">|&") == ""` guard stood here
// for a while and survived mutation: the only input it could still refuse was a
// QUOTED name made of operator characters, `cmd > '>>'`, and that names a real
// file. Rejecting it would have been this reader inventing a rule bash does not
// have, held by no test — so it is gone rather than tested.
func appendTarget(out []string, seen map[string]bool, name string) []string {
	if name == "" || strings.HasPrefix(name, "&") || seen[name] || len(out) >= maxTargets {
		return out
	}
	seen[name] = true
	return append(out, name)
}

// unresolved reports whether a target still holds shell syntax this parser
// does not expand, so the caller can SAY it did not read it instead of
// counting the file as clean.
func unresolved(name string) bool {
	return strings.ContainsAny(name, "$*?~`")
}

// redirOps are the output-redirection operators, LONGEST FIRST.
//
// ⛔ The order and the single list are both load-bearing. With two lists and a
// `len(t) > len(p)` guard, `>>` failed its own entry (2 is not > 2) and fell
// through to `>`, whose remainder is the string ">" — which was then reported
// as a FILE NAME. Caught by TestRedirectTargets: `cmd >> a.log` answered [>].
var redirOps = []string{"&>>", "&>", "2>>", "2>", "1>>", "1>", ">>", ">|", ">"}

// isRedirect reports whether a token begins an output redirection, alone or
// with a file glued to it (`>out.txt`, `2>>log`).
func isRedirect(t string) bool {
	for _, p := range redirOps {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// afterOperator returns the file glued to a redirect operator, or "" when the
// token is the bare operator. Leading operator characters are stripped, so
// `>>>x` yields `x` rather than `>x`: this only ever sees UNQUOTED text, and a
// file whose name really begins with `>` can only be written quoted, which
// arrives as a separate token and never comes through here.
func afterOperator(t string) string {
	for _, p := range redirOps {
		if strings.HasPrefix(t, p) {
			return strings.TrimLeft(t[len(p):], ">|")
		}
	}
	return ""
}

// isSeparator reports whether a token ends a command rather than naming a file.
func isSeparator(t string) bool {
	switch t {
	case ";", "&&", "||", "|", "&", "\n":
		return true
	}
	return false
}

// token is one shell word, with whether quoting produced it and whether the
// quote that produced it was ever closed.
type token struct {
	text   string
	quoted bool
	broken bool
}

// tokens splits a command line into words, honouring single quotes, double
// quotes and backslash escapes, and keeping operators as words of their own.
func tokens(s string) []token {
	var out []token
	var cur strings.Builder
	wasQuoted := false
	wasBroken := false
	have := false
	flush := func() {
		if have {
			out = append(out, token{text: cur.String(), quoted: wasQuoted, broken: wasBroken})
			cur.Reset()
			wasQuoted = false
			wasBroken = false
			have = false
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case ' ', '\t', '\n':
			flush()
		case '\\':
			if i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
				have = true
			}
		case '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				cur.WriteString(s[i+1:])
				have, wasQuoted, wasBroken = true, true, true
				i = len(s)
				break
			}
			cur.WriteString(s[i+1 : i+1+j])
			have, wasQuoted = true, true
			i += j + 1
		case '"':
			j := strings.IndexByte(s[i+1:], '"')
			if j < 0 {
				cur.WriteString(s[i+1:])
				have, wasQuoted, wasBroken = true, true, true
				i = len(s)
				break
			}
			cur.WriteString(s[i+1 : i+1+j])
			have, wasQuoted = true, true
			i += j + 1
		case '>', ';', '|', '&':
			// An operator begins its own word, and `>out` stays glued so
			// afterOperator can split it.
			if c == '>' {
				flush()
				cur.WriteByte(c)
				have = true
				for i+1 < len(s) && (s[i+1] == '>' || s[i+1] == '|' || s[i+1] == '&') {
					i++
					cur.WriteByte(s[i])
				}
				continue
			}
			flush()
			cur.WriteByte(c)
			for i+1 < len(s) && s[i+1] == c {
				i++
				cur.WriteByte(c)
			}
			have = true
			flush()
		default:
			cur.WriteByte(c)
			have = true
		}
	}
	flush()
	return merged(out)
}

// merged glues a descriptor digit that precedes a redirect operator, so `2 >`
// never happens but `cmd 2>f` tokenises as one word.
func merged(in []token) []token {
	var out []token
	for i := 0; i < len(in); i++ {
		t := in[i]
		if (t.text == "1" || t.text == "2") && i+1 < len(in) && !in[i+1].quoted && strings.HasPrefix(in[i+1].text, ">") {
			out = append(out, token{text: t.text + in[i+1].text})
			i++
			continue
		}
		out = append(out, t)
	}
	return out
}
