package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/elftest"
)

func drive(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

// The shape it was written for: a tree whose binaries name their dependencies.
func TestItReadsTheNeededListOfEveryELF(t *testing.T) {
	dir := t.TempDir()
	elftest.WriteNeeded(t, filepath.Join(dir, "bin/iconv"), "libiconv.so.2", "libc.so.6")
	elftest.WriteNeeded(t, filepath.Join(dir, "lib/libiconv.so.2.7.1"), "libc.so.6")

	code, out, errb := drive(t, dir)
	if code != 0 {
		t.Fatalf("want 0, got %d: %s", code, errb)
	}
	for _, want := range []string{"libiconv.so.2", "libc.so.6", "2 ELF file(s) read"} {
		if !strings.Contains(out, want) {
			t.Errorf("output omits %q:\n%s", want, out)
		}
	}
}

// ⛔ THE CONTROL MUST BE ABLE TO FAIL, or it is decoration. Both directions
// are asserted here, because a control that passes unconditionally would let
// every absence below it read as a finding.
func TestThePositiveControlDecidesWhetherAnAbsenceMeansAnything(t *testing.T) {
	dir := t.TempDir()
	elftest.WriteNeeded(t, filepath.Join(dir, "lib/libstdc++.so.6.0.33"), "libgcc_s.so.1", "libc.so.6")
	elftest.WriteNeeded(t, filepath.Join(dir, "lib/libiconv.so.2.7.1"), "libc.so.6")

	// Present: the run is believable, and says so in as many words.
	code, out, errb := drive(t, "-must", "libgcc_s.so.1", dir)
	if code != 0 {
		t.Fatalf("control is present, want 0, got %d: %s", code, errb)
	}
	if !strings.Contains(out, "an absence above is an absence") {
		t.Errorf("a passing control was not stated:\n%s", out)
	}

	// Absent: the run proves nothing and must refuse to look like evidence.
	// This is the real libiconv case — `libstdc++.so.6` has no consumer in a
	// small closure, so naming it as the control measured the fixture.
	code, _, errb = drive(t, "-must", "libstdc++.so.6", dir)
	if code != 1 {
		t.Fatalf("control is absent, want 1, got %d", code)
	}
	if !strings.Contains(errb, "has not shown it can see one") {
		t.Errorf("a failed control was not explained: %s", errb)
	}
}

// ⛔ ZERO ELF FILES IS A BROKEN MEASUREMENT. A wrong path, an unpacked tree
// that never arrived, a store staged for another platform: each reports the
// same emptiness as "these binaries link nothing", and only one of those is an
// answer.
func TestATreeWithNoELFIsAFailureNotAnEmptyAnswer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("not an ELF\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errb := drive(t, dir)
	if code != 1 {
		t.Fatalf("want 1, got %d", code)
	}
	if !strings.Contains(errb, "broken measurement") {
		t.Errorf("the failure was not explained: %s", errb)
	}
}

// A Mach-O, an archive and a script share the tree with the ELF: none is an
// ELF, and none may be counted or reported.
func TestItIsSilentAboutFilesThatAreNotELF(t *testing.T) {
	dir := t.TempDir()
	elftest.WriteNeeded(t, filepath.Join(dir, "lib/real.so"), "libc.so.6")
	for name, body := range map[string]string{
		"lib/libfoo.a":              "!<arch>\n",
		"lib/libstdc++.so.6-gdb.py": "import gdb\n",
		"lib/libmacho.dylib":        "\xcf\xfa\xed\xfe",
		"share/notes.txt":           "nothing here\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, out, errb := drive(t, dir)
	if code != 0 {
		t.Fatalf("want 0, got %d: %s", code, errb)
	}
	if !strings.Contains(out, "1 ELF file(s) read") {
		t.Errorf("want exactly one ELF counted:\n%s", out)
	}
	if strings.Contains(out, "gdb.py") || strings.Contains(out, "libfoo.a") {
		t.Errorf("a file that is not an ELF was reported:\n%s", out)
	}
}

func TestQuietPrintsTheVerdictWithoutTheList(t *testing.T) {
	dir := t.TempDir()
	elftest.WriteNeeded(t, filepath.Join(dir, "lib/a.so"), "libc.so.6")
	code, out, _ := drive(t, "-quiet", "-must", "libc.so.6", dir)
	if code != 0 {
		t.Fatalf("want 0, got %d", code)
	}
	if strings.Contains(out, "NEEDED") {
		t.Errorf("-quiet printed the list:\n%s", out)
	}
	if !strings.Contains(out, "1 ELF file(s) read") {
		t.Errorf("-quiet dropped the totals:\n%s", out)
	}
}

func TestItRefusesBadInput(t *testing.T) {
	for _, c := range []struct {
		what string
		args []string
	}{
		{"no roots", nil},
		{"an unknown flag", []string{"-nope"}},
	} {
		if code, _, _ := drive(t, c.args...); code != 2 {
			t.Errorf("%s: want 2, got %d", c.what, code)
		}
	}
}

// A root that is not there is an error, not an empty tree.
func TestAMissingRootIsReported(t *testing.T) {
	code, _, errb := drive(t, filepath.Join(t.TempDir(), "absent"))
	if code != 1 {
		t.Fatalf("want 1, got %d: %s", code, errb)
	}
}
