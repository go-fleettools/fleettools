// Command ghrunnergroup lists an organisation's Actions runner groups and
// ensures one exists, restricted to named repositories.
//
// It exists because GARM scale sets join an explicit runner group (garm-cli
// scaleset add --runner-group) and no fleet program writes runner groups; a
// hand-written `gh api` write is refused by the guard.
//
// Same model as ghrel: the token comes from a 0600 file (default
// ~/.github-token, scope admin:org) and goes only in the Authorization
// header; https only (http for 127.0.0.1 test servers); redirects refused;
// responses bounded; --dry-run sends only reads.
//
// References (read 2026-11-27):
//
//	https://docs.github.com/en/rest/actions/self-hosted-runner-groups
//	  GET  /orgs/{org}/actions/runner-groups                      200, {total_count, runner_groups[{id,name,visibility,default,allows_public_repositories}]}, per_page<=100
//	  POST /orgs/{org}/actions/runner-groups                      201, body name, visibility all|selected|private, selected_repository_ids[], allows_public_repositories
//	  PUT  /orgs/{org}/actions/runner-groups/{id}/repositories/{repo_id}  204, visibility must be "selected"
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/go-fleettools/fleettools/internal/ghtoken"
	"github.com/spf13/cobra"
)

const (
	maxResponse = 1 << 20
	timeout     = 30 * time.Second
	apiBase     = "https://api.github.com"
)

const longHelp = `Lists an organisation's runner groups, or ensures one exists.

  ghrunnergroup <org> list
  ghrunnergroup <org> ensure <name> --repos r1,r2 [--allow-public] [--dry-run]

ensure creates the group with visibility "selected" restricted to the named
repositories of the organisation, or, when a group of that name exists, adds
the missing repositories to it (it never removes one, never changes
visibility, and refuses a group whose visibility is not "selected").
--allow-public sets allows_public_repositories on creation only; an existing
group that disagrees is reported, not changed.

The token is read only from a file (default ~/.github-token, owner-only).
Exit status: 0 done, 2 refused or failed.`

var newClient = func(d time.Duration) *http.Client {
	return &http.Client{
		Timeout:       d,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

var readToken = ghtoken.Read

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

type opts struct {
	base, tokFile string
	repos         []string
	allowPublic   bool
	dryRun        bool
}

// RootCmd builds the command; tests drive it with SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	var o opts
	cmd := &cobra.Command{
		Use:          "ghrunnergroup <org> list|ensure [name] [flags]",
		Short:        "List or ensure an organisation's Actions runner groups",
		Long:         longHelp,
		Args:         cobra.MinimumNArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, args, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.base, "base", apiBase, "API base URL (https)")
	f.StringVar(&o.tokFile, "token-file", "", "file holding the token (default ~/.github-token)")
	f.StringSliceVar(&o.repos, "repos", nil, "ensure: repositories (names in the org) the group is restricted to")
	f.BoolVar(&o.allowPublic, "allow-public", false, "ensure: allow public repositories (creation only)")
	f.BoolVar(&o.dryRun, "dry-run", false, "send only reads; print what would be written")
	return cmd
}

type group struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
	Default    bool   `json:"default"`
	Public     bool   `json:"allows_public_repositories"`
}

func run(cmd *cobra.Command, args []string, o opts) error {
	out := cmd.OutOrStdout()
	org := strings.TrimSpace(args[0])
	if org == "" || strings.ContainsAny(org, "/ ?#\r\n") {
		return fmt.Errorf("organisation %q: want a plain name", args[0])
	}
	if !strings.HasPrefix(o.base, "https://") && !isTestBase(o.base) {
		return fmt.Errorf("base %q: https only", o.base)
	}
	switch args[1] {
	case "list":
		if len(args) != 2 {
			return fmt.Errorf("list takes no argument")
		}
	case "ensure":
		if len(args) != 3 || strings.TrimSpace(args[2]) == "" {
			return fmt.Errorf("ensure wants exactly one group name")
		}
		if len(o.repos) == 0 {
			return fmt.Errorf("ensure needs --repos: a group open to every repository is not made here")
		}
		for _, r := range o.repos {
			if r == "" || strings.ContainsAny(r, "/ ?#\r\n") {
				return fmt.Errorf("repository %q: want a plain name in %s", r, org)
			}
		}
	default:
		return fmt.Errorf("unknown verb %q: want list or ensure", args[1])
	}
	tok, err := readToken(o.tokFile)
	if err != nil {
		return err
	}
	c := newClient(timeout)
	groups, err := listGroups(c, o.base, tok, org)
	if err != nil {
		return err
	}
	if args[1] == "list" {
		for _, g := range groups {
			fmt.Fprintf(out, "%d\t%s\tvisibility=%s\tdefault=%t\tallows_public=%t\n", g.ID, g.Name, g.Visibility, g.Default, g.Public)
		}
		return nil
	}
	return ensure(out, c, o, tok, org, strings.TrimSpace(args[2]), groups)
}

func listGroups(c *http.Client, base, tok, org string) ([]group, error) {
	var all []group
	for page := 1; ; page++ {
		b, err := do(c, "GET", fmt.Sprintf("%s/orgs/%s/actions/runner-groups?per_page=100&page=%d", base, org, page), tok, nil, 200)
		if err != nil {
			return nil, err
		}
		var r struct {
			Total  int     `json:"total_count"`
			Groups []group `json:"runner_groups"`
		}
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, fmt.Errorf("runner groups: %w", err)
		}
		all = append(all, r.Groups...)
		if len(r.Groups) == 0 || len(all) >= r.Total {
			return all, nil
		}
	}
}

func repoID(c *http.Client, base, tok, org, repo string) (int64, error) {
	b, err := do(c, "GET", fmt.Sprintf("%s/repos/%s/%s", base, org, repo), tok, nil, 200)
	if err != nil {
		return 0, err
	}
	var r struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(b, &r); err != nil || r.ID == 0 {
		return 0, fmt.Errorf("repository %s/%s: no id in the reply", org, repo)
	}
	return r.ID, nil
}

func ensure(out io.Writer, c *http.Client, o opts, tok, org, name string, groups []group) error {
	ids := make([]int64, 0, len(o.repos))
	for _, r := range o.repos {
		id, err := repoID(c, o.base, tok, org, r)
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	var found *group
	for i := range groups {
		if groups[i].Name == name {
			found = &groups[i]
		}
	}
	if found == nil {
		payload, _ := json.Marshal(map[string]any{
			"name": name, "visibility": "selected",
			"selected_repository_ids": ids, "allows_public_repositories": o.allowPublic,
		})
		if o.dryRun {
			fmt.Fprintf(out, "would create group %q (selected, repos %v, allow_public=%t)\n", name, o.repos, o.allowPublic)
			return nil
		}
		b, err := do(c, "POST", fmt.Sprintf("%s/orgs/%s/actions/runner-groups", o.base, org), tok, payload, 201)
		if err != nil {
			return err
		}
		var g group
		if err := json.Unmarshal(b, &g); err != nil {
			return fmt.Errorf("created group: %w", err)
		}
		fmt.Fprintf(out, "created group %d %q visibility=%s allows_public=%t\n", g.ID, g.Name, g.Visibility, g.Public)
		return nil
	}
	if found.Visibility != "selected" {
		return fmt.Errorf("group %d %q has visibility %q: refusing to add repositories to it", found.ID, name, found.Visibility)
	}
	if found.Public != o.allowPublic {
		fmt.Fprintf(out, "note: group %d allows_public=%t, requested %t (not changed)\n", found.ID, found.Public, o.allowPublic)
	}
	for i, id := range ids {
		if o.dryRun {
			fmt.Fprintf(out, "would add %s to group %d\n", o.repos[i], found.ID)
			continue
		}
		if _, err := do(c, "PUT", fmt.Sprintf("%s/orgs/%s/actions/runner-groups/%d/repositories/%d", o.base, org, found.ID, id), tok, nil, 204); err != nil {
			return err
		}
		fmt.Fprintf(out, "group %d: %s added\n", found.ID, o.repos[i])
	}
	fmt.Fprintf(out, "group %d %q ready\n", found.ID, name)
	return nil
}

func do(c *http.Client, method, u, tok string, payload []byte, want int) ([]byte, error) {
	var rdr io.Reader
	if payload != nil {
		rdr = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, u, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != want {
		return nil, fmt.Errorf("%s %s: %s: %s", method, u, resp.Status, firstLine(string(b)))
	}
	return b, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return strings.TrimSpace(s)
}

func isTestBase(base string) bool {
	return strings.HasPrefix(base, "http://127.0.0.1:") || strings.HasPrefix(base, "http://[::1]:")
}
