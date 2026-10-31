package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/spf13/cobra"
)

// The classifier is what every count here rests on, so it is tested against one
// planted source per idiom — including the one that matters, UNREAD, which is
// the state a command was in on 2026-10-11 when `-h` started a fleet sweep.
func TestEachIdiomIsRecognisedAndUnreadIsNotMistakenForOne(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want Idiom
	}{
		{"cobra", `func main() { cli.Run(RootCmd()) }
func RootCmd() *cobra.Command { return &cobra.Command{Use: "x"} }`, Cobra},
		{"flag through the helper", `fs := flag.NewFlagSet("x", flag.ContinueOnError)
if code, ok := fleet.ParseFlags(fs, args); !ok { return code }`, FlagHelped},
		{"flag with an ErrHelp branch", `switch err := fs.Parse(args); {
case errors.Is(err, flag.ErrHelp): return 0
}`, FlagHelped},
		{"a FlagSet with neither helper nor ErrHelp", `fs := flag.NewFlagSet("x", flag.ContinueOnError)
if err := fs.Parse(args); err != nil { return 2 }`, FlagRaw},
		// ⛔ NOT the same defect: the global set is ExitOnError and answers -h
		// with 0 itself. A first version of the classifier called both "flag"
		// and said "-h arrives as an error", which was false for six commands.
		{"the global flag set", `func main() {
	n := flag.Int("max", 0, "")
	flag.Parse()
	_ = n
}`, FlagGlobal},
		{"a hand-rolled -h", `if os.Args[1] == "-h" { usage(); return 0 }`, HandRolled},
		{"nothing at all", `func main() { sweepEveryOrganisation() }`, Unread},
	} {
		if got := classify(tc.src); got != tc.want {
			t.Errorf("%s: classify = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// ⛔ cobra wins over a leftover `flag` import, because what decides how a
// command BEHAVES is the one its main() hands argv to. A command mid-migration
// holds both for an edit or two.
func TestCobraWinsOverALeftoverFlagImport(t *testing.T) {
	const both = `import ("flag"; "github.com/spf13/cobra")
func main() { cli.Run(RootCmd()) }
func RootCmd() *cobra.Command { return &cobra.Command{Use: "x"} }
func old(args []string) { fs := flag.NewFlagSet("x", flag.ContinueOnError); fs.Parse(args) }`
	if got := classify(both); got != Cobra {
		t.Errorf("classify = %q, want %q: the idiom is whichever one reads argv", got, Cobra)
	}
}

// The flag inventory has to be right about three things a migration copies by
// hand: the name, the kind, and the default. The *Var forms put the destination
// first, which shifts every other argument by one.
func TestTheInventoryReadsNameKindAndDefaultThroughBothCallShapes(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main.go"), `package main

import "flag"

func main() {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	n := fs.Int("max", 7, "how many to tolerate")
	var org string
	fs.StringVar(&org, "org", "", "the organisation")
	q := fs.Bool("q", false, "quiet")
	_ = fs.Parse(nil)
	_, _, _ = n, org, q
}
`)
	cmd, ok, err := readCommand(dir, "probe")
	if err != nil || !ok {
		t.Fatalf("readCommand = %v, ok=%v", err, ok)
	}
	byName := map[string]Flag{}
	for _, f := range cmd.Flags {
		byName[f.Name] = f
	}
	if len(cmd.Flags) != 3 {
		t.Fatalf("found %d flags, want 3: %+v", len(cmd.Flags), cmd.Flags)
	}
	if got := byName["max"]; got.Kind != "Int" || got.Default != "7" {
		t.Errorf("max = %+v, want Int with default 7", got)
	}
	if got := byName["org"]; got.Kind != "StringVar" || got.Usage != `"the organisation"` {
		t.Errorf("org = %+v, want StringVar whose usage survived the destination argument", got)
	}
	// One letter keeps working with a single dash; two or more do not.
	if byName["q"].NeedsTwoDashes() {
		t.Error("-q was reported as needing two dashes")
	}
	if !byName["max"].NeedsTwoDashes() {
		t.Error("-max was not reported as needing two dashes")
	}
	if cmd.Breaking() != 2 {
		t.Errorf("Breaking = %d, want 2 (max and org)", cmd.Breaking())
	}
}

// ⛔ The false positive that made a first version of this count 149 flags where
// there were 113: the receiver is not type-checked, so every `x.String()` in the
// command looked like a registration. A method call with neither a name nor a
// usage string is not one.
func TestAMethodCallOnAnUnrelatedValueIsNotAFlag(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main.go"), `package main

import "errors"

type thing struct{}

func (thing) String() string { return "" }

func main() {
	var t thing
	e := errors.New("x")
	_ = t.String()
	_ = e.Error()
}
`)
	cmd, ok, err := readCommand(dir, "probe")
	if err != nil || !ok {
		t.Fatalf("readCommand = %v, ok=%v", err, ok)
	}
	if len(cmd.Flags) != 0 {
		t.Errorf("found %d flags in a command that registers none: %+v", len(cmd.Flags), cmd.Flags)
	}
}

// A directory under cmd/ with no main() is not a command, and a census that
// counted it would report a package with no flags as a command with none.
func TestADirectoryWithoutAMainIsNotACommand(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	helper := filepath.Join(root, "helper")
	mkdir(t, real)
	mkdir(t, helper)
	write(t, filepath.Join(real, "main.go"), "package main\n\nfunc main() {}\n")
	write(t, filepath.Join(helper, "lib.go"), "package helper\n\nfunc F() {}\n")

	cmds, census, err := Read(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if census.Dirs != 2 {
		t.Errorf("Dirs = %d, want 2", census.Dirs)
	}
	if got := sortedNames(cmds); len(got) != 1 || got[0] != "real" {
		t.Errorf("commands = %v, want [real]", got)
	}
}

// An empty tree must REFUSE rather than report a clean repository: zero UNREAD
// out of zero commands is the best possible value in its column.
func TestAnEmptyTreeIsRefusedRatherThanCalledClean(t *testing.T) {
	var out bytes.Buffer
	err := report(&out, t.TempDir(), "", false)
	if err == nil {
		t.Fatal("an empty tree was reported as a census")
	}
	if !strings.Contains(err.Error(), "nothing was measured") {
		t.Errorf("error = %v, want it to say nothing was measured", err)
	}
}

// The three outcomes stay apart at the command boundary: 1 is "I looked and a
// command is UNREAD", 2 is "I could not look".
func TestTheCommandKeepsItsThreeOutcomesApart(t *testing.T) {
	clean := t.TempDir()
	mkdir(t, filepath.Join(clean, "ok"))
	write(t, filepath.Join(clean, "ok", "main.go"),
		"package main\n\nimport \"github.com/spf13/cobra\"\n\nfunc main() { _ = &cobra.Command{} }\n")
	if got := runCmd(t, "--cmd-dir", clean); got != 0 {
		t.Errorf("a cobra-only tree exited %d, want 0", got)
	}

	unread := t.TempDir()
	mkdir(t, filepath.Join(unread, "careless"))
	write(t, filepath.Join(unread, "careless", "main.go"),
		"package main\n\nfunc main() { sweep() }\nfunc sweep() {}\n")
	if got := runCmd(t, "--cmd-dir", unread); got != 1 {
		t.Errorf("an UNREAD command exited %d, want 1", got)
	}

	if got := runCmd(t, "--cmd-dir", filepath.Join(unread, "nope")); got != 2 {
		t.Errorf("a missing directory exited %d, want 2", got)
	}
}

// mains must print the function a migration rewrites, and SAY SO when the
// command's entry points are named something else rather than coming back empty.
func TestMainsPrintsTheEntryPointAndNamesWhatItDidNotFind(t *testing.T) {
	dir := t.TempDir()
	mkdir(t, filepath.Join(dir, "probe"))
	write(t, filepath.Join(dir, "probe", "main.go"),
		"package main\n\nfunc main() { helper() }\n\nfunc helper() {}\n")

	var out bytes.Buffer
	if err := Funcs(&out, filepath.Join(dir, "probe"), "probe", map[string]bool{"main": true}); err != nil {
		t.Fatal(err)
	}
	// The body, not the exact spacing: go/printer puts a tab between the
	// signature and the brace, and asserting that would test go/printer.
	for _, want := range []string{"func main()", "helper()", "main.go:3"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("mains printed %q, want it to contain %q", out.String(), want)
		}
	}

	out.Reset()
	if err := Funcs(&out, filepath.Join(dir, "probe"), "probe", map[string]bool{"absent": true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no function named [absent]") {
		t.Errorf("mains printed %q; an empty answer reads as nothing to migrate", out.String())
	}
}

func runCmd(t *testing.T, args ...string) int {
	t.Helper()
	var out, errOut bytes.Buffer
	return cli.Drive(RootCmd(), args, &out, &errOut)
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

var _ = cobra.Command{}
