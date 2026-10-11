package main

import (
	"bytes"
	"strings"
	"testing"
)

// A request for help, or anything this tool does not understand, must never
// reach the sweep: on 2026-10-11 `redscan -h` was one.
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
		{[]string{"-orgs", "go-widgets"}, 2, false, "flag provided but not defined"},
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
