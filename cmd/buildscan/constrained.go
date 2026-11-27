// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"go/build/constraint"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// noPackagesRE is what `go vet ./...` (exit 1) and `go build ./...` print when the
// pattern matched nothing.
var noPackagesRE = regexp.MustCompile(`matched no packages|no packages to vet`)

// ignoredTags are tags that never name a configuration to compile: `ignore` marks
// a file that is deliberately not built, and the rest are set by the toolchain.
var ignoredTags = map[string]bool{
	"ignore": true, "cgo": true, "gc": true, "gccgo": true, "unix": true,
}

// constrainedOnlyTags returns the build tags that, set together, select the
// .go files of the module in modDir, but ONLY when the module has at least one
// .go file and every one of them carries a //go:build line. It returns nil in
// every other case, including any file it cannot read: a scan that could not read
// must not conclude "all constrained".
//
// ⛔ Born of go-agentsync/go-agentsync.github.io: a Hugo site's kit.go is
// `//go:build tools` so that `go mod tidy` keeps the landingkit requirement, so
// `go vet ./...` finds no package, exits 1, no pass is recorded, and the push gate
// left only BUILDSCAN_SKIP.
func constrainedOnlyTags(modDir string) []string {
	tags := map[string]bool{}
	files := 0
	ok := true
	err := filepath.WalkDir(modDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			ok = false
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p == modDir {
				return nil
			}
			if name == "vendor" || name == "testdata" || strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			if _, e := os.Stat(filepath.Join(p, "go.mod")); e == nil {
				return filepath.SkipDir // a nested module is its own
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
			return nil
		}
		files++
		// An unreadable file reads as empty, has no //go:build line, and so
		// clears ok below: there is no separate branch for it.
		src, _ := os.ReadFile(p)
		expr := buildExpr(string(src))
		if expr == nil {
			ok = false // an unconstrained file: the module is not "all constrained"
			return nil
		}
		collectTags(expr, false, tags)
		return nil
	})
	if err != nil || !ok || files == 0 {
		return nil
	}
	var out []string
	for t := range tags {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// buildExpr is the file's //go:build expression, nil when it has none or it does
// not parse.
func buildExpr(src string) constraint.Expr {
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if constraint.IsGoBuild(line) {
			// Parse returns a nil Expr with its error: a constraint that does
			// not parse is "no expression", which the caller treats as unconstrained.
			e, _ := constraint.Parse(line)
			return e
		}
		if line == "package" || strings.HasPrefix(line, "package ") {
			break
		}
	}
	return nil
}

// collectTags adds the tags the expression needs SET to hold; a negated tag is not
// one.
func collectTags(e constraint.Expr, neg bool, into map[string]bool) {
	switch x := e.(type) {
	case *constraint.TagExpr:
		if !neg && !ignoredTags[x.Tag] {
			into[x.Tag] = true
		}
	case *constraint.NotExpr:
		collectTags(x.X, !neg, into)
	case *constraint.AndExpr:
		collectTags(x.X, neg, into)
		collectTags(x.Y, neg, into)
	case *constraint.OrExpr:
		collectTags(x.X, neg, into)
		collectTags(x.Y, neg, into)
	}
}

// mergeTags is the sorted union.
func mergeTags(a, b []string) []string {
	set := map[string]bool{}
	for _, t := range a {
		set[t] = true
	}
	for _, t := range b {
		set[t] = true
	}
	var out []string
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
