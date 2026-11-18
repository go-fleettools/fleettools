package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDefaultFloorIsTheFixedRelease(t *testing.T) {
	if defaultFloor != "1.27.4" {
		t.Fatalf("defaultFloor = %q", defaultFloor)
	}
	// No --floor given: a module at 1.27.3 is a finding, one at 1.27.4 is not.
	for gomod, wantFound := range map[string]bool{"module x\n\ngo 1.27.3\n": true, "module x\n\ngo 1.27.4\n": false} {
		gh, repos := one("r", fakeRepo{files: map[string]string{"go.mod": gomod, ".github/workflows/ci.yml": cleanCI}})
		code, out, _ := drive(gh, repos, "o")
		if wantFound != (code == 1) || wantFound != strings.Contains(out, "below the floor 1.27.4") {
			t.Errorf("%q: code %d, out %q", gomod, code, out)
		}
	}
}

func TestPlanFloor(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":               "module a\n\ngo 1.27.3\n",
		"sub/mod/go.mod":       "module b\n\ngo 1.27.0\n",
		"at/go.mod":            "module c\n\ngo 1.27.4\n",
		"above/go.mod":         "module d\n\ngo 1.28.1\n",
		"testdata/x/go.mod":    "module e\n\ngo 1.20\n",
		"vendor/y/go.mod":      "module f\n\ngo 1.20\n",
		".git/hooks/go.mod":    "module g\n\ngo 1.20\n",
		"sub/mod/README.md":    "not a go.mod\n",
		"deep/testdata/go.mod": "module h\n\ngo 1.20\n",
	})
	edits, refused, err := planFloor(root, "1.27.4")
	if err != nil || len(refused) != 0 {
		t.Fatalf("err %v refused %v", err, refused)
	}
	var got []string
	for _, e := range edits {
		got = append(got, e.Path+"="+e.Old)
	}
	if want := "go.mod=1.27.3 sub/mod/go.mod=1.27.0"; strings.Join(got, " ") != want {
		t.Fatalf("edits %v, want %s", got, want)
	}
}

func TestPlanFloorRefusesAMalformedGoMod(t *testing.T) {
	for name, src := range map[string]string{
		"no directive": "module a\n",
		"not a number": "module a\n\ngo banana\n",
		"garbage":      "\x00\x01 this is not a go.mod",
		"comment only": "module a\n\ngo 1.27.3 // pinned\n", // the audit reads no directive here either
	} {
		root := t.TempDir()
		writeTree(t, root, map[string]string{"go.mod": "module ok\n\ngo 1.27.3\n", "bad/go.mod": src})
		_, refused, err := planFloor(root, "1.27.4")
		if err != nil || len(refused) != 1 || !strings.HasPrefix(refused[0], "bad/go.mod") {
			t.Errorf("%s: refused %v err %v", name, refused, err)
		}
	}
}

// The edit itself, with the real `go mod edit`: root and sub-module raised, the
// ones at or above the floor byte-identical.
func TestApplyFloorEditsOnlyWhatIsBelow(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":         "module a\n\ngo 1.27.3\n\nrequire example.com/z v1.0.0\n",
		"sub/mod/go.mod": "module b\n\ngo 1.27.0\n",
		"at/go.mod":      "module c\n\ngo 1.27.4\n",
		"above/go.mod":   "module d\n\ngo 1.28.1\n",
	}
	writeTree(t, root, files)
	edits, refused, err := planFloor(root, "1.27.4")
	if err != nil || len(refused) != 0 || len(edits) != 2 {
		t.Fatalf("plan: %v %v %v", edits, refused, err)
	}
	if err := applyFloor(realRun, root, "1.27.4", edits); err != nil {
		t.Fatal(err)
	}
	if g := read(t, filepath.Join(root, "go.mod")); !strings.Contains(g, "go 1.27.4") || !strings.Contains(g, "require example.com/z v1.0.0") {
		t.Errorf("root: %q", g)
	}
	if g := read(t, filepath.Join(root, "sub/mod/go.mod")); !strings.Contains(g, "go 1.27.4") {
		t.Errorf("sub: %q", g)
	}
	for _, p := range []string{"at/go.mod", "above/go.mod"} {
		if g := read(t, filepath.Join(root, p)); g != files[p] {
			t.Errorf("%s was touched: %q", p, g)
		}
	}
	// And a second plan finds nothing left.
	if edits, _, _ := planFloor(root, "1.27.4"); len(edits) != 0 {
		t.Errorf("still below: %v", edits)
	}
}

func TestApplyFloorReportsAFailingGoModEdit(t *testing.T) {
	run := func(string, string, ...string) (string, int, error) { return "boom", 1, nil }
	if err := applyFloor(run, t.TempDir(), "1.27.4", []Edit{{"go.mod", "1.27.3"}}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err %v", err)
	}
}

func TestFixPins(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".github/workflows/ci.yml": `on: push
jobs:
  a:
    steps:
      - uses: actions/setup-go@v7
        with:
          go-version: '1.27.3' # pinned
  b:
    steps:
      - uses: actions/setup-go@v7
        with: { go-version-file: go.mod }
  c:
    steps:
      - uses: actions/setup-go@v7
        with:
          go-version: 1.27
`,
	})
	changed, blocked, err := fixPins(root, "1.27.4")
	if err != nil || len(blocked) != 0 || len(changed) != 1 {
		t.Fatalf("%v %v %v", changed, blocked, err)
	}
	got := read(t, filepath.Join(root, ".github/workflows/ci.yml"))
	if !strings.Contains(got, "go-version: '1.27.4' # pinned") || !strings.Contains(got, "go-version: 1.27\n") {
		t.Errorf("rewritten wrongly:\n%s", got)
	}

	// A pin this cannot rewrite (flow style) blocks the repository.
	root2 := t.TempDir()
	writeTree(t, root2, map[string]string{".github/workflows/ci.yml": `on: push
jobs:
  a:
    steps:
      - uses: actions/setup-go@v7
        with: { go-version: '1.27.3' }
`})
	_, blocked, err = fixPins(root2, "1.27.4")
	if err != nil || len(blocked) != 1 {
		t.Fatalf("blocked %v err %v", blocked, err)
	}
}

// ---- the run, with every seam faked ----

type fixFake struct {
	t              *testing.T
	base           string
	log            []string
	clones         map[string]map[string]string // repo -> files created by `git clone`
	claims         map[string]string            // repo -> agentsync output when refused
	buildNo        map[string]bool              // repo whose go build fails
	pulls          map[string]string            // repo -> JSON of open pulls
	files          map[string]string            // "repo#N" -> JSON of files
	ratewaitStatus int
	rates          []int
	rateErr        error
	reads          []string
	search         string
	prwait         map[string]int // "repo N" -> status
	merge          map[string]int
}

func (f *fixFake) env(repos ...string) *fixEnv {
	var rs []fleet.Repo
	for _, r := range repos {
		rs = append(rs, fleet.Repo{FullName: r, DefaultBranch: "main"})
	}
	return &fixEnv{
		base:  f.base,
		repos: func(string) ([]fleet.Repo, error) { return rs, nil },
		gh: func(args ...string) ([]byte, error) {
			u := args[len(args)-1]
			f.reads = append(f.reads, u)
			switch {
			case strings.Contains(u, "search/issues"):
				return []byte(f.search), nil
			case strings.Contains(u, "/pulls?"):
				repo := strings.TrimSuffix(strings.TrimPrefix(u, "repos/"), "/pulls?state=open&per_page=100")
				if s, ok := f.pulls[repo]; ok {
					return []byte(s), nil
				}
				return []byte("[]"), nil
			case strings.Contains(u, "/files?"):
				i := strings.Index(u, "/pulls/")
				repo := strings.TrimPrefix(u[:i], "repos/")
				n := strings.TrimSuffix(u[i+len("/pulls/"):], "/files?per_page=100")
				if s, ok := f.files[repo+"#"+n]; ok {
					return []byte(s), nil
				}
				return nil, errors.New("HTTP 404")
			}
			return nil, errors.New("unexpected read " + u)
		},
		run: f.run,
		rate: func() (int, error) {
			if f.rateErr != nil {
				return 0, f.rateErr
			}
			if len(f.rates) == 0 {
				return 4000, nil
			}
			v := f.rates[0]
			f.rates = f.rates[1:]
			return v, nil
		},
	}
}

func (f *fixFake) run(dir, name string, args ...string) (string, int, error) {
	line := name + " " + strings.Join(args, " ")
	f.log = append(f.log, line)
	repoOf := func() string {
		for _, a := range args {
			if strings.Contains(a, "/") && !strings.HasPrefix(a, "-") && strings.Count(a, "/") == 1 {
				return a
			}
		}
		return ""
	}
	switch name {
	case "ratewait":
		if f.ratewaitStatus != 0 {
			return "TIMEOUT core 10/2500", f.ratewaitStatus, nil
		}
	case "agentsync":
		if args[0] == "claim" {
			if out, ok := f.claims[args[len(args)-1]]; ok {
				return out, 1, nil
			}
		}
	case "git":
		switch args[0] {
		case "clone":
			repo := strings.TrimSuffix(strings.TrimPrefix(args[1], "https://github.com/"), ".git")
			f.t.Helper()
			writeTree(f.t, args[2], f.clones[repo])
			return "", 0, nil
		case "status":
			return "", 0, nil
		case "restore":
			return "", 0, nil
		}
	case "go":
		if args[0] == "build" {
			for repo := range f.buildNo {
				if strings.Contains(dir, strings.ReplaceAll(repo, "/", "-")) {
					return "main.go:1: undefined: x", 1, nil
				}
			}
			return "", 0, nil
		}
		return realRun(dir, name, args...)
	case "gh":
		return "https://github.com/" + repoOf() + "/pull/7\n", 0, nil
	case "prwait":
		key := args[1] + " " + args[len(args)-1]
		if st, ok := f.prwait[key]; ok {
			return "state of " + key, st, nil
		}
	case "ghmerge":
		if st, ok := f.merge[args[0]+" "+args[1]]; ok {
			return "refused by ghmerge", st, nil
		}
		return "merged " + args[1], 0, nil
	}
	return "", 0, nil
}

func (f *fixFake) logHas(sub string) int {
	for i, l := range f.log {
		if strings.Contains(l, sub) {
			return i
		}
	}
	return -1
}

func driveFix(e *fixEnv, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	c := fixCommand(e)
	code := cli.Drive(c, args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func newFixFake(t *testing.T) *fixFake {
	return &fixFake{t: t, base: t.TempDir(), clones: map[string]map[string]string{}, claims: map[string]string{},
		buildNo: map[string]bool{}, pulls: map[string]string{}, files: map[string]string{},
		prwait: map[string]int{}, merge: map[string]int{}}
}

func TestFixEveryOutcome(t *testing.T) {
	f := newFixFake(t)
	f.clones["o/a-open"] = map[string]string{"go.mod": "module a\n\ngo 1.27.3\n", "sub/go.mod": "module b\n\ngo 1.27.2\n",
		"at/go.mod": "module c\n\ngo 1.27.4\n", "above/go.mod": "module d\n\ngo 1.28.0\n"}
	f.clones["o/b-clean"] = map[string]string{"go.mod": "module a\n\ngo 1.27.4\n"}
	f.clones["o/c-held"] = map[string]string{"go.mod": "module a\n\ngo 1.27.3\n"}
	f.claims["o/c-held"] = "REFUSED  o/c-held\n         held by 7ba8e34e until 22:28:11\n"
	f.clones["o/d-build"] = map[string]string{"go.mod": "module a\n\ngo 1.27.3\n"}
	f.buildNo["o/d-build"] = true
	f.clones["o/e-conflict"] = map[string]string{"go.mod": "module a\n\ngo 1.27.3\n"}
	f.pulls["o/e-conflict"] = `[{"number":5,"html_url":"u","head":{"ref":"renovate/x"}}]`
	f.files["o/e-conflict#5"] = `[{"filename":"README.md"},{"filename":"tools/go.mod"}]`
	f.clones["o/f-already"] = map[string]string{"go.mod": "module a\n\ngo 1.27.3\n"}
	f.pulls["o/f-already"] = `[{"number":9,"html_url":"https://github.com/o/f-already/pull/9","head":{"ref":"gofloor-1.27.4"}}]`
	f.clones["o/g-refused"] = map[string]string{"go.mod": "module a\n\ngo 1.27.3\n", "bad/go.mod": "garbage"}
	f.clones["o/h-pin"] = map[string]string{"go.mod": "module a\n\ngo 1.27.3\n", ".github/workflows/ci.yml": `jobs:
  a:
    steps:
      - uses: actions/setup-go@v7
        with:
          go-version: "1.27.3"
`}
	f.clones["o/i-unrewritable"] = map[string]string{"go.mod": "module a\n\ngo 1.27.3\n", ".github/workflows/ci.yml": `jobs:
  a:
    steps:
      - uses: actions/setup-go@v7
        with: { go-version: '1.27.3' }
`}
	// An open pull request that does not touch a go.mod is no conflict.
	f.pulls["o/h-pin"] = `[{"number":3,"html_url":"u","head":{"ref":"docs"}}]`
	f.files["o/h-pin#3"] = `[{"filename":"README.md"}]`

	e := f.env("o/a-open", "o/b-clean", "o/c-held", "o/d-build", "o/e-conflict", "o/f-already", "o/g-refused", "o/h-pin", "o/i-unrewritable")
	f.rates = []int{4000, 3990, 3990, 3990, 3990, 3990, 3990, 3990, 3990, 3990, 3990, 3990, 3990, 3990, 3990, 3990, 3990, 3980}
	code, out, errOut := driveFix(e, "o")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s%s", code, out, errOut)
	}
	rows := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if fs := strings.Fields(l); len(fs) >= 2 && strings.HasPrefix(fs[1], "o/") {
			rows[fs[1]] = l
		}
	}
	for repo, want := range map[string]string{
		"o/a-open":         "OPENED",
		"o/b-clean":        "CLEAN",
		"o/c-held":         "HELD",
		"o/d-build":        "BUILD-FAILED",
		"o/e-conflict":     "CONFLICT",
		"o/f-already":      "ALREADY",
		"o/g-refused":      "REFUSED",
		"o/h-pin":          "OPENED",
		"o/i-unrewritable": "CI-PIN",
	} {
		if !strings.HasPrefix(rows[repo], want) {
			t.Errorf("%s: row %q, want %s\n%s", repo, rows[repo], want, out)
		}
	}
	if !strings.Contains(rows["o/c-held"], "held by 7ba8e34e until 22:28:11") {
		t.Errorf("held row: %q", rows["o/c-held"])
	}
	if !strings.Contains(rows["o/a-open"], "https://github.com/o/a-open/pull/7") {
		t.Errorf("opened row has no URL: %q", rows["o/a-open"])
	}
	if !strings.Contains(out, "tools/go.mod") {
		t.Errorf("the conflicting file is not named:\n%s", out)
	}
	if strings.Count(out, "\n              cost: ") != 9 || !strings.Contains(out, "TOTAL cost:") {
		t.Errorf("a cost line is missing:\n%s", out)
	}

	// The clone of the opened repository holds the edit, in root and sub-module.
	dir := filepath.Join(f.base, "o-a-open-gofloor")
	for p, want := range map[string]string{"go.mod": "go 1.27.4", "sub/go.mod": "go 1.27.4", "at/go.mod": "go 1.27.4", "above/go.mod": "go 1.28.0"} {
		if !strings.Contains(read(t, filepath.Join(dir, p)), want) {
			t.Errorf("%s lacks %q", p, want)
		}
	}
	if g := read(t, filepath.Join(f.base, "o-h-pin-gofloor", ".github/workflows/ci.yml")); !strings.Contains(g, `go-version: "1.27.4"`) {
		t.Errorf("pin not rewritten: %s", g)
	}

	// Order for the opened repository: ratewait, claim, clone, branch, build,
	// then the gates, commit, push, and only then the pull request.
	order := []string{"ratewait --rest 2500", "agentsync claim", "git clone https://github.com/o/a-open.git", "git fetch",
		"git checkout -B gofloor-1.27.4 origin/main", "go mod edit -go=1.27.4", "go build ./...", "paritycheck text", "prbody --repo o/a-open",
		"git commit -F", "gitpush -u origin gofloor-1.27.4", "gh pr create"}
	prev := -1
	for _, s := range order {
		i := f.logHas(s)
		if i < 0 || i <= prev && s != "ratewait --rest 2500" {
			t.Errorf("%q at %d after %d\n%s", s, i, prev, strings.Join(f.log, "\n"))
		}
		prev = i
	}
	// A refusal, a clean repository and a held one never reach git commit or push.
	for _, repo := range []string{"c-held", "b-clean", "e-conflict", "d-build", "g-refused", "f-already", "i-unrewritable"} {
		for _, l := range f.log {
			if strings.Contains(l, "gitpush") && strings.Contains(l, repo) {
				t.Errorf("pushed %s", repo)
			}
		}
	}
	// Every repository was preceded by its own ratewait, and the claim released.
	if n := countPrefix(f.log, "ratewait"); n != 9 {
		t.Errorf("%d ratewait runs, want 9", n)
	}
	if n := countPrefix(f.log, "gitpush"); n != 2 {
		t.Errorf("%d pushes, want 2", n)
	}
	if f.logHas("agentsync release o/a-open") < 0 {
		t.Error("claim not released")
	}
	// The cost line reads the budget before and after.
	if !strings.Contains(rows["o/a-open"]+out, "4000 -> 3990") {
		t.Errorf("no budget readings in:\n%s", out)
	}
}

func countPrefix(log []string, p string) int {
	n := 0
	for _, l := range log {
		if strings.HasPrefix(l, p) {
			n++
		}
	}
	return n
}

func TestFixRespectsMax(t *testing.T) {
	f := newFixFake(t)
	for _, r := range []string{"o/a", "o/b", "o/c"} {
		f.clones[r] = map[string]string{"go.mod": "module a\n\ngo 1.27.4\n"}
	}
	code, out, _ := driveFix(f.env("o/a", "o/b", "o/c"), "--max", "1", "o")
	if code != 1 || strings.Count(out, "\nNOT-VISITED") != 2 || !strings.Contains(out, "CLEAN") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if n := countPrefix(f.log, "ratewait"); n != 1 {
		t.Errorf("%d ratewait runs", n)
	}
}

func TestFixStopsWhenTheBudgetDoesNotRefill(t *testing.T) {
	f := newFixFake(t)
	f.ratewaitStatus = 1
	f.clones["o/a"] = map[string]string{"go.mod": "module a\n\ngo 1.27.3\n"}
	code, out, _ := driveFix(f.env("o/a", "o/b"), "o")
	if code != 1 || strings.Count(out, "NOT-VISITED  ") != 2 || !strings.Contains(out, "TIMEOUT") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if f.logHas("git ") >= 0 {
		t.Error("touched git with the budget short")
	}
}

func TestFixUnreadableMeansUnreadable(t *testing.T) {
	// A cost that cannot be read is said so, and is not counted as zero.
	f := newFixFake(t)
	f.rateErr = errors.New("no network")
	f.clones["o/a"] = map[string]string{"go.mod": "module a\n\ngo 1.27.4\n"}
	code, out, _ := driveFix(f.env("o/a"), "o")
	if code != 0 || !strings.Contains(out, "rate_limit UNREADABLE") || !strings.Contains(out, "1 with an UNREADABLE cost") {
		t.Fatalf("exit %d\n%s", code, out)
	}

	// A claim that cannot be asked, a clone that fails and a dirty clone are exit 2.
	g := newFixFake(t)
	e := g.env("o/a")
	e.run = func(dir, name string, args ...string) (string, int, error) {
		if name == "agentsync" {
			return "", -1, errors.New("not installed")
		}
		return "", 0, nil
	}
	if code, out, _ := driveFix(e, "o"); code != 2 || !strings.Contains(out, "UNREADABLE") {
		t.Errorf("claim: exit %d\n%s", code, out)
	}
	h := newFixFake(t)
	writeTree(t, h.base, map[string]string{"o-a-gofloor/dirty": "x"})
	e = h.env("o/a")
	e.run = func(dir, name string, args ...string) (string, int, error) {
		if name == "git" && args[0] == "status" {
			return "?? dirty\n", 0, nil
		}
		return "", 0, nil
	}
	if code, out, _ := driveFix(e, "o"); code != 2 || !strings.Contains(out, "uncommitted") {
		t.Errorf("dirty: exit %d\n%s", code, out)
	}
	// An open-pull-request listing that fails is unreadable, not "no conflict".
	k := newFixFake(t)
	k.clones["o/a"] = map[string]string{"go.mod": "module a\n\ngo 1.27.3\n"}
	e = k.env("o/a")
	e.gh = func(...string) ([]byte, error) { return nil, errors.New("HTTP 502") }
	if code, out, _ := driveFix(e, "o"); code != 2 || !strings.Contains(out, "HTTP 502") || k.logHas("gitpush") >= 0 {
		t.Errorf("pulls: exit %d\n%s", code, out)
	}
	// An organisation that cannot be listed is exit 2.
	e = newFixFake(t).env()
	e.repos = func(string) ([]fleet.Repo, error) { return nil, errors.New("HTTP 401") }
	if code, _, _ := driveFix(e, "o"); code != 2 {
		t.Errorf("listing: exit %d", code)
	}
}

func TestFixUsage(t *testing.T) {
	e := newFixFake(t).env()
	for _, args := range [][]string{{}, {"a", "b"}, {"--floor", "banana", "o"}} {
		if code, _, _ := driveFix(e, args...); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

func TestFixBodyNamesTheAdvisory(t *testing.T) {
	o := fixOpts{floor: "1.27.4", advisory: "GO-2026-6859", attribution: defaultAttribution, footer: defaultPRFooter}
	edits := []Edit{{"go.mod", "1.27.3"}, {"sub/go.mod", "1.27.2"}}
	body, msg := prBody(o, edits, nil), commitMessage(o, "t", edits, nil)
	for _, s := range []string{"GO-2026-6859", "`sub/go.mod`: go 1.27.2 -> 1.27.4", "Claude Code"} {
		if !strings.Contains(body, s) {
			t.Errorf("body lacks %q:\n%s", s, body)
		}
	}
	if !strings.Contains(msg, "GO-2026-6859") || !strings.HasSuffix(msg, defaultAttribution+"\n") {
		t.Errorf("commit message:\n%s", msg)
	}
}

func driveMerge(e *fixEnv, args ...string) (int, string) {
	var out, errOut bytes.Buffer
	code := cli.Drive(mergeCommand(e), args, &out, &errOut)
	return code, out.String() + errOut.String()
}

func TestMergeMergesOnlyTheGreen(t *testing.T) {
	f := newFixFake(t)
	f.search = fmt.Sprintf(`{"total_count":3,"incomplete_results":false,"items":[
	 {"number":7,"repository_url":"https://api.github.com/repos/o/green","html_url":"h1"},
	 {"number":8,"repository_url":"https://api.github.com/repos/o/red","html_url":"h2"},
	 {"number":9,"repository_url":"https://api.github.com/repos/o/refused","html_url":"h3"}]}`)
	f.prwait["o/red 8"] = 1
	f.merge["o/refused 9"] = 1
	code, out := driveMerge(f.env(), "o")
	if code != 1 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"MERGED", "NOT-GREEN", "state of o/red 8", "MERGE-REFUSED"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	if f.logHas("ghmerge o/red") >= 0 {
		t.Error("merged a red pull request")
	}
	if f.logHas("ghmerge o/green 7") < 0 {
		t.Error("did not merge the green one")
	}
	if !strings.Contains(strings.Join(f.reads, " "), "head%3Agofloor-1.27.4") {
		t.Errorf("search did not name the branch: %v", f.reads)
	}
}

func TestMergeRefusesAnIncompleteSearch(t *testing.T) {
	f := newFixFake(t)
	f.search = `{"total_count":5,"incomplete_results":false,"items":[]}`
	if code, out := driveMerge(f.env(), "o"); code != 2 || f.logHas("ghmerge") >= 0 {
		t.Errorf("exit %d\n%s", code, out)
	}
	f.search = `<html>`
	if code, _ := driveMerge(f.env(), "o"); code != 2 {
		t.Errorf("not JSON: exit %d", code)
	}
	f.search = `{"total_count":0,"incomplete_results":false,"items":[]}`
	if code, out := driveMerge(f.env(), "o"); code != 0 || !strings.Contains(out, "no open pull request") {
		t.Errorf("none open: exit %d\n%s", code, out)
	}
}
