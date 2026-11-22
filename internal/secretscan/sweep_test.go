package secretscan

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var epoch = time.Date(2026, 11, 22, 15, 0, 0, 0, time.UTC)

// fakeGit answers for the clones by directory name, the way a real clone
// would, so the sweep is tested without a fetch and without a network.
func fakeGit(t *testing.T) Git {
	return func(dir string, args ...string) (string, error) {
		name := filepath.Base(dir)
		switch args[0] {
		case "config":
			switch name {
			case "a-one":
				return "https://github.com/org-a/one.git", nil
			case "c-three":
				return "git@github.com:org-b/three.git", nil
			case "f-six":
				return "plainpath", nil
			}
			return "", errors.New("no remote")
		case "rev-parse":
			switch name {
			case "a-one":
				return ".git/FETCH_HEAD", nil
			case "c-three":
				return filepath.Join(dir, "absent", "FETCH_HEAD"), nil
			case "g-seven":
				return ".git/FETCH_HEAD", nil
			}
			return "", errors.New("git is broken here")
		case "rev-list":
			if name == "a-one" {
				return "2\t3", nil
			}
			return "", errors.New("no upstream configured")
		}
		t.Fatalf("unexpected git %v", args)
		return "", nil
	}
}

func clone(t *testing.T, root, name string, files map[string]string) {
	t.Helper()
	for f, body := range files {
		p := filepath.Join(root, name, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func sweepFixture(t *testing.T) string {
	root := t.TempDir()
	clone(t, root, "a-one", map[string]string{
		".git/FETCH_HEAD": "x", "cmd/d/main.go": serverMain, "c/c.go": configGo,
		"go.mod": "require " + SecretsourcePath + " v0.8.0\n",
	})
	old := epoch.Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(root, "a-one", ".git", "FETCH_HEAD"), old, old); err != nil {
		t.Fatal(err)
	}
	clone(t, root, "c-three", map[string]string{".git/HEAD": "x", "u/app.service": "[Service]\nEnvironment=API_TOKEN=abc123\n"})
	clone(t, root, "d-four", map[string]string{".git/HEAD": "x", "a.go": "package a\n"})
	clone(t, root, "f-six", map[string]string{".git/HEAD": "x", "bad.go": "package {"})
	clone(t, root, "g-seven", map[string]string{".git/FETCH_HEAD": "x", "a.go": "package a\n"})
	clone(t, root, "b-two", map[string]string{"a.go": "package a\n"})
	clone(t, root, ".hidden", map[string]string{"a.go": "package a\n"})
	if err := os.WriteFile(filepath.Join(root, "loose.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSweepCountsWhatItReadAndWhatItDidNot(t *testing.T) {
	root := sweepFixture(t)
	fresh := epoch.Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(root, "g-seven", ".git", "FETCH_HEAD"), fresh, fresh); err != nil {
		t.Fatal(err)
	}
	rep, err := Sweep(root, fakeGit(t), epoch, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	d := rep.Denominator
	want := Denominator{
		Seen: 6, Read: 5, Skipped: 1, FilesRead: 7, FilesUnread: 1, Ignored: 2, GitUnreadable: 2,
		NeverFetched: 1, Stale: 2, DaemonRepos: 2, DaemonsPlaintext: 2, UsesSecretsource: 1,
	}
	if d != want {
		t.Errorf("denominator\n got %+v\nwant %+v", d, want)
	}
	if rep.Complete() {
		t.Error("a sweep with a skipped clone and unread files reported itself complete")
	}
	by := map[string]Clone{}
	for _, c := range rep.Clones {
		by[c.Dir] = c
	}
	a := by["a-one"]
	if a.Org != "org-a" || a.Staleness.Ahead != 2 || a.Staleness.Behind != 3 || !a.Staleness.Stale || a.Staleness.FetchedAt == nil {
		t.Errorf("a-one: %+v", a)
	}
	if c := by["c-three"]; c.Org != "org-b" || !c.Staleness.NeverFetched || !c.Staleness.NoUpstream || !c.Staleness.Stale {
		t.Errorf("c-three: %+v", c)
	}
	if g := by["g-seven"]; g.Staleness.Stale || !g.Staleness.NoUpstream || g.Org != "" {
		t.Errorf("g-seven (fetched an hour ago, nothing behind): %+v", g)
	}
	if f := by["f-six"]; f.Org != "" || f.Staleness.GitError == "" {
		t.Errorf("f-six: %+v", f)
	}
	if b := by["b-two"]; b.Read || !strings.Contains(b.SkipReason, "not a git clone") {
		t.Errorf("b-two: %+v", b)
	}
	if rep.Clones[0].Dir != "a-one" || rep.Clones[len(rep.Clones)-1].Dir != "g-seven" {
		t.Errorf("clones are not sorted: %v", rep.Clones)
	}
}

func TestASweepOfACleanCompleteFleetIsComplete(t *testing.T) {
	root := t.TempDir()
	clone(t, root, "g-seven", map[string]string{".git/FETCH_HEAD": "x", "a.go": "package a\n"})
	fresh := epoch.Add(-time.Minute)
	if err := os.Chtimes(filepath.Join(root, "g-seven", ".git", "FETCH_HEAD"), fresh, fresh); err != nil {
		t.Fatal(err)
	}
	rep, err := Sweep(root, fakeGit(t), epoch, time.Hour)
	if err != nil || !rep.Complete() || rep.Denominator.Read != 1 || len(rep.Top(0)) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
}

func TestSweepOfAMissingRootIsAnError(t *testing.T) {
	if _, err := Sweep(filepath.Join(t.TempDir(), "absent"), fakeGit(t), epoch, time.Hour); err == nil {
		t.Fatal("a root that is not there was swept as empty")
	}
}

func TestACloneThatCannotBeListedIsSkippedWithItsReason(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root lists a mode-0o111 directory")
	}
	root := t.TempDir()
	clone(t, root, "e-five", map[string]string{".git/HEAD": "x", "a.go": "package a\n"})
	dir := filepath.Join(root, "e-five")
	if err := os.Chmod(dir, 0o111); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	rep, err := Sweep(root, fakeGit(t), epoch, time.Hour)
	if err != nil || rep.Denominator.Skipped != 1 || rep.Clones[0].SkipReason == "" || rep.Complete() {
		t.Fatalf("%+v %v", rep, err)
	}
}

func TestTopAndOrgsRankTheExposure(t *testing.T) {
	rep, err := Sweep(sweepFixture(t), fakeGit(t), epoch, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	top := rep.Top(0)
	if len(top) != 2 || top[0].Exposure != 4 || top[0].Clone != "c-three" || top[0].Org != "org-b" {
		t.Fatalf("top = %+v", top)
	}
	if got := rep.Top(1); len(got) != 1 || got[0].Name != "API_TOKEN" {
		t.Errorf("Top(1) = %+v", got)
	}
	// Equal exposure falls back to clone, file, line.
	same := Report{Clones: []Clone{
		{Dir: "z", Repo: Repo{Findings: []Finding{newFinding("input", "flag", Plaintext, "token", "b.go", 2), newFinding("input", "flag", Plaintext, "token", "b.go", 1), newFinding("input", "flag", Plaintext, "token", "a.go", 9)}}},
		{Dir: "y", Repo: Repo{Findings: []Finding{newFinding("input", "flag", Plaintext, "token", "c.go", 1)}}},
	}}
	order := ""
	for _, h := range same.Top(0) {
		order += h.Clone + h.File + string(rune('0'+h.Line)) + " "
	}
	if order != "yc.go1 za.go9 zb.go1 zb.go2 " {
		t.Errorf("order %q", order)
	}
	orgs := rep.Orgs()
	if len(orgs) != 2 || orgs[0] != (OrgCount{Org: "org-a", Inputs: 1}) || orgs[1] != (OrgCount{Org: "org-b", Inputs: 1}) {
		t.Errorf("orgs = %+v", orgs)
	}
	tied := Report{Clones: []Clone{
		{Dir: "a", Org: "zeta", Repo: Repo{Findings: []Finding{newFinding("input", "flag", Plaintext, "token", "a.go", 1), newFinding("input", "flag", Plaintext, "token", "a.go", 2)}}},
		{Dir: "b", Org: "alpha", Repo: Repo{Findings: []Finding{newFinding("input", "flag", Plaintext, "token", "a.go", 1)}}},
		{Dir: "c", Repo: Repo{Findings: []Finding{newFinding("input", "flag", Plaintext, "token", "a.go", 1)}}},
	}}
	if got := tied.Orgs(); got[0].Org != "zeta" || got[1].Org != "(unknown)" || got[2].Org != "alpha" {
		t.Errorf("tied orgs = %+v", got)
	}
}

func TestTheReportsSayWhatWasNotRead(t *testing.T) {
	rep, err := Sweep(sweepFixture(t), fakeGit(t), epoch, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var js bytes.Buffer
	if err := rep.WriteJSON(&js); err != nil {
		t.Fatal(err)
	}
	var back Report
	if err := json.Unmarshal(js.Bytes(), &back); err != nil || back.Denominator != rep.Denominator || len(back.Clones) != len(rep.Clones) {
		t.Fatalf("round trip: %v", err)
	}
	var md bytes.Buffer
	if err := rep.WriteMarkdown(&md); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"| clones skipped (NOT READ) | 1 |", "Complete: NO, see below", "| b-two | clone | not a git clone (no .git) |",
		"| f-six | bad.go |", "NOT READ: rev-parse --git-path FETCH_HEAD", "never fetched; +0/-0",
		"; no upstream", "ahead 2, behind 3", "| a-one | org-a | c/c.go:", "- a-one", "unit-env",
		"| a-one | c/c.go:", "| true |",
	} {
		if !strings.Contains(md.String(), want) {
			t.Errorf("markdown lacks %q", want)
		}
	}
	complete := Report{Generated: epoch, Clones: []Clone{{Dir: "x|y", Read: true, Repo: Repo{Findings: []Finding{newFinding("sample", "doc", Plaintext, "token", "R|M.md", 3)}}, Staleness: Staleness{FetchedAt: &epoch}}}}
	md.Reset()
	if err := complete.WriteMarkdown(&md); err != nil || !strings.Contains(md.String(), "Complete: yes") || !strings.Contains(md.String(), `x\|y`) || !strings.Contains(md.String(), `R\|M.md:3`) {
		t.Errorf("%v\n%s", err, md.String())
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestAReportThatCannotBeWrittenSaysSo(t *testing.T) {
	var rep Report
	if rep.WriteJSON(failWriter{}) == nil || rep.WriteMarkdown(failWriter{}) == nil {
		t.Fatal("a failed write was reported as written")
	}
}

func TestOrgOfReadsBothURLForms(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/go-x/y.git": "go-x", "git@github.com:go-x/y": "go-x", "justaword": "", "/a/b": "a",
	} {
		got := orgOf(originOf(func(string, ...string) (string, error) { return url, nil }, "."))
		if got != want {
			t.Errorf("orgOf(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestExecGitRunsGitLocally(t *testing.T) {
	if out, err := ExecGit(".", "rev-parse", "--git-dir"); err != nil || out == "" {
		t.Errorf("rev-parse in a clone: %q %v", out, err)
	}
	if _, err := ExecGit(t.TempDir(), "rev-parse", "--git-dir"); err == nil {
		t.Error("git answered outside a repository")
	}
}

func TestWorktreesOfOneOriginAreOneRepository(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"w-one", "w-two", "w-three", "x-solo", "y-solo"} {
		clone(t, root, n, map[string]string{
			".git/HEAD": "x", "cmd/d/main.go": serverMain, "c/c.go": configGo,
		})
	}
	git := func(dir string, args ...string) (string, error) {
		switch args[0] {
		case "config":
			switch filepath.Base(dir) {
			case "w-one":
				return "https://github.com/go-x/y.git", nil
			case "w-two":
				return "git@github.com:Go-X/y", nil // same repository, other spelling
			case "w-three":
				return "https://github.com/go-x/y/", nil
			}
			return "", errors.New("no remote") // x-solo, y-solo: never merged
		case "rev-parse":
			return "", errors.New("git is broken here")
		}
		return "", errors.New("no upstream")
	}
	rep, err := Sweep(root, git, epoch, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	d := rep.Denominator
	if d.Read != 5 || d.DaemonRepos != 3 || d.DaemonsPlaintext != 3 {
		t.Errorf("denominator = %+v, want 5 clones read, 3 daemon repos, 3 with plaintext", d)
	}
	top := rep.Top(0)
	if len(top) != 3 {
		t.Fatalf("Top = %d findings, want 1 per repository (3): %+v", len(top), top)
	}
	if got := rep.Orgs(); len(got) != 2 || got[0].Org != "(unknown)" || got[0].Inputs != 2 || got[1].Org != "go-x" || got[1].Inputs != 1 {
		t.Errorf("Orgs = %+v", got)
	}
}

func TestOriginOfIsOneSpellingForHTTPSAndSSH(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/Go-X/y.git": "github.com/go-x/y",
		"git@github.com:go-x/y":         "github.com/go-x/y",
		"ssh://git@github.com/go-x/y/":  "github.com/go-x/y",
	} {
		got := originOf(func(string, ...string) (string, error) { return url, nil }, ".")
		if got != want {
			t.Errorf("originOf(%q) = %q, want %q", url, got, want)
		}
	}
	if (Clone{Dir: "d"}).Key() == (Clone{Dir: "e"}).Key() {
		t.Error("two clones without an origin share a key")
	}
}
