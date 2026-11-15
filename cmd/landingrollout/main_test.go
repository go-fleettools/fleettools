package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
)

func TestBodyDispositionsItsReference(t *testing.T) {
	b := body("v0.38.0", "x")
	if !strings.Contains(b, "Refs go-fleettools/landingkit#55") {
		t.Fatal("a bare reference makes ghmerge refuse")
	}
	if !strings.HasSuffix(b, "Generated with [Claude Code](https://claude.com/claude-code)\n") {
		t.Fatal(b)
	}
	if !strings.Contains(commitMsg("v0.38.0", "x"), "Co-Authored-By: Claude Sonnet 5.5") {
		t.Fatal("attribution")
	}
}

func TestParseRemaining(t *testing.T) {
	if n, err := parseRemaining("4682\n"); err != nil || n != 4682 {
		t.Fatal(n, err)
	}
	if _, err := parseRemaining(""); err == nil {
		t.Fatal("an empty answer is not a budget")
	}
}

func TestBudgetWaitsOnAnUnreadAnswerAndOnALowOne(t *testing.T) {
	oldRun, oldSleep := run, sleep
	defer func() { run, sleep = oldRun, oldSleep }()
	var answers = []string{"", "100", "2000"}
	slept := 0
	run = func(dir, name string, args ...string) (string, int, error) {
		a := answers[0]
		answers = answers[1:]
		if a == "" {
			return "", 1, nil
		}
		return a, 0, nil
	}
	sleep = func(time.Duration) { slept++ }
	if err := budget(1500); err != nil || slept != 2 {
		t.Fatalf("err %v slept %d: an unread answer and a low one are both 'wait'", err, slept)
	}
}

func TestStateRoundTripAndMissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if rs, err := readState(p); err != nil || rs != nil {
		t.Fatal("a state file that does not exist yet is an empty state", rs, err)
	}
	if err := record(p, result{Org: "a", Status: "merged", PR: 3}); err != nil {
		t.Fatal(err)
	}
	if err := record(p, result{Org: "b", Status: "red", Detail: "x"}); err != nil {
		t.Fatal(err)
	}
	rs, err := readState(p)
	if err != nil || len(rs) != 2 || rs[0].Org != "a" || rs[1].Status != "red" {
		t.Fatal(rs, err)
	}
}

func TestAnEmptyListIsRefusedNotFinished(t *testing.T) {
	dir := t.TempDir()
	l := filepath.Join(dir, "l.txt")
	os.WriteFile(l, nil, 0o644)
	var out, errOut bytes.Buffer
	code := cli.Drive(RootCmd(), []string{"--list", l, "--work", dir, "--state", filepath.Join(dir, "s"), "--kit", "v0.38.0"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("an empty list is unanswerable, got %d", code)
	}
}

func TestAFinishedOrgIsNotTouchedAgain(t *testing.T) {
	oldRun := run
	defer func() { run = oldRun }()
	var calls []string
	run = func(dir, name string, args ...string) (string, int, error) {
		calls = append(calls, name)
		if name == "gh" {
			return "5000", 0, nil
		}
		return "", 1, nil
	}
	dir := t.TempDir()
	l, s := filepath.Join(dir, "l.txt"), filepath.Join(dir, "s.jsonl")
	os.WriteFile(l, []byte("a\n"), 0o644)
	record(s, result{Org: "a", Status: "merged", PR: 1})
	var out, errOut bytes.Buffer
	if code := cli.Drive(RootCmd(), []string{"--list", l, "--work", dir, "--state", s, "--kit", "v0.38.0"}, &out, &errOut); code != 0 {
		t.Fatalf("%d %s", code, errOut.String())
	}
	for _, c := range calls {
		if c == "agentsync" || c == "git" {
			t.Fatalf("a merged organisation was claimed or cloned again: %v", calls)
		}
	}
}
