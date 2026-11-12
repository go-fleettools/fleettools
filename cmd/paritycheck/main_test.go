package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/parityrepo"
	"github.com/go-fleettools/fleettools/internal/paritytext"
)

const floaty = "Works on float64 and float32 with FMA, checked against NumPy and SciPy to within 1 ULP.\n"

const contract = "## Accuracy contract\n\nBound: 4 ulp\nOracle: double-double\nExact: Options.Exact\n"

func drive(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := cli.Drive(RootCmd(), args, &out, &errb)
	return code, out.String(), errb.String()
}

func write(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, c := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRepoVerdicts(t *testing.T) {
	pass := write(t, map[string]string{"README.md": floaty + "Bit-identical.\n\n" + contract, "a_test.go": "package p\nfunc TestAccuracyBound(t *testing.T) {}\n"})
	fail := write(t, map[string]string{"README.md": floaty + "Output is bit-identical to the reference.\n"})
	quiet := write(t, map[string]string{"README.md": "Nothing to see.\n"})

	code, out, _ := drive("repo", pass)
	if code != 0 || !strings.HasPrefix(out, "PASS ") || strings.Contains(out, "paritycheck:") {
		t.Errorf("pass: %d %q", code, out)
	}
	code, out, _ = drive("repo", fail)
	if code != 1 || !strings.HasPrefix(out, "FAIL ") || !strings.Contains(out, "statement README.md:2: Output is bit-identical to the reference.") {
		t.Errorf("fail: %d %q", code, out)
	}
	absent := filepath.Join(t.TempDir(), "absent")
	code, out, _ = drive("repo", quiet, fail, absent)
	if code != 2 || !strings.Contains(out, "1 PASS, 1 FAIL, 1 UNREAD of 3") || !strings.Contains(out, "UNREAD "+absent) {
		t.Errorf("sweep: %d %q", code, out)
	}
	code, out, _ = drive("repo", quiet, pass)
	if code != 0 || !strings.Contains(out, "2 PASS, 0 FAIL, 0 UNREAD of 2") {
		t.Errorf("sweep ok: %d %q", code, out)
	}
	if code, _, errs := drive("repo"); code == 0 || errs == "" {
		t.Errorf("no clone: %d %q", code, errs)
	}
}

func TestRepoUnreadShowsItsSurface(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	root := write(t, map[string]string{"README.md": "x\n"})
	if err := os.Chmod(filepath.Join(root, "README.md"), 0); err != nil {
		t.Fatal(err)
	}
	code, out, _ := drive("repo", root)
	if code != 2 || !strings.Contains(out, "UNREAD README.md") || !strings.HasPrefix(out, "UNREAD ") {
		t.Errorf("%d %q", code, out)
	}
}

// A FAIL read off part of a repository is UNREAD while a surface could not be
// read: status 2, not 1.
func TestRepoUnreadOutranksFail(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	root := write(t, map[string]string{"README.md": floaty + "Output is bit-identical to the reference.\n", "docs/a.md": "x\n"})
	locked := filepath.Join(root, "docs", "a.md")
	if code, out, _ := drive("repo", root); code != 1 || !strings.HasPrefix(out, "FAIL ") {
		t.Fatalf("control: %d %q", code, out)
	}
	os.Chmod(locked, 0)
	t.Cleanup(func() { os.Chmod(locked, 0o644) })
	if code, out, _ := drive("repo", root); code != 2 || !strings.HasPrefix(out, "UNREAD ") || !strings.Contains(out, "statement README.md:2") {
		t.Errorf("%d %q", code, out)
	}
}

func TestRepoJSON(t *testing.T) {
	fail := write(t, map[string]string{"README.md": floaty + "Output is bit-identical to the reference.\n"})
	code, out, _ := drive("repo", "--json", fail, write(t, map[string]string{"README.md": "x\n"}))
	if code != 1 {
		t.Errorf("status %d", code)
	}
	var reps []parityrepo.Report
	if err := json.Unmarshal([]byte(out), &reps); err != nil || len(reps) != 2 || reps[0].Verdict != parityrepo.Fail || reps[1].Verdict != parityrepo.Pass {
		t.Errorf("%v %q", err, out)
	}
}

func TestTextPassesAndRefuses(t *testing.T) {
	dir := t.TempDir()
	ok := filepath.Join(dir, "ok.md")
	os.WriteFile(ok, []byte("Output unchanged, bit-identical.\n"), 0o644)
	if code, out, _ := drive("text", ok); code != 0 || out != "PASS "+ok+"\n" {
		t.Errorf("%d %q", code, out)
	}
	bad := filepath.Join(dir, "bad.md")
	body := "We do not use FMA in the default kernel because it would break bit-parity.\n"
	os.WriteFile(bad, []byte(body), 0o644)
	code, _, errs := drive("text", bad)
	if code != 1 {
		t.Errorf("status %d", code)
	}
	for _, want := range []string{paritytext.Issue, "porting step", "--parity-waiver", "line 1"} {
		if !strings.Contains(errs, want) {
			t.Errorf("refusal lacks %q:\n%s", want, errs)
		}
	}
	if got, _ := os.ReadFile(bad); string(got) != body {
		t.Errorf("a refusal must not touch the file: %q", got)
	}
}

func TestTextWaiverIsStampedIntoTheFile(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.md")
	os.WriteFile(bad, []byte("We do not use FMA in the default kernel because it would break bit-parity.\n"), 0o644)
	code, out, _ := drive("text", "--parity-waiver", "the reference users need identical bits this release", bad)
	if code != 0 || !strings.HasPrefix(out, "WAIVED ") {
		t.Errorf("%d %q", code, out)
	}
	got, _ := os.ReadFile(bad)
	if !strings.HasSuffix(string(got), paritytext.StampPrefix+"the reference users need identical bits this release\n") {
		t.Errorf("not stamped: %q", got)
	}
}

func TestTextWaiverStampFailureIsUnanswerable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes everything")
	}
	bad := filepath.Join(t.TempDir(), "bad.md")
	os.WriteFile(bad, []byte("We do not use FMA in the default kernel because it would break bit-parity.\n"), 0o444)
	if code, _, errs := drive("text", "--parity-waiver", "why", bad); code != 2 || !strings.Contains(errs, "stamping") {
		t.Errorf("%d %q", code, errs)
	}
}

func TestTextUnanswerable(t *testing.T) {
	dir := t.TempDir()
	if code, _, _ := drive("text", filepath.Join(dir, "absent")); code != 2 {
		t.Errorf("absent: %d", code)
	}
	empty := filepath.Join(dir, "empty")
	os.WriteFile(empty, []byte(" \n"), 0o644)
	if code, _, errs := drive("text", empty); code != 2 || !strings.Contains(errs, "empty") {
		t.Errorf("empty: %d %q", code, errs)
	}
	big := filepath.Join(dir, "big")
	os.WriteFile(big, bytes.Repeat([]byte("a"), paritytext.MaxText+1), 0o644)
	if code, _, errs := drive("text", big); code != 2 || !strings.Contains(errs, "unread text is not a pass") {
		t.Errorf("big: %d %q", code, errs)
	}
	if code, _, _ := drive("text", dir); code != 2 {
		t.Errorf("directory: %d", code)
	}
	if code, _, _ := drive("text"); code == 0 {
		t.Error("no file")
	}
}

func TestHelpDocumentsTheMarker(t *testing.T) {
	code, out, errs := drive("--help")
	if code != 0 {
		t.Fatalf("status %d", code)
	}
	out += errs // cobra's help goes to either writer, depending on its version
	for _, want := range []string{"## Accuracy contract", "Bound:", "Oracle:", "Exact:", "Domain: exact", "TestAccuracyBound", "UNREAD is not PASS", "--parity-waiver", "go-fft/fft#121", "go-fft/fft#116"} {
		if !strings.Contains(out, want) {
			t.Errorf("--help lacks %q", want)
		}
	}
}

func TestRepoPrintsRefusedSentencesWithTheirCue(t *testing.T) {
	root := write(t, map[string]string{
		"README.md":    floaty + "\n" + contract,
		"a_test.go":    "package p\nfunc TestAccuracyBound(t *testing.T) {}\n",
		"docs/perf.md": "| trsm | bit for bit against SciPy's Fortran | **left** |\n",
	})
	code, out, _ := drive("repo", root)
	if code != 1 || !strings.HasPrefix(out, "FAIL ") || !strings.Contains(out, "REFUSED docs/perf.md:1 [left-on-parity]: ") {
		t.Errorf("%d %q", code, out)
	}
}
