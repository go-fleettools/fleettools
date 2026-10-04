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
// ⛔ A MINOR SUGGESTION IS NOT PROOF THE API GREW. gorelease bumps the minor
// for THREE reasons, and its source is the only place that says so:
//
//	} else if r.haveCompatibleChanges || (r.haveIncompatibleChanges && major == "0") || r.requirementsChanged() {
//
// and requirementsChanged() is true when a requirement moved up a MINOR, when
// one was added, or when the go directive rose. go-simd/floats suggests v0.2.0
// with no changes section printed at all: its only cause is
// golang.org/x/sys v0.46.0 -> v0.48.0. A first version of this labelled that
// "additions only", which the tool never said -- a story invented to fill a
// column. The three causes are now reported apart, and the absence of both
// sections is reported as what it is: the exported API did not change.
//
// ⛔ A FROZEN TAG IS NOT AN ABSENT ONE. Eight modules carry a tag that
// `git tag --merged` cannot see, because a history rewrite left it on an
// orphaned line -- GitHub answers "No common ancestor". A first version filed
// them under "never tagged", which is false and dangerous in the one direction
// that matters: go-filesystems/xfs v0.1.0 is published and required by SIX
// repositories, and "never tagged" invites cutting it again. They get their own
// section. gorelease still answers for them, because it fetches the base from
// the module proxy and never consults git ancestry.
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
	"time"

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
	breaks    int // "## incompatible changes" sections
	grew      int // "## compatible changes" sections
	err       string
}

type verdict struct {
	repo     string
	base     string
	next     string // the highest suggestion across platforms
	breaks   int    // platforms reporting incompatible changes
	disagree bool   // platforms suggested different versions
	grew     int    // platforms reporting compatible (additive) changes
	frozen   bool   // the base tag is unreachable from the branch: a rewritten history
	sha      string // the commit the verdict was measured on
	branch   string // the default branch that commit was the head of
	unread   []string
}

// safe reports whether this module can be tagged without anybody looking at it:
// every platform read, every platform agreed, nothing broke, and the bump is a
// patch -- which in semver means the exported API did not change at all.
func (v verdict) safe() bool {
	// ⛔ A frozen base is never safe however small the number looks: the tag
	// names a tree from before a rewrite, so a person has to see it.
	return len(v.unread) == 0 && !v.disagree && !v.frozen && v.breaks == 0 && isPatchOf(v.base, v.next)
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
	apply := flag.String("apply", "", "CREATE TAGS for these categories, comma-separated: safe, no-api-change, additions, frozen. There is deliberately no value for incompatible.")
	dryRun := flag.Bool("dry-run", false, "with -apply, say what would be tagged and create nothing")
	pause := flag.Duration("pause", 2*time.Second, "with -apply, wait this long between tags; GitHub's SECONDARY limit brakes a burst and waiting is the whole remedy")
	flag.Parse()
	os.Exit(run(os.Stdout, os.Stderr, *root, *only, strings.Split(*platforms, ","), *jobs, *doFetch, *apply, *dryRun, *pause))
}

func run(stdout, stderr io.Writer, root, only string, platforms []string, jobs int, doFetch bool, apply string, dryRun bool, pause time.Duration) int {
	plan, err := parseApply(apply)
	if err != nil {
		fmt.Fprintln(stderr, "-apply:", err)
		return 2
	}
	if _, err := exec.LookPath("gorelease"); err != nil {
		fmt.Fprintln(stderr, "vscan needs gorelease on PATH:")
		fmt.Fprintln(stderr, "    go install golang.org/x/exp/cmd/gorelease@latest")
		return 2
	}
	repos, err2 := checkout.Repos(root, only)
	if err2 != nil {
		err = err2
	}
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
			base, frozen, ok := latestTag(dir, ref)
			if !ok {
				mu.Lock()
				noTag++
				mu.Unlock()
				return
			}
			if !frozen && sameCommit(dir, base, ref) {
				mu.Lock()
				atTag++
				mu.Unlock()
				return
			}
			tree, cleanup, ok := checkout.MaterialiseAt(dir, ref)
			defer cleanup()
			if !ok {
				mu.Lock()
				verdicts = append(verdicts, verdict{repo: repo, base: base, frozen: frozen, unread: []string{"could not materialise " + ref}})
				mu.Unlock()
				return
			}
			v := read(repo, tree, base, platforms)
			v.frozen = frozen
			v.branch = strings.TrimPrefix(ref, "origin/")
			if h, ok := git(dir, "rev-parse", ref); ok {
				v.sha = strings.TrimSpace(h)
			}
			mu.Lock()
			verdicts = append(verdicts, v)
			mu.Unlock()
		}(r, dir)
	}
	wg.Wait()

	sort.Slice(verdicts, func(i, j int) bool { return verdicts[i].repo < verdicts[j].repo })

	var safe, look, frozen, unread []verdict
	for _, v := range verdicts {
		switch {
		case len(v.unread) > 0:
			unread = append(unread, v)
		case v.frozen:
			frozen = append(frozen, v)
		case v.safe():
			safe = append(safe, v)
		default:
			look = append(look, v)
		}
	}

	fmt.Fprintf(stdout, "checkouts: %d   not a Go module: %d   never tagged: %d   already at its tag: %d\n",
		len(repos), notGo, noTag, atTag)
	fmt.Fprintf(stdout, "ahead of their tag: %d   derived safely: %d   want a look: %d   FROZEN base: %d   UNREAD: %d\n",
		len(verdicts), len(safe), len(look), len(frozen), len(unread))

	if len(safe) > 0 {
		fmt.Fprintf(stdout, "\nPATCH, every platform agreed, nothing broke -- the exported API did not change:\n")
		for _, v := range safe {
			fmt.Fprintf(stdout, "  %-46s %s -> %s\n", v.repo, v.base, v.next)
		}
	}
	if len(look) > 0 {
		fmt.Fprintf(stdout, "\nA patch is not enough for these. In v0.x the number alone cannot say whether\nsomething BROKE, and a minor does not even mean the API moved, so the reason is\nnamed next to each one:\n")
		for _, v := range look {
			why := []string{}
			if v.breaks > 0 {
				why = append(why, fmt.Sprintf("INCOMPATIBLE on %d platform(s)", v.breaks))
			}
			if v.grew > 0 {
				why = append(why, fmt.Sprintf("API additions on %d platform(s)", v.grew))
			}
			if v.disagree {
				why = append(why, "platforms disagreed")
			}
			// ⛔ Never invent a reason. When gorelease printed no changes
			// section, the exported API did not move and the bump comes from
			// its requirementsChanged() rule: a dependency up a minor, a new
			// requirement, or a raised go directive.
			if len(why) == 0 {
				why = append(why, "no API change reported -- requirements moved (dependency minor, or the go directive)")
			}
			fmt.Fprintf(stdout, "  %-46s %s -> %s   %s\n", v.repo, v.base, v.next, strings.Join(why, "; "))
		}
	}

	if len(frozen) > 0 {
		fmt.Fprintf(stdout, "\nFROZEN base: the latest tag sits on a history the branch no longer shares, so\n")
		fmt.Fprintf(stdout, "`go get -u` can never move off it. The suggestion still holds -- gorelease reads\n")
		fmt.Fprintf(stdout, "the base from the module proxy, not from git -- but a person should see these:\n")
		for _, v := range frozen {
			fmt.Fprintf(stdout, "  %-46s %s -> %s\n", v.repo, v.base, v.next)
		}
	}

	if plan.any() {
		fmt.Fprintf(stdout, "\n%s tags for: %s\n", map[bool]string{true: "WOULD create", false: "Creating"}[dryRun], apply)
		done, skipped, failed := applyTags(stdout, verdicts, plan, pause, dryRun)
		fmt.Fprintf(stdout, "  %d tag(s) %s, %d skipped\n",
			len(done), map[bool]string{true: "would be created", false: "created and read back"}[dryRun], len(skipped))
		if failed != nil {
			fmt.Fprintf(stdout, "  STOPPED at %s: %s\n", failed.repo, failed.note)
			fmt.Fprintf(stdout, "  Nothing after it was attempted. Fix that one and run again; what is done is skipped.\n")
			return 1
		}
		if len(skipped) > 0 {
			// Said out loud: a run that quietly left things alone is a run
			// whose total nobody can reconcile with the plan.
			fmt.Fprintf(stdout, "  (the %d skipped are listed above, each with its reason; none was written to)\n", len(skipped))
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
		if r.grew > 0 {
			v.grew++
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
		case strings.HasPrefix(line, "## compatible changes"):
			r.grew++
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
func latestTag(dir, ref string) (tag string, frozen bool, ok bool) {
	if t, found := highestTag(dir, "--merged", ref); found {
		return t, false, true
	}
	// Nothing reachable. A tag that exists anyway sits on a history the branch
	// no longer shares, which is a different answer from "never tagged".
	if t, found := highestTag(dir); found {
		return t, true, true
	}
	return "", false, false
}

func highestTag(dir string, extra ...string) (string, bool) {
	out, ok := git(dir, append([]string{"tag", "--list", "v*"}, extra...)...)
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
