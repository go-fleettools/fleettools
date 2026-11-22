package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ⛔ THE SECOND FALSE NEGATIVE OF THE SAME SHAPE, 2026-11-22, and the first one
// was the typographic apostrophe fixed in TestTypographicPunctuationIsFolded...
// Twice in one session a published page was investigated -- its deploy re-run
// once -- because this command answered `false` to a want I had typed with the
// wrong case: the docs page says "An explicit -interval is never scaled" at the
// head of a sentence and my want said "an explicit". plain() folds punctuation
// on BOTH sides so that difference can never reach the verdict, and nothing
// folded case, so a present-but-capitalised want printed exactly what an absent
// one prints. The verdict must stay false -- the question was wrong, and a
// command that lowered its standard to match the question would be worse than
// useless -- so what changes is the report, which now says which of the two it
// is. A want's case is the asker's mistake; a missing sentence is the page's.
func TestACaseOnlyDifferenceIsNamedInsteadOfLookingAbsent(t *testing.T) {
	srv := httptest.NewServer(page(`<p>An explicit -interval is never scaled.</p>`))
	defer srv.Close()
	code, out, _ := drive(t, "-retry", "0", srv.URL, "an explicit -interval is never scaled")
	if code != 1 {
		t.Fatalf("code %d, want 1: the page does not contain what was asked for", code)
	}
	if !strings.Contains(out, `false "an explicit -interval is never scaled"`) {
		t.Fatalf("the verdict must stay false; out %q", out)
	}
	if !strings.Contains(out, "present on the page, with a different case") {
		t.Fatalf("no case diagnosis; out %q", out)
	}
}

// The control, and the whole value of the diagnosis: a want that is genuinely
// absent must NOT carry it. Without this test the note could be printed beside
// every false and still pass the test above.
func TestAGenuinelyAbsentWantIsNotBlamedOnItsCase(t *testing.T) {
	srv := httptest.NewServer(page(`<p>An explicit -interval is never scaled.</p>`))
	defer srv.Close()
	code, out, _ := drive(t, "-retry", "0", srv.URL, "the default interval grows")
	if code != 1 || !strings.Contains(out, `false "the default interval grows"`) {
		t.Fatalf("code %d, out %q", code, out)
	}
	if strings.Contains(out, "different case") {
		t.Fatalf("a sentence that is not on the page at all was reported as a case difference; out %q", out)
	}
}

// Both at once, in one group: the counts must not borrow from each other.
func TestACaseDifferenceAndARealAbsenceAreCountedApart(t *testing.T) {
	srv := httptest.NewServer(page(`<p>An explicit -interval is never scaled.</p>`))
	defer srv.Close()
	old := sleep
	sleep = func(time.Duration) {}
	defer func() { sleep = old }()
	code, out, errOut := drive(t, "-retry", "1", srv.URL,
		"an explicit -interval is never scaled", "the default interval grows")
	if code != 1 {
		t.Fatalf("code %d, want 1", code)
	}
	if strings.Count(out, "different case") != 1 {
		t.Fatalf("exactly one of the two is a case difference; out %q", out)
	}
	// ⛔ THE WAITING LINE IS THE ONE A PERSON READS. A want that differs only in
	// case will be just as missing after the last retry, so saying it in the
	// final report alone still spends the whole wait before anyone can know.
	if !strings.Contains(errOut, "2 string(s) missing, 1 of them only by case") {
		t.Fatalf("the retry line must name it; errOut %q", errOut)
	}
}

// A page whose text matches exactly gets no note anywhere near it.
func TestAPresentWantCarriesNoNote(t *testing.T) {
	srv := httptest.NewServer(page(`<p>An explicit -interval is never scaled.</p>`))
	defer srv.Close()
	code, out, _ := drive(t, srv.URL, "An explicit -interval is never scaled")
	if code != 0 || strings.Contains(out, "different case") {
		t.Fatalf("code %d, out %q", code, out)
	}
}

// ⛔ --raw DELIBERATELY HAS NO DIAGNOSIS, and that is a property worth a test:
// scanFor consumes the stream as it reads it, so when a want is known to be
// missing there is nothing left to re-test, and a page too large for the text
// match is precisely the one not to buffer for the sake of a hint. A reader of
// the code should learn that from a test rather than guess it from an absence.
func TestRawModeReportsNoCaseDiagnosis(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `<p>An explicit -interval is never scaled.</p>`)
	}))
	defer srv.Close()
	code, out, _ := drive(t, "--raw", "-retry", "0", srv.URL, "an explicit -interval is never scaled")
	if code != 1 || hits.Load() != 1 {
		t.Fatalf("code %d hits %d", code, hits.Load())
	}
	if strings.Contains(out, "different case") {
		t.Fatalf("--raw cannot know this, and must not claim it; out %q", out)
	}
}
