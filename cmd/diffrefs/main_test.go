package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The diff from the recurrence this command exists for: prose committed with a
// reference to the pull request the commit is about to open.
const diffCommittingAFutureNumber = `diff --git a/README.md b/README.md
index 1111111..2222222 100644
--- a/README.md
+++ b/README.md
@@ -1,3 +1,4 @@
 # reader

+Linearization is described in #68, with the measurement that settled it.
 Nothing else changed.
`

func TestAddedReadsOnlyAddedLines(t *testing.T) {
	n, refs := Added(diffCommittingAFutureNumber, "go-pdfkit/reader", 2)
	if n != 1 {
		t.Errorf("added = %d, want 1 (the +++ header is not an added line)", n)
	}
	if len(refs) != 1 || refs[0].String() != "go-pdfkit/reader#68" {
		t.Fatalf("refs = %v, want one go-pdfkit/reader#68", refs)
	}
}

// ⛔ The cross-repository form was invisible to an earlier control whose
// lookbehind was blocked by the letter before the "#".
func TestAddedFindsTheCrossRepositoryForm(t *testing.T) {
	d := "+++ b/x\n+see go-tex/math#27 and #412 and go-zpkg/recipes#150\n"
	_, refs := Added(d, "o/r", 2)
	var got []string
	for _, r := range refs {
		got = append(got, r.String())
	}
	want := []string{"go-tex/math#27", "go-zpkg/recipes#150", "o/r#412"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("refs = %v, want %v", got, want)
	}
}

// ⛔ #1..#3 are TeX macro parameters, C stringification and shell positionals
// far more often than they are references.
func TestAddedIgnoresSmallNumbers(t *testing.T) {
	_, refs := Added("+++ b/x\n+\\def\\foo#1#2#3{#1}\n+and #412\n", "o/r", 4)
	if len(refs) != 1 || refs[0].Number != 412 {
		t.Errorf("refs = %v, want only #412", refs)
	}
}

// A line already in the file is somebody else's problem: it was checked when it
// was written, and re-reporting it would drown the one line that is new.
func TestAddedSkipsContextAndRemovedLines(t *testing.T) {
	d := "--- a/x\n+++ b/x\n see #700\n-gone #701\n+new #702\n"
	_, refs := Added(d, "o/r", 2)
	if len(refs) != 1 || refs[0].Number != 702 {
		t.Errorf("refs = %v, want only #702", refs)
	}
}

type api struct {
	exists map[int]string
	asked  []int
}

func (a *api) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var n int
		if i := strings.LastIndex(r.URL.Path, "/"); i >= 0 {
			fmt.Sscanf(r.URL.Path[i+1:], "%d", &n)
		}
		a.asked = append(a.asked, n)
		title, ok := a.exists[n]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"title": title, "state": "open"})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func tok(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(p, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func patch(t *testing.T, s string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "p.diff")
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ⛔⛔ The case this command exists for, end to end: at commit time #68 does not
// exist, and the run must be red even though the number will turn out right.
func TestARefusalForANumberThatDoesNotExistYet(t *testing.T) {
	a := &api{exists: map[int]string{67: "the previous one"}}
	base := a.start(t)
	var out, errBuf bytes.Buffer
	got := run(patch(t, diffCommittingAFutureNumber), "go-pdfkit/reader", tok(t), base, 2, &out, &errBuf)
	if got != 1 {
		t.Fatalf("status = %d, want 1\n%s%s", got, out.String(), errBuf.String())
	}
	if !strings.Contains(out.String(), "does not exist") || !strings.Contains(out.String(), "#68") {
		t.Errorf("the report must name the reference:\n%s", out.String())
	}
}

// The accepting side of the boundary: a reference that already exists passes,
// and its TITLE is printed, because existence alone never proves it right.
func TestAnExistingReferencePassesAndItsTitleIsPrinted(t *testing.T) {
	a := &api{exists: map[int]string{68: "Linearize: the cross-reference stream"}}
	base := a.start(t)
	var out, errBuf bytes.Buffer
	got := run(patch(t, diffCommittingAFutureNumber), "go-pdfkit/reader", tok(t), base, 2, &out, &errBuf)
	if got != 0 {
		t.Fatalf("status = %d, want 0\n%s%s", got, out.String(), errBuf.String())
	}
	if !strings.Contains(out.String(), "Linearize: the cross-reference stream") {
		t.Errorf("the title must be printed, so a person can compare it:\n%s", out.String())
	}
}

// ⛔ A run that read nothing must not look like a clean one.
func TestARunThatReadNothingIsNotClean(t *testing.T) {
	a := &api{}
	base := a.start(t)
	var out, errBuf bytes.Buffer
	if got := run(patch(t, "--- a/x\n+++ b/x\n unchanged\n"), "o/r", tok(t), base, 2, &out, &errBuf); got != 2 {
		t.Errorf("status = %d, want 2 for a diff that adds no lines", got)
	}
	out.Reset()
	errBuf.Reset()
	if got := run(filepath.Join(t.TempDir(), "nope.diff"), "o/r", tok(t), base, 2, &out, &errBuf); got != 2 {
		t.Errorf("status = %d, want 2 for an unreadable diff", got)
	}
	if len(a.asked) != 0 {
		t.Errorf("a run that read nothing asked the API %d time(s)", len(a.asked))
	}
}

// A diff with added lines and no references is clean, and says so: that is a
// different answer from "nothing was read".
func TestAddedLinesWithNoReferencesAreClean(t *testing.T) {
	a := &api{}
	base := a.start(t)
	var out, errBuf bytes.Buffer
	if got := run(patch(t, "+++ b/x\n+a line with no reference\n"), "o/r", tok(t), base, 2, &out, &errBuf); got != 0 {
		t.Errorf("status = %d, want 0\n%s%s", got, out.String(), errBuf.String())
	}
	if !strings.Contains(out.String(), "1 added line(s) read") {
		t.Errorf("the count must be printed:\n%s", out.String())
	}
}

// ⛔ An empty title is not an answer: a 200 with nothing in it is how a check
// comes to report a reference it never saw.
func TestAnEmptyTitleIsRefused(t *testing.T) {
	a := &api{exists: map[int]string{68: ""}}
	base := a.start(t)
	var out, errBuf bytes.Buffer
	if got := run(patch(t, diffCommittingAFutureNumber), "go-pdfkit/reader", tok(t), base, 2, &out, &errBuf); got != 2 {
		t.Errorf("status = %d, want 2\n%s%s", got, out.String(), errBuf.String())
	}
}

func TestRootCmdRefusesAMissingRepo(t *testing.T) {
	c := RootCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs([]string{"some.diff"})
	if err := c.Execute(); err == nil {
		t.Fatal("a bare #N has no meaning without --repo; want a refusal")
	}
}
