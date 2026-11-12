// Command commentrename renames a retired name inside Go COMMENTS, and
// nowhere else.
//
// ⛔⛔ IN A GO FILE THERE IS NO QUOTING SYNTAX, AND TWO TOOLS LEARNED IT THE
// HARD WAY ON THE SAME DAY. A backtick opens a RAW STRING, not a quotation,
// so a line-based rule that treats `name` as "a name being quoted" is reading
// data as prose. Measured 2026-11-12:
//
//   - staleref skipped backticked spans in .go files and went blind inside
//     raw strings — a usage string, a path default and a struct tag, all
//     carrying the damage, reported as 0 findings;
//
//   - a rename script was one command away from rewriting
//
//     for _, retired := range []string{`bk`, `pkgx`, `pkgm`} {
//
//     — the GUARD in go-zpkg/bottle that refuses those three names in a lock
//     header — into `zbk`, `zpkgx`, `zpkgm`: a guard that looks for the
//     CORRECT names, so it fires on good code and never on the retired ones.
//
// Both defects are the same wrong question. "Is this backtick a quotation?"
// cannot be answered by looking at the line. "Is this byte inside a comment?"
// can be answered exactly, by the parser, and that is what this asks.
//
// # What it renames, and what it refuses to
//
//	a comment          renamed   — prose about our own tooling
//	a string literal   LEFT      — a path on disk, a flag value, an error text
//	an identifier      LEFT      — gofmt -r 'old -> new' does those, on the AST
//
// The division is not a preference. A string is a VALUE: renaming "pkgx" in
// filepath.Join(root, "pkgx") makes the code disagree with the filesystem it
// reads, and renaming it in a recorded provenance URL makes the code disagree
// with bottles that exist. An identifier is Go's to rewrite and gofmt already
// does it correctly. What is left — prose — is the half no tool was covering.
//
// # The left boundary, which is the whole of the matching
//
// Every retired spelling in this fleet is a PREFIX of its replacement: bk/zbk,
// pkgx/zpkgx, pkgm/zpkgm. Without a left boundary the rule fires on every
// already-correct mention, and a report whose bulk is the thing it checks FOR
// is one nobody reads. With it, `zbk` is not `bk`.
//
// And because the replacement contains what it replaces, a second pass over
// the first pass's output doubles the prefix — ZpkgxBin became ZzpkgxBin that
// way, and it COMPILED. This makes one pass, emits into a separate buffer, and
// counts the doubled form afterwards: a run that produced one writes nothing.
//
// Exit status: 0 nothing to change, 1 something would change (or did), 2 the
// question could not be put.
package main

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
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

const longHelp = `Renames a retired name inside Go comments, and nowhere else.

  commentrename --retired bk=zbk --retired pkgx=zpkgx DIR...      report
  commentrename --retired bk=zbk --write DIR...                   rewrite

A comment is prose about our own tooling and is renamed. A STRING LITERAL is
a value -- a path on disk, a flag value, a recorded provenance URL -- and is
left alone, because renaming it makes the code disagree with something that
exists. An IDENTIFIER is left to gofmt -r 'old -> new', which rewrites the AST
and cannot touch a string either.

Which byte is inside a comment is answered by go/parser, not by looking at the
line: in Go a backtick opens a raw string, so no line-based rule can tell a
quotation from data.

Matching has a left word boundary, because every retired spelling in this
fleet is a prefix of its replacement and without it every correct mention
reads as drift. After rewriting, the doubled form (zzbk, zzpkgx) is counted:
a pass that produced one wrote nothing.

Exit status: 0 nothing to change, 1 something would change or did, 2 the
question could not be put.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

type pair struct {
	old, now string
	re       *regexp.Regexp
	doubled  string
}

type opts struct {
	retired []string
	write   bool
	exts    []string
}

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	var o opts
	c := cli.New("commentrename --retired was=now [--write] DIR...",
		"rename a retired name in Go comments, and nowhere else", longHelp)
	c.Flags().StringArrayVar(&o.retired, "retired", nil, "a retired spelling and its replacement, was=now (repeatable)")
	c.Flags().BoolVar(&o.write, "write", false, "rewrite the files; without it, report and change nothing")
	c.RunE = func(cmd *cobra.Command, args []string) error { return run(cmd, &o, args) }
	return c
}

func run(cmd *cobra.Command, o *opts, dirs []string) error {
	if cli.HelpRequested(cmd) {
		return nil
	}
	if len(dirs) == 0 {
		return cli.Unanswerable("name at least one directory to read")
	}
	pairs, err := compile(o.retired)
	if err != nil {
		return cli.Unanswerable("%v", err)
	}
	if len(pairs) == 0 {
		return cli.Unanswerable("no --retired was=now given: there is nothing to look for")
	}

	out, errOut := cli.Out(cmd), cli.Err(cmd)
	var files []string
	for _, d := range dirs {
		got, err := goFiles(d)
		if err != nil {
			return cli.Unanswerable("%s: %v", d, err)
		}
		files = append(files, got...)
	}
	sort.Strings(files)

	changed, counts := 0, map[string]int{}
	for _, f := range files {
		n, err := one(out, f, pairs, o.write)
		if err != nil {
			return cli.Unanswerable("%s: %v", f, err)
		}
		if n > 0 {
			changed++
			counts[f] = n
		}
	}

	total := 0
	for _, n := range counts {
		total += n
	}
	fmt.Fprintf(out, "\n%d comment occurrence(s) in %d of %d file(s)\n", total, changed, len(files))
	if total == 0 {
		fmt.Fprintln(out, "commentrename: nothing to change — note that identifiers and string literals are NOT this command's business")
		return nil
	}
	if !o.write {
		fmt.Fprintln(errOut, "commentrename: nothing was written; pass --write")
	}
	return cli.Found("%d comment occurrence(s) carry a retired name", total)
}

func compile(spec []string) ([]pair, error) {
	var ps []pair
	for _, s := range spec {
		was, now, ok := strings.Cut(s, "=")
		if !ok || was == "" || now == "" {
			return nil, fmt.Errorf("--retired %q is not was=now", s)
		}
		// ⛔ THE LEFT BOUNDARY IS THE WHOLE OF IT: `zbk` must not read as `bk`.
		// The right side is deliberately open, because a retired name is often
		// the head of a longer word in prose (pkgx's, bk-shaped) and those are
		// the same mention.
		ps = append(ps, pair{
			old: was, now: now,
			re:      regexp.MustCompile(`\b` + regexp.QuoteMeta(was)),
			doubled: doubledForm(was, now),
		})
	}
	return ps, nil
}

// doubledForm is what `now` looks like when a rename runs over its own output:
// its added prefix in front of it twice. Empty when `now` is not `was` with a
// prefix, since then there is nothing to double.
func doubledForm(was, now string) string {
	if len(now) <= len(was) || !strings.EqualFold(now[len(now)-len(was):], was) {
		return ""
	}
	return now[:len(now)-len(was)] + now
}

// rewriteComment replaces every retired name in one comment, in a SINGLE
// left-to-right pass that never re-reads what it wrote.
//
// ⛔⛔ APPLYING THE PAIRS ONE AFTER ANOTHER IS THE ORDERING TRAP, and the first
// version of this function had it: each pair rewrote the PREVIOUS pair's
// output, so `--retired Pkgx=Zpkgx --retired pkgx=zpkgx` turns `PkgxBin` into
// `ZpkgxBin` and then into `ZzpkgxBin`. That is the defect measured on
// go-zpkg/zbk on 2026-11-11, and it COMPILED — a consistently doubled
// identifier is a valid identifier, so nothing downstream could see it.
//
// One alternation, one pass, each match replaced from the pair that matched
// it. The output buffer is never an input, so no ordering of the pairs can
// produce it. Longer spellings are tried first, because Go's regexp
// alternation is leftmost-FIRST rather than leftmost-longest and `pkg` would
// otherwise shadow `pkgx`.
func rewriteComment(text string, pairs []pair) (string, int) {
	if len(pairs) == 0 {
		return text, 0
	}
	ordered := make([]pair, len(pairs))
	copy(ordered, pairs)
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i].old) > len(ordered[j].old) })

	alts := make([]string, len(ordered))
	for i, p := range ordered {
		alts[i] = regexp.QuoteMeta(p.old)
	}
	all := regexp.MustCompile(`\b(?:` + strings.Join(alts, "|") + `)`)

	n := 0
	out := all.ReplaceAllStringFunc(text, func(m string) string {
		for _, p := range ordered {
			if m == p.old {
				n++
				return p.now
			}
		}
		return m
	})
	return out, n
}

func goFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "testdata" || d.Name() == "vendor" {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(p) == ".go" {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}

// one rewrites the comments of a single file and returns how many occurrences
// it found.
//
// ⛔ IT EDITS BYTES, NOT THE AST. Printing a parsed file reformats it, and a
// reformat buried inside a rename is a diff nobody can review. The parser is
// used only to say WHERE the comments are; the bytes between them are never
// touched.
func one(out io.Writer, path string, pairs []pair, write bool) (int, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return 0, err
	}

	type span struct{ lo, hi int }
	var spans []span
	for _, g := range f.Comments {
		for _, c := range g.List {
			lo := fset.Position(c.Pos()).Offset
			hi := fset.Position(c.End()).Offset
			if lo >= 0 && hi <= len(src) && lo < hi {
				spans = append(spans, span{lo, hi})
			}
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].lo < spans[j].lo })

	var buf bytes.Buffer
	buf.Grow(len(src))
	last, found := 0, 0
	for _, s := range spans {
		buf.Write(src[last:s.lo])
		text, n := rewriteComment(string(src[s.lo:s.hi]), pairs)
		found += n
		buf.WriteString(text)
		last = s.hi
	}
	buf.Write(src[last:])

	if found == 0 {
		return 0, nil
	}
	// ⛔ A PASS THAT DOUBLED A PREFIX WRITES NOTHING. The replacement contains
	// what it replaces, so an occurrence already correct would become ZzpkgxBin
	// — which compiles, which is why nothing downstream would catch it.
	for _, p := range pairs {
		if p.doubled == "" {
			continue
		}
		before := strings.Count(strings.ToLower(string(src)), strings.ToLower(p.doubled))
		after := strings.Count(strings.ToLower(buf.String()), strings.ToLower(p.doubled))
		if after > before {
			return 0, fmt.Errorf("would create %d doubled %q (%d before, %d after): nothing written",
				after-before, p.doubled, before, after)
		}
	}
	fmt.Fprintf(out, "  %-4d %s\n", found, path)
	if write {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			return 0, err
		}
	}
	return found, nil
}
