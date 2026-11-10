package priorledger

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func useTemp(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sub", "priorart.jsonl")
	old := Path
	t.Cleanup(func() { Path = old })
	Path = func() (string, error) { return p, nil }
	return p
}

func TestLatestFindsTheNewestCoveringRun(t *testing.T) {
	p := useTemp(t)
	now := time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)
	if _, err := Latest("o/r", time.Hour, now); !errors.Is(err, ErrNone) {
		t.Fatalf("an empty ledger: %v", err)
	}
	for _, r := range []Record{
		{Time: now.Add(-3 * time.Hour), Repos: []string{"O/R"}, Subject: "too old", Verdict: None},
		{Time: now.Add(-30 * time.Minute), Repos: []string{"o/r"}, Subject: "found", Verdict: Found},
		{Time: now.Add(-10 * time.Minute), Repos: []string{"o/other"}, Subject: "other repo", Verdict: None},
		{Time: now.Add(-5 * time.Minute), Org: "O", Subject: "org-wide", Verdict: Gap},
	} {
		if err := Append(r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Latest("o/r", time.Hour, now)
	if err != nil || got.Subject != "org-wide" || got.Verdict != Gap {
		t.Fatalf("%+v, %v", got, err)
	}
	if got, err := Latest("x/r", time.Hour, now); !errors.Is(err, ErrNone) {
		t.Fatalf("another owner: %+v, %v", got, err)
	}
	if _, err := Latest("o/r", time.Minute, now); !errors.Is(err, ErrNone) {
		t.Fatalf("outside the window: %v", err)
	}
	// A run dated in the future is not trusted.
	if err := Append(Record{Time: now.Add(time.Hour), Repos: []string{"o/r"}, Subject: "future"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := Latest("o/r", time.Hour, now); got.Subject == "future" {
		t.Fatal("a future-dated run was used")
	}
	// Permissions: owner-only.
	if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v, %v", st.Mode().Perm(), err)
	}
	if st, err := os.Stat(filepath.Dir(p)); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v, %v", st.Mode().Perm(), err)
	}
}

func TestTornLinesAreSkippedAndTheFileIsBounded(t *testing.T) {
	p := useTemp(t)
	now := time.Now()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Append(Record{Time: now, Repos: []string{"o/r"}, Subject: "ok"}); err != nil {
		t.Fatal(err)
	}
	if got, err := Latest("o/r", time.Hour, now); err != nil || got.Subject != "ok" {
		t.Fatalf("%+v, %v", got, err)
	}
	big := strings.Repeat("s", 2000)
	for i := 0; i < 700; i++ { // ~1.4 MB of records
		if err := Append(Record{Time: now, Repos: []string{"o/r"}, Subject: big}); err != nil {
			t.Fatal(err)
		}
	}
	st, err := os.Stat(p)
	if err != nil || st.Size() > MaxBytes {
		t.Fatalf("size %d, %v", st.Size(), err)
	}
	if got, err := Latest("o/r", time.Hour, now); err != nil || got.Subject != big {
		t.Fatalf("after trimming: %v", err)
	}
}
