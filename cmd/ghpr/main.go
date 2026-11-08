// Command ghpr performs the pull-request writes that ghissue and ghmerge do
// not: edit the title or body, close, reopen, mark ready for review, update the
// branch from its base, and submit a review. It shows the pull request first.
//
// It exists because sessions typed these by hand (`gh pr edit` 437 times, `gh pr
// close` 188, `gh pr update-branch` 45, `gh pr ready` 32, `gh pr review` 13,
// plus `gh api -X PATCH .../pulls/N -F body=@f`) and the fleet's guard now
// refuses those forms and names this program. A rule with a hole in its toolbox
// is followed until the hole is reached.
//
// Same handling as ghissue: the token is read from a 0600 file and sent only in
// the Authorization header, over https, redirects refused; prose comes from a
// file, never argv (the title is the one-line exception).
//
// References (read 2026-11-08):
//
//	https://docs.github.com/en/rest/pulls/pulls    PATCH /repos/{o}/{r}/pulls/{n}: "title", "body", "state" (open|closed), 200
//	                                               PUT .../pulls/{n}/update-branch: "expected_head_sha" (422 when it differs from the head), 202 with "message"
//	                                               GET .../pulls/{n}: "number", "state", "draft", "title", "html_url", "node_id", "merged", head/base "ref" and "sha"
//	https://docs.github.com/en/rest/pulls/reviews  POST .../pulls/{n}/reviews: "event" (APPROVE|REQUEST_CHANGES|COMMENT), "body" (required for the last two), "commit_id"; 200
//	https://docs.github.com/en/graphql/reference/pulls  markPullRequestReadyForReview(input: {pullRequestId}) -> pullRequest; REST has no endpoint for it
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const (
	maxBody     = 64 << 10
	maxResponse = 1 << 20
	maxTokFile  = 4 << 10
	maxTitle    = 256
	timeout     = 30 * time.Second
	apiBase     = "https://api.github.com"
)

const longHelp = `Pull-request writes, after showing the pull request.

  ghpr <owner/repo> <n> edit [--title T] [--body-file F]
  ghpr <owner/repo> <n> close | reopen | ready | update-branch
  ghpr <owner/repo> <n> review --approve|--comment|--request-changes [--body-file F]

The pull request is read and printed first (number, state, draft, title,
head -> base, URL); --dry-run sends only that read. What is printed after a
write is the state the API reports, not the one asked for. The token is read
only from a file (default ~/.github-token, owner-only) and sent only in the
Authorization header. Prose comes from a FILE; the title is one line, at most
256 characters. Commenting is ghissue, merging is ghmerge.

update-branch pins expected_head_sha to the head just read, so it cannot update
a head that moved. ready is a GraphQL mutation (REST has none), sent with the
same token. A review is pinned to the head just read; --comment and
--request-changes need a body.

Exit status: 0 done, 2 refused or failed.`

var newClient = func() *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

type opts struct {
	base, tokFile, bodyFile, title string
	dryRun                         bool
	approve, comment, reqChanges   bool
	titleSet                       bool
}

// RootCmd builds the command; tests drive it with SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	var o opts
	cmd := &cobra.Command{
		Use:          "ghpr <owner/repo> <number> edit|close|reopen|ready|update-branch|review",
		Short:        "Edit, close, reopen, ready, update or review a GitHub pull request",
		Long:         longHelp,
		Args:         cobra.ExactArgs(3),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.titleSet = cmd.Flags().Changed("title")
			return run(cmd, args, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.base, "base", apiBase, "API base URL (https)")
	f.StringVar(&o.tokFile, "token-file", "", "file holding the token (default ~/.github-token)")
	f.StringVar(&o.bodyFile, "body-file", "", "file holding the body (edit, review)")
	f.StringVar(&o.title, "title", "", "new title, one line (edit)")
	f.BoolVar(&o.dryRun, "dry-run", false, "show the pull request and what would be sent; send nothing")
	f.BoolVar(&o.approve, "approve", false, "review: approve")
	f.BoolVar(&o.comment, "comment", false, "review: comment only")
	f.BoolVar(&o.reqChanges, "request-changes", false, "review: request changes")
	return cmd
}

type pull struct {
	Number  int    `json:"number"`
	State   string `json:"state"`
	Draft   bool   `json:"draft"`
	Merged  bool   `json:"merged"`
	Title   string `json:"title"`
	HTMLURL string `json:"html_url"`
	NodeID  string `json:"node_id"`
	Head    struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func (p pull) line(owner, repo string) string {
	return fmt.Sprintf("%s/%s#%d [%s%s] %s\n  %s -> %s  %s\n", owner, repo, p.Number, p.State,
		map[bool]string{true: ", draft", false: ""}[p.Draft], p.Title, p.Head.Ref, p.Base.Ref, p.HTMLURL)
}

func run(cmd *cobra.Command, args []string, o opts) error {
	out := cmd.OutOrStdout()
	owner, repo, err := splitRepo(args[0])
	if err != nil {
		return err
	}
	num, err := strconv.Atoi(args[1])
	if err != nil || num <= 0 {
		return fmt.Errorf("pull request number: %q is not a positive number", args[1])
	}
	verb := args[2]
	switch verb {
	case "edit", "close", "reopen", "ready", "update-branch", "review":
	default:
		return fmt.Errorf("verb %q: want edit, close, reopen, ready, update-branch or review", verb)
	}
	if verb != "edit" && o.titleSet {
		return errors.New("--title applies to edit")
	}
	if verb != "edit" && verb != "review" && o.bodyFile != "" {
		return errors.New("--body-file applies to edit and review")
	}
	if verb != "review" && (o.approve || o.comment || o.reqChanges) {
		return errors.New("--approve, --comment and --request-changes apply to review")
	}
	if !strings.HasPrefix(o.base, "https://") && !isTestBase(o.base) {
		return fmt.Errorf("base %q: must be https", o.base)
	}

	var title, body, event string
	switch verb {
	case "edit":
		if !o.titleSet && o.bodyFile == "" {
			return errors.New("edit: nothing to change; give --title and/or --body-file")
		}
		if o.titleSet {
			if title, err = checkTitle(o.title); err != nil {
				return err
			}
		}
	case "review":
		n := 0
		for _, b := range []bool{o.approve, o.comment, o.reqChanges} {
			if b {
				n++
			}
		}
		if n != 1 {
			return errors.New("review: give exactly one of --approve, --comment, --request-changes")
		}
		event = map[bool]string{true: "APPROVE"}[o.approve]
		if o.comment {
			event = "COMMENT"
		}
		if o.reqChanges {
			event = "REQUEST_CHANGES"
		}
		if event != "APPROVE" && o.bodyFile == "" {
			return errors.New("review: --comment and --request-changes need --body-file")
		}
	}
	if o.bodyFile != "" {
		if body, err = readBody(o.bodyFile); err != nil {
			return err
		}
	}

	tok, err := readToken(o.tokFile)
	if err != nil {
		return err
	}
	c := newClient()
	p, err := getPull(c, o.base, tok, owner, repo, num)
	if err != nil {
		return err
	}
	fmt.Fprint(out, p.line(owner, repo))

	// Refuse what GitHub would accept and ignore, or accept and misreport.
	switch verb {
	case "close":
		if p.State != "open" {
			return fmt.Errorf("close: already %s", p.State)
		}
	case "reopen":
		if p.Merged {
			return errors.New("reopen: it is merged; a merged pull request cannot be reopened")
		}
		if p.State != "closed" {
			return errors.New("reopen: it is not closed")
		}
	case "ready":
		if p.State != "open" {
			return fmt.Errorf("ready: it is %s", p.State)
		}
		if !p.Draft {
			return errors.New("ready: it is not a draft")
		}
	case "update-branch":
		if p.State != "open" {
			return fmt.Errorf("update-branch: it is %s", p.State)
		}
		if p.Head.SHA == "" {
			return errors.New("update-branch: the read carried no head sha to pin")
		}
	case "review":
		if p.Head.SHA == "" {
			return errors.New("review: the read carried no head sha to pin")
		}
	}

	if o.dryRun {
		switch verb {
		case "edit":
			if title != "" {
				fmt.Fprintf(out, "would set title: %s\n", title)
			}
			if body != "" {
				fmt.Fprintf(out, "\n-- body (%d bytes), NOT sent --\n%s\n", len(body), body)
			}
		case "update-branch":
			fmt.Fprintf(out, "would merge %s into %s, pinned to head %s\n", p.Base.Ref, p.Head.Ref, p.Head.SHA)
		case "review":
			fmt.Fprintf(out, "would submit %s on head %s\n", event, p.Head.SHA)
			if body != "" {
				fmt.Fprintf(out, "\n-- review body (%d bytes), NOT sent --\n%s\n", len(body), body)
			}
		default:
			fmt.Fprintf(out, "would %s it\n", verb)
		}
		return nil
	}

	pu := pullURL(o.base, owner, repo, num)
	switch verb {
	case "edit", "close", "reopen":
		m := map[string]string{}
		switch verb {
		case "edit":
			if o.titleSet {
				m["title"] = title
			}
			if o.bodyFile != "" {
				m["body"] = body
			}
		case "close":
			m["state"] = "closed"
		case "reopen":
			m["state"] = "open"
		}
		payload, _ := json.Marshal(m)
		b, err := do(c, http.MethodPatch, pu, tok, payload, http.StatusOK)
		if err != nil {
			return err
		}
		var got pull
		if err := json.Unmarshal(b, &got); err != nil || got.State == "" {
			return errors.New("the reply did not say what the state is now")
		}
		fmt.Fprintf(out, "now: [%s] %s\n", stateWord(got), got.Title)
		if verb == "edit" && o.titleSet && got.Title != title {
			return fmt.Errorf("edit: accepted but the title reads %q, not %q", got.Title, title)
		}
		want := map[string]string{"close": "closed", "reopen": "open"}[verb]
		if want != "" && got.State != want {
			return fmt.Errorf("%s: accepted but the state is %q", verb, got.State)
		}
	case "update-branch":
		payload, _ := json.Marshal(map[string]string{"expected_head_sha": p.Head.SHA})
		b, err := do(c, http.MethodPut, pu+"/update-branch", tok, payload, http.StatusAccepted)
		if err != nil {
			return err
		}
		var got struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(b, &got)
		fmt.Fprintf(out, "accepted (asynchronous): %s\n", got.Message)
	case "ready":
		if p.NodeID == "" {
			return errors.New("ready: the read carried no node_id for the GraphQL mutation")
		}
		if err := markReady(c, o.base, tok, p.NodeID); err != nil {
			return err
		}
		got, err := getPull(c, o.base, tok, owner, repo, num)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "now: [%s] %s\n", stateWord(got), got.Title)
		if got.Draft {
			return errors.New("ready: accepted but the pull request is still a draft")
		}
	case "review":
		m := map[string]string{"event": event, "commit_id": p.Head.SHA}
		if body != "" {
			m["body"] = body
		}
		payload, _ := json.Marshal(m)
		b, err := do(c, http.MethodPost, pu+"/reviews", tok, payload, http.StatusOK)
		if err != nil {
			return err
		}
		var got struct {
			State   string `json:"state"`
			HTMLURL string `json:"html_url"`
		}
		if err := json.Unmarshal(b, &got); err != nil || got.State == "" {
			return errors.New("review: the reply did not say what the review state is")
		}
		fmt.Fprintf(out, "review: %s %s\n", got.State, got.HTMLURL)
	}
	return nil
}

func stateWord(p pull) string {
	if p.Draft {
		return p.State + ", draft"
	}
	return p.State
}

// markReady sends the GraphQL mutation. GraphQL answers 200 with an "errors"
// array on failure, so the status alone proves nothing.
func markReady(c *http.Client, base, tok, nodeID string) error {
	payload, err := json.Marshal(map[string]any{
		"query":     "mutation($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { pullRequest { isDraft } } }",
		"variables": map[string]string{"id": nodeID},
	})
	if err != nil {
		return err
	}
	b, err := do(c, http.MethodPost, graphqlURL(base), tok, payload, http.StatusOK)
	if err != nil {
		return err
	}
	var got struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Data struct {
			M *struct {
				PR *struct {
					IsDraft bool `json:"isDraft"`
				} `json:"pullRequest"`
			} `json:"markPullRequestReadyForReview"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		return fmt.Errorf("ready: %w", err)
	}
	if len(got.Errors) > 0 {
		return fmt.Errorf("ready: GraphQL: %s", firstLine(got.Errors[0].Message))
	}
	if got.Data.M == nil || got.Data.M.PR == nil {
		return errors.New("ready: the reply carried no pull request")
	}
	return nil
}

// graphqlURL: api.github.com serves GraphQL at /graphql; a test base is used as is.
func graphqlURL(base string) string { return strings.TrimRight(base, "/") + "/graphql" }

func checkTitle(t string) (string, error) {
	t = strings.TrimSpace(t)
	switch {
	case t == "":
		return "", errors.New("--title: empty")
	case strings.ContainsAny(t, "\r\n"):
		return "", errors.New("--title: one line; the rest belongs in --body-file")
	case len([]rune(t)) > maxTitle:
		return "", fmt.Errorf("--title: over %d characters", maxTitle)
	}
	return t, nil
}

func getPull(c *http.Client, base, tok, owner, repo string, num int) (pull, error) {
	var p pull
	b, err := do(c, http.MethodGet, pullURL(base, owner, repo, num), tok, nil, http.StatusOK)
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("pull request: %w", err)
	}
	if p.Number == 0 {
		return p, errors.New("pull request: the reply carried no number")
	}
	return p, nil
}

func pullURL(base, owner, repo string, num int) string {
	return fmt.Sprintf("%s/repos/%s/%s/pulls/%d", strings.TrimRight(base, "/"), owner, repo, num)
}

func splitRepo(s string) (owner, repo string, err error) {
	owner, repo, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", fmt.Errorf("repository %q: want owner/repo", s)
	}
	return owner, repo, nil
}

func readBody(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("body file: %w", err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxBody+1))
	if err != nil {
		return "", fmt.Errorf("body file: %w", err)
	}
	if len(b) > maxBody {
		return "", fmt.Errorf("body file: over %d bytes", maxBody)
	}
	if strings.TrimSpace(string(b)) == "" {
		return "", errors.New("body file: empty")
	}
	return string(b), nil
}

func readToken(path string) (string, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, ".github-token")
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("token file %s: not a regular file", path)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("token file %s: mode %04o, want owner-only (0600)", path, st.Mode().Perm())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	if len(b) > maxTokFile {
		return "", fmt.Errorf("token file %s: over %d bytes", path, maxTokFile)
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", fmt.Errorf("token file %s: empty", path)
	}
	return tok, nil
}

func do(c *http.Client, method, url, tok string, payload []byte, want int) ([]byte, error) {
	var rdr io.Reader
	if payload != nil {
		rdr = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, url, rdr)
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
		return nil, fmt.Errorf("%s %s: %s: %s", method, url, resp.Status, firstLine(string(b)))
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
