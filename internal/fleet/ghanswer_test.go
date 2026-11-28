package fleet

import (
	"errors"
	"testing"
	"time"
)

// A failing gh that still printed its answer: GHAnswer hands both back, GH
// keeps its old contract (no bytes with an error).
func TestGHAnswerKeepsStdoutOfAFailedCall(t *testing.T) {
	oldRun, oldBackoff, oldGuard := run, Backoff, Guard
	t.Cleanup(func() { run, Backoff, Guard = oldRun, oldBackoff, oldGuard })
	Backoff = func(int) time.Duration { return 0 }
	Guard = nil
	run = func([]string) ([]byte, string, error) {
		return []byte(`{"data":{},"errors":[]}`), "gh: Could not resolve\nmore", errors.New("exit 1")
	}
	out, err := GHAnswer("api", "graphql")
	if err == nil || err.Error() != "gh: Could not resolve\nmore" || string(out) != `{"data":{},"errors":[]}` {
		t.Errorf("GHAnswer gave %q, %v", out, err)
	}
	out, err = GH("api", "graphql")
	if err == nil || out != nil {
		t.Errorf("GH gave %q, %v, want no bytes with an error", out, err)
	}
	run = func([]string) ([]byte, string, error) { return []byte("ok"), "", nil }
	if out, err = GHAnswer("api"); err != nil || string(out) != "ok" {
		t.Errorf("a good call gave %q, %v", out, err)
	}
}
