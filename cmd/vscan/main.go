// Reports, for every tagged Go module whose default branch has moved past its
// latest tag, what the next version number should be -- DERIVED from the public
// API rather than guessed.
//
// tagscan says 280 of 333 tagged modules carry merged work `go get` cannot
// reach. Choosing a bump for each by hand is 280 chances to publish a number
// that lies about compatibility. golang.org/x/exp/cmd/gorelease answers the
// same question from the API diff, so this asks it and reports what it said.
//
// ⛔ IT ASKS ONCE PER PLATFORM, AND THIS IS NOT A REFINEMENT. gorelease reads
// the package under ONE build context. Removing New from go-xrkit/depth3d's
// !darwin file -- a break for every Linux consumer -- was measured on a darwin
// host as:
//
//	GOOS=darwin    Suggested version: v0.1.1    (a PATCH)
//	GOOS=linux     Suggested version: v0.2.0    incompatible changes: 1
//	GOOS=windows   Suggested version: v0.2.0    incompatible changes: 1
//
// One reading would have published a patch over a broken API. The verdict here
// is the HIGHEST suggestion across the platforms asked, and a module where any
// platform could not be read is reported as unread, never as agreed.
//
// ⛔ IT READS THE DEFAULT BRANCH, NEVER THE CHECKOUT. A first version ran
// gorelease where the clone happened to be, and go-simd/floats -- six commits
// behind -- came back v0.1.4 against v0.2.0 from its own branch head. A version
// derived from stale code is worse than no answer, because it looks like one.
// Each module is fetched, then materialised at origin's default ref, which also
// disposes of gorelease's refusal to read an unclean tree.
//
// ⛔ IN v0.x THE NUMBER DOES NOT TELL YOU WHETHER SOMETHING BROKE. Adding an
// exported symbol and removing one both suggest v0.2.0, because semver gives
// v0 no major slot. So the incompatible-changes section is recorded SEPARATELY
// from the number, and a module with one is never called safe.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/go-fleettools/fleettools/internal/checkout"
	"github.com/go-fleettools/fleettools/internal/fleet"
)

// Platforms is what every module is read as. GOARCH is left at the host's:
// an API that differs by architecture is rarer than one that differs by OS,
// and -platforms can name more.
var defaultPlatforms = []string{"linux", "darwin", "windows"}

type reading struct {
	goos      string
	suggested string
	breaks    int
	err       string
}

type verdict struct {
	repo     string
	base     string
	next     string // the highest suggestion across platforms
	breaks   int    // platforms reporting incompatible changes
	disagree bool   // platforms suggested different versions
	unread   []string
}

// safe reports whether this module can be tagged without anybody looking at it:
// every platform read, every platform agreed, nothing broke, and the bump is a
// patch -- which in semver means the exported API did not change at all.
func (v verdict) safe() bool {
	return len(v.unread) == 0 && !v.disagree && v.breaks == 0 && isPatchOf(v.base, v.next)
}

// isPatchOf reports whether next differs from base only in its patch field.
func isPatchOf(base, next string) bool {
	b, n := splitSemver(base), splitSemver(next)
	if b == nil || n == nil {
		return false
	}
	return b[0] == n[0] && b[1] == n[1] && b[2] != n[2]
}

// splitSemver returns the three numeric fields of vX.Y.Z, or nil when the tag
// is not that shape -- a prerelease or build suffix makes it nil on purpose,
// because "safe" must not be decided from a tag nobody can compare.
func splitSemver(v string) []string {
	if !strings.HasPrefix(v, "v") {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return nil
	}
	for _, p := range parts {
		if p == "" || strings.ContainsAny(p, "-+") {
			return nil
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return nil
			}
		}
	}
	return parts
}

// higher returns whichever of a and b is the later version, by field.
func higher(a, b string) string {
	x, y := splitSemver(a), splitSemver(b)
	if x == nil {
		return b
	}
	if y == nil {
		return a
	}
	for i := 0; i < 3; i++ {
		if len(x[i]) != len(y[i]) {
			if len(x[i]) > len(y[i]) {
				return a
			}
			return b
		}
		if x[i] != y[i] {
			if x[i] > y[i] {
				return a
			}
			return b
		}
	}
	return a
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	root := flag.String("root", checkout.DefaultRoot(), "directory holding org/repo checkouts")
	only := flag.String("repo", "", "read a single org/repo, verbosely")
	platforms := flag.String("platforms", strings.Join(defaultPlatforms, ","), "comma-separated GOOS values to read the API under")
	jobs := flag.Int("jobs", 4, "how many modules to read at once")
	doFetch := flag.Bool("fetch", true, "fetch each checkout, so the version is derived from the branch head and not from whatever the clone was left at")
	flag.Parse()
	os.Exit(run(os.Stdout, os.Stderr, *root, *only, strings.Split(*platforms, ","), *jobs, *doFetch))
}

func run(stdout, stderr io.Writer, root, only string, platforms []string, jobs int, doFetch bool) int {
	if _, err := exec.LookPath("gorelease"); err != nil {
		fmt.Fprintln(stderr, "vscan needs gorelease on PATH:")
		fmt.Fprintln(stderr, "    go install golang.org/x/exp/cmd/gorelease@latest")
		return 2
	}
	repos, err := checkout.Repos(root, only)
	if err != nil {
		fmt.Fprintln(stderr, "repos:", err)
		return 2
	}

	var (
		mu       sync.Mutex
		verdicts []verdict
		noTag    int
		atTag    int
		notGo    int
		sem      = make(chan struct{}, max(1, jobs))
		wg       sync.WaitGroup
	)
	for _, r := range repos {
		dir := filepath.Join(root, r)
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
			notGo++
			continue
		}
		wg.Add(1)
		go func(repo, dir string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Fetch first: the distance is the signal, and a clone is only ever
			// as current as its last fetch.
			if doFetch {
				_, _ = git(dir, "fetch", "--quiet", "--tags", "origin")
			}
			ref, ok := checkout.DefaultRef(dir)
			if !ok {
				mu.Lock()
				verdicts = append(verdicts, verdict{repo: repo, unread: []string{"no remote default ref"}})
				mu.Unlock()
				return
			}
			base, ok := latestTag(dir, ref)
			if !ok {
				mu.Lock()
				noTag++
				mu.Unlock()
				return
			}
			if sameCommit(dir, base, ref) {
				mu.Lock()
				atTag++
				mu.Unlock()
				return
			}
			tree, cleanup, ok := checkout.MaterialiseAt(dir, ref)
			defer cleanup()
			if !ok {
				mu.Lock()
				verdicts = append(verdicts, verdict{repo: repo, base: base, unread: []string{"could not materialise " + ref}})
				mu.Unlock()
				return
			}
			v := read(repo, tree, base, platforms)
			mu.Lock()
			verdicts = append(verdicts, v)
			mu.Unlock()
		}(r, dir)
	}
	wg.Wait()

	sort.Slice(verdicts, func(i, j int) bool { return verdicts[i].repo < verdicts[j].repo })

	var safe, look, unread []verdict
	for _, v := range verdicts {
		switch {
		case len(v.unread) > 0:
			unread = append(unread, v)
		case v.safe():
			safe = append(safe, v)
		default:
			look = append(look, v)
		}
	}

	fmt.Fprintf(stdout, "checkouts: %d   not a Go module: %d   never tagged: %d   already at its tag: %d\n",
		len(repos), notGo, noTag, atTag)
	fmt.Fprintf(stdout, "ahead of their tag: %d   derived safely: %d   want a look: %d   UNREAD: %d\n",
		len(verdicts), len(safe), len(look), len(unread))

	if len(safe) > 0 {
		fmt.Fprintf(stdout, "\nPATCH, every platform agreed, nothing broke -- the exported API did not change:\n")
		for _, v := range safe {
			fmt.Fprintf(stdout, "  %-46s %s -> %s\n", v.repo, v.base, v.next)
		}
	}
	if len(look) > 0 {
		fmt.Fprintf(stdout, "\nThe API moved. In v0.x the number cannot say whether something BROKE, so these want a person:\n")
		for _, v := range look {
			why := []string{}
			if v.breaks > 0 {
				why = append(why, fmt.Sprintf("INCOMPATIBLE on %d platform(s)", v.breaks))
			}
			if v.disagree {
				why = append(why, "platforms disagreed")
			}
			if len(why) == 0 {
				why = append(why, "additions only")
			}
			fmt.Fprintf(stdout, "  %-46s %s -> %s   %s\n", v.repo, v.base, v.next, strings.Join(why, "; "))
		}
	}

	// A sweep that could not read is not a sweep that found nothing.
	if len(unread) > 0 {
		fmt.Fprintf(stdout, "\nINCOMPLETE: this pass says nothing about what follows.\n")
		fmt.Fprintf(stdout, "  %d module(s) at least one platform could not be read for:\n", len(unread))
		for _, v := range unread {
			fmt.Fprintf(stdout, "      %-42s %s\n", v.repo, strings.Join(v.unread, "; "))
		}
		return 1
	}
	return 0
}

// read asks gorelease once per platform and folds the answers into one verdict.
func read(repo, dir, base string, platforms []string) verdict {
	v := verdict{repo: repo, base: base}
	seen := map[string]bool{}
	for _, goos := range platforms {
		goos = strings.TrimSpace(goos)
		if goos == "" {
			continue
		}
		r := askGorelease(dir, base, goos)
		if r.err != "" {
			v.unread = append(v.unread, goos+": "+r.err)
			continue
		}
		if r.breaks > 0 {
			v.breaks++
		}
		seen[r.suggested] = true
		if v.next == "" {
			v.next = r.suggested
		} else {
			v.next = higher(v.next, r.suggested)
		}
	}
	v.disagree = len(seen) > 1
	return v
}

// askGorelease is the exec seam; a test replaces it.
var askGorelease = func(dir, base, goos string) reading {
	cmd := exec.Command("gorelease", "-base="+base)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOWORK=off")
	out, _ := cmd.CombinedOutput()
	return parseGorelease(string(out), goos)
}

// parseGorelease reads what gorelease said.
//
// ⛔ It does NOT trust the exit status, and it treats a missing suggestion as
// an ERROR rather than as "no change". gorelease refuses a dirty tree with
// `gorelease: repo ... has uncommitted changes`, and a first draft of the probe
// that built this grepped for "Suggested" and found nothing -- which read
// exactly like a module with nothing to report.
func parseGorelease(out, goos string) reading {
	r := reading{goos: goos}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "Suggested version: "):
			r.suggested = strings.TrimSpace(strings.TrimPrefix(line, "Suggested version: "))
		case strings.HasPrefix(line, "## incompatible changes"):
			r.breaks++
		case strings.HasPrefix(line, "gorelease: "):
			r.err = strings.TrimSpace(strings.TrimPrefix(line, "gorelease: "))
		}
	}
	if r.suggested == "" && r.err == "" {
		r.err = "said nothing this could read"
	}
	if r.err != "" {
		r.suggested = ""
	}
	return r
}

// latestTag is the highest vX.Y.Z tag reachable from ref.
//
// ⛔ From REF, not from HEAD. A tag merged into the branch but not into the
// checkout's HEAD would otherwise be invisible, and the base would be an older
// release than the one consumers actually resolve.
func latestTag(dir, ref string) (string, bool) {
	out, ok := git(dir, "tag", "--list", "v*", "--merged", ref)
	if !ok {
		return "", false
	}
	best := ""
	for _, t := range strings.Split(strings.TrimSpace(out), "\n") {
		t = strings.TrimSpace(t)
		if splitSemver(t) == nil {
			continue
		}
		if best == "" || higher(best, t) == t {
			best = t
		}
	}
	return best, best != ""
}

// sameCommit reports whether tag and ref name the same commit, which is how a
// module already at its latest release is recognised.
func sameCommit(dir, tag, ref string) bool {
	a, ok1 := git(dir, "rev-list", "-n", "1", tag)
	b, ok2 := git(dir, "rev-parse", ref)
	return ok1 && ok2 && strings.TrimSpace(a) != "" && strings.TrimSpace(a) == strings.TrimSpace(b)
}

func git(dir string, args ...string) (string, bool) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err == nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
