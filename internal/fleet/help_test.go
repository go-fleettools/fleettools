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
		if !strings.Contains(src, ".Parse(") && !strings.Contains(src, `"-h"`) {
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
