package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

// The "before" and "after" are the real job from go-wasmcontainers/docs#62.
const shellJob = `jobs:
  links:
    name: every external link resolves
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - name: Check
        run: |
          set -uo pipefail
          urls=$(grep -rhoE 'https?://[^)">< [:space:]]+' content/ README.md | sed -E 's/[.,` + "`" + `]+$//' | sort -u)
          fail=0
          while read -r u; do
            case "$u" in
              *img.shields.io*) continue ;;
            esac
            code=000
            for _ in 1 2 3; do
              code=$(curl -sS -o /dev/null -w '%{http_code}' -L --max-time 25 "$u" 2>/dev/null) || true
              case "$code" in 2*|3*) break ;; esac
              sleep 3
            done
          done <<< "$urls"
`

const linkcheckJob = `jobs:
  links:
    name: every external link resolves
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      # was: curl -w '%{http_code}' in a while read loop over grep -o URLs
      - name: Check
        run: |
          umask 077
          token="${RUNNER_TEMP}/linkcheck-token"
          printf '%s' '${{ github.token }}' > "${token}"
          go run github.com/go-fleettools/fleettools/cmd/linkcheck@v0.74.0 \
            --token-file "${token}" \
            --skip go-wasmcontainers.github.io/docs \
            content/ README.md
`

const lycheeOffline = `jobs:
  l:
    steps:
      - uses: lycheeverse/lychee-action@v2
        with:
          args: --offline --no-progress .
`

const lycheeOnline = `jobs:
  l:
    steps:
      - uses: lycheeverse/lychee-action@v2
        with:
          args: --no-progress .
`

// A deploy smoke test: curl, a status test and a loop, but no URL extraction.
const smokeTest = `jobs:
  s:
    steps:
      - run: |
          for i in 1 2 3 4 5; do
            code=$(curl -s -o /dev/null -w '%{http_code}' https://example.github.io/)
            [ "$code" = 200 ] && break
            sleep 5
          done
`

// An install: curl in a pipeline with a grep, but no status test.
const installOnly = `jobs:
  s:
    steps:
      - run: |
          for f in a b; do curl -fsSL https://x/$f | grep -o 'http.*'; done
`

func TestClassifyEachClassFromRealSnippets(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		class Class
		ver   string
		off   bool
	}{
		{"shell before #62", map[string]string{"docs.yml": shellJob}, Shell, "", false},
		{"linkcheck after #62", map[string]string{"docs.yml": linkcheckJob}, Linkcheck, "v0.74.0", false},
		{"lychee offline", map[string]string{"ci.yml": lycheeOffline}, Lychee, "", true},
		{"lychee online", map[string]string{"ci.yml": lycheeOnline}, Lychee, "", false},
		{"smoke test is not a link checker", map[string]string{"ci.yml": smokeTest}, None, "", false},
		{"install is not a link checker", map[string]string{"ci.yml": installOnly}, None, "", false},
		{"no workflows", map[string]string{}, None, "", false},
	}
	for _, c := range cases {
		v := Classify(c.files)
		if v.Class != c.class || v.Version != c.ver || v.Offline != c.off {
			t.Errorf("%s: got %+v", c.name, v)
		}
	}
}

// The go-fleettools landing's own checker: a Go tool, not the shell loop.
const landingkitJob = `jobs:
  links:
    name: every link and image resolves
    steps:
      - name: Check
        env:
          GOWORK: "off"
        run: go run github.com/go-fleettools/landingkit/cmd/landingkit links public
`

func TestOtherCheckersAreNotNone(t *testing.T) {
	v := Classify(map[string]string{"links.yml": landingkitJob})
	if v.Class != Other || v.Tool != "landingkit-links" {
		t.Fatalf("%+v", v)
	}
	v = Classify(map[string]string{"a.yml": "- uses: gaurav-nelson/github-action-markdown-link-check@v1\n  with: markdown-link-check\n"})
	if v.Class != Other || v.Tool != "markdown-link-check" {
		t.Fatalf("%+v", v)
	}
	v = Classify(map[string]string{"a.yml": linkcheckJob, "b.yml": landingkitJob})
	if v.Class != Linkcheck || len(v.Also) != 1 || v.Also[0] != Other || v.Tool != "landingkit-links" {
		t.Fatalf("%+v", v)
	}
}

func TestACommentThatQuotesTheOldLoopIsNotTheLoop(t *testing.T) {
	// linkcheckJob's comment names curl, http_code, while and grep -o http.
	if v := Classify(map[string]string{"docs.yml": linkcheckJob}); v.Class != Linkcheck {
		t.Fatalf("%+v", v)
	}
}

func TestShellWinsAndTheOthersAreListedAlso(t *testing.T) {
	v := Classify(map[string]string{
		"a.yml": linkcheckJob, "b.yml": shellJob, "c.yml": lycheeOffline,
	})
	if v.Class != Shell || len(v.Files) != 1 || v.Files[0] != "b.yml" {
		t.Fatalf("%+v", v)
	}
	if len(v.Also) != 2 || v.Also[0] != Linkcheck || v.Also[1] != Lychee || v.Version != "v0.74.0" || !v.Offline {
		t.Fatalf("%+v", v)
	}
	// LINKCHECK over LYCHEE
	v = Classify(map[string]string{"a.yml": linkcheckJob, "c.yml": lycheeOnline})
	if v.Class != Linkcheck || len(v.Also) != 1 || v.Also[0] != Lychee || v.Offline {
		t.Fatalf("%+v", v)
	}
}

func TestAnUnpinnedLinkcheckSaysSo(t *testing.T) {
	y := "run: go run github.com/go-fleettools/fleettools/cmd/linkcheck content/\n"
	if v := Classify(map[string]string{"x.yml": y}); v.Class != Linkcheck || v.Version != "unpinned" {
		t.Fatalf("%+v", v)
	}
}

func TestKind(t *testing.T) {
	for in, want := range map[string]string{
		"go-fleettools/go-fleettools.github.io": "landing",
		"go-wasmcontainers/docs":                "docs",
		"go-widgets/toolkit-docs":               "docs",
		"go-widgets/toolkit":                    "other",
	} {
		if got := kind(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
}

const gqlBody = `{"data":{
 "r0":{"object":{"entries":[
   {"name":"docs.yml","object":{"text":"x: 1","isTruncated":false}},
   {"name":"sub.yml","object":{}},
   {"name":"README.md","object":{"text":"no","isTruncated":false}}]}},
 "r1":{"object":null}}}`

func TestParseGraphQL(t *testing.T) {
	got, err := parseGraphQL([]byte(gqlBody), []string{"o/a", "o/b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got["o/a"]) != 1 || got["o/a"]["docs.yml"] != "x: 1" {
		t.Fatalf("%v", got)
	}
	if w, ok := got["o/b"]; !ok || len(w) != 0 {
		t.Fatalf("a repository without workflows is READ and empty: %v", got)
	}
}

func TestParseGraphQLRefusals(t *testing.T) {
	for name, body := range map[string]string{
		"garbage":   "<html>",
		"errors":    `{"data":{},"errors":[{"message":"boom"}]}`,
		"null repo": `{"data":{"r0":null}}`,
		"truncated": `{"data":{"r0":{"object":{"entries":[{"name":"a.yml","object":{"text":"x","isTruncated":true}}]}}}}`,
	} {
		if _, err := parseGraphQL([]byte(body), []string{"o/a"}); err == nil {
			t.Errorf("%s: an unread answer must be an error, not an empty repository", name)
		}
	}
}

func TestGhGraphQLRefusesAnOddNameBeforeAnyCall(t *testing.T) {
	_, err := ghGraphQL([]string{`o/a"){x}#`})
	if err == nil || !strings.Contains(err.Error(), "odd name") {
		t.Fatal(err)
	}
}

func TestGhGraphQLRunsGHAndParses(t *testing.T) {
	old := ghCall
	defer func() { ghCall = old }()
	ghCall = func(args ...string) ([]byte, error) {
		q := args[len(args)-1]
		if !strings.Contains(q, `r0:repository(owner:"o",name:"a")`) || !strings.Contains(q, "HEAD:.github/workflows") {
			t.Errorf("query: %s", q)
		}
		return []byte(`{"data":{"r0":{"object":null}}}`), nil
	}
	got, err := ghGraphQL([]string{"o/a"})
	if err != nil || len(got["o/a"]) != 0 {
		t.Fatal(got, err)
	}
	ghCall = func(...string) ([]byte, error) { return nil, errors.New("HTTP 401") }
	if _, err := ghGraphQL([]string{"o/a"}); err == nil {
		t.Fatal("a failed call is an error")
	}
}

func writeInv(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "inventory.json")
	src := `{"taken":"2026-11-07T11:27:59+01:00","orgs":2,"repos":[
	 {"org":"go-a","name":"go-a.github.io"},
	 {"org":"go-a","name":"docs"},
	 {"org":"go-a","name":"lib"},
	 {"org":"go-b","name":"docs"},
	 {"org":"go-b","name":"gone"},
	 {"org":"go-b","name":"ly"},
	 {"org":"go-b","name":"misc"},
	 {"org":"go-b","name":"ot"},
	 {"org":"go-b","name":"old","archived":true}]}`
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func fakeFleet(batch []string) (map[string]workflows, error) {
	out := map[string]workflows{}
	for _, r := range batch {
		switch r {
		case "go-a/go-a.github.io":
			out[r] = workflows{"links.yml": shellJob}
		case "go-a/docs":
			out[r] = workflows{"docs.yml": linkcheckJob}
		case "go-a/lib":
			out[r] = workflows{}
		case "go-b/docs":
			out[r] = workflows{"ci.yml": lycheeOffline}
		case "go-b/gone":
			return nil, errors.New("go-b/gone: not visible to this account")
		case "go-b/ly":
			out[r] = workflows{"ci.yml": lycheeOnline}
		case "go-b/ot":
			out[r] = workflows{"links.yml": landingkitJob}
		case "go-b/misc":
			out[r] = workflows{"a.yml": linkcheckJob, "b.yml": lycheeOnline}
		case "go-b/old":
			panic("an archived repository must not be fetched without --archived")
		}
	}
	for _, r := range batch {
		if r == "go-b/gone" {
			return nil, errors.New("go-b/gone: not visible to this account")
		}
	}
	return out, nil
}

func setSeams(t *testing.T, f func([]string) (map[string]workflows, error), b func(int, int) error) {
	oldF, oldB := fetchBatch, startBudget
	fetchBatch, startBudget = f, b
	t.Cleanup(func() { fetchBatch, startBudget = oldF, oldB })
}

func okBudget(int, int) error { return nil }

func TestRunBisectsAFailedBatchAndNeverCountsUnreadAsNone(t *testing.T) {
	setSeams(t, fakeFleet, okBudget)
	var out, errb bytes.Buffer
	o := options{inventory: writeInv(t), jobs: 2, batch: 10}
	code := run(o, &out, &errb)
	s := out.String()
	if code != 2 {
		t.Fatalf("an unread repository makes the status 2, got %d\n%s", code, s)
	}
	for _, w := range []string{
		"SHELL     landing  go-a/go-a.github.io",
		"LINKCHECK docs     go-a/docs",
		"linkcheck@v0.74.0",
		"LYCHEE    docs     go-b/docs",
		" offline",
		"8 repositories listed (inventory of 2026-11-07 11:27), 7 read, 1 UNREAD",
		"classes: SHELL 1, LINKCHECK 2, LYCHEE 2, OTHER 1, NONE 1",
		"links.yml landingkit-links",
		"linkcheck versions: v0.74.0 x2",
		" online",
		"(also LYCHEE)",
		"SHELL go-a: 1 (go-a/go-a.github.io)",
	} {
		if !strings.Contains(s, w) {
			t.Errorf("missing %q in\n%s", w, s)
		}
	}
	if strings.Contains(s, "go-a/lib") {
		t.Error("NONE repositories are listed only with --all")
	}
	if !strings.Contains(errb.String(), "unread: go-b/gone:") {
		t.Errorf("stderr: %s", errb.String())
	}
}

func TestRunStatusesAndFlags(t *testing.T) {
	setSeams(t, fakeFleet, okBudget)
	var out, errb bytes.Buffer
	// only go-a, minus the landing: no unread, one shell -> 1
	o := options{inventory: writeInv(t), jobs: 1, batch: 1, owners: []string{"go-a"}, showNone: true}
	if code := run(o, &out, &errb); code != 1 {
		t.Fatalf("code %d\n%s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "NONE      other    go-a/lib") {
		t.Errorf("--all lists NONE:\n%s", out.String())
	}
	// no SHELL -> 0
	setSeams(t, func(b []string) (map[string]workflows, error) {
		m := map[string]workflows{}
		for _, r := range b {
			m[r] = workflows{}
		}
		return m, nil
	}, okBudget)
	out.Reset()
	if code := run(options{inventory: writeInv(t), jobs: 1, batch: 5, owners: []string{"go-b"}, archived: true}, &out, &errb); code != 0 {
		t.Fatalf("code %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "6 repositories listed") {
		t.Errorf("--archived:\n%s", out.String())
	}
}

func TestRunRefusals(t *testing.T) {
	setSeams(t, fakeFleet, okBudget)
	var out, errb bytes.Buffer
	if code := run(options{inventory: filepath.Join(t.TempDir(), "none")}, &out, &errb); code != 2 {
		t.Fatal(code)
	}
	bad := filepath.Join(t.TempDir(), "inv.json")
	_ = os.WriteFile(bad, []byte("{}"), 0o644)
	if code := run(options{inventory: bad}, &out, &errb); code != 2 {
		t.Fatal(code)
	}
	if code := run(options{inventory: writeInv(t), owners: []string{"nobody"}, batch: 1, jobs: 1}, &out, &errb); code != 2 {
		t.Fatal(code)
	}
	setSeams(t, fakeFleet, func(int, int) error { return fmt.Errorf("%w: 10 left", fleet.ErrBudgetFloor) })
	errb.Reset()
	if code := run(options{inventory: writeInv(t), batch: 5, jobs: 1}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "not starting") {
		t.Fatal(code, errb.String())
	}
}

func TestABudgetStopMarksTheRestUnreadWithoutBisecting(t *testing.T) {
	calls := 0
	setSeams(t, func(b []string) (map[string]workflows, error) {
		calls++
		return nil, fmt.Errorf("%w: 5 left", fleet.ErrBudgetReserve)
	}, okBudget)
	var out, errb bytes.Buffer
	code := run(options{inventory: writeInv(t), batch: 10, jobs: 1}, &out, &errb)
	if code != 2 || calls != 1 || !strings.Contains(out.String(), "0 read, 8 UNREAD") {
		t.Fatal(code, calls, out.String())
	}
}

func TestRootCmd(t *testing.T) {
	setSeams(t, fakeFleet, okBudget)
	c := RootCmd()
	var out, errb bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errb)
	c.SetArgs([]string{"--inventory", writeInv(t), "--owner", "go-b", "--batch", "1"})
	err := c.Execute()
	if err == nil || !strings.Contains(out.String(), "go-b/gone") && !strings.Contains(errb.String(), "go-b/gone") {
		t.Fatalf("go-b/gone is unreadable: %v\n%s%s", err, out.String(), errb.String())
	}
	c = RootCmd()
	c.SetOut(&out)
	c.SetErr(&errb)
	c.SetArgs([]string{"stray"})
	if err := c.Execute(); err == nil {
		t.Fatal("an operand is refused")
	}
	c = RootCmd()
	c.SetOut(&out)
	c.SetErr(&errb)
	c.SetArgs([]string{"-j", "0"})
	if err := c.Execute(); err == nil {
		t.Fatal("-j 0 is refused")
	}
}

func TestDefaultInventory(t *testing.T) {
	if p := defaultInventory(); !strings.HasSuffix(p, "inventory.json") {
		t.Fatal(p)
	}
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	if p := defaultInventory(); p != "inventory.json" {
		t.Fatal(p)
	}
}
