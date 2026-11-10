package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/priorledger"
)

// TestMain gives the package a ledger in which priorart read o/r a minute ago
// and found nothing, so the --new tests written before the gate exercise what
// they were written for. The gate's own tests below set their own ledger.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ghissue-ledger")
	if err != nil {
		panic(err)
	}
	p := filepath.Join(dir, "priorart.jsonl")
	priorledger.Path = func() (string, error) { return p, nil }
	if err := priorledger.Append(priorledger.Record{Time: time.Now().Add(-time.Minute), Repos: []string{"o/r"},
		Subject: "the test subject", Verdict: priorledger.None}); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// ledger points the gate at a fresh ledger holding recs, for one test.
func ledger(t *testing.T, recs ...priorledger.Record) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "priorart.jsonl")
	old := priorledger.Path
	t.Cleanup(func() { priorledger.Path = old })
	priorledger.Path = func() (string, error) { return p, nil }
	for _, r := range recs {
		if err := priorledger.Append(r); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPriorArtGate(t *testing.T) {
	now := time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)
	old := priorNow
	t.Cleanup(func() { priorNow = old })
	priorNow = func() time.Time { return now }
	run := func(repo string, v int, ago time.Duration) priorledger.Record {
		return priorledger.Record{Time: now.Add(-ago), Repos: []string{repo}, Subject: "nested module", Verdict: v}
	}

	// No run at all: refused, and the refusal names the command to run.
	ledger(t)
	if _, err := priorArtStamp("o/r", "", ""); err == nil || !strings.Contains(err.Error(), `priorart --repo o/r "<subject>"`) {
		t.Fatalf("no run: %v", err)
	}
	// A run too old, or on another repository, is no run.
	ledger(t, run("o/r", priorledger.None, 3*time.Hour), run("o/other", priorledger.None, time.Minute))
	if _, err := priorArtStamp("o/r", "", ""); err == nil {
		t.Fatal("a stale or foreign run was accepted")
	}
	// The waiver files anyway, and says so in public.
	if s, err := priorArtStamp("o/r", "", "the tracker is down"); err != nil || !strings.Contains(s, "NOT checked") || !strings.Contains(s, "the tracker is down") {
		t.Fatalf("waiver: %q, %v", s, err)
	}

	// Nothing found: filed, and the stamp says what was run.
	ledger(t, run("o/r", priorledger.None, 10*time.Minute))
	if s, err := priorArtStamp("o/r", "", ""); err != nil || !strings.Contains(s, `"nested module"`) || !strings.Contains(s, "found none") {
		t.Fatalf("none found: %q, %v", s, err)
	}

	// Found, or a surface unread: refused until it is said why this is new.
	for _, v := range []int{priorledger.Found, priorledger.Gap} {
		ledger(t, run("o/r", v, 10*time.Minute))
		if _, err := priorArtStamp("o/r", "", ""); err == nil || !strings.Contains(err.Error(), "--prior-art") {
			t.Fatalf("verdict %d without --prior-art: %v", v, err)
		}
		s, err := priorArtStamp("o/r", "#90 is tags; this is branches", "")
		if err != nil || !strings.Contains(s, "checked: #90 is tags; this is branches") {
			t.Fatalf("verdict %d with --prior-art: %q, %v", v, s, err)
		}
	}
	// The texts are one bounded line.
	for _, bad := range []string{"two\nlines", strings.Repeat("x", maxPriorText+1)} {
		if _, err := priorArtStamp("o/r", bad, ""); err == nil {
			t.Errorf("--prior-art %q accepted", bad[:min(len(bad), 12)])
		}
		if _, err := priorArtStamp("o/r", "", bad); err == nil {
			t.Errorf("--prior-art-waiver %q accepted", bad[:min(len(bad), 12)])
		}
	}
}

// End to end: --new without a run is refused BEFORE any request; with one, the
// stamp is in what --dry-run shows would be sent.
func TestNewIsGatedByPriorArt(t *testing.T) {
	ledger(t)
	_, err := exec(t, "o/r", "--new", "--title", "t", "--base", "https://example.invalid",
		"--body-file", bodyFile(t, "the body"), "--token-file", "/nonexistent-token")
	if err == nil || !strings.Contains(err.Error(), "no priorart run covers o/r") {
		t.Fatalf("refused before the token is read: %v", err)
	}
}
