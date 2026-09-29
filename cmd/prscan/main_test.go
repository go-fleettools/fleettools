package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

func withFakeGH(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/gh", []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	oldPause, oldBackoff := betweenBatches, fleet.Backoff
	betweenBatches = 0
	fleet.Backoff = func(int) time.Duration { return 0 }
	t.Cleanup(func() { betweenBatches, fleet.Backoff = oldPause, oldBackoff })
}

const twoPRs = `{"items":[
  {"html_url":"https://github.com/acme/one/pull/3"},
  {"html_url":"https://github.com/acme/two/pull/1"}]}`

func TestOpenPullRequestsAreCountedByRepository(t *testing.T) {
	withFakeGH(t, `
case "$*" in
  *user/orgs*) echo "acme" ;;
  *search/issues*) echo '`+twoPRs+`' ;;
esac`)
	var out, errOut strings.Builder
	if code := run(&out, &errOut, nil); code != 0 {
		t.Errorf("exit = %d: %s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "open PRs: 2 across 2 repos") {
		t.Errorf("the summary reads:\n%s", got)
	}
	if strings.Contains(got, "INCOMPLETE") {
		t.Errorf("a complete pass claimed to be incomplete:\n%s", got)
	}
}

func TestABatchThatCouldNotBeSearchedMakesTheCountAFloor(t *testing.T) {
	// A batch covers ~15 organisations. When one fails, the total silently
	// drops by everything they hold, and on stderr alone it is a line that a
	// caller reading the count with `| tail` never sees. This morning's
	// "205 open PRs" was read from exactly such a pipe.
	withFakeGH(t, `
case "$*" in
  *user/orgs*) printf 'acme\nbeta\n' ;;
  *search/issues*) echo "API rate limit exceeded for user ID 1." >&2; exit 1 ;;
esac`)
	var out, errOut strings.Builder
	code := run(&out, &errOut, nil)
	got := out.String()
	if code == 0 {
		t.Errorf("an incomplete pass exited 0:\n%s", got)
	}
	if !strings.Contains(got, "INCOMPLETE") || !strings.Contains(got, "floor and not a total") {
		t.Errorf("the count is offered as a total:\n%s", got)
	}
	// The organisations behind the gap are named: which ones are missing is
	// the whole of what a reader needs.
	if !strings.Contains(got, "acme") || !strings.Contains(got, "rate limit") {
		t.Errorf("the report does not say what was missed, or why:\n%s", got)
	}
	// stderr keeps its line too, for whoever is watching that instead.
	if !strings.Contains(errOut.String(), "batch 0") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestWithoutTheOrganisationsThereIsNothingToSearch(t *testing.T) {
	withFakeGH(t, `echo "gh: not logged in" >&2; exit 1`)
	var out, errOut strings.Builder
	if code := run(&out, &errOut, nil); code == 0 {
		t.Error("a pass that could not list the organisations exited 0")
	}
	if !strings.Contains(errOut.String(), "orgs:") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

// TestANetworkBlipNoLongerHolesTheSweep is the payoff of moving fleet.GH() into
// internal/fleet. Before that, this command retried a throttle and gave up on
// the first dial error -- so a link flap turned a whole batch of
// organisations into a silent zero, and the count came back smaller with
// nothing saying why. prmerge had learned this and prscan had not.
//
// The fake gh fails once with gh's own DNS wording, then succeeds.
func TestANetworkBlipNoLongerHolesTheSweep(t *testing.T) {
	dir := t.TempDir()
	withFakeGH(t, `
case "$*" in
  *user/orgs*) echo "acme" ;;
  *search/issues*)
    if [ ! -f `+dir+`/tripped ]; then
      : > `+dir+`/tripped
      echo "error connecting to api.github.com" >&2
      exit 1
    fi
    echo '`+twoPRs+`' ;;
esac`)
	var out, errOut strings.Builder
	if code := run(&out, &errOut, nil); code != 0 {
		t.Fatalf("exit = %d, want the blip retried through: %s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "open PRs: 2 across 2 repos") {
		t.Errorf("the count did not survive one dial error:\n%s", got)
	}
	if strings.Contains(got, "INCOMPLETE") {
		t.Errorf("a retried blip was reported as an incomplete pass:\n%s", got)
	}
}
