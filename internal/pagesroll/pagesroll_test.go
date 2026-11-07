package pagesroll

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func golden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// go-redfish/docs#11: before is the parent of merge 1beaa7d, after is the merge.
func TestApplyReproducesPullRequest11(t *testing.T) {
	got, err := Apply(golden(t, "before.yml"), DefaultVersion)
	if err != nil {
		t.Fatal(err)
	}
	if want := golden(t, "after.yml"); got != want {
		t.Fatalf("output differs from go-redfish/docs@1beaa7d:\n%s", got)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	after := golden(t, "after.yml")
	got, err := Apply(after, DefaultVersion)
	if !errors.Is(err, ErrAlready) || got != after {
		t.Fatalf("err=%v, changed=%v", err, got != after)
	}
}

func TestApplyRefusesUnknownShapes(t *testing.T) {
	before := golden(t, "before.yml")
	cases := map[string]string{
		"extra permission":   strings.Replace(before, "      contents: write\n    steps:", "      contents: write\n      id-token: write\n    steps:", 1),
		"no deploy job":      strings.Replace(before, "\n  deploy:\n", "\n  publish:\n", 1),
		"renamed push step":  strings.Replace(before, "name: Push gh-pages", "name: Push pages", 1),
		"push step changed":  strings.Replace(before, "git push origin HEAD:gh-pages", "git push --force origin HEAD:gh-pages", 1),
		"no setup-go":        strings.Replace(before, "actions/setup-go@v6", "actions/setup-node@v6", 1),
		"already has output": strings.Replace(before, "git push origin HEAD:gh-pages", "echo x >> \"$GITHUB_OUTPUT\"\n          git push origin HEAD:gh-pages", 1),
		"crlf":               strings.ReplaceAll(before, "\n", "\r\n"),
	}
	for name, src := range cases {
		if src == before && name != "crlf" {
			t.Fatalf("%s: the case did not change the workflow", name)
		}
		got, err := Apply(src, DefaultVersion)
		if !errors.Is(err, ErrRefused) || got != "" {
			t.Errorf("%s: err=%v got=%d bytes", name, err, len(got))
		}
	}
	if _, err := Apply(before, "0.80.0"); err == nil {
		t.Error("a version without the v was accepted")
	}
}

// A workflow with another step after Push gh-pages keeps that step.
func TestApplyInsertsBeforeALaterStep(t *testing.T) {
	before := golden(t, "before.yml") + "\n      - name: Later\n        run: echo later\n"
	got, err := Apply(before, DefaultVersion)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "\"${{ steps.push.outputs.sha }}\"\n\n      - name: Later\n        run: echo later\n") {
		t.Fatalf("tail:\n%s", got[len(got)-300:])
	}
}

func TestEditLintsTheResult(t *testing.T) {
	al, err := exec.LookPath("actionlint")
	if err != nil {
		t.Skip("actionlint is not installed")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, Workflow)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(golden(t, "before.yml")), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Edit(dir, DefaultVersion, al)
	if err != nil || !strings.Contains(rep, "actionlint clean") {
		t.Fatalf("%q %v", rep, err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != golden(t, "after.yml") {
		t.Fatal("file differs from the golden after")
	}
	if rep, err := Edit(dir, DefaultVersion, al); !errors.Is(err, ErrAlready) {
		t.Fatalf("second run: %q %v", rep, err)
	}
}

// An edit that introduces an actionlint finding is rolled back.
func TestEditRollsBackOnAFinding(t *testing.T) {
	al, err := exec.LookPath("actionlint")
	if err != nil {
		t.Skip("actionlint is not installed")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, Workflow)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	// Clean workflow whose deploy job has the shape but a step that makes
	// the inserted text reference an unknown step id once 'id: push' is renamed.
	src := strings.Replace(golden(t, "before.yml"), "      - uses: actions/download-artifact@v8", "      - id: push\n        uses: actions/download-artifact@v8", 1)
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if rep, err := Lint(al, p); err == nil && rep != "" {
		t.Skipf("the base is not clean for actionlint: %s", rep)
	}
	_, err = Edit(dir, DefaultVersion, al)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("expected a refusal from actionlint, got %v", err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != src {
		t.Fatal("the file was not restored")
	}
}
