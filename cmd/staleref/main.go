// staleref classifies the references a renamed organisation's documentation
// still makes to its old name, and separates them from the ones that are
// citations of somebody else.
//
//	staleref --old go-pkgx --new go-zpkg --upstream pkgxdev DIR...
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

Exit status: 0 nothing stale, 1 at least one STALE or UNSURE reference,
2 the files could not be read.`

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

// classifier holds the patterns, built from --old/--new/--upstream so the
// command is not specific to one rename.
type classifier struct {
	old, new string
	upstream []string

	orgPath  *regexp.Regexp
	upstrRE  *regexp.Regexp
	envVar   *regexp.Regexp
	bareOld  *regexp.Regexp
	codeVars map[string]bool
}

// newClassifier compiles the patterns for one rename.
//
// `old` is the organisation's former name ("go-pkgx"); `stem` is the part of it
// that also names the old tools and variables ("pkgx" out of "go-pkgx"), which
// is derived rather than asked for because the two are the same word in every
// rename this fleet has done.
func newClassifier(old, new string, upstream []string, codeVars map[string]bool) *classifier {
	stem := strings.TrimPrefix(old, "go-")
	c := &classifier{old: old, new: new, upstream: upstream, codeVars: codeVars}
	c.orgPath = regexp.MustCompile(`(?:github\.com/)?` + regexp.QuoteMeta(old) + `/[A-Za-z0-9._-]+`)
	if len(upstream) > 0 {
		alts := make([]string, len(upstream))
		for i, u := range upstream {
			alts[i] = regexp.QuoteMeta(u)
		}
		c.upstrRE = regexp.MustCompile(`(?:` + strings.Join(alts, "|") + `)`)
	}
	c.envVar = regexp.MustCompile(`\$?\b` + strings.ToUpper(stem) + `_[A-Z0-9_]+\b`)
	c.bareOld = regexp.MustCompile(`(?i)` + regexp.QuoteMeta(stem))
	return c
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

	if c.upstrRE != nil {
		for _, m := range c.upstrRE.FindAllStringIndex(line, -1) {
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
	for _, m := range c.envVar.FindAllStringIndex(line, -1) {
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
	for _, m := range c.bareOld.FindAllStringIndex(line, -1) {
		if !free(m[0], m[1]) {
			continue
		}
		cover(m[0], m[1])
		out = append(out, finding{File: file, Line: n, Verdict: unsure,
			Kind: "bare name — ours or theirs is a question about the sentence",
			Text: strings.TrimSpace(trimAround(line, m[0], m[1]))})
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

// scanTree classifies every documentation file under one directory.
func scanTree(root string, c *classifier) ([]finding, int, error) {
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
		for i, line := range strings.Split(string(b), "\n") {
			out = append(out, c.classifyLine(rel, i+1, line)...)
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
	var (
		old, new      string
		upstream      []string
		codeVar       []string
		showCitations bool
		quiet         bool
	)
	c := cli.New("staleref [flags] DIR...",
		"What a renamed organisation's docs still call themselves", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return cli.FromStatus(run(cli.Out(cmd), cli.Err(cmd),
			old, new, upstream, codeVar, showCitations, quiet, args))
	}
	c.Flags().StringVar(&old, "old", "", "the organisation's former name, eg. go-pkgx (required)")
	c.Flags().StringVar(&new, "new", "", "the name it has now, eg. go-zpkg (required)")
	c.Flags().StringArrayVar(&upstream, "upstream", nil,
		"a name that belongs to the project upstream and must NOT be rewritten; repeatable")
	c.Flags().StringArrayVar(&codeVar, "code-var", nil,
		"an environment variable the CODE still reads, so documenting it is correct; repeatable")
	c.Flags().BoolVar(&showCitations, "show-citations", false,
		"list the CITATION findings too, not only their count")
	c.Flags().BoolVarP(&quiet, "quiet", "q", false,
		"the per-tree counts and the totals only, no findings")
	return c
}

func run(stdout, stderr io.Writer, old, new string, upstream, codeVar []string, showCitations, quiet bool, dirs []string) int {
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
	for _, v := range codeVar {
		vars[strings.TrimPrefix(v, "$")] = true
	}
	c := newClassifier(old, new, upstream, vars)

	byRoot := map[string][]finding{}
	files := map[string]int{}
	for _, d := range dirs {
		found, n, err := scanTree(d, c)
		if err != nil {
			fmt.Fprintln(stderr, "staleref:", err)
			return 2
		}
		byRoot[d] = found
		files[d] = n
	}
	if report(stdout, byRoot, files, showCitations, quiet) {
		return 0
	}
	return 1
}
