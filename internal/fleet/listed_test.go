package fleet_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every command under cmd/ has to be named in the README. #50 landed because a
// script added by #49 was not, and nothing failed -- a README that is only
// mostly complete is read as complete.
//
// It enumerates cmd/ ON DISK rather than naming the commands, for the reason
// the staleness gate gives: a list written in a test has to be remembered, a
// directory does not.
func TestTheReadmeNamesEveryCommand(t *testing.T) {
	root := ".."
	for i := 0; i < 3; i++ {
		if _, err := os.Stat(filepath.Join(root, "README.md")); err == nil {
			break
		}
		root = filepath.Join(root, "..")
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(readme)

	dirs, err := filepath.Glob(filepath.Join(root, "cmd", "*"))
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
		if !strings.Contains(body.String(), "func main()") {
			continue // not a command
		}
		name := filepath.Base(d)
		cmds = append(cmds, name)
		if !strings.Contains(text, "`cmd/"+name+"`") {
			missing = append(missing, name)
		}
	}

	// A sweep that could not read reports zero: prove this one read something.
	if len(cmds) < 2 {
		t.Fatalf("found %d commands under cmd/, so this test is measuring itself", len(cmds))
	}
	if len(missing) > 0 {
		t.Fatalf("%d of %d commands are not named in README.md: %v\n"+
			"Add a `| `+\"`cmd/<name>`\"+` | … |` row saying what it is for and what it measured.",
			len(missing), len(cmds), missing)
	}
	t.Logf("%d commands, all listed: %v", len(cmds), cmds)
}
