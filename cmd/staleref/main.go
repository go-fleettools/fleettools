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
// Three verdicts, and the third is the honest one:
//
//	STALE     names our own old identity — an org path, our own old command,
//	          our own old environment variable
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

// docExts are the files documentation lives in. Source files are modrename's
// business and a recipe's own fields are the pantry's.
var docExts = map[string]bool{
	".md": true, ".html": true, ".txt": true, ".rst": true,
	".yml": true, ".yaml": true, ".toml": true, ".json": true,
}

// verdict is what one occurrence is.
type verdict string

const (
	stale    verdict = "STALE"
	citation verdict = "CITATION"
	unsure   verdict = "UNSURE"
)

// finding is one classified occurrence.
type finding struct {
	File    string
	Line    int
	Verdict verdict
	Kind    string
	Text    string
	Suggest string
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
	c.retired = []retired{newRetired(stem, strings.TrimPrefix(new, "go-"))}
	for _, r := range alsoRetired {
		was, now, _ := strings.Cut(r, "=")
		c.retired = append(c.retired, newRetired(was, now))
	}
	return c
}

// newRetired compiles the two patterns one retired spelling needs: its
// environment variables, and the bare word.
func newRetired(was, now string) retired {
	return retired{
		old:    was,
		new:    now,
		envVar: regexp.MustCompile(`\$?\b` + strings.ToUpper(was) + `_[A-Z0-9_]+\b`),
		bare:   regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(was) + `\b`),
	}
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
				// ⛔ THE CODE DECIDES. A variable the code still reads is not drift,
				// whatever its name looks like.
				v, kind = citation, "environment variable the code still reads"
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
	return out
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

// scanTree classifies every documentation file under one directory.
func scanTree(root string, c *classifier, allow *allowList) ([]finding, int, error) {
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
		if !docExts[strings.ToLower(filepath.Ext(p))] {
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
					f.Verdict, f.Suggest = citation, ""
					f.Kind = "the old name is this page's subject (--allow)"
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
	clean := true
	for _, root := range roots {
		counts := map[verdict]int{}
		for _, f := range byRoot[root] {
			counts[f.Verdict]++
			totals[f.Verdict]++
		}
		fmt.Fprintf(w, "%s: %d doc file(s), %d STALE, %d UNSURE, %d CITATION\n",
			root, files[root], counts[stale], counts[unsure], counts[citation])
		for _, f := range byRoot[root] {
			if f.Verdict != citation {
				clean = false
			}
			if quiet || (f.Verdict == citation && !showCitations) {
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
	fmt.Fprintf(w, "\n%d STALE, %d UNSURE, %d CITATION across %d tree(s)\n",
		totals[stale], totals[unsure], totals[citation], len(roots))
	if totals[stale] == 0 && totals[unsure] == 0 {
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

	byRoot := map[string][]finding{}
	files := map[string]int{}
	for _, d := range dirs {
		found, n, err := scanTree(d, c, allow)
		if err != nil {
			fmt.Fprintln(stderr, "staleref:", err)
			return 2
		}
		byRoot[d] = found
		files[d] = n
	}
	clean := report(stdout, byRoot, files, o.showCitations, o.quiet)
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
