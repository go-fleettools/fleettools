package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRepo stands in for a clone: `git clone` populates a directory, so the
// stub does the same and everything after it is ordinary file handling.
type fakeRepo struct {
	files    map[string]string
	gitLog   []string
	pushed   string
	prArgs   string
	sent     map[string]string
	dir      string
	noClone  bool
	archived bool
}

func (r *fakeRepo) install(t *testing.T) func() {
	t.Helper()
	pg, pp, pgh := git, push, ghJSON
	git = func(dir string, args ...string) (string, error) {
		r.gitLog = append(r.gitLog, strings.Join(args, " "))
		if args[0] == "clone" {
			if r.noClone {
				return "fatal: repository not found", fmt.Errorf("exit 128")
			}
			target := args[len(args)-1]
			r.dir = target
			for p, body := range r.files {
				full := filepath.Join(target, p)
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					return "", err
				}
				if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
					return "", err
				}
			}
		}
		return "", nil
	}
	// Snapshot at push time, not after open returns: open removes its clone
	// on the way out, and what was PUSHED is the question anyway.
	push = func(dir, branch string) (string, error) {
		r.pushed = branch
		r.sent = map[string]string{}
		_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(dir, p)
			b, err := os.ReadFile(p)
			if err == nil {
				r.sent[filepath.ToSlash(rel)] = string(b)
			}
			return nil
		})
		return "", nil
	}
	ghJSON = func(args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if strings.HasPrefix(joined, "pr create") {
			r.prArgs = joined
			return []byte("https://example/pr/1"), nil
		}
		if strings.Contains(joined, "api repos/o/r --jq .default_branch") {
			if r.archived {
				return []byte("main\ntrue\n"), nil
			}
			return []byte("main\nfalse\n"), nil
		}
		return nil, fmt.Errorf("unexpected call: %s", joined)
	}
	return func() { git, push, ghJSON = pg, pp, pgh }
}

// read returns what the push carried, which is what a reviewer will see.
func (r *fakeRepo) read(t *testing.T, p string) string {
	t.Helper()
	v, ok := r.sent[p]
	if !ok {
		t.Fatalf("%s was not in what was pushed (pushed: %v)", p, keys(r.sent))
	}
	return v
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestOpenEditsTheWorkflowAndTheGoDirective(t *testing.T) {
	r := &fakeRepo{files: map[string]string{
		".github/workflows/ci.yml": "        with: { go-version: stable }\n          go-version: stable\n",
		"go.mod":                   "module x\n\ngo 1.26.4\n",
	}}
	defer r.install(t)()
	if err := open("o/r", finding{Repo: "o/r", Files: []string{"ci.yml"}, Aliases: 2, GoMod: "1.26.4"}, "1.27.1", ""); err != nil {
		t.Fatalf("open: %v", err)
	}
	wf := r.read(t, ".github/workflows/ci.yml")
	if strings.Contains(wf, "stable") {
		t.Errorf("an alias survived:\n%s", wf)
	}
	if strings.Count(wf, "'1.27.1'") != 2 {
		t.Errorf("both occurrences should be pinned:\n%s", wf)
	}
	if got := r.read(t, "go.mod"); !strings.Contains(got, "go 1.27.1") {
		t.Errorf("go.mod = %q", got)
	}
}

// The machine's rule, asserted rather than trusted: the push goes through
// gitpush, which names a credential helper instead of reading a token. A test
// cannot see which binary runs, but it CAN see that the tool never asks git
// to push — the only other way a token could reach a URL.
func TestOpenNeverAsksGitToPush(t *testing.T) {
	r := &fakeRepo{files: map[string]string{
		".github/workflows/ci.yml": "          go-version: stable\n",
	}}
	defer r.install(t)()
	if err := open("o/r", finding{Repo: "o/r", Files: []string{"ci.yml"}, Aliases: 1}, "1.27.1", ""); err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, c := range r.gitLog {
		if strings.HasPrefix(c, "push") {
			t.Fatalf("git was asked to push: %q", c)
		}
		// A credential on a command line is the other half of the same rule.
		if strings.Contains(c, "x-access-token") || strings.Contains(c, "@github.com") {
			t.Fatalf("a credential-shaped argument reached git: %q", c)
		}
	}
	if r.pushed == "" {
		t.Error("nothing was pushed")
	}
}

// A go.mod already at or past the target must not be rewritten: a commit that
// changes nothing is noise in 258 repositories.
func TestOpenLeavesAGoModThatIsAlreadyCurrent(t *testing.T) {
	r := &fakeRepo{files: map[string]string{
		".github/workflows/ci.yml": "          go-version: stable\n",
		"go.mod":                   "module x\n\ngo 1.28.0\n",
	}}
	defer r.install(t)()
	if err := open("o/r", finding{Repo: "o/r", Files: []string{"ci.yml"}, Aliases: 1, GoMod: "1.28.0"}, "1.27.1", ""); err != nil {
		t.Fatalf("open: %v", err)
	}
	if got := r.read(t, "go.mod"); !strings.Contains(got, "go 1.28.0") {
		t.Errorf("go.mod was moved backwards: %q", got)
	}
}

// If the edit turns out to change nothing, there must be no commit, no push
// and above all no empty pull request.
func TestOpenOpensNothingWhenThereIsNothingToChange(t *testing.T) {
	r := &fakeRepo{files: map[string]string{
		".github/workflows/ci.yml": "          go-version: '1.27.1'\n",
	}}
	defer r.install(t)()
	if err := open("o/r", finding{Repo: "o/r", Files: []string{"ci.yml"}, Aliases: 1}, "1.27.1", ""); err != nil {
		t.Fatalf("open: %v", err)
	}
	if r.pushed != "" || r.prArgs != "" {
		t.Errorf("pushed=%q pr=%q; want neither", r.pushed, r.prArgs)
	}
	for _, c := range r.gitLog {
		if strings.HasPrefix(c, "commit") {
			t.Errorf("an empty commit was made: %q", c)
		}
	}
}

func TestOpenReportsAFailedClone(t *testing.T) {
	r := &fakeRepo{noClone: true, files: map[string]string{}}
	defer r.install(t)()
	err := open("o/r", finding{Repo: "o/r", Files: []string{"ci.yml"}, Aliases: 1}, "1.27.1", "")
	if err == nil || !strings.Contains(err.Error(), "clone") {
		t.Fatalf("open: %v; want the clone named", err)
	}
}

func TestCommitMessageCarriesTheAttributionAndTheReason(t *testing.T) {
	m := commitMessage(finding{GoMod: "1.26.4"}, "1.27.1")
	for _, want := range []string{"Co-Authored-By: Claude Opus 5", "golang/go#81000", "go1.27.1", "1.26.4"} {
		if !strings.Contains(m, want) {
			t.Errorf("commit message lacks %q", want)
		}
	}
}

// An archived repository is a SKIP, not a failure. The sweep spent a clone,
// an edit and a push on go-freedesktop/dbus to learn that GitHub would refuse
// the push, and the result read like something had gone wrong.
func TestOpenSkipsAnArchivedRepositoryBeforeCloning(t *testing.T) {
	r := &fakeRepo{archived: true, files: map[string]string{
		".github/workflows/ci.yml": "          go-version: stable\n",
	}}
	defer r.install(t)()
	err := open("o/r", finding{Repo: "o/r", Files: []string{"ci.yml"}, Aliases: 1}, "1.27.1", "")
	if !errors.Is(err, errArchived) {
		t.Fatalf("open = %v; want errArchived", err)
	}
	// Before cloning, so the skip costs one call and not a clone plus a push.
	for _, c := range r.gitLog {
		t.Errorf("git ran for an archived repository: %q", c)
	}
	if r.pushed != "" {
		t.Error("an archived repository was pushed to")
	}
}
