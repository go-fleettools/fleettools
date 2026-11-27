package main

import (
	"encoding/json"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// read is one observation, in a tool result of the turn, of a pull request's
// state: MERGED, OPEN or CLOSED.
type read struct {
	ref   ref
	state string
	via   string
}

// call is a Bash tool call of the turn and what it printed.
type call struct {
	command string
	output  string
}

// readsOf returns what a call observed. ⛔ The COMMAND decides whether the
// output is about a pull request at all (a `gh pr view`, a `gh api …/pulls/N`,
// `ghmerge`, `ghrelease`), and the OUTPUT decides what it said. A
// `"state":"MERGED"` echoed by anything else is not a read, and `prwait` is
// not a read of the merge state however many times it prints "MERGED".
func readsOf(c call) []read {
	f := strings.Fields(c.command)
	var out []read
	for i, w := range f {
		switch path.Base(trimQuotes(w)) {
		case "gh":
			if i+2 < len(f) && f[i+1] == "pr" && (f[i+2] == "view" || f[i+2] == "list") {
				args := segment(f[i+3:])
				if f[i+2] == "view" {
					out = append(out, viewRead(args, c.output)...)
				} else {
					out = append(out, listRead(args, c.output)...)
				}
			} else if i+1 < len(f) && f[i+1] == "api" {
				out = append(out, apiRead(segment(f[i+2:]), c.output)...)
			}
		case "ghmerge", "ghrelease":
			out = append(out, mergeToolRead(path.Base(trimQuotes(w)), segment(f[i+1:]), c.output)...)
		}
	}
	return out
}

// segment cuts the arguments of one command at the first shell control token.
func segment(f []string) []string {
	for i, w := range f {
		switch w {
		case "&&", "||", ";", "|", "&":
			return f[:i]
		}
	}
	return f
}

func trimQuotes(s string) string { return strings.Trim(s, `'"`) }

var (
	repoTokRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9._-]+$`)
	pullURLRe = regexp.MustCompile(`^https?://[^/]+/([^/]+/[^/]+)/pull/(\d+)`)
	// The flags of `gh pr view|list` that take a separate value, so that value
	// is not mistaken for the pull request.
	valueFlag = map[string]bool{"-R": true, "--repo": true, "--json": true, "-q": true, "--jq": true,
		"-t": true, "--template": true, "-B": true, "--base": true, "-H": true, "--head": true,
		"-s": true, "--state": true, "-L": true, "--limit": true, "-S": true, "--search": true,
		"-a": true, "--author": true, "-l": true, "--label": true, "-A": true, "--assignee": true}
)

// repoFlag returns the -R/--repo value of an argument list.
func repoFlag(args []string) string {
	for i, a := range args {
		a = trimQuotes(a)
		switch {
		case (a == "-R" || a == "--repo") && i+1 < len(args):
			return strings.ToLower(trimQuotes(args[i+1]))
		case strings.HasPrefix(a, "--repo="):
			return strings.ToLower(trimQuotes(strings.TrimPrefix(a, "--repo=")))
		}
	}
	return ""
}

// positional returns the arguments that are neither flags nor flag values.
func positional(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := trimQuotes(args[i])
		switch {
		case strings.HasPrefix(a, "-"):
			if valueFlag[a] {
				i++
			}
		case a != "":
			out = append(out, a)
		}
	}
	return out
}

var (
	jsonStateRe  = regexp.MustCompile(`"state"\s*:\s*"([A-Za-z]+)"`)
	jsonNumberRe = regexp.MustCompile(`"number"\s*:\s*(\d+)`)
	textStateRe  = regexp.MustCompile(`(?mi)^state:\s*(merged|open|closed)\s*$`)
	bareStateRe  = regexp.MustCompile(`(?i)^\s*(merged|open|closed|draft)\s*$`)
	tsvRowRe     = regexp.MustCompile(`(?m)^(\d+)\t.*\t(MERGED|OPEN|CLOSED|DRAFT)\t`)
)

func normState(s string) string {
	s = strings.ToUpper(s)
	if s == "DRAFT" {
		return "OPEN"
	}
	return s
}

// viewRead reads `gh pr view [N|URL] [-R repo] …`: the state, bound to the
// number in the command or, failing that, the number the output itself gives.
func viewRead(args []string, out string) []read {
	r := ref{repo: repoFlag(args)}
	for _, a := range positional(args) {
		if m := pullURLRe.FindStringSubmatch(a); m != nil {
			r.repo = strings.ToLower(m[1])
			r.number, _ = strconv.Atoi(m[2])
			break
		}
		if n, err := strconv.Atoi(a); err == nil {
			r.number = n
			break
		}
	}
	var state string
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &obj) == nil {
		var s string
		_ = json.Unmarshal(obj["state"], &s)
		state = s
		if r.number == 0 {
			_ = json.Unmarshal(obj["number"], &r.number)
		}
	} else if m := jsonStateRe.FindStringSubmatch(out); m != nil {
		state = m[1]
		if n := jsonNumberRe.FindStringSubmatch(out); n != nil && r.number == 0 {
			r.number, _ = strconv.Atoi(n[1])
		}
	} else if m := textStateRe.FindStringSubmatch(out); m != nil {
		state = m[1]
	} else if m := bareStateRe.FindStringSubmatch(out); m != nil {
		state = m[1]
	}
	if state == "" {
		return nil
	}
	if r.number < 0 {
		r.number = 0
	}
	return []read{{ref: r, state: normState(state), via: "gh pr view"}}
}

// listRead reads `gh pr list`: a JSON array of objects with number and state,
// or the tab-separated rows.
func listRead(args []string, out string) []read {
	repo := repoFlag(args)
	var rows []struct {
		Number int    `json:"number"`
		State  string `json:"state"`
	}
	var reads []read
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &rows) == nil {
		for _, x := range rows {
			if x.Number > 0 && x.State != "" {
				reads = append(reads, read{ref{repo, x.Number}, normState(x.State), "gh pr list"})
			}
		}
		return reads
	}
	for _, m := range tsvRowRe.FindAllStringSubmatch(out, -1) {
		n, _ := strconv.Atoi(m[1])
		reads = append(reads, read{ref{repo, n}, normState(m[2]), "gh pr list"})
	}
	return reads
}

var pullsPathRe = regexp.MustCompile(`repos/([^/\s]+/[^/\s]+)/pulls/(\d+)(?:["'\s]|$)`)

// apiRead reads `gh api repos/o/r/pulls/N`: merged:true is MERGED, otherwise
// the state field says open or closed.
func apiRead(args []string, out string) []read {
	var r ref
	for _, a := range positional(args) {
		if m := pullsPathRe.FindStringSubmatch(strings.TrimPrefix(a, "/") + " "); m != nil {
			r.repo = strings.ToLower(m[1])
			r.number, _ = strconv.Atoi(m[2])
			break
		}
	}
	if r.number == 0 {
		return nil
	}
	var pr struct {
		Merged *bool  `json:"merged"`
		State  string `json:"state"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &pr) != nil {
		return nil
	}
	switch {
	case pr.Merged != nil && *pr.Merged:
		return []read{{r, "MERGED", "gh api"}}
	case pr.State != "":
		return []read{{r, normState(pr.State), "gh api"}}
	}
	return nil
}

var (
	mergedLineRes = []*regexp.Regexp{
		regexp.MustCompile(`(?m)^(?:ghrelease: )?merged(?:,|\s*$)`),
		regexp.MustCompile(`#\d+ is (?:already )?merged`),
		regexp.MustCompile(`(?:was|IS) merged, but`),
		regexp.MustCompile(`pull request IS merged`),
	}
	hashRe = regexp.MustCompile(`([A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9._-]+)#(\d+) is (?:already )?merged`)
)

// mergeToolRead reads what ghmerge and ghrelease print when they report that
// the pull request is merged: "merged" on a line of its own, "owner/repo#N is
// already merged", "was merged, but …". The pull request is the one in the
// arguments (`ghmerge owner/repo N`) or the one the output names.
func mergeToolRead(tool string, args []string, out string) []read {
	hit := false
	for _, re := range mergedLineRes {
		if re.MatchString(out) {
			hit = true
			break
		}
	}
	if !hit {
		return nil
	}
	var r ref
	pos := positional(args)
	for i, a := range pos {
		if repoTokRe.MatchString(a) {
			r.repo = strings.ToLower(a)
			if i+1 < len(pos) {
				r.number, _ = strconv.Atoi(pos[i+1])
			}
			break
		}
		if x, y, ok := strings.Cut(a, "#"); ok && repoTokRe.MatchString(x) {
			r.repo = strings.ToLower(x)
			r.number, _ = strconv.Atoi(y)
			break
		}
	}
	if m := hashRe.FindStringSubmatch(out); m != nil && r.number == 0 {
		r.repo = strings.ToLower(m[1])
		r.number, _ = strconv.Atoi(m[2])
	}
	return []read{{r, "MERGED", tool}}
}

// match reports whether a read is about the pull request a claim names.
func (r ref) matches(o ref) bool {
	if r.number == 0 || o.number == 0 || r.number != o.number {
		return false
	}
	return r.repo == "" || o.repo == "" || r.repo == o.repo
}
