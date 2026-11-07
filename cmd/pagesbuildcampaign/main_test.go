package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

type fake struct {
	t       *testing.T
	calls   []string
	held    map[string]bool   // repos another session holds
	yml     map[string]string // repo -> docs.yml served by the "clone"
	red     map[string]bool   // repos whose checks fail
	core    int
	clonedN int

	cloneFails map[string]int     // repo -> clones that answer 403 first
	watch      map[string][]reply // repo -> scripted `gh pr checks` answers, then the default
}

type reply struct {
	out  string
	code int
	err  error
}

const netErr = `Post "https://api.github.com/graphql": dial tcp 20.250.119.72:443: connect: can't assign requested address`

func (f *fake) merges() int {
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, "ghmerge ") {
			n++
		}
	}
	return n
}

func runOne(t *testing.T, f *fake, repo string) Result {
	t.Helper()
	cfg := cfgFor(t, f)
	cfg.State = filepath.Join(cfg.Workdir, "state.jsonl")
	rs, err := Campaign(cfg, []string{repo})
	if err != nil || len(rs) != 1 {
		t.Fatal(err, rs)
	}
	return rs[0]
}

func TestCloneRetriesAfter403ThroughGhRepoClone(t *testing.T) {
	f := &fake{t: t, yml: map[string]string{"a/docs": readGolden(t, "before.yml")}, core: 4000,
		cloneFails: map[string]int{"a/docs": 2}}
	if r := runOne(t, f, "a/docs"); r.Status != Merged {
		t.Fatalf("%+v", r)
	}
	if !f.has("gh repo clone a/docs ") {
		t.Error("did not clone through gh repo clone")
	}
}

func TestCloneFailsAfterBoundedAttempts(t *testing.T) {
	f := &fake{t: t, yml: map[string]string{"a/docs": readGolden(t, "before.yml")}, core: 4000,
		cloneFails: map[string]int{"a/docs": 99}}
	if r := runOne(t, f, "a/docs"); r.Status != Failed || !strings.Contains(r.Detail, "403") {
		t.Fatalf("%+v", r)
	}
}

func TestWatchTransportErrorThenGreenMerges(t *testing.T) {
	f := &fake{t: t, yml: map[string]string{"a/docs": readGolden(t, "before.yml")}, core: 4000,
		watch: map[string][]reply{"a/docs": {
			{out: netErr, code: 1},
			{err: errors.New("exec failed")},
			{out: "docs build\tpass\t1m\turl", code: 0},
		}}}
	if r := runOne(t, f, "a/docs"); r.Status != Merged || f.merges() != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestWatchWithZeroChecksIsNotGreen(t *testing.T) {
	// Exit 0 and no check line, every time: nothing was observed.
	f := &fake{t: t, yml: map[string]string{"a/docs": readGolden(t, "before.yml")}, core: 4000,
		watch: map[string][]reply{"a/docs": {{}, {}, {}, {}, {}, {}}}}
	r := runOne(t, f, "a/docs")
	if r.Status != Unobserved || r.PR != 7 || f.merges() != 0 {
		t.Fatalf("%+v merges=%d", r, f.merges())
	}
}

func TestPersistentWatchErrorIsUnobservedAndPROpen(t *testing.T) {
	q := make([]reply, 6)
	for i := range q {
		q[i] = reply{out: netErr, code: 1}
	}
	f := &fake{t: t, yml: map[string]string{"a/docs": readGolden(t, "before.yml")}, core: 4000,
		watch: map[string][]reply{"a/docs": q}}
	r := runOne(t, f, "a/docs")
	if r.Status != Unobserved || r.PR != 7 || f.merges() != 0 || !strings.Contains(r.Detail, "can't assign") {
		t.Fatalf("%+v", r)
	}
	if f.has("gh pr close") {
		t.Error("the pull request must stay open")
	}
}

func TestNoChecksYetThenRedIsFailedCI(t *testing.T) {
	f := &fake{t: t, yml: map[string]string{"a/docs": readGolden(t, "before.yml")}, core: 4000,
		watch: map[string][]reply{"a/docs": {{out: "no checks reported on the 'x' branch", code: 1},
			{out: "docs build\tfail\t1m\turl", code: 1}}}}
	if r := runOne(t, f, "a/docs"); r.Status != FailedCI || f.merges() != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestVerdict(t *testing.T) {
	for _, c := range []struct {
		out  string
		code int
		want int
	}{
		{"", 0, unseen},
		{"a\tpass\t1s\tu", 0, seenGreen},
		{"a\tpass\t1s\tu", 1, unseen},
		{"a\tskipping\t0\tu", 0, unseen},
		{"a\tpass\t1s\tu\nb\tfail\t1s\tu", 1, seenRed},
		{"a\tcancel\t1s\tu", 1, seenRed},
		{netErr, 1, unseen},
	} {
		if got := verdict(c.out, c.code); got != c.want {
			t.Errorf("verdict(%q,%d)=%d want %d", c.out, c.code, got, c.want)
		}
	}
}

func TestStateFormatBackwardCompatible(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	old := `{"repo":"cloud-boot/docs","status":"failed-ci","pr":25,"detail":"checks not green: x"}` + "\n"
	if err := os.WriteFile(p, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := loadState(p)
	if err != nil || m["cloud-boot/docs"].Status != FailedCI || m["cloud-boot/docs"].PR != 25 {
		t.Fatal(m, err)
	}
	if err := appendState(p, Result{Repo: "x/docs", Status: Unobserved, PR: 3}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `{"repo":"x/docs","status":"unobserved","pr":3}`) {
		t.Fatal(string(b))
	}
}

func (f *fake) run(dir, name string, args ...string) (string, int, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	switch {
	case name == "agentsync" && args[0] == "claim":
		if f.held[args[len(args)-1]] {
			return "held by another session", 1, nil
		}
		return "", 0, nil
	case name == "agentsync":
		return "", 0, nil
	case name == "gh" && args[0] == "api" && args[1] == "rate_limit":
		return `{"resources":{"core":{"remaining":` + itoa(f.core) + `}}}`, 0, nil
	case name == "gh" && args[0] == "api":
		return sha + "\n", 0, nil
	case name == "git" && args[0] == "clone":
		f.t.Errorf("unauthenticated git clone: %s", line)
		return "", 1, nil
	case name == "gh" && args[0] == "repo" && args[1] == "clone":
		repo := args[2]
		if f.cloneFails[repo] > 0 {
			f.cloneFails[repo]--
			return "fatal: unable to access: The requested URL returned error: 403", 128, nil
		}
		d := args[3]
		p := filepath.Join(d, pagesrollWorkflow)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.yml[repo]), 0o644); err != nil {
			f.t.Fatal(err)
		}
		return "", 0, nil
	case name == "git" && args[0] == "rev-parse":
		return sha + "\n", 0, nil
	case name == "gh" && args[0] == "pr" && args[1] == "create":
		return "https://github.com/" + args[3] + "/pull/7\n", 0, nil
	case name == "gh" && args[0] == "pr" && args[1] == "checks":
		if q := f.watch[args[4]]; len(q) > 0 {
			f.watch[args[4]] = q[1:]
			return q[0].out, q[0].code, q[0].err
		}
		if f.red[args[4]] {
			return "docs build\tfail", 1, nil
		}
		return "docs build\tpass", 0, nil
	}
	return "", 0, nil
}

const pagesrollWorkflow = ".github/workflows/docs.yml"

func itoa(n int) string { return strconv.Itoa(n) }

func readGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "pagesroll", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func cfgFor(t *testing.T, f *fake) Config {
	return Config{
		Workdir: t.TempDir(), Version: "v0.80.0", Actionlint: "", Wave: 2, MinCore: 1000,
		Run: f.run, Sleep: func(time.Duration) {}, Out: &bytes.Buffer{},
	}
}

func (f *fake) has(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func TestCampaignOutcomes(t *testing.T) {
	before := readGolden(t, "before.yml")
	f := &fake{
		t:    t,
		held: map[string]bool{"held/docs": true},
		yml: map[string]string{
			"good/docs": before, "red/docs": before, "odd/docs": strings.Replace(before, "name: Push gh-pages", "name: Push", 1),
			"done/docs": readGolden(t, "after.yml"),
		},
		red:  map[string]bool{"red/docs": true},
		core: 4000,
	}
	cfg := cfgFor(t, f)
	cfg.State = filepath.Join(cfg.Workdir, "state.jsonl")
	rs, err := Campaign(cfg, []string{"good/docs", "held/docs", "odd/docs", "done/docs", "red/docs"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rs {
		got[r.Repo] = r.Status
	}
	want := map[string]string{"good/docs": Merged, "held/docs": SkippedClaim, "odd/docs": RefusedShape, "done/docs": Already, "red/docs": FailedCI}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
	// The workflow that was merged is the golden after.
	b, _ := os.ReadFile(filepath.Join(cfg.Workdir, "good-docs-pagesbuild", pagesrollWorkflow))
	if string(b) != readGolden(t, "after.yml") {
		t.Error("the edited workflow is not the golden after")
	}
	if !f.has("gitpush -q -u origin pagesbuild-after-push") {
		t.Error("nothing was pushed")
	}
	merged := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, "ghmerge ") {
			merged++
			if c != "ghmerge good/docs 7" {
				t.Errorf("merged the wrong one: %s", c)
			}
		}
	}
	if merged != 1 {
		t.Errorf("ghmerge ran %d times, want 1 (red and refused must not merge)", merged)
	}
	// Every claim is released.
	for _, r := range []string{"good/docs", "held/docs", "odd/docs", "done/docs", "red/docs"} {
		if !f.has("agentsync release " + r) {
			t.Errorf("claim of %s not released", r)
		}
	}
	if s := Summary(rs); !strings.Contains(s, "merged 1, already 1, skipped-claimed 1, refused-shape 1, failed-ci 1, unobserved 0, failed 0") ||
		!strings.Contains(s, "red/docs: failed-ci") {
		t.Errorf("summary:\n%s", s)
	}
}

func TestCampaignResumesAndStopsOnBudget(t *testing.T) {
	before := readGolden(t, "before.yml")
	f := &fake{t: t, yml: map[string]string{"a/docs": before, "b/docs": before}, core: 999}
	cfg := cfgFor(t, f)
	cfg.State = filepath.Join(cfg.Workdir, "state.jsonl")
	if err := os.WriteFile(cfg.State, []byte(`{"repo":"a/docs","status":"merged","pr":3}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rs, err := Campaign(cfg, []string{"a/docs", "b/docs"})
	if err == nil || !strings.Contains(err.Error(), "budget 999") {
		t.Fatalf("expected a budget stop, got %v", err)
	}
	if len(rs) != 0 || f.has("agentsync claim") {
		t.Fatalf("work was done below the budget: %v %v", rs, f.calls)
	}
	if !strings.Contains(cfg.Out.(*bytes.Buffer).String(), "a/docs: merged earlier, not redone") {
		t.Error("a finished repository was not skipped")
	}
}

func TestPrNumberComesFromTheURL(t *testing.T) {
	if n, ok := prNumber("Creating...\nhttps://github.com/o/docs/pull/42\n"); !ok || n != 42 {
		t.Fatal(n, ok)
	}
	if _, ok := prNumber("no url"); ok {
		t.Fatal("guessed a number")
	}
}

func TestRootCmdNeedsRepos(t *testing.T) {
	c := RootCmd()
	c.SetArgs([]string{})
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	if err := c.Execute(); err == nil {
		t.Fatal("expected a usage error")
	}
}
