package main

import (
	"regexp"
	"sort"
	"strings"
)

// Cell is one place a page states a module's version.
type Cell struct {
	Line   int    // 1-based
	Module string // the repository name, which is the module's last element
	Tag    string // what the page says
	start  int    // byte offsets of the tag in the file
	end    int

	// hasDate is set on a prose claim that dates its reading:
	// (`git ls-remote --tags`, 2026-11-15). The date is part of the claim.
	hasDate            bool
	dateStart, dateEnd int
}

// proseClaimRE is the sentence the go-fleettools repository pages use, which
// may break after "Latest tag:" and after the version:
//
//	[Repository →](https://github.com/go-fleettools/staletools)
//	Latest tag: **v0.4.0** (`git ls-remote --tags`, 2026-11-15); this page is
//
// Only the version FOLLOWING "Latest tag:" is a claim about the latest tag; the
// "written from its README at **v0.3.0**" that comes after it is deliberately
// older and never matches. The gaps are at most one line break each, and the
// regexp is only ever run on one paragraph, so neither can cross a blank line.
var proseClaimRE = regexp.MustCompile("Latest tag:[ \\t]*(?:\\r?\\n[ \\t]*)?\\*\\*(v[0-9]+\\.[0-9]+\\.[0-9]+)\\*\\*" +
	"(?:[ \\t]*(?:\\r?\\n[ \\t]*)?\\(`[^`\\n]*`, ([0-9]{4}-[0-9]{2}-[0-9]{2}))?")

var repoLinkRE = regexp.MustCompile(`https://github\.com/([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)`)

// paragraphs splits src at blank lines, returning each paragraph's byte offset.
func paragraphs(src string) (offs []int, texts []string) {
	start, off, blank := 0, 0, true
	flush := func(end int) {
		if !blank {
			offs, texts = append(offs, start), append(texts, src[start:end])
		}
	}
	for _, line := range strings.SplitAfter(src, "\n") {
		if strings.TrimSpace(line) == "" {
			flush(off)
			blank, start = true, off+len(line)
		} else {
			blank = false
		}
		off += len(line)
	}
	flush(off)
	return offs, texts
}

// findProse reads the claims of one paragraph each. A claim belongs to the
// nearest repository link of org BEFORE it in the same paragraph; a paragraph
// is the unit because a link and its claim may be split across lines, and a
// claim never reaches into another paragraph's link.
//
// ⛔ A GREEN `mutsweep` SAYS NOTHING ABOUT THIS FUNCTION. Measured 2026-11-22:
// the sweep finds exactly ONE mutable condition in this whole file (the `toml`
// branch in Find), because what decides everything here is a regexp, a byte
// offset and a loop bound -- none of which it mutates. "0 survived" over this
// file is a count of what it could reach, not a coverage claim, and the same
// blindness is why a wrong answer from it exempts a line instead of demanding a
// test (go-fleettools/fleettools#391). The pairing and the two-versions trap
// are therefore held by mutations applied BY HAND and re-run; each one is
// recorded beside the test that catches it in prose_test.go. Change the regexp
// and you must repeat them: nothing automated will stop you.
func findProse(src, org string) []Cell {
	var cells []Cell
	offs, texts := paragraphs(src)
	for i, p := range texts {
		links := repoLinkRE.FindAllStringSubmatch(p, -1)
		linkAt := repoLinkRE.FindAllStringIndex(p, -1)
		for _, m := range proseClaimRE.FindAllStringSubmatchIndex(p, -1) {
			k := -1
			for j := range linkAt {
				if linkAt[j][0] < m[0] {
					k = j
				}
			}
			if k < 0 || links[k][1] != org {
				continue
			}
			c := Cell{
				Line:   strings.Count(src[:offs[i]+m[2]], "\n") + 1,
				Module: strings.TrimRight(links[k][2], "."), Tag: p[m[2]:m[3]],
				start: offs[i] + m[2], end: offs[i] + m[3],
			}
			if m[4] >= 0 {
				c.hasDate, c.dateStart, c.dateEnd = true, offs[i]+m[4], offs[i]+m[5]
			}
			cells = append(cells, c)
		}
	}
	return cells
}

// mdRowRE is a Markdown table row that links the module's repository and puts
// its tag in the next cell, as go-widgets/docs' modules page does in every
// language:
//
//	| [`window`](https://github.com/go-widgets/window) | v0.89.0 | ... |
var mdRowRE = regexp.MustCompile("\\| \\[`([A-Za-z0-9._-]+)`\\]\\(https://github\\.com/([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)\\) \\| (v[0-9]+\\.[0-9]+\\.[0-9]+) \\|")

// tomlNameRE and tomlTagRE find a landing page's module block:
//
//	[[params.modules]]
//	  name = "window"
//	  layer = "surface"
//	  tag = "v0.89.0"
//
// ⛔ AND THE SHAPE THE go-* LANDINGS ACTUALLY USE, which this command could not
// read until 2026-11-01:
//
//	[[params.repos]]
//	  name   = "fleettools"
//	  status = "ready"
//	  label  = "v0.65.0"
//
// Run against go-fleettools.github.io/hugo.toml it answered "1 file(s), 0
// cell(s), 0 module(s), 0 stale" and exited 2 — right to refuse to call nothing
// a pass, and the consequence was that thirteen version labels on this fleet's
// own landing, dated in a comment above them, had never once been checked by the
// command that exists for exactly that. So the key is `tag` OR `label`.
//
// A version belongs to the nearest name above it inside the same block. ⛔ The
// block ends at ANY table header and not only at a `[[...]]` one: a name
// followed by `[params.something]` carrying a version would otherwise be
// credited a number from the next table. No page here does that today, which is
// the point — a guard whose first failure is the one that teaches it is a guard
// bought at the price of a wrong label.
var (
	tomlHeaderRE = regexp.MustCompile(`^\s*\[`)
	tomlNameRE   = regexp.MustCompile(`^\s*name\s*=\s*"([A-Za-z0-9._-]+)"`)
	tomlTagRE    = regexp.MustCompile(`^(\s*(?:tag|label)\s*=\s*")(v[0-9]+\.[0-9]+\.[0-9]+)(")`)
)

// Find returns every version cell in a file, for repositories of org.
// A Markdown row naming another organisation's repository is not this
// organisation's to update, and is left alone.
//
// A `label` that is not a version — `"no tag"` on a repository with no release,
// `"on main"` on a docs site — yields no cell, which is correct: there is
// nothing to keep equal to a latest release that does not exist. It also means
// such a repository is invisible here the day it IS tagged.
func Find(src, org string, toml bool) []Cell {
	if toml {
		return findTOML(src)
	}
	var cells []Cell
	for _, m := range mdRowRE.FindAllStringSubmatchIndex(src, -1) {
		name, owner, repo := src[m[2]:m[3]], src[m[4]:m[5]], src[m[6]:m[7]]
		if owner != org || repo != name {
			continue
		}
		cells = append(cells, Cell{
			Line: strings.Count(src[:m[0]], "\n") + 1, Module: name,
			Tag: src[m[8]:m[9]], start: m[8], end: m[9],
		})
	}
	cells = append(cells, findProse(src, org)...)
	sort.Slice(cells, func(i, j int) bool { return cells[i].start < cells[j].start })
	return cells
}

func findTOML(src string) []Cell {
	var cells []Cell
	name, off := "", 0
	for i, line := range strings.SplitAfter(src, "\n") {
		switch {
		case tomlHeaderRE.MatchString(line):
			name = ""
		case tomlNameRE.MatchString(line):
			name = tomlNameRE.FindStringSubmatch(line)[1]
		case name != "" && tomlTagRE.MatchString(line):
			m := tomlTagRE.FindStringSubmatchIndex(line)
			cells = append(cells, Cell{Line: i + 1, Module: name, Tag: line[m[4]:m[5]], start: off + m[4], end: off + m[5]})
			name = ""
		}
		off += len(line)
	}
	return cells
}

// Rewrite returns src with each cell set to want[cell.Module], when that is
// known and different. Offsets are applied from the end so earlier ones stay
// valid. A prose claim's date is rewritten to date with its version (the date
// comes after the version in the text, so it is replaced first).
func Rewrite(src string, cells []Cell, want map[string]string, date string) string {
	out := src
	for i := len(cells) - 1; i >= 0; i-- {
		c := cells[i]
		if t, ok := want[c.Module]; ok && t != "" && t != c.Tag {
			if c.hasDate && date != "" {
				out = out[:c.dateStart] + date + out[c.dateEnd:]
			}
			out = out[:c.start] + t + out[c.end:]
		}
	}
	return out
}
