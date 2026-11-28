// Command ghbranch deletes remote branches, the one repository write that had
// no fleet program: `gh api -X DELETE .../git/refs/heads/<b>` and
// `git push --delete` are refused by guard-bash and name this tool.
//
//	ghbranch <owner/repo> delete <branch>... [--dry-run] [--force-unlanded]
//
// Every branch is checked BEFORE any is deleted (all or nothing), and a branch
// is refused when it is
//
//   - the repository's default branch,
//   - protected,
//   - the head of an open pull request, or
//   - unlanded: its head is not contained in a merged or closed pull request
//     from that branch, and is not already reachable from the default branch.
//     --force-unlanded waives only this last check.
//
// After each delete the branch is read back and must answer 404.
//
// The token is read from a 0600 file and sent only in the Authorization header,
// over https, with redirects refused.
//
// References (read 2026-11-28):
//
//	https://docs.github.com/en/rest/branches/branches#get-a-branch    GET /repos/{o}/{r}/branches/{b}: 200 {protected, commit.sha}, 404
//	https://docs.github.com/en/rest/pulls/pulls#list-pull-requests    GET /repos/{o}/{r}/pulls?state=all&head={o}:{b}: number, state, merged_at, head.sha
//	https://docs.github.com/en/rest/commits/commits#compare-two-commits GET /repos/{o}/{r}/compare/{base}...{head}: status ahead|behind|identical|diverged
//	https://docs.github.com/en/rest/git/refs#delete-a-reference       DELETE /repos/{o}/{r}/git/refs/heads/{b}: 204, 422
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	maxBranches = 50
)

const longHelp = `Delete remote branches, after checking each one.

  ghbranch <owner/repo> delete <branch>... [--dry-run] [--force-unlanded]

A branch is refused when it is the default branch, is protected, is the head of
an open pull request, or is unlanded: its head is neither contained in a merged
or closed pull request from that branch nor reachable from the default branch.
--force-unlanded waives only the last check; the other three have no waiver.
Every branch is checked before any is deleted, so one refusal deletes nothing.
Each delete is read back and the branch must then answer 404.
--dry-run sends only reads. The token is read from a file (default
~/.github-token, mode 0600), sent only in the Authorization header.

Exit status: 0 done, 2 refused or failed.`

// newClient is replaced by tests. Redirects are never followed: they would
// carry the token header wherever they point.
var newClient = func() *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// readToken is ghtoken.Read: the token from a file only its owner may read.
var readToken = ghtoken.Read

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

type opts struct {
	base, tokFile     string
	dryRun, forceLand bool
}

// RootCmd builds the command; tests drive it with SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	var o opts
	cmd := &cobra.Command{
		Use:          "ghbranch <owner/repo> delete <branch>...",
		Short:        "Delete remote branches that are not default, protected, open or unlanded",
		Long:         longHelp,
		Args:         cobra.MinimumNArgs(3),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, args, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.base, "base", apiBase, "API base URL (https)")
	f.StringVar(&o.tokFile, "token-file", "", "file holding the token (default ~/.github-token)")
	f.BoolVar(&o.dryRun, "dry-run", false, "send only reads")
	f.BoolVar(&o.forceLand, "force-unlanded", false, "delete a branch whose head no merged or closed pull request holds")
	return cmd
}

func run(cmd *cobra.Command, args []string, o opts) error {
	out := cmd.OutOrStdout()
	owner, repo, err := splitRepo(args[0])
	if err != nil {
		return err
	}
	if args[1] != "delete" {
		return fmt.Errorf("unknown verb %q: only delete", args[1])
	}
	branches := args[2:]
	if len(branches) > maxBranches {
		return fmt.Errorf("%d branches: at most %d per run", len(branches), maxBranches)
	}
	seen := map[string]bool{}
	for _, b := range branches {
		if err := branchOK(b); err != nil {
			return err
		}
		if seen[b] {
			return fmt.Errorf("branch %q given twice", b)
		}
		seen[b] = true
	}
	if !strings.HasPrefix(o.base, "https://") && !isTestBase(o.base) {
		return fmt.Errorf("base %q: must be https", o.base)
	}
	tok, err := readToken(o.tokFile)
	if err != nil {
		return err
	}
	c := newClient()
	repoURL := fmt.Sprintf("%s/repos/%s/%s", strings.TrimRight(o.base, "/"), owner, repo)

	var info struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	}
	if err := getJSON(c, repoURL, tok, &info); err != nil {
		return err
	}
	if info.FullName == "" || info.DefaultBranch == "" {
		return errors.New("repository: the reply carried no name or default branch")
	}
	fmt.Fprintf(out, "%s  default_branch=%s\n", info.FullName, info.DefaultBranch)

	// Check every branch first: one refusal deletes nothing.
	var refused []string
	for _, b := range branches {
		why, err := check(c, repoURL, tok, owner, info.DefaultBranch, b, o.forceLand)
		if err != nil {
			return err
		}
		if why != "" {
			refused = append(refused, fmt.Sprintf("%s: %s", b, why))
			fmt.Fprintf(out, "refuse  %s: %s\n", b, why)
			continue
		}
		fmt.Fprintf(out, "ok      %s\n", b)
	}
	if len(refused) > 0 {
		return fmt.Errorf("%d of %d branches refused, none deleted: %s", len(refused), len(branches), strings.Join(refused, "; "))
	}
	if o.dryRun {
		fmt.Fprintf(out, "dry run: would delete %d branch(es)\n", len(branches))
		return nil
	}
	for _, b := range branches {
		if _, _, err := do(c, http.MethodDelete, refURL(repoURL, b), tok, http.StatusNoContent); err != nil {
			return err
		}
		code, _, err := do(c, http.MethodGet, branchURL(repoURL, b), tok, http.StatusNotFound, http.StatusOK)
		if err != nil {
			return fmt.Errorf("read back %s: %w", b, err)
		}
		if code != http.StatusNotFound {
			return fmt.Errorf("deleted %s but it still answers %d", b, code)
		}
		fmt.Fprintf(out, "deleted %s\n", b)
	}
	return nil
}

type branchInfo struct {
	Protected bool `json:"protected"`
	Commit    struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

type pull struct {
	Number   int    `json:"number"`
	State    string `json:"state"`
	MergedAt string `json:"merged_at"`
	Head     struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
}

// check returns the reason a branch must not be deleted, or "".
func check(c *http.Client, repoURL, tok, owner, def, b string, force bool) (string, error) {
	if b == def {
		return "the default branch", nil
	}
	var bi branchInfo
	code, body, err := do(c, http.MethodGet, branchURL(repoURL, b), tok, http.StatusOK, http.StatusNotFound)
	if err != nil {
		return "", err
	}
	if code == http.StatusNotFound {
		return "no such branch", nil
	}
	if err := json.Unmarshal(body, &bi); err != nil {
		return "", fmt.Errorf("branch %s: %w", b, err)
	}
	if bi.Commit.SHA == "" {
		return "", fmt.Errorf("branch %s: the reply carried no head commit", b)
	}
	if bi.Protected {
		return "protected", nil
	}
	var prs []pull
	q := url.Values{"state": {"all"}, "head": {owner + ":" + b}, "per_page": {"100"}}
	if err := getJSON(c, repoURL+"/pulls?"+q.Encode(), tok, &prs); err != nil {
		return "", err
	}
	var landed bool
	for _, p := range prs {
		if p.Head.Ref != b {
			continue
		}
		if p.State == "open" {
			return fmt.Sprintf("the head of open pull request #%d", p.Number), nil
		}
		in, err := contained(c, repoURL, tok, bi.Commit.SHA, p.Head.SHA)
		if err != nil {
			return "", err
		}
		if in {
			landed = true
		}
	}
	if landed || force {
		return "", nil
	}
	in, err := contained(c, repoURL, tok, bi.Commit.SHA, def)
	if err != nil {
		return "", err
	}
	if in {
		return "", nil
	}
	return "unlanded: its head is in no merged or closed pull request and not in " + def + " (--force-unlanded waives this)", nil
}

// contained reports whether commit sha is reachable from tip (sha equals tip
// or is one of its ancestors): compare tip...sha says "behind" or "identical".
func contained(c *http.Client, repoURL, tok, sha, tip string) (bool, error) {
	if sha == tip {
		return true, nil
	}
	var cmp struct {
		Status string `json:"status"`
	}
	if err := getJSON(c, repoURL+"/compare/"+url.PathEscape(tip)+"..."+url.PathEscape(sha), tok, &cmp); err != nil {
		return false, err
	}
	return cmp.Status == "behind" || cmp.Status == "identical", nil
}

// branchOK refuses names git itself refuses and anything that could rewrite
// the request path or a flag.
func branchOK(b string) error {
	bad := b == "" || strings.HasPrefix(b, "-") || strings.HasPrefix(b, "/") || strings.HasSuffix(b, "/") ||
		strings.HasSuffix(b, ".") || strings.HasSuffix(b, ".lock") || strings.Contains(b, "..") ||
		strings.Contains(b, "//") || strings.Contains(b, "@{") || b == "@" || strings.ContainsAny(b, " ~^:?*[\\\x7f")
	for _, r := range b {
		if r < 0x20 {
			bad = true
		}
	}
	if bad {
		return fmt.Errorf("branch %q: not a branch name this tool will send", b)
	}
	return nil
}

func escapeRef(b string) string {
	parts := strings.Split(b, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func refURL(repoURL, b string) string    { return repoURL + "/git/refs/heads/" + escapeRef(b) }
func branchURL(repoURL, b string) string { return repoURL + "/branches/" + escapeRef(b) }

func splitRepo(s string) (owner, repo string, err error) {
	owner, repo, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", fmt.Errorf("repository %q: want owner/repo", s)
	}
	return owner, repo, nil
}

func getJSON(c *http.Client, u, tok string, into any) error {
	_, b, err := do(c, http.MethodGet, u, tok, http.StatusOK)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, into); err != nil {
		return fmt.Errorf("%s: %w", u, err)
	}
	return nil
}

// do sends one request and insists on one of the statuses the caller expects,
// returning the status that came. A failure quotes the API; a redirect names
// where it points, since it is never followed.
func do(c *http.Client, method, u, tok string, want ...int) (int, []byte, error) {
	req, err := http.NewRequest(method, u, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	for _, w := range want {
		if resp.StatusCode == w {
			return resp.StatusCode, b, nil
		}
	}
	msg := firstLine(string(b))
	if loc := resp.Header.Get("Location"); loc != "" && resp.StatusCode >= 300 && resp.StatusCode < 400 {
		msg += " (moved, not followed: " + loc + ")"
	}
	return resp.StatusCode, nil, fmt.Errorf("%s %s: %s: %s", method, u, resp.Status, msg)
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
