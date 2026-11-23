package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/rollout"
)

func init() { rollout.HeadPollEvery = 0; rollout.HeadPollTries = 3 }

const (
	oldText = "npx --yes --package renovate@latest renovate-config-validator"
	newText = "npx --yes --package renovate@44 renovate-config-validator"
	ciFile  = ".github/workflows/ci.yml"
	branch  = "pin-renovate"
	target1 = "name: ci\njobs:\n  validate:\n    steps:\n      - run: " + oldText + "\n      - run: echo ok\n"
)

type answer struct {
	status int
	body   string
}

// world is one test's fake GitHub (an httptest server for the contents API and
// the organisation list) and fake programs first on PATH, each logging its argv.
type world struct {
	t                      *testing.T
	dir, root, ledger, log string
	bin                    string
	body, commit           string
	files                  map[string]answer // org -> contents answer
	orgs                   []string
	remaining              atomic.Int64
	gets                   atomic.Int64
	srv                    *httptest.Server
	override               http.HandlerFunc // replaces the whole fake site when set, before a drive
}

func newWorld(t *testing.T, files map[string]answer) *world {
	t.Helper()
	w := &world{t: t, dir: t.TempDir(), files: files}
	w.root, w.ledger = filepath.Join(w.dir, "clones"), filepath.Join(w.dir, "ledger.txt")
	w.log, w.bin = filepath.Join(w.dir, "log"), filepath.Join(w.dir, "bin")
	w.body, w.commit = filepath.Join(w.dir, "body.md"), filepath.Join(w.dir, "commit.txt")
	w.remaining.Store(4000)
	for _, p := range []string{w.body, w.commit} {
		if err := os.WriteFile(p, []byte("text\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(w.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for org := range files {
		w.orgs = append(w.orgs, org)
	}
	w.srv = httptest.NewServer(http.HandlerFunc(w.serve))
	t.Cleanup(w.srv.Close)
	sweepBase = w.srv.URL
	oldTok := readToken
	readToken = func(string) (string, error) { return "test-token", nil }
	t.Cleanup(func() { sweepBase = ""; readToken = oldTok })

	rate := `echo '{"resources":{"core":{"remaining":4000}}}'`
	w.fake("gh", `repo=; prev=
for a in "$@"; do case $prev in -R|--repo) repo=$a;; esac; prev=$a; done
f=`+w.dir+`/prlist-$(echo $repo | tr / _)
[ -f $f ] || echo '[]' > $f
case "$*" in
  *rate_limit*) `+rate+` ;;
  *"pr list"*) cat $f ;;
  *"pr create"*) echo '[{"number":7,"headRefOid":"abc123"}]' > $f; echo https://github.com/$repo/pull/7 ;;
  *statusCheckRollup*) cat `+w.dir+`/view ;;
  *"--json headRefOid"*) echo '{"headRefOid":"abc123"}' ;;
  *) echo "unexpected gh $*" >&2; exit 1 ;;
esac`)
	w.view(`{"headRefOid":"abc123","statusCheckRollup":[{"conclusion":"SUCCESS"},{"conclusion":"SKIPPED"}]}`)
	w.fake("agentsync", "")
	w.fake("git", `case "$*" in
  *"clone"*) for a in "$@"; do d=$a; done; mkdir -p "$d/.github/workflows"; cat `+w.dir+`/cloned > "$d/`+ciFile+`" ;;
  *"symbolic-ref"*) echo origin/main ;;
  *rev-parse*) echo abc123 ;;
esac`)
	if err := os.WriteFile(filepath.Join(w.dir, "cloned"), []byte(target1), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"gitpush", "prbody", "paritycheck", "prwait", "ghmerge"} {
		w.fake(n, "")
	}
	t.Setenv("PATH", w.bin+":"+os.Getenv("PATH"))
	return w
}

func (w *world) fake(name, body string) {
	w.t.Helper()
	script := "#!/bin/sh\necho \"" + name + " $*\" >> " + w.log + "\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(w.bin, name), []byte(script), 0o755); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) view(s string) {
	w.t.Helper()
	if err := os.WriteFile(filepath.Join(w.dir, "view"), []byte(s), 0o644); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) rate(body string) {
	w.t.Helper()
	// replace only the rate_limit arm: rewrite the gh fake with another body
	b, _ := os.ReadFile(filepath.Join(w.bin, "gh"))
	s := strings.Replace(string(b), `echo '{"resources":{"core":{"remaining":4000}}}'`, body, 1)
	if err := os.WriteFile(filepath.Join(w.bin, "gh"), []byte(s), 0o755); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) serve(rw http.ResponseWriter, r *http.Request) {
	if w.override != nil {
		w.override(rw, r)
		return
	}
	rw.Header().Set("X-RateLimit-Remaining", fmt.Sprint(w.remaining.Load()))
	if r.URL.Path == "/user/orgs" {
		parts := []string{}
		for _, o := range w.orgs {
			parts = append(parts, fmt.Sprintf(`{"login":%q}`, o))
		}
		fmt.Fprintf(rw, "[%s]", strings.Join(parts, ","))
		return
	}
	w.gets.Add(1)
	for org, a := range w.files {
		if r.URL.Path == "/repos/"+org+"/.github/contents/"+ciFile {
			rw.WriteHeader(a.status)
			rw.Write([]byte(a.body))
			return
		}
	}
	http.NotFound(rw, r)
}

// clone makes the clone directory the command would reuse.
func (w *world) clone(org, content string) string {
	w.t.Helper()
	d := filepath.Join(w.root, org+"-.github-"+branch)
	if err := os.MkdirAll(filepath.Join(d, ".github", "workflows"), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, ciFile), []byte(content), 0o644); err != nil {
		w.t.Fatal(err)
	}
	return d
}

func (w *world) logged() string {
	b, _ := os.ReadFile(w.log)
	return string(b)
}

func (w *world) drive(extra ...string) (int, string, string) {
	w.t.Helper()
	args := []string{"--file", ciFile, "--old", oldText, "--new", newText, "--skip-if", "RENOVATE_VERSION",
		"--branch", branch, "--title", "ci: pin the validator", "--body-file", w.body, "--commit-file", w.commit,
		"--root", w.root, "--ledger", w.ledger}
	var out, errOut bytes.Buffer
	st := cli.Drive(RootCmd(), append(args, extra...), &out, &errOut)
	return st, out.String(), errOut.String()
}

func ok(body string) answer { return answer{200, body} }

func TestATargetIsEditedPushedWaitedForAndMerged(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	d := w.clone("go-a", target1+"      - run: "+oldText+" --strict\n")
	st, out, errOut := w.drive("--orgs", "go-a")
	if st != 0 {
		t.Fatalf("status %d: %s %s", st, out, errOut)
	}
	b, _ := os.ReadFile(filepath.Join(d, ciFile))
	if strings.Contains(string(b), "renovate@latest") || strings.Count(string(b), "renovate@44") != 2 {
		t.Fatalf("not every occurrence replaced:\n%s", b)
	}
	l := w.logged()
	order := []string{
		"agentsync claim --note orgedit: pin-renovate go-a/.github",
		"git switch -C pin-renovate origin/main",
		"git add -- " + ciFile,
		"git commit -F " + w.commit,
		"gitpush origin pin-renovate",
		"prbody " + w.body + " --repo go-a/.github",
		"paritycheck text " + w.body,
		"gh pr create --repo go-a/.github --head pin-renovate --title ci: pin the validator --body-file " + w.body,
		"prwait -repo go-a/.github 7",
		"ghmerge go-a/.github 7",
		"agentsync release go-a/.github",
	}
	pos := -1
	for _, s := range order {
		i := strings.Index(l, s)
		if i <= pos {
			t.Fatalf("%q missing or out of order in:\n%s", s, l)
		}
		pos = i
	}
	if !strings.Contains(out, "merged    go-a/.github#7") || !strings.Contains(out, "target 1") || !strings.Contains(out, "merged 1") {
		t.Fatalf("output:\n%s", out)
	}
	led, _ := os.ReadFile(w.ledger)
	if !strings.HasSuffix(strings.TrimSpace(string(led)), " go-a/.github#7") {
		t.Fatalf("ledger: %q", led)
	}
}

func TestANewCloneIsMadeUnderTheDurableRoot(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	st, out, errOut := w.drive("--orgs", "go-a")
	if st != 0 {
		t.Fatalf("status %d: %s %s", st, out, errOut)
	}
	want := filepath.Join(w.root, "go-a-.github-pin-renovate")
	if !strings.Contains(w.logged(), "git clone https://github.com/go-a/.github.git "+want) {
		t.Fatalf("no clone at %s:\n%s", want, w.logged())
	}
	b, _ := os.ReadFile(filepath.Join(want, ciFile))
	if !strings.Contains(string(b), "renovate@44") {
		t.Fatalf("clone not edited:\n%s", b)
	}
}

func TestEveryVerdictAndAnUnreadFileIsNeverNofile(t *testing.T) {
	w := newWorld(t, map[string]answer{
		"a-none":    {404, "Not Found"},
		"b-done":    ok("run: npx --package renovate@44 renovate-config-validator\n"),
		"c-pinned":  ok(target1 + "env:\n  RENOVATE_VERSION: 44\n"),
		"d-unread":  {500, "boom"},
		"e-limited": {403, "rate"},
	})
	st, out, errOut := w.drive()
	if st != 1 {
		t.Fatalf("status %d (an unread file must exit 1): %s %s", st, out, errOut)
	}
	for _, s := range []string{
		"nofile    a-none/.github", "unchanged b-done/.github", `skip      c-pinned/.github  contains "RENOVATE_VERSION"`,
		"unread    d-unread/.github  HTTP 500", "unread    e-limited/.github  HTTP 403",
		"totals: 5 organisations, target 0, unchanged 1, skip 1, nofile 1, unread 2; merged 0",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q in:\n%s", s, out)
		}
	}
	if !strings.Contains(errOut, "2 organisation(s) unread") {
		t.Errorf("stderr: %s", errOut)
	}
	if l := w.logged(); strings.Contains(l, "agentsync") || strings.Contains(l, "gitpush") {
		t.Fatalf("acted on a non-target:\n%s", l)
	}
}

func TestATruncatedReadIsUnread(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok("x")})
	w.override = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user/orgs" {
			rw.Write([]byte(`[{"login":"go-a"}]`))
			return
		}
		rw.Header().Set("Content-Length", "100")
		rw.Write([]byte("abc"))
		rw.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	})
	st, out, _ := w.drive()
	if st != 1 || !strings.Contains(out, "unread    go-a/.github") {
		t.Fatalf("status %d\n%s", st, out)
	}
}

func TestDryRunReadsAndPrintsTheDiffAndChangesNothing(t *testing.T) {
	two := "a\n" + oldText + "\nb\n" + oldText + " " + oldText + "\n"
	w := newWorld(t, map[string]answer{"go-a": ok(two), "go-b": {404, ""}, "go-c": ok(target1)})
	d := w.clone("go-a", two)
	// the dry run needs none of the pull request flags
	var out, errOut bytes.Buffer
	st := cli.Drive(RootCmd(), []string{"--file", ciFile, "--old", oldText, "--new", newText,
		"--root", w.root, "--ledger", w.ledger, "--dry-run"}, &out, &errOut)
	if st != 0 {
		t.Fatalf("status %d: %s %s", st, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "target    go-a/.github  4 line(s): -2 +2") ||
		!strings.Contains(out.String(), "target    go-c/.github  2 line(s): -1 +1") ||
		!strings.Contains(out.String(), "nofile    go-b/.github") ||
		!strings.Contains(out.String(), "target 2") {
		t.Fatalf("output:\n%s", out.String())
	}
	if w.logged() != "" {
		t.Fatalf("a dry run ran programs:\n%s", w.logged())
	}
	b, _ := os.ReadFile(filepath.Join(d, ciFile))
	if string(b) != two {
		t.Fatal("a dry run edited a file")
	}
	if _, err := os.Stat(w.ledger); err == nil {
		t.Fatal("a dry run wrote the ledger")
	}
}

func TestDryRunStillStopsAtTheReserve(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1), "go-b": ok(target1)})
	w.remaining.Store(999)
	st, out, errOut := w.drive("--dry-run", "--reserve", "1000")
	if st != 2 || !strings.Contains(errOut, "STOPPED") || !strings.Contains(errOut, "reserve") {
		t.Fatalf("status %d: %s %s", st, out, errOut)
	}
}

func TestTheReserveStopsBeforeTheNextRead(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok("x"), "go-b": ok("x"), "go-c": ok("x")})
	w.override = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user/orgs" {
			rw.Header().Set("X-RateLimit-Remaining", "4000")
			rw.Write([]byte(`[{"login":"go-a"},{"login":"go-b"},{"login":"go-c"}]`))
			return
		}
		// the first file read leaves the quota below the reserve
		rw.Header().Set("X-RateLimit-Remaining", "10")
		w.gets.Add(1)
		rw.Write([]byte("x"))
	})
	st, out, errOut := w.drive("--reserve", "100")
	if st != 2 || !strings.Contains(errOut, "STOPPED before go-b: the REST reserve is reached (10 left, 100 reserved)") {
		t.Fatalf("status %d: %s %s", st, out, errOut)
	}
	if w.gets.Load() != 1 || !strings.Contains(out, "2 organisation(s) not finished") {
		t.Fatalf("%d reads\n%s", w.gets.Load(), out)
	}
}

func TestAHeldClaimSkipsTheOrganisationAndTheRunGoesOn(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1), "go-b": ok(target1)})
	w.fake("agentsync", `case "$*" in *"claim"*go-a*) echo held by another session; exit 1 ;; esac`)
	w.clone("go-a", target1)
	st, out, errOut := w.drive()
	if st != 0 {
		t.Fatalf("status %d: %s %s", st, out, errOut)
	}
	if !strings.Contains(out, "claim refused go-a/.github: held by another session") ||
		!strings.Contains(out, "merged    go-b/.github#7") || !strings.Contains(out, "claim refused 1") {
		t.Fatalf("output:\n%s", out)
	}
	l := w.logged()
	if strings.Contains(l, "gitpush origin pin-renovate\nprbody "+w.body+" --repo go-a") || strings.Contains(l, "agentsync release go-a") {
		t.Fatalf("touched a repository somebody else holds:\n%s", l)
	}
	if strings.Contains(l, "go-a/.github 7") {
		t.Fatalf("waited or merged on a refused claim:\n%s", l)
	}
}

func TestAnOpenPullRequestFromTheBranchIsResumedNotDoubled(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	if err := os.WriteFile(filepath.Join(w.dir, "prlist-go-a_.github"), []byte(`[{"number":41,"headRefOid":"abc123"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, out, errOut := w.drive("--orgs", "go-a")
	if st != 0 || !strings.Contains(out, "merged    go-a/.github#41") {
		t.Fatalf("status %d: %s %s", st, out, errOut)
	}
	l := w.logged()
	for _, forbidden := range []string{"git clone", "git commit", "gitpush", "pr create", "prbody"} {
		if strings.Contains(l, forbidden) {
			t.Fatalf("%q ran on a resumed pull request:\n%s", forbidden, l)
		}
	}
	if !strings.Contains(l, "prwait -repo go-a/.github 41") || !strings.Contains(l, "ghmerge go-a/.github 41") {
		t.Fatalf("not waited and merged:\n%s", l)
	}
}

func TestTwoOpenPullRequestsStopTheRun(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	if err := os.WriteFile(filepath.Join(w.dir, "prlist-go-a_.github"), []byte(`[{"number":41,"headRefOid":"a"},{"number":42,"headRefOid":"b"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, _, errOut := w.drive("--orgs", "go-a")
	if st != 1 || !strings.Contains(errOut, "STOPPED at go-a/.github: 2 open pull requests") || strings.Contains(w.logged(), "ghmerge") {
		t.Fatalf("status %d: %s", st, errOut)
	}
}

func TestRedCIStopsTheRunBeforeTheMergeAndReleasesTheClaim(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1), "go-b": ok(target1)})
	w.fake("prwait", "echo RED; exit 1")
	st, out, errOut := w.drive()
	if st != 1 || !strings.Contains(errOut, "STOPPED at go-a/.github: not green, not merging #7") {
		t.Fatalf("status %d: %s %s", st, out, errOut)
	}
	l := w.logged()
	if strings.Contains(l, "ghmerge") || strings.Contains(l, "go-b") {
		t.Fatalf("went on after a red CI:\n%s", l)
	}
	if !strings.Contains(l, "agentsync release go-a/.github") {
		t.Fatalf("claim kept after a stop:\n%s", l)
	}
	if !strings.Contains(out, "2 organisation(s) not finished") {
		t.Fatalf("output:\n%s", out)
	}
}

// prwait can say yes while the checks read at the pushed commit say otherwise.
func TestTheChecksAreReadAgainBoundToThePushedCommit(t *testing.T) {
	for name, tc := range map[string]struct{ view, want string }{
		"red":     {`{"headRefOid":"abc123","statusCheckRollup":[{"conclusion":"SUCCESS"},{"conclusion":"FAILURE"}]}`, "red CI on #7: a check failed"},
		"state":   {`{"headRefOid":"abc123","statusCheckRollup":[{"state":"ERROR"}]}`, "red CI on #7"},
		"pending": {`{"headRefOid":"abc123","statusCheckRollup":[{"conclusion":"SUCCESS"},{"conclusion":""}]}`, "not conclusive: 1 check(s) not finished"},
		"empty":   {`{"headRefOid":"abc123","statusCheckRollup":[]}`, "no checks: an empty list is not a pass"},
		"stale":   {`{"headRefOid":"old0000","statusCheckRollup":[{"conclusion":"SUCCESS"}]}`, "not at the pushed commit"},
		"garbage": {`<html>`, "unreadable"},
	} {
		w := newWorld(t, map[string]answer{"go-a": ok(target1)})
		w.view(tc.view)
		st, _, errOut := w.drive("--orgs", "go-a")
		if st != 1 || !strings.Contains(errOut, tc.want) || strings.Contains(w.logged(), "ghmerge") {
			t.Errorf("%s: status %d: %s", name, st, errOut)
		}
	}
}

func TestAGhmergeRefusalStopsTheRunAndIsNotRecorded(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1), "go-b": ok(target1)})
	w.fake("ghmerge", "echo refused: not mergeable >&2; exit 1")
	st, _, errOut := w.drive()
	if st != 1 || !strings.Contains(errOut, "STOPPED at go-a/.github: ghmerge refused #7") || strings.Contains(w.logged(), "go-b") {
		t.Fatalf("status %d: %s\n%s", st, errOut, w.logged())
	}
	if _, err := os.Stat(w.ledger); err == nil {
		t.Fatal("a refused merge was recorded")
	}
}

func TestADirtyCloneIsNotTouched(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	d := w.clone("go-a", target1)
	w.fake("git", `case "$*" in *status*) echo " M file" ;; esac`)
	st, _, errOut := w.drive("--orgs", "go-a")
	if st != 1 || !strings.Contains(errOut, "is dirty or unreadable") {
		t.Fatalf("status %d: %s", st, errOut)
	}
	b, _ := os.ReadFile(filepath.Join(d, ciFile))
	if string(b) != target1 || strings.Contains(w.logged(), "gitpush") {
		t.Fatal("edited or pushed a dirty clone")
	}
}

func TestACloneThatNoLongerHoldsTheTextStopsTheRun(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	w.clone("go-a", "nothing to do here\n")
	st, _, errOut := w.drive("--orgs", "go-a")
	if st != 1 || !strings.Contains(errOut, "no longer a target") || strings.Contains(w.logged(), "gitpush") {
		t.Fatalf("status %d: %s", st, errOut)
	}
}

func TestAnUnreadableDefaultBranchStopsTheRun(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	w.clone("go-a", target1)
	w.fake("git", `case "$*" in *symbolic-ref*) echo HEAD ;; esac`)
	st, _, errOut := w.drive("--orgs", "go-a")
	if st != 1 || !strings.Contains(errOut, "default branch of go-a/.github is unreadable") || strings.Contains(w.logged(), "switch") {
		t.Fatalf("status %d: %s", st, errOut)
	}
}

func TestEveryFailingStepStopsTheRun(t *testing.T) {
	for _, step := range []string{"git", "gitpush", "prbody", "paritycheck", "prwait"} {
		w := newWorld(t, map[string]answer{"go-a": ok(target1), "go-b": ok(target1)})
		w.clone("go-a", target1)
		w.clone("go-b", target1)
		w.fake(step, "echo "+step+" failed >&2; exit 1")
		st, _, errOut := w.drive()
		if st != 1 || !strings.Contains(errOut, "STOPPED at go-a/.github") {
			t.Errorf("%s: status %d: %s", step, st, errOut)
		}
		if l := w.logged(); strings.Contains(l, "go-b") || strings.Contains(l, "ghmerge") {
			t.Errorf("%s: went on:\n%s", step, l)
		}
	}
}

func TestAPullRequestThatIsNotListedAfterCreateStopsTheRun(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	w.clone("go-a", target1)
	b, _ := os.ReadFile(filepath.Join(w.bin, "gh"))
	s := strings.Replace(string(b), `echo '[{"number":7,"headRefOid":"abc123"}]' > $f;`, ``, 1)
	if err := os.WriteFile(filepath.Join(w.bin, "gh"), []byte(s), 0o755); err != nil {
		t.Fatal(err)
	}
	st, _, errOut := w.drive("--orgs", "go-a")
	if st != 1 || !strings.Contains(errOut, "just created is not listed") {
		t.Fatalf("status %d: %s", st, errOut)
	}
}

func TestAHeadThatNeverMovesStopsTheRun(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	w.clone("go-a", target1)
	w.fake("git", `case "$*" in *symbolic-ref*) echo origin/main ;; *rev-parse*) echo new999 ;; esac`)
	st, _, errOut := w.drive("--orgs", "go-a")
	if st != 1 || !strings.Contains(errOut, "has not moved to the pushed commit") || strings.Contains(w.logged(), "prwait") {
		t.Fatalf("status %d: %s", st, errOut)
	}
}

func TestAnUnreadableRateLimitStopsTheRunBeforeAnyClaim(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	w.rate("echo rate limited >&2; exit 1")
	st, _, errOut := w.drive("--orgs", "go-a")
	if st != 2 || !strings.Contains(errOut, "could not read the rate limit") || !strings.Contains(errOut, "STOPPED before go-a/.github") {
		t.Fatalf("status %d: %s", st, errOut)
	}
	if strings.Contains(w.logged(), "agentsync") {
		t.Fatal("claimed with an unreadable budget")
	}
}

func TestTheRESTReserveStopsTheRunBeforeAnyClaim(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	w.rate(`echo '{"resources":{"core":{"remaining":999}}}'`)
	st, _, errOut := w.drive("--orgs", "go-a")
	if st != 2 || !strings.Contains(errOut, "budget reserve: 999 left, 1000 reserved") || strings.Contains(w.logged(), "agentsync") {
		t.Fatalf("status %d: %s", st, errOut)
	}
}

func TestTheHourlyCapStopsTheRun(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	now := time.Now()
	line := func(ago time.Duration) string { return fmt.Sprintf("%d x/y#1\n", now.Add(-ago).Unix()) }
	if err := os.WriteFile(w.ledger, []byte(line(10*time.Minute)+line(20*time.Minute)+line(2*time.Hour)), 0o644); err != nil {
		t.Fatal(err)
	}
	st, _, errOut := w.drive("--orgs", "go-a", "--max", "2")
	if st != 2 || !strings.Contains(errOut, "hourly cap: 2 merged in the last hour, cap 2") || strings.Contains(w.logged(), "agentsync") {
		t.Fatalf("status %d: %s", st, errOut)
	}
	// the two-hour-old line does not count
	if st, _, errOut := w.drive("--orgs", "go-a", "--max", "3"); st != 0 {
		t.Fatalf("status %d: %s", st, errOut)
	}
}

func TestTheCapCountsThisRunsOwnMerges(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1), "go-b": ok(target1), "go-c": ok(target1)})
	st, out, errOut := w.drive("--max", "2")
	if st != 2 || !strings.Contains(errOut, "hourly cap: 2 merged") || !strings.Contains(errOut, "STOPPED before go-c/.github") {
		t.Fatalf("status %d: %s %s", st, out, errOut)
	}
	if !strings.Contains(out, "merged    go-a/.github#7") || !strings.Contains(out, "merged    go-b/.github#7") ||
		!strings.Contains(out, "merged 2") || !strings.Contains(out, "1 organisation(s) not finished") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestAnUnreadableLedgerStopsTheRun(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	for name, setup := range map[string]func(){
		"a directory": func() { os.MkdirAll(w.ledger, 0o755) },
		"garbage":     func() { os.WriteFile(w.ledger, []byte("not-a-time x\n"), 0o644) },
	} {
		os.RemoveAll(w.ledger)
		setup()
		st, _, errOut := w.drive("--orgs", "go-a")
		if st != 2 || errOut == "" || strings.Contains(w.logged(), "agentsync") {
			t.Errorf("%s: status %d: %q", name, st, errOut)
		}
	}
}

func TestALedgerThatCannotBeWrittenStopsTheRunAfterTheMerge(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1), "go-b": ok(target1)})
	w.fake("ghmerge", "mkdir -p "+w.ledger)
	st, _, errOut := w.drive()
	if st != 1 || !strings.Contains(errOut, "merged #7 but the ledger could not be written") || strings.Contains(w.logged(), "go-b") {
		t.Fatalf("status %d: %s", st, errOut)
	}
}

func TestOrganisationsAreListedWhenNoneAreNamed(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok("x"), "go-b": {404, ""}})
	st, out, _ := w.drive()
	if st != 0 || !strings.Contains(out, "totals: 2 organisations, target 0, unchanged 1, skip 0, nofile 1") {
		t.Fatalf("status %d\n%s", st, out)
	}
}

func TestAnEmptyOrganisationListIsNotAPass(t *testing.T) {
	w := newWorld(t, map[string]answer{})
	if st, _, errOut := w.drive(); st != 2 || !strings.Contains(errOut, "no organisation to read") {
		t.Fatalf("status %d: %s", st, errOut)
	}
}

func TestAListingFailureAndAMissingTokenAreExit2(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok("x")})
	w.override = func(rw http.ResponseWriter, r *http.Request) { rw.WriteHeader(401) }
	if st, _, errOut := w.drive(); st != 2 || !strings.Contains(errOut, "could not list organisations") {
		t.Fatalf("listing: status %d: %s", st, errOut)
	}
	readToken = func(string) (string, error) { return "", fmt.Errorf("token file: absent") }
	if st, _, errOut := w.drive("--orgs", "go-a"); st != 2 || !strings.Contains(errOut, "absent") {
		t.Fatalf("token: status %d: %s", st, errOut)
	}
}

func TestUsageRefusals(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no file":      {[]string{"--file", ""}, "--file is required"},
		"traversal":    {[]string{"--file", "../x"}, "--file is required"},
		"no old":       {[]string{"--old", ""}, "--old is required"},
		"same":         {[]string{"--new", oldText}, "the same"},
		"superstring":  {[]string{"--new", oldText + " --strict"}, "--new contains --old"},
		"bad repo":     {[]string{"--repo", "a/b"}, "is not a repository name"},
		"bad org":      {[]string{"--orgs", "a b"}, "is not a GitHub name"},
		"empty skip":   {[]string{"--skip-if", ""}, "empty --skip-if"},
		"max":          {[]string{"--max", "0"}, "--max must be at least 1"},
		"no branch":    {[]string{"--branch", ""}, "--branch is required"},
		"dash branch":  {[]string{"--branch", "-x"}, "--branch is required"},
		"dots branch":  {[]string{"--branch", "a..b"}, "--branch is required"},
		"space branch": {[]string{"--branch", "a b"}, "--branch is required"},
		"lock branch":  {[]string{"--branch", "a.lock"}, "--branch is required"},
		"no title":     {[]string{"--title", ""}, "--title is required"},
		"no body":      {[]string{"--body-file", ""}, "--body-file and --commit-file are required"},
		"no commit":    {[]string{"--commit-file", ""}, "--body-file and --commit-file are required"},
		"body missing": {[]string{"--body-file", "/no/such/file"}, "is not a readable file"},
		"body is dir":  {[]string{"--commit-file", os.TempDir()}, "is not a readable file"},
	} {
		st, _, errOut := w.drive(append([]string{"--orgs", "go-a"}, tc.args...)...)
		if st != 2 || !strings.Contains(errOut, tc.want) {
			t.Errorf("%s: status %d: %s", name, st, errOut)
		}
	}
	if w.gets.Load() != 0 || w.logged() != "" {
		t.Fatal("a refused invocation read a file or ran a program")
	}
	var out, errOut bytes.Buffer
	if st := cli.Drive(RootCmd(), []string{"--file", ciFile, "--old", "a"}, &out, &errOut); st != 2 || !strings.Contains(errOut.String(), "--new is required") {
		t.Errorf("--new omitted: status %d: %s", st, errOut.String())
	}
	if st := cli.Drive(RootCmd(), []string{"extra"}, &out, &errOut); st != 2 {
		t.Errorf("positional argument: status %d", st)
	}
}

func TestAnEmptyNewDeletesTheText(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	d := w.clone("go-a", target1)
	if st, out, errOut := w.drive("--orgs", "go-a", "--new", ""); st != 0 {
		t.Fatalf("status %d: %s %s", st, out, errOut)
	}
	b, _ := os.ReadFile(filepath.Join(d, ciFile))
	if strings.Contains(string(b), "renovate") {
		t.Fatalf("not deleted:\n%s", b)
	}
}

func TestRelativeMessagePathsAreMadeAbsolute(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	w.clone("go-a", target1)
	cwd, _ := os.Getwd()
	rel, err := filepath.Rel(cwd, w.commit)
	if err != nil {
		t.Skip(err)
	}
	if st, _, errOut := w.drive("--orgs", "go-a", "--commit-file", rel); st != 0 || !strings.Contains(w.logged(), "git commit -F "+w.commit) {
		t.Fatalf("status %d: %s\n%s", st, errOut, w.logged())
	}
}

func TestJudgeChecksTable(t *testing.T) {
	for name, tc := range map[string]struct {
		out, want string
		red       bool
	}{
		"green":      {`{"headRefOid":"h","statusCheckRollup":[{"conclusion":"SUCCESS"},{"state":"SUCCESS"},{"conclusion":"NEUTRAL"}]}`, "", false},
		"cancelled":  {`{"headRefOid":"h","statusCheckRollup":[{"conclusion":"CANCELLED"}]}`, "a check failed", true},
		"timed out":  {`{"headRefOid":"h","statusCheckRollup":[{"conclusion":"TIMED_OUT"}]}`, "a check failed", true},
		"queued":     {`{"headRefOid":"h","statusCheckRollup":[{"status":"QUEUED"}]}`, "1 check(s) not finished", false},
		"no head":    {`{"statusCheckRollup":[{"conclusion":"SUCCESS"}]}`, "the pull request's checks were unreadable", false},
		"pending st": {`{"headRefOid":"h","statusCheckRollup":[{"state":"PENDING"}]}`, "1 check(s) not finished", false},
	} {
		why, red := judgeChecks(tc.out, "h")
		if why != tc.want || red != tc.red {
			t.Errorf("%s: got %q red=%v", name, why, red)
		}
	}
}

func TestDiffLines(t *testing.T) {
	o := options{old: "AA", new: "B"}
	for name, tc := range map[string]struct {
		src      string
		rem, add int
	}{
		"one":             {"xAAx\n", 1, 1},
		"two lines":       {"AA\nzz\nAA\n", 2, 2},
		"one line twice":  {"AA AA\nz\n", 1, 1},
		"no trailing nl":  {"zz\nAA", 1, 1},
		"adjacent merged": {"AA\nAA\nq\n", 2, 2},
		"none":            {"zz\n", 0, 0},
	} {
		if rem, add := diffLines(tc.src, o); rem != tc.rem || add != tc.add {
			t.Errorf("%s: -%d +%d, want -%d +%d", name, rem, add, tc.rem, tc.add)
		}
	}
	multi := options{old: "a\nb", new: "x"}
	if rem, add := diffLines("1\na\nb\n2\n", multi); rem != 2 || add != 1 {
		t.Errorf("multi-line old: -%d +%d", rem, add)
	}
	grow := options{old: "a", new: "x\ny\nz"}
	if rem, add := diffLines("a\n", grow); rem != 1 || add != 3 {
		t.Errorf("multi-line new: -%d +%d", rem, add)
	}
}

func TestJudgeOrder(t *testing.T) {
	o := options{old: "OLD", skipIf: []string{"PIN", "PIN2"}}
	for src, want := range map[string]verdict{
		"x":        vUnchanged,
		"OLD":      vTarget,
		"OLD PIN":  vSkip,
		"OLD PIN2": vSkip,
		"PIN":      vUnchanged, // nothing to change, whatever else it says
	} {
		if got, _ := judge(src, o); got != want {
			t.Errorf("%q: %s, want %s", src, got, want)
		}
	}
}
