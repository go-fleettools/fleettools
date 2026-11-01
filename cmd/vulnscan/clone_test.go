package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// noBackoff makes these tests take microseconds instead of a minute. The waits
// themselves are not under test; which failures earn one is.
func noBackoff(t *testing.T) {
	was := cloneBackoff
	t.Cleanup(func() { cloneBackoff = was })
	cloneBackoff = func(int) {}
}

// the real refusal, copied from a sweep of 58 repositories on 2026-10-31.
const paced = "fatal: unable to access 'https://github.com/go-images/gif.git/': " +
	"The requested URL returned error: 403\n"

// and the real one for a repository that is gone, which the fake in
// main_test.go also returns.
const gone = "remote: Repository not found.\nfatal: repository 'https://github.com/acme/gone.git' not found\n"

// TestACloneRefusedForPaceIsRetried: a 403 on the transport means "come back in
// a moment", and reporting UNREAD for it is correct about this run and wrong
// about the repository -- UNREAD makes the lane red, so a sweep could go red
// over nothing but its own pace.
func TestACloneRefusedForPaceIsRetried(t *testing.T) {
	noBackoff(t)
	calls := 0
	run := func(context.Context, string, []string, string, ...string) ([]byte, []byte, error) {
		calls++
		if calls < 3 {
			return nil, []byte(paced), errors.New("exit status 128")
		}
		return nil, nil, nil
	}
	refused, err := clone(context.Background(), run, "go-images/gif", t.TempDir())
	if err != nil {
		t.Fatalf("clone failed although the third attempt succeeded: %v", err)
	}
	if refused != 2 || calls != 3 {
		t.Errorf("refused=%d calls=%d, want 2 and 3", refused, calls)
	}
}

// TestACloneRefusedForPaceStillGivesUp is the half that matters: a retry that
// eventually stops must NOT become a pass. UNREAD is not clean, and a sweep
// that called an unreadable repository clean would be the one defect this tool
// exists to refuse.
func TestACloneRefusedForPaceStillGivesUp(t *testing.T) {
	noBackoff(t)
	calls := 0
	run := func(context.Context, string, []string, string, ...string) ([]byte, []byte, error) {
		calls++
		return nil, []byte(paced), errors.New("exit status 128")
	}
	refused, err := clone(context.Background(), run, "go-images/gif", t.TempDir())
	if err == nil {
		t.Fatal("a clone refused every time came back as success")
	}
	if calls != cloneAttempts {
		t.Errorf("%d attempts, want %d", calls, cloneAttempts)
	}
	if refused != cloneAttempts-1 {
		t.Errorf("refused=%d, want %d", refused, cloneAttempts-1)
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("the error does not carry the reason: %v", err)
	}
}

// TestARepositoryThatIsGoneIsNotRetried: a 404 is not transient. The repository
// was deleted, made private or renamed, and retrying it spends the budget that
// would have read the next one -- on a 58-repository sweep that is the
// difference between a slow pass and a refused one.
func TestARepositoryThatIsGoneIsNotRetried(t *testing.T) {
	noBackoff(t)
	calls := 0
	run := func(context.Context, string, []string, string, ...string) ([]byte, []byte, error) {
		calls++
		return nil, []byte(gone), errors.New("exit status 128")
	}
	refused, err := clone(context.Background(), run, "acme/gone", t.TempDir())
	if err == nil {
		t.Fatal("a missing repository came back as success")
	}
	if calls != 1 || refused != 0 {
		t.Errorf("calls=%d refused=%d, want 1 and 0 — a 404 must fail on the first attempt", calls, refused)
	}
}

// TestRefusedForPaceReadsOnlyPaceSignatures pins the discriminator itself,
// because the dangerous mistake is the generous one: a predicate that said
// "true" for any failure would retry every deleted repository three times and
// turn a sweep into a crawl, while still reporting UNREAD at the end.
func TestRefusedForPaceReadsOnlyPaceSignatures(t *testing.T) {
	for _, c := range []struct {
		name   string
		stderr string
		want   bool
	}{
		{"the real 403", paced, true},
		{"a 429", "fatal: unable to access '…': The requested URL returned error: 429\n", true},
		{"a named rate limit", "remote: You have exceeded a secondary rate limit\n", true},
		{"a repository that is gone", gone, false},
		{"a private one asking for a name", "fatal: could not read Username for 'https://github.com': terminal prompts disabled\n", false},
		{"a disk error", "fatal: could not create leading directories\n", false},
		{"nothing at all", "", false},
	} {
		if got := refusedForPace([]byte(c.stderr)); got != c.want {
			t.Errorf("%s: refusedForPace = %v, want %v", c.name, got, c.want)
		}
	}
}
