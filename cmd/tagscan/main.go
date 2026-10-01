// tagscan reports every tagged Go module whose default branch is AHEAD of its
// latest tag — work that is merged and that `go get` cannot reach.
//
//	tagscan            # the whole fleet
//	tagscan -orgs a,b  # narrowed, and it says so
//
// It exists because a written rule was not enough. Merging a pull request on a
// tagged Go module with ghmerge alone leaves the module's consumers on the last
// tag, which does not contain the merge: `go get -u` then fails with
// `undefined: <the new symbol>` against a repository whose main plainly has it.
// That happened on go-widgets/toolkit on 2026-09-05 and again on go-pdfkit/ops
// on 2026-09-07, the second time with the rule already written down. A tool
// that reports the state beats a note that asks somebody to remember it.
//
// Being ahead is not by itself wrong: a module may be between releases on
// purpose. What is wrong is not KNOWING, which is why this prints the distance
// and the age rather than a verdict.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

// gh runs the CLI, retrying only what waiting can fix.
//
// The SECONDARY rate limit is a burst brake and lifts in seconds. The PRIMARY
// one is an hourly budget that resets at a fixed time, and no short backoff
// reaches it — matching "rate" alone made a spent budget sleep five minutes per
// call and fail anyway. See prscan.

type repo struct {
	FullName string `json:"full_name"`
	Default  string `json:"default_branch"`
	Archived bool   `json:"archived"`
	Fork     bool   `json:"fork"`
	Pushed   string `json:"pushed_at"`
}

// An ahead is one module with work its consumers cannot reach.
type ahead struct {
	repo, tag string
	commits   int
}

// orphaned is what "no common ancestor" means, and it is not a failed read.
//
// The tag predates a rewrite of the branch's history — a purge by blob id
// leaves the old line in place and the tag on it. Measured on
// go-filesystems/xfs: v0.1.0 points at a coherent 104-file tree of the same
// module from 2026-08-05, `go get ...@v0.1.0` serves it, and it is the
// repository's ONLY tag. So consumers are not broken; they are FROZEN, and no
// `go get -u` will ever move them, because there is no later version on the
// history they are on.
//
// It is worse than being merely ahead and reads the same from the outside,
// which is why it gets a line of its own rather than a place among the reads
// that failed.
const noCommonAncestor = "No common ancestor"

func main() {
	fleet.WarnIfStale(os.Stderr)
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the whole program, so a test can drive it and read what it says.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tagscan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	only := fs.String("orgs", "", "comma-separated organisations instead of the whole fleet")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	orgs, err := organisations(*only)
	if err != nil {
		fmt.Fprintln(stderr, "orgs:", err)
		return 1
	}
	if *only != "" {
		fmt.Fprintln(stdout, "NARROWED to the organisations named: this says nothing about the others.")
	}
	fmt.Fprintf(stdout, "orgs: %d\n", len(orgs))

	var mu sync.Mutex
	var repos []repo
	var unreadOrgs []string
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for _, o := range orgs {
		wg.Add(1)
		go func(o string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			b, err := fleet.GH("api", "orgs/"+o+"/repos?per_page=100", "--paginate")
			if err != nil {
				mu.Lock()
				unreadOrgs = append(unreadOrgs, o+": "+firstLine(err.Error()))
				mu.Unlock()
				return
			}
			dec := json.NewDecoder(strings.NewReader(string(b)))
			for {
				var page []repo
				if dec.Decode(&page) != nil {
					break
				}
				mu.Lock()
				for _, r := range page {
					if !r.Archived && !r.Fork {
						repos = append(repos, r)
					}
				}
				mu.Unlock()
			}
		}(o)
	}
	wg.Wait()
	fmt.Fprintf(stdout, "repos: %d\n", len(repos))

	var aheads, orphans []ahead
	var unread []string
	tagged, checked := 0, 0
	for _, r := range repos {
		wg.Add(1)
		go func(r repo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			tag, sha, head, err := latestTag(r)
			if err != nil {
				mu.Lock()
				unread = append(unread, r.FullName+": "+firstLine(err.Error()))
				mu.Unlock()
				return
			}
			if tag == "" {
				return // never tagged: nothing to be ahead of
			}
			mu.Lock()
			tagged++
			mu.Unlock()
			// The tag IS the head: not ahead, settled without an API call.
			if sha != "" && sha == head {
				mu.Lock()
				checked++
				mu.Unlock()
				return
			}
			n, err := aheadBy(r, tag)
			if err != nil {
				mu.Lock()
				if strings.Contains(err.Error(), noCommonAncestor) {
					checked++
					orphans = append(orphans, ahead{r.FullName, tag, 0})
				} else {
					unread = append(unread, r.FullName+": "+firstLine(err.Error()))
				}
				mu.Unlock()
				return
			}
			mu.Lock()
			checked++
			if n > 0 {
				aheads = append(aheads, ahead{r.FullName, tag, n})
			}
			mu.Unlock()
		}(r)
	}
	wg.Wait()

	sort.Slice(aheads, func(i, j int) bool {
		if aheads[i].commits != aheads[j].commits {
			return aheads[i].commits > aheads[j].commits
		}
		return aheads[i].repo < aheads[j].repo
	})
	fmt.Fprintf(stdout, "tagged: %d   checked: %d of %d   AHEAD of their latest tag: %d\n",
		tagged, checked, tagged, len(aheads))
	for _, a := range aheads {
		fmt.Fprintf(stdout, "  %-46s latest tag %-10s +%d commit(s) its consumers cannot reach\n",
			a.repo, a.tag, a.commits)
	}
	if len(orphans) > 0 {
		sort.Slice(orphans, func(i, j int) bool { return orphans[i].repo < orphans[j].repo })
		fmt.Fprintf(stdout, "\nFROZEN: %d module(s) whose only tag sits on a history the branch no longer\n"+
			"shares. The tag still resolves, so nothing looks wrong -- but it names a tree\n"+
			"from before the rewrite, and no `go get -u` will ever move off it.\n", len(orphans))
		for _, a := range orphans {
			fmt.Fprintf(stdout, "  %-46s latest tag %s\n", a.repo, a.tag)
		}
	}

	// A pass that could not read is not a pass that found nothing.
	if len(unreadOrgs) == 0 && len(unread) == 0 {
		return 0
	}
	fmt.Fprintln(stdout, "\nINCOMPLETE: this pass says nothing about what follows.")
	report(stdout, "organisations whose repositories could not be listed", unreadOrgs, len(orgs))
	report(stdout, "repositories whose tags could not be read", unread, len(repos))
	return 1
}

// organisations is the fleet, or the list the caller named.
func organisations(only string) ([]string, error) {
	if only != "" {
		var out []string
		for _, o := range strings.Split(only, ",") {
			if o = strings.TrimSpace(o); o != "" {
				out = append(out, o)
			}
		}
		return out, nil
	}
	b, err := fleet.GH("api", "user/orgs", "--paginate", "--jq", ".[].login")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out, nil
}

// latestTag is the newest version tag and the commit it points at, with the
// default branch's own head beside it.
//
// It reads TAGS and not releases, and the difference is the whole point. `go
// get` resolves a TAG; a GitHub release is a separate object that a tag need
// not have. The first draft of this asked /releases/latest and reported
// go-pdfkit/ops as +4 behind v0.11.0 fifteen minutes after v0.12.0 had been
// pushed and PROVED to resolve from the module proxy. The tool was asking a
// question whose answer no consumer depends on.
//
// git ls-remote costs no API budget and needs no token, which is why ghrelease
// uses it too. An annotated tag's ref points at the tag object, so the peeled
// "^{}" entry is preferred where there is one.
func latestTag(r repo) (tag, sha, head string, err error) {
	out, err := lsRemote("https://github.com/" + r.FullName)
	if err != nil {
		return "", "", "", err
	}
	peeled, plain := map[string]string{}, map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		switch {
		case f[1] == "refs/heads/"+r.Default:
			head = f[0]
		case strings.HasSuffix(f[1], "^{}"):
			peeled[strings.TrimSuffix(strings.TrimPrefix(f[1], "refs/tags/"), "^{}")] = f[0]
		case strings.HasPrefix(f[1], "refs/tags/"):
			plain[strings.TrimPrefix(f[1], "refs/tags/")] = f[0]
		}
	}
	best := ""
	for name := range plain {
		if !semverish(name) {
			continue
		}
		if best == "" || newer(name, best) {
			best = name
		}
	}
	if best == "" {
		return "", "", head, nil
	}
	if s, ok := peeled[best]; ok {
		return best, s, head, nil
	}
	return best, plain[best], head, nil
}

// lsRemote is a variable so the tests can answer without a network.
var lsRemote = func(url string) (string, error) {
	cmd := exec.Command("git", "ls-remote", "--tags", "--heads", url)
	var errb strings.Builder
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s", strings.TrimSpace(errb.String()))
	}
	return string(out), nil
}

// semverish accepts the shape Go modules require: a leading v and three
// numbers. A tag this does not recognise is not a version `go get` will pick
// as latest, so leaving it out understates rather than invents.
func semverish(tag string) bool {
	if !strings.HasPrefix(tag, "v") {
		return false
	}
	parts := strings.SplitN(strings.TrimPrefix(tag, "v"), ".", 3)
	if len(parts) != 3 {
		return false
	}
	for i, p := range parts {
		if i == 2 {
			p = strings.SplitN(p, "-", 2)[0] // v1.2.3-rc1
			p = strings.SplitN(p, "+", 2)[0]
		}
		if p == "" {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

// newer compares two semver-ish tags NUMERICALLY. A lexical compare puts v0.9.0
// after v0.10.0 and would report a module as ahead of a tag it is behind.
func newer(a, b string) bool {
	an, bn := nums(a), nums(b)
	for i := 0; i < 3; i++ {
		if an[i] != bn[i] {
			return an[i] > bn[i]
		}
	}
	return false
}

func nums(tag string) [3]int {
	var out [3]int
	parts := strings.SplitN(strings.TrimPrefix(tag, "v"), ".", 3)
	for i := 0; i < 3 && i < len(parts); i++ {
		p := parts[i]
		if i == 2 {
			p = strings.SplitN(strings.SplitN(p, "-", 2)[0], "+", 2)[0]
		}
		fmt.Sscan(p, &out[i])
	}
	return out
}

// aheadBy is how many commits the default branch has that the tag does not.
func aheadBy(r repo, tag string) (int, error) {
	b, err := fleet.GH("api", "repos/"+r.FullName+"/compare/"+tag+"..."+r.Default,
		"--jq", ".ahead_by")
	if err != nil {
		return 0, err
	}
	var n int
	if _, err := fmt.Sscan(strings.TrimSpace(string(b)), &n); err != nil {
		return 0, err
	}
	return n, nil
}

// report names what could not be read, with a sample of why.
func report(w io.Writer, what string, items []string, of int) {
	if len(items) == 0 {
		return
	}
	sort.Strings(items)
	fmt.Fprintf(w, "  %d of %d %s:\n", len(items), of, what)
	for i, it := range items {
		if i == 5 {
			fmt.Fprintf(w, "    ... and %d more\n", len(items)-5)
			break
		}
		fmt.Fprintf(w, "    %s\n", it)
	}
}

// firstLine keeps an error readable in a list of them.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 90 {
		s = s[:90] + "..."
	}
	return strings.TrimSpace(s)
}
