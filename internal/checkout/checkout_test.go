package checkout

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// clone builds a real repository with a real remote, so the distance measured
// is git's own and not a number this test and the code agree on.
func clone(t *testing.T, commitsAhead int) string {
	t.Helper()
	base := t.TempDir()
	origin, work := filepath.Join(base, "origin"), filepath.Join(base, "clone")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	run(origin, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "a.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(origin, "add", "-A")
	run(origin, "commit", "-qm", "first")
	run(base, "clone", "-q", origin, work)
	for i := range commitsAhead {
		if err := os.WriteFile(filepath.Join(origin, "a.txt"), []byte{byte('a' + i)}, 0o644); err != nil {
			t.Fatal(err)
		}
		run(origin, "commit", "-qam", "later")
	}
	// A FETCH, not a pull: it refreshes FETCH_HEAD and the remote refs and
	// leaves the working tree where it was. That state is the whole defect.
	run(work, "fetch", "-q", "origin")
	return work
}

func TestStalenessCountsCommitsAndNotMinutes(t *testing.T) {
	behind, age, ok := Staleness(clone(t, 3))
	if !ok {
		t.Fatal("a clone that has fetched reported unknown")
	}
	if behind != 3 {
		t.Errorf("behind = %d, want 3", behind)
	}
	// The age says "fresh", which is exactly why it is the wrong signal.
	if age > time.Hour {
		t.Errorf("the fetch was seconds ago and the age reads %v", age)
	}
}

func TestACloneLevelWithItsRemoteIsNotBehind(t *testing.T) {
	behind, _, ok := Staleness(clone(t, 0))
	if !ok {
		t.Fatal("a clone that has fetched reported unknown")
	}
	if behind != 0 {
		t.Errorf("a clone level with its remote reads %d behind", behind)
	}
}

// Unknown must not read as up to date: that is how a directory this cannot
// measure would slip through looking fresh.
func TestSomethingThatIsNotARepositoryIsUnknown(t *testing.T) {
	if _, _, ok := Staleness(t.TempDir()); ok {
		t.Error("a directory with no .git reported a known staleness")
	}
}

// The failure that matters is the quiet one: an unanswered question must never
// read as "not ours", because that deletes a real finding — and the minutes
// when the API refuses are the minutes a sweep is running.
func TestWhyNotOursOnlyAnswersWhenItWasAsked(t *testing.T) {
	for name, c := range map[string]struct {
		in   Ownership
		want string
	}{
		"a fork":                     {Ownership{Fork: true, Asked: true}, "a fork"},
		"not on GitHub":              {Ownership{Missing: true, Asked: true}, "not on GitHub"},
		"ours":                       {Ownership{Asked: true}, ""},
		"the question failed":        {Ownership{}, ""},
		"failed, looked like a fork": {Ownership{Fork: true}, ""},
	} {
		if got := WhyNotOurs(c.in); got != c.want {
			t.Errorf("%s: WhyNotOurs = %q, want %q", name, got, c.want)
		}
	}
}

func TestOursKeepsWhatItCannotAskAbout(t *testing.T) {
	silent := func(string) Ownership { return Ownership{} }

	keep, dropped := Ours([]string{"someone/else"}, nil, false, silent)
	if len(keep) != 1 || len(dropped) != 0 {
		t.Errorf("an unanswered question dropped a finding: keep=%v dropped=%v", keep, dropped)
	}

	owners := map[string]bool{"go-encryptions": true}
	keep, dropped = Ours([]string{"usbarmory/tamago"}, owners, true, silent)
	if len(keep) != 0 || len(dropped) != 1 {
		t.Errorf("a checkout under another account was kept: keep=%v dropped=%v", keep, dropped)
	}

	keep, dropped = Ours([]string{"go-encryptions/ccm"}, owners, true,
		func(string) Ownership { return Ownership{Fork: true, Asked: true} })
	if len(keep) != 0 || len(dropped) != 1 {
		t.Errorf("a fork in one of our own organisations was kept: keep=%v dropped=%v", keep, dropped)
	}

	keep, dropped = Ours([]string{"go-encryptions/ccm"}, owners, true,
		func(string) Ownership { return Ownership{Asked: true} })
	if len(keep) != 1 || len(dropped) != 0 {
		t.Errorf("one of ours was dropped: keep=%v dropped=%v", keep, dropped)
	}
}

func TestOwnerOf(t *testing.T) {
	for in, want := range map[string]string{
		"go-encryptions/ccm": "go-encryptions",
		"tannevaled/hcl":     "tannevaled",
		"nameonly":           "nameonly",
	} {
		if got := OwnerOf(in); got != want {
			t.Errorf("OwnerOf(%q) = %q, want %q", in, got, want)
		}
	}
}
