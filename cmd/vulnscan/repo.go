package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
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

// clone makes a depth-1 copy of repo's default branch at dir.
//
// GIT_TERMINAL_PROMPT=0 because a repository that was deleted or made private
// otherwise asks for a username on a terminal nobody is watching, and the
// sweep stops there for good.
func clone(ctx context.Context, run runner, repo, dir string) error {
	_, stderr, err := run(ctx, "", []string{"GIT_TERMINAL_PROMPT=0"},
		"git", "clone", "--quiet", "--depth", "1", cloneURL(repo), dir)
	if err != nil {
		return fmt.Errorf("git clone %s: %v: %s", repo, err, tail(stderr))
	}
	return nil
}

// skipDir is a directory no module of the repository's own lives under.
func skipDir(name string) bool {
	return name == ".git" || name == "node_modules"
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

// siblings clones every repository of the same organisation that a module's
// `replace => ../X` reaches, and the ones THOSE reach in turn.
//
// ⛔ WITHOUT THIS THE SCAN IS UNREAD, NOT SMALLER. A replace whose directory is
// missing stops `go list` cold -- fixtures/loadfail.stderr is that failure --
// so a repository developed beside its siblings could never be scanned alone.
//
// orgDir is the directory the repository was cloned into (…/org), so a target
// that leaves the clone lands in orgDir/X, which is where X is cloned. A target
// that leaves orgDir as well names another organisation and is reported, not
// guessed at.
func siblings(ctx context.Context, run runner, org, orgDir, repoDir string, mods []string) (cloned, problems []string) {
	var queue []string
	for _, m := range mods {
		queue = append(queue, filepath.Join(repoDir, m))
	}
	seenDir := map[string]bool{}
	have := map[string]bool{filepath.Base(repoDir): true}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		if seenDir[dir] {
			continue
		}
		seenDir[dir] = true
		for _, target := range localReplaces(filepath.Join(dir, "go.mod")) {
			abs := filepath.Clean(filepath.Join(dir, target))
			rel, err := filepath.Rel(orgDir, abs)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				problems = append(problems, fmt.Sprintf("replace %s leaves the organisation", target))
				continue
			}
			name := strings.Split(filepath.ToSlash(rel), "/")[0]
			if !have[name] {
				have[name] = true
				if err := clone(ctx, run, org+"/"+name, filepath.Join(orgDir, name)); err != nil {
					problems = append(problems, "sibling "+err.Error())
					continue
				}
				cloned = append(cloned, org+"/"+name)
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
