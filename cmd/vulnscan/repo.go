package main

import (
	"context"
	"fmt"
	"go/build"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

// runner is the one seam through which this program starts a process: git to
// clone, govulncheck to scan. dir is the working directory, env is ADDED to the
// inherited environment.
//
// ⛔ A TEST MUST NEVER REACH A REAL ONE. mutsweep's tests spawned the real
// thing and became a fork bomb -- load 583, 10894 processes. Every test here
// replaces this with a fake that writes files and returns recorded output, and
// TestMain makes the real one panic, so a test that forgets is a failure and
// not a process.
type runner func(ctx context.Context, dir string, env []string, name string, args ...string) (stdout, stderr []byte, err error)

// cloneURL is where a repository is cloned from. https and no credential in
// it: the credential helper answers over a pipe, and a public repository needs
// none at all.
func cloneURL(repo string) string { return "https://github.com/" + repo + ".git" }

// cloneAttempts is how many times a clone REFUSED FOR GOING TOO FAST is tried.
//
// ⛔ Short waits on purpose. The aim is to ride out a burst limit, not to sit
// out an hourly reset: a 49-repository sweep that waited a minute per refusal
// would spend most of an hour on the ones it could not read. A repository still
// refused after these attempts stays UNREAD and the sweep moves on, which is
// the honest answer -- and `-state` with `-retry-unread` is how a person
// finishes it later, when the limit has actually cleared.
const cloneAttempts = 3

// cloneBackoff is the wait before the next attempt. A var so a test does not
// take a minute to prove a retry happened.
var cloneBackoff = func(attempt int) { time.Sleep(time.Duration(attempt) * 20 * time.Second) }

// refusedForPace reports whether git's stderr says the refusal was about the
// RATE of our requests rather than about the repository.
//
// ⛔ THE DISTINCTION IS THE WHOLE POINT. A 403 or 429 on the transport means
// "come back in a moment" -- measured 2026-10-31, a 58-repository sweep from
// one workstation got `The requested URL returned error: 403` on 14 clones
// while nothing was wrong with any of them. A 404, a "Repository not found" or
// a request for a username is NOT transient: the repository was deleted, made
// private, or renamed, and retrying it burns the budget that would have read
// the next one. Those must fail on the first attempt, which is why this matches
// the pace signatures only and never a generic failure.
func refusedForPace(stderr []byte) bool {
	s := string(stderr)
	return strings.Contains(s, "error: 403") ||
		strings.Contains(s, "error: 429") ||
		strings.Contains(s, "rate limit")
}

// latestTag is the version a consumer resolves: the highest semantic version
// among the repository's ROOT-module tags, read from the remote without cloning
// it.
//
// ⛔ WHY THIS EXISTS. govulncheck over a clone of the DEFAULT BRANCH answers
// about the tree nobody imports. Measured 2026-11-15 on go-xrkit/player: at
// v0.1.0 -- the only version published at the time -- the code CALLED five
// vulnerabilities (four x/net HTTP/2 advisories and one in brotli), while main
// was clean, because the dependencies carrying them had left the graph. A sweep
// of that organisation would have printed the repository CLEAN, truthfully,
// about a tree no consumer had ever fetched.
//
// ls-remote and not the API: this is git over https, which is metered apart from
// the REST budget the fleet's Renovate runners share.
func latestTag(ctx context.Context, run runner, repo string) (string, error) {
	stdout, stderr, err := run(ctx, "", []string{"GIT_TERMINAL_PROMPT=0"},
		"git", "ls-remote", "--tags", "--refs", cloneURL(repo))
	if err != nil {
		return "", fmt.Errorf("git ls-remote %s: %v: %s", repo, err, tail(stderr))
	}
	return highestTag(string(stdout)), nil
}

// highestTag picks the tag out of `git ls-remote --tags --refs` output, whose
// lines are "<sha>\trefs/tags/<tag>". It returns "" when the repository
// publishes no version this scan can read, which is a state of its own and not
// an error.
//
// ⛔ A NESTED MODULE'S TAG IS NOT THIS MODULE'S VERSION. `sub/dir/v1.2.3`
// versions the module in that directory; taking it as the repository's would
// scan a tree whose root module is at some other version entirely. Only a bare
// vX.Y.Z counts, and semver.IsValid is what says so: it requires the leading
// "v", so every nested form fails it.
//
// ⚠ That last sentence is the measurement and not a guess. This function first
// carried an explicit `strings.Contains(tag, "/")` beside IsValid; mutating it
// away left the tests GREEN, which is how dead code announces itself. The case
// is covered below by what rejects it rather than by a second guard nobody
// exercises.
//
// ⛔ AND A PRERELEASE IS NOT WHAT `@latest` RESOLVES. Go's module proxy serves
// the highest RELEASE version and falls back to a prerelease only when there is
// no release at all, so that is the order here: any release beats every
// prerelease, and within a kind the higher version wins. A sweep that scanned
// v2.0.0-rc1 while consumers were served v1.9.0 would be measuring a tree
// nobody has, which is the defect this whole flag exists to remove.
func highestTag(lsRemote string) string {
	var best string
	bestIsRelease := false
	for _, line := range strings.Split(lsRemote, "\n") {
		_, after, ok := strings.Cut(line, "refs/tags/")
		if !ok {
			continue
		}
		tag := strings.TrimSpace(after)
		if !semver.IsValid(tag) {
			continue
		}
		release := semver.Prerelease(tag) == ""
		switch {
		case best == "",
			release && !bestIsRelease,
			release == bestIsRelease && semver.Compare(tag, best) > 0:
			best, bestIsRelease = tag, release
		}
	}
	return best
}

// clone makes a depth-1 copy of repo at dir and reports how many times it was
// refused for pace before it got through. ref is a branch or tag to clone
// instead of the default branch; empty means the default branch.
//
// GIT_TERMINAL_PROMPT=0 because a repository that was deleted or made private
// otherwise asks for a username on a terminal nobody is watching, and the
// sweep stops there for good.
//
// ⛔ A REFUSAL THAT IS RECOVERABLE MUST NOT BECOME A VERDICT. Reporting UNREAD
// for a transient 403 is correct about this run and wrong about the repository,
// and UNREAD makes the lane red -- so a sweep could go red over nothing but its
// own pace. The retry count is returned rather than swallowed, so a slow pass
// is legible instead of mysterious.
func clone(ctx context.Context, run runner, repo, dir, ref string) (refused int, err error) {
	args := []string{"clone", "--quiet", "--depth", "1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, cloneURL(repo), dir)
	for attempt := 1; ; attempt++ {
		_, stderr, runErr := run(ctx, "", []string{"GIT_TERMINAL_PROMPT=0"}, "git", args...)
		if runErr == nil {
			return refused, nil
		}
		if attempt == cloneAttempts || !refusedForPace(stderr) {
			return refused, fmt.Errorf("git clone %s: %v: %s", repo, runErr, tail(stderr))
		}
		refused++
		cloneBackoff(attempt)
	}
}

// skipDir is a directory no module of the repository's own lives under. The go
// command itself ignores a directory whose name starts with "_" or ".", so a
// go.mod there is a snapshot nobody builds -- cloud-boot/tamago-uefi keeps one
// as `_ufs_at_head`, whose replace has no target, and scanning it made the
// repository UNREAD.
func skipDir(name string) bool {
	return name == "node_modules" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// hasGoCode reports whether a module holds a non-test .go file THIS SCAN CAN
// BUILD -- outside nested modules, testdata and ignored directories.
//
// ⛔ A go.mod IS NOT GO CODE. Hugo sites declare their theme as a Go module:
// go-fft/docs, go-pdfkit/docs and pocketdesk/docs each have a go.mod and not
// one .go file, and govulncheck answers "no packages matched" (exit 2). That is
// neither a finding nor a failure to read, so such a module is not scanned, and
// a repository made only of them is NO-GO.
//
// ⛔ AND A FILE THE BUILD EXCLUDES IS NOT CODE EITHER -- which counting .go
// files could not tell. Measured 2026-10-31, deploying the scheduled sweep:
// go-fleettools/landingkit came back UNREAD, and the whole reason was
// example/kit.go and example-i18n/kit.go, which say
//
//	//go:build tools
//	// This file never builds. It is here so that `go mod tidy` -- which
//	// Renovate runs after every bump -- sees a use of the module and keeps
//	// its requirement: a Hugo site has no Go code of its own.
//
// That idiom is deliberate, documented, and on every landing in this fleet, and
// `tools` is a tag NO lane sets -- so unlike a GOOS constraint it can never be
// satisfied by scanning something else. A repository permanently UNREAD for a
// file that never builds is an alarm that gets read past, which is the one
// failure a scanner may not have.
//
// So the question is asked of the go/build CONTEXT rather than of the filename,
// under the scan's own GOOS and GOARCH and with no custom tags -- the context
// govulncheck itself uses. This also settles the case the previous comment here
// accepted and left: a module whose files are all constrained to another GOOS
// is now named as not scanned, instead of being reported as unreadable.
//
// ⛔ It does NOT soften "UNREAD IS NOT CLEAN". Anything that holds a buildable
// file and still fails to load is scanned and stays UNREAD; what changes is
// only the case where this scan has nothing to compile at all, and the note
// says which module and why.
func hasGoCode(modDir, goos, goarch string) bool {
	bc := build.Default
	bc.GOOS, bc.GOARCH = goos, goarch
	bc.UseAllFiles = false
	bc.BuildTags = nil
	bc.ToolTags = nil

	found := false
	filepath.WalkDir(modDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if p == modDir {
				return nil
			}
			if skipDir(d.Name()) || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") && !strings.HasSuffix(d.Name(), "_test.go") {
			// ⛔ MatchFile READS THE FILE: it applies //go:build and the
			// _GOOS/_GOARCH name suffixes, so a file this scan cannot compile
			// does not count. An error here means the file could not be read or
			// its constraint could not be parsed -- which is NOT a reason to
			// call the module empty, so it counts as code and the scan decides.
			ok, err := bc.MatchFile(filepath.Dir(p), d.Name())
			if err != nil || ok {
				found = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	return found
}

// fixtureDir reports whether a module directory sits under testdata or vendor,
// where a go.mod is a fixture or a copy, not something anybody builds.
func fixtureDir(rel string) bool {
	for _, p := range strings.Split(filepath.ToSlash(rel), "/") {
		if p == "testdata" || p == "vendor" {
			return true
		}
	}
	return false
}

// modules lists every module directory in a clone, relative to it, root first.
//
// A fixture module under testdata/ or vendor/ is skipped, UNLESS it replaces
// something with ../ -- that is a module built against the repository's own
// code (an example, an integration harness), and its imports are real.
func modules(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "go.mod" {
			return nil
		}
		dir := filepath.Dir(p)
		rel, _ := filepath.Rel(root, dir)
		if fixtureDir(rel) && len(localReplaces(p)) == 0 {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if (out[i] == ".") != (out[j] == ".") {
			return out[i] == "."
		}
		return out[i] < out[j]
	})
	return out, err
}

// localReplaces lists the directory targets of a go.mod's replace directives
// that point at a path on disk, as written.
func localReplaces(gomod string) []string {
	b, err := os.ReadFile(gomod)
	if err != nil {
		return nil
	}
	// ⛔ NOT ParseLax: it drops replace directives by design (they only apply
	// to a main module), and this function then reported none -- every
	// sibling went uncloned, and the test below caught it.
	f, err := modfile.Parse(gomod, b, nil)
	if err != nil {
		return nil
	}
	var out []string
	for _, r := range f.Replace {
		if r.New.Version == "" && (strings.HasPrefix(r.New.Path, "./") || strings.HasPrefix(r.New.Path, "../")) {
			out = append(out, r.New.Path)
		}
	}
	return out
}

// siblings clones every repository that a module's `replace => ../X` reaches,
// and the ones THOSE reach in turn.
//
// ⛔ WITHOUT THIS THE SCAN IS UNREAD, NOT SMALLER. A replace whose directory is
// missing stops `go list` cold -- fixtures/loadfail.stderr is that failure --
// so a repository developed beside its siblings could never be scanned alone.
//
// Clones are laid out as root/org/repo, the layout these replaces are written
// against: ../X from a repository's root is root/org/X, a sibling in the same
// organisation, and ../../other-org/X is root/other-org/X. The first real run
// met the second kind in its first fifty repositories
// (cloud-boot/tamago-uefi -> go-filesystems/interface), so it is cloned too. A
// target that leaves root entirely, or stops at root/org, is reported, not
// guessed at.
func siblings(ctx context.Context, run runner, root, repo, repoDir string, mods []string) (cloned, problems []string) {
	var queue []string
	for _, m := range mods {
		queue = append(queue, filepath.Join(repoDir, m))
	}
	seenDir := map[string]bool{}
	have := map[string]bool{repo: true}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		if seenDir[dir] {
			continue
		}
		seenDir[dir] = true
		for _, target := range localReplaces(filepath.Join(dir, "go.mod")) {
			abs := filepath.Clean(filepath.Join(dir, target))
			rel, err := filepath.Rel(root, abs)
			parts := strings.Split(filepath.ToSlash(rel), "/")
			if err != nil || parts[0] == ".." || len(parts) < 2 {
				problems = append(problems, fmt.Sprintf("replace %s points outside any org/repo", target))
				continue
			}
			name := parts[0] + "/" + parts[1]
			if !have[name] {
				have[name] = true
				// A sibling gets the same retry as the repository itself: a
				// replace whose target was refused for pace makes the whole
				// module fail to load, so it is the same defect one level
				// down. The count goes into problems, where a reader
				// already looks for why a sibling was odd.
				refused, cerr := clone(ctx, run, name, filepath.Join(root, parts[0], parts[1]), "")
				if refused > 0 {
					problems = append(problems, fmt.Sprintf("sibling %s: clone refused %d time(s) for pace and retried", name, refused))
				}
				if err := cerr; err != nil {
					problems = append(problems, "sibling "+err.Error())
					continue
				}
				cloned = append(cloned, name)
			}
			if _, err := os.Stat(filepath.Join(abs, "go.mod")); err == nil {
				queue = append(queue, abs)
			}
		}
	}
	sort.Strings(cloned)
	return cloned, problems
}

var goVersionLine = regexp.MustCompile(`(?m)^\s*-?\s*go-version(?:-file)?:\s*(.+?)\s*$`)

// ciGo lists the distinct go-version values the repository's workflows ask
// setup-go for. The scan is judged against ONE toolchain (-go); this is what
// lets the reader see where that differs from what CI actually builds with.
func ciGo(repoDir string) []string {
	files, _ := filepath.Glob(filepath.Join(repoDir, ".github", "workflows", "*.y*ml"))
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, m := range goVersionLine.FindAllStringSubmatch(string(b), -1) {
			v := strings.Trim(m[1], `'"`)
			if strings.Contains(m[0], "go-version-file") {
				v = "file:" + v
			}
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	sort.Strings(out)
	return out
}
