package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// stubGoMods is the root go.mod text graphqlStub returns per repository.
var stubGoMods = map[string]string{}

var reAlias = regexp.MustCompile(`r(\d+): repository\(owner: "([^"]+)", name: "([^"]+)"\)`)

// graphqlStub answers a prefilter query from repo -> workflow files. A repo in
// bad makes any batch containing it fail the way a missing repository does;
// quota makes every call a quota refusal.
func graphqlStub(files map[string]map[string]string, bad map[string]bool, quota bool, calls *int) func(args ...string) ([]byte, error) {
	return func(args ...string) ([]byte, error) {
		if args[1] == "rate_limit" {
			return nil, errors.New("not asked")
		}
		*calls++
		if quota {
			return nil, errors.New("GraphQL: API rate limit already exceeded for user ID 1")
		}
		q := args[len(args)-1]
		data := map[string]any{}
		var problem string
		for _, m := range reAlias.FindAllStringSubmatch(q, -1) {
			repo := m[2] + "/" + m[3]
			if bad[repo] {
				problem = "Could not resolve to a Repository with the name '" + repo + "'."
				data["r"+m[1]] = nil
				continue
			}
			fs, ok := files[repo]
			if !ok {
				data["r"+m[1]] = map[string]any{"object": nil}
				continue
			}
			var entries []any
			for n, body := range fs {
				entries = append(entries, map[string]any{"name": n, "object": map[string]any{"text": body}})
			}
			// a subtree entry has an object with no text
			entries = append(entries, map[string]any{"name": "dir", "object": nil})
			entries = append(entries, map[string]any{"name": "README.md", "object": map[string]any{"text": "go-version: stable"}})
			item := map[string]any{"object": map[string]any{"entries": entries}}
			if mod, ok := stubGoMods[repo]; ok {
				item["gomod"] = map[string]any{"text": mod}
			}
			data["r"+m[1]] = item
		}
		resp := map[string]any{"data": data}
		if problem != "" {
			resp["errors"] = []any{map[string]any{"message": problem, "type": "NOT_FOUND"}}
		}
		return json.Marshal(resp)
	}
}

func TestPrefilterKeepsOnlyWhatHasSomethingToMove(t *testing.T) {
	w := newWorld(t)
	files := map[string]map[string]string{
		"o/old":     {"ci.yml": pinned272},
		"o/alias":   {"ci.yml": "          go-version: stable\n"},
		"o/gomod":   {"ci.yml": "          go-version-file: go.mod\n"},
		"o/newer":   {"ci.yml": "          go-version: '1.27.3'\n"},
		"o/empty":   {},
		"o/held":    {"ci.yml": "          go-version: '1.26.4'\n"},
		"o/matrix":  {"ci.yml": "          go-version: ${{ matrix.go }}\n"},
		"o/nodata":  nil,
		"o/another": {"b.yaml": pinned272},
	}
	// o/nodata has files == nil but is present: treated as a workflows dir with nothing.
	var calls int
	ghRaw = graphqlStub(files, map[string]bool{"o/bad": true}, false, &calls)
	var repos []string
	for r := range files {
		repos = append(repos, r)
	}
	repos = append(repos, "o/bad", "o/nowf")
	list := w.list(repos...)
	for _, tc := range []struct {
		args []string
		want []string
		not  []string
	}{
		{[]string{"-from", "1.27.2", "-version", "1.27.3"}, []string{"o/old", "o/alias", "o/another", "o/bad"}, []string{"o/gomod", "o/newer", "o/empty", "o/held", "o/matrix", "o/nowf", "o/nodata"}},
		{[]string{"-from", "1.27.2", "-version", "1.27.3", "-gomod"}, []string{"o/old", "o/alias", "o/another", "o/gomod", "o/bad"}, []string{"o/newer", "o/matrix"}},
	} {
		var o, e strings.Builder
		code := run(append(tc.args, "-prefilter", "-list", list), strings.NewReader(""), &o, &e)
		if code != 0 {
			t.Fatalf("%v: code=%d %s", tc.args, code, e.String())
		}
		got := map[string]bool{}
		for _, l := range strings.Fields(o.String()) {
			got[l] = true
		}
		for _, r := range tc.want {
			if !got[r] {
				t.Errorf("%v: %s missing from %v", tc.args, r, o.String())
			}
		}
		for _, r := range tc.not {
			if got[r] {
				t.Errorf("%v: %s kept", tc.args, r)
			}
		}
		// the sum closes: candidates + nothing to move = read
		if !strings.Contains(e.String(), fmt.Sprintf("prefilter: %d read", len(repos))) || !strings.Contains(e.String(), "1 unreadable by GraphQL") {
			t.Errorf("summary: %s", e.String())
		}
	}
	// 11 repositories fit one batch of 20, plus bisection of the failing one.
	if calls < 2 {
		t.Errorf("calls = %d; the failing batch should have been split", calls)
	}
}

func TestPrefilterBatchesAndStopsOnQuota(t *testing.T) {
	w := newWorld(t)
	var repos []string
	files := map[string]map[string]string{}
	for i := range 45 {
		r := fmt.Sprintf("o/r%02d", i)
		repos = append(repos, r)
		files[r] = map[string]string{"ci.yml": "name: x\n"}
	}
	var calls int
	ghRaw = graphqlStub(files, nil, false, &calls)
	var o, e strings.Builder
	if code := run([]string{"-version", "1.27.3", "-prefilter", "-list", w.list(repos...)}, strings.NewReader(""), &o, &e); code != 0 || calls != 3 || o.String() != "" {
		t.Fatalf("code=%d calls=%d out=%q err=%s", code, calls, o.String(), e.String())
	}
	calls = 0
	ghRaw = graphqlStub(files, nil, true, &calls)
	o.Reset()
	e.Reset()
	code := run([]string{"-version", "1.27.3", "-prefilter", "-list", w.list(repos...)}, strings.NewReader(""), &o, &e)
	if code != 1 || !strings.Contains(e.String(), "prefilter stopped at 0/45") || calls != quotaRetries+1 {
		t.Fatalf("code=%d calls=%d err=%s", code, calls, e.String())
	}
	// A quota refusal in the second half of a bisected batch stops it too.
	code, _, errs := w.run("-version", "1.27.3", "-prefilter", "-apply", "-list", w.list("o/a"))
	if code != 2 || !strings.Contains(errs, "only reads") {
		t.Fatalf("code=%d %s", code, errs)
	}
}

func TestPrefilterQuotaDuringBisection(t *testing.T) {
	w := newWorld(t)
	n := 0
	ghRaw = func(args ...string) ([]byte, error) {
		n++
		if n == 1 {
			return []byte(`{"errors":[{"message":"x","type":"NOT_FOUND"}],"data":{}}`), nil
		}
		if n == 2 {
			return nil, errors.New("API rate limit exceeded")
		}
		return nil, errors.New("API rate limit exceeded")
	}
	quotaRetries = 0
	code, _, errs := w.run("-version", "1.27.3", "-prefilter", "-list", w.list("o/a", "o/b"))
	if code != 1 || !strings.Contains(errs, "prefilter stopped") {
		t.Fatalf("code=%d %s", code, errs)
	}
	// ... and in the second half
	n = 0
	ghRaw = func(args ...string) ([]byte, error) {
		n++
		switch n {
		case 1:
			return []byte(`not json`), nil
		case 2:
			return []byte(`{"data":{"r0":{"object":null}}}`), nil
		}
		return nil, errors.New("API rate limit exceeded")
	}
	code, _, errs = w.run("-version", "1.27.3", "-prefilter", "-list", w.list("o/a", "o/b"))
	if code != 1 || !strings.Contains(errs, "prefilter stopped") {
		t.Fatalf("code=%d %s", code, errs)
	}
}

// A workflow reading its version from root go.mod is a candidate only when that
// directive is exactly --from; anything GraphQL cannot settle is passed on.
func TestPrefilterReadsTheGoModBehindGoVersionFile(t *testing.T) {
	w := newWorld(t)
	vf := "          go-version-file: go.mod\n"
	files := map[string]map[string]string{
		"o/exact":   {"ci.yml": vf},
		"o/newer":   {"ci.yml": vf},
		"o/nomod":   {"ci.yml": vf},
		"o/nested":  {"ci.yml": "          go-version-file: tools/go.mod\n"},
		"o/nodirec": {"ci.yml": vf},
	}
	stubGoMods = map[string]string{"o/exact": "module x\n\ngo 1.27.2\n", "o/newer": "module x\n\ngo 1.27.3\n",
		"o/nested": "module x\n\ngo 1.21\n", "o/nodirec": "module x\n"}
	t.Cleanup(func() { stubGoMods = map[string]string{} })
	var calls int
	ghRaw = graphqlStub(files, nil, false, &calls)
	var o, e strings.Builder
	code := run([]string{"-from", "1.27.2", "-version", "1.27.3", "-gomod", "-prefilter", "-list", w.list("o/exact", "o/newer", "o/nomod", "o/nested", "o/nodirec")}, strings.NewReader(""), &o, &e)
	got := strings.Fields(o.String())
	if code != 0 || strings.Join(got, " ") != "o/exact o/nomod o/nested o/nodirec" {
		t.Fatalf("code=%d candidates=%v (o/newer is already past --from and must be dropped)\n%s", code, got, e.String())
	}
}

func TestNoAliasAndTheElsewhereReport(t *testing.T) {
	w := newWorld(t)
	files := map[string]map[string]string{
		"o/alias":  {"ci.yml": "          go-version: stable\n"},
		"o/both":   {"ci.yml": "          go-version: stable\n          go-version: '1.27.2'\n"},
		"o/matrix": {"ci.yml": "        go: ['1.26.4', '1.27.2']\n"},
		"o/env":    {"ci.yml": "env:\n  GO_VERSION: 1.27.2\n"},
	}
	var calls int
	ghRaw = graphqlStub(files, nil, false, &calls)
	list := w.list("o/alias", "o/both", "o/matrix", "o/env")
	var o, e strings.Builder
	if code := run([]string{"-from", "1.27.2", "-version", "1.27.3", "-no-alias", "-prefilter", "-list", list}, strings.NewReader(""), &o, &e); code != 0 {
		t.Fatalf("code=%d %s", code, e.String())
	}
	if strings.Fields(o.String())[0] != "o/both" || len(strings.Fields(o.String())) != 1 {
		t.Errorf("candidates = %q; want only o/both (an alias alone is not moved)", o.String())
	}
	for _, want := range []string{"elsewhere o/matrix", "elsewhere o/env", "2 naming 1.27.2 elsewhere"} {
		if !strings.Contains(e.String(), want) {
			t.Errorf("missing %q in %s", want, e.String())
		}
	}
	// the sweep side: o/both is moved but its alias is left as it is
	w.files["o/both"] = map[string]string{".github/workflows/ci.yml": "          go-version: stable\n          go-version: '1.27.2'\n"}
	ghRaw = w.gh
	code, out, _ := w.run("-from", "1.27.2", "-version", "1.27.3", "-no-alias", "-list", w.list("o/both"), "-apply", "-pause", "0s")
	if code != 0 || !strings.Contains(out, "REPIN   o/both") || strings.Contains(out, "ALIAS") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}
