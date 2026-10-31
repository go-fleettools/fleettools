package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
)

// fakeRepo answers the two reads gofloor makes: the tree, and a file.
type fakeRepo struct {
	files     map[string]string
	treeErr   error
	truncated bool
	badPath   string // a file whose read fails
}

func (f fakeRepo) gh(args ...string) ([]byte, error) {
	url := args[len(args)-1]
	if strings.Contains(url, "/git/trees/") {
		if f.treeErr != nil {
			return nil, f.treeErr
		}
		var entries []treeEntry
		for p := range f.files {
			entries = append(entries, treeEntry{Path: p, Type: "blob"})
		}
		entries = append(entries, treeEntry{Path: ".github", Type: "tree"})
		return json.Marshal(map[string]any{"tree": entries, "truncated": f.truncated})
	}
	i := strings.Index(url, "/contents/")
	p := url[i+len("/contents/") : strings.Index(url, "?ref=")]
	if p == f.badPath {
		return nil, errors.New("HTTP 502")
	}
	c, ok := f.files[p]
	if !ok {
		return nil, errors.New("HTTP 404")
	}
	return []byte(c), nil
}

func one(name string, f fakeRepo) (ghFunc, reposFunc) {
	return f.gh, func(string) ([]fleet.Repo, error) {
		return []fleet.Repo{{FullName: "o/" + name, DefaultBranch: "main"}}, nil
	}
}

func drive(gh ghFunc, repos reposFunc, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := cli.Drive(command(gh, repos), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

const cleanCI = `on: push
jobs:
  test:
    steps:
      - uses: actions/setup-go@v7
        with: { go-version-file: go.mod }
  govulncheck:
    steps:
      - uses: actions/setup-go@v7
        with: { go-version-file: go.mod }
      - run: go run golang.org/x/vuln/cmd/govulncheck@latest ./...
`

// ⛔ The planted positive: a floor of 1.27.1 (twice: a nested module too), a
// hard-coded 1.27.2, and a govulncheck step with continue-on-error. All three
// must be flagged, or the tool proves nothing.
func TestThePlantedRepositoryIsFlaggedThreeWays(t *testing.T) {
	gh, repos := one("bad", fakeRepo{files: map[string]string{
		"go.mod":                          "module x\n\ngo 1.27.1\n",
		"interop/crewjam/go.mod":          "module y\n\ngo 1.27.1\n",
		"interop/crewjam/testdata/go.mod": "module z\n\ngo 1.20\n", // not ours to judge
		".github/workflows/ci.yml": `on: push
jobs:
  test:
    steps:
      - uses: actions/setup-go@v7
        with: { go-version: '1.27.2', check-latest: true }
  vuln:
    steps:
      - uses: actions/setup-go@v7
        with: { go-version-file: go.mod }
      - run: govulncheck ./...
        continue-on-error: true
`}})
	code, out, _ := drive(gh, repos, "o")
	for _, want := range []string{
		"go.mod: go 1.27.1 is below the floor 1.27.3",
		"interop/crewjam/go.mod: go 1.27.1 is below the floor",
		"setup-go hard-coded 1.27.2",
		"govulncheck cannot fail the build (continue-on-error)",
		"present, gates nothing",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("not flagged: %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "testdata") {
		t.Errorf("a testdata module was judged:\n%s", out)
	}
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
}

func TestACleanRepositoryIsClean(t *testing.T) {
	gh, repos := one("good", fakeRepo{files: map[string]string{
		"go.mod": "module x\n\ngo 1.27.3\n", ".github/workflows/ci.yml": cleanCI}})
	code, out, _ := drive(gh, repos, "o")
	if code != 0 || strings.Contains(out, "FOUND") || !strings.Contains(out, "govulncheck: gates") ||
		!strings.Contains(out, "go-version-file") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestWhatDoesNotGate(t *testing.T) {
	for name, tc := range map[string]struct{ step, why string }{
		"json":       {"- run: govulncheck -format json ./...", "-format json exits 0"},
		"sarif":      {"- run: govulncheck -format=sarif ./...", "-format=sarif exits 0"},
		"openvex":    {"- run: govulncheck -format openvex ./...", "openvex"},
		"or true":    {"- run: govulncheck ./... || true", "|| true"},
		"binary":     {"- run: govulncheck -mode binary ./app", "does not scan the source"},
		"extract":    {"- run: govulncheck -mode=extract ./app", "does not scan the source"},
		"action":     {"- uses: golang/govulncheck-action@v1\n        with: { output-format: sarif }", "output-format sarif"},
		"job level":  {"- run: govulncheck ./...\n    continue-on-error: true", "continue-on-error"},
		"expression": {"- run: govulncheck ./...\n        continue-on-error: ${{ github.event_name == 'push' }}", "continue-on-error"},
	} {
		t.Run(name, func(t *testing.T) {
			src := "jobs:\n  v:\n    steps:\n      " + tc.step + "\n"
			if name == "job level" {
				src = "jobs:\n  v:\n    continue-on-error: true\n    steps:\n      - run: govulncheck ./...\n"
			}
			_, vulns, err := Audit("ci.yml", []byte(src))
			if err != nil || len(vulns) != 1 || vulns[0].Gates || !strings.Contains(vulns[0].Why, tc.why) {
				t.Fatalf("%v %+v", err, vulns)
			}
		})
	}
	for name, step := range map[string]string{
		"plain":    "- run: go run golang.org/x/vuln/cmd/govulncheck@latest ./...",
		"vulngate": "- run: govulncheck -format json ./... | vulngate",
		"action":   "- uses: golang/govulncheck-action@v1",
		"text":     "- uses: golang/govulncheck-action@v1\n        with: { output-format: text }",
	} {
		_, vulns, _ := Audit("ci.yml", []byte("jobs:\n  v:\n    steps:\n      "+step+"\n"))
		if len(vulns) != 1 || !vulns[0].Gates {
			t.Errorf("%s: %+v", name, vulns)
		}
	}
	if _, vulns, _ := Audit("ci.yml", []byte("jobs:\n  v:\n    steps:\n      - run: go test ./...\n")); len(vulns) != 0 {
		t.Errorf("a test step is not govulncheck: %+v", vulns)
	}
}

func TestToolchainSources(t *testing.T) {
	src := `jobs:
  a:
    steps:
      - uses: actions/setup-go@v7
        with: { go-version: stable }
      - uses: actions/setup-go@v7
        with: { go-version: oldstable }
      - uses: actions/setup-go@v7
        with: { go-version: "${{ matrix.go }}" }
      - uses: actions/setup-go@v7
      - uses: actions/setup-go@v7
        with: { go-version-file: go.mod }
`
	setups, _, err := Audit("ci.yml", []byte(src))
	if err != nil || len(setups) != 5 {
		t.Fatalf("%v %+v", err, setups)
	}
	want := []struct {
		src string
		bad bool
	}{{"stable", true}, {"oldstable", true}, {"expression", false}, {"default", true}, {"go-version-file", false}}
	for i, w := range want {
		if setups[i].Source != w.src || setups[i].Bad != w.bad {
			t.Errorf("%d: %+v, want %+v", i, setups[i], w)
		}
	}
}

// ⛔ UNREADABLE is not clean.
func TestWhatCannotBeReadIsNeverClean(t *testing.T) {
	good := map[string]string{"go.mod": "go 1.27.3\n", ".github/workflows/ci.yml": cleanCI}
	for name, f := range map[string]fakeRepo{
		"tree error":     {treeErr: errors.New("HTTP 403 rate limit")},
		"truncated tree": {files: good, truncated: true},
		"workflow read":  {files: good, badPath: ".github/workflows/ci.yml"},
		"go.mod read":    {files: good, badPath: "go.mod"},
		"not yaml":       {files: map[string]string{"go.mod": "go 1.27.3\n", ".github/workflows/ci.yml": "jobs: [\n"}},
	} {
		t.Run(name, func(t *testing.T) {
			gh, repos := one("r", f)
			code, out, _ := drive(gh, repos, "o")
			if code != 2 || !strings.Contains(out, "UNREADABLE") || strings.Contains(out, "0 unreadable") {
				t.Fatalf("exit %d\n%s", code, out)
			}
		})
	}
}

func TestSweepEdges(t *testing.T) {
	// The organisation cannot be listed.
	gh := fakeRepo{}.gh
	code, _, errOut := drive(gh, func(string) ([]fleet.Repo, error) { return nil, errors.New("HTTP 404") }, "o")
	if code != 2 || !strings.Contains(errOut, "listing o") {
		t.Errorf("%d %q", code, errOut)
	}
	// Nothing Go at all is not a clean bill.
	code, _, errOut = drive(fakeRepo{files: map[string]string{"README.md": "x"}}.gh, func(string) ([]fleet.Repo, error) {
		return []fleet.Repo{{FullName: "o/docs", DefaultBranch: "main"}}, nil
	}, "o")
	if code != 2 || !strings.Contains(errOut, "no Go module") {
		t.Errorf("%d %q", code, errOut)
	}
	// A module with no workflow, and an unreadable repository beside a bad one: 2 outranks 1.
	g1, r1 := one("nociI", fakeRepo{files: map[string]string{"go.mod": "go 1.27.3\n"}})
	code, out, _ := drive(g1, r1, "o")
	if code != 1 || !strings.Contains(out, "no workflow") {
		t.Errorf("%d\n%s", code, out)
	}
	// An empty default branch name reads HEAD.
	rep := Check(fakeRepo{files: map[string]string{"go.mod": "go 1.27.3\n"}}.gh, fleet.Repo{FullName: "o/x"}, "1.27.3")
	if rep == nil {
		t.Error("no report for HEAD")
	}
	// A tree that is not JSON.
	bad := Check(func(...string) ([]byte, error) { return []byte("<html>"), nil }, fleet.Repo{FullName: "o/x"}, "1.27.3")
	if len(bad.Unreadable) == 0 {
		t.Error("an HTML reply was believed")
	}
}

func TestUsage(t *testing.T) {
	gh, repos := one("r", fakeRepo{})
	if code, _, errOut := drive(gh, repos); code != 2 || !strings.Contains(errOut, "usage") {
		t.Errorf("no org: %d %q", code, errOut)
	}
	if code, _, errOut := drive(gh, repos, "--floor", "banana", "o"); code != 2 || !strings.Contains(errOut, "banana") {
		t.Errorf("bad floor: %d %q", code, errOut)
	}
	if RootCmd() == nil {
		t.Fatal("no command")
	}
}

func TestVersions(t *testing.T) {
	for s, want := range map[string]bool{"1.27.3": true, "1.27": true, "1.27rc1": true, "": false, "1": false, "1.x": false, "1.2.3.4": false, "latest": false} {
		if _, ok := parseVersion(s); ok != want {
			t.Errorf("%q: %v", s, ok)
		}
	}
	a, _ := parseVersion("1.27.1")
	b, _ := parseVersion("1.27.3")
	if !less(a, b) || less(b, a) || less(a, a) {
		t.Error("ordering")
	}
	if goDirective("module x\ngo 1.27.3\ntoolchain go1.27.4\n") != "1.27.3" || goDirective("module x") != "" {
		t.Error("directive")
	}
	// A module without a directive, and one with a nonsense directive.
	rep := Check(fakeRepo{files: map[string]string{"go.mod": "module x\n", "a/go.mod": "go banana\n"}}.gh, fleet.Repo{FullName: "o/x", DefaultBranch: "main"}, "1.27.3")
	got := strings.Join(rep.Findings, "|")
	if !strings.Contains(got, "has no go directive") || !strings.Contains(got, "go banana is not a version") {
		t.Errorf("%s", got)
	}
}
