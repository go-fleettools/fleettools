package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// probe is a command with the shapes that actually occur in this repository:
// a long flag of two or more letters, a one-letter shorthand, and two
// shorthands whose letters spell a third flag's name.
func probe() *cobra.Command {
	c := New("probe", "a probe", "")
	var (
		maxAge time.Duration
		goVer  string
		jobs   int
		a, b   bool
		ab     string
	)
	f := c.Flags()
	f.DurationVar(&maxAge, "max-db-age", 21*24*time.Hour, "")
	f.StringVar(&goVer, "go", "", "")
	f.IntVarP(&jobs, "jobs", "j", 2, "")
	f.BoolVarP(&a, "alpha", "a", false, "")
	f.BoolVarP(&b, "beta", "b", false, "")
	f.StringVar(&ab, "ab", "", "")
	c.RunE = func(*cobra.Command, []string) error { return nil }
	return c
}

// The planted positive: the spelling 13 workflow lanes use must keep working.
func TestTheFleetsOwnSpellingKeepsWorking(t *testing.T) {
	for _, tc := range []struct {
		in       []string
		want     []string
		saysWhat string // the EXACT rendering the warning must contain
	}{
		{[]string{"-go", "go1.27.2", "f.json"}, []string{"--go", "go1.27.2", "f.json"}, "-go now takes two dashes; read as --go."},
		{[]string{"-go=go1.27.2"}, []string{"--go=go1.27.2"}, "read as --go."},
		{[]string{"-max-db-age", "48h"}, []string{"--max-db-age", "48h"}, "-max-db-age now takes two dashes; read as --max-db-age."},
	} {
		var warn bytes.Buffer
		got, err := Compat(probe(), tc.in, &warn)
		if err != nil {
			t.Fatalf("Compat(%v) = %v", tc.in, err)
		}
		if strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("Compat(%v) = %v, want %v", tc.in, got, tc.want)
		}
		// It must SAY it rewrote something, and say it in the spelling a reader
		// is meant to copy. Asserting only the phrase "two dashes" let a
		// message reading "read as ---max-busy" pass: the token already carried
		// its dash and the renderer prepended two more.
		if !strings.Contains(warn.String(), tc.saysWhat) {
			t.Errorf("Compat(%v) warned:\n  %q\nwant it to contain:\n  %q",
				tc.in, warn.String(), tc.saysWhat)
		}
		if strings.Contains(warn.String(), "---") {
			t.Errorf("Compat(%v) warned %q — three dashes is not a spelling", tc.in, warn.String())
		}
	}
}

// The other half, in three directions: what must NOT be touched.
func TestCompatLeavesAloneWhatIsNotItsBusiness(t *testing.T) {
	for _, in := range [][]string{
		{"--go", "go1.27.2"},        // already correct
		{"-j", "4"},                 // a real one-letter shorthand
		{"-unknown", "x"},           // not ours: pflag must produce its own error
		{"f.json"},                  // a plain argument
		{"-"},                       // stdin
		{"-5"},                      // a negative number
		{"--", "-go", "not-a-flag"}, // after the terminator everything is an argument
	} {
		var warn bytes.Buffer
		got, err := Compat(probe(), in, &warn)
		if err != nil {
			t.Fatalf("Compat(%v) = %v", in, err)
		}
		if strings.Join(got, " ") != strings.Join(in, " ") {
			t.Errorf("Compat(%v) = %v, want it unchanged", in, got)
		}
		if warn.Len() != 0 {
			t.Errorf("Compat(%v) warned %q, want silence", in, warn.String())
		}
	}

	// And the control that the untouched unknown flag still FAILS, rather than
	// being quietly accepted: leaving it alone is only right if pflag rejects it.
	c := probe()
	args, err := Compat(c, []string{"-unknown"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	c.SetArgs(args)
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	if err := c.Execute(); err == nil {
		t.Error("-unknown was accepted; the shim must not widen what the parser allows")
	}
}

// The ambiguity is REFUSED, not guessed: pflag reads -ab as the cluster -a -b
// and the standard flag package read it as the long flag ab. Guessing would
// silently change what a lane means.
func TestAnAmbiguousClusterIsRefusedRatherThanGuessed(t *testing.T) {
	_, err := Compat(probe(), []string{"-ab", "x"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("-ab was resolved silently; both readings are live")
	}
	msg := err.Error()
	for _, want := range []string{"--ab", "-a -b", "ambiguous"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not name %q — a refusal has to say both readings", msg, want)
		}
	}
	// And it carries status 2: the question could not be put, which is not the
	// same as an answer of "nothing found".
	var e *Exit
	if !asExit(err, &e) || e.Code != 2 {
		t.Errorf("status = %v, want 2", err)
	}
}

func TestTheThreeStatusesStayApart(t *testing.T) {
	if got := statusOf(Found("something")); got != 1 {
		t.Errorf("Found => %d, want 1", got)
	}
	if got := statusOf(Unanswerable("could not look")); got != 2 {
		t.Errorf("Unanswerable => %d, want 2", got)
	}
	if got := statusOf(Wrap(errTest)); got != 2 {
		t.Errorf("Wrap => %d, want 2", got)
	}
	// Wrap must not relabel a status that is already chosen: a finding wrapped
	// on its way out would be reported as a failure to measure.
	if got := statusOf(Wrap(Found("something"))); got != 1 {
		t.Errorf("Wrap(Found) => %d, want 1 — Wrap must not relabel", got)
	}
	if Wrap(nil) != nil {
		t.Error("Wrap(nil) must stay nil")
	}
}

var errTest = errTestType{}

type errTestType struct{}

func (errTestType) Error() string { return "a plain error" }

func statusOf(err error) int {
	var e *Exit
	if asExit(err, &e) {
		return e.Code
	}
	return -1
}

func asExit(err error, target **Exit) bool {
	e, ok := err.(*Exit)
	if ok {
		*target = e
	}
	return ok
}
