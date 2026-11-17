package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The four shapes a generated configure can take for one switch. Each is a
// WITNESS: the accepting rows matter as much as the refusing one, because a
// check that only ever says "wrong" is a check nobody can trust.
const (
	// --with-python in libxml2 2.15.4: documented (on), assigned only inside
	// --with-minimum, compared only against "yes" — so empty, so OFF.
	srcPython = `
  --with-python           Python bindings (on)
if test "$with_minimum" = "yes"; then
    test "$with_python" = "" && with_python=no
fi
if test ${with_python+y}
then :
  withval=$with_python;
fi
 if test "x$with_python" = "xyes"; then
   :
 fi
if test "$with_python" = "yes" || test "$with_docs" = "yes"; then
  as_fn_error $? "cannot find doxygen" "$LINENO" 5
fi
`
	// --with-output: documented (on) and really on, because the ONLY comparison
	// is against "no". Empty means ON here — in the same file as the one above.
	srcOutput = `
  --with-output           serialization support (on)
if test "$with_output" = "no"; then
    with_c14n=no
fi
`
	// --with-docs: documented (off) and off. Agreement, nothing to report.
	srcDocs = `
  --with-docs             Build documentation (off)
if test "$with_docs" = "yes" ; then
    :
fi
`
	// --enable-cet: an unconditional assignment states the default outright.
	srcAssigned = `
  --enable-cet            enable CET [default=no]
enable_cet=no
if test "$enable_cet" = "yes"; then
  :
fi
`
	// Compared against both: the case this tool must NOT guess at.
	srcBoth = `
  --with-threads          threads support (on)
if test "$with_threads" = "yes"; then
  :
fi
if test "$with_threads" = "no"; then
  :
fi
`
)

func TestExamine(t *testing.T) {
	for _, tc := range []struct {
		name      string
		src       string
		flag      string
		effective string
		agrees    bool
	}{
		{"python documented on but really off", srcPython, "with-python", "off", false},
		{"output empty means ON", srcOutput, "with-output", "on", true},
		{"docs off and off", srcDocs, "with-docs", "off", true},
		{"an unconditional assignment states it", srcAssigned, "enable-cet", "off", true},
		{"compared against both is undetermined", srcBoth, "with-threads", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := Examine(tc.src, tc.flag)
			v.Documented = Documented(tc.src)[tc.flag]
			if v.Effective != tc.effective {
				t.Errorf("effective = %q, want %q (why: %s)", v.Effective, tc.effective, v.Why)
			}
			if got := v.Agrees(); got != tc.agrees {
				t.Errorf("Agrees() = %v, want %v (documented %q, effective %q)",
					got, tc.agrees, v.Documented, v.Effective)
			}
			if v.Why == "" {
				t.Error("Why is empty: a verdict must name what decided it")
			}
		})
	}
}

// ⛔ The mutation that matters: if the tool read the HELP STRING rather than the
// code — the mistake it exists to refuse — python would come back "on" and the
// row would turn green. Changing only the help line must change nothing.
func TestVerdictIgnoresTheHelpString(t *testing.T) {
	stale := Examine(srcPython, "with-python")
	fresh := Examine(strings.Replace(srcPython, "(on)", "(off)", 1), "with-python")
	if stale.Effective != fresh.Effective || stale.Effective != "off" {
		t.Fatalf("the help string moved the verdict: %q vs %q; both must be \"off\"",
			stale.Effective, fresh.Effective)
	}
}

func TestDocumented(t *testing.T) {
	for _, tc := range []struct{ src, flag, want string }{
		{srcPython, "with-python", "on"},
		{srcDocs, "with-docs", "off"},
		{srcAssigned, "enable-cet", "off"},
		{"  --with-mystery         no default is stated here", "with-mystery", ""},
	} {
		if got := Documented(tc.src)[tc.flag]; got != tc.want {
			t.Errorf("Documented(%s)[%s] = %q, want %q", tc.flag, tc.flag, got, tc.want)
		}
	}
}

func TestVarName(t *testing.T) {
	for in, want := range map[string]string{
		"with-python":    "with_python",
		"without-python": "with_python",
		"--without-docs": "with_docs",
		"disable-cet":    "enable_cet",
		"enable-shared":  "enable_shared",
	} {
		if got := VarName(in); got != want {
			t.Errorf("VarName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "configure"), srcPython+srcOutput+srcDocs)

	var out, errBuf bytes.Buffer
	if got := run(dir, []string{"with-docs", "with-output"}, &out, &errBuf); got != 0 {
		t.Fatalf("status = %d, want 0 for two agreeing switches\n%s%s", got, out.String(), errBuf.String())
	}
	out.Reset()
	if got := run(dir, []string{"with-python"}, &out, &errBuf); got != 1 {
		t.Fatalf("status = %d, want 1 for the switch whose help lies\n%s", got, out.String())
	}
	if !strings.Contains(out.String(), "documented on") || !strings.Contains(out.String(), "actual off") {
		t.Errorf("the report must name both readings, got:\n%s", out.String())
	}
}

// ⛔ A run that could not read must never look like a clean one.
func TestRunRefusesWhatItCannotRead(t *testing.T) {
	var out, errBuf bytes.Buffer
	if got := run(t.TempDir(), nil, &out, &errBuf); got != 2 {
		t.Errorf("status = %d, want 2 for a directory with no generated configure", got)
	}

	dir := t.TempDir()
	write(t, filepath.Join(dir, "configure"), srcPython)
	out.Reset()
	errBuf.Reset()
	if got := run(dir, []string{"with-nonesuch"}, &out, &errBuf); got != 2 {
		t.Errorf("status = %d, want 2 for a flag the help text never documents", got)
	}
	if !strings.Contains(errBuf.String(), "with-nonesuch") {
		t.Errorf("the refusal must name the flag, got: %s", errBuf.String())
	}

	// A tree whose configure documents no switch at all: 2, not 0.
	bare := t.TempDir()
	write(t, filepath.Join(bare, "configure"), "#! /bin/sh\necho hello\n")
	out.Reset()
	errBuf.Reset()
	if got := run(bare, nil, &out, &errBuf); got != 2 {
		t.Errorf("status = %d, want 2 when nothing was examined", got)
	}
}

// configure.ac's help lines win, because they are the ones a person quotes —
// and in libxml2 they were the stale half.
func TestConfigureAcHelpWins(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "configure"), strings.Replace(srcPython, "(on)", "(off)", 1))
	write(t, filepath.Join(dir, "configure.ac"), "  --with-python           Python bindings (on)\n")

	var out, errBuf bytes.Buffer
	if got := run(dir, []string{"with-python"}, &out, &errBuf); got != 1 {
		t.Fatalf("status = %d, want 1: configure.ac still claims (on)\n%s", got, out.String())
	}
}

func TestRootCmd(t *testing.T) {
	c := RootCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs([]string{"--help"})
	if err := c.Execute(); err != nil {
		t.Fatalf("--help: %v", err)
	}
	for _, want := range []string{"generated", "UNDETERMINED", "Exit status"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("--help does not mention %q", want)
		}
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
