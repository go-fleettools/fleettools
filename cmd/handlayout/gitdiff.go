// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// git runs git in dir and returns stdout. A variable so a test can make one
// subcommand fail, which a real repository will not do on demand.
var git = runGit

// runGit runs git in dir and returns stdout. Stdin is closed.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "core.quotePath=false"}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// span is a run of added lines, both ends included.
type span struct{ from, to int }

var hunkRE = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// parseAdded reads `git diff -U0` output into the added lines of each file.
func parseAdded(diff string) map[string][]span {
	out := map[string][]span{}
	file := ""
	for _, ln := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(ln, "+++ "):
			p := strings.TrimRight(strings.TrimPrefix(ln, "+++ "), "\t")
			if p == "/dev/null" {
				file = ""
			} else {
				file = strings.TrimPrefix(p, "b/")
			}
		case file != "" && strings.HasPrefix(ln, "@@"):
			m := hunkRE.FindStringSubmatch(ln)
			if m == nil {
				continue
			}
			start, _ := strconv.Atoi(m[1])
			n := 1
			if m[2] != "" {
				n, _ = strconv.Atoi(m[2])
			}
			if n > 0 {
				out[file] = append(out[file], span{start, start + n - 1})
			}
		}
	}
	return out
}

// addedLines returns the lines the range base..HEAD adds, per file.
func addedLines(dir, base string) (map[string][]span, error) {
	d, err := git(dir, "diff", "--unified=0", "--no-color", "--no-ext-diff", base, "HEAD", "--", "*.go")
	if err != nil {
		return nil, err
	}
	return parseAdded(d), nil
}

// findingAdded reports whether a finding overlaps a line the range adds. prefix
// is the audited directory's path below the repository top ("" at the top),
// because git names files from the top and a finding names them from the module.
func findingAdded(f Finding, added map[string][]span, prefix string) bool {
	for _, s := range added[prefix+f.File] {
		if f.Line <= s.to && f.EndLine >= s.from {
			return true
		}
	}
	return false
}

var waiverRE = regexp.MustCompile(`(?m)^[ \t]*handlayout-waiver:[ \t]*(\S.*?)[ \t]*$`)

// waivers returns the reasons of every handlayout-waiver line in the commit
// messages of base..HEAD.
func waivers(dir, base string) ([]string, error) {
	msgs, err := git(dir, "log", "--format=%B%x00", base+"..HEAD")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range waiverRE.FindAllStringSubmatch(msgs, -1) {
		out = append(out, m[1])
	}
	return out, nil
}

// pushBase is the commit the pushed range starts from: where HEAD leaves the
// remote's default branch.
func pushBase(dir string) (string, error) {
	for _, ref := range []string{"origin/HEAD", "origin/main", "origin/master"} {
		if _, err := git(dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
			continue
		}
		mb, err := git(dir, "merge-base", "HEAD", ref)
		if err != nil {
			continue
		}
		return strings.TrimSpace(mb), nil
	}
	return "", fmt.Errorf("no origin/HEAD, origin/main or origin/master to measure the pushed range from")
}
