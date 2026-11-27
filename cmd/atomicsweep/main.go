// Command atomicsweep lists committed mutsweep temp files in the default branch
// of every repository an organisation pushed to since a date.
//
// ⛔ WHY IT EXISTS. mutsweep leaves `.<name>.go.atomicwrite-<digits>` beside a
// source file it was mutating when it is killed, and `git add -A` committed
// that file and shipped it in releases four times (go-fde/veracrypt v0.8.0,
// go-filesystems/smb v0.17.2, go-filesystems/remote, go-fde/luks, go-volumes/
// safeio). The pre-push guard now refuses a NEW one; this finds the ones
// already in a tree.
//
// ⛔ A REPOSITORY IT COULD NOT READ IS UNREAD, NEVER CLEAN: a failed listing, a
// failed or unparseable tree call, a truncated tree (GitHub caps it, and a
// path beyond the cut would read as absent) and a budget wait that failed are
// each reported UNREAD and make the exit status 2.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// temp is the name mutsweep gives its temp copy.
var temp = regexp.MustCompile(`(^|/)\.[^/]+\.atomicwrite-[0-9]+$`)

// runGH is the exec seam, replaced by the tests.
var runGH = func(args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	cmd.Stdin = nil
	return cmd.Output()
}

// waitBudget blocks until the REST budget has room for another call; replaced
// by the tests.
var waitBudget = func() error {
	cmd := exec.Command("ratewait", "--rest", "50", "--timeout", "15m")
	cmd.Stdin = nil
	_, err := cmd.Output()
	return err
}

type repo struct {
	Name     string    `json:"name"`
	PushedAt time.Time `json:"pushedAt"`
}

type tree struct {
	Truncated bool `json:"truncated"`
	Tree      []struct {
		Path string `json:"path"`
	} `json:"tree"`
}

// hits returns the temp paths of a tree listing, in order.
func hits(t tree) []string {
	var out []string
	for _, e := range t.Tree {
		if temp.MatchString(e.Path) {
			out = append(out, e.Path)
		}
	}
	return out
}

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	c := cli.New("atomicsweep --since YYYY-MM-DD ORG...",
		"List committed mutsweep .atomicwrite temp files in repositories pushed since a date",
		`Lists each organisation's repositories (one GraphQL call per organisation), keeps
those pushed on or after --since, and reads each default branch's tree
(GET repos/O/R/git/trees/HEAD?recursive=1) looking for a path matching
(^|/)\.[^/]+\.atomicwrite-[0-9]+$. Before each tree it runs ratewait --rest 50.

A repository it could not read is UNREAD, never clean.

exit 0 every repository read, none holds a temp file
exit 1 at least one temp file found (and every repository was read)
exit 2 something could not be read, or the question could not be put`)
	since := c.Flags().String("since", "", "only repositories pushed on or after this date (YYYY-MM-DD)")

	c.RunE = func(c *cobra.Command, orgs []string) error {
		if cli.HelpRequested(c) {
			return nil
		}
		from, err := time.Parse("2006-01-02", *since)
		if err != nil {
			return cli.Unanswerable("--since must be a date YYYY-MM-DD, got %q", *since)
		}
		if len(orgs) == 0 {
			return cli.Unanswerable("name at least one organisation")
		}
		out := cli.Out(c)
		read, hit, unread := 0, 0, 0
		for _, org := range orgs {
			body, err := runGH("repo", "list", org, "--limit", "1000", "--json", "name,pushedAt")
			var repos []repo
			if err == nil {
				err = json.Unmarshal(body, &repos)
			}
			if err != nil {
				fmt.Fprintf(out, "UNREAD-ORG %s %v\n", org, err)
				unread++
				continue
			}
			for _, r := range repos {
				if r.PushedAt.Before(from) {
					continue
				}
				full := org + "/" + r.Name
				if err := waitBudget(); err != nil {
					fmt.Fprintf(out, "UNREAD %s budget wait: %v\n", full, err)
					unread++
					continue
				}
				body, err := runGH("api", "repos/"+full+"/git/trees/HEAD?recursive=1")
				var t tree
				if err == nil {
					err = json.Unmarshal(body, &t)
				}
				if err != nil {
					fmt.Fprintf(out, "UNREAD %s %v\n", full, err)
					unread++
					continue
				}
				if t.Truncated {
					fmt.Fprintf(out, "UNREAD %s tree truncated\n", full)
					unread++
					continue
				}
				read++
				if h := hits(t); len(h) > 0 {
					hit++
					fmt.Fprintf(out, "HIT %s %s\n", full, strings.Join(h, " "))
				}
			}
		}
		fmt.Fprintf(out, "SUMMARY read=%d hits=%d unread=%d\n", read, hit, unread)
		if unread > 0 {
			return cli.Unanswerable("%d repositories or organisations could not be read", unread)
		}
		if hit > 0 {
			return cli.Found("%d repositories hold a temp file", hit)
		}
		return nil
	}
	return c
}
