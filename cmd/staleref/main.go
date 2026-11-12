// staleref classifies the references a renamed organisation's documentation
// still makes to its old name, and separates them from the ones that are
// citations of somebody else.
//
//	staleref --old go-pkgx --new go-zpkg --upstream pkgxdev \
//	         --retired bk=zbk --retired pkgm=zpkgm \
//	         --allow filiation.md DIR...
//
// ⛔⛔ A PLAIN GREP CANNOT DO THIS, which is the whole reason the command
// exists. `go-zpkg` was `go-pkgx`, and its tools were renamed with it
// (`bk`→`zbk`, `pkgx`→`zpkgx`, `$PKGX_DIR`→`$ZPKG_DIR`) — but the project it
// forked from is still called pkgx, still lives at pkgx.sh, still publishes
// `pkgxdev/pantry`, and the documentation cites it on purpose, including to say
// what it deliberately does NOT copy. Counting every "pkgx" as drift would
// propose deleting the citations; counting none would leave the dead links.
// Measured over the twelve repositories of go-zpkg: 901 occurrences, of which
// 31 name pkgxdev and must stay.
//
// ⛔ AND IT DOES NOT REWRITE. `modrename` already rewrites module paths, where
// the rule is mechanical ("an occurrence directly preceded by '/' is a file
// path"). Prose has no such rule: whether a sentence is about our tool or
// theirs is a question about the sentence. So this reports, with the line, and
// a person decides. What it buys is that nobody has to FIND them.
//
// The verdicts, of which UNSURE is the honest one and DOUBLED the one nothing
// else here can reach:
//
//	STALE     names our own old identity — an org path, our own old command,
//	          our own old environment variable
//	DEBT      the code reads it AND its name is retired: the CODE is the half
//	          to change, and --code-var does not excuse it
//	DOUBLED   the replacement carries its own added prefix twice, because the
//	          rename ran over its own output: `ZpkgxBin` → `ZzpkgxBin`. It
//	          COMPILES, and looking for the RETIRED spelling cannot find it,
//	          because what is there is the CORRECT name with a letter in front
//	CITATION  names upstream — their org, their domain, their tool as theirs
//	UNSURE    says the old token with nothing around it to decide by
//
// ⛔ An environment variable is checked against the CODE, never renamed on
// sight: `BREWKIT_TARGET` is still what zbk reads (`cmd/zbk/main.go`), so a
// document saying `BREWKIT_TARGET` is CORRECT and a document saying `ZBK_TARGET`
// would be the drift. The rename of a variable is a decision in the code, and
// documentation follows it rather than leading it.
package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Finds what a renamed organisation's documentation still calls itself, and
separates it from what it calls somebody else.

A fork keeps citing the project it forked from, so most occurrences of the old
name are not drift. This classifies each one and prints the line; it does not
rewrite, because whether a sentence is about our tool or theirs is a question
about the sentence. Use modrename for module paths, where the rule is
mechanical.

One rename is never one word: --retired names the other spellings it took with
it, which a check built from the organisation's stem alone reports as clean.

--allow names the pages on which the old name is the SUBJECT — a filiation, a
CHANGELOG. Everything outside them is then drift by construction, which is what
lets this gate a CI lane instead of only reporting.

Exit status: 0 nothing stale, 1 at least one STALE or UNSURE reference,
2 the files could not be read, or an --allow pattern matched no file.`

// docExts are the files documentation lives in by default. Source files are
// modrename's business and a recipe's own fields are the pantry's.
//
// ⛔⛔ THIS LIST IS A CLAIM ABOUT WHERE THE ANSWER CAN BE, and on 2026-11-10 it
// was wrong in a way that read as a clean report. The go-zpkg sweep said
// zpkgx was accounted for while `zpkgx --help` printed
// `ZPKG_DIR  bottle store (default: ~/.pkgx)` and
// `internal/app/environments.go:82` READ `$HOME/.pkgx/environments` — live
// code pointing into the retired store, in files no extension here matches.
// The same hole hid `zbk/config/config.go` returning `~/.pkgx` while
// `bottle.Dir()` returned `~/.zpkg`.
//
// User-visible strings live in CODE: help text, usage blocks, warnings, error
// messages, flag names, flag defaults and flag values. A rename sweep that
// cannot open a `.go` file cannot see any of them, and reports their absence
// as cleanliness — `a-scan-that-could-not-read-reported-zero`.
//
// So --ext exists, and the default stays conservative: widening it silently
// would flood the report with a language's own identifiers. The caller says
// which files are in scope, which makes the scope reviewable.
var docExts = map[string]bool{
	".md": true, ".html": true, ".txt": true, ".rst": true,
	".yml": true, ".yaml": true, ".toml": true, ".json": true,
}

// extSet is the file set one run reads: docExts plus whatever --ext added.
// An entry is normalised to a lower-case, leading-dot extension so that
// `--ext go`, `--ext .go` and `--ext .GO` are one thing.
func extSet(extra []string) map[string]bool {
	s := make(map[string]bool, len(docExts)+len(extra))
	for e := range docExts {
		s[e] = true
	}
	for _, e := range extra {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		s[e] = true
	}
	return s
}

// verdict is what one occurrence is.
type verdict string

const (
	stale    verdict = "STALE"
	citation verdict = "CITATION"
	unsure   verdict = "UNSURE"
	debt     verdict = "DEBT"
	doubled  verdict = "DOUBLED"
)

// ⛔⛔ DOUBLED IS THE ONE SHAPE A RETIRED-NAME SWEEP STRUCTURALLY CANNOT SEE.
//
// Boundaries protect the SOURCE from over-matching. Ordering protects the
// RESULT from being matched again, and nothing here was watching it.
//
// Measured 2026-11-11, renaming zbk's 410 occurrences. The capitalised pair ran
// first — `Pkgx`→`Zpkgx` — and the lowercase pair second, `pkgx`→`zpkgx`. The
// new name `Zpkgx` CONTAINS the old one, so the second rule fired inside the
// first rule's output:
//
//	ZpkgxBin  →  ZzpkgxBin
//
// ⛔ AND IT COMPILED. `go build ./...` returned 0, because a consistently
// doubled identifier is a valid identifier. The tests passed. Every sweep in
// this command reported the file clean — correctly, by its own rule: `Zzpkgx`
// contains `zpkgx`, which is the CORRECT name, and the retired name `pkgx` is
// nowhere in it. Looking for what was retired can never find this.
//
// What CAN find it is the pair itself. In every rename this fleet has done the
// replacement is the retired spelling with something in front of it — `z`+`pkgx`,
// `Z`+`BK_` — so the damage a wrong order does has a fixed shape, `prefix + now`,
// computable from `was=now` and from nothing else. That is what this looks for.
//
// ⛔ The match is deliberately UNANCHORED on both sides. The real occurrence was
// `ZzpkgxBin`: a right boundary would have missed it, and a left boundary would
// miss `getZzpkgxBin`. The string it looks for is specific enough that this
// costs nothing — no English word, path or identifier contains `zzpkgx`.
//
// ⛔ A pair whose replacement is NOT its retired spelling with a prefix gets no
// pattern and is skipped, rather than guessed at. `pkgx`→`zpkgx` qualifies;
// a rename to an unrelated word has no doubled form to look for, and inventing
// one would report findings nobody can act on.

// ⛔⛔ DEBT EXISTS BECAUSE --code-var WAS THE INSTRUMENT OF A REPEATED MISTAKE.
//
// `--code-var X` says "the code reads X, so a document naming it is correct",
// and that is true — but it was used all day on 2026-11-10 to excuse the name
// ITSELF. `BK_CC`, `PKGE_SPECS`, `PKGX_DIR`: each was declared, each turned
// from a finding into a silent CITATION, and each time the conclusion drawn
// was "documentation correct, leave it". The user's correction was the whole
// point: *if the code is still on the old naming, the migration was done
// badly*. A prefix that is not ours is the old product's naming however live
// it is.
//
// So a code-var whose NAME carries a retired spelling is not an exemption. It
// is migration debt, it is reported under its own verdict, and it FAILS —
// which is precisely what declaring the variable used to prevent. Checking the
// code tells you which half to fix; it never tells you to stop.
//
// `ZBK_CC` does not match `BK_`, because the prefix test is anchored: the name
// must BEGIN with the retired spelling followed by `_`.

// retiredVarPrefix reports whether an environment variable's name begins with
// a retired spelling, and what that spelling was replaced by.
func (c *classifier) retiredVarPrefix(name string) (was, now string, ok bool) {
	up := strings.ToUpper(name)
	for _, r := range c.retired {
		if r.new == "" {
			continue // no replacement stated: nothing to migrate it to
		}
		if strings.HasPrefix(up, strings.ToUpper(r.old)+"_") {
			return r.old, r.new, true
		}
	}
	return "", "", false
}

// finding is one classified occurrence.
type finding struct {
	File    string
	Line    int
	Verdict verdict
	Kind    string
	Text    string
	Suggest string
	// Was is the verdict this finding would have carried had --allow not
	// exempted its page. An exemption that cannot be counted is one nobody can
	// review.
	Was verdict
}

// retired is one spelling the project no longer uses, with the spelling that
// replaced it. The organisation's own stem is the first of them; --retired adds
// the rest.
//
// ⛔⛔ ONE RENAME IS NEVER ONE WORD. `go-pkgx` became `go-zpkg` and the stem
// `pkgx` became `zpkgx` — but the same decision also retired `bk`, `pkgm`,
// `pkgs` and `pkge`, and a classifier built from the stem alone cannot see any
// of them. Measured on 2026-11-10, after a sweep that checked `pkgx` only and
// was declared finished: `zbk/docs/from-scratch-toolchain.md` still said `bk`
// 72 times, and `zpkgx/README.md` still printed a `pkgx <pkg>` usage block.
// A gate that watches one of five words reports the other four as clean.
type retired struct {
	old, new string
	bare     *regexp.Regexp
	envVar   *regexp.Regexp
	// dbl matches the replacement with its own added prefix in front of it
	// again — the damage a wrong rename ORDER does. nil when the replacement
	// is not the retired spelling plus a prefix.
	dbl     *regexp.Regexp
	dblText string
}

// classifier holds the patterns, built from --old/--new/--upstream/--retired so
// the command is not specific to one rename.
type classifier struct {
	old, new string
	upstream []string
	keep     []string

	orgPath  *regexp.Regexp
	upstrRE  *regexp.Regexp
	keepRE   *regexp.Regexp
	retired  []retired
	codeVars map[string]bool
}

// ⛔⛔ --keep EXISTS BECAUSE THE OBVIOUS SWEEP WOULD HAVE BEEN DESTRUCTIVE.
//
// `go-zpkg` was `go-pkgx`, and every repository followed the transfer — but
// GitHub's container packages are ORG-SCOPED and did not. Every bottle the
// project has ever published still lives at `ghcr.io/go-pkgx/packages`, and
// `go-zpkg/packages/.github/workflows/wasip1.yml:190-204` says so. Renaming
// `bottle.DistBase` on the assumption that packages follow a transfer is
// exactly what shipped an EMPTY registry in v0.64.0 through v0.68.0.
//
// So an address can carry the old name AND be the live one. Nothing about its
// spelling distinguishes it from a dead link — only knowing where the artifacts
// are does — which means it has to be stated, not inferred. A `--keep` match is
// reported as a CITATION with its own kind, so it appears in a `--show-citations`
// listing rather than vanishing: an exemption nobody can see is how the next
// person re-proposes the same rewrite.

// newClassifier compiles the patterns for one rename.
//
// `old` is the organisation's former name ("go-pkgx"); `stem` is the part of it
// that also names the old tools and variables ("pkgx" out of "go-pkgx"), which
// is derived rather than asked for because the two are the same word in every
// rename this fleet has done.
func newClassifier(old, new string, upstream, keep, alsoRetired []string, codeVars map[string]bool) *classifier {
	stem := strings.TrimPrefix(old, "go-")
	c := &classifier{old: old, new: new, upstream: upstream, keep: keep, codeVars: codeVars}
	if len(keep) > 0 {
		alts := make([]string, len(keep))
		for i, k := range keep {
			alts[i] = regexp.QuoteMeta(k)
		}
		c.keepRE = regexp.MustCompile(`(?:` + strings.Join(alts, "|") + `)[A-Za-z0-9._/-]*`)
	}
	// ⛔⛔ AN ADDRESS IS STALE; THE NAME IN PROSE IS A QUESTION. This line was
	// got wrong twice, in opposite directions, and both were measured.
	//
	// It first required `/<repo>`, which left all eight of registry-viewer's
	// findings UNSURE although each was plainly the old org: a link to the org
	// page, the landing host, a page title. So the suffix was made optional —
	// and the next run put TWO findings in zbk's README under STALE, with a
	// suggested replacement, that must NOT be rewritten:
	//
	//	every repository went from `go-pkgx` to `go-zpkg`
	//	Nothing in go-pkgx carries a build identity — it predates them
	//
	// Those sentences are ABOUT the rename. A net gain of 24 decided findings
	// had hidden two confident wrong answers, which is the failure recorded in
	// `a-net-gain-hides-an-example-that-went-red`.
	//
	// ⭐ The distinction that holds is not how much of the name is there, it is
	// whether the occurrence is an ADDRESS. A host, a forge shorthand, a
	// `.github.io` landing host or a repository path must be updated whatever
	// the sentence says — GitHub redirects a transferred repository, so a stale
	// link keeps working and stays invisible, and a retired landing host does
	// not. The bare name in prose is left UNSURE with its sentence, because
	// deciding it needs the sentence read.
	c.orgPath = regexp.MustCompile(
		`(?:(?:https?://)?github\.com/` + regexp.QuoteMeta(old) + `(?:/[A-Za-z0-9._-]+)?` +
			`|github>` + regexp.QuoteMeta(old) + `(?:/[A-Za-z0-9._-]+)?` +
			`|` + regexp.QuoteMeta(old) + `\.github\.io(?:/[A-Za-z0-9._-]+)*` +
			`|` + regexp.QuoteMeta(old) + `/[A-Za-z0-9._-]+)`)
	if len(upstream) > 0 {
		alts := make([]string, len(upstream))
		for i, u := range upstream {
			alts[i] = regexp.QuoteMeta(u)
		}
		c.upstrRE = regexp.MustCompile(`(?:` + strings.Join(alts, "|") + `)`)
	}
	// ⛔⛔ WORD BOUNDARIES, BECAUSE THE NEW NAME CONTAINS THE OLD ONE. `pkgx`
	// became `zpkgx`, so a stem match with no boundary fires on every mention
	// of the CURRENT tool — and that was most of the UNSURE findings in the
	// first sweep: 137 in the zpkgx repository, 124 in zbk's README, 38 in
	// zpkgm, nearly all of them lines that are already correct. A report whose
	// bulk is the thing it is checking FOR is one nobody reads
	// (`a-sweep-that-measured-its-own-regex`).
	//
	// A boundary on both sides is exactly right here: `zpkgx` has a word
	// character before the stem so it does not match, `go-pkgx` has a hyphen so
	// it does, and `pkgxdev` has a word character after it — which upstream
	// covers anyway, but it would be wrong to call it a bare name regardless.
	//
	// The same boundary is what makes --retired safe: `bk` retired in favour of
	// `zbk` must not fire on `zbk` itself, and `\bbk\b` does not, because the
	// `z` is a word character. Every retired spelling in this fleet's renames
	// is a prefix of its replacement, so without the boundary the gate would
	// report every CORRECT line as drift — the failure recorded in
	// `a-sweep-that-measured-its-own-regex`.
	//
	// ⛔ THE STEM'S REPLACEMENT IS NOT DERIVABLE, so it is not guessed. The
	// organisation went `go-pkgx` → `go-zpkg`, but its command went `pkgx` →
	// `zpkgx`: trimming the prefix off the new org name yields `zpkg`, which is
	// nothing. A suggestion is a string somebody pastes into a document, so a
	// wrong one is worse than none. `--retired pkgx=zpkgx` states it, and an
	// explicit entry REPLACES the stem rather than being shadowed by it.
	c.retired = []retired{newRetired(stem, "")}
	for _, r := range alsoRetired {
		was, now, _ := strings.Cut(r, "=")
		if was == stem {
			c.retired[0] = newRetired(was, now)
			continue
		}
		c.retired = append(c.retired, newRetired(was, now))
	}
	return c
}

// doubledArmed reports whether any retired pair has a doubled form to look
// for. Without one, "0 DOUBLED" means the check did not run.
func (c *classifier) doubledArmed() bool {
	for _, r := range c.retired {
		if r.dbl != nil {
			return true
		}
	}
	return false
}

// newRetired compiles the two patterns one retired spelling needs: its
// environment variables, and the bare word.
func newRetired(was, now string) retired {
	r := retired{
		old:    was,
		new:    now,
		envVar: regexp.MustCompile(`\$?\b` + strings.ToUpper(was) + `_[A-Z0-9_]+\b`),
		bare:   regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(was) + `\b`),
	}
	if d, ok := doubledForm(was, now); ok {
		r.dblText = d
		r.dbl = regexp.MustCompile(`(?i)` + regexp.QuoteMeta(d))
	}
	return r
}

// doubledForm returns what `now` looks like when the rename ran in the wrong
// order: its own added prefix in front of it a second time.
//
// It answers only when `now` IS `was` with something in front of it, because
// that is the only case where the prefix can be read off the pair rather than
// guessed. `pkgx`→`zpkgx` gives `zzpkgx`; `bk`→`zbk` gives `zzbk`; a rename to
// an unrelated word gives nothing.
func doubledForm(was, now string) (string, bool) {
	if was == "" || now == "" || len(now) <= len(was) {
		return "", false
	}
	if !strings.EqualFold(now[len(now)-len(was):], was) {
		return "", false
	}
	prefix := now[:len(now)-len(was)]
	if prefix == "" {
		return "", false
	}
	d := prefix + now
	// ⛔⛔ "NO ENGLISH WORD CONTAINS THE DOUBLED FORM" WAS TRUE OF ONE PAIR AND
	// WRITTEN AS A GENERAL LAW — the failure recorded in
	// `one-case-measured-general-law-written`, committed in the commit message
	// that cited it. `--retired` is CALLER-SUPPLIED, and review measured the
	// consequence: `--retired a=ca` reports `The yucca is a plant.` as drift.
	//
	// A one-letter retired spelling makes a doubled form that is a substring,
	// not a name. The check arms only from two characters up, which keeps every
	// pair this fleet has (`bk`→`zbk` gives `zzbk`).
	//
	// ⛔ THIS IS A THRESHOLD, NOT A PROOF, and the class survives it: review
	// measured `--retired ll=all` reporting `for all intents aall the same`.
	// `--allow` is the escape. Saying so is the point — the previous version
	// of this comment asserted that no word contains a doubled form, which was
	// true of one pair and written as a law.
	//
	// ⛔ A second condition here, `len(d) < 4`, was DEAD: d is prefix+prefix+was
	// with a prefix of at least one character, so two characters of `was`
	// already force four. Review found it. A condition that cannot fire reads
	// as a guard and is not one.
	if len(was) < 2 {
		return "", false
	}
	return d, true
}

// classifyLine returns every finding on one line.
//
// ⛔ THE ORDER IS THE RULE. An upstream name is matched FIRST and its span is
// then excluded, because `pkgxdev/pantry` contains the stem: a classifier that
// asked "does this line mention pkgx" would call every citation drift. The
// organisation path is matched next, because `go-pkgx/zbk` also contains the
// stem and is unambiguously ours.
func (c *classifier) classifyLine(file string, n int, line string) []finding {
	var out []finding
	covered := make([]bool, len(line))
	cover := func(lo, hi int) {
		for i := lo; i < hi && i < len(covered); i++ {
			covered[i] = true
		}
	}
	free := func(lo, hi int) bool {
		for i := lo; i < hi && i < len(covered); i++ {
			if covered[i] {
				return false
			}
		}
		return true
	}

	// ⛔ KEEP IS MATCHED FIRST OF ALL, before upstream and before the org path.
	// `ghcr.io/go-pkgx/packages` contains the old org path, so anything that
	// looked at the org path first would report the live registry as a dead
	// link and suggest the rewrite that emptied it.
	if c.keepRE != nil {
		for _, m := range c.keepRE.FindAllStringIndex(line, -1) {
			cover(m[0], m[1])
			out = append(out, finding{File: file, Line: n, Verdict: citation,
				Kind: "live address, deliberately NOT renamed (--keep)",
				Text: line[m[0]:m[1]]})
		}
	}
	if c.upstrRE != nil {
		for _, m := range c.upstrRE.FindAllStringIndex(line, -1) {
			if !free(m[0], m[1]) {
				continue
			}
			cover(m[0], m[1])
			out = append(out, finding{File: file, Line: n, Verdict: citation,
				Kind: "upstream", Text: line[m[0]:m[1]]})
		}
	}
	for _, m := range c.orgPath.FindAllStringIndex(line, -1) {
		if !free(m[0], m[1]) {
			continue
		}
		cover(m[0], m[1])
		was := line[m[0]:m[1]]
		out = append(out, finding{File: file, Line: n, Verdict: stale,
			Kind: "org path", Text: was,
			Suggest: strings.Replace(was, c.old, c.new, 1)})
	}
	// Every retired spelling's environment variables before any bare word, so
	// that `BK_CC` is decided as a variable — against the code — rather than
	// reported as a bare `bk` with the rest of its name cut off.
	for _, r := range c.retired {
		for _, m := range r.envVar.FindAllStringIndex(line, -1) {
			if !free(m[0], m[1]) {
				continue
			}
			cover(m[0], m[1])
			name := strings.TrimPrefix(line[m[0]:m[1]], "$")
			v := stale
			kind := "environment variable the code no longer reads"
			if c.codeVars[name] {
				// ⛔ THE CODE DECIDES WHICH HALF IS WRONG — NOT WHETHER TO STOP.
				// A variable the code still reads is not documentation drift.
				v, kind = citation, "environment variable the code still reads"
				if was, now, ok := c.retiredVarPrefix(name); ok {
					v = debt
					kind = fmt.Sprintf(
						"the code reads it AND its name is retired (%s_ → %s_): the CODE is the half to change",
						strings.ToUpper(was), strings.ToUpper(now))
				}
			}
			out = append(out, finding{File: file, Line: n, Verdict: v, Kind: kind,
				Text: line[m[0]:m[1]]})
		}
	}
	for _, r := range c.retired {
		for _, m := range r.bare.FindAllStringIndex(line, -1) {
			if !free(m[0], m[1]) {
				continue
			}
			cover(m[0], m[1])
			kind := "bare name — ours or theirs is a question about the sentence"
			if r.new != "" {
				kind += " (retired in favour of " + r.new + ")"
			}
			out = append(out, finding{File: file, Line: n, Verdict: unsure, Kind: kind,
				Text: strings.TrimSpace(trimAround(line, m[0], m[1]))})
		}
	}

	// ⛔⛔ THE DOUBLED CHECK IS AN OVERLAY: IT NEITHER CHECKS `free` NOR
	// `cover`s. Review measured both halves of why, 2026-11-12.
	//
	// Running it FIRST and covering its span stole another rule's verdict:
	// `https://github.com/go-pkgx/zzpkgx` reported 1 DOUBLED and ZERO STALE,
	// because the covered span failed orgPath's `free` check — so a stale
	// organisation URL lost both its verdict and its rewrite suggestion. It
	// also made this function's own "KEEP IS MATCHED FIRST OF ALL" comment
	// false, and reported a `--keep` address that was deliberately exempt.
	//
	// Both facts are true of that line at once: the address is stale AND the
	// name in it is doubled. They are different repairs, so they are different
	// findings, and neither may hide the other. Not covering is safe for the
	// bare-name rule by construction: `Zzpkgx` contains `pkgx` only with a word
	// character in front of it, which `\b` already refuses.
	// ⛔⛔ TWO PAIRS CAN MATCH THE SAME SPAN, AND THE FLEET'S REAL CONFIGURATION
	// DOES. Review measured it: `--retired PkgX=ZPkgX --retired pkgx=zpkgx` —
	// the very pair set whose ORDER causes this defect — reported
	// `ZZPkgXBin` twice, suggesting `ZPkgX` and `Zpkgx`, two contradictory
	// repairs for one occurrence; four findings for two names on a line.
	//
	// Removing `cover` is what allowed it (deliberately, so the address around
	// a doubled name keeps its own verdict), so the de-duplication is local:
	// one finding per span, first pair wins.
	reported := map[[2]int]bool{}
	for _, r := range c.retired {
		if r.dbl == nil {
			continue
		}
		spans := codeSpans(file, line)
		// ⛔ --keep STILL WINS, and that had to be restored by hand once this
		// stopped reading `covered`. A kept address is one somebody checked and
		// declared live — `ghcr.io/go-pkgx/packages` is the measured case — so
		// a doubled-looking name INSIDE one is part of a real address, not
		// damage. Reporting it would propose the rewrite that `--keep` exists
		// to prevent, which is how an empty registry got published.
		if c.keepRE != nil {
			spans = append(spans, spansOf(c.keepRE.FindAllStringIndex(line, -1))...)
		}
		for _, m := range r.dbl.FindAllStringIndex(line, -1) {
			// ⛔ A NAME INSIDE BACKTICKS IS BEING QUOTED, NOT USED. Measured:
			// run against its own directory this command reported 15 DOUBLED,
			// every one a comment or fixture explaining the defect, seven of
			// them inside code spans — a report whose bulk is the thing it
			// checks FOR (`a-sweep-that-measured-its-own-regex`).
			if inSpan(spans, m[0]) {
				continue
			}
			key := [2]int{m[0], m[1]}
			if reported[key] {
				continue
			}
			reported[key] = true
			was := line[m[0]:m[1]]
			out = append(out, finding{File: file, Line: n, Verdict: doubled,
				Kind: fmt.Sprintf(
					"%s doubled: the rename ran %s→%s over its own output — it COMPILES, and a search for %q cannot find it",
					r.new, r.old, r.new, r.old),
				Text:    strings.TrimSpace(trimAround(line, m[0], m[1])),
				Suggest: matchCase(was, r.new)})
		}
	}
	return out
}

// markupExts are the files where a backtick delimits a QUOTATION. Nowhere
// else.
//
// ⛔⛔ A BACKTICK IN GO OPENS A RAW STRING LITERAL, AND THAT IS WHERE THIS
// BUG LIVES. Skipping code spans in source blinded the check in precisely the
// files --ext go exists to open — help text, usage blocks, flag defaults,
// struct tags. Review measured it at 260a93f: three real occurrences, a usage
// string, a path default and a json tag, all holding `ZzpkgxBin`, reported
// 0 DOUBLED and exit 0 under the message "nothing stale and nothing unsure".
//
// The fix for a false positive had created a blind spot larger than the false
// positive, in the one file type that matters most. A rule about PROSE must be
// applied only to prose.
var markupExts = map[string]bool{
	".md": true, ".rst": true, ".txt": true, ".html": true,
}

// codeSpans returns the byte ranges of backtick-delimited quotations on a
// line of markup. For any other file it returns nothing, because a backtick
// there is syntax rather than quoting.
//
// ⛔ AN ODD NUMBER OF BACKTICKS PAIRS NOTHING. Review measured the hole: on
//
//	don`t use ZzpkgxBin yet, see `--help`
//
// the first and second backticks paired across the name and hid it. Three
// backticks cannot be paired by position, and guessing which two belong
// together is how a check loses a finding silently, so a line whose backticks
// do not pair evenly is treated as having no quotations at all. That errs
// towards reporting, which is the right direction for a verdict nothing else
// can see.
func codeSpans(file, line string) [][2]int {
	if !markupExts[strings.ToLower(path.Ext(file))] {
		return nil
	}
	if strings.Count(line, "`")%2 != 0 {
		return nil
	}
	var spans [][2]int
	open := -1
	for i := 0; i < len(line); i++ {
		if line[i] != '`' {
			continue
		}
		if open < 0 {
			open = i
			continue
		}
		spans = append(spans, [2]int{open, i})
		open = -1
	}
	return spans
}

// spansOf converts regexp index pairs into spans, widened by one on each side
// so that inSpan's strict comparison includes the match's own first and last
// byte: a code span's delimiters are outside it, a match's are not.
func spansOf(ms [][]int) [][2]int {
	out := make([][2]int, 0, len(ms))
	for _, m := range ms {
		out = append(out, [2]int{m[0] - 1, m[1]})
	}
	return out
}

func inSpan(spans [][2]int, i int) bool {
	for _, s := range spans {
		if i > s[0] && i < s[1] {
			return true
		}
	}
	return false
}

// matchCase returns s in the case SHAPE of src, so that the repair offered for
// `ZzpkgxBin` reads `Zpkgx` and the one for `ZZBK_TARGET` reads `ZBK`.
//
// ⛔ The first letter is not enough, and that was the first version: an
// environment variable is all capitals, so taking only the lead gave `Zbk` for
// `ZZBK` — a suggestion is a string somebody pastes, so a wrongly-cased one
// hands back a second defect in place of the first.
// ⛔⛔ AND IT MUST NOT LOWER-CASE THE REPLACEMENT'S OWN CAPITALS. The version
// above this one did `strings.ToLower(s)` first, so `--retired PkgX=ZPkgX` on
// `ZZPkgXBin` suggested **`Zpkgx`** — committing, one layer down, the exact
// failure the comment above warns about. Measured in review 2026-11-12.
//
// The replacement is the caller's string and is used VERBATIM. Only two things
// are imposed on it: an all-capitals occurrence takes an all-capitals repair
// (an environment variable), and a capitalised occurrence takes a capitalised
// first letter (an identifier). Nothing else is touched.
func matchCase(src, s string) string {
	if src == "" || s == "" {
		return s
	}
	if allUpper(src) {
		return strings.ToUpper(s)
	}
	r := []rune(s)
	if unicode.IsUpper([]rune(src)[0]) {
		r[0] = unicode.ToUpper(r[0])
	}
	return string(r)
}

// allUpper reports whether src has letters and none of them is lower case.
func allUpper(src string) bool {
	letters := false
	for _, r := range src {
		if unicode.IsLower(r) {
			return false
		}
		if unicode.IsUpper(r) {
			letters = true
		}
	}
	return letters
}

// trimAround returns the match with a little of its sentence, which is what
// makes an UNSURE finding cheap to decide.
func trimAround(line string, lo, hi int) string {
	const ctx = 40
	start, end := lo-ctx, hi+ctx
	if start < 0 {
		start = 0
	}
	if end > len(line) {
		end = len(line)
	}
	return line[start:end]
}

// ⛔⛔ --allow IS WHAT MAKES THIS A DOOR RATHER THAN A REPORT.
//
// A fork has to be able to say where it came from, so there is always one page
// — a filiation, a "the break", a CHANGELOG — on which the old name is the
// SUBJECT and every occurrence is correct. Without a way to name those pages,
// every run of this command ends in hundreds of UNSURE findings that a reader
// has to re-decide from scratch, and the decision is never written down: the
// sweep of 2026-11-09 was declared finished and the drift was back the next
// day, because nothing could tell a page about pkgx from a page that had not
// caught up with the rename.
//
// With the filiation pages named, UNSURE outside them is drift by construction
// and the exit status means something.
//
// ⛔ AN ALLOW PATTERN THAT MATCHES NOTHING IS AN ERROR, never a quiet no-op.
// The whole point is that the exemption is narrow; a pattern naming a page that
// has since been renamed or deleted would widen to nothing and leave the gate
// looking green while the exemption it documents no longer exists. That is the
// vacuous guard recorded in `three-ways-a-guard-passes-its-own-mutation` —
// a guard has to fail when the thing it guards moves.

// allowList decides whether a file's findings are exempt, and remembers which
// patterns have fired so that an unused one can be refused.
type allowList struct {
	pats []string
	used []bool
}

func newAllowList(pats []string) *allowList {
	return &allowList{pats: pats, used: make([]bool, len(pats))}
}

// match reports whether rel is exempt. A pattern holding no separator is
// matched against the base name, so `filiation.md` covers it wherever it lives;
// one holding a separator is matched against the whole relative path.
func (a *allowList) match(rel string) bool {
	if a == nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	hit := false
	for i, p := range a.pats {
		p = filepath.ToSlash(p)
		subject := rel
		if !strings.Contains(p, "/") {
			subject = path.Base(rel)
		}
		if ok, err := path.Match(p, subject); err == nil && ok {
			a.used[i] = true
			hit = true
		}
	}
	return hit
}

// unused names the patterns that matched no file in any tree.
func (a *allowList) unused() []string {
	if a == nil {
		return nil
	}
	var out []string
	for i, p := range a.pats {
		if !a.used[i] {
			out = append(out, p)
		}
	}
	return out
}

// scanTree classifies every file under one directory whose extension is in
// exts.
func scanTree(root string, c *classifier, allow *allowList, exts map[string]bool) ([]finding, int, error) {
	var out []finding
	files := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		if d.IsDir() {
			// .git holds every old name forever and is not documentation.
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !exts[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		files++
		rel, _ := filepath.Rel(root, p)
		exempt := allow.match(rel)
		for i, line := range strings.Split(string(b), "\n") {
			for _, f := range c.classifyLine(rel, i+1, line) {
				if exempt && f.Verdict != citation {
					// The old name is this page's SUBJECT. Reported as a
					// citation with its own kind, so that --show-citations
					// still lists it: an exemption nobody can see is how the
					// next person re-proposes the rewrite it exists to stop.
					//
					// ⛔⛔ A DOUBLED FINDING IS EXEMPTED HERE TOO, AND THAT IS
					// WHY ITS COUNT HAD TO BE KEPT. Review measured the hole:
					// `ZzpkgxBin` in an allowed CHANGELOG.md became a citation
					// and the run reported 0 DOUBLED, exit 0 — while the
					// filiation page is exactly the place a doubled name
					// survives, since it is the page that talks about the
					// rename. Exempting it is still right (a page explaining
					// the defect must be able to spell it), but the exemption
					// is now COUNTED and printed. A silent exemption of the one
					// verdict nothing else can see is a gate that turns itself
					// off on the page that needs it most.
					f.Was = f.Verdict
					f.Verdict, f.Suggest = citation, ""
					f.Kind = "the old name is this page's subject (--allow)"
					if f.Was == doubled {
						f.Kind = "a DOUBLED name on a page whose subject is the rename (--allow)"
					}
				}
				out = append(out, f)
			}
		}
		return nil
	})
	return out, files, err
}

// report prints the verdict, grouped so that the work is visible as work.
// ⛔ --quiet EXISTS BECAUSE grep COULD NOT READ THIS COMMAND'S OWN OUTPUT.
// The findings quote the matched lines, and a tree's documentation includes
// files with bytes a grep treats as binary — so `grep -c UNSURE` over the saved
// report printed NOTHING, which reads exactly like "no matches" and is actually
// "I did not look". The command that produced the data has to be able to
// summarise it, rather than relying on a filter that can go silent.
func report(w io.Writer, byRoot map[string][]finding, files map[string]int, showCitations, quiet bool) bool {
	roots := make([]string, 0, len(byRoot))
	for r := range byRoot {
		roots = append(roots, r)
	}
	sort.Strings(roots)

	totals := map[verdict]int{}
	exempted := 0
	clean := true
	for _, root := range roots {
		counts := map[verdict]int{}
		for _, f := range byRoot[root] {
			counts[f.Verdict]++
			totals[f.Verdict]++
			if f.Was == doubled {
				exempted++
			}
		}
		fmt.Fprintf(w, "%s: %d doc file(s), %d DOUBLED, %d STALE, %d DEBT, %d UNSURE, %d CITATION\n",
			root, files[root], counts[doubled], counts[stale], counts[debt], counts[unsure], counts[citation])
		for _, f := range byRoot[root] {
			if f.Verdict != citation {
				clean = false
			}
			// ⛔ A DEBT FINDING IS ALWAYS LISTED, even under --quiet. --quiet
			// trades findings for counts, and the one finding that must not be
			// reducible to a count is the one declaring a flag made disappear.
			//
			// A DOUBLED finding is always listed for the opposite reason: it is
			// the one finding whose COUNT is uninformative. Nobody is looking
			// for it, nothing else can see it, and it names a file that compiles
			// and passes its tests — so it has to arrive as a location.
			if f.Verdict != debt && f.Verdict != doubled &&
				(quiet || (f.Verdict == citation && !showCitations)) {
				continue
			}
			suffix := ""
			if f.Suggest != "" {
				suffix = " → " + f.Suggest
			}
			fmt.Fprintf(w, "  %-8s %s:%d  %s%s\n      %s\n",
				f.Verdict, f.File, f.Line, f.Kind, suffix, f.Text)
		}
	}
	fmt.Fprintf(w, "\n%d DOUBLED, %d STALE, %d DEBT, %d UNSURE, %d CITATION across %d tree(s)\n",
		totals[doubled], totals[stale], totals[debt], totals[unsure], totals[citation], len(roots))
	if totals[doubled] > 0 {
		fmt.Fprintf(w, "staleref: %d name(s) carry their replacement's prefix twice — a rename that ran over its own output. It compiles, the tests pass, and looking for the RETIRED spelling cannot find it; re-run the rename with the lowercase pairs first\n", totals[doubled])
	}
	// ⛔ An exemption of the one verdict nothing else can see is never silent.
	if exempted > 0 {
		fmt.Fprintf(w, "staleref: %d DOUBLED finding(s) were exempted by --allow — a page whose subject is the rename may spell the damage, but check that each one is QUOTED and not a doubled name the page itself still carries\n", exempted)
	}
	if totals[debt] > 0 {
		fmt.Fprintf(w, "staleref: %d name(s) the code reads and the rename retired — declaring them with --code-var does not make them correct, it records that the CODE has not been migrated\n", totals[debt])
	}
	// ⛔ AND IT MUST NOT CALL A TREE CLEAN IN THE LINE UNDER AN EXEMPTION.
	// Review measured the two printed one after the other: "N DOUBLED findings
	// were exempted by --allow" and then "nothing stale and nothing unsure".
	// The second is true of the verdicts and false of the tree, which is the
	// reading a lane takes from it.
	if exempted > 0 {
		fmt.Fprintf(w, "staleref: this run is NOT a clean tree — %d finding(s) were exempted, and the exit status cannot tell a lane about them\n", exempted)
	}
	if exempted == 0 && totals[stale] == 0 && totals[unsure] == 0 && totals[debt] == 0 && totals[doubled] == 0 {
		// ⛔ A tree with no documentation files reports no findings, which is
		// the same output as a clean one. The file count is what tells them
		// apart, so it is always printed.
		fmt.Fprintln(w, "staleref: nothing stale and nothing unsure — check the doc file counts above, because a tree that was not read reports the same")
	}
	return clean
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	var o opts
	c := cli.New("staleref [flags] DIR...",
		"What a renamed organisation's docs still call themselves", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return cli.FromStatus(run(cli.Out(cmd), cli.Err(cmd), o, args))
	}
	c.Flags().StringVar(&o.old, "old", "", "the organisation's former name, eg. go-pkgx (required)")
	c.Flags().StringVar(&o.new, "new", "", "the name it has now, eg. go-zpkg (required)")
	c.Flags().StringArrayVar(&o.upstream, "upstream", nil,
		"a name that belongs to the project upstream and must NOT be rewritten; repeatable")
	c.Flags().StringArrayVar(&o.keep, "keep", nil,
		"an address that carries the old name and is still LIVE, so it must not be rewritten (eg. a container registry that did not follow the transfer); repeatable")
	c.Flags().StringArrayVar(&o.codeVar, "code-var", nil,
		"an environment variable the CODE still reads, so documenting it is correct; repeatable")
	c.Flags().StringArrayVar(&o.alsoRetired, "retired", nil,
		"another spelling the rename retired, as was=now (eg. bk=zbk); one rename is never one word, and a stem-only check reports the rest as clean; repeatable")
	c.Flags().StringArrayVar(&o.exts, "ext", nil,
		"also read files with this extension, eg. --ext go --ext sh. The default set is documentation only, and user-visible strings live in CODE — help text, usage blocks, warnings, flag names and flag DEFAULTS — so a sweep that cannot open a source file reports their absence as cleanliness; repeatable")
	c.Flags().StringArrayVar(&o.allowPats, "allow", nil,
		"a file on which the old name is the SUBJECT and every occurrence is correct (a filiation page, a CHANGELOG); its findings become citations, which is what lets the exit status gate. A pattern matching no file is an error; repeatable")
	c.Flags().BoolVar(&o.showCitations, "show-citations", false,
		"list the CITATION findings too, not only their count")
	c.Flags().BoolVarP(&o.quiet, "quiet", "q", false,
		"the per-tree counts and the totals only, no findings")
	return c
}

// opts is one invocation. A struct rather than a parameter list because five of
// the fields are []string: at a call site they are five indistinguishable nils,
// and swapping --keep for --allow would compile and quietly change the verdict.
type opts struct {
	old, new      string
	upstream      []string
	keep          []string
	codeVar       []string
	alsoRetired   []string
	allowPats     []string
	exts          []string
	showCitations bool
	quiet         bool
}

func run(stdout, stderr io.Writer, o opts, dirs []string) int {
	old, new := o.old, o.new
	var missing []string
	if old == "" {
		missing = append(missing, "--old")
	}
	if new == "" {
		missing = append(missing, "--new")
	}
	if len(dirs) == 0 {
		missing = append(missing, "at least one directory")
	}
	if len(missing) > 0 {
		fmt.Fprintf(stderr, "staleref: %s required\n", strings.Join(missing, " and "))
		return 2
	}

	vars := map[string]bool{}
	for _, v := range o.codeVar {
		vars[strings.TrimPrefix(v, "$")] = true
	}
	c := newClassifier(old, new, o.upstream, o.keep, o.alsoRetired, vars)
	allow := newAllowList(o.allowPats)
	exts := extSet(o.exts)

	byRoot := map[string][]finding{}
	files := map[string]int{}
	for _, d := range dirs {
		found, n, err := scanTree(d, c, allow, exts)
		if err != nil {
			fmt.Fprintln(stderr, "staleref:", err)
			return 2
		}
		byRoot[d] = found
		files[d] = n
	}
	clean := report(stdout, byRoot, files, o.showCitations, o.quiet)
	// ⛔⛔ "0 DOUBLED" FROM A CHECK THAT WAS NEVER ARMED READS EXACTLY LIKE A
	// CLEAN TREE — `a-corpus-row-green-because-the-subject-was-absent`. The
	// doubled form exists only for a `--retired was=now` whose replacement is
	// the retired spelling plus a prefix, and the organisation's own stem
	// carries NO replacement unless one is stated (it is never guessed). So a
	// run with no `--retired` at all scans every tree, prints 0 DOUBLED, and
	// has not looked. Say so, where the count is.
	if !c.doubledArmed() {
		fmt.Fprintln(stderr, "staleref: the doubled check is NOT ARMED — no --retired was=now whose replacement is the retired spelling with a prefix, so 0 DOUBLED here means \"did not look\", not \"nothing found\"")
	}
	// After the report, so that the findings are on screen either way: an
	// exemption that no longer names a file is a broken gate, not a clean run.
	if u := allow.unused(); len(u) > 0 {
		fmt.Fprintf(stderr, "staleref: --allow matched no file in any tree: %s\n", strings.Join(u, " "))
		fmt.Fprintln(stderr, "staleref: an exemption that names nothing exempts nothing — delete it, or correct the path it was written for")
		return 2
	}
	if clean {
		return 0
	}
	return 1
}
