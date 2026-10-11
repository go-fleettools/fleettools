package main

import (
	"regexp"
	"strings"
)

// Ref is one Go identifier a README names.
//
//	Pkg  == ""  a bare name in an inline code span: `LoadGTKTheme(css)`,
//	            `Entry.Text` -- resolved against the module's own packages
//	Pkg  != ""  a qualified name: `toolkit.NewButton`, `window.Config.Title`
type Ref struct {
	Line   int
	Pkg    string
	Name   string
	Member string // the part after Name, when the text names one
	Text   string // what the README actually says, for the report
}

// qualifiedRE finds pkg.Name and pkg.Name.Member. The package part is
// lower-case and the name exported: that is the shape of a reference to a
// package's API. Whether `pkg` really IS a package is decided later, against
// the packages actually loaded -- `w.Run(box)` names a variable, not a package,
// and is dropped then, not here.
var qualifiedRE = regexp.MustCompile(`\b([a-z][a-z0-9]*)\.([A-Z][A-Za-z0-9]*)(?:\.([A-Z][A-Za-z0-9]*))?`)

// bareRE is an inline code span that is exactly one exported name, possibly
// with a member and a call: `LoadGTKTheme(css)`, `Entry.Text`, `Label.Text()`.
// Anything else in a span -- a command, a type expression, a sentence -- is not
// a reference this tool can check, and is left alone.
var bareRE = regexp.MustCompile(`^\*?([A-Z][A-Za-z0-9]*)(?:\.([A-Z][A-Za-z0-9]*))?(?:\(.*\))?$`)

var spanRE = regexp.MustCompile("`([^`\n]+)`")

// allowRE is the one way to tell docsym a name is meant: an HTML comment, so
// nothing renders, naming the references and WHY.
//
//	<!-- docsym:allow mvvm.Queue mvvm.NewQueue -- an application's code: needs mvvm v0.14.0, window builds against older -->
//
// ⛔ The reason is mandatory, as bricolint's is: an allow with no reason is
// ignored, and the reference keeps being reported until somebody writes down
// why it is right.
var allowRE = regexp.MustCompile(`<!--\s*docsym:allow\s+(.*?)\s+--\s+(\S.*?)\s*-->`)

// Allowed returns the references a README exempts, each with its reason.
func Allowed(readme string) map[string]string {
	out := map[string]string{}
	for _, m := range allowRE.FindAllStringSubmatch(readme, -1) {
		for _, name := range strings.Fields(m[1]) {
			out[name] = m[2]
		}
	}
	return out
}

// Extract returns every reference in a README, with its line number.
//
// It reads inline code spans everywhere, and whole lines inside ```go fences
// (where a qualified name is the only kind looked for: a bare `Foo` inside
// code is as likely a local type the example declares). Other fences -- sh,
// yaml, json, text -- are skipped: their dots are not Go selectors.
func Extract(readme string) []Ref {
	var refs []Ref
	inFence, goFence := false, false
	for i, line := range strings.Split(readme, "\n") {
		n := i + 1
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if !inFence {
				inFence = true
				lang := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "```")))
				goFence = lang == "go"
			} else {
				inFence, goFence = false, false
			}
			continue
		}
		if inFence {
			if goFence {
				code := line
				if j := strings.Index(code, "//"); j >= 0 {
					code = code[:j] // a comment's prose is not a reference
				}
				refs = append(refs, qualified(n, code)...)
			}
			continue
		}
		for _, m := range spanRE.FindAllStringSubmatch(line, -1) {
			span := strings.TrimSpace(m[1])
			if q := qualified(n, span); len(q) > 0 {
				refs = append(refs, q...)
				continue
			}
			if b := bareRE.FindStringSubmatch(span); b != nil && plausibleName(b[1]) {
				refs = append(refs, Ref{Line: n, Name: b[1], Member: b[2], Text: span})
			}
		}
	}
	return refs
}

func qualified(line int, s string) []Ref {
	var out []Ref
	for _, m := range qualifiedRE.FindAllStringSubmatchIndex(s, -1) {
		// "github.com/go-widgets/toolkit" and "pkg.go.dev" are not selectors:
		// the character before a selector is never a slash, a dot or a hyphen.
		if m[0] > 0 && strings.ContainsRune("/.-", rune(s[m[0]-1])) {
			continue
		}
		r := Ref{Line: line, Pkg: s[m[2]:m[3]], Name: s[m[4]:m[5]], Text: s[m[0]:m[1]]}
		if m[6] >= 0 {
			r.Member = s[m[6]:m[7]]
		}
		out = append(out, r)
	}
	return out
}

// plausibleName drops the all-capital words a code span also holds -- `CGO`,
// `RGBA_8888`, `GOOS` -- which are not Go identifiers of this module but would
// otherwise be reported as missing ones. The cost is that an all-capital Go
// name (`RGBA`) is not checked when it stands alone.
func plausibleName(s string) bool {
	return strings.ToUpper(s) != s && !strings.Contains(s, "_")
}
