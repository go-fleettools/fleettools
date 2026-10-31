// Command buildscan answers two questions a green CI cannot: which build
// configurations does a repository's CI actually compile, and is there source
// in the tree that none of them reaches.
//
//	buildscan                  # the whole fleet, under the GitHub root
//	buildscan -root DIR        # somewhere else
//	buildscan -repo o/r        # one repository, verbosely
//	buildscan -vet             # also COMPILE: go vet under each configuration
//	buildscan -all             # include the repositories that are fine
//	buildscan -fail            # exit 1 if anything is found
//
// # Why
//
// A file behind a build constraint is not compiled by a `go test` that does
// not select it, so every statement a native run makes about it is a statement
// about a configuration the file is not in. Two failures here came from that,
// days apart and in opposite directions:
//
//   - go-crdt/collab, 2026-10-11. A new test file in the root package had no
//     constraint at all, and the helpers it calls live in a file that is
//     `!js`. `go test ./...` passed on this machine and the js/wasm lane
//     failed to compile -- `undefined: serve`. The lane was the only witness,
//     and it answers in eight minutes rather than in one second.
//   - go-compressions/blake3, 2026-10-07. `blake3_simd_arm64.go` is behind
//     `goexperiment.simd && arm64`: the job that sets the experiment is amd64,
//     and the only arm64 workflow says in its own header "no GOEXPERIMENT".
//     Each half compiled by some lane, the conjunction by none. The file does
//     not build, and the CI had been green since the day it was written,
//     because no configuration that could fail existed. It was found by
//     running `GOEXPERIMENT=simd go test ./...` by hand on an arm64 machine.
//
// Mutation sweeps make the second kind visible from the other side: a refusal
// in a file no configuration compiles can only ever be reported SURVIVED,
// since the mutant is never built. That is how the browser half of go-crdt's
// sweep came to be swept at all.
//
// This is the single-configuration blind spot that the product-line literature
// names: Kästner et al.'s variability-aware analysis type-checks ALL
// configurations of Busybox's 811 options without the brute-force product, and
// its motivation is exactly that per-configuration tools answer about one
// variant and are read as answering about the program. Go's own tools have the
// same gap -- golang.org/x/tools/cmd/toolstash/buildall builds std for every
// GOOS/GOARCH, and nothing does it for a repository's declared set.
//
// # What it reads, and what it cannot
//
// It reads the working tree: every .go file's constraints (through
// go/build, so a `_windows.go` name counts as much as a `//go:build` line),
// and every `go test|build|vet|install|run` COMMAND in .github/workflows,
// with the strategy matrix expanded.
//
// Per command rather than per step, because a step holding two of them
// declares two configurations -- which this tool got wrong at first, and
// which using it is what found: a lane written as one block with
// `GOOS=darwin GOARCH=arm64 go vet ./...` and the amd64 line under it was
// reported as one configuration, and the workflow was rewritten as two steps
// to work around a limitation its reader should never have had to carry.
//
// Four things it cannot resolve, each reported rather than guessed:
//
//   - a GOOS or GOARCH that comes from somewhere outside the file -- a shell
//     variable, an expression, a value computed in an earlier step;
//   - a self-hosted runs-on label, which says nothing about its platform:
//     this fleet has arm64 self-hosted runners whose labels do not say so;
//   - a workflow that hands the work to a script or a Taskfile, whose
//     contents are not in the workflow;
//   - a workflow the YAML parser refuses, which is reported as unreadable
//     rather than silently contributing nothing.
//
// Two deliberate biases, both towards saying LESS:
//
//   - `exclude:` in a matrix is not applied, and cgo is assumed enabled. Both
//     make the CI look like it covers more. A file wrongly named as compiled
//     by nobody sends somebody to read a file that is fine; a file wrongly
//     left out of that list is the silence this tool exists to break, so the
//     error is taken in the direction that cannot invent work.
//   - `_test.go` files count. A test nothing compiles is the collab failure
//     above, and it is the common case: test files are where new constraints
//     are forgotten.
//
// # -vet
//
// `-vet` stops asking and compiles: for each configuration, `go vet ./...` in
// the module that configuration's step runs in, with GOOS, GOARCH and -tags
// set to it. That is the check that would have caught the collab failure in
// the second it takes, rather than in the eight minutes a lane takes. It is
// off by default because a fleet-wide run of it is hundreds of
// cross-compilations.
//
// ⛔ It COMPILES the tree, which means it builds the dependencies that tree
// names and may fetch them. The scan without it reads files and makes no API
// call at all -- one local `git` for each checkout that has no workflows, to
// tell a repository missing a lane from a tree with no origin, and nothing
// else; `-vet` does not have that property, and pointing it at a
// checkout somebody else controls is the same decision as building that
// checkout. GOOS, GOARCH and the tags reach `go` as arguments and environment
// values, never through a shell, and a `working-directory` that leaves the
// checkout -- absolute, or climbing out with `..` -- is refused rather than
// followed.
package main

import (
	"fmt"
	"go/build"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/checkout"
	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

func main() {
	fleet.WarnIfStale(os.Stderr)
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// finding is one repository's verdict.
type finding struct {
	repo    string
	configs []config
	// orphans are files no configuration compiles, grouped by the constraint
	// they carry: twenty files behind one `//go:build integration` are one
	// thing to decide about, not twenty.
	orphans map[string][]string
	// unbuilt counts the Go files of each module no configuration compiles at
	// all. A whole module nobody builds is one decision, not one per file.
	unbuilt map[string]int
	open    []unresolved
	vetFail []vetFailure
	noCI    bool
	// noRemote: no origin at all, so this tree exists only on this disk.
	//
	// ⛔ It is the difference between a repository with no CI and a directory
	// that could not have any. Ten checkouts came back "no workflows at all"
	// on 2026-10-18, and reading them one by one: five were forks or upstream
	// (hashicorp's hcl, usbarmory's tamago…), two had no remote, one held no
	// Go file at all and two were two- and three-file stubs. Not one was a
	// repository of ours missing a lane — so the ten was a number with no
	// decision behind it, which is the kind this tool exists to avoid.
	noRemote bool
	noGo     bool
	// branch is what the working tree is on, "" when detached, and dirty says
	// whether it has uncommitted changes. Printed for every repository that is
	// NOT on its default branch, because that is when a finding may be about
	// somebody else's work in progress rather than about the repository.
	branch string
	dirty  bool
	// dir is where it was read, kept so the report can ask git what the
	// default branch is without scanning again.
	dir string
}

// ignoreTag is the constraint that means "never compile this".
const ignoreTag = "ignore"

type vetFailure struct {
	cfg    config
	module string
	err    string
}

// interesting decides whether to PRINT the repository at all. Something this
// tool could not resolve is worth seeing, since it bounds what the rest of the
// report means.
func (f finding) interesting() bool {
	// ⛔ `f.noCI` is here as well as in actionable(), and dropping it made
	// three checkouts VANISH rather than be printed-and-not-counted: with no
	// origin they are not actionable, and with no unresolved note they were
	// not interesting either, so the branch that counts them never ran and the
	// summary line never appeared. The fleet run said "7 with no workflows"
	// where the previous one said 10 and accounted for the other three
	// nowhere.
	//
	// The test for it could not see that: it used -dir, which prints every
	// repository whatever this says.
	return f.actionable() || len(f.open) > 0 || f.noCI
}

// actionable decides whether it COUNTS towards "something to decide about".
//
// ⛔ Not the same question, and conflating them moved the fleet total from 91
// to 147 in one edit: a repository whose only entry is "this tool could not
// read that step" is a limit of the instrument, and putting it in the same
// number as a file nobody compiles makes the number mean neither.
func (f finding) actionable() bool {
	// ⛔ A tree with no REMOTE is not a repository missing a lane: nothing can
	// run CI for something nobody can fetch. Still printed, because "it exists
	// only on this disk" is worth seeing -- just not counted as a decision.
	return len(f.orphans) > 0 || len(f.unbuilt) > 0 || len(f.vetFail) > 0 || (f.noCI && !f.noRemote)
}

const longHelp = `Reports the build configurations a repository's CI compiles, and the source
that none of them reaches.

--dir names one directory, whatever it is called, for a worktree made for a
measurement that is not under owner/name; --root and --repo name a place in the
fleet's layout, so they cannot be combined with it.

--vet also runs go vet under each configuration, which COMPILES the tree.

⛔ A tree with no REMOTE is not a repository missing a lane: nothing can run CI
for something nobody can fetch. It is still printed, because "it exists only on
this disk" is worth seeing — just not counted as a decision.

Exit status: 0, or 1 with --fail when anything is found.`

// RootCmd builds the command. Tests drive it through run, below.
func RootCmd() *cobra.Command {
	var (
		root, only, one string
		vet, all, fail  bool
	)
	c := cli.New("buildscan [flags]",
		"What a repository's CI compiles, and the source none of it reaches", longHelp)
	c.Args = func(_ *cobra.Command, args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("unexpected argument %q; directories come from --root", args[0])
		}
		return nil
	}
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		return cli.FromStatus(scanAll(cli.Out(cmd), cli.Err(cmd), root, only, one, vet, all, fail))
	}
	f := c.Flags()
	f.StringVar(&root, "root", "", "directory holding the checkouts (default: the fleet's GitHub root)")
	f.StringVar(&only, "repo", "", "scan this owner/name only, and say more about it")
	f.StringVar(&one, "dir", "", "scan this directory, whatever it is called:\n"+
		"a worktree made for a measurement is not under owner/name")
	f.BoolVar(&vet, "vet", false, "also run go vet under each configuration, which COMPILES the tree")
	f.BoolVar(&all, "all", false, "list the repositories with nothing to report too")
	f.BoolVar(&fail, "fail", false, "exit 1 if anything is found")
	return c
}

// run keeps the signature this command's tests were written against.
func run(argv []string, stdout, stderr io.Writer) int {
	return cli.Drive(RootCmd(), argv, stdout, stderr)
}

// scanAll is the command's work. The name avoids `scan`, which this package
// already uses for one repository.
func scanAll(stdout, stderr io.Writer, root, only, one string, vet, all, fail bool) int {
	if one != "" && (only != "" || root != "") {
		fmt.Fprintln(stderr, "buildscan: --dir names one directory; --root and --repo name a place in the fleet's layout")
		return 2
	}

	dir := root
	if dir == "" {
		dir = checkout.DefaultRoot()
	}
	var repos []string
	if one != "" {
		dir, repos = filepath.Dir(one), []string{filepath.Base(one)}
	} else {
		var err error
		repos, err = checkout.Repos(dir, only)
		if err != nil {
			fmt.Fprintf(stderr, "buildscan: %v\n", err)
			return 2
		}
	}
	if len(repos) == 0 {
		fmt.Fprintf(stderr, "buildscan: no checkouts under %s\n", dir)
		return 2
	}

	var found []finding
	for _, r := range repos {
		// checkout.Repos answers in paths RELATIVE to the root, which is what
		// the report wants to print; the scan needs the directory.
		path := filepath.Join(dir, r)
		// A directory reached through a symlink walks as a single FILE rather
		// than as a tree: WalkDir lstats what it is given and does not follow
		// it. That read as "no Go files here" the first time this tool was
		// pointed at a worktree through one -- a silence, not an answer.
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
		f, err := scan(path, r)
		if err != nil {
			fmt.Fprintf(stderr, "buildscan: %s: %v\n", r, err)
			continue
		}
		if f.noGo {
			continue
		}
		if vet {
			f.vetFail = vetAll(f, path)
		}
		found = append(found, f)
	}

	return report(found, only != "" || one != "", all, fail, vet, stdout)
}

// scan reads one checkout. name is how the report calls it, which is not
// always what the directory is called: a worktree made for a measurement sits
// anywhere, and `-repo owner/name` is the name that means something.
func scan(dir, name string) (finding, error) {
	if name == "" {
		name = repoName(dir)
	}
	f := finding{repo: name, orphans: map[string][]string{}, unbuilt: map[string]int{}}

	cfgs, open, wfFiles, err := configsOf(dir)
	if err != nil {
		return f, err
	}
	f.configs, f.open = cfgs, open
	// ⛔ WHICH BRANCH was read. This tool reads the working tree, and on a
	// machine with twenty sessions on it that tree is usually on somebody's
	// branch: of seventeen checkouts read on 2026-10-18, six were, and three
	// findings were wrong because of it. A report that does not say what it
	// read is a report about as many different things as it has rows.
	f.dir = dir
	f.branch, f.dirty = checkout.Branch(dir)
	f.noCI = wfFiles == 0
	if f.noCI {
		// Asked only when it changes the answer: one `git` per checkout with
		// no workflows, not per checkout.
		if _, ok := checkout.DefaultRef(dir); !ok {
			f.noRemote = true
		}
	}

	files, err := goFiles(dir)
	if err != nil {
		return f, err
	}
	if len(files) == 0 {
		f.noGo = true
		return f, nil
	}
	if len(cfgs) == 0 {
		// Nothing to match against. Saying every file is an orphan would be
		// true and useless: the finding is that there is no configuration.
		return f, nil
	}

	mods, err := modules(dir)
	if err != nil {
		return f, err
	}
	// Each configuration belongs to the module its step runs in. The contexts
	// are built once per module, not once per file.
	byModule := map[string][]build.Context{}
	for _, c := range cfgs {
		m := moduleOf(c.dir, mods)
		byModule[m] = append(byModule[m], contextsOf(c)...)
	}
	for _, rel := range files {
		m := moduleOf(filepath.Dir(rel), mods)
		ctxs := byModule[m]
		if len(ctxs) == 0 {
			// Not one lane compiles this module. Saying so once is the
			// finding; naming each of its files would say it forty times.
			f.unbuilt[m]++
			continue
		}
		matched, err := matchedByAny(dir, rel, ctxs)
		if err != nil {
			f.open = append(f.open, unresolved{where: rel, what: "could not be read: " + err.Error()})
			continue
		}
		if matched {
			continue
		}
		c, err := constraintOf(filepath.Join(dir, rel))
		if err != nil || c == "" {
			c = "(from the file name)"
		}
		if c == ignoreTag {
			// ⛔ `//go:build ignore` MEANS never compiled. It is Go's own
			// idiom for a file the toolchain must not see -- a generator run
			// with `go run gen.go`, a kept example -- so reporting it as
			// compiled by nobody is reporting the author's decision back to
			// them. Nineteen of the 76 file findings on this fleet's first
			// scan were this, which is a quarter of the report saying
			// nothing.
			continue
		}
		f.orphans[c] = append(f.orphans[c], rel)
	}
	return f, nil
}

// moduleOf is the module a path belongs to: the DEEPEST module directory it is
// inside. `go test ./...` in the root module does not descend into a nested
// go.mod, so a file under gitstore/ is compiled by gitstore's lanes and by
// nobody else's.
func moduleOf(rel string, mods []string) string {
	rel = filepath.Clean(rel)
	best := "."
	for _, m := range mods {
		if m == "." {
			continue
		}
		if rel == m || strings.HasPrefix(rel, m+string(filepath.Separator)) {
			if len(m) > len(best) || best == "." {
				best = m
			}
		}
	}
	return best
}

// contextsOf builds the go/build contexts of one configuration: one, or TWO
// when the workflow does not say whether cgo is enabled.
//
// Each starts from build.Default so that ReleaseTags and ToolTags are the
// toolchain's: a context built from zero has no release tags at all, and every
// `//go:build go1.21` file would read as compiled by nobody.
//
// ⛔ The pair is not thoroughness, it is the only cautious answer. Fixing cgo
// ON makes `//go:build cgo` files read as compiled and `!cgo` files read as
// compiled by nobody; fixing it OFF does the reverse. The polarity flips with
// the file, so there is no forgiving single assumption -- and the forgiving
// one was chosen in the comment and the inventing one in the code, which cost
// 42 false findings on tannevaled/purego alone.
func contextsOf(c config) []build.Context {
	base := build.Default
	base.GOOS, base.GOARCH = c.goos, c.goarch
	base.BuildTags = c.tags
	switch c.cgo {
	case cgoOn:
		base.CgoEnabled = true
		return []build.Context{base}
	case cgoOff:
		base.CgoEnabled = false
		return []build.Context{base}
	}
	on, off := base, base
	on.CgoEnabled, off.CgoEnabled = true, false
	return []build.Context{on, off}
}

func matchedByAny(dir, rel string, ctxs []build.Context) (bool, error) {
	fileDir := filepath.Join(dir, filepath.Dir(rel))
	name := filepath.Base(rel)
	var firstErr error
	for i := range ctxs {
		ok, err := ctxs[i].MatchFile(fileDir, name)
		if err != nil {
			// A constraint this configuration cannot evaluate is not a match,
			// but a file nothing can read is a different finding.
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if ok {
			return true, nil
		}
	}
	if firstErr != nil {
		return false, firstErr
	}
	return false, nil
}

// constraintOf is the //go:build line of a file, for the report. The name
// carries the constraint when the line does not.
func constraintOf(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "//go:build ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "//go:build")), nil
		}
		if line == "package" || strings.HasPrefix(line, "package ") {
			break
		}
	}
	return "", nil
}

// goFiles are the .go files of a checkout, relative to it.
//
// vendor and testdata are left out: neither is this repository's source, and
// testdata routinely holds files that are deliberately not compiled.
func goFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable directory is not a finding about builds
		}
		name := d.Name()
		if d.IsDir() {
			if p != dir && (name == "vendor" || name == "testdata" || name == ".git" ||
				strings.HasPrefix(name, "_") || (strings.HasPrefix(name, ".") && name != ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	sort.Strings(out)
	return out, err
}

// vetAll compiles each configuration's OWN module, and only that one.
//
// ⛔ Not the whole tree. A lane that cross-compiles the root module for
// js/wasm has not asked a submodule to build there, and vetting it anyway
// reported go-crdt/collab's gitstore and migrate as broken under js/wasm --
// two findings out of three, both invented, on this tool's first real run.
func vetAll(f finding, dir string) []vetFailure {
	mods, err := modules(dir)
	if err != nil || len(mods) == 0 {
		return nil
	}
	var out []vetFailure
	done := map[string]bool{}
	for _, c := range f.configs {
		m := moduleOf(c.dir, mods)
		if done[c.key()] {
			continue
		}
		done[c.key()] = true
		args := []string{"vet"}
		if len(c.tags) > 0 {
			args = append(args, "-tags="+strings.Join(c.tags, ","))
		}
		args = append(args, "./...")
		cmd := exec.Command("go", args...)
		cmd.Dir = filepath.Join(dir, m)
		cmd.Env = append(os.Environ(),
			"GOOS="+c.goos, "GOARCH="+c.goarch,
			// GOWORK=off so a workspace on this machine cannot change which
			// version of a dependency is read, which is not what the lane
			// will do.
			"GOWORK=off")
		b, err := cmd.CombinedOutput()
		if err == nil {
			continue
		}
		out = append(out, vetFailure{cfg: c, module: m, err: firstError(string(b), err)})
	}
	return out
}

// modules are the directories of the checkout holding a go.mod, relative to
// it, with "." for the root module.
func modules(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if p != dir && (name == "vendor" || name == "testdata" || name == ".git" ||
				(strings.HasPrefix(name, ".") && name != ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "go.mod" {
			rel, err := filepath.Rel(dir, filepath.Dir(p))
			if err != nil {
				return nil
			}
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// firstError is the line of vet's output worth printing: its first real
// complaint, not the "# package" header above it.
func firstError(out string, err error) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(line) > 160 {
			line = line[:157] + "…"
		}
		return line
	}
	return err.Error()
}

func repoName(dir string) string {
	owner := filepath.Base(filepath.Dir(dir))
	return owner + "/" + filepath.Base(dir)
}

// totals is what the last line says. A report whose reader has to add up
// seventy sections to learn how much there is cannot be compared with the next
// run -- and this tool's own first fleet reading was summed by hand, in a
// shell, twice, before the numbers were trusted.
type totals struct {
	repos, hits      int
	files, modules   int
	noCI, unreadable int
	noRemote         int
	vetFails         int
	offDefault       int
}

// readFrom says what was read when that is not the repository's default
// branch, and "" when it is.
//
// ⛔ The whole point of the sentence is that a finding may be about another
// session's work in progress. Of seventeen checkouts read on 2026-10-18, six
// were on somebody's branch and three findings were wrong because of it -- two
// nested modules looked fine because the branch they sat on had already been
// fixed. The fleet report said nothing, so nothing warned anybody.
func readFrom(f finding) string {
	def, ok := checkout.DefaultRef(f.dir)
	if f.branch == "" {
		s := "read from a DETACHED HEAD, not a branch"
		if f.dirty {
			s += ", with uncommitted changes"
		}
		return s
	}
	// defaultRemoteRef answers like "origin/main"; compare the last element.
	if ok {
		if i := strings.LastIndexByte(def, '/'); i >= 0 {
			def = def[i+1:]
		}
		if f.branch == def && !f.dirty {
			return ""
		}
	} else if !f.dirty {
		// No remote to compare against: nothing to warn about beyond what the
		// no-origin line already says.
		return ""
	}
	s := "read from branch " + f.branch
	if ok && f.branch != def {
		s += ", not " + def + " — a finding here may be somebody's work in progress"
	}
	if f.dirty {
		s += "; the tree has uncommitted changes"
	}
	return s
}

func report(found []finding, verbose, all, fail, vetted bool, w io.Writer) int {
	sort.Slice(found, func(i, j int) bool { return found[i].repo < found[j].repo })
	var t totals
	t.repos = len(found)
	for _, f := range found {
		if !f.interesting() && !all && !verbose {
			continue
		}
		// ⛔ One repository is ONE thing to decide about, however many kinds
		// of finding it has. Counting each section instead -- three `hits++`
		// in the three branches below -- made this tool's own first fleet
		// reading say "91 with something to decide about" over 56 + 35
		// sections in fewer repositories than that. An instrument that cannot
		// count its own output cannot be compared with its next run.
		if f.actionable() {
			t.hits++
		}
		fmt.Fprintf(w, "\n%s\n", f.repo)
		// Said only when it is not the default branch, so the common case
		// stays quiet and the exception is impossible to miss. A detached HEAD
		// prints as such: a worktree made for a measurement has no branch.
		if note := readFrom(f); note != "" {
			fmt.Fprintf(w, "  %s\n", note)
			t.offDefault++
		}
		switch {
		case f.noCI && f.noRemote:
			t.noRemote++
			fmt.Fprintln(w, "  no workflows, and no origin either: this exists only on this disk, "+
				"so there is nowhere for CI to run")
		case f.noCI:
			t.noCI++
			fmt.Fprintln(w, "  no workflows at all: every configuration below is compiled by nobody")
		case len(f.configs) == 0:
			t.unreadable++
			fmt.Fprintln(w, "  workflows, but no `go test|build|vet|install|run` step this tool can read")
		default:
			fmt.Fprintf(w, "  %d configuration(s) in CI:\n", len(f.configs))
			for _, c := range f.configs {
				fmt.Fprintf(w, "    %-34s %s\n", c.String(), c.where)
			}
		}
		if n := countFiles(f.orphans); n > 0 {
			t.files += n
			fmt.Fprintf(w, "  ⛔ %d file(s) no configuration compiles:\n", n)
			for _, c := range sortedKeys(f.orphans) {
				files := f.orphans[c]
				sort.Strings(files)
				fmt.Fprintf(w, "    %s\n", c)
				show := files
				if !verbose && len(show) > 4 {
					show = show[:4]
				}
				for _, p := range show {
					fmt.Fprintf(w, "      %s\n", p)
				}
				if len(show) < len(files) {
					fmt.Fprintf(w, "      … and %d more\n", len(files)-len(show))
				}
			}
		}
		if len(f.unbuilt) > 0 {
			t.modules += len(f.unbuilt)
			mods := make([]string, 0, len(f.unbuilt))
			for m := range f.unbuilt {
				mods = append(mods, m)
			}
			sort.Strings(mods)
			fmt.Fprintf(w, "  ⛔ %d module(s) no configuration compiles at all:\n", len(mods))
			for _, m := range mods {
				fmt.Fprintf(w, "    %s (%d .go file(s))\n", m, f.unbuilt[m])
			}
		}
		if len(f.vetFail) > 0 {
			t.vetFails += len(f.vetFail)
			fmt.Fprintf(w, "  ⛔ %d configuration(s) the tree does not compile under:\n", len(f.vetFail))
			for _, v := range f.vetFail {
				fmt.Fprintf(w, "    %s in %s\n      %s\n", v.cfg.String(), v.module, v.err)
			}
		} else if vetted && len(f.configs) > 0 {
			fmt.Fprintf(w, "  go vet passed under all %d\n", len(f.configs))
		}
		if len(f.open) > 0 {
			fmt.Fprintf(w, "  %d thing(s) this tool could not resolve:\n", len(f.open))
			for _, u := range f.open {
				fmt.Fprintf(w, "    %s: %s\n", u.where, u.what)
			}
		}
	}
	fmt.Fprintf(w, "\n%d repositories, %d with something to decide about\n", t.repos, t.hits)
	fmt.Fprintf(w, "  %d file(s) and %d module(s) no configuration compiles\n", t.files, t.modules)
	if t.vetFails > 0 {
		fmt.Fprintf(w, "  %d configuration(s) the tree does not compile under\n", t.vetFails)
	}
	if t.noCI > 0 || t.unreadable > 0 {
		fmt.Fprintf(w, "  %d with no workflows at all, %d whose steps could not be read\n", t.noCI, t.unreadable)
	}
	if t.noRemote > 0 {
		fmt.Fprintf(w, "  %d with no workflows AND no origin, which are not counted above\n", t.noRemote)
	}
	if t.offDefault > 0 {
		fmt.Fprintf(w, "  ⛔ %d were NOT read from the default branch: see the note under each\n", t.offDefault)
	}
	if t.hits > 0 && fail {
		return 1
	}
	return 0
}

func countFiles(m map[string][]string) int {
	var n int
	for _, v := range m {
		n += len(v)
	}
	return n
}

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
