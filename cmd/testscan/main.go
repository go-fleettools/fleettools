// testscan reports every repository whose CI runs `go test` on only part of
// what has tests — or on nothing at all.
//
//	testscan                  # the whole fleet, under the GitHub root
//	testscan -root DIR        # somewhere else
//	testscan -repo o/r        # one repository, verbosely
//	testscan -all             # include the repositories that are fine
//
// It exists because of openweft/weft. Five workflows, `go test` in exactly one
// of them, naming four packages with -tags=integration. 83 packages have
// tests. The other 79 had never run in CI, and nothing said so: every lane was
// green, because every lane was green about the four.
//
// Switching on a plain `go test ./...` there found two tests that could not
// pass on Linux at all — one asserting the host's own hypervisor, one racing
// for a TCP port. Both had been waiting since the packages were written.
//
// # What it looks at, and what it cannot see
//
// It reads the working tree: which directories hold _test.go files, and which
// `go test` invocations appear in .github/workflows. An invocation naming
// `./...` is taken to cover everything; one naming paths covers those paths.
//
// Three things it cannot resolve, each reported rather than guessed:
//
//   - `go test` inside a script the workflow calls (run: bash scripts/ci.sh).
//     The call is visible, the contents are not; such a repository is marked
//     "defers to a script" and left for a human.
//   - Build tags. `go test ./...` does not compile files behind
//     `//go:build integration`, so a repo can cover every package and still
//     not run those tests. Noted in the output when tagged test files exist.
//   - A `-run` filter, which narrows what executes inside a package that IS
//     named. go-widgets/window's lanes each name one test; that turned out to
//     be fine (prefix matching covered all 19), but only counting said so.
//
// It reads the working tree only. The SCAN makes no API calls, so it cannot
// starve Renovate — but each FINDING is confirmed with one, because a checkout
// on disk is not necessarily one of our repositories. Six of nineteen findings
// on 2026-09-27 were forks, a local-only directory, or upstream's own project;
// telling somebody hashicorp/hcl has no CI is worse than saying nothing. That
// is nineteen calls, not nine hundred and twenty-five, and an unanswered
// question keeps the finding.
package main

import (
	"flag"
	"fmt"
	"github.com/go-fleettools/fleettools/internal/checkout"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

// goTestRE finds a `go test` invocation and captures the rest of its line.
//
// The arguments matter more than the call: `go test ./...` and
// `go test ./floatingipnat/` are the difference this tool exists to see.
//
// A trailing backslash continues the command onto the next line, and the
// packages are routinely on that next line:
//
//	go test -mod=mod -timeout 600s -covermode=atomic \
//	  -coverprofile=cover.out ./...
//
// Stopping at the first newline read that as naming no packages at all --
// grpc-transports/webrtc, fully covered, reported as "0 of 1 tested".
var goTestRE = regexp.MustCompile(`go test((?:[^\n|&;]*\\\n)*[^\n|&;]*)`)

// scriptCallRE finds a workflow step that hands the work somewhere else: a
// script, a Makefile, or a Taskfile. What those run is not in the workflow.
//
// `task ci` was the miss that mattered: go-fde/fde and
// go-filesystems/interface both run it, and their Taskfile's ci target does
// `go test -race ./...`. Without this they read as "no go test in CI" —
// pointing at two repositories that are fully covered.
var scriptCallRE = regexp.MustCompile(`(?:bash|sh|\./)\s*\S*scripts?/\S+|make\s+(?:test|check|ci)|task\s+[a-z:-]+`)

// taskfileNames are the files a `task` invocation reads. When one is present
// the delegation can be followed instead of merely reported.
var taskfileNames = []string{"Taskfile.yml", "Taskfile.yaml", "taskfile.yml", "Makefile"}

// dynamicArgRE matches a `go test` whose packages come from a VARIABLE or a
// list built elsewhere:
//
//	go test $PURE_GO_PKGS                    (go-doom/engine)
//	go test -cover "$pkg"                    (go-composites/*, inside a loop)
//	xargs go test < "$RUNNER_TEMP/pkgs.txt"  (openweft/weft, fed by go list)
//
// The set is real but not readable here. Reporting "0 of N tested" for those
// would be a confident falsehood — and the first fleet run did exactly that
// for three dozen repositories, one of which this tool's author had fixed an
// hour earlier. Same rule as judgescan: what cannot be resolved is reported as
// unresolved, never guessed.
var dynamicArgRE = regexp.MustCompile(`\$[A-Za-z_{(]|xargs\s+go test`)

// goListAllRE marks the idiom that builds the full package set at run time.
// Paired with a `go test` fed from it, that IS full coverage.
var goListAllRE = regexp.MustCompile(`go list \./\.\.\.`)

// pkgArgRE picks package arguments out of a `go test` line: ./..., ./pkg/,
// ./pkg/... and bare names. Flags and their values are skipped by the caller.
var pkgArgRE = regexp.MustCompile(`^\.{1,2}(?:/\S*)?$`)

// finding is one repository's verdict.
type finding struct {
	repo       string
	testedPkgs int  // packages a `go test` invocation names, directly or via ./...
	withTests  int  // packages that have at least one _test.go
	catchAll   bool // some invocation used ./...
	noCI       bool // no workflow files at all
	noRemote   bool // no origin remote: this repository exists only on this disk
	noGoTest   bool // workflows exist, none of them runs go test
	viaScript  bool // a workflow defers to a script; contents unknown
	dynamic    bool // a go test whose package set is computed, not written out
	tagged     int  // test files behind a build tag
	workflows  int  // workflow files actually read -- the positive control
	bytesRead  int
	fetchAge   time.Duration // how long since this clone last heard from its remote
	fetchKnown bool
	behind     int      // commits this checkout's HEAD is behind its remote default
	named      []string // the paths invocations named, when not ./...
}

// covered reports whether CI runs the tests of every package that has them.
func (f finding) covered() bool { return f.catchAll || f.testedPkgs >= f.withTests }

func main() {
	fleet.WarnIfStale(os.Stderr)
	root := flag.String("root", defaultRoot(), "directory holding org/repo checkouts")
	doFetch := flag.Bool("fetch", true, "git fetch each repository about to be reported, so its distance is measured and not remembered")
	only := flag.String("repo", "", "scan a single org/repo, verbosely")
	all := flag.Bool("all", false, "also list repositories whose CI covers everything")
	remote := flag.Bool("remote", false, "read origin/HEAD instead of the working tree (fetch first)")
	flag.Parse()

	repos, err := findRepos(*root, *only)
	if err != nil {
		fmt.Fprintln(os.Stderr, "testscan:", err)
		os.Exit(1)
	}

	var found []finding
	scanned, withTests, ok := 0, 0, 0
	for _, r := range repos {
		var f finding
		var has bool
		if *remote {
			f, has = scanRef(*root, r)
		} else {
			f, has = scan(*root, r)
		}
		scanned++
		if !has {
			continue
		}
		withTests++
		if !isFinding(f) {
			ok++
			if !*all {
				continue
			}
		}
		found = append(found, f)
	}
	// ⛔ Confirm each finding belongs to us before printing it. Six of
	// nineteen did not on 2026-09-27: four forks, one checkout GitHub does not
	// have, and upstream's own project in upstream's own account. See ours.go.
	var dropped []string
	if *only == "" {
		found, dropped = sift(found)
	}

	sort.Slice(found, func(i, j int) bool {
		// Worst first: the further from covered, the higher.
		gi := found[i].withTests - found[i].testedPkgs
		gj := found[j].withTests - found[j].testedPkgs
		if gi != gj {
			return gi > gj
		}
		return found[i].repo < found[j].repo
	})

	// ⛔ scanRef already reads origin/main rather than the working tree, and
	// this tool still reported ten repositories with no CI of which EIGHT had
	// one. Its own comment says why: "origin/main in a clone is itself a
	// cached value". Nobody had fetched. The Refresh below did — AFTER the
	// verdict was formed.
	//
	// So the order is now fetch, then derive again, for the findings only.
	// Nine hundred fetches to answer a question about ten is a cost, not a
	// measurement.
	ages := make([]checkout.Age, 0, len(found))
	var survived []finding
	rederived, vanished := 0, 0
	for _, f := range found {
		a := checkout.Age{Behind: f.behind, FetchAge: f.fetchAge, FetchKnown: f.fetchKnown}
		if *doFetch {
			if n, fetched := checkout.Refresh(filepath.Join(*root, f.repo)); fetched {
				a = checkout.Age{Behind: n, FetchAge: 0, FetchKnown: true}
				rederived++
				nf, still := scanRef(*root, f.repo)
				if !still || !isFinding(nf) {
					vanished++
					// ⛔ And move it to the other column. A finding that is
					// dropped without being counted anywhere makes the summary
					// stop adding up -- 1 with tests, 0 conforming, 0 findings
					// -- and a total that does not close is the shape of a
					// scan that lost something.
					ok++
					continue
				}
				nf.behind, nf.fetchAge, nf.fetchKnown = a.Behind, a.FetchAge, a.FetchKnown
				f = nf
			}
		}
		survived = append(survived, f)
		ages = append(ages, a)
	}
	found = survived
	if vanished > 0 {
		fmt.Fprintf(os.Stderr,
			"\n%d finding(s) dropped: fetched and re-derived, and no longer true\n"+
				"  (%d checkout(s) were re-scanned against their remote default).\n", vanished, rederived)
	}

	for _, f := range found {
		fmt.Print(render(f))
		// ⛔ Per FINDING, not as an aggregate at the bottom. The aggregate was
		// there and it said nothing useful: it counted clones that had not
		// FETCHED in a week, and every one of the five wrong findings on
		// 2026-09-27 had fetched minutes earlier and was still up to twenty
		// commits behind. What the reader needs is beside the line they are
		// about to act on.
		if f.behind > 0 {
			// ⛔ Two messages about the same fact must not disagree. This line
			// said "Pull, then RE-RUN this" while the summary underneath said
			// the finding had already been re-derived and still held. A reader
			// who follows the first does work the tool has done.
			if *doFetch {
				fmt.Printf("    this checkout is %d commit(s) behind — re-derived against the remote "+
					"default, and it still holds.\n", f.behind)
			} else {
				fmt.Printf("    ⚠ this checkout is %d commit(s) behind its remote and -fetch=false, so "+
					"the finding above describes code that may no longer exist.\n", f.behind)
			}
		}
		if *only != "" {
			fmt.Printf("    read %d workflow file(s), %d bytes\n", f.workflows, f.bytesRead)
			if f.tagged > 0 {
				fmt.Printf("    NOTE: %d test file(s) behind a build tag; go test ./... does not compile those\n", f.tagged)
			}
			if f.viaScript {
				fmt.Println("    NOTE: a workflow calls a script; what it runs is not visible here")
			}
		}
	}

	reportDropped(dropped)

	fmt.Fprint(os.Stderr, checkout.StalenessWarning(ages, *doFetch))

	// A scan that cannot read reports zero, and zero reads as good news.
	fmt.Fprintf(os.Stderr, "\n%d repositories scanned, %d have tests, %d run them all in CI, %d do not\n",
		scanned, withTests, ok, len(found)-boolCount(*all, ok))
}

func boolCount(listed bool, n int) int {
	if listed {
		return n
	}
	return 0
}

// defaultRoot is the GitHub checkout root on this machine.
func defaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, "Documents", "VCS", "GIT", "github.com")
}

// findRepos lists org/repo directories that are git checkouts.
//
// A worktree's .git is a FILE holding `gitdir: ...`, not a directory. Counting
// it makes one repository look like several.
func findRepos(root, only string) ([]string, error) {
	if only != "" {
		if _, err := os.Stat(filepath.Join(root, only)); err != nil {
			return nil, fmt.Errorf("%s: %w", only, err)
		}
		return []string{only}, nil
	}
	orgs, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, o := range orgs {
		if !o.IsDir() {
			continue
		}
		names, err := os.ReadDir(filepath.Join(root, o.Name()))
		if err != nil {
			continue
		}
		for _, n := range names {
			if !n.IsDir() {
				continue
			}
			rel := filepath.Join(o.Name(), n.Name())
			if st, err := os.Stat(filepath.Join(root, rel, ".git")); err == nil && st.IsDir() {
				out = append(out, rel)
			}
		}
	}
	return out, nil
}

// scan returns one repository's finding, and whether it has any tests at all.
// isFinding is the ONE definition of "this repository is a problem".
//
// It was written inline in the scan loop, so the re-derivation pass had to
// restate it -- and a predicate stated twice is a predicate that drifts. The
// headline count and the re-check now ask the same question.
func isFinding(f finding) bool { return !f.covered() || f.noCI || f.noGoTest }

func scan(root, repo string) (finding, bool) {
	dir := filepath.Join(root, repo)
	pkgs, tagged := testPackages(dir)
	if len(pkgs) == 0 {
		return finding{}, false
	}
	f := finding{repo: repo, withTests: len(pkgs), tagged: tagged}
	f.behind, f.fetchAge, f.fetchKnown = checkout.Staleness(dir)

	yaml, n, err := readWorkflows(dir)
	if err != nil || n == 0 {
		f.noCI = true
		return f, true
	}
	f.workflows, f.bytesRead = n, len(yaml)
	return classify(f, yaml, pkgs, dir), true
}

// classify turns one repository's workflow text into a verdict. Shared by the
// working-tree scan and the remote-ref scan, so the two can never drift into
// answering the same question differently.
func classify(f finding, yaml string, pkgs []string, dir string) finding {
	invocations := goTestRE.FindAllStringSubmatch(yaml, -1)
	if len(invocations) == 0 {
		// The workflow runs no go test of its own. Before calling that a gap,
		// follow the delegation where it can be followed: `task ci` and `make
		// test` live in a file in the repository, and that file is readable.
		// Only when the target cannot be read does this stay unresolved.
		if scriptCallRE.MatchString(yaml) {
			if body, ok := readDelegate(dir); ok && goTestRE.MatchString(body) {
				yaml = body // measure the delegate's invocations instead
				invocations = goTestRE.FindAllStringSubmatch(body, -1)
			} else {
				f.noGoTest = true
				f.viaScript = true
				return f
			}
		} else {
			f.noGoTest = true
			return f
		}
	}

	if goListAllRE.MatchString(yaml) && dynamicArgRE.MatchString(yaml) {
		// `go list ./...` feeding a `go test`: the set is everything, minus
		// whatever the pipeline filters out. Treated as covered; a deliberate
		// exclusion is visible in the workflow and is a decision, not a gap.
		f.catchAll = true
		f.testedPkgs = f.withTests
		return f
	}
	named := map[string]bool{}
	for _, m := range invocations {
		if dynamicArgRE.MatchString(m[0]) {
			f.dynamic = true
			continue
		}
		for _, arg := range packageArgs(m[1]) {
			if strings.HasSuffix(arg, "...") && (arg == "./..." || arg == "...") {
				f.catchAll = true
			}
			named[arg] = true
		}
	}
	if f.catchAll {
		f.testedPkgs = f.withTests
		return f
	}
	for a := range named {
		f.named = append(f.named, a)
	}
	sort.Strings(f.named)
	f.testedPkgs = countCovered(pkgs, f.named)
	return f
}

// packageArgs pulls the package arguments out of a `go test` argument string,
// skipping flags and the values that belong to them.
func packageArgs(args string) []string {
	var out []string
	fields := strings.Fields(args)
	for i := 0; i < len(fields); i++ {
		w := fields[i]
		if strings.HasPrefix(w, "-") {
			// -timeout 20m and -run TestX take a separate value; -x=y does not.
			if !strings.Contains(w, "=") && i+1 < len(fields) && !strings.HasPrefix(fields[i+1], "-") &&
				!pkgArgRE.MatchString(fields[i+1]) {
				i++
			}
			continue
		}
		if pkgArgRE.MatchString(w) || strings.Contains(w, "/") {
			out = append(out, strings.TrimSuffix(w, "/"))
		}
	}
	return out
}

// countCovered counts how many of a repository's test packages fall under at
// least one named path.
func countCovered(pkgs []string, named []string) int {
	n := 0
	for _, p := range pkgs {
		for _, a := range named {
			a = strings.TrimPrefix(strings.TrimSuffix(a, "/..."), "./")
			a = strings.TrimSuffix(a, "/")
			if a == "" || a == "." {
				// `go test .` is the root package only.
				if p == "." {
					n++
					break
				}
				continue
			}
			if p == a || strings.HasPrefix(p, a+"/") {
				n++
				break
			}
		}
	}
	return n
}

// testPackages lists the directories holding _test.go files, relative to the
// repository root, and counts how many of those files sit behind a build tag.
func testPackages(dir string) ([]string, int) {
	seen := map[string]bool{}
	tagged := 0
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(dir, filepath.Dir(p))
		if err != nil {
			return nil
		}
		seen[rel] = true
		if b, err := os.ReadFile(p); err == nil {
			head := b
			if len(head) > 512 {
				head = head[:512]
			}
			if strings.Contains(string(head), "//go:build ") && !strings.Contains(string(head), "//go:build ignore") {
				tagged++
			}
		}
		return nil
	})
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, tagged
}

// readWorkflows concatenates every workflow file, returning the text and the
// file count. The count is the positive control: a scan that silently read
// nothing would otherwise report every repository as having no CI.
func readWorkflows(dir string) (string, int, error) {
	wf := filepath.Join(dir, ".github", "workflows")
	entries, err := os.ReadDir(wf)
	if err != nil {
		return "", 0, err
	}
	var sb strings.Builder
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".yml") && !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(wf, e.Name()))
		if err != nil {
			continue
		}
		sb.Write(b)
		sb.WriteByte('\n')
		n++
	}
	return sb.String(), n, nil
}

// readDelegate reads the Taskfile or Makefile a workflow hands its work to,
// so `task ci` can be followed to the `go test` inside it rather than
// reported as an absence.
//
// It reads only files at the repository root, and only these names: following
// arbitrary shell would be guessing again, and the point is the opposite.
func readDelegate(dir string) (string, bool) {
	for _, name := range taskfileNames {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			return string(b), true
		}
	}
	return "", false
}

// gitRef runs one git command inside a repository and returns stdout.
func gitRef(dir string, args ...string) (string, bool) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// defaultRemoteRef resolves origin's default branch, e.g. "origin/main".
func defaultRemoteRef(dir string) (string, bool) {
	if s, ok := gitRef(dir, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); ok {
		if r := strings.TrimSpace(s); r != "" {
			return r, true
		}
	}
	for _, r := range []string{"origin/main", "origin/master"} {
		if _, ok := gitRef(dir, "rev-parse", "--verify", "-q", r); ok {
			return r, true
		}
	}
	return "", false
}

// scanRef answers the same question as scan, but about what the REMOTE holds
// rather than what this checkout happens to contain.
//
// The working tree is a cache, and on this machine it is often weeks stale: of
// thirteen repositories reported as having no CI at all, ELEVEN already had a
// workflow on origin/main. Reading the ref removes that whole class of error
// instead of asking the operator to remember to fetch — and `git fetch` is
// still required first, because origin/main in a clone is itself a cached
// value.
func scanRef(root, repo string) (finding, bool) {
	dir := filepath.Join(root, repo)
	ref, ok := defaultRemoteRef(dir)
	if !ok {
		// No remote at all. That is worth reporting on its own: cloud-boot/init
		// holds nine packages with tests and exists nowhere but this disk.
		f, has := scan(root, repo)
		if has {
			f.noRemote = true
		}
		return f, has
	}
	listing, ok := gitRef(dir, "ls-tree", "-r", "--name-only", ref)
	if !ok {
		return scan(root, repo)
	}

	f := finding{repo: repo}
	var wf []string
	seen := map[string]bool{}
	for _, p := range strings.Split(listing, "\n") {
		p = strings.TrimSpace(p)
		switch {
		case p == "":
		case strings.HasPrefix(p, ".github/workflows/") &&
			(strings.HasSuffix(p, ".yml") || strings.HasSuffix(p, ".yaml")):
			wf = append(wf, p)
		case strings.HasSuffix(p, "_test.go"):
			d := filepath.Dir(p)
			if d == "vendor" || strings.HasPrefix(d, "vendor/") ||
				d == "testdata" || strings.Contains(d, "/testdata") {
				continue
			}
			seen[d] = true
		}
	}
	f.withTests = len(seen)
	if f.withTests == 0 {
		return f, false
	}
	pkgs := make([]string, 0, len(seen))
	for k := range seen {
		pkgs = append(pkgs, k)
	}
	sort.Strings(pkgs)

	if len(wf) == 0 {
		f.noCI = true
		return f, true
	}
	var sb strings.Builder
	for _, w := range wf {
		if body, ok := gitRef(dir, "show", ref+":"+w); ok {
			sb.WriteString(body)
			sb.WriteByte('\n')
		}
	}
	yaml := sb.String()
	f.workflows, f.bytesRead = len(wf), len(yaml)
	f.behind, f.fetchAge, f.fetchKnown = checkout.Staleness(dir)
	return classify(f, yaml, pkgs, dir), true
}

// render turns one finding into its output line.
//
// Separated from main so a TEST can assert on the line, not merely on the
// struct field behind it. `noRemote` was set correctly and never printed: the
// field had a test, the output did not, and a value nobody reads is the same
// as a value nobody set.
func render(f finding) string {
	switch {
	case f.noRemote:
		// Not the same problem as a missing lane, and worse: there is nowhere
		// for a lane to run. cloud-boot/init holds nine packages with tests
		// and exists on no machine but this one.
		return fmt.Sprintf("%-42s  NO REMOTE           %d package(s), exists only on this disk\n", f.repo, f.withTests)
	case f.noCI:
		return fmt.Sprintf("%-42s  no CI at all        %d package(s) with tests\n", f.repo, f.withTests)
	case f.noGoTest && f.viaScript:
		return fmt.Sprintf("%-42s  defers to a script  %d with tests, cannot tell\n", f.repo, f.withTests)
	case f.noGoTest:
		return fmt.Sprintf("%-42s  no go test in CI    %d package(s) with tests\n", f.repo, f.withTests)
	case f.covered():
		return fmt.Sprintf("%-42s  covered             %d package(s)\n", f.repo, f.withTests)
	case f.dynamic && f.testedPkgs == 0:
		// Every invocation computed its own package set. Nothing readable here
		// says how much that covers, and a number would be a lie.
		return fmt.Sprintf("%-42s  computed set        %d with tests, cannot tell\n", f.repo, f.withTests)
	default:
		return fmt.Sprintf("%-42s  %d of %d tested       %s\n", f.repo, f.testedPkgs, f.withTests,
			strings.Join(f.named, " "))
	}
}
