package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryCommandReadsItsArguments: a command that never parses its arguments
// treats `-h` as nothing, and nothing as "sweep the whole fleet". On 2026-10-11
// `redscan -h`, run to read its flags, started a sweep of every organisation --
// hundreds of REST calls against the budget Renovate shares -- and had to be
// killed. Every command here must either parse flags (flag.Parse, a FlagSet's
// Parse) or handle "-h" itself, as prmerge does.
//
// It reads the SOURCES rather than running the binaries: running every command
// with -h is exactly how the sweep was started, and a test that launches real
// processes of a tool that launches real processes is a fork bomb waiting to
// happen in this repository.
func TestEveryCommandReadsItsArguments(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("..", "..", "cmd", "*"))
	if err != nil {
		t.Fatal(err)
	}
	var cmds, missing []string
	for _, d := range dirs {
		fi, err := os.Stat(d)
		if err != nil || !fi.IsDir() {
			continue
		}
		srcs, err := filepath.Glob(filepath.Join(d, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		var body strings.Builder
		for _, f := range srcs {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			body.Write(b)
		}
		src := body.String()
		if !strings.Contains(src, "func main()") {
			continue
		}
		cmds = append(cmds, filepath.Base(d))
		// ⛔ A COBRA COMMAND PARSES BY CONSTRUCTION, and this control could
		// not see it: it looks for the `flag` idiom — `.Parse(` — or a
		// hand-rolled "-h", and mkdocs2hextra has neither. cobra's Execute
		// reads the flags, answers -h with the usage text, and refuses the
		// wrong number of arguments before RunE is ever called.
		//
		// The INTENT is what matters — `-h` must not start a sweep — and
		// grepping for one library's spelling tests the library instead. So
		// the check names both idioms. This is the first command here written
		// on cobra; the other 33 are being migrated separately.
		//
		// ⛔ AND IT BROKE THE MOMENT THE IDIOM MOVED. On 2026-10-18 the flag
		// parsing of 17 commands went through [ParseFlags] and three
		// hand-written `-h` comparisons went through [IsHelpRequest], so
		// neither `.Parse(` nor `"-h"` appeared in their sources any more and
		// this control failed for 13 commands it had just been made RIGHT
		// about. A check that names one spelling of the answer fails at the
		// refactor rather than at the defect; the two helpers are named here
		// because the intent -- the arguments are read before anything
		// happens -- is what they guarantee by construction.
		if !strings.Contains(src, ".Parse(") && !strings.Contains(src, `"-h"`) &&
			!strings.Contains(src, "cobra.Command") &&
			!strings.Contains(src, "ParseFlags") && !strings.Contains(src, "IsHelpRequest") {
			missing = append(missing, filepath.Base(d))
		}
	}
	if len(cmds) == 0 {
		t.Fatal("found no commands at all: the glob is wrong, not the commands")
	}
	if len(missing) > 0 {
		t.Fatalf("%d of %d commands never read their arguments, so -h runs them: %v", len(missing), len(cmds), missing)
	}
}

// TestEveryCommandAnswersHelpWithZero is the other half of the question above:
// a command that parses its arguments must also answer `-h` with 0.
//
// ⛔ MEASURED, 2026-10-18: 13 of the 36 commands here answered `-h` with exit
// 2. Live witness, a binary of docscan built from 428e3277548d:
//
//	$ docscan -h; echo $?
//	Usage of docscan: …
//	2
//
// The cause is one the flag package hands you: it reports a help request as an
// error like any other, and the two-line shape every command was written with
// -- `if err := fs.Parse(args); err != nil { return 2 }` -- cannot tell the two
// apart. flag.ExitOnError does tell them apart (os.Exit(0) for ErrHelp), and
// every command here uses flag.ContinueOnError instead so that run() is
// testable, which quietly gave that up. [ParseFlags] is the one place it is
// decided now.
//
// It reads sources for the same reason as its sibling: running every command
// with -h is how a full-fleet sweep got started on 2026-10-11.
//
// ⛔ AND IT IS TEXT, SO IT HAS A BLIND SPOT, named rather than hidden: the
// per-site check knows one spelling of the defect, `.Parse(args); err != nil`.
// It also REFUSES that spelling when the branch under it is correct -- four
// commands here were exactly that, and they were converted rather than
// excused, which is what keeps this a question about one line instead of a
// reader's judgement of the five under it.
// A command that parses `os.Args[1:]` directly, or splits the assignment over
// two statements, goes through. The per-command check behind it is the net:
// anything using flag.ContinueOnError must name ParseFlags or ErrHelp
// somewhere. Two checks, two different holes; neither is a proof, and
// TestParseFlagsSeparatesAQuestionFromAMistake is where the behaviour itself is
// pinned.
func TestEveryCommandAnswersHelpWithZero(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("..", "..", "cmd", "*"))
	if err != nil {
		t.Fatal(err)
	}
	var cmds int
	var broken []string
	for _, d := range dirs {
		fi, err := os.Stat(d)
		if err != nil || !fi.IsDir() {
			continue
		}
		srcs, err := filepath.Glob(filepath.Join(d, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		var body strings.Builder
		for _, f := range srcs {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			body.Write(b)
		}
		src := body.String()
		if !strings.Contains(src, "func main()") {
			continue
		}
		cmds++
		if why := dropsTheHelpDistinction(src); why != "" {
			broken = append(broken, filepath.Base(d)+" ("+why+")")
		}
	}
	if cmds == 0 {
		t.Fatal("found no commands at all: the glob is wrong, not the commands")
	}
	if len(broken) > 0 {
		t.Fatalf("%d of %d commands answer -h with a non-zero status; use fleet.ParseFlags: %v",
			len(broken), cmds, broken)
	}
}

// dropsTheHelpDistinction says why a command's source would answer -h with 2,
// or "" when it would not. Split out so the gate above has a control:
// TestTheHelpGateCanFail.
func dropsTheHelpDistinction(src string) string {
	if strings.Contains(src, ".Parse(args); err != nil") {
		return "`.Parse(args); err != nil` returns the same status for -h as for a typo"
	}
	if strings.Contains(src, "flag.ContinueOnError") &&
		!strings.Contains(src, "ParseFlags") && !strings.Contains(src, "ErrHelp") {
		return "flag.ContinueOnError without ParseFlags or ErrHelp: -h arrives as an error"
	}
	return ""
}

// ⛔ A GATE NOBODY HAS SEEN FAIL IS NOT A GATE. This feeds the predicate the
// shape it is meant to catch and the shapes it must leave alone -- including
// the hand-rolled `-h` of prmerge and the cobra command, which answer with 0
// without any of these words.
func TestTheHelpGateCanFail(t *testing.T) {
	for _, tc := range []struct {
		name  string
		src   string
		wrong bool
	}{
		{"the shape all 13 were written with", "fs := flag.NewFlagSet(\"x\", flag.ContinueOnError)\nif err := fs.Parse(args); err != nil {\n\treturn 2\n}", true},
		{"ContinueOnError and no sign of the distinction", "fs := flag.NewFlagSet(\"x\", flag.ContinueOnError)\nfs.Parse(args)", true},
		{"the helper", "fs := flag.NewFlagSet(\"x\", flag.ContinueOnError)\nif code, ok := fleet.ParseFlags(fs, args); !ok {\n\treturn code\n}", false},
		{"the hand-written switch, as redscan has it", "fs := flag.NewFlagSet(\"x\", flag.ContinueOnError)\nswitch err := fs.Parse(args); {\ncase errors.Is(err, flag.ErrHelp):\n\treturn 0\n}", false},
		{"the global flag set: ExitOnError answers -h with 0 itself", "flag.Parse()", false},
		{"a hand-rolled -h, as prmerge has it", "if os.Args[1] == \"-h\" {\n\tusage()\n\treturn 0\n}", false},
		{"cobra, which answers -h by construction", "c := &cobra.Command{Use: \"x\"}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			why := dropsTheHelpDistinction(tc.src)
			if (why != "") != tc.wrong {
				t.Errorf("dropsTheHelpDistinction = %q, want wrong=%v", why, tc.wrong)
			}
		})
	}
}
