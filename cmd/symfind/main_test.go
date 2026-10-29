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

// ⛔ THE DISTINCTION THE WHOLE TOOL IS FOR. `provided` is what the file gives
// other libraries; `consumed` is what it asks of them. glibc DEFINES gettext,
// which is why nothing links -lintl on linux. A library that merely CALLS
// gettext defines nothing, and treating the two alike would turn "glibc
// provides this" into "something here mentions this".
func TestItReportsWhatIsDefinedAndNotWhatIsMerelyCalled(t *testing.T) {
	p := filepath.Join(t.TempDir(), "libc.so.6")
	elftest.WriteExports(t,
		p,
		[]string{"printf", "gettext", "dcgettext", "ngettext"}, // defined
		"malloc_hook_absent", // undefined: consumed, not provided
	)

	code, out, errb := drive(t, "-must", "printf", p, "gettext", "dcgettext", "ngettext", "malloc_hook_absent", "iconv_open")
	if code != 0 {
		t.Fatalf("want 0, got %d: %s", code, errb)
	}
	for _, want := range []string{
		"gettext                  true",
		"dcgettext                true",
		"ngettext                 true",
		"malloc_hook_absent       false", // present in .dynsym, but UNDEF
		"iconv_open               false", // not there at all
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output omits %q:\n%s", want, out)
		}
	}
}

// ⛔ BOTH DIRECTIONS OF THE CONTROL, because one that cannot fail is
// decoration and every `false` beneath it would be unearned.
func TestThePositiveControlDecidesWhetherAFalseMeansAnything(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lib.so")
	elftest.WriteExports(t, p, []string{"printf"})

	code, out, _ := drive(t, "-must", "printf", p, "gettext")
	if code != 0 {
		t.Fatalf("control present, want 0, got %d", code)
	}
	if !strings.Contains(out, "an absence below is an absence") {
		t.Errorf("a passing control was not stated:\n%s", out)
	}

	code, _, errb := drive(t, "-must", "nosuchsymbol", p, "gettext")
	if code != 1 {
		t.Fatalf("control absent, want 1, got %d", code)
	}
	if !strings.Contains(errb, "means nothing") {
		t.Errorf("a failed control was not explained: %s", errb)
	}
}

// A stripped object, or the wrong file entirely: reporting `false` for every
// symbol would be an answer, and the wrong one.
func TestAFileThatExportsNothingIsAFailedRead(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty.so")
	elftest.WriteExports(t, p, nil)
	code, _, errb := drive(t, p, "gettext")
	if code != 1 {
		t.Fatalf("want 1, got %d", code)
	}
	if !strings.Contains(errb, "broken read") {
		t.Errorf("the failure was not explained: %s", errb)
	}
}

// A versioned symbol is the ordinary spelling in glibc: the caller asks for
// `gettext` and must not have to know about `@@GLIBC_2.2.5`.
func TestAVersionedSymbolAnswersToItsBareName(t *testing.T) {
	p := filepath.Join(t.TempDir(), "libc.so.6")
	elftest.WriteExports(t, p, []string{"gettext@@GLIBC_2.2.5", "printf@@GLIBC_2.2.5"})
	code, out, errb := drive(t, "-must", "printf", p, "gettext")
	if code != 0 {
		t.Fatalf("want 0, got %d: %s", code, errb)
	}
	if !strings.Contains(out, "gettext                  true") {
		t.Errorf("a versioned symbol did not answer to its bare name:\n%s", out)
	}
}

func TestItRefusesBadInput(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lib.so")
	elftest.WriteExports(t, p, []string{"printf"})
	for _, c := range []struct {
		what string
		args []string
	}{
		{"no arguments", nil},
		{"a file but no symbol", []string{p}},
		{"an unknown flag", []string{"-nope", p, "x"}},
	} {
		if code, _, _ := drive(t, c.args...); code != 2 {
			t.Errorf("%s: want 2, got %d", c.what, code)
		}
	}
}

func TestAFileThatIsNotAnELFIsReported(t *testing.T) {
	p := filepath.Join(t.TempDir(), "notelf.so")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := drive(t, p, "gettext"); code != 1 {
		t.Fatalf("want 1, got %d", code)
	}
}
