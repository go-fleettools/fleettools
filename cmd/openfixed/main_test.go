package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
)

// fake is a GitHub stand-in: search results per scope, timelines per issue.
type fake struct {
	t         *testing.T
	issues    map[string][]string // scope query suffix → "repo#N|title"
	timelines map[string]string   // "repo#N" → JSON (array of events)
	failTL    map[string]error
	calls     []string
	posts     []string // POST/PATCH calls
	budgetAt  int      // timeline call number at which the reserve is hit (0: never)
	tlCalls   int
	reported  int // override total_count (0: len)
}

func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{t: t, issues: map[string][]string{}, timelines: map[string]string{}, failTL: map[string]error{}}
	savedGH, savedBudget, savedPause := gh, startBudget, pause
	gh, startBudget, pause = f.gh, func(int, int) error { return nil }, func(time.Duration) {}
	t.Cleanup(func() { gh, startBudget, pause = savedGH, savedBudget, savedPause })
	return f
}

func (f *fake) gh(args ...string) ([]byte, error) {
	joined := strings.Join(args, " ")
	f.calls = append(f.calls, joined)
	switch {
	case strings.Contains(joined, "search/issues"):
		var q string
		for i, a := range args {
			if a == "-f" && strings.HasPrefix(args[i+1], "q=") {
				q = args[i+1]
			}
		}
		var items []string
		for suffix, list := range f.issues {
			if !strings.HasSuffix(q, " "+suffix) {
				continue
			}
			for _, e := range list {
				head, title, _ := strings.Cut(e, "|")
				repo, n, _ := strings.Cut(head, "#")
				items = append(items, fmt.Sprintf(`{"number":%s,"title":%q,"repository_url":"https://api.github.com/repos/%s"}`, n, title, repo))
			}
		}
		total := len(items)
		if f.reported > 0 && !strings.Contains(q, "linked:pr") {
			total = f.reported
		}
		if strings.Contains(joined, "page=2") {
			items = nil
		}
		return []byte(fmt.Sprintf(`{"total_count":%d,"items":[%s]}`, total, strings.Join(items, ","))), nil
	case strings.Contains(joined, "/timeline"):
		f.tlCalls++
		if f.budgetAt > 0 && f.tlCalls >= f.budgetAt {
			return nil, fmt.Errorf("%w: 1000 left, 1000 reserved", fleet.ErrBudgetReserve)
		}
		for ref, err := range f.failTL {
			repo, n, _ := strings.Cut(ref, "#")
			if strings.Contains(joined, "repos/"+repo+"/issues/"+n+"/timeline") {
				return nil, err
			}
		}
		for ref, js := range f.timelines {
			repo, n, _ := strings.Cut(ref, "#")
			if strings.Contains(joined, "repos/"+repo+"/issues/"+n+"/timeline") {
				return []byte("[" + js + "]"), nil
			}
		}
		return []byte("[[]]"), nil
	default:
		f.posts = append(f.posts, joined)
		return []byte("{}"), nil
	}
}

func drive(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := cli.Drive(RootCmd(), args, &out, &errOut)
	return code, out.String() + errOut.String()
}

func xref(repo string, n int, merged string, body string) string {
	pr := `null`
	if merged != "" {
		pr = fmt.Sprintf(`{"merged_at":%q}`, merged)
	} else {
		pr = `{"merged_at":null}`
	}
	return fmt.Sprintf(`{"event":"cross-referenced","source":{"type":"issue","issue":{"number":%d,"body":%q,"pull_request":%s,"repository":{"full_name":%q}}}}`,
		n, body, pr, repo)
}

const mergedAt = "2026-11-01T10:00:00Z"

func TestClassificationTable(t *testing.T) {
	cases := []struct {
		name string
		evs  string
		want Class
	}{
		{"fixes keyword, same repo", xref("o/r", 9, mergedAt, "Fixes #5"), Closable},
		{"closes cross-repo", xref("o/other", 9, mergedAt, "closes o/r#5"), Closable},
		{"colon and case", xref("o/r", 9, mergedAt, "RESOLVES: #5"), Closable},
		{"url form", xref("o/other", 9, mergedAt, "fixed https://github.com/o/r/issues/5"), Closable},
		{"bare #N from another repo is not this issue", xref("o/other", 9, mergedAt, "fixes #5"), Review},
		{"other number", xref("o/r", 9, mergedAt, "fixes #50"), Review},
		{"mention without keyword", xref("o/r", 9, mergedAt, "see #5"), Review},
		{"unmerged with keyword", xref("o/r", 9, "", "fixes #5"), None},
		{"no events", ``, None},
		{"connected plus merged mention", `{"event":"connected"},` + xref("o/r", 9, mergedAt, "work on #5"), Closable},
		{"connected but PR unmerged", `{"event":"connected"},` + xref("o/r", 9, "", "x"), None},
		{"reopened after merge", xref("o/r", 9, mergedAt, "fixes #5") + `,{"event":"reopened","created_at":"2026-11-02T00:00:00Z"}`, Review},
		{"reopened before merge", `{"event":"reopened","created_at":"2026-10-01T00:00:00Z"},` + xref("o/r", 9, mergedAt, "fixes #5"), Closable},
		{"the issue itself", xref("o/r", 5, mergedAt, "fixes #5"), None},
		{"not a PR", `{"event":"cross-referenced","source":{"issue":{"number":3,"pull_request":null,"repository":{"full_name":"o/r"}}}}`, None},
		{"no source", `{"event":"cross-referenced"}`, None},
		{"unrelated event", `{"event":"labeled"}`, None},
	}
	for _, c := range cases {
		evs, err := parseTimeline([]byte("[[" + c.evs + "]]"))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got, _ := Classify("o/r", 5, evs)
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// The body of go-tex/engine#336, as quoted in #205: the only keyword-like word
// is "fixed" in "not fixed", in a heading, two lines above the reference.
const alsoFiledNotFixed = "## Also filed, not fixed\n\ngo-tex/math#22: `delimited()` asks a stretchy delimiter to cover `2*delta1` where TeX asks for less.\nThe rule cannot land until that is found."

// A body shaped like go-richdoc/markdown#18: many headings and reference-shaped
// text, no closing keyword adjacent to the reference.
const headingsEverywhere = "### What this fixes\n\n#5 is the parser; o/r#5 is the renderer.\n\n### Resolves nothing yet\n\nSee also #5 and o/r#5.\n\n### Fix list\n\n- o/r#5\n- #6\n"

func TestAKeywordOnAnotherLineDoesNotCloseTheIssue(t *testing.T) {
	cases := []struct {
		name string
		repo string
		body string
		want Class
	}{
		// the negative controls: none of these may come out CLOSABLE ...
		{"#205: heading, blank line, bare cross-repo reference", "o/engine", strings.Replace(alsoFiledNotFixed, "go-tex/math#22", "o/r#5", 1), Review},
		{"heading keyword, reference on the next line", "o/r", "## Fixes\n#5", Review},
		{"headings and reference-shaped text (markdown#18 shape)", "o/r", headingsEverywhere, Review},
		{"keyword inside a code span", "o/r", "the `fixes #5` syntax is documented", Review},
		{"keyword inside a fence", "o/r", "```\nCloses #5\n```", Review},
		{"keyword inside an HTML comment", "o/r", "<!-- Closes #5 -->", Review},
		{"only the first of a list closes", "o/r", "Closes #6, #5", Review},
		{"Refs keeps it open", "o/r", "Refs #5", Review},
		// ... and the positive controls, so the fix cannot pass by making
		// everything REVIEW.
		{"Closes #N on one line", "o/r", "Closes #5", Closable},
		{"Closes owner/repo#N on one line", "o/other", "Closes o/r#5", Closable},
		{"keyword after headings still closes", "o/r", headingsEverywhere + "\nFixes #5", Closable},
		{"colon, same line", "o/other", "## Notes\n\nResolves: o/r#5", Closable},
	}
	for _, c := range cases {
		evs, err := parseTimeline([]byte("[[" + xref(c.repo, 9, mergedAt, c.body) + "]]"))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got, _ := Classify("o/r", 5, evs); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestACrossReferenceAloneIsNotConnected(t *testing.T) {
	// The issue's own timeline: one cross-referenced event from a merged PR
	// whose body does not close it. Without a `connected` event this is REVIEW.
	evs, _ := parseTimeline([]byte("[[" + xref("o/engine", 9, mergedAt, alsoFiledNotFixed) + "]]"))
	if got, _ := Classify("o/r", 5, evs); got != Review {
		t.Fatalf("cross-referenced alone: %q", got)
	}
	// A real `connected` event is independent evidence and does close.
	evs, _ = parseTimeline([]byte(`[[{"event":"connected"},` + xref("o/engine", 9, mergedAt, alsoFiledNotFixed) + `]]`))
	if got, _ := Classify("o/r", 5, evs); got != Closable {
		t.Fatalf("connected: %q", got)
	}
}

func TestParseTimelineShapes(t *testing.T) {
	if evs, err := parseTimeline([]byte(`[{"event":"labeled"}]`)); err != nil || len(evs) != 1 {
		t.Errorf("bare events: %v %v", evs, err)
	}
	if _, err := parseTimeline([]byte(`nope`)); err == nil {
		t.Error("garbage accepted")
	}
	if _, err := parseTimeline([]byte(`["x"]`)); err == nil {
		t.Error("non-event page accepted")
	}
	if got := closingRef("", "o/r", "o/r", 1); got {
		t.Error("empty body matched")
	}
}

func scenario(t *testing.T) *fake {
	f := newFake(t)
	f.issues["org:A"] = []string{"A/x#1|fixed one", "A/x#2|mentioned", "A/x#3|only unmerged", "A/x#4|nothing"}
	f.issues["org:B"] = []string{"B/y#7|other org"}
	f.timelines["A/x#1"] = xref("A/x", 10, mergedAt, "Closes #1")
	f.timelines["A/x#2"] = xref("A/z", 11, mergedAt, "related to A/x#2")
	f.timelines["A/x#3"] = xref("A/x", 12, "", "Fixes #3")
	f.timelines["B/y#7"] = xref("B/y", 13, mergedAt, "fixes #7")
	return f
}

func TestDryRunReportsAndDenominator(t *testing.T) {
	f := scenario(t)
	code, out := drive(t, "--org", "A", "--org", "B")
	if code != 0 {
		t.Fatalf("status %d\n%s", code, out)
	}
	for _, want := range []string{
		"CLOSABLE A/x#1  fixed one  <- A/x#10 (merged 2026-11-01)",
		"REVIEW   A/x#2  mentioned  <- A/z#11 (merged 2026-11-01)",
		"CLOSABLE B/y#7",
		"A  ", "searched 4, listed 4, timelines read 4, skipped 0 | CLOSABLE 1, REVIEW 1",
		"searched 1, listed 1, timelines read 1, skipped 0 | CLOSABLE 1, REVIEW 0",
		"total: CLOSABLE 2, REVIEW 1",
		"nothing was closed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "only unmerged") || strings.Contains(out, "nothing  ") {
		t.Errorf("unlisted issues printed:\n%s", out)
	}
	if len(f.posts) != 0 {
		t.Errorf("dry run wrote: %v", f.posts)
	}
}

func TestCloseOnlyClosable(t *testing.T) {
	f := scenario(t)
	code, out := drive(t, "--org", "A", "--org", "B", "--close")
	if code != 0 {
		t.Fatalf("status %d\n%s", code, out)
	}
	if len(f.posts) != 4 { // comment + patch for two CLOSABLE
		t.Fatalf("posts: %v", f.posts)
	}
	for _, p := range f.posts {
		if strings.Contains(p, "A/x/issues/2") || strings.Contains(p, "issues/3") || strings.Contains(p, "issues/4") {
			t.Errorf("touched a non-CLOSABLE issue: %s", p)
		}
	}
	if !strings.Contains(f.posts[0], "body=Closed: fixed by A/x#10 (merged 2026-11-01), found by fleettools openfixed") {
		t.Errorf("comment: %s", f.posts[0])
	}
	if !strings.Contains(f.posts[1], "state=closed") || !strings.Contains(out, "closed 1, failed 0") {
		t.Errorf("patch/summary: %s\n%s", f.posts[1], out)
	}
}

func TestCloseFailureAndKeywordPreferred(t *testing.T) {
	f := newFake(t)
	f.issues["user:u"] = []string{"u/r#1|t"}
	f.timelines["u/r#1"] = xref("u/r", 2, mergedAt, "mentions #1") + "," + xref("u/r", 3, mergedAt, "fixes #1")
	saved := gh
	gh = func(args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "comments") {
			return nil, errors.New("403")
		}
		return saved(args...)
	}
	code, out := drive(t, "--owner", "u", "--close")
	if code != 1 || !strings.Contains(out, "close FAILED") || !strings.Contains(out, "failed 1") {
		t.Fatalf("status %d\n%s", code, out)
	}
	gh = saved
	f.posts = nil
	drive(t, "--owner", "u", "--close")
	if !strings.Contains(f.posts[0], "u/r#3 (merged") || strings.Contains(f.posts[0], "u/r#2") {
		t.Errorf("comment should cite only the keyword PR: %s", f.posts[0])
	}
}

func TestSkipsAreCountedNotZero(t *testing.T) {
	f := scenario(t)
	f.failTL["A/x#1"] = errors.New("HTTP 502")
	code, out := drive(t, "--org", "A")
	if code != 2 {
		t.Fatalf("status %d, want 2\n%s", code, out)
	}
	for _, want := range []string{"NOT READ: A/x#1", "timelines read 3, skipped 1", "skipped 1: timeline unreadable"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestCapAndMaxTimelines(t *testing.T) {
	f := scenario(t)
	f.reported = 1500
	code, out := drive(t, "--org", "A", "--max-timelines", "2")
	if code != 2 {
		t.Fatalf("status %d\n%s", code, out)
	}
	for _, want := range []string{"skipped 2: --max-timelines reached", "skipped 1496: beyond the search's 1000-result cap", "searched 1500"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestBudgetStop(t *testing.T) {
	f := scenario(t)
	f.budgetAt = 2
	code, out := drive(t, "--org", "A", "--org", "B", "--margin", "1000")
	if code != 2 {
		t.Fatalf("status %d\n%s", code, out)
	}
	for _, want := range []string{"STOPPED at the budget margin of 1000", "budget reserve reached", "INCOMPLETE"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	// the second owner's timeline must not have been attempted after the stop
	if f.tlCalls != 2 {
		t.Errorf("timeline calls after stop: %d", f.tlCalls)
	}
}

func TestBudgetStopInSearchAndFloor(t *testing.T) {
	f := newFake(t)
	saved := gh
	gh = func(args ...string) ([]byte, error) {
		return nil, fmt.Errorf("%w: 3 left", fleet.ErrBudgetReserve)
	}
	code, out := drive(t, "--org", "A", "--org", "B")
	gh = saved
	if code != 2 || !strings.Contains(out, "NOT READ: A search") || !strings.Contains(out, "STOPPED") {
		t.Fatalf("status %d\n%s", code, out)
	}
	_ = f
	startBudget = func(int, int) error { return fleet.ErrBudgetFloor }
	code, out = drive(t, "--org", "A")
	if code != 2 || !strings.Contains(out, "below the floor") {
		t.Fatalf("floor: status %d\n%s", code, out)
	}
}

func TestSearchErrorsAndPaging(t *testing.T) {
	f := newFake(t)
	saved := gh
	gh = func(args ...string) ([]byte, error) {
		j := strings.Join(args, " ")
		switch {
		case strings.Contains(j, "linked:pr"):
			return []byte(`{not json`), nil
		case strings.Contains(j, "-f page=1"):
			return []byte(`{"total_count":2,"items":[{"number":1,"title":"a","repository_url":"https://api.github.com/repos/o/r"}]}`), nil
		case strings.Contains(j, "-f page=2"):
			return []byte(`{"total_count":2,"items":[{"number":2,"title":"b","repository_url":"odd"}]}`), nil
		}
		return saved(args...)
	}
	is, total, err := collect(scope{"o", "org:o"})
	if err != nil || total != 2 || len(is) != 2 || is[1].Repo != "odd" {
		t.Fatalf("paging: %v %d %v", is, total, err)
	}
	gh = func(args ...string) ([]byte, error) { return []byte(`{not json`), nil }
	if _, _, err := collect(scope{"o", "org:o"}); err == nil {
		t.Fatal("bad search JSON accepted")
	}
	_ = f
}

func TestNoOwnerAndHelp(t *testing.T) {
	newFake(t)
	if code, out := drive(t); code != 2 || !strings.Contains(out, "no owner") {
		t.Errorf("no owner: %d %s", code, out)
	}
	if code, out := drive(t, "--help"); code != 0 || !strings.Contains(out, "CLOSABLE") {
		t.Errorf("help: %d", code)
	}
	if code, _ := drive(t, "--owner", "x", "--org", "X", "--owner", "x"); code != 0 {
		t.Errorf("dedupe run: %d", code)
	}
}

func TestOwnerOfFallback(t *testing.T) {
	if got := ownerOf("zzz/r", []scope{{"A", ""}}); got != "A" {
		t.Errorf("fallback %q", got)
	}
}
