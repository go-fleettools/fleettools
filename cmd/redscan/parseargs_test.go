package main

import (
	"bytes"
	"strings"
	"testing"
)

// A request for help, or anything this tool does not understand, must never
// reach the sweep: on 2026-10-11 `redscan -h` was one.
//
// ⛔ ONE EXPECTED SENTENCE MOVED WITH THE MIGRATION TO COBRA, and it is worth
// saying which and why. The pairs below -- the status and whether to proceed --
// are what this test is about, and every one of them is unchanged. The stderr
// strings are not all ours: "flag provided but not defined" was the standard
// `flag` package's wording, and pflag says "unknown shorthand flag: 'o' in
// -orgs" instead. Same refusal, same status, different words, so the
// expectation follows the words.
//
// "takes no arguments" is OURS and stayed: cobra.NoArgs would have said
// `unknown command "go-widgets" for "redscan"`, which describes the parser's
// model rather than the risk, so the Args function spells out what the refusal
// is for. That is the sentence an operator reads, and it is pinned here on
// purpose.
func TestOnlyNoArgumentsSweeps(t *testing.T) {
	for _, c := range []struct {
		args    []string
		code    int
		proceed bool
		says    string
	}{
		{nil, 0, true, ""},
		{[]string{}, 0, true, ""},
		{[]string{"-h"}, 0, false, "usage: redscan"},
		{[]string{"--help"}, 0, false, "usage: redscan"},
		{[]string{"-orgs", "go-widgets"}, 2, false, "unknown shorthand flag"},
		{[]string{"go-widgets"}, 2, false, "takes no arguments"},
		{[]string{"help"}, 2, false, "takes no arguments"},
	} {
		var stderr bytes.Buffer
		code, proceed := parseArgs(c.args, &stderr)
		if code != c.code || proceed != c.proceed {
			t.Errorf("%q: got (%d, %v), want (%d, %v)", c.args, code, proceed, c.code, c.proceed)
		}
		if c.says != "" && !strings.Contains(stderr.String(), c.says) {
			t.Errorf("%q: stderr %q does not say %q", c.args, stderr.String(), c.says)
		}
		if c.proceed && stderr.Len() != 0 {
			t.Errorf("%q: a clean start prints nothing, got %q", c.args, stderr.String())
		}
	}
}
