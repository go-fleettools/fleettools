package main

import (
	"bytes"
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
		repo := strings.TrimPrefix(args[len(args)-2], "https://github.com/")
		d := args[len(args)-1]
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
	if s := Summary(rs); !strings.Contains(s, "merged 1, already 1, skipped-claimed 1, refused-shape 1, failed-ci 1, failed 0") ||
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
