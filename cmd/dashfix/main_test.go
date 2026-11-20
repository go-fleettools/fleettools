package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMigrateRewritesTheBoolean(t *testing.T) {
	out, changed, err := migrate("a: 1,\n  dependencyDashboard: true,\n  b: 2,\n")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if want := "a: 1,\n  dependencyDashboard: { enabled: true },\n  b: 2,\n"; out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestMigrateLeavesTheObjectAlone(t *testing.T) {
	src := "  dependencyDashboard: { enabled: true },\n"
	out, changed, err := migrate(src)
	if err != nil || changed || out != src {
		t.Fatalf("out=%q changed=%v err=%v", out, changed, err)
	}
}

// ⛔ "nothing to do" and "could not find the key" must not read alike.
func TestMigrateRefusesAFileWithoutTheKey(t *testing.T) {
	if _, _, err := migrate("module.exports = {};\n"); err == nil {
		t.Fatal("a file with no dependencyDashboard key was accepted")
	}
}

type env struct {
	root, ledger, log, head string
}

func init() { headPollEvery = 0; headPollTries = 3 }

// ⛔ The race that stopped the first real run: prwait, started the instant after
// the push, judged the pull request's OLD head — which was red — because GitHub
// had not yet moved the head to the pushed commit.
func TestAnUnreadablePushedCommitStopsTheRun(t *testing.T) {
	e := setup(t, redView, "", "", "4000")
	clone(t, e, "go-x-.github", "")
	script := "#!/bin/sh\ncase \"$*\" in *rev-parse*) echo cannot resolve HEAD >&2; exit 1 ;; esac\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(e.head), "bin", "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	st, _, errOut := drive(t, e, nil, "go-x/.github#9")
	if st != 1 || !strings.Contains(errOut, "cannot resolve HEAD") || strings.Contains(logged(t, e), "prwait") {
		t.Fatalf("status %d: %s", st, errOut)
	}
}

func TestItDoesNotWaitUntilThePullRequestHeadIsThePushedCommit(t *testing.T) {
	e := setup(t, redView, "", "", "4000")
	clone(t, e, "go-x-.github", "")
	if err := os.WriteFile(e.head, []byte("old000"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, _, errOut := drive(t, e, nil, "go-x/.github#9")
	if st != 1 || !strings.Contains(errOut, "has not moved to the pushed commit") {
		t.Fatalf("status %d: %s", st, errOut)
	}
	if l := logged(t, e); strings.Contains(l, "prwait") || strings.Contains(l, "ghmerge") {
		t.Fatalf("waited on a stale head:\n%s", l)
	}
}

// setup puts fake programs first on PATH. Each appends its argv to one log;
// behaviour is chosen by the variables the caller sets in the script bodies.
func setup(t *testing.T, view, claim, prwait, remaining string) env {
	t.Helper()
	dir := t.TempDir()
	e := env{root: filepath.Join(dir, "clones"), ledger: filepath.Join(dir, "ledger.txt"), log: filepath.Join(dir, "log")}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := func(name, body string) {
		script := "#!/bin/sh\necho \"" + name + " $*\" >> " + e.log + "\n" + body + "\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rate := `echo '{"resources":{"core":{"remaining":` + remaining + `}}}'`
	if remaining == "fail" {
		rate = "echo rate limited >&2; exit 1"
	}
	pr := `echo '` + view + `'`
	if view == "fail" {
		pr = "echo no such pull request >&2; exit 1"
	}
	head := filepath.Join(dir, "head")
	if err := os.WriteFile(head, []byte("abc123"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.head = head
	fake("gh", `case "$*" in
  *rate_limit*) `+rate+` ;;
  *"--json headRefOid"*) echo "{\"headRefOid\":\"$(cat `+head+`)\"}" ;;
  *"pr view"*) `+pr+` ;;
  *) echo "unexpected gh $*" >&2; exit 1 ;;
esac`)
	fake("agentsync", claim)
	fake("git", `case "$*" in *rev-parse*) echo abc123 ;; esac`)
	fake("gitpush", "")
	fake("prwait", prwait)
	fake("ghmerge", "")
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	return e
}

const redView = `{"state":"OPEN","headRefName":"renovate/deps","statusCheckRollup":[{"conclusion":"FAILURE"}]}`
const greenView = `{"state":"OPEN","headRefName":"renovate/deps","statusCheckRollup":[{"conclusion":"SUCCESS"}]}`

func clone(t *testing.T, e env, name, workflow string) string {
	t.Helper()
	d := filepath.Join(e.root, name)
	if err := os.MkdirAll(filepath.Join(d, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "config.js"), []byte("module.exports = {\n  dependencyDashboard: true,\n};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if workflow != "" {
		w := filepath.Join(d, ".github", "workflows")
		if err := os.MkdirAll(w, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(w, "ci.yml"), []byte(workflow), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func drive(t *testing.T, e env, extra []string, targets ...string) (int, string, string) {
	t.Helper()
	var out, errOut strings.Builder
	args := append([]string{"--root", e.root, "--ledger", e.ledger}, extra...)
	return run(&out, &errOut, append(args, targets...)), out.String(), errOut.String()
}

func logged(t *testing.T, e env) string {
	t.Helper()
	b, _ := os.ReadFile(e.log)
	return string(b)
}

func TestARedPullRequestIsFixedWaitedForAndMerged(t *testing.T) {
	e := setup(t, redView, "", "", "4000")
	d := clone(t, e, "go-x-.github", "")
	st, out, errOut := drive(t, e, nil, "go-x/.github#9")
	if st != 0 {
		t.Fatalf("status %d: %s %s", st, out, errOut)
	}
	b, _ := os.ReadFile(filepath.Join(d, "config.js"))
	if !strings.Contains(string(b), "dependencyDashboard: { enabled: true },") {
		t.Fatalf("config.js not migrated: %s", b)
	}
	l := logged(t, e)
	order := []string{"agentsync claim", "git add config.js", "git commit -F", "gitpush origin renovate/deps", "prwait -repo go-x/.github 9", "ghmerge go-x/.github 9"}
	pos := -1
	for _, w := range order {
		i := strings.Index(l, w)
		if i <= pos {
			t.Fatalf("%q missing or out of order in:\n%s", w, l)
		}
		pos = i
	}
	if !strings.Contains(out, "totals: merged 1, skipped 0, refused 0") {
		t.Fatalf("totals: %s", out)
	}
	msg, _ := os.ReadFile(filepath.Join(d, ".git", "dashfix-msg"))
	if !strings.Contains(string(msg), "Co-Authored-By: Claude Sonnet 5.5") {
		t.Fatalf("attribution missing: %s", msg)
	}
}

func TestAGreenPullRequestIsNeverTouched(t *testing.T) {
	e := setup(t, greenView, "", "", "4000")
	clone(t, e, "go-x-.github", "")
	st, out, _ := drive(t, e, nil, "go-x/.github#9")
	if st != 0 || !strings.Contains(out, "not red") {
		t.Fatalf("status %d: %s", st, out)
	}
	if l := logged(t, e); strings.Contains(l, "agentsync") || strings.Contains(l, "gitpush") {
		t.Fatalf("a green PR was acted on:\n%s", l)
	}
}

func TestAHeldClaimSkipsTheRepository(t *testing.T) {
	e := setup(t, redView, "echo held by another session; exit 1", "", "4000")
	d := clone(t, e, "go-x-.github", "")
	st, out, _ := drive(t, e, nil, "go-x/.github#9")
	if st != 0 || !strings.Contains(out, "claim refused: held by another session") {
		t.Fatalf("status %d: %s", st, out)
	}
	b, _ := os.ReadFile(filepath.Join(d, "config.js"))
	if strings.Contains(string(b), "enabled") {
		t.Fatal("edited a repository somebody else holds")
	}
}

func TestATwoHalvesPinIsSkippedNotEdited(t *testing.T) {
	e := setup(t, redView, "", "", "4000")
	clone(t, e, "go-x-.github", "env:\n  RENOVATE_VERSION: '44.1.0'\n")
	st, out, _ := drive(t, e, nil, "go-x/.github#9")
	if st != 0 || !strings.Contains(out, "RENOVATE_VERSION") {
		t.Fatalf("status %d: %s", st, out)
	}
	if strings.Contains(logged(t, e), "gitpush") {
		t.Fatal("pushed a repository with a two-halves pin")
	}
}

// ⛔ A red wait stops the run and merges nothing; it is not retried.
func TestARedWaitStopsTheRunBeforeTheMerge(t *testing.T) {
	e := setup(t, redView, "", "echo RED; exit 1", "4000")
	clone(t, e, "go-x-.github", "")
	clone(t, e, "go-y-.github", "")
	st, out, errOut := drive(t, e, nil, "go-x/.github#9", "go-y/.github#3")
	if st != 1 || !strings.Contains(errOut, "STOPPED at go-x/.github#9") {
		t.Fatalf("status %d: %s %s", st, out, errOut)
	}
	l := logged(t, e)
	if strings.Contains(l, "ghmerge") || strings.Contains(l, "go-y") {
		t.Fatalf("went on after a refusal:\n%s", l)
	}
}

func TestTheReserveStopsTheRun(t *testing.T) {
	e := setup(t, redView, "", "", "999")
	st, _, errOut := drive(t, e, nil, "go-x/.github#9")
	if st != 2 || !strings.Contains(errOut, "budget reserve: 999 left, 1000 reserved") {
		t.Fatalf("status %d: %s", st, errOut)
	}
	if strings.Contains(logged(t, e), "pr view") {
		t.Fatal("read a pull request below the reserve")
	}
}

func TestTheHourlyCapStopsTheRun(t *testing.T) {
	e := setup(t, redView, "", "", "4000")
	now := time.Now()
	line := func(ago time.Duration) string {
		return strconvI(now.Add(-ago).Unix()) + " a/b#1\n"
	}
	if err := os.WriteFile(e.ledger, []byte(line(10*time.Minute)+line(20*time.Minute)+line(2*time.Hour)), 0o644); err != nil {
		t.Fatal(err)
	}
	st, _, errOut := drive(t, e, []string{"--max", "2"}, "go-x/.github#9")
	if st != 2 || !strings.Contains(errOut, "hourly cap: 2 merged") {
		t.Fatalf("status %d: %s", st, errOut)
	}
	// the two-hour-old line does not count: a cap of 3 lets it through
	clone(t, e, "go-x-.github", "")
	if st, out, errOut := drive(t, e, []string{"--max", "3"}, "go-x/.github#9"); st != 0 {
		t.Fatalf("status %d: %s %s", st, out, errOut)
	}
}

func TestABadTargetIsRefused(t *testing.T) {
	e := setup(t, redView, "", "", "4000")
	if st, _, _ := drive(t, e, nil, "go-x/.github"); st != 2 {
		t.Fatalf("status %d", st)
	}
	if st, _, _ := drive(t, e, nil); st != 2 {
		t.Fatalf("no targets: status %d", st)
	}
}

func TestAFailedRateLimitReadStopsTheRun(t *testing.T) {
	e := setup(t, redView, "", "", "fail")
	if st, _, errOut := drive(t, e, nil, "go-x/.github#9"); st != 2 || !strings.Contains(errOut, "could not read the rate limit") {
		t.Fatalf("status %d: %s", st, errOut)
	}
}

// ⛔ An unreadable ledger is a refusal, not a count of zero.
func TestAnUnreadableLedgerStopsTheRun(t *testing.T) {
	e := setup(t, redView, "", "", "4000")
	if err := os.MkdirAll(e.ledger, 0o755); err != nil {
		t.Fatal(err)
	}
	if st, _, errOut := drive(t, e, nil, "go-x/.github#9"); st != 2 || errOut == "" {
		t.Fatalf("status %d: %q", st, errOut)
	}
	if strings.Contains(logged(t, e), "pr view") {
		t.Fatal("went on with an unreadable ledger")
	}
}

func TestALedgerThatCannotBeWrittenStopsTheRun(t *testing.T) {
	e := setup(t, redView, "", "", "4000")
	clone(t, e, "go-x-.github", "")
	// ghmerge itself plants a directory where the ledger goes
	bin := filepath.Dir(filepath.Join(e.root, "..", "bin", "ghmerge"))
	script := "#!/bin/sh\nmkdir -p " + e.ledger + "\n"
	if err := os.WriteFile(filepath.Join(bin, "ghmerge"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if st, _, errOut := drive(t, e, nil, "go-x/.github#9"); st != 1 || !strings.Contains(errOut, "is a directory") {
		t.Fatalf("status %d: %s", st, errOut)
	}
}

func TestAFailedPullRequestReadStopsTheRun(t *testing.T) {
	e := setup(t, "fail", "", "", "4000")
	if st, _, errOut := drive(t, e, nil, "go-x/.github#9"); st != 1 || !strings.Contains(errOut, "no such pull request") {
		t.Fatalf("status %d: %s", st, errOut)
	}
}

func TestAClosedPullRequestIsNeverTouched(t *testing.T) {
	e := setup(t, `{"state":"CLOSED","headRefName":"renovate/deps","statusCheckRollup":[{"conclusion":"FAILURE"}]}`, "", "", "4000")
	st, out, _ := drive(t, e, nil, "go-x/.github#9")
	if st != 0 || !strings.Contains(out, "not open") || strings.Contains(logged(t, e), "agentsync") {
		t.Fatalf("status %d: %s\n%s", st, out, logged(t, e))
	}
}

func TestDryRunChangesAndClaimsNothing(t *testing.T) {
	e := setup(t, redView, "", "", "4000")
	d := clone(t, e, "go-x-.github", "")
	st, out, _ := drive(t, e, []string{"--dry-run"}, "go-x/.github#9")
	if st != 0 || !strings.Contains(out, "dry run") {
		t.Fatalf("status %d: %s", st, out)
	}
	b, _ := os.ReadFile(filepath.Join(d, "config.js"))
	if strings.Contains(string(b), "enabled") || strings.Contains(logged(t, e), "agentsync") {
		t.Fatal("a dry run acted")
	}
}

func TestAMissingConfigStopsTheRun(t *testing.T) {
	e := setup(t, redView, "", "", "4000")
	d := clone(t, e, "go-x-.github", "")
	if err := os.Remove(filepath.Join(d, "config.js")); err != nil {
		t.Fatal(err)
	}
	if st, _, errOut := drive(t, e, nil, "go-x/.github#9"); st != 1 || !strings.Contains(errOut, "no such file") {
		t.Fatalf("status %d: %s", st, errOut)
	}
}

func TestAConfigWithoutTheKeyStopsTheRun(t *testing.T) {
	e := setup(t, redView, "", "", "4000")
	d := clone(t, e, "go-x-.github", "")
	if err := os.WriteFile(filepath.Join(d, "config.js"), []byte("module.exports = {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, _, errOut := drive(t, e, nil, "go-x/.github#9")
	if st != 1 || !strings.Contains(errOut, "no `dependencyDashboard: true,` line") {
		t.Fatalf("status %d: %s", st, errOut)
	}
	if strings.Contains(logged(t, e), "gitpush") {
		t.Fatal("pushed without an edit")
	}
}

func strconvI(n int64) string {
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
