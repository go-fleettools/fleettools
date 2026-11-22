package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
)

const serverMain = "package main\nimport \"net/http\"\nfunc main() { _ = http.ListenAndServe(\":0\", nil) }\n"

const plaintextCfg = "package c\ntype C struct {\n\tPassword string `toml:\"password\"`\n}\n"
const referenceCfg = "package c\ntype C struct {\n\tPasswordFrom string `toml:\"password_from\"`\n}\n"

func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func drive(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := cli.Drive(RootCmd(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func withFakes(t *testing.T) {
	t.Helper()
	oldNow, oldGit := now, git
	t.Cleanup(func() { now, git = oldNow, oldGit })
	fixed := time.Date(2026, 11, 22, 15, 0, 0, 0, time.UTC)
	now = func() time.Time { return fixed }
	git = func(_ string, args ...string) (string, error) {
		if args[0] == "rev-parse" {
			return ".git/FETCH_HEAD", nil
		}
		return "", errors.New("none")
	}
}

func clones(t *testing.T) string {
	root := t.TempDir()
	write(t, root, map[string]string{
		"d-one/.git/FETCH_HEAD": "x", "d-one/cmd/d/main.go": serverMain, "d-one/c/c.go": plaintextCfg,
	})
	return root
}

func TestWithoutASubcommandItIsAnUnansweredQuestion(t *testing.T) {
	code, _, errOut := drive()
	if code != 2 || !strings.Contains(errOut, "a subcommand is required") {
		t.Fatalf("%d %q", code, errOut)
	}
}

func TestSweepWritesJSONAndMarkdownAndSaysWhatItCovered(t *testing.T) {
	withFakes(t)
	root := clones(t)
	out := t.TempDir()
	js, md := filepath.Join(out, "r.json"), filepath.Join(out, "r.md")
	code, stdout, errOut := drive("sweep", "--root", root, "--json", js, "--md", md, "--stale-after", "1h")
	if code != 0 || stdout != "" {
		t.Fatalf("code %d stdout %q err %q", code, stdout, errOut)
	}
	if !strings.Contains(errOut, "1 clones seen, 1 read, 0 skipped") || !strings.Contains(errOut, "1 with a plaintext secret") {
		t.Errorf("summary: %q", errOut)
	}
	for f, want := range map[string]string{js: `"clones_seen": 1`, md: "# Plaintext secret inputs of fleet daemons"} {
		b, err := os.ReadFile(f)
		if err != nil || !strings.Contains(string(b), want) {
			t.Errorf("%s: %v lacks %q", f, err, want)
		}
	}
}

func TestSweepWithNoOutputFilesPrintsTheMarkdown(t *testing.T) {
	withFakes(t)
	code, stdout, _ := drive("sweep", "--root", clones(t))
	if code != 0 || !strings.Contains(stdout, "d-one") {
		t.Fatalf("%d %q", code, stdout)
	}
	code, stdout, _ = drive("sweep", "--root", clones(t), "--json", filepath.Join(t.TempDir(), "only.json"))
	if code != 0 || stdout != "" {
		t.Fatalf("json only: %d %q", code, stdout)
	}
}

func TestSweepIsNotReadWhenAnythingWasSkipped(t *testing.T) {
	withFakes(t)
	root := clones(t)
	write(t, root, map[string]string{"plain/readme.txt": "x"})
	code, _, errOut := drive("sweep", "--root", root, "--json", filepath.Join(t.TempDir(), "r.json"))
	if code != 2 || !strings.Contains(errOut, "NOT READ: 1 clone(s) skipped") {
		t.Fatalf("%d %q", code, errOut)
	}
}

func TestSweepFailuresAreUnanswered(t *testing.T) {
	withFakes(t)
	root := clones(t)
	dir := t.TempDir()
	missing := filepath.Join(dir, "no", "such", "dir", "x")
	if code, _, _ := drive("sweep", "--root", filepath.Join(dir, "absent")); code != 2 {
		t.Errorf("absent root: %d", code)
	}
	if code, _, _ := drive("sweep", "--root", root, "--json", missing); code != 2 {
		t.Errorf("unwritable json: %d", code)
	}
	if code, _, _ := drive("sweep", "--root", root, "--md", missing); code != 2 {
		t.Errorf("unwritable md: %d", code)
	}
	var errOut bytes.Buffer
	c := RootCmd()
	if code := cli.Drive(c, []string{"sweep", "--root", root}, failWriter{}, &errOut); code != 2 {
		t.Errorf("unwritable stdout: %d", code)
	}
	if code, _, _ := drive("sweep", "extra"); code != 2 {
		t.Errorf("an operand: %d", code)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestWriteFileClosesAndReportsAFailedWrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x")
	err := writeFile(p, func(io.Writer) error { return errors.New("encode") })
	if err == nil || err.Error() != "encode" {
		t.Fatalf("%v", err)
	}
}

func repoOf(t *testing.T, files map[string]string) string {
	root := t.TempDir()
	write(t, root, files)
	return root
}

func TestRepoGateRefusesAPlaintextInputOfADaemon(t *testing.T) {
	root := repoOf(t, map[string]string{"cmd/d/main.go": serverMain, "c/c.go": plaintextCfg})
	code, out, _ := drive("repo", root)
	if code != 1 || !strings.Contains(out, "c.go:3: plaintext secret input `password` (config-field)") {
		t.Fatalf("%d %q", code, out)
	}
}

func TestRepoGatePassesAReferenceAndAnUnneededWaiverIsSaid(t *testing.T) {
	root := repoOf(t, map[string]string{"cmd/d/main.go": serverMain, "c/c.go": referenceCfg})
	code, out, _ := drive("repo", root)
	if code != 0 || !strings.Contains(out, "ok: no plaintext secret input") || strings.Contains(out, "waiver") {
		t.Fatalf("%d %q", code, out)
	}
	code, out, _ = drive("repo", "--waive", "no reason is needed here", root)
	if code != 0 || !strings.Contains(out, "note: the waiver was not needed") {
		t.Fatalf("%d %q", code, out)
	}
}

func TestRepoWaiverStampsItsReason(t *testing.T) {
	withFakes(t)
	root := repoOf(t, map[string]string{"cmd/d/main.go": serverMain, "c/c.go": plaintextCfg})
	code, out, _ := drive("repo", "--waive", "  legacy bridge, ends with v2  ", root)
	want := "WAIVED 2026-11-22T15:00:00Z " + root + ": 1 plaintext secret input(s): legacy bridge, ends with v2"
	if code != 0 || !strings.Contains(out, want) {
		t.Fatalf("%d %q", code, out)
	}
}

func TestRepoWaiverWithoutAReasonIsRefused(t *testing.T) {
	root := repoOf(t, map[string]string{"cmd/d/main.go": serverMain, "c/c.go": plaintextCfg})
	for _, reason := range []string{"", "   ", "short"} {
		code, _, errOut := drive("repo", "--waive", reason, root)
		if code != 2 || !strings.Contains(errOut, "at least 12 characters") {
			t.Errorf("waive %q: %d %q", reason, code, errOut)
		}
	}
}

func TestRepoGateOnAClientNeedsAll(t *testing.T) {
	root := repoOf(t, map[string]string{"main.go": "package main\nimport \"flag\"\nfunc main() { flag.String(\"token\", \"\", \"\") }\n"})
	code, out, _ := drive("repo", root)
	if code != 0 || !strings.Contains(out, "is not a daemon (1 Go files read)") {
		t.Fatalf("%d %q", code, out)
	}
	code, out, _ = drive("repo", "--all", root)
	if code != 1 || !strings.Contains(out, "plaintext secret input `token` (flag)") {
		t.Fatalf("--all: %d %q", code, out)
	}
}

func TestRepoGateHasNoVerdictOnWhatItCouldNotRead(t *testing.T) {
	root := repoOf(t, map[string]string{"cmd/d/main.go": serverMain, "bad.go": "package {"})
	code, out, errOut := drive("repo", root)
	if code != 2 || !strings.Contains(out, "NOT READ bad.go") || !strings.Contains(errOut, "no verdict") {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	if code, _, _ := drive("repo", filepath.Join(t.TempDir(), "absent")); code != 2 {
		t.Errorf("absent clone: %d", code)
	}
	if code, _, _ := drive("repo"); code != 2 {
		t.Errorf("no clone named: %d", code)
	}
}

func TestDefaultRootIsTheFleetsClonesDirectory(t *testing.T) {
	if got := defaultRoot(); !strings.HasSuffix(got, filepath.Join("VCS", "GIT", "localhost")) {
		t.Errorf("defaultRoot() = %q", got)
	}
	t.Setenv("HOME", "")
	if got := defaultRoot(); got != "." {
		t.Errorf("defaultRoot() without HOME = %q", got)
	}
}
