// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

// FuzzRedirectTargets drives the command-line reader. Its input is my own
// shell commands, which are not hostile — but a hook that panics reports
// nothing, and reporting nothing is the whole defect this tool exists for, so
// the invariants asserted here are "it answers, within its bounds, and never
// calls a descriptor or an operator a file".
func FuzzRedirectTargets(f *testing.F) {
	for _, s := range []string{
		"", ">", ">>", "2>&1", "cmd > a.log 2>&1", "cmd >>a", "cmd > 'a b'",
		`cmd > "a b"`, "cmd >| x", "cmd &>> y", "a > b | c > d", "cmd > $X",
		"cmd \\> notaredirect", "cmd 1>&2", "> > >", "cmd >>>> x", "''", `"`,
		"cmd > a.log; cmd2 >> a.log && cmd3 2> b.log",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, command string) {
		got := redirectTargets(command)
		if len(got) > maxTargets {
			t.Fatalf("%q: %d targets, past the bound %d", command, len(got), maxTargets)
		}
		seen := map[string]bool{}
		for _, g := range got {
			if g == "" {
				t.Fatalf("%q: an empty target", command)
			}
			if seen[g] {
				t.Fatalf("%q: %q reported twice", command, g)
			}
			seen[g] = true
			if strings.HasPrefix(g, "&") {
				t.Fatalf("%q: %q is a descriptor, not a file", command, g)
			}
			// ⛔ A bare operator must never be read as a file name: that was a
			// real defect (`cmd >> a.log` answered ">"), so it is an
			// invariant. It holds only for an UNQUOTED command: `cmd > "2>"`
			// really does name a file called `2>`, and the fuzzer found that
			// distinction before this comment existed.
			if !strings.ContainsAny(command, `'"\`) {
				for _, op := range redirOps {
					if g == op {
						t.Fatalf("%q: the operator %q was read as a file name", command, g)
					}
				}
			}
		}
	})
}

// FuzzScan drives the line reader over arbitrary bytes: a log file is written
// by other programs, and one of them will one day write invalid UTF-8, a NUL
// or a 200 MB single line into it.
func FuzzScan(f *testing.F) {
	f.Add("")
	f.Add("\n\n\n")
	f.Add("no record\n")
	f.Add("NO RECORD\r\n")
	f.Add("\x00skipped\x00\n")
	f.Add("\xff\xfe not counted\n")
	f.Add(strings.Repeat("a", 4096))
	f.Fuzz(func(t *testing.T, body string) {
		hits, n, err := scan("f", strings.NewReader(body), declineWords)
		if err != nil {
			return // a refusal is an answer; it just must not be a crash
		}
		if n < len(hits) {
			t.Fatalf("%d declining lines out of %d lines read", len(hits), n)
		}
		for _, h := range hits {
			if h.Line < 1 || h.Line > n {
				t.Fatalf("hit on line %d of %d", h.Line, n)
			}
			if h.File != "f" {
				t.Fatalf("hit names file %q", h.File)
			}
			if matches(h.Text, declineWords) == "" {
				t.Fatalf("reported a line no decline word matches: %q", h.Text)
			}
			if strings.ContainsAny(h.Text, "\n") {
				t.Fatalf("a reported line holds a newline: %q", h.Text)
			}
		}
	})
}

// FuzzHookPayload drives the envelope reader. The payload comes from the
// harness, so it is trusted — but a hook must still answer, and must never
// print anything but one JSON object.
func FuzzHookPayload(f *testing.F) {
	for _, s := range []string{
		"", "{}", "null", "[]", `{"tool_name":"Bash"}`,
		`{"tool_name":"Bash","tool_input":{"command":"x > y"}}`,
		`{"tool_name":"Bash","cwd":"/nope","tool_input":{"command":"x > y"},"tool_response":{"is_error":true}}`,
		`{"tool_response":"a string"}`,
		`{"tool_input":{"command":"x > /dev/null"}}`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body string) {
		old := stdin
		stdin = strings.NewReader(body)
		defer func() { stdin = old }()
		var out, errOut strings.Builder
		// ⛔ The status is an invariant of its own: a PostToolUse hook that
		// exits non-zero on a payload it dislikes breaks every Bash call.
		if code := cli.Drive(RootCmd(), []string{"--hook"}, &out, &errOut); code != 0 {
			t.Fatalf("--hook exited %d on %q", code, body)
		}
		s := out.String()
		if s == "" {
			return
		}
		var r hookResult
		if err := json.Unmarshal([]byte(s), &r); err != nil {
			t.Fatalf("stdout is not one JSON object: %v\n%s", err, s)
		}
		if r.HookSpecificOutput.HookEventName != "PostToolUse" {
			t.Fatalf("hookEventName = %q", r.HookSpecificOutput.HookEventName)
		}
		if r.HookSpecificOutput.AdditionalContext == "" {
			t.Fatal("an envelope was printed with nothing in it")
		}
	})
}
