package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-gitsafe/gitsafe/issuegate"
	"github.com/spf13/cobra"
)

// ⛔ WHY THIS EXISTS. `gh pr create` was the one pull-request write with no fleet
// program: the guard could not name a replacement for it, and the auto-mode
// classifier refused it three times on go-filesystems/btrfs on 2026-11-16. The
// user's instruction was to make the tool the fleet lacks rather than work around
// the refusal.
//
// References (read 2026-11-16):
//
//	https://docs.github.com/en/rest/pulls/pulls     POST /repos/{o}/{r}/pulls: "title", "head", "base", "body", "draft"; 201 with "number", "html_url"
//	                                                GET  .../pulls?state=open&head=owner:branch: the open pull requests of one head
//	https://docs.github.com/en/rest/commits/commits GET  .../compare/{base}...{head}: "ahead_by", "status"
//	https://docs.github.com/en/rest/repos/repos     GET  /repos/{o}/{r}: "default_branch", "html_url", "full_name"

type repoInfo struct {
	FullName      string `json:"full_name"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
	Archived      bool   `json:"archived"`
}

// runCreate opens one pull request. Everything that can be refused locally is
// refused before the token is read, and the repository and the head are shown
// before anything is written: they decide who is notified and what is proposed.
func runCreate(cmd *cobra.Command, owner, repo string, o opts) error {
	out := cmd.OutOrStdout()
	if o.approve || o.comment || o.reqChanges {
		return errors.New("--approve, --comment and --request-changes apply to review")
	}
	if o.head == "" {
		return errors.New("create needs --head: the branch to merge")
	}
	if strings.ContainsAny(o.head, " \t\r\n") || strings.HasPrefix(o.head, ":") || strings.HasSuffix(o.head, ":") {
		return fmt.Errorf("--head %q: want branch or owner:branch", o.head)
	}
	if strings.ContainsAny(o.baseBranch, " \t\r\n") {
		return fmt.Errorf("--base-branch %q: not a branch name", o.baseBranch)
	}
	if !o.titleSet {
		return errors.New("create needs --title")
	}
	title, err := checkTitle(o.title, o.privateWaiver)
	if err != nil {
		return err
	}
	if o.bodyFile == "" {
		return errors.New("create needs --body-file: a pull request says what and why")
	}
	body, err := readBody(o.bodyFile, o.parityWaiver, o.privateWaiver)
	if err != nil {
		return err
	}
	if err := checkDispositions(body, owner+"/"+repo); err != nil {
		return err
	}
	if !strings.HasPrefix(o.base, "https://") && !isTestBase(o.base) {
		return fmt.Errorf("base %q: must be https", o.base)
	}

	tok, err := readToken(o.tokFile)
	if err != nil {
		return err
	}
	c := newClient()
	r, err := getRepo(c, o.base, tok, owner, repo)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s  %s\n", r.FullName, r.HTMLURL)
	if r.Archived {
		return fmt.Errorf("%s is archived", r.FullName)
	}
	baseBranch := o.baseBranch
	if baseBranch == "" {
		if r.DefaultBranch == "" {
			return errors.New("the read carried no default branch; give --base-branch")
		}
		baseBranch = r.DefaultBranch
	}
	fmt.Fprintf(out, "head: %s -> %s\n", o.head, baseBranch)

	ahead, err := commitsAhead(c, o.base, tok, owner, repo, baseBranch, o.head)
	if err != nil {
		return err
	}
	if ahead == 0 {
		return fmt.Errorf("%s has no commits ahead of %s: there is nothing to propose", o.head, baseBranch)
	}
	fmt.Fprintf(out, "%d commit(s) ahead of %s\n", ahead, baseBranch)

	open, err := openPullsOfHead(c, o.base, tok, owner, repo, o.head)
	if err != nil {
		return err
	}
	if len(open) > 0 {
		return fmt.Errorf("%s already has an open pull request: %s#%d %s", o.head, owner+"/"+repo, open[0].Number, open[0].HTMLURL)
	}

	if o.dryRun {
		fmt.Fprintf(out, "\n-- new pull request%s, NOT sent --\ntitle: %s\n\n%s\n",
			map[bool]string{true: " (draft)", false: ""}[o.draft], title, body)
		return nil
	}
	payload, _ := json.Marshal(map[string]any{ // strings and a bool: cannot fail
		"title": title, "head": o.head, "base": baseBranch, "body": body, "draft": o.draft,
	})
	b, err := do(c, http.MethodPost, reposURL(o.base, owner, repo)+"/pulls", tok, payload, http.StatusCreated)
	if err != nil {
		return err
	}
	var got pull
	if err := json.Unmarshal(b, &got); err != nil || got.Number == 0 || got.HTMLURL == "" {
		return errors.New("create: the reply did not say which pull request it made")
	}
	fmt.Fprintf(out, "created: %s/%s#%d %s\n", owner, repo, got.Number, got.HTMLURL)
	if o.draft && !got.Draft {
		return errors.New("create: accepted but the pull request is not a draft")
	}
	return nil
}

// checkDispositions refuses a body with an issue reference that says nothing
// about what happens to it. It is the merge gate's own parser
// (go-gitsafe/gitsafe issuegate, which ghmerge and prbody call), so a body that
// passes here is not refused at merge time for this reason, and a bare #N means
// THIS repository.
func checkDispositions(body, repo string) error {
	var bare []string
	for _, r := range issuegate.Parse(body, repo) {
		if r.Kind == issuegate.Bare {
			bare = append(bare, r.String())
		}
	}
	if len(bare) == 0 {
		return nil
	}
	return fmt.Errorf("the body mentions %s without saying what happens to it, and ghmerge will refuse the merge: write `Closes <ref>` if this pull request finishes it, or `Refs <ref>` if it stays open on purpose (`prbody <file> --repo %s` shows the same verdict)",
		strings.Join(bare, ", "), repo)
}

func reposURL(base, owner, repo string) string {
	return fmt.Sprintf("%s/repos/%s/%s", strings.TrimRight(base, "/"), owner, repo)
}

func getRepo(c *http.Client, base, tok, owner, repo string) (repoInfo, error) {
	var r repoInfo
	b, err := do(c, http.MethodGet, reposURL(base, owner, repo), tok, nil, http.StatusOK)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("repository: %w", err)
	}
	if r.FullName == "" {
		return r, errors.New("repository: the reply carried no full_name")
	}
	return r, nil
}

// commitsAhead is how many commits head has that base lacks. The comparison is
// a read, so --dry-run does it too.
func commitsAhead(c *http.Client, base, tok, owner, repo, baseBranch, head string) (int, error) {
	u := reposURL(base, owner, repo) + "/compare/" + escapeRef(baseBranch) + "..." + escapeRef(head)
	b, err := do(c, http.MethodGet, u, tok, nil, http.StatusOK)
	if err != nil {
		return 0, err
	}
	var got struct {
		AheadBy *int `json:"ahead_by"`
	}
	if err := json.Unmarshal(b, &got); err != nil || got.AheadBy == nil {
		return 0, errors.New("compare: the reply did not say how many commits are ahead")
	}
	return *got.AheadBy, nil
}

func openPullsOfHead(c *http.Client, base, tok, owner, repo, head string) ([]pull, error) {
	if !strings.Contains(head, ":") {
		head = owner + ":" + head
	}
	u := reposURL(base, owner, repo) + "/pulls?state=open&head=" + url.QueryEscape(head)
	b, err := do(c, http.MethodGet, u, tok, nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var ps []pull
	if err := json.Unmarshal(b, &ps); err != nil {
		return nil, fmt.Errorf("open pull requests: %w", err)
	}
	return ps, nil
}

// escapeRef escapes a branch name for a URL path and keeps its slashes, which
// GitHub reads as part of the name.
func escapeRef(s string) string {
	return (&url.URL{Path: s}).EscapedPath()
}
