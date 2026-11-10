package main

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// ⛔ THE DEFECT, STATED AS A TEST: a subject of seven terms or more lost the
// tracker half entirely.
//
// The terms are OR-ed, so seven clauses need six ORs — and GitHub's search API
// allows FIVE AND/OR/NOT operators per query. Measured in #200, same repository
// and the same minute: six clauses read 94 entries, seven answered HTTP 422 and
// the surface was reported NOT READ.
//
// The terms that overflow are exactly the careful ones: a subject written
// precisely is the one most likely to have prior art, and it was the one
// guaranteed not to look.
func TestASubjectOfManyTermsStillReadsTheTracker(t *testing.T) {
	terms := []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine"}
	var queries []string
	stub(t, func(args []string) ([]byte, error) {
		for i, a := range args {
			if a == "-f" && i+1 < len(args) && strings.HasPrefix(args[i+1], "q=") {
				q := strings.TrimPrefix(args[i+1], "q=")
				queries = append(queries, q)
				// What GitHub does to a query with six or more operators.
				if strings.Count(q, " OR ") > 5 {
					return nil, errors.New("gh: Validation Failed (HTTP 422)")
				}
			}
		}
		return []byte(`{"total_count":0,"items":[]}`), nil
	})

	if _, _, err := Tracker("repo:o/r", terms); err != nil {
		t.Fatalf("nine terms lost the tracker: %v", err)
	}
	if len(queries) < 2 {
		t.Fatalf("nine terms produced %d quer(ies); they cannot fit in one", len(queries))
	}
	for _, q := range queries {
		if n := strings.Count(q, " OR "); n > 5 {
			t.Errorf("a query carries %d operators, over GitHub's limit of five: %s", n, q)
		}
	}
}

// Every term is still searched for — a split that quietly dropped the tail
// would turn "no prior art" into a statement about the first six words.
func TestEveryTermReachesAQuery(t *testing.T) {
	terms := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel"}
	var all strings.Builder
	stub(t, func(args []string) ([]byte, error) {
		for i, a := range args {
			if a == "-f" && i+1 < len(args) && strings.HasPrefix(args[i+1], "q=") {
				all.WriteString(args[i+1])
				all.WriteByte('\n')
			}
		}
		return []byte(`{"total_count":0,"items":[]}`), nil
	})
	if _, _, err := Tracker("repo:o/r", terms); err != nil {
		t.Fatal(err)
	}
	for _, term := range terms {
		if !strings.Contains(all.String(), `"`+term+`"`) {
			t.Errorf("term %q was never searched for", term)
		}
	}
}

// ⛔ ONE FAILED BATCH IS THE WHOLE SURFACE UNREAD, not a partial answer.
// Returning what the other batches found would be a list that LOOKS complete
// and is not — the shape of every defect this tool exists to refuse.
func TestAFailedBatchFailsTheWholeSurface(t *testing.T) {
	terms := []string{"one", "two", "three", "four", "five", "six", "seven"}
	calls := 0
	stub(t, func(args []string) ([]byte, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("gh: Validation Failed (HTTP 422)")
		}
		return []byte(`{"total_count":1,"items":[{"number":7,"title":"one","state":"open","repository_url":"https://api.github.com/repos/o/r"}]}`), nil
	})
	items, _, err := Tracker("repo:o/r", terms)
	if err == nil {
		t.Fatalf("a failed batch was reported as a result of %d item(s)", len(items))
	}
	if items != nil {
		t.Errorf("items were returned alongside the error: %+v", items)
	}
}

// An entry matching terms in two different batches is ONE piece of prior art,
// and a reader counting rows must not be told otherwise.
func TestAnEntryMatchedTwiceIsListedOnce(t *testing.T) {
	terms := []string{"one", "two", "three", "four", "five", "six", "seven"}
	stub(t, func(args []string) ([]byte, error) {
		// Both batches return the same entry.
		return []byte(`{"total_count":1,"items":[{"number":42,"title":"shared","state":"closed","repository_url":"https://api.github.com/repos/o/r"}]}`), nil
	})
	items, _, err := Tracker("repo:o/r", terms)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		var got []string
		for _, it := range items {
			got = append(got, it.Repo+"#"+strconv.Itoa(it.Number))
		}
		t.Fatalf("one entry listed %d times: %v", len(items), got)
	}
}

// A subject that fits in one query must still make exactly ONE call: the split
// is for the subjects that need it, and a tool that doubled every search would
// have traded one defect for a rate limit.
func TestAShortSubjectStillMakesOneQuery(t *testing.T) {
	var queries int
	stub(t, func(args []string) ([]byte, error) {
		for i, a := range args {
			if a == "-f" && i+1 < len(args) && strings.HasPrefix(args[i+1], "q=") {
				queries++
			}
		}
		return []byte(`{"total_count":0,"items":[]}`), nil
	})
	if _, _, err := Tracker("repo:o/r", []string{"one", "two", "three"}); err != nil {
		t.Fatal(err)
	}
	if queries != 1 {
		t.Errorf("a three-term subject made %d queries, want 1", queries)
	}
}
