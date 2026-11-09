package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write puts a body in a temp file and returns its path.
func write(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const leak = "## The measurement\n\n" +
	"`/Users/alicia/Documents/fairshare-b300/fairshare-b300.tex`, 36 pages.\n" +
	"filed from localhost-55 by alicia.\n"

func TestScanFileReportsAndRefuses(t *testing.T) {
	p := write(t, "body.md", leak)
	var out, errOut bytes.Buffer
	if code := run([]string{"--no-self", p}, &out, &errOut); code != 1 {
		t.Errorf("exit %d, want 1 (found); stderr=%s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "line 3") {
		t.Errorf("no location in the report:\n%s", got)
	}
	// ⛔ A refusal is printed where the body was going, and that is sometimes a
	// public CI log. The gate must not publish the path in refusing it.
	for _, secret := range []string{"alicia", "fairshare-b300", "/Users/"} {
		if strings.Contains(got, secret) {
			t.Errorf("the report reprinted %q:\n%s", secret, got)
		}
	}
	if !strings.Contains(got, "finding(s) across 1 input(s)") {
		t.Errorf("no summary of what was read:\n%s", got)
	}
}

func TestShowRevealsForALocalRun(t *testing.T) {
	p := write(t, "body.md", leak)
	var out, errOut bytes.Buffer
	if code := run([]string{"--no-self", "--show", p}, &out, &errOut); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(out.String(), "/Users/alicia/Documents/fairshare-b300/fairshare-b300.tex") {
		t.Errorf("--show did not print the match:\n%s", out.String())
	}
}

func TestFixRewritesTheFile(t *testing.T) {
	p := write(t, "body.md", leak)
	var out, errOut bytes.Buffer
	// ⛔ Exit 1, not 0: something WAS found. A lane that fixes a body and reads
	// 0 cannot tell a clean body from one that had to be cleaned.
	if code := run([]string{"--no-self", "--fix", p}, &out, &errOut); code != 1 {
		t.Errorf("exit %d, want 1 (found and fixed); stderr=%s", code, errOut.String())
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	fixed := string(b)
	for _, secret := range []string{"alicia", "fairshare-b300", "/Users/"} {
		if strings.Contains(fixed, secret) {
			t.Errorf("the fix left %q behind:\n%s", secret, fixed)
		}
	}
	if !strings.Contains(fixed, "The measurement") || !strings.Contains(fixed, "36 pages") {
		t.Errorf("the fix destroyed the prose that was the point:\n%s", fixed)
	}
	// Running it again finds nothing, or a pre-commit hook would rewrite the
	// file on every run.
	var out2, err2 bytes.Buffer
	if code := run([]string{"--no-self", p}, &out2, &err2); code != 0 {
		t.Errorf("a fixed file still refused: exit %d\n%s", code, out2.String())
	}
}

// ⛔ A published body cannot be fixed by rewriting: the original stays under
// "edited". Offering the fix would tell the author the leak is undone.
func TestFixRefusesAForgeBody(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"--fix", "--repo", "o/r"}, &out, &errOut)
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "edited") || !strings.Contains(errOut.String(), "delete") {
		t.Errorf("the refusal does not say why or what to do instead:\n%s", errOut.String())
	}
}

func TestUnreadableFileCannotBeAnswered(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--no-self", filepath.Join(t.TempDir(), "absent.md")}, &out, &errOut); code != 2 {
		t.Errorf("exit %d, want 2 (could not answer)", code)
	}
}

func TestNoArgumentsIsAUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if out.Len() != 0 {
		t.Errorf("help went to stdout, which is the answer stream: %q", out.String())
	}
}

// --- the forge half ------------------------------------------------------

type page []map[string]any

func fixture(t *testing.T, issues, comments, review page) func(...string) ([]byte, error) {
	t.Helper()
	enc := func(p page) []byte {
		if p == nil {
			p = page{}
		}
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	return func(args ...string) ([]byte, error) {
		for _, a := range args {
			switch {
			case strings.Contains(a, "/pulls/comments"):
				return enc(review), nil
			case strings.Contains(a, "/issues/comments"):
				return enc(comments), nil
			case strings.Contains(a, "/issues?"):
				return enc(issues), nil
			}
		}
		t.Fatalf("unexpected call: %v", args)
		return nil, nil
	}
}

// ⛔ THE GAP THE FIRST SWEEP HAD. A code search does not index comments
// reliably, so searching found the leak in a body and could not have found one
// in a comment. This asserts the comment is read.
func TestRepoScanReadsComments(t *testing.T) {
	old := ghAPI
	defer func() { ghAPI = old }()
	ghAPI = fixture(t,
		page{{"number": 1, "title": "a clean title", "body": "nothing here"}},
		page{{"html_url": "https://github.com/o/r/issues/1#issuecomment-9",
			"body": "measured at /Users/alicia/thesis/draft.tex"}},
		nil)

	var out, errOut bytes.Buffer
	if code := run([]string{"--no-self", "--repo", "o/r"}, &out, &errOut); code != 1 {
		t.Errorf("exit %d, want 1; stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "issuecomment-9") {
		t.Errorf("the comment was not reported:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "(comment)") {
		t.Errorf("the report does not say it was a comment:\n%s", out.String())
	}
}

// A title is published text too.
func TestRepoScanReadsTitles(t *testing.T) {
	old := ghAPI
	defer func() { ghAPI = old }()
	ghAPI = fixture(t,
		page{{"number": 7, "title": "crash in /Users/alicia/x.tex", "body": "clean"}},
		nil, nil)

	var out, errOut bytes.Buffer
	if code := run([]string{"--no-self", "--repo", "o/r"}, &out, &errOut); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(out.String(), "o/r#7") || !strings.Contains(out.String(), "title") {
		t.Errorf("the title was not reported:\n%s", out.String())
	}
}

// The control for the two above: the same shape with nothing to find must come
// back clean, or they would pass on a scanner that reports everything.
func TestRepoScanCleanIsClean(t *testing.T) {
	old := ghAPI
	defer func() { ghAPI = old }()
	ghAPI = fixture(t,
		page{{"number": 1, "title": "a clean title", "body": "`/Users/runner/.pkgx` is the runner's home"}},
		page{{"html_url": "u", "body": "no path at all"}},
		nil)

	var out, errOut bytes.Buffer
	if code := run([]string{"--no-self", "--repo", "o/r"}, &out, &errOut); code != 0 {
		t.Errorf("exit %d, want 0; out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "texts read") {
		t.Errorf("a clean sweep does not say how much it read:\n%s", out.String())
	}
}

// ⛔⛔ A SWEEP THAT COULD NOT READ MUST NOT REPORT A CLEAN FLEET. This
// repository has that defect on record: a blind scan returning zero reads
// exactly like a population with nothing wrong.
func TestFailedReadIsNotCleanliness(t *testing.T) {
	old := ghAPI
	defer func() { ghAPI = old }()
	ghAPI = func(...string) ([]byte, error) { return nil, os.ErrPermission }

	var out, errOut bytes.Buffer
	code := run([]string{"--no-self", "--repo", "o/r"}, &out, &errOut)
	if code != 2 {
		t.Errorf("exit %d, want 2 — a failed read is not a clean repository", code)
	}
	if strings.Contains(out.String(), "clean") {
		t.Errorf("a failed read was reported as clean:\n%s", out.String())
	}
}

// ⛔ The sweep's hardest case: a body that names the account with NO path in it.
// A scanner that can only learn names from paths calls it clean, and that is
// most of what a sweep reads.
func TestNameHuntsWithoutAPath(t *testing.T) {
	p := write(t, "body.md", "filed from localhost-55 by alicia\n")
	var out, errOut bytes.Buffer
	if code := run([]string{"--no-self", p}, &out, &errOut); code != 0 {
		t.Fatalf("there is no path to learn from: exit %d\n%s", code, out.String())
	}
	var out2, err2 bytes.Buffer
	if code := run([]string{"--no-self", "--name", "alicia", p}, &out2, &err2); code != 1 {
		t.Errorf("--name did not hunt it: exit %d\n%s", code, out2.String())
	}
}

// The running account is hunted by default, for the same reason.
func TestSelfAccountIsHuntedByDefault(t *testing.T) {
	self := selfAccount()
	if self == "" || len(self) < 5 {
		t.Skipf("the running account is %q, which this gate does not hunt bare", self)
	}
	p := write(t, "body.md", "reported by "+self+"\n")
	var out, errOut bytes.Buffer
	if code := run([]string{p}, &out, &errOut); code != 1 {
		t.Errorf("the running account was not hunted: exit %d\n%s", code, out.String())
	}
	var out2, err2 bytes.Buffer
	if code := run([]string{"--no-self", p}, &out2, &err2); code != 0 {
		t.Errorf("--no-self still hunted it: exit %d\n%s", code, out2.String())
	}
}

// gh --paginate concatenates one array per page instead of merging them.
func TestDecodeItemsReadsConcatenatedPages(t *testing.T) {
	raw := []byte(`[{"number":1,"title":"t1","body":"b1"}][{"number":2,"title":"t2","body":"b2"}]`)
	got, err := decodeItems(raw, "o/r", "issue")
	if err != nil {
		t.Fatal(err)
	}
	// Two items per entry: the title and the body.
	if len(got) != 4 {
		t.Fatalf("read %d items from two pages, want 4: %v", len(got), got)
	}
	if got[0].Ref != "o/r#1" || got[3].Body != "b2" {
		t.Errorf("pages were not read in order: %v", got)
	}
}
