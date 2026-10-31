package main

import (
	"bytes"
	"strings"
	"testing"
)

// ⛔ THIS FILE EXISTS BECAUSE THE COMMAND COULD NOT BE TESTED BEFORE. hostload
// parsed os.Args in place through the package-level flag.Parse() and printed
// with fmt.Printf from inside main, so there was nothing to call: the only way
// to exercise it was to run the binary on a Linux host. The parsing logic it
// shares now lives in internal/procstat, and what remains here -- the flags,
// the exit statuses, the refusal -- had no test at all.
//
// The measurement itself is not asserted: /proc/stat does not exist on every
// host this suite runs on, and a test that needs it would be skipped exactly
// where it matters. What is asserted is everything around it.

// Every flag the help promises must exist, under both spellings a call site may
// use: -i unchanged, and --pick and --max-busy through the compatibility shim.
func TestEveryFlagTheHelpNamesIsWiredUp(t *testing.T) {
	c := RootCmd()
	for _, name := range []string{"interval", "pick", "max-busy"} {
		if c.Flags().Lookup(name) == nil {
			t.Errorf("--%s is named in the help but not registered", name)
		}
	}
	// -i was the flag's whole name before the move; it survives as the
	// shorthand of --interval, so every existing call site reaches it.
	if c.Flags().ShorthandLookup("i") == nil {
		t.Error("-i is not registered; the old spelling must keep working")
	}
}

// -h answers, on stderr, without reading /proc/stat: a command run to see its
// flags must not do its work.
func TestHelpAnswersWithoutMeasuring(t *testing.T) {
	for _, help := range []string{"-h", "--help"} {
		var out, errOut bytes.Buffer
		if code := run([]string{help}, &out, &errOut); code != 0 {
			t.Errorf("%s exited %d, want 0", help, code)
		}
		if out.Len() != 0 {
			t.Errorf("%s wrote %q to stdout; help belongs on stderr, where this "+
				"command's own output does not go", help, out.String())
		}
		if !strings.Contains(errOut.String(), "idlest") {
			t.Errorf("%s: stderr = %q, want the long help", help, errOut.String())
		}
	}
}

// ⛔ An unexpected argument is REFUSED rather than ignored. hostload takes none,
// and a line that passed one meant something the tool cannot do.
func TestAnArgumentIsRefused(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"cpu0"}, &out, &errOut); code != 2 {
		t.Errorf("an argument exited %d, want 2", code)
	}
	if errOut.Len() == 0 {
		t.Error("the refusal said nothing")
	}
}

// The old spelling of a multi-letter flag still reaches it, through the shim,
// and says so once. This is the behaviour every existing call site depends on.
func TestTheOldSingleDashSpellingStillReachesTheFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	// -pick 1 with no /proc/stat fails at the READ (status 2), not at the flag:
	// the distinction is the whole point, so the warning is what is asserted
	// rather than the status.
	run([]string{"-pick", "1"}, &out, &errOut)
	if !strings.Contains(errOut.String(), "-pick now takes two dashes") {
		t.Errorf("stderr = %q, want the shim to say -pick moved", errOut.String())
	}
	if strings.Contains(errOut.String(), "unknown shorthand flag") {
		t.Errorf("stderr = %q: -pick was refused rather than rewritten", errOut.String())
	}
}
