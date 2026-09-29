package fleet

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// fake replaces the exec seam with a canned sequence of failures followed by
// one success, and counts the attempts. A test that only checks the final
// answer cannot tell "retried and succeeded" from "succeeded first time",
// which is the whole property here.
func fake(t *testing.T, failures []string, then string) *int {
	t.Helper()
	calls := 0
	oldRun, oldBackoff, oldGuard := run, Backoff, Guard
	run = func([]string) ([]byte, string, error) {
		i := calls
		calls++
		if i < len(failures) {
			return nil, failures[i], errors.New("exit 1")
		}
		return []byte(then), "", nil
	}
	Backoff = func(int) time.Duration { return 0 }
	Guard = nil
	t.Cleanup(func() { run, Backoff, Guard = oldRun, oldBackoff, oldGuard })
	return &calls
}

// TestTheTwoRateLimitsWantOppositeTreatment: the secondary limit is a burst
// brake and lifts in seconds; the primary one is an hourly budget that no
// backoff here reaches. Matching on "rate" alone made a spent budget sleep
// five minutes and fail anyway, per batch.
func TestTheTwoRateLimitsWantOppositeTreatment(t *testing.T) {
	for _, c := range []struct {
		msg  string
		want bool
	}{
		{"You have exceeded a secondary rate limit", true},
		{"was submitted too quickly", true},
		{"triggered an abuse detection mechanism", true},
		{"ratelimit-remaining: 0", true},
		{"API rate limit exceeded for user ID 11405852.", false},
		{"gh: not logged in", false},
	} {
		if got := Throttled(c.msg); got != c.want {
			t.Errorf("Throttled(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

// TestATransientNetworkFaultIsRetried is the half that four of the six sweeps
// did not have. prmerge's comment records the cost: 186 of 460 candidates lost
// to "no route to host" while a link flapped.
func TestATransientNetworkFaultIsRetried(t *testing.T) {
	for _, msg := range []string{
		"dial tcp: no route to host",
		"net/http: TLS handshake timeout",
		"read: connection reset by peer",
		"dial tcp: i/o timeout",
		"unexpected EOF",
		"operation timed out",
		// gh's own wording. Matching only the Go-level strings above is what
		// let a DNS outage produce 109 failures with the retry never firing.
		"error connecting to api.github.com",
		"dial tcp: lookup api.github.com: no such host",
		"check your internet connection",
	} {
		if !Transient(msg) {
			t.Errorf("Transient(%q) = false", msg)
		}
		calls := fake(t, []string{msg}, "ok")
		out, err := GH("api", "x")
		if err != nil {
			t.Errorf("%q: GH = %v, want it to retry through", msg, err)
		}
		if string(out) != "ok" {
			t.Errorf("%q: out = %q", msg, out)
		}
		if *calls != 2 {
			t.Errorf("%q: %d attempts, want 2 -- one failure then one success", msg, *calls)
		}
	}
}

// TestASpentBudgetFailsAtOnce: retrying an hourly budget spends minutes to
// fail anyway. The attempt count is the assertion; the error alone looks the
// same either way.
func TestASpentBudgetFailsAtOnce(t *testing.T) {
	calls := fake(t, []string{"API rate limit exceeded for user ID 1."}, "ok")
	if _, err := GH("api", "x"); err == nil {
		t.Fatal("a spent budget was reported as success")
	}
	if *calls != 1 {
		t.Errorf("%d attempts, want 1: a spent budget must not be slept on", *calls)
	}
}

func TestARealRefusalIsNotRetried(t *testing.T) {
	calls := fake(t, []string{"gh: Not Found (HTTP 404)"}, "ok")
	_, err := GH("api", "x")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v, want the 404 passed through", err)
	}
	if *calls != 1 {
		t.Errorf("%d attempts, want 1", *calls)
	}
}

func TestItGivesUpEventually(t *testing.T) {
	var many []string
	for range 20 {
		many = append(many, "no route to host")
	}
	calls := fake(t, many, "ok")
	if _, err := GH("api", "x"); err == nil {
		t.Fatal("an endless fault was reported as success")
	}
	if *calls != Attempts+1 {
		t.Errorf("%d attempts, want %d", *calls, Attempts+1)
	}
}

// TestTheGuardCanRefuseACall: quietscan is a watcher, and a tool that only
// looks must not be one edit away from writing.
func TestTheGuardCanRefuseACall(t *testing.T) {
	calls := fake(t, nil, "ok")
	Guard = func(args []string) error {
		for _, a := range args {
			if a == "-X" {
				return errors.New("read-only: refusing a write")
			}
		}
		return nil
	}
	if _, err := GH("api", "-X", "POST", "x"); err == nil {
		t.Error("the guard did not refuse a write")
	}
	if *calls != 0 {
		t.Errorf("gh ran %d times despite the guard", *calls)
	}
	if _, err := GH("api", "x"); err != nil {
		t.Errorf("the guard refused a read: %v", err)
	}
}

func TestOrgsSkipsBlankLines(t *testing.T) {
	fake(t, nil, "acme\n\nbeta\n")
	got, err := Orgs()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "acme" || got[1] != "beta" {
		t.Errorf("Orgs() = %v", got)
	}
}

// TestReposDecodesConcatenatedPages: `gh api --paginate` concatenates JSON
// arrays rather than merging them, so a decoder that reads ONE array sees only
// the first hundred repositories and reports a total that is a page, not a
// fleet.
func TestReposDecodesConcatenatedPages(t *testing.T) {
	fake(t, nil, `[{"full_name":"a/one","default_branch":"main"}]`+
		`[{"full_name":"a/two","default_branch":"dev"}]`)
	got, err := Repos("a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Repos() returned %d, want both pages: %v", len(got), got)
	}
	if got[1].DefaultBranch != "dev" {
		t.Errorf("default branch = %q, want the one the API gave", got[1].DefaultBranch)
	}
}

func TestReposSkipsArchivedAndForks(t *testing.T) {
	fake(t, nil, `[{"full_name":"a/live"},{"full_name":"a/old","archived":true},`+
		`{"full_name":"a/copy","fork":true}]`)
	got, err := Repos("a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].FullName != "a/live" {
		t.Errorf("Repos() = %v, want only a/live", got)
	}
}

func TestReposReportsAFailureRatherThanAnEmptyOrg(t *testing.T) {
	fake(t, []string{"gh: Not Found (HTTP 404)"}, "")
	got, err := Repos("a")
	if err == nil {
		t.Errorf("an unreadable org returned %v and no error, which a caller "+
			"cannot tell from an org with no repositories", got)
	}
}
