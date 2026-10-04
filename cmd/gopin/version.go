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

// heldBackBy names the oldest literal pin that is OLDER than the target, if
// there is one, so that a repository holding a job back on purpose does not
// get its go directive raised past that job.
//
// Raising it is not a cosmetic mismatch, it breaks the pin. With GOTOOLCHAIN
// unset — the fleet's default, and what every `setup-go` lane here uses —
// `go` satisfies a directive it is too old for by DOWNLOADING a newer
// toolchain. Measured on go1.26.4 against a module asking for 1.27.1:
//
//	GOTOOLCHAIN=go1.26.4+auto  ->  runs go1.27.1
//	GOTOOLCHAIN=go1.26.4       ->  go.mod requires go >= 1.27.1, refused
//	(directive left at 1.26.4) ->  runs go1.26.4
//
// So the lane goes on printing the version it was pinned to in its setup
// step while running the one it was pinned AWAY from, and the reason it was
// pinned comes back invisibly. `go-gfx/gfx` is the live case: its loong64
// lane names go1.26.4 because golang/go#81000 miscompiles that package there
// and golang/go#81147, the 1.27 backport, is still open.
//
// A pin NEWER than the target does not hold anything back, so it does not
// count; neither does an unparsable one, which this cannot reason about.
//
// A pin that names no patch is NOT a hold within its own minor, because
// setup-go resolves it to the newest patch there. Measured on
// cloud-boot/tamago-uefi, whose workflow says 1.27.x:
//
//	Setup go version spec 1.27.x
//	go version go1.27.1 linux/amd64
//
// So against a target of 1.27.1 that job already runs 1.27.1, and holding
// go.mod back for it would withhold a raise nothing objects to. `1.26` does
// hold, because no patch of 1.26 reaches 1.27.1.
func heldBackBy(literals []string, target goVersion) (string, bool) {
	var oldest goVersion
	name, found := "", false
	for _, l := range literals {
		v, err := parseGoVersion(l)
		if err != nil {
			continue
		}
		// reLiteral captures 1.27 out of both `1.27` and `1.27.x`, so a
		// literal with one dot names a minor and no patch.
		behind := v.olderThan(target)
		if strings.Count(l, ".") == 1 {
			behind = v.major < target.major ||
				(v.major == target.major && v.minor < target.minor)
		}
		if !behind {
			continue
		}
		if !found || v.olderThan(oldest) {
			oldest, name, found = v, l, true
		}
	}
	return name, found
}
