package checkout

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// ⛔ TestZeroBehindIsNotAlwaysCurrent pins the case that produced this:
// go-compressions/compress, 36 minutes after its last fetch, reported 0 behind
// and was 1 behind. The scanner printed it as a finding on a tree whose fix
// was already merged, and said nothing about the tree being old.
func TestZeroBehindIsNotAlwaysCurrent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		behind int
		age    time.Duration
		known  bool
		want   Verdict
	}{
		{"fetched minutes ago, nothing upstream", 0, 5 * time.Minute, true, Current},
		{"the witness: 36 minutes, looked current", 0, 36 * time.Minute, true, Current},
		{"stopped asking two hours ago", 0, 2 * time.Hour, true, Unknown},
		{"never fetched at all", 0, 0, false, Unknown},
		{"behind, and recently asked", 3, time.Minute, true, Behind},
		// ⛔ Behind wins over Unknown: a distance already known to be non-zero
		// is a fact, and an old fetch can only make it larger.
		{"behind, and asked long ago", 3, 48 * time.Hour, true, Behind},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Judge(tc.behind, tc.age, tc.known); got != tc.want {
				t.Errorf("Judge(%d, %v, %v) = %v, want %v", tc.behind, tc.age, tc.known, got, tc.want)
			}
		})
	}
}

// TestTheWarningSaysNothingWhenThereIsNothingToSay, and says both halves when
// there are both. A warning that fires on a clean fleet is a warning nobody
// reads by the third run.
func TestTheWarningSaysNothingWhenThereIsNothingToSay(t *testing.T) {
	if s := StalenessWarning(nil, false); s != "" {
		t.Errorf("warned about an empty list: %q", s)
	}
	fresh := []Age{{Behind: 0, FetchAge: time.Minute, FetchKnown: true}}
	if s := StalenessWarning(fresh, false); s != "" {
		t.Errorf("warned about a current checkout: %q", s)
	}
	both := []Age{
		{Behind: 4, FetchAge: time.Minute, FetchKnown: true},
		{Behind: 0, FetchAge: 9 * time.Hour, FetchKnown: true},
	}
	s := StalenessWarning(both, false)
	if !strings.Contains(s, "BEHIND its remote") {
		t.Errorf("lost the behind half: %q", s)
	}
	if !strings.Contains(s, "not FETCHED") {
		t.Errorf("lost the unknown half: %q", s)
	}
	// ⛔ With confirmed=true the sentence must NOT tell the reader to pull and
	// re-run, because the caller already re-derived. A warning that asks for
	// work already done teaches people to ignore warnings.
	c := StalenessWarning(both, true)
	if strings.Contains(c, "RE-RUN this") {
		t.Errorf("still asked for a re-run after re-derivation: %q", c)
	}
	if !strings.Contains(c, "RE-DERIVED") {
		t.Errorf("did not say the finding was re-derived: %q", c)
	}
}

// gitIn runs git in dir, with an environment that ignores the machine's own
// git configuration -- otherwise the test measures the user's setup.
func gitIn(t *testing.T, dir string, args ...string) {
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

// ⛔ TestRefreshMeasuresWhatAHorizonCannotKnow is the test the 36-minute
// witness asks for: a clone that fetched a moment ago, a commit landing
// upstream AFTER that, and the remembered distance still 0. No interval can
// tell that apart from a repository with nothing waiting for it.
func TestRefreshMeasuresWhatAHorizonCannotKnow(t *testing.T) {
	base := t.TempDir()
	origin, work := filepath.Join(base, "origin"), filepath.Join(base, "clone")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, origin, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "a.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, origin, "add", "-A")
	gitIn(t, origin, "commit", "-qm", "first")
	gitIn(t, base, "clone", "-q", origin, work)
	gitIn(t, work, "fetch", "-q", "origin")

	// Upstream moves AFTER the clone last looked.
	gitIn(t, origin, "commit", "-q", "--allow-empty", "-m", "landed after you looked")

	behind, age, ok := Staleness(work)
	if !ok {
		t.Fatal("Staleness could not read the checkout")
	}
	if behind != 0 {
		t.Fatalf("the remembered distance was %d; this test needs it to be 0", behind)
	}
	if age > FetchHorizon {
		t.Fatalf("fetch age %v is already past the horizon; this test needs it inside", age)
	}
	if v := Judge(behind, age, ok); v != Current {
		t.Fatalf("Judge said %v; the point of this test is that it says Current here", v)
	}

	// And the fetch says what no horizon could have known.
	got, ok := Refresh(work)
	if !ok {
		t.Fatal("Refresh failed")
	}
	if got != 1 {
		t.Errorf("Refresh = %d commits behind, want 1", got)
	}
}

// TestMaterialiseAtGivesTheRefsBytesNotTheTrees. The whole point is that the
// working tree and the ref disagree; a helper that returned the tree would be
// indistinguishable from not calling it.
func TestMaterialiseAtGivesTheRefsBytesNotTheTrees(t *testing.T) {
	base := t.TempDir()
	origin, work := filepath.Join(base, "origin"), filepath.Join(base, "clone")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, origin, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "ci.yml"), []byte("old: nothing installed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, origin, "add", "-A")
	gitIn(t, origin, "commit", "-qm", "first")
	gitIn(t, base, "clone", "-q", origin, work)

	// Upstream gains the thing the scan is looking for. The clone does not.
	if err := os.WriteFile(filepath.Join(origin, "ci.yml"), []byte("new: apt-get install swtpm\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, origin, "commit", "-qam", "install the judge")
	gitIn(t, work, "fetch", "-q", "origin")

	onDisk, err := os.ReadFile(filepath.Join(work, "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(onDisk), "old:") {
		t.Fatalf("the clone is not behind; this test needs it to be: %q", onDisk)
	}

	ref, ok := DefaultRef(work)
	if !ok {
		t.Fatal("DefaultRef could not name the remote default")
	}
	tmp, cleanup, ok := MaterialiseAt(work, ref)
	if !ok {
		t.Fatal("MaterialiseAt failed")
	}
	defer cleanup()

	at, err := os.ReadFile(filepath.Join(tmp, "ci.yml"))
	if err != nil {
		t.Fatalf("the materialised tree has no ci.yml: %v", err)
	}
	if !strings.Contains(string(at), "swtpm") {
		t.Errorf("materialised %q, want the ref's bytes with swtpm in them", at)
	}
	// And the control in the other direction: the working tree is untouched.
	after, _ := os.ReadFile(filepath.Join(work, "ci.yml"))
	if string(after) != string(onDisk) {
		t.Error("MaterialiseAt changed the working tree")
	}
}

func TestMaterialiseAtRefusesARefThatIsNotThere(t *testing.T) {
	_, cleanup, ok := MaterialiseAt(t.TempDir(), "origin/main")
	defer cleanup()
	if ok {
		t.Error("materialised a ref from a directory that is not a repository")
	}
}
