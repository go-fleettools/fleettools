package main

import (
	"regexp"
	"strings"
)

// ⛔ A SECOND way a foreign judge never runs, and this one survives installing
// the tool.
//
// go-compressions/compress, 2026-09-27. The arm64 lane installed ncompress,
// `command -v compress` printed /usr/bin/compress, and go test then answered
//
//	ok  github.com/go-compressions/compress  (cached)
//
// in fifty milliseconds, replaying a result recorded on main where compress
// was absent. All nine subtests "skipped" again. The coverage gate replayed
// too: `ok ... (cached) coverage: 100.0%` -- a gate reading a profile measured
// on another machine's software.
//
// Go's test cache covers the sources, the command line, the environment
// variables a test READS and the files it OPENS. exec.LookPath only STATS, so
// a program appearing on the machine is invisible to the cache key. The fix is
// -count=1, and it is needed most in exactly the situation this tool creates:
// a pull request that changes only a workflow, leaving every source file the
// cached entry was keyed on untouched.
//
// This is reported SEPARATELY from a missing tool. The tool is installed here;
// what is wrong is that the run may not be a run.

// goTestRE finds a `go test` invocation in a workflow's shell.
//
// `go test -c` compiles a test binary without running it -- several repositories
// cross-compile that way to check a lane builds -- and a compile cannot replay
// a result. Matching it would put every one of them on this list.
var goTestRE = regexp.MustCompile(`\bgo\s+test\b`)

var compileOnlyRE = regexp.MustCompile(`\bgo\s+test\b[^|;&\n]*\s-c\b`)

// replayable returns the `go test` command lines in a workflow that could
// replay a cached result, in the order they appear.
//
// A line is safe if it passes -count=1, which Go documents as the idiomatic
// way to disable test caching. -count=2 or more also defeats the cache, so the
// match is on the flag rather than on the exact value -- but anything other
// than 1 changes what the run MEANS, so only -count=1 is accepted and a
// different count is reported, deliberately, for a human to look at.
func replayable(yaml string) []string {
	var out []string
	for _, line := range strings.Split(yaml, "\n") {
		t := strings.TrimSpace(line)
		// ⛔ A step's NAME is not a command. The first version of this matched
		// `- name: go test (Linux 386)` eleven times in tannevaled/purego and
		// reported a repository whose every lane it had misread. Drop the
		// label lines, then strip the `run:` key so what is left is shell.
		if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "- name:") || strings.HasPrefix(t, "name:") {
			continue
		}
		t = strings.TrimPrefix(t, "- ")
		t = strings.TrimSpace(strings.TrimPrefix(t, "run:"))
		if t == "" || t == "|" || !goTestRE.MatchString(t) {
			continue
		}
		if compileOnlyRE.MatchString(t) {
			continue
		}
		if strings.Contains(t, "-count=1") || strings.Contains(t, "-count 1") {
			continue
		}
		out = append(out, t)
	}
	return out
}
