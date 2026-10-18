package fleet

import (
	"flag"
	"io"
	"strings"
	"testing"
)

// TestParseFlagsSeparatesAQuestionFromAMistake drives all three outcomes,
// because the one that was wrong everywhere -- the help request -- is also the
// one no other test ever reaches.
func TestParseFlagsSeparatesAQuestionFromAMistake(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code int
		ok   bool
	}{
		{"a help request is answered, not refused", []string{"-h"}, 0, false},
		{"--help likewise", []string{"--help"}, 0, false},
		{"an unknown flag IS a usage error", []string{"-nope"}, 2, false},
		{"a bad value for a known flag too", []string{"-workers", "lots"}, 2, false},
		{"nothing to complain about", []string{"-workers", "3", "dir"}, 0, true},
		{"no arguments at all", nil, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("x", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			fs.Int("workers", 6, "concurrent organisations")
			code, ok := ParseFlags(fs, tc.args)
			if code != tc.code || ok != tc.ok {
				t.Errorf("ParseFlags(%q) = (%d, %v), want (%d, %v)", tc.args, code, ok, tc.code, tc.ok)
			}
		})
	}
}

// ⛔ EACH OF THE THREE COMMANDS THAT ASKED THIS BY HAND KNEW A DIFFERENT THREE
// OF THE FOUR SPELLINGS: prmerge missed `-help`, pagespublish missed `help`,
// blobpurge asked nothing and answered `-h` with 2. The negative half matters
// as much: `-help-me`, and a repository actually called `help`, are not
// requests for help.
func TestIsHelpRequestKnowsAllFourSpellings(t *testing.T) {
	for _, arg := range []string{"-h", "-help", "--help", "help"} {
		if !IsHelpRequest(arg) {
			t.Errorf("IsHelpRequest(%q) = false, want true", arg)
		}
	}
	for _, arg := range []string{"", "h", "-H", "--Help", "helpme", "-help-me", "scan", "go-helpers/help"} {
		if IsHelpRequest(arg) {
			t.Errorf("IsHelpRequest(%q) = true, want false", arg)
		}
	}
}

// ⭐ The help TEXT still has to come out, or a command would answer -h with
// silence and a zero status, which is worse than the 2 this replaces: a tool
// that looks like it worked and said nothing.
func TestAHelpRequestStillPrintsTheFlags(t *testing.T) {
	var out strings.Builder
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	fs.SetOutput(&out)
	fs.Int("workers", 6, "concurrent organisations")
	if code, ok := ParseFlags(fs, []string{"-h"}); code != 0 || ok {
		t.Fatalf("ParseFlags = (%d, %v), want (0, false)", code, ok)
	}
	if !strings.Contains(out.String(), "workers") {
		t.Errorf("the usage text does not name the flag:\n%s", out.String())
	}
}
