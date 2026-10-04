package main

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

// stubGH answers the three API shapes inspect uses, from a map of
// path-fragment -> body. Anything unmatched is a 404, which is what GitHub
// returns for a repository with no workflows directory and no go.mod.
func stubGH(t *testing.T, files map[string]string) func() {
	t.Helper()
	prev := ghJSON
	ghJSON = func(args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "contents/.github/workflows\""),
			strings.HasSuffix(joined, "contents/.github/workflows --jq .[].name"):
			var names []string
			for k := range files {
				if n, ok := strings.CutPrefix(k, ".github/workflows/"); ok {
					names = append(names, n)
				}
			}
			if len(names) == 0 {
				return nil, fmt.Errorf("404 Not Found")
			}
			return []byte(strings.Join(names, "\n")), nil
		}
		for path, body := range files {
			if strings.Contains(joined, "contents/"+path+" ") || strings.HasSuffix(joined, "contents/"+path) {
				return []byte(base64.StdEncoding.EncodeToString([]byte(body))), nil
			}
		}
		return nil, fmt.Errorf("404 Not Found")
	}
	return func() { ghJSON = prev }
}

func TestInspectFindsBothSpellingsOfTheAlias(t *testing.T) {
	defer stubGH(t, map[string]string{
		".github/workflows/ci.yml": "" +
			"      - uses: actions/setup-go@v7\n" +
			"        with:\n" +
			"          go-version: stable\n" +
			"      - uses: actions/setup-go@v7\n" +
			"        with: { go-version: stable }\n",
		"go.mod": "module x\n\ngo 1.26.4\n",
	})()
	f := inspect("o/r", "1.27.1")
	if f.Err != "" {
		t.Fatalf("err = %q", f.Err)
	}
	// Block mapping and inline flow mapping are both in use across the fleet;
	// matching only one would silently leave half of them behind.
	if f.Aliases != 2 {
		t.Errorf("aliases = %d; want 2", f.Aliases)
	}
	if f.GoMod != "1.26.4" {
		t.Errorf("gomod = %q; want 1.26.4", f.GoMod)
	}
}

func TestInspectLeavesAnExplicitVersionAloneAndReportsIt(t *testing.T) {
	defer stubGH(t, map[string]string{
		".github/workflows/ci.yml": "        with: { go-version: \"1.24.0\" }\n",
	})()
	f := inspect("o/r", "1.27.1")
	if f.Aliases != 0 {
		t.Fatalf("aliases = %d; want 0 — a chosen version is not this tool's business", f.Aliases)
	}
	if len(f.Literals) != 1 || f.Literals[0] != "1.24.0" {
		t.Errorf("literals = %v; want [1.24.0]", f.Literals)
	}
}

// A repository with no workflows is not an error. It has nothing to pin, and
// treating absence as failure would drown the real failures.
func TestInspectIsQuietAboutARepositoryWithNoWorkflows(t *testing.T) {
	defer stubGH(t, map[string]string{})()
	f := inspect("o/r", "1.27.1")
	if f.Err != "" || f.Aliases != 0 {
		t.Fatalf("got err=%q aliases=%d; want a quiet zero", f.Err, f.Aliases)
	}
}

func TestReplacementKeepsTheSurroundingSyntax(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"          go-version: stable\n", "          go-version: '1.27.1'\n"},
		{"        with: { go-version: stable }\n", "        with: { go-version: '1.27.1' }\n"},
		// `stable-1` is a different channel; a word boundary keeps it out.
		{"          go-version: stable-1\n", "          go-version: stable-1\n"},
		// oldstable is a real setup-go channel and must survive untouched.
		{"          go-version: oldstable\n", "          go-version: oldstable\n"},
		// Last line of a file, no trailing newline.
		{"  go-version: stable", "  go-version: '1.27.1'"},
	} {
		if got := reStable.ReplaceAllString(tc.in, "${1}'1.27.1'${2}"); got != tc.want {
			t.Errorf("replace(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestGoDirectiveIsRewrittenWithoutTouchingRequires(t *testing.T) {
	in := "module x\n\ngo 1.26.4\n\nrequire (\n\tgolang.org/x/mod v0.41.0 // go 1.21\n)\n"
	got := reGoDirective.ReplaceAllString(in, "go 1.27.1")
	if !strings.Contains(got, "go 1.27.1") {
		t.Fatalf("directive not rewritten:\n%s", got)
	}
	// The anchored pattern must not reach a `go 1.21` inside a comment.
	if !strings.Contains(got, "// go 1.21") {
		t.Errorf("a comment was rewritten:\n%s", got)
	}
}

func TestReadListRejectsSomethingThatIsNotOrgRepo(t *testing.T) {
	if _, err := readListFrom("just-a-name\n"); err == nil {
		t.Fatal("readList: nil; want a refusal")
	}
	got, err := readListFrom("a/b  # comment\n\n# whole line\na/b\nc/d\n")
	if err != nil {
		t.Fatalf("readList: %v", err)
	}
	if len(got) != 2 || got[0] != "a/b" || got[1] != "c/d" {
		t.Errorf("got %v; want [a/b c/d] — duplicates and comments dropped", got)
	}
}

func TestPrBodyCarriesThisRepositorysOwnNumbers(t *testing.T) {
	b := prBody(finding{Aliases: 3, Files: []string{"ci.yml"}, GoMod: "1.26.4", Literals: []string{"1.24.0"}}, "1.27.1")
	for _, want := range []string{"**3**", "`ci.yml`", "go 1.26.4", "go 1.27.1", "1.24.0", "golang/go#81147"} {
		if !strings.Contains(b, want) {
			t.Errorf("body does not mention %q", want)
		}
	}
}

// run must refuse an empty corpus: a sweep that read nothing reports success
// exactly like one that read everything and found it clean.
func TestRunRefusesAnEmptyList(t *testing.T) {
	var out, errb strings.Builder
	f, err := writeTemp(t, "# only a comment\n")
	if err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"-version", "1.27.1", "-list", f}, &out, &errb); code != 1 {
		t.Fatalf("code = %d; want 1", code)
	}
	if !strings.Contains(errb.String(), "not a clean fleet") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestRunRequiresAVersion(t *testing.T) {
	var out, errb strings.Builder
	if code := run(nil, &out, &errb); code != 2 {
		t.Fatalf("code = %d; want 2", code)
	}
	if !strings.Contains(errb.String(), "same unreviewable choice as `stable`") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func writeTemp(t *testing.T, body string) (string, error) {
	t.Helper()
	p := t.TempDir() + "/list.txt"
	return p, writeFile(p, body)
}
