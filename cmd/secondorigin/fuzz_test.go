package main

import "testing"

// The shell is a page this command does not control: whatever it serves, the
// walk must end, and the CSP normal form must be a fixed point.
func FuzzShellAndCSP(f *testing.F) {
	for _, s := range []string{
		"",
		`<meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'self'"><script src="a.js"></script>`,
		"var a = \"https://a.example\"; // \"x\"\n/* 'y' */ postMessage(m, '*')",
		"`unterminated", "'\\", "/*", "\"\n\"", "default-src;;; ; a b c",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, l := range stringLiterals(s) {
			originOf(l)
		}
		p := parseShell([]byte(s))
		for _, c := range p.csp {
			normCSP(c)
		}
		n := normCSP(s)
		if normCSP(n) != n {
			t.Fatalf("normCSP is not a fixed point: %q -> %q -> %q", s, n, normCSP(n))
		}
		wildcardPost.MatchString(s)
		messageGuards(s)
	})
}
