package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/go-gitsafe/gitsafe/issuegate"
)

// ⛔ THIS COMMAND IS A PARSER, and CLAUDE.md asks every parser for a fuzz
// target. It had none, although both of its inputs come from outside: a REST
// timeline (JSON, paginated, with fields that may be null or absent) and the
// BODY of a pull request, which anybody who can open one writes. Since
// 2026-11-22 that body also decides a THIRD class, so what it says now changes
// what the report says about an issue.
//
// `openfixed` writes nothing to disk and runs no shell, so the harm a bad body
// can do is not a crash but a WRONG VERDICT: an issue reported settled that
// nobody settled, or the reverse. Those are the properties asserted here.

// FuzzParseTimelineNeverPanics drives the JSON reader with arbitrary bytes. An
// error is a perfectly good answer -- the point is that there is always an
// answer, and that a page it accepted can be read again.
func FuzzParseTimelineNeverPanics(f *testing.F) {
	f.Add("[[" + xref("o/r", 9, mergedAt, "Closes #5") + "]]")
	f.Add("[" + xref("o/r", 9, mergedAt, "Refs #5") + "]")
	f.Add(`[[{"event":"connected"}]]`)
	f.Add(`[[{"event":"cross-referenced","source":{"issue":null}}]]`)
	f.Add(`[[{"event":"cross-referenced","source":{"issue":{"number":9,"pull_request":{"merged_at":""},"repository":{"full_name":"o/r"}}}}]]`)
	f.Add(`[[{"event":"reopened","created_at":"not a date"}]]`)
	f.Add(`[]`)
	f.Add(`nope`)

	f.Fuzz(func(t *testing.T, raw string) {
		evs, err := parseTimeline([]byte(raw))
		if err != nil {
			if evs != nil {
				t.Fatalf("both an error and %d event(s)", len(evs))
			}
			return
		}
		// A classification of what it read must not depend on reading it twice.
		c1, prs1 := Classify("o/r", 5, evs)
		c2, prs2 := Classify("o/r", 5, evs)
		if c1 != c2 || len(prs1) != len(prs2) {
			t.Fatalf("not a function of its input: %q/%d then %q/%d", c1, len(prs1), c2, len(prs2))
		}
		switch c1 {
		case None, Closable, Review, Stated:
		default:
			t.Fatalf("unknown class %q", c1)
		}
		if (c1 == None) != (len(prs1) == 0) {
			t.Fatalf("class %q with %d pull request(s)", c1, len(prs1))
		}
	})
}

// FuzzAClassNeverContradictsTheBodyItRead fuzzes the PULL REQUEST BODY, which
// is the input a stranger controls, rather than the JSON around it: a mutated
// byte stream almost never lands inside a quoted body, so fuzzing the envelope
// would explore the envelope and nothing else.
//
// The invariants are the three sentences the report makes about an issue, each
// written so that it fails if the class does not match what the body SAYS:
//
//  1. ⛔ CLOSABLE is never claimed without a closing keyword or a `connected`
//     event. This is the one with teeth: CLOSABLE is what `--close` acts on, so
//     a false CLOSABLE closes an open issue nobody finished.
//  2. ⛔ REFS is never claimed without a `Refs` in the body, and never beside a
//     closing keyword -- Closes outranks Refs, so the two can never both be
//     the verdict.
//  3. ⛔ REVIEW means the body classified NOTHING: if the class is REVIEW and
//     the issue was not reopened after the merge, the gate's parser must agree
//     that the body holds neither keyword for this issue.
//
// Each is compared against issuegate.Parse -- the MERGE gate's own reading --
// so an answer this command gives can never disagree with the rule the pull
// request was merged under.
func FuzzAClassNeverContradictsTheBodyItRead(f *testing.F) {
	f.Add("Closes #5", false, false)
	f.Add("Refs #5", false, false)
	f.Add("see #5", false, false)
	f.Add("Refs #4, #5 and #6", false, false)
	f.Add("## Also filed, not fixed\n\no/r#5: not done.", false, false)
	f.Add("`fixes #5` is the syntax", false, false)
	f.Add("```\nCloses #5\n```", true, false)
	f.Add("Closes #5", false, true)
	f.Add("fixed https://github.com/o/r/issues/5", false, false)

	f.Fuzz(func(t *testing.T, body string, connected, reopenedAfter bool) {
		// ⛔ THE BODY IS WRITTEN WITH A JSON ENCODER AND READ BACK, not
		// formatted with %q as the hand-written fixtures do. The first version
		// of this target used `xref`, whose %q escapes a control byte as
		// \xNN -- which JSON cannot read -- so the fuzzer produced a body that
		// is perfectly valid UTF-8 and an UNPARSEABLE fixture, and the failure
		// was in the test rather than in the command. Round-tripping also
		// yields the exact string the parser will hand to Classify, so the
		// gate below is asked about THAT and not about the input: an encoder
		// that replaced something must not make the two disagree.
		quoted, err := json.Marshal(body)
		if err != nil {
			return
		}
		var seen string
		if err := json.Unmarshal(quoted, &seen); err != nil {
			t.Fatalf("a string this encoder wrote is unreadable: %v", err)
		}
		raw := "[[" + fmt.Sprintf(`{"event":"cross-referenced","source":{"type":"issue","issue":{"number":9,"body":%s,"pull_request":{"merged_at":%q},"repository":{"full_name":"o/pr"}}}}`, quoted, mergedAt) + "]"
		if connected {
			raw += `,[{"event":"connected"}]`
		}
		if reopenedAfter {
			raw += `,[{"event":"reopened","created_at":"2026-11-02T00:00:00Z"}]`
		}
		raw += "]"
		evs, err := parseTimeline([]byte(raw))
		if err != nil {
			t.Fatalf("fixture unparseable: %v", err)
		}
		class, prs := Classify("o/r", 5, evs)
		if len(prs) != 1 {
			t.Fatalf("%d pull request(s), want the one in the fixture", len(prs))
		}

		// The merge gate's own reading of the same body, for the same issue.
		kind := issuegate.Bare
		for _, r := range issuegate.Parse(seen, "o/pr") {
			if strings.EqualFold(r.Repo, "o/r") && r.Number == 5 {
				kind = r.Kind
			}
		}

		switch class {
		case Closable:
			if kind != issuegate.Closes && !connected {
				t.Fatalf("CLOSABLE with no closing keyword and no connected event; the gate reads %v in:\n%q", kind, body)
			}
			if reopenedAfter {
				t.Fatalf("CLOSABLE although it was reopened after the merge:\n%q", body)
			}
		case Stated:
			if kind != issuegate.Refs {
				t.Fatalf("REFS although the gate reads %v in:\n%q", kind, body)
			}
			if connected || reopenedAfter {
				t.Fatalf("REFS although connected=%v reopenedAfter=%v:\n%q", connected, reopenedAfter, body)
			}
		case Review:
			if !reopenedAfter && kind != issuegate.Bare {
				t.Fatalf("REVIEW although the gate reads %v in:\n%q", kind, seen)
			}
		default:
			t.Fatalf("class %q for a merged cross-reference:\n%q", class, seen)
		}
	})
}
