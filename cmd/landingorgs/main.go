// landingorgs lists the organisations a landing rollout should look at, one per
// line: every organisation the authenticated account is a member of, read LIVE
// from the API (GET user/orgs, paginated). Whether an organisation has a
// <org>.github.io, and which landingkit it pins, is `landingmigrate inventory`'s
// question, asked over this list.
//
// ⛔ IT READS NO CACHE BY DEFAULT. A rollout fed from fleetfind's cached
// inventory (342 organisations with a landing) missed go-blas and go-theca, two
// organisations created or joined after the cache was taken, and left them on
// an old kit. `--fleetfind FILE` still reads that cache, and says so on stderr
// every time, because the list it prints is then a snapshot and not the fleet.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/spf13/cobra"
)

type org struct {
	Login string `json:"login"`
}

// parsePages reads what `gh api --paginate` prints for an array endpoint: one
// JSON array per page, back to back. An answer with no organisation is an
// unread answer, never an empty account.
func parsePages(r io.Reader) ([]string, error) {
	dec := json.NewDecoder(r)
	var out []string
	pages := 0
	for {
		var page []org
		if err := dec.Decode(&page); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("a page did not decode: %v", err)
		}
		pages++
		for _, o := range page {
			if o.Login != "" {
				out = append(out, o.Login)
			}
		}
	}
	if pages == 0 || len(out) == 0 {
		return nil, fmt.Errorf("the API answered no organisation: an unread answer, not an empty account")
	}
	sort.Strings(out)
	return out, nil
}

type repo struct {
	Org      string `json:"org"`
	Name     string `json:"name"`
	Archived bool   `json:"archived"`
}

// fromCache lists the organisations owning an unarchived <org>.github.io in a
// fleetfind inventory file.
func fromCache(r io.Reader) ([]string, error) {
	var inv struct {
		Repos []repo `json:"repos"`
	}
	if err := json.NewDecoder(r).Decode(&inv); err != nil {
		return nil, err
	}
	if len(inv.Repos) == 0 {
		return nil, fmt.Errorf("the inventory holds no repository: wrong file, not an empty fleet")
	}
	var out []string
	for _, x := range inv.Repos {
		if x.Name == x.Org+".github.io" && !x.Archived {
			out = append(out, x.Org)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ghOrgs is the live read; a test replaces it.
var ghOrgs = func() ([]byte, error) {
	return exec.Command("gh", "api", "--paginate", "user/orgs?per_page=100").Output()
}

const longHelp = `Lists the organisations of the authenticated account, one per line, read live
from the API. Feed it to ` + "`landingmigrate inventory`" + `.

--fleetfind FILE reads fleetfind's cached inventory instead and says so loudly:
that list is a snapshot, and a rollout built from one missed organisations
created or joined after it was taken.`

func RootCmd() *cobra.Command {
	var cache string
	c := cli.New("landingorgs [--fleetfind CACHE.json]", "The organisations to roll a landing kit out to, read live", longHelp)
	c.Args = cobra.NoArgs
	c.Flags().StringVar(&cache, "fleetfind", "", "read this fleetfind inventory instead of the API (a snapshot, said loudly)")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		var list []string
		var err error
		if cache != "" {
			fmt.Fprintf(cli.Err(cmd), "landingorgs: READING A CACHE (%s), not the fleet: organisations created or joined since it was taken are missing\n", cache)
			f, e := os.Open(cache)
			if e != nil {
				return cli.Wrap(e)
			}
			defer f.Close()
			list, err = fromCache(f)
		} else {
			b, e := ghOrgs()
			if e != nil {
				return cli.Wrap(fmt.Errorf("gh api user/orgs: %v", e))
			}
			list, err = parsePages(bytes.NewReader(b))
		}
		if err != nil {
			return cli.Wrap(err)
		}
		for _, s := range list {
			fmt.Fprintln(cli.Out(cmd), s)
		}
		return nil
	}
	return c
}

func main() { cli.Run(RootCmd()) }
