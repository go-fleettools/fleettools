// renovatedup finds repositories that apply their organisation's Renovate
// preset twice.
//
//	renovatedup                      # every organisation the account belongs to
//	renovatedup --orgs go-crdt,go-tex
//
// The defect (go-crdt/.github#45, measured with a debug run): an organisation's
// self-hosted config.js sets inheritConfig, inheritConfigRepoName and
// inheritConfigFileName, and Renovate then merges that inherited file into EVERY
// repository -- and also reads the repository's own renovate.json. A repository
// whose renovate.json says `extends: ["github>ORG/.github"]` therefore applies
// the same default.json a second time, and every customManager is registered
// twice (the dashboard showed `regex (4)` where it should say `(2)`).
//
// Cost: ONE GraphQL query per organisation page of 100 repositories, with each
// repository's candidate config files as `object(expression: "HEAD:<path>")`
// aliases and the .github repository's config.js beside them. GraphQL has its
// own budget, separate from the REST one other sessions exhaust.
//
// Exit status: 0 no duplicate and everything read, 1 at least one duplicate,
// 2 something could not be read -- an organisation's query failed, a config is
// not strict JSON, no organisation at all. An unread is not a clean answer, so
// 2 outranks 1.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Finds repositories that apply their organisation's Renovate preset twice.

When <org>/.github/config.js sets inheritConfig: true, Renovate merges the
inherited file (inheritConfigRepoName / inheritConfigFileName, default
{{parentOrg}}/renovate-config and org-inherited-config.json) into every
repository AND reads the repository's own config. An extends entry that names
the inherited file therefore applies it twice and registers every customManager
twice. Only an entry that IS the inherited repo and file is flagged:
github>ORG/.github or local>ORG/.github, optionally with :default, :default.json
or #ref; the organisation is compared case-insensitively.

Per hit: STUB (the file holds only that extends, and maybe $schema: the whole
file is removable) or MIXED (other keys or presets too: remove only the entry).
Archived repositories and forks are skipped.

Renovate's config files are read in its order (renovate.json, renovate.json5,
.github/renovate.json, .github/renovate.json5, .gitlab/renovate.json,
.renovaterc, .renovaterc.json, .renovaterc.json5); the first one present is the
one judged. The "renovate" key of package.json is NOT read. Files are parsed as
strict JSON: JSON5 features make a file UNREAD, with the reason.

Cost: one GraphQL query per 100 repositories of each organisation; no REST.
When GitHub answers HTTP 502 or 504 (its GraphQL time limit), the same page is
asked again with half as many repositories, down to one, and each halving is
printed; the smaller size is kept for the rest of that organisation.

Exit status: 0 no duplicate and everything read, 1 a duplicate found, 2 something
could not be read (a failed query, an unparsable config, no organisation) --
2 outranks 1, an unread is not a clean answer.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

type options struct {
	orgs string
}

// RootCmd builds the command. Tests drive it with SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command { return rootWith(fetchGraphQL, fleet.Orgs) }

func rootWith(fetch fetchFunc, orgs func() ([]string, error)) *cobra.Command {
	var o options
	c := cli.New("renovatedup [--orgs ORG[,ORG...]]",
		"Find repositories applying their organisation's Renovate preset twice", longHelp)
	c.Flags().StringVar(&o.orgs, "orgs", "", "comma-separated organisations (default: every one the account belongs to)")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return cli.Unanswerable("usage: renovatedup [--orgs ORG[,ORG...]]")
		}
		list, err := orgList(o.orgs, orgs)
		if err != nil {
			return cli.Unanswerable("listing organisations: %v", err)
		}
		return cli.FromStatus(run(list, cli.Out(cmd), cli.Err(cmd), fetch))
	}
	return c
}

func orgList(flagValue string, all func() ([]string, error)) ([]string, error) {
	if flagValue == "" {
		return all()
	}
	var out []string
	for _, s := range strings.Split(flagValue, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// fetchFunc runs the organisation query for one page of at most first
// repositories and returns the raw JSON.
type fetchFunc func(org, cursor string, first int) ([]byte, error)

// pageSize is the first page asked for: GraphQL's maximum.
const pageSize = 100

// gatewayGaveUp reports a GitHub front end that gave up on the query, which
// for GraphQL is its ~10 s execution limit: openweft answered HTTP 502 four
// times out of four over four days (2026-11-23 to 2026-11-27) to a page of
// 100 repositories x 9 blob objects. The same cursor asked for fewer
// repositories is a smaller query, not the same one again.
func gatewayGaveUp(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "HTTP 502") || strings.Contains(msg, "HTTP 504")
}

// fetchShrinking asks for one page and, while the gateway gives up, asks for
// the SAME cursor with half as many repositories. Nothing of a failed page was
// read, so nothing is counted twice. The smaller size is kept for the pages
// that follow. A page of one that still fails is the answer, returned as is.
// Every halving is printed, so a slow organisation is visible as one.
func fetchShrinking(fetch fetchFunc, org, cursor string, size *int, stdout io.Writer) ([]byte, error) {
	// The condition is on the way BACK into the loop, not out of it: what is
	// not a halving returns, so a broken condition ends the loop instead of
	// spinning on a successful answer.
	for {
		raw, err := fetch(org, cursor, *size)
		if err != nil && gatewayGaveUp(err) && *size > 1 {
			*size /= 2
			fmt.Fprintf(stdout, "%s  page size halved to %d after: %v\n", org, *size, err)
			continue
		}
		return raw, err
	}
}

const fileFragment = `fragment B on Blob { text isTruncated }`

func buildQuery() string {
	var b strings.Builder
	b.WriteString(`query($org: String!, $cursor: String, $first: Int!) { organization(login: $org) {
 cfg: repository(name: ".github") { object(expression: "HEAD:config.js") { ...B } }
 repositories(first: $first, after: $cursor) {
  pageInfo { hasNextPage endCursor }
  nodes { name isArchived isFork`)
	for i, p := range candidates {
		fmt.Fprintf(&b, "\n   f%d: object(expression: \"HEAD:%s\") { ...B }", i, p)
	}
	b.WriteString("\n  } } } }\n" + fileFragment)
	return b.String()
}

// ghAnswer is the gh seam; a test replaces it.
var ghAnswer = fleet.GHAnswer

func fetchGraphQL(org, cursor string, first int) ([]byte, error) {
	// -F, not -f: $first is an Int!, and -f would send the string "100".
	args := []string{"api", "graphql", "-f", "query=" + buildQuery(), "-f", "org=" + org, "-F", "first=" + strconv.Itoa(first)}
	if cursor != "" {
		args = append(args, "-f", "cursor="+cursor)
	}
	out, err := ghAnswer(args...)
	if err != nil && json.Valid(out) {
		// gh exits non-zero when the answer carries `errors`, but prints the
		// answer all the same: decodePage judges which errors are tolerable.
		return out, nil
	}
	return out, err
}

type blob struct {
	Text        *string `json:"text"`
	IsTruncated bool    `json:"isTruncated"`
}

type page struct {
	Data struct {
		Organization *struct {
			Cfg *struct {
				Object *blob `json:"object"`
			} `json:"cfg"`
			Repositories struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []map[string]json.RawMessage `json:"nodes"`
			} `json:"repositories"`
		} `json:"organization"`
	} `json:"data"`
	Errors []struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Path    []any  `json:"path"`
	} `json:"errors"`

	// noGithub: the answer carried the one tolerated error, NOT_FOUND on
	// organization.cfg -- the organisation has no .github repository.
	noGithub bool
}

// isNoGithub: exactly NOT_FOUND at ["organization","cfg"].
func isNoGithub(typ string, path []any) bool {
	return typ == "NOT_FOUND" && len(path) == 2 && path[0] == "organization" && path[1] == "cfg"
}

func decodePage(b []byte) (*page, error) {
	var p page
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("decoding the GraphQL answer: %w", err)
	}
	for _, e := range p.Errors {
		if !isNoGithub(e.Type, e.Path) {
			return nil, fmt.Errorf("GraphQL: %s", e.Message)
		}
		p.noGithub = true
	}
	if p.Data.Organization == nil {
		return nil, errors.New("GraphQL answered no organisation")
	}
	if p.noGithub && p.Data.Organization.Cfg != nil {
		return nil, errors.New("GraphQL: NOT_FOUND on .github yet a .github answer")
	}
	return &p, nil
}

type tally struct {
	orgsRead, orgsUnread, orgsInherit int
	repos, stub, mixed, unread        int
}

func run(orgs []string, stdout, stderr io.Writer, fetch fetchFunc) int {
	if len(orgs) == 0 {
		fmt.Fprintln(stderr, "renovatedup: no organisation to look at")
		return 2
	}
	var t tally
	for _, org := range orgs {
		if err := scanOrg(org, stdout, &t, fetch); err != nil {
			t.orgsUnread++
			fmt.Fprintf(stderr, "renovatedup: %s: %v\n", org, err)
			fmt.Fprintf(stdout, "%s  UNREAD  %v\n", org, err)
			continue
		}
		t.orgsRead++
	}
	fmt.Fprintf(stdout, "\n%d org(s) read, %d unread, %d with inheritConfig, %d repo(s) read, %d STUB, %d MIXED, %d UNREAD\n",
		t.orgsRead, t.orgsUnread, t.orgsInherit, t.repos, t.stub, t.mixed, t.unread)
	switch {
	case t.orgsUnread > 0 || t.unread > 0:
		return 2
	case t.stub+t.mixed > 0:
		return 1
	}
	return 0
}

// scanOrg reads one organisation. Counters of repositories are only added to
// the tally for an organisation that was read to the end: a half-read one is
// reported as unread, not as a smaller clean one.
func scanOrg(org string, stdout io.Writer, t *tally, fetch fetchFunc) error {
	var (
		local  tally
		lines  []string
		cursor string
		inh    Inherited
		on     bool
		size   = pageSize
	)
	for first := true; ; first = false {
		raw, err := fetchShrinking(fetch, org, cursor, &size, stdout)
		if err != nil {
			return err
		}
		p, err := decodePage(raw)
		if err != nil {
			return err
		}
		o := p.Data.Organization
		if first {
			if p.noGithub {
				fmt.Fprintf(stdout, "%s  no .github repository (nothing inherited, nothing to duplicate)\n", org)
				return nil
			}
			text := ""
			if o.Cfg != nil && o.Cfg.Object != nil && o.Cfg.Object.Text != nil {
				if o.Cfg.Object.IsTruncated {
					return errors.New(".github/config.js is truncated")
				}
				text = *o.Cfg.Object.Text
			}
			if on, inh, err = ParseOrgConfig(text, org); err != nil {
				return fmt.Errorf(".github/config.js: %w", err)
			}
			if !on {
				fmt.Fprintf(stdout, "%s  inheritConfig off (nothing inherited, nothing to duplicate)\n", org)
				return nil
			}
			local.orgsInherit++
			fmt.Fprintf(stdout, "%s  inheritConfig on, inherited preset %s %s\n", org, inh.Repo, inh.File)
		}
		for _, n := range o.Repositories.Nodes {
			scanRepo(org, n, inh, &local, &lines)
		}
		if !o.Repositories.PageInfo.HasNextPage {
			break
		}
		if o.Repositories.PageInfo.EndCursor == "" {
			return errors.New("GraphQL says another page exists but gave no cursor")
		}
		cursor = o.Repositories.PageInfo.EndCursor
	}
	for _, l := range lines {
		fmt.Fprintln(stdout, l)
	}
	t.orgsInherit += local.orgsInherit
	t.repos += local.repos
	t.stub += local.stub
	t.mixed += local.mixed
	t.unread += local.unread
	return nil
}

func scanRepo(org string, n map[string]json.RawMessage, in Inherited, t *tally, lines *[]string) {
	var name string
	var archived, fork bool
	_ = json.Unmarshal(n["name"], &name)
	_ = json.Unmarshal(n["isArchived"], &archived)
	_ = json.Unmarshal(n["isFork"], &fork)
	if archived || fork {
		return
	}
	full := org + "/" + name
	for i, path := range candidates {
		var b *blob
		if json.Unmarshal(n[fmt.Sprintf("f%d", i)], &b) != nil || b == nil {
			continue // absent (null) -- or a key the answer lacks, which decodes to nil too
		}
		// First file present is the one Renovate uses.
		if b.IsTruncated || b.Text == nil {
			t.unread++
			*lines = append(*lines, fmt.Sprintf("%s  UNREAD  %s: truncated or binary", full, path))
			return
		}
		kind, entry, err := Classify(*b.Text, in)
		if err != nil {
			t.unread++
			*lines = append(*lines, fmt.Sprintf("%s  UNREAD  %s: %v", full, path, err))
			return
		}
		t.repos++
		switch kind {
		case Stub:
			t.stub++
			*lines = append(*lines, fmt.Sprintf("%s  STUB  %s: extends %q duplicates the inherited %s -- remove the file", full, path, entry, in.File))
		case Mixed:
			t.mixed++
			*lines = append(*lines, fmt.Sprintf("%s  MIXED  %s: extends %q duplicates the inherited %s -- remove that entry only", full, path, entry, in.File))
		}
		return
	}
	t.repos++ // no Renovate config file of its own: read, and clean
}
