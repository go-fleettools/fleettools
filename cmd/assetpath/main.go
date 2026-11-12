// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

// Command assetpath resolves a NAMED durable asset — a corpus, a reference set,
// a dataset — to the directory that holds it, so that where it lives is a
// question a command answers rather than something a session remembers.
//
// ⛔ WHY IT EXISTS. On 2026-11-10 a session needed the arXiv corpus the engine's
// fidelity sweeps measure against. It looked for `*corpus*` and `*arxiv*` under
// the clone root, found nothing, and reported to the user that the corpus was
// not on this machine. It was there all along: 174 paper directories, in a
// shared scratch area, under a directory whose name is a three-letter
// abbreviation. Neither word it searched for appears anywhere in that path, so
// the search could not have succeeded WHATEVER the machine held: it measured
// the words it expected, not the thing. Three pieces of work were parked on that false
// absence, and the user had to say "I am sure it is on the machine".
//
// So this never searches by NAME. It searches by SHAPE — what the asset's
// contents look like — because the shape is the thing and the name is a habit.
//
// Exit status, following priorart's:
//
//	0  found; the path alone on stdout, so it can be substituted
//	1  searched every readable root and it is genuinely not here
//	2  REFUSED TO DECIDE: a root could not be read, or the name has no known
//	   shape. This is NOT an absence and must never be reported as one.
//
// The 1-versus-2 split is the whole point. A tool that cannot tell "it is not
// here" from "I could not look" hands back exactly the answer that caused the
// defect above.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

// osExit is replaceable so tests drive the command without ending the process.
var osExit = os.Exit

// shape is what an asset's directory LOOKS like: a pattern its children match,
// and how many must match before a directory is that asset rather than a
// coincidence.
type shape struct {
	Child *regexp.Regexp
	Min   int
	What  string
}

// builtinShapes are the assets this fleet already measures against. A name that
// is neither here nor registered exits 2 — "I do not know what that looks
// like" — and never 1.
//
// ⛔ Min is not decoration. At 1, any directory holding a single paper would
// answer, including a scratch copy somebody left in a temp tree, and the caller
// would measure against it without ever knowing. The number is the difference
// between recognising a corpus and recognising one of its members.
var builtinShapes = map[string]shape{
	"arxiv-corpus": {
		Child: regexp.MustCompile(`^\d{4}\.\d{4,5}$`),
		Min:   20,
		What:  "a directory of arXiv paper sub-directories (gotex-refdiff -corpus)",
	},
}

// searchRoots are the durable places this machine keeps large assets. They are
// SEARCHED, not guessed at: the roots are broad and the shape is narrow, which
// is the opposite of the failure this tool exists for. It is a var so tests can
// point it at a tree of their own.
var searchRoots = func() []string {
	roots := []string{"/Users/Shared"}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots,
			filepath.Join(home, "Documents", "VCS", "GIT", "localhost"),
			home,
		)
	}
	return roots
}

// maxDepth bounds the walk. An asset parked deeper than this is not found, and
// the refusal names the depth: a bound the tool knows it has is honest, a bound
// it hides is the same defect in a new costume.
const maxDepth = 4

func registryPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "fleet", "assets.json"), nil
}

func loadRegistry() (map[string]string, error) {
	p, err := registryPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return m, nil
}

func saveRegistry(m map[string]string) error {
	p, err := registryPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o644)
}

// findings is what a search produced: the matches, and the surfaces it could not
// read. unread is why exit 2 exists.
type findings struct {
	matches []string
	unread  []string
}

// search walks roots to maxDepth and collects directories matching sh. A
// directory it cannot read is RECORDED, never skipped in silence: a walk that
// swallows its own blind spots reports an absence it did not establish.
func search(roots []string, sh shape) findings {
	var f findings
	seen := map[string]bool{}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				f.unread = append(f.unread, fmt.Sprintf("%s: %v", path, err))
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if depthUnder(root, path) > maxDepth {
				return fs.SkipDir
			}
			if seen[path] {
				return fs.SkipDir
			}
			seen[path] = true
			n, readErr := countMatching(path, sh.Child)
			if readErr != nil {
				f.unread = append(f.unread, fmt.Sprintf("%s: %v", path, readErr))
				return nil
			}
			if n >= sh.Min {
				f.matches = append(f.matches, path)
				return fs.SkipDir // its children are members, not candidates
			}
			return nil
		})
	}
	sort.Strings(f.matches)
	return f
}

// depthUnder reports how many path elements separate path from root.
func depthUnder(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return len(strings.Split(filepath.ToSlash(rel), "/"))
}

// countMatching counts the entries of dir whose name matches re.
func countMatching(dir string, re *regexp.Regexp) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if re.MatchString(e.Name()) {
			n++
		}
	}
	return n, nil
}

// RootCmd builds the command, driven by tests through SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	var register string
	var list bool

	cmd := &cobra.Command{
		Use:   "assetpath <name>",
		Short: "Resolve a named durable asset to its directory, by shape and not by name",
		Long: `assetpath answers "where is the X on this machine", so a session does not
answer it from memory.

It searches by SHAPE -- what the asset's contents look like -- never by the
words in its path. A corpus directory called "axc" is found; a search for
"*corpus*" finds nothing and calls it an absence.

Exit: 0 found (the path alone on stdout), 1 genuinely absent, 2 REFUSED to
decide -- a root could not be read, or the name has no known shape. 2 is not an
absence and must not be reported as one.`,
		SilenceUsage: true,
		Args:         cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, args, register, list)
		},
	}
	cmd.Flags().StringVar(&register, "register", "", "record this path as the named asset, instead of searching")
	cmd.Flags().BoolVar(&list, "list", false, "print every registered asset")
	return cmd
}

func run(cmd *cobra.Command, args []string, register string, list bool) error {
	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
	reg, err := loadRegistry()
	if err != nil {
		return exitWith(2, err)
	}

	if list {
		names := make([]string, 0, len(reg))
		for n := range reg {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(out, "%s\t%s\n", n, reg[n])
		}
		return nil
	}

	if len(args) != 1 {
		return exitWith(2, errors.New("one asset name is required"))
	}
	name := args[0]

	if register != "" {
		abs, err := filepath.Abs(register)
		if err != nil {
			return exitWith(2, err)
		}
		if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
			return exitWith(2, fmt.Errorf("%s is not a readable directory", abs))
		}
		reg[name] = abs
		if err := saveRegistry(reg); err != nil {
			return exitWith(2, err)
		}
		fmt.Fprintf(out, "%s\t%s\n", name, abs)
		return nil
	}

	// A registered path is the answer, while it is still there. If it is NOT
	// still there that is not an absence either: the registry is stale, says so,
	// and the search runs anyway.
	if p, ok := reg[name]; ok {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			fmt.Fprintln(out, p)
			return nil
		}
		fmt.Fprintf(errOut, "note: %s was registered at %s, which is gone; searching\n", name, p)
	}

	sh, ok := builtinShapes[name]
	if !ok {
		return exitWith(2, fmt.Errorf(
			"no shape is known for %q, so this cannot tell absent from unlooked-for.\n"+
				"Register it once and every session after this one gets an answer:\n"+
				"    assetpath %s --register <path>", name, name))
	}

	f := search(searchRoots(), sh)
	// ⛔ AMBIGUITY IS NOT AN ANSWER, and this is not hypothetical: the first run
	// of this command on its author's machine returned THREE sibling directories
	// of the right shape where one was expected, and the one it would have
	// picked does not even hold the papers the caller meant. An earlier draft
	// registered matches[0] — the first in
	// alphabetical order — and would have pointed every later session at a
	// corpus nobody chose, silently. Picking for the caller is the defect this
	// command exists to prevent, wearing a different hat.
	if len(f.matches) > 1 {
		for _, m := range f.matches {
			fmt.Fprintln(out, m)
		}
		return exitWith(2, fmt.Errorf(
			"%d directories have the shape of %q; this command will not choose for you.\n"+
				"Name the one you mean, once, and every later session gets it:\n"+
				"    assetpath %s --register <one of the paths above>", len(f.matches), name, name))
	}
	if len(f.matches) == 1 {
		fmt.Fprintln(out, f.matches[0])
		// One match is an answer: record it, so the next session does not walk
		// the disk.
		reg[name] = f.matches[0]
		if err := saveRegistry(reg); err != nil {
			fmt.Fprintf(errOut, "note: could not record it: %v\n", err)
		}
		return nil
	}
	if len(f.unread) > 0 {
		for _, u := range f.unread {
			fmt.Fprintf(errOut, "unread: %s\n", u)
		}
		return exitWith(2, fmt.Errorf(
			"%d surface(s) could not be read, so %q is UNFOUND, not absent (%s)",
			len(f.unread), name, sh.What))
	}
	return exitWith(1, fmt.Errorf("%q (%s) is not under any searched root, to depth %d",
		name, sh.What, maxDepth))
}

// exitErr carries the status a failure must exit with.
type exitErr struct {
	code int
	err  error
}

func (e exitErr) Error() string { return e.err.Error() }

func exitWith(code int, err error) error { return exitErr{code: code, err: err} }

func main() {
	// ⛔ A RESOLVER THAT IS ITSELF OUT OF DATE answers from an older idea of
	// what the shapes are, and its answer looks exactly like a current one.
	// The fleet gate TestEveryCommandAsksWhetherItIsStale requires this of
	// every command, and it caught this one: the tests here were green and the
	// conformance test was not.
	fleet.WarnIfStale(os.Stderr)
	if err := RootCmd().Execute(); err != nil {
		var ee exitErr
		code := 2
		if errors.As(err, &ee) {
			code = ee.code
		}
		fmt.Fprintf(os.Stderr, "assetpath: %v\n", err)
		osExit(code)
	}
}
