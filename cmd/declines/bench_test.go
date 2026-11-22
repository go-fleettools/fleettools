// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

// ⛔ This hook runs after EVERY Bash call, in about twenty-five sessions on one
// machine, so its cost is part of the change rather than a detail. What is
// measured is throughput over a log of the shape it will meet — go test output,
// one decline in it — and the per-call cost of a command with no redirect at
// all, which is the common case and must be nearly free.

// logBody returns a synthetic log of about n bytes.
func logBody(n int) string {
	var b strings.Builder
	b.Grow(n + 64)
	i := 0
	for b.Len() < n {
		switch i % 7 {
		case 0:
			fmt.Fprintf(&b, "=== RUN   TestSomething%d\n", i)
		case 3:
			fmt.Fprintf(&b, "    --- PASS: TestSomething%d (0.00s)\n", i)
		case 5:
			fmt.Fprintf(&b, "ok  \tgithub.com/go-fleettools/fleettools/cmd/x\t0.%03ds\n", i%1000)
		default:
			fmt.Fprintf(&b, "some ordinary line of output number %d with a little length to it\n", i)
		}
		i++
	}
	b.WriteString("buildscan: no record: the working tree has uncommitted changes\n")
	return b.String()
}

func BenchmarkScan8MiB(b *testing.B) {
	body := logBody(8 << 20)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for b.Loop() {
		hits, n, err := scan("bench.log", strings.NewReader(body), declineWords)
		if err != nil || len(hits) != 1 || n == 0 {
			b.Fatalf("hits=%d n=%d err=%v", len(hits), n, err)
		}
	}
}

func BenchmarkHookWithNoRedirect(b *testing.B) {
	payload := `{"hook_event_name":"PostToolUse","tool_name":"Bash","cwd":"/tmp","tool_input":{"command":"go test ./... -count=1"}}`
	old := stdin
	defer func() { stdin = old }()
	b.ReportAllocs()
	for b.Loop() {
		stdin = strings.NewReader(payload)
		var out, errOut strings.Builder
		if code := cli.Drive(RootCmd(), []string{"--hook"}, &out, &errOut); code != 0 {
			b.Fatalf("exited %d", code)
		}
		if out.Len() != 0 {
			b.Fatalf("not silent: %s", out.String())
		}
	}
}

func BenchmarkHookOverAOneMiBLog(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "out.txt")
	body := logBody(1 << 20)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		b.Fatal(err)
	}
	payload := fmt.Sprintf(`{"hook_event_name":"PostToolUse","tool_name":"Bash","cwd":%q,"tool_input":{"command":"go test ./... > out.txt 2>&1"}}`, dir)
	old := stdin
	defer func() { stdin = old }()
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for b.Loop() {
		stdin = strings.NewReader(payload)
		var out, errOut strings.Builder
		if code := cli.Drive(RootCmd(), []string{"--hook"}, &out, &errOut); code != 0 {
			b.Fatalf("exited %d", code)
		}
		if !strings.Contains(out.String(), "no record") {
			b.Fatalf("the decline was not reported: %s", out.String())
		}
	}
}

func BenchmarkRedirectTargets(b *testing.B) {
	const command = "buildscan --dir /Users/x/Documents/VCS/GIT/localhost/some-clone --vet > /Users/x/logs/vet.log 2>&1"
	b.ReportAllocs()
	for b.Loop() {
		if got := redirectTargets(command); len(got) != 1 {
			b.Fatalf("got %v", got)
		}
	}
}
