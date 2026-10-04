package main

import (
	"fmt"
	"strconv"
	"strings"
)

// This file exists so that gopin adds no dependency. fleettools has none at
// all — `go.mod` holds the module line and the go directive and nothing else —
// and golang.org/x/mod would have been the first, for two functions over
// three integers. A tool whose whole job is to make a version explicit should
// not quietly make a dependency implicit.

// goVersion is a Go toolchain version: three numbers, patch optional.
type goVersion struct{ major, minor, patch int }

// parseGoVersion accepts 1.27 and 1.27.1 and nothing else. In particular it
// refuses `stable`, `oldstable` and `1.x`, which is the point: those are the
// values this tool exists to replace, and accepting one as a target would let
// it write back what it just read.
func parseGoVersion(s string) (goVersion, error) {
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return goVersion{}, fmt.Errorf("%q is not a Go version like 1.27.1", s)
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || (i > 0 && p != strconv.Itoa(n)) {
			return goVersion{}, fmt.Errorf("%q is not a Go version like 1.27.1", s)
		}
		out[i] = n
	}
	return goVersion{out[0], out[1], out[2]}, nil
}

// olderThan reports whether v precedes w. A missing patch is zero, which is
// what the Go distribution itself means by 1.27 — go1.27 and go1.27.0 are the
// same release.
func (v goVersion) olderThan(w goVersion) bool {
	if v.major != w.major {
		return v.major < w.major
	}
	if v.minor != w.minor {
		return v.minor < w.minor
	}
	return v.patch < w.patch
}

// olderGoMod reports whether a go.mod directive is behind the target. An
// absent or unparsable directive is NOT behind it: there is nothing to raise,
// and guessing would rewrite a line nobody wrote.
func olderGoMod(directive string, target goVersion) bool {
	if directive == "" {
		return false
	}
	cur, err := parseGoVersion(directive)
	if err != nil {
		return false
	}
	return cur.olderThan(target)
}

// mustParse is for a value run has already validated.
func mustParse(s string) goVersion {
	v, _ := parseGoVersion(s)
	return v
}
