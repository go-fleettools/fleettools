package main

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Release is one module's latest release: the tag AND the commit it points at.
//
// ⛔ THE COMMIT IS NOT AN EXTRA: IT IS THE HALF OF THE CLAIM THIS COMMAND USED
// TO THROW AWAY. `git ls-remote --tags` answers "<oid>\trefs/tags/<tag>" and
// highest() kept the tag alone, so a page saying "**v0.141.0** (`a57b2c0`)" came
// out of a rewrite saying "**v0.154.0** (`a57b2c0`)" -- a new number vouched for
// by the old commit. A page citing a commit reads as MORE sourced than one
// citing none, which is why that is worse than the staleness it fixed.
type Release struct {
	Tag string
	// Commit is the full object id the tag resolves to, or "" when it could
	// not be read. Empty is never written onto a page: it makes the claim
	// unprovable, which is a refusal (see Unproven).
	Commit string
}

// Cell is one place a page states a module's version.
type Cell struct {
	Line   int    // 1-based
	Module string // the repository name, which is the module's last element
	Tag    string // what the page says
	start  int    // byte offsets of the tag in the file
	end    int

	// hasPar is set when the version is followed by a parenthetical, which is
	// the rest of the same claim:
	//
	//	Latest tag: **v0.4.0** (`git ls-remote --tags`, 2026-11-15)
	//	Latest tag: **v0.154.0** (`6abde5b`, read on 2026-11-22 with
	//	`git ls-remote --tags … refs/tags/v0.154.0`)
	//
	// Everything between the parentheses dates or sources the version in front
	// of it, so a rewrite that moves the version alone leaves the page
	// asserting the OLD reading of the NEW tag. parStart/parEnd bracket the
	// text inside the parentheses; parClosed is false when they open and never
	// close in the paragraph, which is a claim this command cannot move and
	// must therefore refuse rather than half-write.
	hasPar, parClosed bool
	parStart, parEnd  int
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
//
// ⛔ THE PARENTHETICAL IS NOT IN THIS REGEXP ON PURPOSE. It was, as an optional
// group matching one backticked command and a date, and a group that does not
// match is indistinguishable from an absence: the pages whose parenthetical
// also named a commit, or wrapped over three lines, read as claims with nothing
// after them and were half-rewritten in silence. parenAfter scans for it
// instead and reports the difference between "there is none" and "there is one
// I cannot read".
var proseClaimRE = regexp.MustCompile("Latest tag:[ \\t]*(?:\\r?\\n[ \\t]*)?\\*\\*(v[0-9]+\\.[0-9]+\\.[0-9]+)\\*\\*")

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

// parenAfter reports the parenthetical that follows the version ending at off
// in paragraph p: start and end bracket the text INSIDE the parentheses.
//
// found=false is "this claim has no parenthetical". found=true with
// closed=false is "one opens and never closes before the end of the
// paragraph", which is a different answer and must stay a different answer: a
// paragraph has no blank line in it, so the end of p is the end of the search.
// Nested parentheses are counted, because a parenthetical that explains a
// command often contains one.
func parenAfter(p string, off int) (start, end int, found, closed bool) {
	i := off
	for i < len(p) && (p[i] == ' ' || p[i] == '\t') {
		i++
	}
	if i < len(p) && p[i] == '\r' {
		i++
	}
	if i < len(p) && p[i] == '\n' {
		i++
		for i < len(p) && (p[i] == ' ' || p[i] == '\t') {
			i++
		}
	}
	if i >= len(p) || p[i] != '(' {
		return 0, 0, false, false
	}
	depth := 0
	for j := i; j < len(p); j++ {
		switch p[j] {
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return i + 1, j, true, true
			}
		}
	}
	return i + 1, len(p), true, false
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
// test (go-fleettools/fleettools#391). The pairing, the two-versions trap and
// the parenthetical are therefore held by mutations applied BY HAND and re-run;
// each one is recorded beside the test that catches it in prose_test.go. Change
// the regexp and you must repeat them: nothing automated will stop you.
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
			if s, e, found, closed := parenAfter(p, m[1]); found {
				c.hasPar, c.parClosed = true, closed
				c.parStart, c.parEnd = offs[i]+s, offs[i]+e
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

// Unproven is a claim the rewrite would have made partly true: moving the
// version would have left the parentheses sourcing it with something belonging
// to the release it replaces. Such a cell is left EXACTLY as it was -- stale
// and visibly stale -- and named.
type Unproven struct {
	Cell
	Why string
}

// claimTokenRE is every kind of token a parenthetical can use to source the
// version in front of it: the date it was read, the tag it names (again, often
// inside the command), and the commit the tag resolved to.
//
// The commit must be backticked, which is how these pages write it. An object
// id in bare prose -- "at 6abde5b" -- is neither moved nor refused: a stated
// limit, not an oversight, because the alternative is refusing every lowercase
// seven-letter word that happens to be hex ("defaced" is one) on every page.
var claimTokenRE = regexp.MustCompile("([0-9]{4}-[0-9]{2}-[0-9]{2})|(v[0-9]+\\.[0-9]+\\.[0-9]+)|`([0-9a-f]{7,40})`")

// moveClaim rewrites a parenthetical onto r, or returns the reason it cannot.
//
// ⛔ ONE PASS, ONE REGEXP, AND THE REFUSALS ARE THE CASES THIS PASS CANNOT
// FINISH. It was written the other way first -- substitute, then read the
// result back and refuse anything belonging to the old release -- and two of
// that version's three checks SURVIVED mutation (2026-11-22): a judge holding
// the same regexp as the writer can only ever agree with it. The leftover-tag
// check in particular was unreachable by construction, since every version the
// substitution could see, it had already replaced. A check no input can redden
// is not a safety net; it is a sentence about a safety net.
//
// ⛔ THE COMMIT IS TRUNCATED, NEVER EXTENDED. The page's seven characters
// become the first seven of the NEW commit; an abbreviation is never grown into
// a full object id, because an id invented from a prefix is well formed and
// answers for nothing -- four occurrences in this fleet.
func moveClaim(par string, r Release, date string) (string, string) {
	why := ""
	out := claimTokenRE.ReplaceAllStringFunc(par, func(tok string) string {
		switch {
		case tok[0] == '`':
			oid := tok[1 : len(tok)-1]
			// One message, because the two cases cannot be told apart by
			// anything that matters here and a second branch would be
			// unreachable: `highest` yields a full object id or none, so the
			// width read is 40 or 0 and the page asks for between 7 and 40.
			if len(r.Commit) < len(oid) {
				why = fmt.Sprintf("the parentheses name the commit `%s` and the commit of %s was not read in full (%d of the %d characters the page shows), so there is nothing to put in its place",
					oid, r.Tag, len(r.Commit), len(oid))
				return tok
			}
			return "`" + r.Commit[:len(oid)] + "`"
		case tok[0] == 'v':
			return r.Tag
		default: // the date the claim was read
			if date == "" {
				why = fmt.Sprintf("the parentheses date the reading %s and this run has no date to put in its place", tok)
				return tok
			}
			return date
		}
	})
	return out, why
}

// Rewrite returns src with each cell set to want[cell.Module].Tag, when that is
// known and different, together with every claim it refused to touch.
//
// Offsets are applied from the end so earlier ones stay valid, and a claim's
// parenthetical is rewritten before its version for the same reason: the
// parentheses come after the version in the text.
//
// ⛔ A CLAIM IS MOVED WHOLE OR LEFT ALONE. There is no third outcome, because
// the third outcome is what this function used to do: it replaced the version
// NUMBER and nothing else, so a page that cited the commit and the command it
// had read came out of the rewrite stating a new tag beside the old commit, in
// a sentence asserting the two agree -- and dated today, so the freshest part
// of it vouched for the stale part. A page that cites its source reads as more
// sourced than one that cites none; a wrong citation is therefore worse than
// the staleness it replaced, and the refusal is the only honest outcome.
func Rewrite(src string, cells []Cell, want map[string]Release, date string) (string, []Unproven) {
	out := src
	var refused []Unproven
	for i := len(cells) - 1; i >= 0; i-- {
		c := cells[i]
		r, ok := want[c.Module]
		if !ok || r.Tag == "" || r.Tag == c.Tag {
			continue
		}
		if c.hasPar {
			if !c.parClosed {
				refused = append(refused, Unproven{c, "the parenthesis opened after the version never closes in this paragraph, so the reading it describes cannot be moved with the version"})
				continue
			}
			par, why := moveClaim(src[c.parStart:c.parEnd], r, date)
			if why != "" {
				refused = append(refused, Unproven{c, why})
				continue
			}
			out = out[:c.parStart] + par + out[c.parEnd:]
		}
		out = out[:c.start] + r.Tag + out[c.end:]
	}
	slices.Reverse(refused) // built from the end of the file; reported in page order
	return out, refused
}
