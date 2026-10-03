package fleet

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

const headSha = "b832cd19a059b50593d1f10e4a6adb1c1934d57e"

func never(string) (comparison, error) {
	panic("compare must not be called once the stamps already settle it")
}

// TestWarnIfStaleIsSilentOnlyWhenItHasEarnedIt walks every branch, and the
// assertion that matters is the LAST one: silence is reserved for the one case
// that needs no attention. A check that falls silent when it cannot tell is the
// same defect as no check at all.
func TestWarnIfStaleIsSilentOnlyWhenItHasEarnedIt(t *testing.T) {
	for _, c := range []struct {
		name    string
		v       Vintage
		compare func(string) (comparison, error)
		want    string // substring; "" means it must say nothing
	}{
		{
			name:    "no stamp at all",
			v:       Vintage{},
			compare: never,
			want:    "carries no VCS stamp",
		},
		{
			name:    "a modified tree is not reproducible",
			v:       Vintage{Revision: headSha, Modified: true},
			compare: never,
			want:    "MODIFIED tree",
		},
		{
			name:    "cannot reach GitHub",
			v:       Vintage{Revision: headSha},
			compare: func(string) (comparison, error) { return comparison{}, errString("no route to host") },
			want:    "could not tell",
		},
		{
			name:    "behind the default branch",
			v:       Vintage{Revision: "a5d2f90dea2500000000000000000000000000ff"},
			compare: func(string) (comparison, error) { return comparison{Status: "ahead", AheadBy: 7}, nil },
			want:    "STALE -- a5d2f90dea25 is 7 commit(s) behind",
		},
		{
			name:    "on a branch of its own",
			v:       Vintage{Revision: headSha},
			compare: func(string) (comparison, error) { return comparison{Status: "diverged", AheadBy: 3, BehindBy: 2}, nil },
			want:    "not on the default branch",
		},
		{
			name:    "built from the head of a clean tree",
			v:       Vintage{Revision: headSha},
			compare: func(string) (comparison, error) { return comparison{Status: "identical"}, nil },
			want:    "",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var b strings.Builder
			warnStale(&b, c.v, c.compare)
			got := b.String()
			switch {
			case c.want == "" && got != "":
				t.Fatalf("must say nothing, said %q", got)
			case c.want != "" && !strings.Contains(got, c.want):
				t.Fatalf("want a line containing %q, got %q", c.want, got)
			}
		})
	}
}

// TestAheadByZeroIsNotReportedAsStale pins the arithmetic the message depends
// on. "ahead" with AheadBy 0 cannot be called "0 commits behind": that sentence
// is both wrong and reassuring, which is the worst pair.
func TestAheadByZeroIsNotReportedAsStale(t *testing.T) {
	var b strings.Builder
	warnStale(&b, Vintage{Revision: headSha}, func(string) (comparison, error) {
		return comparison{Status: "behind", AheadBy: 0, BehindBy: 4}, nil
	})
	if got := b.String(); strings.Contains(got, "STALE") {
		t.Fatalf("a binary AHEAD of the branch is not stale, said: %q", got)
	}
}

// TestVintageReadsTheStampsGoActuallyWrites pins the two key names.
//
// ⛔ The obvious test -- call BuildVintage and look at the result -- can only
// ever SKIP: the Go toolchain writes no vcs stamps into a `go test` binary.
// Written that way it passed, said nothing, and would have passed just as
// happily with "vcs.rev" misspelt. The names below are the ones `go version
// -m ~/.local/bin/redscan` prints for a real installed binary.
func TestVintageReadsTheStampsGoActuallyWrites(t *testing.T) {
	v := vintageFrom([]debug.BuildSetting{
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: headSha},
		{Key: "vcs.time", Value: "2026-09-29T07:16:20Z"},
		{Key: "vcs.modified", Value: "false"},
	})
	if v.Revision != headSha {
		t.Errorf("revision: got %q, want %q", v.Revision, headSha)
	}
	if v.Modified {
		t.Error(`modified: "false" must not read as modified`)
	}
	if got := vintageFrom([]debug.BuildSetting{{Key: "vcs.modified", Value: "true"}}); !got.Modified {
		t.Error(`modified: "true" must read as modified`)
	}
	// The absence that matters: a binary built outside a checkout.
	if got := vintageFrom([]debug.BuildSetting{{Key: "-buildmode", Value: "exe"}}); got.Revision != "" {
		t.Errorf("no vcs stamp must give an empty revision, got %q", got.Revision)
	}
}

// TestEveryCommandAsksWhetherItIsStale is the guard on the guard.
//
// ⛔ It enumerates cmd/ ON DISK rather than naming the commands, because the
// defect this whole file answers was a correct guard that nothing ran. A list
// written here would have to be remembered; a directory listing cannot be
// forgotten, so a ninth command fails this test until it is wired up.
func TestEveryCommandAsksWhetherItIsStale(t *testing.T) {
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
		if !strings.Contains(body.String(), "func main()") {
			continue // not a command
		}
		cmds = append(cmds, filepath.Base(d))
		if !strings.Contains(body.String(), "fleet.WarnIfStale(") {
			missing = append(missing, filepath.Base(d))
		}
	}
	// A sweep that could not read reports zero: prove this one read something.
	if len(cmds) < 2 {
		t.Fatalf("found %d commands under cmd/, so this test is measuring itself", len(cmds))
	}
	if len(missing) > 0 {
		t.Fatalf("%d of %d commands never ask whether they are stale: %v\n"+
			"Add fleet.WarnIfStale(os.Stderr) as the first thing main() does.",
			len(missing), len(cmds), missing)
	}
	t.Logf("%d commands, all wired: %v", len(cmds), cmds)
}

type errString string

func (e errString) Error() string { return string(e) }
