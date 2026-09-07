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
	old := backoff
	backoff = func(int) time.Duration { return 0 }
	t.Cleanup(func() { backoff = old })
}

// withRemote answers git ls-remote from a table instead of the network.
func withRemote(t *testing.T, byURL map[string]string) {
	t.Helper()
	old := lsRemote
	lsRemote = func(url string) (string, error) {
		out, ok := byURL[url]
		if !ok {
			return "", errString("repository not found")
		}
		return out, nil
	}
	t.Cleanup(func() { lsRemote = old })
}

type errString string

func (e errString) Error() string { return string(e) }

const oneOrgTwoRepos = `
case "$*" in
  *user/orgs*) echo "acme" ;;
  *orgs/acme/repos*)
    echo '[{"full_name":"acme/moved","default_branch":"main","archived":false,"fork":false},
           {"full_name":"acme/still","default_branch":"main","archived":false,"fork":false}]' ;;
`

func TestAModuleWhoseTagIsItsHeadIsNotAhead(t *testing.T) {
	// Settled without an API call: the tag and the branch point at the same
	// commit, so there is nothing a consumer cannot reach.
	withFakeGH(t, oneOrgTwoRepos+`
  *compare*) echo "9" ;;
esac`)
	withRemote(t, map[string]string{
		"https://github.com/acme/moved": "aaa\trefs/heads/main\nbbb\trefs/tags/v0.2.0\n",
		"https://github.com/acme/still": "ccc\trefs/heads/main\nccc\trefs/tags/v0.2.0\n",
	})
	var out, errOut strings.Builder
	if code := run([]string{"-orgs", "acme"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "AHEAD of their latest tag: 1") {
		t.Errorf("the summary reads:\n%s", got)
	}
	if !strings.Contains(got, "acme/moved") || strings.Contains(got, "acme/still ") {
		t.Errorf("the wrong module was named:\n%s", got)
	}
	// And what it says is what a consumer would find, not a count of commits.
	if !strings.Contains(got, "cannot reach") {
		t.Errorf("the finding is not stated in terms of consumers:\n%s", got)
	}
}

func TestAModuleWithNoTagAtAllIsNotCounted(t *testing.T) {
	// A repository that has never tagged has nothing to be ahead OF. It is not
	// a finding, and counting it as one would bury the real ones.
	withFakeGH(t, oneOrgTwoRepos+`esac`)
	withRemote(t, map[string]string{
		"https://github.com/acme/moved": "aaa\trefs/heads/main\n",
		"https://github.com/acme/still": "ccc\trefs/heads/main\n",
	})
	var out, errOut strings.Builder
	if code := run([]string{"-orgs", "acme"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "tagged: 0") {
		t.Errorf("untagged repositories were counted:\n%s", out.String())
	}
}

func TestAnAnnotatedTagIsFollowedToItsCommit(t *testing.T) {
	// An annotated tag's ref names the TAG OBJECT. Comparing that against the
	// branch head says "ahead" for a module that is exactly level.
	withFakeGH(t, oneOrgTwoRepos+`
  *compare*) echo "3" ;;
esac`)
	withRemote(t, map[string]string{
		"https://github.com/acme/moved": "aaa\trefs/heads/main\n" +
			"tagobj\trefs/tags/v1.0.0\naaa\trefs/tags/v1.0.0^{}\n",
		"https://github.com/acme/still": "ccc\trefs/heads/main\n",
	})
	var out, errOut strings.Builder
	run([]string{"-orgs", "acme"}, &out, &errOut)
	if !strings.Contains(out.String(), "AHEAD of their latest tag: 0") {
		t.Errorf("an annotated tag was read as behind:\n%s", out.String())
	}
}

func TestTheNewestTagIsTheHIGHESTAndNotTheLAST(t *testing.T) {
	for _, c := range []struct{ a, b string }{
		{"v0.10.0", "v0.9.0"}, // the lexical trap
		{"v1.0.0", "v0.99.0"}, //
		{"v0.2.10", "v0.2.9"}, //
	} {
		if !newer(c.a, c.b) {
			t.Errorf("newer(%q, %q) said no", c.a, c.b)
		}
		if newer(c.b, c.a) {
			t.Errorf("newer(%q, %q) said yes", c.b, c.a)
		}
	}
	if newer("v1.2.3", "v1.2.3") {
		t.Error("a tag is newer than itself")
	}
}

func TestWhatCountsAsAVersionGoWouldPick(t *testing.T) {
	for tag, want := range map[string]bool{
		"v1.2.3": true, "v0.0.1": true, "v1.2.3-rc1": true, "v1.2.3+meta": true,
		"1.2.3": false, "v1.2": false, "vX.Y.Z": false, "latest": false,
		"v1.2.": false, "v..": false,
	} {
		if got := semverish(tag); got != want {
			t.Errorf("semverish(%q) = %v", tag, got)
		}
	}
}

func TestAnOrganisationThatCouldNotBeListedIsNotAnEmptyOne(t *testing.T) {
	withFakeGH(t, `
case "$*" in
  *orgs/acme/repos*) echo "API rate limit exceeded for user ID 1." >&2; exit 1 ;;
esac`)
	var out, errOut strings.Builder
	code := run([]string{"-orgs", "acme"}, &out, &errOut)
	if code == 0 {
		t.Errorf("an incomplete pass exited 0:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "INCOMPLETE") || !strings.Contains(out.String(), "acme") {
		t.Errorf("the gap is not named:\n%s", out.String())
	}
}

func TestATagThatCouldNotBeReadIsNotAModuleThatIsLevel(t *testing.T) {
	withFakeGH(t, oneOrgTwoRepos+`esac`)
	withRemote(t, map[string]string{}) // every ls-remote fails
	var out, errOut strings.Builder
	code := run([]string{"-orgs", "acme"}, &out, &errOut)
	if code == 0 {
		t.Errorf("a pass that read no tags exited 0:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "repositories whose tags could not be read") {
		t.Errorf("the gap is not named:\n%s", out.String())
	}
}

func TestWithoutTheOrganisationsThereIsNothingToSweep(t *testing.T) {
	withFakeGH(t, `echo "gh: not logged in" >&2; exit 1`)
	var out, errOut strings.Builder
	if code := run(nil, &out, &errOut); code == 0 {
		t.Error("a pass that could not list the organisations exited 0")
	}
	if !strings.Contains(errOut.String(), "orgs:") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestNarrowingSaysItIsNarrowed(t *testing.T) {
	// A pass over some organisations must not read as a pass over the fleet.
	withFakeGH(t, oneOrgTwoRepos+`esac`)
	withRemote(t, map[string]string{
		"https://github.com/acme/moved": "aaa\trefs/heads/main\n",
		"https://github.com/acme/still": "ccc\trefs/heads/main\n",
	})
	var out, errOut strings.Builder
	run([]string{"-orgs", "acme"}, &out, &errOut)
	if !strings.Contains(out.String(), "NARROWED") {
		t.Errorf("a narrowed pass did not say so:\n%s", out.String())
	}
}

func TestTheTwoRateLimitsWantOppositeTreatment(t *testing.T) {
	for msg, want := range map[string]bool{
		"You have exceeded a secondary rate limit":     true,
		"was submitted too quickly":                    true,
		"API rate limit exceeded for user ID 11405852": false,
		"gh: not logged in":                            false,
	} {
		if got := retryable(msg); got != want {
			t.Errorf("retryable(%q) = %v", msg, got)
		}
	}
}

func TestAComparisonThatDoesNotAnswerWithANumber(t *testing.T) {
	// gh answering something that is not a count is a read that failed, not a
	// module that is level.
	withFakeGH(t, oneOrgTwoRepos+`
  *compare*) echo "not a number" ;;
esac`)
	withRemote(t, map[string]string{
		"https://github.com/acme/moved": "aaa\trefs/heads/main\nbbb\trefs/tags/v1.0.0\n",
		"https://github.com/acme/still": "ccc\trefs/heads/main\nccc\trefs/tags/v1.0.0\n",
	})
	var out, errOut strings.Builder
	if code := run([]string{"-orgs", "acme"}, &out, &errOut); code == 0 {
		t.Errorf("an unreadable comparison exited 0:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "INCOMPLETE") {
		t.Errorf("it was not reported:\n%s", out.String())
	}
}

func TestALongListOfFailuresIsTruncatedRatherThanPrintedWhole(t *testing.T) {
	var w strings.Builder
	items := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	report(&w, "things", items, 100)
	got := w.String()
	if !strings.Contains(got, "8 of 100 things") {
		t.Errorf("the count is wrong:\n%s", got)
	}
	if !strings.Contains(got, "... and 3 more") {
		t.Errorf("the list was not truncated after five:\n%s", got)
	}
	var empty strings.Builder
	report(&empty, "things", nil, 100)
	if empty.String() != "" {
		t.Errorf("nothing to report printed %q", empty.String())
	}
}

func TestKeepingAnErrorReadableInAList(t *testing.T) {
	long := strings.Repeat("x", 200)
	if got := firstLine(long); len(got) > 95 {
		t.Errorf("a %d-character reason would push the others off the screen", len(got))
	}
	if got := firstLine("first\nsecond"); got != "first" {
		t.Errorf("firstLine kept %q", got)
	}
	if got := firstLine("  short  "); got != "short" {
		t.Errorf("firstLine(short) = %q", got)
	}
}
