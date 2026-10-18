package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// vulngate is a real command, and the one the peer session measured.
const vulngate = "github.com/go-fleettools/fleettools/cmd/vulngate"

// TestCommitsThatCannotReachThisCommandAreNotCalledStale drives BOTH
// directions, which is the whole point: a change under another command must go
// quiet, and a change that can reach this one must still say STALE.
//
// ⛔ One direction alone would pass on a function that always went quiet --
// which is a worse defect than the vacuous warning this replaces, because it
// takes away a true warning instead of adding a false one. The "still warns"
// rows below are the control, and the last one is the live case: the only thing
// shared between two commands is code neither of them owns.
func TestCommitsThatCannotReachThisCommandAreNotCalledStale(t *testing.T) {
	for _, c := range []struct {
		name  string
		files []string
		quiet bool
	}{
		{
			// The measured case: four commits, v0.18.1..v0.20.1, none of them
			// touching cmd/vulngate or internal/fleet.
			name:  "another command's directory, and a README",
			files: []string{"README.md", "cmd/buildscan/main.go", "cmd/docscan/docscan.go", "cmd/modrename/main.go"},
			quiet: true,
		},
		{
			name:  "docs and CI only",
			files: []string{"docs/index.md", ".github/workflows/ci.yml", ".gitignore", "renovate.json", "LICENSE"},
			quiet: true,
		},
		{
			name:  "shared code: still warns",
			files: []string{"cmd/buildscan/main.go", "internal/fleet/vintage.go"},
			quiet: false,
		},
		{
			name:  "this command's own directory: still warns",
			files: []string{"cmd/vulngate/main.go"},
			quiet: false,
		},
		{
			name:  "a dependency bump: still warns",
			files: []string{"go.mod", "go.sum"},
			quiet: false,
		},
		{
			// cmd/<name> with no directory under it is a file in cmd/ itself,
			// which no path-prefix rule can attribute to another command.
			name:  "a loose file under cmd/: still warns",
			files: []string{"cmd/doc.go"},
			quiet: false,
		},
		{
			name:  "a root package file: still warns",
			files: []string{"fleet.go"},
			quiet: false,
		},
		{
			// A README is inert, but it is not the only thing in the commit.
			name:  "one reaching file among inert ones: still warns",
			files: []string{"README.md", "cmd/docscan/main.go", "internal/gh/gh.go"},
			quiet: false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var b strings.Builder
			warnStale(&b, Vintage{Module: "v0.18.1", Command: vulngate}, func(string) (comparison, error) {
				return comparison{Status: "ahead", AheadBy: len(c.files), Files: filesOf(c.files)}, nil
			})
			got := b.String()
			switch {
			case c.quiet && strings.Contains(got, "STALE"):
				t.Fatalf("those commits cannot reach %s, yet it said: %q", vulngate, got)
			case c.quiet && !strings.Contains(got, "none of them touching"):
				t.Fatalf("want the quieter note, got %q", got)
			case !c.quiet && !strings.Contains(got, "STALE"):
				t.Fatalf("those commits CAN reach %s, so it must warn; said: %q", vulngate, got)
			}
		})
	}
}

// TestAnUnknownCommandIsNotGivenTheQuietAnswer: not knowing which command is
// running is not evidence that the commits miss it.
func TestAnUnknownCommandIsNotGivenTheQuietAnswer(t *testing.T) {
	var b strings.Builder
	warnStale(&b, Vintage{Module: "v0.18.1"}, func(string) (comparison, error) {
		return comparison{Status: "ahead", AheadBy: 1, Files: filesOf([]string{"cmd/buildscan/main.go"})}, nil
	})
	if got := b.String(); !strings.Contains(got, "STALE") {
		t.Fatalf("an empty Command must keep the warning, said: %q", got)
	}
}

// TestATruncatedFileListKeepsTheWarning pins the one way this check could go
// quiet about something that matters.
//
// ⛔ GitHub's compare endpoint returns at most 300 files and does not say that
// it truncated, so "everything here belongs to another command" can be true of
// the 300 it sent and false of the diff. At the cap the answer is unknown, and
// unknown keeps the warning. The 299-file row is the control: it proves the cap
// is what makes the difference, not merely "a long list".
func TestATruncatedFileListKeepsTheWarning(t *testing.T) {
	for _, n := range []int{299, 300} {
		var files []string
		for i := 0; i < n; i++ {
			files = append(files, "cmd/buildscan/f"+itoa(i)+".go")
		}
		var b strings.Builder
		warnStale(&b, Vintage{Module: "v0.18.1", Command: vulngate}, func(string) (comparison, error) {
			return comparison{Status: "ahead", AheadBy: 1, Files: filesOf(files)}, nil
		})
		got := b.String()
		if n == 300 && !strings.Contains(got, "STALE") {
			t.Fatalf("%d files is the cap, so the list may be truncated and the warning must stand; said: %q", n, got)
		}
		if n == 299 && strings.Contains(got, "STALE") {
			t.Fatalf("%d files is a complete list of another command's files; said: %q", n, got)
		}
	}
}

// TestNothingEmbedsWhatIsCalledInert is the other half of inert().
//
// inert() claims a path cannot end up inside any binary this module builds.
// That is true only while nothing embeds it, and `go:embed` is exactly the way
// a markdown file gets into a binary. Without this test the list would be a
// guess that rots the first time somebody embeds a README -- quietly, because
// the only symptom is a warning that stops being printed.
//
// ⛔ It asserts it SAW at least one directive. A walk that reads nothing and
// finds nothing passes, and would have passed just as happily with a glob that
// matched no file at all.
func TestNothingEmbedsWhatIsCalledInert(t *testing.T) {
	root := ".."
	for i := 0; i < 3; i++ {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		root = filepath.Join(root, "..")
	}
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "*.go").Output()
	if err != nil {
		t.Skipf("git ls-files: %v (not a checkout?)", err)
	}

	var directives int
	var bad []string
	for _, name := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if name == "" {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue // tracked but not checked out here
		}
		dir := filepath.ToSlash(filepath.Dir(name))
		for _, line := range strings.Split(string(src), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "//go:embed ") {
				continue
			}
			directives++
			for _, pat := range strings.Fields(strings.TrimPrefix(line, "//go:embed ")) {
				// all: and - are the two prefixes go:embed accepts.
				pat = strings.TrimPrefix(strings.TrimPrefix(pat, "all:"), "-")
				pat = strings.Trim(pat, `"`)
				p := pat
				if dir != "." {
					p = dir + "/" + pat
				}
				if inert(p) {
					bad = append(bad, name+": "+p)
				}
			}
		}
	}
	if directives == 0 {
		t.Fatal("read no go:embed directive at all, so this test asserted nothing -- check the glob")
	}
	if len(bad) > 0 {
		t.Fatalf("inert() says these cannot reach a binary, but they are embedded: %v", bad)
	}
	t.Logf("%d go:embed directive(s) read, none of them naming an inert path", directives)
}

// filesOf is the shape GitHub's compare response has.
func filesOf(names []string) []struct {
	Filename string `json:"filename"`
} {
	var fs []struct {
		Filename string `json:"filename"`
	}
	for _, n := range names {
		fs = append(fs, struct {
			Filename string `json:"filename"`
		}{n})
	}
	return fs
}

// itoa keeps this file free of a strconv import for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
