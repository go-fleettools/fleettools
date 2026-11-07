package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
)

// stub replaces the GitHub call with a function of the argument list, and
// restores the real one afterwards.
func stub(t *testing.T, f func(args []string) ([]byte, error)) *[][]string {
	t.Helper()
	saved := gh
	var calls [][]string
	gh = func(args ...string) ([]byte, error) {
		calls = append(calls, args)
		return f(args)
	}
	t.Cleanup(func() { gh = saved })
	return &calls
}

func drive(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := cli.Drive(RootCmd(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

const emptyGraphQL = `{"data":{"repository":{"refs":{"nodes":[]}}}}`
const emptySearch = `{"items":[]}`

func answer(args []string) ([]byte, error) {
	if len(args) > 1 && args[1] == "graphql" {
		return []byte(emptyGraphQL), nil
	}
	return []byte(emptySearch), nil
}

// ⛔⛔ THE TEST THIS COMMAND EXISTS FOR.
//
// The two branches that already held the work were `unrounded-space` and
// `space-refresh`; the subject in hand was "unrounded space width". A search
// for that phrase finds NEITHER — which is why the work was done twice. If this
// test ever goes green on a phrase-only match, the gate is decoration.
func TestTheBranchesThatWereMissedAreFound(t *testing.T) {
	stub(t, func(args []string) ([]byte, error) {
		if args[1] == "graphql" {
			return []byte(`{"data":{"repository":{"refs":{"nodes":[
				{"name":"unrounded-space","target":{"messageHeadline":"font: do not round the size"}},
				{"name":"space-refresh","target":{"messageHeadline":"NOT MERGED - see #352"}},
				{"name":"main","target":{"messageHeadline":"packages: 11pt is 10.95"}}
			]}}}}`), nil
		}
		return []byte(emptySearch), nil
	})

	code, out, _ := drive(t, "-repo", "go-tex/engine", "unrounded space width")
	if code != 1 {
		t.Fatalf("status %d, want 1 (prior art found)\n%s", code, out)
	}
	for _, want := range []string{"unrounded-space", "space-refresh"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not name %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\n    main ") {
		t.Errorf("matched an unrelated branch:\n%s", out)
	}
}

// A branch is named for its mechanism and its commit for the symptom, so the
// head-commit subject is a surface of its own and not a decoration.
func TestABranchIsFoundByItsCommitSubjectAlone(t *testing.T) {
	stub(t, func(args []string) ([]byte, error) {
		if args[1] == "graphql" {
			return []byte(`{"data":{"repository":{"refs":{"nodes":[
				{"name":"wip-3","target":{"messageHeadline":"footnote: pack the text block to vsize"}}
			]}}}}`), nil
		}
		return []byte(emptySearch), nil
	})
	code, out, _ := drive(t, "-repo", "go-tex/engine", "vsize")
	if code != 1 {
		t.Fatalf("status %d, want 1; the commit subject was not read\n%s", code, out)
	}
	if !strings.Contains(out, "wip-3") {
		t.Errorf("output does not name the branch:\n%s", out)
	}
}

// ⛔ A closed entry is the most valuable prior art there is, and the query must
// not exclude it.
func TestTheSearchDoesNotRestrictItselfToOpenEntries(t *testing.T) {
	calls := stub(t, answer)
	drive(t, "-repo", "go-tex/engine", "running head")
	var q string
	for _, c := range *calls {
		for _, a := range c {
			if strings.HasPrefix(a, "q=") {
				q = a
			}
		}
	}
	if q == "" {
		t.Fatal("no search query was sent")
	}
	if strings.Contains(q, "is:open") {
		t.Errorf("the query excludes closed entries, which is the one search that misses the answer: %s", q)
	}
	if !strings.Contains(q, "repo:go-tex/engine") {
		t.Errorf("the query is not scoped to the repository: %s", q)
	}
}

func TestAClosedPullRequestIsReportedAsPriorArt(t *testing.T) {
	stub(t, func(args []string) ([]byte, error) {
		if args[1] == "graphql" {
			return []byte(emptyGraphQL), nil
		}
		return []byte(`{"items":[{"number":352,"title":"Space width is rounded","state":"closed",
			"body":"NOT MERGED - see the measurement","repository_url":"https://api.github.com/repos/go-tex/engine",
			"pull_request":{"url":"x"}}]}`), nil
	})
	code, out, _ := drive(t, "-repo", "go-tex/engine", "space width")
	if code != 1 {
		t.Fatalf("status %d, want 1\n%s", code, out)
	}
	for _, want := range []string{"#352", "closed", "pr"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not say %q:\n%s", want, out)
		}
	}
}

// ⛔⛔ AN ANSWER NOT OBTAINED IS NOT A NEGATIVE ANSWER. A gate that opens when a
// call fails opens precisely when nobody can check it.
func TestAnUnreadableSurfaceHoldsTheGateShut(t *testing.T) {
	stub(t, func(args []string) ([]byte, error) {
		if args[1] == "graphql" {
			return nil, errors.New("no route to host")
		}
		return []byte(emptySearch), nil
	})
	code, out, _ := drive(t, "-repo", "go-tex/engine", "running head")
	if code != 2 {
		t.Fatalf("status %d, want 2 (a surface could not be read)\n%s", code, out)
	}
	if !strings.Contains(out, "NOT READ") {
		t.Errorf("output does not say the surface was unread:\n%s", out)
	}
}

// ⛔ A GraphQL refusal arrives with HTTP 200 and an `errors` array. Read past it
// and a refused query becomes an empty branch list: a clean gate on a surface
// never read.
func TestAGraphqlErrorIsNotAnEmptyBranchList(t *testing.T) {
	stub(t, func(args []string) ([]byte, error) {
		if args[1] == "graphql" {
			return []byte(`{"data":{"repository":null},"errors":[{"message":"Could not resolve to a Repository"}]}`), nil
		}
		return []byte(emptySearch), nil
	})
	code, out, _ := drive(t, "-repo", "go-tex/typo", "running head")
	if code != 2 {
		t.Fatalf("status %d, want 2; a 200 with errors was read as an empty list\n%s", code, out)
	}
	if !strings.Contains(out, "Could not resolve") {
		t.Errorf("the refusal's reason is not reported:\n%s", out)
	}
}

// ⛔⛔ A CAP IS ONLY SAFE WHEN SOMETHING CHECKS WHETHER IT WAS REACHED.
//
// The first repository this gate was pointed at, go-tex/engine, has 93 branches
// against a page size of 100. At 101 a single-page query would have answered
// cleanly and seven branches short, and the gate would have opened on a surface
// it had not finished reading. Both halves are pinned here: the second page is
// fetched, and a page that claims more without handing back a cursor is an
// unread surface rather than the end of one.
func TestTheBranchListIsReadToItsEnd(t *testing.T) {
	calls := stub(t, func(args []string) ([]byte, error) {
		if args[1] != "graphql" {
			return []byte(emptySearch), nil
		}
		for _, a := range args {
			if a == "after=CURSOR1" {
				return []byte(`{"data":{"repository":{"refs":{
					"pageInfo":{"hasNextPage":false,"endCursor":""},
					"nodes":[{"name":"space-refresh","target":{"messageHeadline":"x"}}]}}}}`), nil
			}
		}
		return []byte(`{"data":{"repository":{"refs":{
			"pageInfo":{"hasNextPage":true,"endCursor":"CURSOR1"},
			"nodes":[{"name":"unrelated","target":{"messageHeadline":"y"}}]}}}}`), nil
	})
	code, out, _ := drive(t, "--repo", "go-tex/engine", "space")
	if code != 1 {
		t.Fatalf("status %d, want 1 — the branch on the SECOND page was missed\n%s", code, out)
	}
	if !strings.Contains(out, "space-refresh") {
		t.Errorf("the second page was not read:\n%s", out)
	}
	graph := 0
	for _, c := range *calls {
		if len(c) > 1 && c[1] == "graphql" {
			graph++
		}
	}
	if graph != 2 {
		t.Errorf("%d GraphQL calls, want 2 (one per page)", graph)
	}
}

func TestMorePagesWithoutACursorIsAnUnreadSurface(t *testing.T) {
	stub(t, func(args []string) ([]byte, error) {
		if args[1] != "graphql" {
			return []byte(emptySearch), nil
		}
		return []byte(`{"data":{"repository":{"refs":{
			"pageInfo":{"hasNextPage":true,"endCursor":""},
			"nodes":[{"name":"unrelated","target":{"messageHeadline":"y"}}]}}}}`), nil
	})
	code, out, _ := drive(t, "--repo", "go-tex/engine", "space")
	if code != 2 {
		t.Fatalf("status %d, want 2; a list that could not be finished read as finished\n%s", code, out)
	}
	if !strings.Contains(out, "no cursor") {
		t.Errorf("the reason is not reported:\n%s", out)
	}
}

// ⛔ THE DEFAULT BRANCH IS THE STRONGEST PRIOR ART AND THE WEAKEST WARNING.
//
// The first real run of the released binary matched `main`, whose head commit was
// the subject itself, and printed "a pushed branch with no pull request is
// INVISIBLE work" under it — exactly backwards: that work is merged and visible.
// A gate whose loudest line is wrong about the first hit it shows is one the
// reader learns to skim, and then it is not a control.
func TestTheDefaultBranchIsCalledMergedAndNotInvisible(t *testing.T) {
	answerWith := func(nodes string) func([]string) ([]byte, error) {
		return func(args []string) ([]byte, error) {
			if args[1] != "graphql" {
				return []byte(emptySearch), nil
			}
			return []byte(`{"data":{"repository":{"defaultBranchRef":{"name":"main"},
				"refs":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[` + nodes + `]}}}}`), nil
		}
	}

	// Only the default branch matched: no "invisible work" line at all.
	stub(t, answerWith(`{"name":"main","target":{"messageHeadline":"priorart: the gate"}}`))
	_, out, _ := drive(t, "--repo", "go-fleettools/fleettools", "priorart")
	if !strings.Contains(out, "already MERGED") {
		t.Errorf("the default branch is not called merged:\n%s", out)
	}
	if strings.Contains(out, "INVISIBLE") {
		t.Errorf("a merged branch was called invisible work:\n%s", out)
	}

	// A side branch matched too: both lines, and only the default one marked.
	stub(t, answerWith(`{"name":"main","target":{"messageHeadline":"priorart: the gate"}},
		{"name":"priorart-wip","target":{"messageHeadline":"x"}}`))
	_, out, _ = drive(t, "--repo", "go-fleettools/fleettools", "priorart")
	if !strings.Contains(out, "already MERGED") || !strings.Contains(out, "INVISIBLE") {
		t.Errorf("one of the two lines is missing:\n%s", out)
	}
	if !strings.Contains(out, "✓ main") || strings.Contains(out, "✓ priorart-wip") {
		t.Errorf("the mark is on the wrong branch:\n%s", out)
	}
}

func TestACleanSubjectPasses(t *testing.T) {
	stub(t, answer)
	code, out, _ := drive(t, "-repo", "go-tex/engine", "quaternion shading")
	if code != 0 {
		t.Fatalf("status %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "no prior art") {
		t.Errorf("output does not state the clean result:\n%s", out)
	}
}

// ⛔ A surface skipped for cost must be named as skipped. Otherwise a status of 0
// reads as "and the branches were clean too", which is the same error as reading
// an unread answer as a negative one.
func TestOrgModeSaysWhichBranchesItDidNotRead(t *testing.T) {
	savedRepos := repos
	repos = func(org string) ([]fleet.Repo, error) {
		return []fleet.Repo{{FullName: org + "/engine"}, {FullName: org + "/docs"}}, nil
	}
	t.Cleanup(func() { repos = savedRepos })
	calls := stub(t, answer)

	code, out, _ := drive(t, "-org", "go-tex", "running head")
	if code != 0 {
		t.Fatalf("status %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "org:go-tex") {
		t.Errorf("the org-wide tracker search is not reported:\n%s", out)
	}
	if !strings.Contains(out, "NOT read") || !strings.Contains(out, "does not cover them") {
		t.Errorf("output does not name the branches it skipped:\n%s", out)
	}
	for _, c := range *calls {
		if len(c) > 1 && c[1] == "graphql" {
			t.Errorf("branches were read without -branches: %v", c)
		}
	}
}

func TestOrgModeWithBranchesReadsEveryRepository(t *testing.T) {
	savedRepos := repos
	repos = func(org string) ([]fleet.Repo, error) {
		return []fleet.Repo{{FullName: org + "/engine"}, {FullName: org + "/docs"}}, nil
	}
	t.Cleanup(func() { repos = savedRepos })
	calls := stub(t, answer)

	if code, out, _ := drive(t, "-org", "go-tex", "-branches", "running head"); code != 0 {
		t.Fatalf("status %d, want 0\n%s", code, out)
	}
	graph := 0
	for _, c := range *calls {
		if len(c) > 1 && c[1] == "graphql" {
			graph++
		}
	}
	if graph != 2 {
		t.Errorf("%d branch calls, want 2 (one per repository)", graph)
	}
}

// -report is for a caller that wants to look rather than be stopped; it must
// still print the findings.
func TestReportPrintsTheFindingAndExitsZero(t *testing.T) {
	stub(t, func(args []string) ([]byte, error) {
		if args[1] == "graphql" {
			return []byte(`{"data":{"repository":{"refs":{"nodes":[
				{"name":"space-refresh","target":{"messageHeadline":"x"}}]}}}}`), nil
		}
		return []byte(emptySearch), nil
	})
	code, out, _ := drive(t, "-repo", "go-tex/engine", "-report", "space")
	if code != 0 {
		t.Fatalf("status %d, want 0 under -report\n%s", code, out)
	}
	if !strings.Contains(out, "space-refresh") {
		t.Errorf("-report hid the finding:\n%s", out)
	}
}

func TestUsageRefusals(t *testing.T) {
	stub(t, answer)
	if code, _, _ := drive(t, "running head"); code != 2 {
		t.Errorf("no repository at all: status %d, want 2", code)
	}
	if code, _, _ := drive(t, "-repo", "engine", "running head"); code != 2 {
		t.Errorf("a repository without an owner: status %d, want 2", code)
	}
	if code, _, _ := drive(t, "-repo", "go-tex/engine", "---"); code != 2 {
		t.Errorf("a subject with no word: status %d, want 2", code)
	}
}

// ⛔⛔ GITHUB'S ORDER IS KEPT, AND THE CUT STATES THE TOTAL.
//
// A first version re-ranked the entries by a score of its own, so that a gate
// printing 25 lines would lead with the strongest. Two real runs on
// go-tex/engine for "unrounded space width" measured it:
//
//	GitHub's order   #352, #612, #722 in the first six of 238
//	the score        #95, #476, #177 first — #352 fourth
//
// #95's title holds two of the terms and #352's holds one, so the score was
// right about the words and wrong about the subject. This test pins the order to
// the one that was measured better, and the cut line to the search's OWN total:
// a list truncated at -n without saying so is a population nobody can see, and
// the entry that holds the answer may be the one cut.
func TestTheSearchesOwnOrderIsKeptAndTheCutStatesTheTotal(t *testing.T) {
	stub(t, func(args []string) ([]byte, error) {
		if args[1] == "graphql" {
			return []byte(emptyGraphQL), nil
		}
		return []byte(`{"total_count":238,"items":[
			{"number":352,"title":"The interword space is rounded","state":"open",
			 "body":"x","repository_url":"https://api.github.com/repos/go-tex/engine"},
			{"number":95,"title":"adopt unrounded advance (remove width inflation)","state":"closed",
			 "body":"y","repository_url":"https://api.github.com/repos/go-tex/engine"}
		]}`), nil
	})
	code, out, errOut := drive(t, "--repo", "go-tex/engine", "-n", "1", "unrounded space width")
	if code != 1 {
		t.Fatalf("status %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "#352") {
		t.Errorf("the first entry the search returned is not the one shown:\n%s", out)
	}
	if strings.Contains(out, "#95") {
		t.Errorf("the order was changed; two measured runs say that is worse:\n%s", out)
	}
	// 238 is the search's own total_count, 2 the page it returned, 1 what was
	// shown — and none of the three is inferred from another.
	if !strings.Contains(out, "and 237 more") || !strings.Contains(out, "1 of 238") {
		t.Errorf("the cut does not state the total:\n%s", out)
	}
}

func TestTheMatchedTermIsNamedBesideEachEntry(t *testing.T) {
	stub(t, func(args []string) ([]byte, error) {
		if args[1] == "graphql" {
			return []byte(emptyGraphQL), nil
		}
		return []byte(`{"total_count":2,"items":[
			{"number":1,"title":"The interword space is rounded","state":"open","body":"x",
			 "repository_url":"https://api.github.com/repos/go-tex/engine"},
			{"number":2,"title":"Unrelated","state":"open","body":"also unrelated",
			 "repository_url":"https://api.github.com/repos/go-tex/engine"}
		]}`), nil
	})
	_, out, _ := drive(t, "--repo", "go-tex/engine", "space width")
	if !strings.Contains(out, "(space)") {
		t.Errorf("the term that matched is not named:\n%s", out)
	}
	// Search reads comments and this reads title and body: an entry it cannot
	// account for is labelled, not dropped and not claimed to match a word.
	if !strings.Contains(out, "(comment)") {
		t.Errorf("an entry matched outside title and body is not labelled:\n%s", out)
	}
}

func TestTerms(t *testing.T) {
	got := Terms("Unrounded Space Width")
	want := []string{"unrounded space width", "unrounded", "space", "width"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Terms = %v, want %v", got, want)
	}
	// Short words and words that would match half a tracker are dropped.
	for _, s := range []string{"fix the pt", "that with when"} {
		if ts := Terms(s); len(ts) != 1 {
			t.Errorf("Terms(%q) = %v, want the phrase alone", s, ts)
		}
	}
	if ts := Terms("   "); ts != nil {
		t.Errorf("Terms(blank) = %v, want nil", ts)
	}
}

func TestRepoOf(t *testing.T) {
	if got := repoOf("https://api.github.com/repos/go-tex/engine"); got != "go-tex/engine" {
		t.Errorf("repoOf = %q", got)
	}
}
