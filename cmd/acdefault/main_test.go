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

// ⛔⛔ THE SPELLING THAT COST THREE REFUSALS BEFORE IT WAS ADDED. mpdecimal
// 4.0.1 writes "(default is yes)", and this command reported "documents 6
// switch(es) and a stated default for NONE of them" — true of its own reading
// and false of the file. The fixtures are the real lines.
func TestDocumentedReadsTheWordsSpelling(t *testing.T) {
	for _, tc := range []struct{ line, flag, want string }{
		{"  --enable-cxx            enable building libmpdec++ (default is yes)", "enable-cxx", "on"},
		{"  --enable-doc            enable installing the documentation (default is yes)", "enable-doc", "on"},
		{"  --with-thing            a thing (default is no)", "with-thing", "off"},
		{"  --with-other            another [default is off]", "with-other", "off"},
		{"  --with-colon            punctuation (default: yes)", "with-colon", "on"},
	} {
		if got := Documented(tc.line)[tc.flag]; got != tc.want {
			t.Errorf("Documented(%q)[%s] = %q, want %q", tc.line, tc.flag, got, tc.want)
		}
	}
}

// ⛔ AND THE OTHER HALF, WHICH IS WHAT KEEPS THE CENSUS HONEST: a project that
// genuinely states no default must still read as stating none. pcre2 and
// ncurses are both like this, and a pattern loose enough to "find" something
// here would turn their silence into an invented verdict.
func TestADescriptionWithNoDefaultStaysUndocumented(t *testing.T) {
	for _, line := range []string{
		"  --enable-jit            enable Just-In-Time compiling support",
		"  --with-shared           generate shared-libraries",
		"  --enable-pcre2-16       enable 16 bit character support",
		"  --with-gpm              use Alessandro Rubini's GPM library",
	} {
		m := reHelpLine.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("the help line itself was not recognised: %q", line)
		}
		if got := Documented(line)[m[1]]; got != "" {
			t.Errorf("Documented(%q) invented a default %q", line, got)
		}
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

// ⛔⛔ "N examined" ALONE IS THE SHAPE THAT HIDES A SCAN THAT READ NOTHING, and
// both halves of it were measured on real trees: pcre2 10.49 printed "3
// switch(es) examined" while walking past forty whose help states no default,
// and ncurses 6.6 refused outright while documenting thirty-three of them.
func TestTheCensusSaysHowManyItWalkedPast(t *testing.T) {
	dir := t.TempDir()
	// Two switches with a stated default, two without.
	write(t, filepath.Join(dir, "configure"), srcDocs+srcOutput+`
  --with-mystery          no default is stated here
  --enable-enigma         nor here
if test "$with_mystery" = "yes"; then
  :
fi
`)
	var out, errBuf bytes.Buffer
	if got := run(dir, nil, &out, &errBuf); got != 0 {
		t.Fatalf("status = %d, want 0\n%s%s", got, out.String(), errBuf.String())
	}
	if !strings.Contains(out.String(), "2 of 4 documented switch(es) examined") {
		t.Errorf("the census must say N OF M, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "2 switch(es) NOT examined") {
		t.Errorf("it must name what it walked past, got:\n%s", out.String())
	}
}

// ⛔ A tree documenting switches but no defaults must say WHICH fact that is —
// "this project states none", not "nothing is here". ncurses 6.6 is the case.
func TestATreeWithSwitchesAndNoDefaultsSaysSo(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "configure"), `
  --with-shared           generate shared-libraries
  --with-termlib          generate separate terminfo library
if test "$with_shared" = "yes"; then
  :
fi
`)
	var out, errBuf bytes.Buffer
	if got := run(dir, nil, &out, &errBuf); got != 2 {
		t.Fatalf("status = %d, want 2\n%s%s", got, out.String(), errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "documents 2 ") {
		t.Errorf("the refusal must count the switches it DID find, got: %s", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "fact about this project") {
		t.Errorf("the refusal must say it is the project's silence, not a failure here, got: %s", errBuf.String())
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
