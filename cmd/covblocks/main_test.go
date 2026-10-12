package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// profile writes a coverage profile and returns its path.
func profile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cov.out")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func covblocks(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	rc := run(args, &out, &errb)
	return rc, out.String(), errb.String()
}

const twoCoveredOneNot = `mode: set
example.com/m/a.go:3.10,5.2 2 1
example.com/m/a.go:7.10,9.2 1 0
example.com/m/b.go:3.10,4.2 1 1
`

func TestCovblocksNamesEveryUncoveredBlock(t *testing.T) {
	rc, out, errb := covblocks(t, profile(t, twoCoveredOneNot))
	if rc != 1 {
		t.Fatalf("rc = %d, want 1 — an uncovered block is a failure by default", rc)
	}
	if !strings.Contains(out, "example.com/m/a.go:7.10,9.2 1 stmt(s)") {
		t.Errorf("the block is not named:\n%s", out)
	}
	// The TOTALS always, named, so a run that read nothing cannot be mistaken
	// for one that found nothing.
	if !strings.Contains(out, "3 block(s), 4 statement(s); 1 block(s) uncovered, 1 statement(s) uncovered") {
		t.Errorf("want the totals:\n%s", out)
	}
	// And the per-file count, so the worst file is visible.
	if !strings.Contains(out, "example.com/m/a.go") {
		t.Errorf("want the per-file line:\n%s", out)
	}
	if !strings.Contains(errb, "1 uncovered block(s) > 0 allowed") {
		t.Errorf("stderr = %q", errb)
	}
}

func TestCovblocksPassesWhenEverythingIsCovered(t *testing.T) {
	rc, out, errb := covblocks(t, profile(t, "mode: set\nexample.com/m/a.go:3.10,5.2 2 1\n"))
	if rc != 0 {
		t.Fatalf("rc = %d, stderr = %q", rc, errb)
	}
	// The zero is STATED, not implied by silence.
	if !strings.Contains(out, "0 block(s) uncovered") {
		t.Errorf("want the zero said out loud:\n%s", out)
	}
}

// ⛔ THE SAME BLOCK TWICE. The profile format permits it and Go's own parser
// SUMS the counts — golang.org/x/tools/cover does `Count += b.Count` — so a
// block covered by one entry and not by another is COVERED. A line-by-line
// check would call it uncovered on the strength of the zero.
//
// Measured on three real profiles from this fleet (2592, 1268 and 2592 blocks):
// none repeated a block, so this is a difference in semantics rather than a bug
// anybody is hitting. It is tested because the semantics are the reason this
// parses with x/tools/cover instead of reading lines.
func TestCovblocksSumsARepeatedBlockRatherThanCountingTheZero(t *testing.T) {
	const repeated = `mode: count
example.com/m/a.go:3.10,5.2 2 0
example.com/m/a.go:3.10,5.2 2 1
`
	rc, out, errb := covblocks(t, profile(t, repeated))
	if rc != 0 {
		t.Fatalf("rc = %d — the block is covered by the second entry\nstdout: %s\nstderr: %s", rc, out, errb)
	}
	if !strings.Contains(out, "1 block(s), 2 statement(s); 0 block(s) uncovered") {
		t.Errorf("want one block, covered:\n%s", out)
	}
}

func TestCovblocksMaxTolerates(t *testing.T) {
	p := profile(t, twoCoveredOneNot)
	t.Run("exactly the allowance passes", func(t *testing.T) {
		if rc, _, errb := covblocks(t, "-max", "1", p); rc != 0 {
			t.Errorf("rc = %d, stderr = %q", rc, errb)
		}
	})
	t.Run("one over fails", func(t *testing.T) {
		const two = `mode: set
example.com/m/a.go:3.10,5.2 2 0
example.com/m/a.go:7.10,9.2 1 0
`
		if rc, _, errb := covblocks(t, "-max", "1", profile(t, two)); rc != 1 {
			t.Errorf("rc = %d, stderr = %q", rc, errb)
		}
	})
}

// -q prints the totals and not the blocks, for a lane that wants a number.
func TestCovblocksQuietKeepsTheTotals(t *testing.T) {
	_, out, _ := covblocks(t, "-q", profile(t, twoCoveredOneNot))
	if strings.Contains(out, "a.go:7.10,9.2") {
		t.Errorf("-q listed a block:\n%s", out)
	}
	if !strings.Contains(out, "1 block(s) uncovered") {
		t.Errorf("-q dropped the totals:\n%s", out)
	}
}

// Several profiles, one verdict — a repository whose lanes write one each.
func TestCovblocksAddsUpSeveralProfiles(t *testing.T) {
	a := profile(t, "mode: set\nexample.com/m/a.go:3.10,5.2 2 1\n")
	b := profile(t, "mode: set\nexample.com/m/b.go:3.10,5.2 3 0\n")
	rc, out, _ := covblocks(t, a, b)
	if rc != 1 {
		t.Fatalf("rc = %d", rc)
	}
	if !strings.Contains(out, "2 block(s), 5 statement(s); 1 block(s) uncovered, 3 statement(s) uncovered") {
		t.Errorf("the two profiles were not added up:\n%s", out)
	}
}

func TestCovblocksRefusals(t *testing.T) {
	t.Run("a profile with no blocks", func(t *testing.T) {
		// ⛔ NOT A PASS. It reports the same zero as a profile that covered
		// everything, and this fleet has published a zero that meant "could
		// not read".
		rc, _, errb := covblocks(t, profile(t, "mode: set\n"))
		if rc != 2 {
			t.Fatalf("rc = %d, want 2", rc)
		}
		if !strings.Contains(errb, "nothing was measured") {
			t.Errorf("stderr = %q", errb)
		}
	})
	t.Run("a file that is not there", func(t *testing.T) {
		rc, _, errb := covblocks(t, filepath.Join(t.TempDir(), "nope.out"))
		if rc != 2 || !strings.Contains(errb, "nope.out") {
			t.Errorf("rc = %d, stderr = %q", rc, errb)
		}
	})
	t.Run("a file that is not a profile", func(t *testing.T) {
		rc, _, errb := covblocks(t, profile(t, "hello\n"))
		if rc != 2 || !strings.Contains(errb, "covblocks:") {
			t.Errorf("rc = %d, stderr = %q", rc, errb)
		}
	})
	t.Run("no arguments", func(t *testing.T) {
		rc, _, errb := covblocks(t)
		if rc != 2 || !strings.Contains(errb, "usage: covblocks") {
			t.Errorf("rc = %d, stderr = %q", rc, errb)
		}
	})
	t.Run("a negative allowance", func(t *testing.T) {
		rc, _, errb := covblocks(t, "-max", "-1", profile(t, twoCoveredOneNot))
		if rc != 2 || !strings.Contains(errb, "cannot be negative") {
			t.Errorf("rc = %d, stderr = %q", rc, errb)
		}
	})
	t.Run("an unknown flag", func(t *testing.T) {
		if rc, _, _ := covblocks(t, "-nonesuch"); rc != 2 {
			t.Error("an unknown flag must be a usage error")
		}
	})
	t.Run("asking for help is not a failure", func(t *testing.T) {
		if rc, _, _ := covblocks(t, "-h"); rc != 0 {
			t.Error("-h must exit 0")
		}
	})
}

// Files are ordered by how many uncovered blocks they hold, most first, so the
// worst is the first line a reader sees and two runs print the same thing.
//
// ⛔ THE FILE WITH MORE IS THE ONE THAT SORTS LATER BY NAME. The first version
// of this gave a.go the larger count, where ordering by count and ordering by
// name agree — so deleting the count comparison left the test green, and a
// mutation said so. A test of a tie-break has to put the two keys in conflict.
// This is the third time in one session I have written that test wrongly.
func TestCovblocksOrdersFilesByCountNotByName(t *testing.T) {
	const mixed = `mode: set
example.com/m/a.go:3.10,5.2 1 0
example.com/m/z.go:3.10,5.2 1 0
example.com/m/z.go:7.10,9.2 1 0
`
	_, out, _ := covblocks(t, profile(t, mixed))
	// Only the per-file lines, which are the ones after the totals.
	_, tail, found := strings.Cut(out, "uncovered\n")
	if !found {
		t.Fatalf("no totals line to cut at:\n%s", out)
	}
	ia, iz := strings.Index(tail, "m/a.go"), strings.Index(tail, "m/z.go")
	if ia < 0 || iz < 0 {
		t.Fatalf("both files must be listed:\n%s", tail)
	}
	if iz > ia {
		t.Errorf("z.go (2 uncovered) must precede a.go (1), count before name:\n%s", tail)
	}
}

// And the name is the tie-break when the counts are equal, which is the branch
// a count-only comparison leaves uncovered — and the one that makes the output
// non-deterministic.
func TestCovblocksEqualCountsAreOrderedByName(t *testing.T) {
	const equal = `mode: set
example.com/m/z.go:3.10,5.2 1 0
example.com/m/a.go:3.10,5.2 1 0
`
	_, out, _ := covblocks(t, profile(t, equal))
	_, tail, _ := strings.Cut(out, "uncovered\n")
	if ia, iz := strings.Index(tail, "m/a.go"), strings.Index(tail, "m/z.go"); ia > iz {
		t.Errorf("at one each, a.go must precede z.go:\n%s", tail)
	}
}
