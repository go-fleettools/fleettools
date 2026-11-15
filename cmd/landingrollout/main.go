// landingrollout moves a list of landings (<org>/<org>.github.io) to a
// landing-kit version with `landingmigrate upgrade`, one pull request each,
// and merges the green ones. Two phases per batch so CI runs in parallel: open
// every pull request of the batch, then wait for and merge each. Every
// organisation ends in exactly one recorded state in --state (JSON lines, the
// LAST record of an organisation is its state; read it with landingrollsum),
// and a rerun skips the finished ones.
//
// ⛔ IT NEVER FORCES. A red pull request is left open and recorded with prwait's
// own verdict as the cause; a landing the upgrade refuses is recorded as
// skipped with the refusal, and nothing is written to it; one another session
// holds (agentsync) is recorded as claimed and retried by the next run.
//
// ⛔ IT WAITS FOR THE REST BUDGET instead of spending the reserve other sessions
// and Renovate share: below --floor (1500) it sleeps two minutes and asks
// again, and an answer it cannot read counts as "wait", never as "go".
//
// Measured 2026-11-15: 326 landings, 318 merged green in about three hours, of
// which most of the time was waiting for the budget (~75 REST calls a landing).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/spf13/cobra"
)

type result struct {
	Org    string `json:"org"`
	Status string `json:"status"` // merged, red, skipped, current, claimed, open, error
	Detail string `json:"detail,omitempty"`
	PR     int    `json:"pr,omitempty"`
	Time   string `json:"time"`
}

// run executes a program with a closed stdin and returns its combined output.
// A var so a test can stand in for every program the rollout drives.
var run = func(dir, name string, args ...string) (string, int, error) {
	c := exec.Command(name, args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
			err = nil
		}
	}
	return strings.TrimSpace(string(out)), code, err
}

var sleep = time.Sleep

func parseRemaining(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("the rate limit answer is not a number: %q", s)
	}
	return n, nil
}

// budget waits until the REST budget is at least floor.
func budget(floor int) error {
	for i := 0; i < 40; i++ {
		out, code, err := run("", "gh", "api", "rate_limit", "--jq", ".resources.core.remaining")
		if err == nil && code == 0 {
			if n, e := parseRemaining(out); e == nil && n >= floor {
				return nil
			} else if e == nil {
				fmt.Printf("REST budget %d < %d: pausing 2 min\n", n, floor)
			}
		}
		sleep(2 * time.Minute)
	}
	return fmt.Errorf("the REST budget stayed below %d for 80 minutes", floor)
}

func body(kit, did string) string {
	return "Brings the landing to landingkit " + kit + " (Refs go-fleettools/landingkit#55): the fork ribbon carries the GitHub mark to the right of its words and sits closer to the Digital Commons ribbon. Done by `landingmigrate upgrade`, which also applies the current recipe.\n\n" +
		"What it did here: " + did + "\n\n" +
		"CI builds the site and runs `landingkit check`.\n\n" +
		"🤖 Generated with [Claude Code](https://claude.com/claude-code)\n"
}

func commitMsg(kit, did string) string {
	return "landingkit " + kit + ": the GitHub mark in the fork ribbon\n\n" + did + "\n\nCo-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>\n"
}

type opened struct {
	org  string
	repo string
	pr   int
}

type options struct {
	list, work, state, kit string
	start, count, floor    int
	all                    bool
}

func RootCmd() *cobra.Command {
	var o options
	c := cli.New("landingrollout --list FILE --work DIR --state FILE [flags]",
		"Move landings to a kit version, one pull request each, merging only green", "")
	c.Args = cobra.NoArgs
	f := c.Flags()
	f.StringVar(&o.list, "list", "", "file of organisations, one per line (landingkitdist writes it)")
	f.StringVar(&o.work, "work", "", "durable directory for the clones")
	f.StringVar(&o.state, "state", "", "JSON-lines state file")
	f.StringVar(&o.kit, "kit", "", "kit version to require (vX.Y.Z)")
	f.IntVar(&o.start, "start", 0, "first index of the first batch")
	f.IntVar(&o.count, "count", 20, "batch size")
	f.IntVar(&o.floor, "floor", 1500, "pause below this many REST calls left")
	f.BoolVar(&o.all, "all", false, "go on through every batch to the end of the list")
	for _, n := range []string{"list", "work", "state", "kit"} {
		_ = c.MarkFlagRequired(n)
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if o.count < 1 {
			return cli.Unanswerable("--count must be at least 1")
		}
		return cli.Wrap(rollout(o))
	}
	return c
}

func rollout(o options) error {
	orgs, err := readLines(o.list)
	if err != nil {
		return err
	}
	if len(orgs) == 0 {
		return fmt.Errorf("%s holds no organisation: an empty list is not a finished rollout", o.list)
	}
	prior, err := readState(o.state)
	if err != nil {
		return err
	}
	done := map[string]bool{}
	for _, r := range prior {
		if r.Status != "open" && r.Status != "claimed" && r.Status != "error" {
			done[r.Org] = true
		}
	}
	if o.start >= len(orgs) {
		return fmt.Errorf("--start %d is past the %d organisations", o.start, len(orgs))
	}
	for b := o.start; b < len(orgs); b += o.count {
		if err := batch(orgs[b:min(len(orgs), b+o.count)], done, o); err != nil {
			return err
		}
		if !o.all {
			return nil
		}
	}
	return nil
}

func batch(orgs []string, done map[string]bool, o options) error {
	var pending []opened
	for _, org := range orgs {
		if done[org] {
			continue
		}
		if err := budget(o.floor); err != nil {
			return err
		}
		op, r := openOne(org, o.kit, o.work)
		if op != nil {
			pending = append(pending, *op)
		}
		if err := record(o.state, r); err != nil {
			return err
		}
	}
	for _, p := range pending {
		if err := budget(o.floor); err != nil {
			return err
		}
		if err := record(o.state, finish(p)); err != nil {
			return err
		}
	}
	return nil
}

func openOne(org, kit, work string) (*opened, result) {
	repo := org + "/" + org + ".github.io"
	mk := func(status, detail string, pr int) result {
		return result{Org: org, Status: status, Detail: detail, PR: pr}
	}
	if out, code, err := run("", "agentsync", "claim", "--note", "landingkit "+kit+" rollout", repo); err != nil || code != 0 {
		return nil, mk("claimed", clip(out), 0)
	}
	dir := filepath.Join(work, org)
	if _, err := os.Stat(dir); err == nil {
		os.RemoveAll(dir)
	}
	if out, code, err := run("", "git", "clone", "-q", "https://github.com/"+repo+".git", dir); err != nil || code != 0 {
		return nil, mk("error", "clone: "+clip(out), 0)
	}
	branch := "kit-" + kit
	if out, code, _ := run(dir, "git", "checkout", "-q", "-b", branch); code != 0 {
		return nil, mk("error", "branch: "+clip(out), 0)
	}
	out, code, err := run("", "landingmigrate", "upgrade", dir, "--kit", kit)
	if err != nil || code != 0 {
		return nil, mk("skipped", clip(out), 0)
	}
	did := strings.TrimPrefix(lastLine(out), "upgraded: ")
	if st, _, _ := run(dir, "git", "status", "--porcelain"); st == "" {
		return nil, mk("current", "upgrade changed nothing", 0)
	}
	msg := filepath.Join(work, org+".commit.txt")
	pr := filepath.Join(work, org+".pr.md")
	if err := os.WriteFile(msg, []byte(commitMsg(kit, did)), 0o644); err != nil {
		return nil, mk("error", err.Error(), 0)
	}
	if err := os.WriteFile(pr, []byte(body(kit, did)), 0o644); err != nil {
		return nil, mk("error", err.Error(), 0)
	}
	if out, code, _ := run(dir, "git", "add", "-A"); code != 0 {
		return nil, mk("error", "add: "+clip(out), 0)
	}
	if out, code, _ := run(dir, "git", "commit", "-q", "-F", msg); code != 0 {
		return nil, mk("error", "commit: "+clip(out), 0)
	}
	home, _ := os.UserHomeDir()
	if out, code, _ := run(dir, filepath.Join(home, ".local/bin/gitpush"), "-u", "origin", branch); code != 0 {
		return nil, mk("error", "push: "+clip(out), 0)
	}
	for _, c := range [][]string{{"prbody", pr, "--repo", repo}, {"paritycheck", "text", pr}} {
		if out, code, _ := run(dir, c[0], c[1:]...); code != 0 {
			return nil, mk("error", c[0]+": "+clip(out), 0)
		}
	}
	url, code, _ := run(dir, "gh", "pr", "create", "-R", repo, "--title", "landingkit "+kit+": the GitHub mark in the fork ribbon", "--body-file", pr, "--head", branch)
	if code != 0 {
		return nil, mk("error", "pr create: "+clip(url), 0)
	}
	n, e := strconv.Atoi(url[strings.LastIndex(url, "/")+1:])
	if e != nil {
		return nil, mk("error", "pr create answered "+clip(url), 0)
	}
	return &opened{org, repo, n}, mk("open", did, n)
}

func finish(o opened) result {
	mk := func(status, detail string) result {
		return result{Org: o.org, Status: status, Detail: detail, PR: o.pr}
	}
	out, code, _ := run("", "prwait", "-repo", o.repo, strconv.Itoa(o.pr))
	if code != 0 {
		return mk("red", clip(out))
	}
	out, code, _ = run("", "ghmerge", o.repo, strconv.Itoa(o.pr))
	if code != 0 {
		return mk("red", "ghmerge refused: "+clip(out))
	}
	return mk("merged", "")
}

func clip(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " | ")
	if len(s) > 400 {
		return s[:400] + "..."
	}
	return s
}

func lastLine(s string) string {
	i := strings.LastIndex(s, "\n")
	return s[i+1:]
}

func readLines(p string) ([]string, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if t := strings.TrimSpace(sc.Text()); t != "" {
			out = append(out, t)
		}
	}
	return out, sc.Err()
}

// readState reads the state file; a file that does not exist yet is an empty
// state, one that exists and cannot be read is an error.
func readState(p string) ([]result, error) {
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return nil, nil
	}
	ls, err := readLines(p)
	if err != nil {
		return nil, err
	}
	var rs []result
	for _, l := range ls {
		var r result
		if json.Unmarshal([]byte(l), &r) == nil {
			rs = append(rs, r)
		}
	}
	return rs, nil
}

func record(p string, r result) error {
	r.Time = time.Now().Format(time.RFC3339)
	b, _ := json.Marshal(r)
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "%s\n", b); err != nil {
		return err
	}
	fmt.Printf("%-28s %-8s #%d %s\n", r.Org, r.Status, r.PR, r.Detail)
	return nil
}

func main() { cli.Run(RootCmd()) }
