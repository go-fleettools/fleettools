package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func withFakeGH(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/gh", []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	oldPause, oldBackoff := betweenBatches, backoff
	betweenBatches = 0
	backoff = func(int) time.Duration { return 0 }
	t.Cleanup(func() { betweenBatches, backoff = oldPause, oldBackoff })
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
	if code := run(&out, &errOut); code != 0 {
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
	code := run(&out, &errOut)
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
	if code := run(&out, &errOut); code == 0 {
		t.Error("a pass that could not list the organisations exited 0")
	}
	if !strings.Contains(errOut.String(), "orgs:") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestTheTwoRateLimitsWantOppositeTreatment(t *testing.T) {
	// The secondary limit is a burst brake and lifts in seconds; waiting is
	// the remedy. The primary one is an hourly budget that resets at a fixed
	// time, and no short backoff reaches it -- matching on "rate" alone made a
	// spent budget sleep five minutes and fail anyway, per batch.
	for _, c := range []struct {
		msg  string
		want bool
	}{
		{"You have exceeded a secondary rate limit", true},
		{"was submitted too quickly", true},
		{"triggered an abuse detection mechanism", true},
		{"API rate limit exceeded for user ID 11405852.", false},
		{"gh: not logged in", false},
	} {
		if got := retryable(c.msg); got != c.want {
			t.Errorf("retryable(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}
