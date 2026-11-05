// Brings open pull request branches up to date with their base branch and says
// what happened to each one — in particular which ones CANNOT be brought up to
// date, because a pull request in conflict with its base gets no workflow run
// at all and so never goes green on its own.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Updates pull request branches with the current head of their base branch.

Merging one pull request moves the base under every other one, and a check that
passed before that move was never run against the tree that results from it. So
the branches are refreshed between merges — and the refresh has to say which of
them it could NOT refresh, because that is the case the caller cannot see:

  ⛔ A pull request whose branch conflicts with its base gets NO workflow run.
     Not a failing one — none. It sits with its last green from before the
     conflict and reads exactly like a pull request that is ready.

Every pull request lands in one bucket and all the totals are printed, zero
included, because a report that lists only what it changed cannot be told from
one that could not look:

  refreshed  the base was merged into the branch just now
  current    the branch was not behind its base; nothing was done
  behind     it needs the base merged in, and --dry-run meant nothing was
  conflict   it conflicts with its base and CANNOT be updated — needs a person
  moved      its head changed between the read and the update; nothing was done
  notopen    the pull request is not open
  unjudged   a call failed or an answer was unreadable, so it was NOT judged

Exit status: 0 every pull request is now current, 1 at least one is not and no
further run of this command will change that by itself, 2 the run could not
read what it needed — which includes any pull request left unjudged. One that
could not be judged is not one that is fine.

⛔ The update is sent with expected_head_sha, so it refuses rather than acts if
the branch moved after it was read. On a machine where other sessions push, an
update that merged into a head nobody had looked at would be a write performed
blind.

⛔ A mergeable field of null is NOT a refusal. GitHub computes mergeability in
a background job and answers null until it finishes; the documented remedy is
to ask again. Reading null as "not mergeable" would report a conflict for every
pull request asked about too soon, so this waits (--wait) and reports unjudged
if the answer never arrives.`

// pull is the subset of the pull request this needs.
//
// Mergeable is *bool on purpose: GitHub sends null while a background job
// computes it, and null, true and false are three different answers.
type pull struct {
	Number    int    `json:"number"`
	State     string `json:"state"`
	Mergeable *bool  `json:"mergeable"`
	Base      ref    `json:"base"`
	Head      ref    `json:"head"`
}

type ref struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

// comparison is GitHub's compare payload. Status is decoded so an unreadable
// answer is an error rather than a silent zero: "not behind" and "could not
// tell" must not read alike.
type comparison struct {
	Status   string `json:"status"`
	BehindBy int    `json:"behind_by"`
}

// verdict is the bucket a pull request landed in. The zero value is unjudged
// on purpose: one no code reached is not one that passed.
type verdict int

const (
	unjudged verdict = iota
	refreshed
	current
	behind
	conflict
	moved
	notopen
)

func (v verdict) String() string {
	switch v {
	case refreshed:
		return "refreshed"
	case current:
		return "current"
	case behind:
		return "behind"
	case conflict:
		return "conflict"
	case moved:
		return "moved"
	case notopen:
		return "notopen"
	}
	return "unjudged"
}

// order is the printing order, which is also the order the totals line uses, so
// a reader comparing two runs reads the same columns in the same places.
var order = []verdict{refreshed, current, behind, conflict, moved, notopen, unjudged}

type result struct {
	number  int
	verdict verdict
	note    string
}

type options struct {
	repo   string
	dryRun bool
	wait   time.Duration
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it through run, below.
func RootCmd() *cobra.Command {
	var (
		allOpen bool
		dryRun  bool
		wait    time.Duration
	)
	c := cli.New("prrefresh [flags] <owner/repo> [number...]",
		"Update pull request branches with their base, and name the ones that cannot be", longHelp)
	c.Flags().BoolVar(&allOpen, "all-open", false, "refresh every open pull request of the repository")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "judge every pull request but change nothing")
	c.Flags().DurationVar(&wait, "wait", time.Minute,
		"how long to keep asking while GitHub has not finished computing mergeability")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return cli.FromStatus(sweep(cli.Out(cmd), cli.Err(cmd), args, allOpen, options{dryRun: dryRun, wait: wait}))
	}
	return c
}

func run(stdout, stderr io.Writer, args []string) int {
	return cli.Drive(RootCmd(), args, stdout, stderr)
}

func sweep(stdout, stderr io.Writer, args []string, allOpen bool, o options) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "prrefresh: name the repository as owner/repo")
		return 2
	}
	o.repo = args[0]
	if !strings.Contains(o.repo, "/") {
		fmt.Fprintf(stderr, "prrefresh: %s is not an owner/repo\n", o.repo)
		return 2
	}

	numbers, status := wanted(stderr, args[1:], allOpen, o.repo)
	if status != 0 {
		return status
	}
	if len(numbers) == 0 {
		// ⛔ An empty population is a REFUSAL and not a clean run. Otherwise a
		// mistyped repository prints "0 pull requests, nothing to do" and exits
		// 0, which is the same thing a quiet repository prints.
		fmt.Fprintln(stderr, "prrefresh: no pull requests to refresh — name some, or --all-open")
		return 2
	}

	results := make([]result, 0, len(numbers))
	for _, n := range numbers {
		results = append(results, refresh(stdout, n, o))
	}
	return report(stdout, stderr, results)
}

// wanted resolves which pull requests this run is about. Naming numbers and
// --all-open together is refused rather than silently resolved one way.
func wanted(stderr io.Writer, args []string, allOpen bool, repo string) ([]int, int) {
	if allOpen && len(args) > 0 {
		fmt.Fprintln(stderr, "prrefresh: --all-open and a list of numbers say different things; give one")
		return nil, 2
	}
	if allOpen {
		ns, err := listOpen(repo)
		if err != nil {
			fmt.Fprintf(stderr, "prrefresh: %v\n", err)
			return nil, 2
		}
		return ns, 0
	}
	var ns []int
	for _, a := range args {
		n, err := strconv.Atoi(strings.TrimPrefix(a, "#"))
		if err != nil || n <= 0 {
			fmt.Fprintf(stderr, "prrefresh: %q is not a pull request number\n", a)
			return nil, 2
		}
		ns = append(ns, n)
	}
	return ns, 0
}

func listOpen(repo string) ([]int, error) {
	b, err := fleet.GH("api", "repos/"+repo+"/pulls?state=open&per_page=100", "--paginate")
	if err != nil {
		return nil, fmt.Errorf("open pull requests: %w", err)
	}
	ps, err := fleet.DecodeArrays[pull](b)
	if err != nil {
		return nil, fmt.Errorf("open pull requests: %w", err)
	}
	ns := make([]int, 0, len(ps))
	for _, p := range ps {
		ns = append(ns, p.Number)
	}
	sort.Ints(ns)
	return ns, nil
}

// refresh judges and, unless --dry-run, updates one pull request.
func refresh(stdout io.Writer, n int, o options) result {
	r := result{number: n}

	p, err := get(o.repo, n)
	if err != nil {
		r.note = err.Error()
		return r
	}
	if p.State != "open" {
		r.verdict, r.note = notopen, "state is "+p.State
		return r
	}

	// Behind-ness is read from the comparison rather than from mergeable_state,
	// whose values GitHub's reference does not document.
	cmp, err := compare(o.repo, p.Base.Ref, p.Head.SHA)
	if err != nil {
		r.note = err.Error()
		return r
	}
	if cmp.BehindBy == 0 {
		r.verdict, r.note = current, "already carries "+p.Base.Ref
		return r
	}

	p, err = awaitMergeability(o.repo, n, o.wait)
	if err != nil {
		r.note = err.Error()
		return r
	}
	if !*p.Mergeable {
		r.verdict = conflict
		r.note = fmt.Sprintf("conflicts with %s — no workflow run will be produced for it", p.Base.Ref)
		return r
	}
	if o.dryRun {
		// ⛔ NOT reported as refreshed. The bucket is what a reader scans, and a
		// run that wrote nothing must not put a pull request in the bucket that
		// means it was written to.
		r.verdict = behind
		r.note = fmt.Sprintf("%d commit(s) behind %s — NOT updated (--dry-run)", cmp.BehindBy, p.Base.Ref)
		return r
	}

	if err := update(o.repo, n, p.Head.SHA); err != nil {
		// ⛔ The head is read AGAIN rather than the error message pattern-matched.
		// "Somebody else pushed" and "the call failed" need opposite responses,
		// and only the head itself can tell them apart.
		if now, errNow := get(o.repo, n); errNow == nil && now.Head.SHA != p.Head.SHA {
			r.verdict = moved
			r.note = fmt.Sprintf("head moved %s → %s while this ran; nothing was written",
				short(p.Head.SHA), short(now.Head.SHA))
			return r
		}
		r.note = err.Error()
		return r
	}
	r.verdict = refreshed
	r.note = fmt.Sprintf("%d commit(s) of %s merged in", cmp.BehindBy, p.Base.Ref)
	return r
}

// awaitMergeability asks again while GitHub answers null, which is documented
// to mean "the background job has not finished", not "no".
func awaitMergeability(repo string, n int, wait time.Duration) (pull, error) {
	deadline := time.Now().Add(wait)
	for attempt := 0; ; attempt++ {
		p, err := get(repo, n)
		if err != nil {
			return p, err
		}
		if p.Mergeable != nil {
			return p, nil
		}
		if time.Now().After(deadline) {
			return p, fmt.Errorf("GitHub had not finished computing mergeability after %s", wait)
		}
		time.Sleep(fleet.Backoff(attempt))
	}
}

func get(repo string, n int) (pull, error) {
	var p pull
	b, err := fleet.GH("api", fmt.Sprintf("repos/%s/pulls/%d", repo, n))
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, err
	}
	if p.State == "" || p.Head.SHA == "" || p.Base.Ref == "" {
		return p, fmt.Errorf("the pull request answer is missing its state, head or base")
	}
	return p, nil
}

func compare(repo, base, head string) (comparison, error) {
	var c comparison
	b, err := fleet.GH("api", "repos/"+repo+"/compare/"+base+"..."+head)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.Status == "" {
		return c, fmt.Errorf("compare: no status in the answer")
	}
	return c, nil
}

func update(repo string, n int, head string) error {
	_, err := fleet.GH("api", "-X", "PUT",
		fmt.Sprintf("repos/%s/pulls/%d/update-branch", repo, n),
		"-f", "expected_head_sha="+head)
	return err
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// report prints every pull request and every bucket total, and asserts that the
// buckets add up to the pull requests examined.
func report(stdout, stderr io.Writer, all []result) int {
	counts := map[verdict]int{}
	for _, v := range order {
		counts[v] = 0
	}
	for _, r := range all {
		counts[r.verdict]++
	}
	sort.Slice(all, func(i, j int) bool { return all[i].number < all[j].number })
	for _, r := range all {
		fmt.Fprintf(stdout, "%-9s #%d  %s\n", strings.ToUpper(r.verdict.String()), r.number, r.note)
	}

	sum := 0
	var cols []string
	for _, v := range order {
		sum += counts[v]
		cols = append(cols, fmt.Sprintf("%s: %d", v, counts[v]))
	}
	fmt.Fprintf(stdout, "pull requests examined: %d  %s\n", sum, strings.Join(cols, "  "))
	if sum != len(all) {
		fmt.Fprintf(stderr, "prrefresh: %d bucketed but %d examined — the report is wrong about itself\n",
			sum, len(all))
		return 2
	}

	if counts[unjudged] > 0 {
		return 2
	}
	// conflict, moved and notopen all mean the same thing to the caller: this
	// pull request did not end up current and no further run of this command
	// will change that on its own.
	if counts[conflict] > 0 || counts[moved] > 0 || counts[notopen] > 0 || counts[behind] > 0 {
		return 1
	}
	return 0
}
