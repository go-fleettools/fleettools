package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/priorledger"
)

// TestMain keeps every test of this package off the user's real ledger: the
// command records each run, and a test run of `priorart --repo o/r` must not
// tell the next `ghissue --new` that o/r was read.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "priorart-ledger")
	if err != nil {
		panic(err)
	}
	priorledger.Path = func() (string, error) { return filepath.Join(dir, "priorart.jsonl"), nil }
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestRecordWritesTheVerdict(t *testing.T) {
	p := filepath.Join(t.TempDir(), "priorart.jsonl")
	old := priorledger.Path
	t.Cleanup(func() { priorledger.Path = old })
	priorledger.Path = func() (string, error) { return p, nil }
	latest := func(repo string) priorledger.Record {
		t.Helper()
		r, err := priorledger.Latest(repo, time.Hour, time.Now())
		if err != nil {
			t.Fatalf("%s: %v", repo, err)
		}
		return r
	}
	var warn bytes.Buffer

	record(&warn, "s0", []string{"o/none"}, "", false, nil)
	record(&warn, "s1", []string{"o/found"}, "", false, cli.Found("found"))
	record(&warn, "s2", []string{"o/gap"}, "", false, cli.Unanswerable("unread"))
	record(&warn, "s3", []string{"o/other"}, "", false, errors.New("plain failure"))
	record(&warn, "s4", []string{"o/report"}, "", true, nil) // --report exits 0 "even so"
	record(&warn, "s5", nil, "Org", false, nil)
	record(&warn, "deps only", nil, "", false, nil) // names no tracker: not recorded

	for repo, want := range map[string]int{"o/none": priorledger.None, "o/found": priorledger.Found,
		"o/gap": priorledger.Gap, "o/other": priorledger.Gap, "o/report": priorledger.Gap, "org/anything": priorledger.None} {
		if got := latest(repo); got.Verdict != want {
			t.Errorf("%s: verdict %d, want %d", repo, got.Verdict, want)
		}
	}
	if warn.Len() != 0 {
		t.Errorf("warnings: %s", warn.String())
	}

	// A ledger that cannot be written is said, not swallowed.
	priorledger.Path = func() (string, error) { return filepath.Join("/nonexistent-dir-for-test", "x", "l.jsonl"), nil }
	record(&warn, "s", []string{"o/r"}, "", false, nil)
	if warn.Len() == 0 {
		t.Error("an unwritable ledger went unreported")
	}
}
