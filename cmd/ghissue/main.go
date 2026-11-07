// Command ghissue posts one comment to a GitHub issue or pull request, and can
// close or reopen it in the same pass, after showing which issue and which
// account it is about to speak for.
//
// It exists for the same reason bzcomment does, one host over: so a session can
// write to a tracker without the token ever passing through a URL, an argument
// vector, the environment or a log. The token is read from a 0600 file and sent
// only in the Authorization header.
//
// It also exists because the body of a report is PROSE, and prose does not
// belong on a command line. A shell that builds `--body "$(cat file)"` hands the
// whole text to the argument vector, where every quote, backtick and dollar in
// it is the shell's business before it is GitHub's; `--body-file` reads the
// bytes and sends them.
//
// References (read 2026-11-07):
//
//	https://docs.github.com/en/rest/issues/comments  POST /repos/{owner}/{repo}/issues/{issue_number}/comments, body field "body"
//	https://docs.github.com/en/rest/issues/issues    PATCH /repos/{owner}/{repo}/issues/{issue_number}, "state" (open|closed) and "state_reason" (completed|not_planned|duplicate|reopened|null, ignored unless state changes)
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
	maxBody     = 64 << 10 // bytes read from the body file
	maxResponse = 1 << 20  // bytes read from any response
	maxTokFile  = 4 << 10
	timeout     = 30 * time.Second
	apiBase     = "https://api.github.com"
)

const longHelp = `Posts a comment to a GitHub issue or pull request, and can close or reopen it.

The token is read only from a file (default ~/.github-token, which must be a
regular file readable by its owner alone) and is sent only in the Authorization
header — never in a URL, an argument or the environment.

The comment body comes from a FILE. A report is prose, and prose on a command
line is the shell's business before it is GitHub's.

--close marks the issue closed; --reason sets why (completed, not_planned or
duplicate; GitHub ignores it unless the state changes). --reopen is its inverse.
Either may be given without a body file, which changes the state and says
nothing. --dry-run prints the issue and the exact comment and sends nothing.

Exit status: 0 done, 2 refused or failed.`

// newClient is replaced by tests, which need to trust an httptest TLS server.
// Redirects are never followed: a redirect would carry the token header to
// wherever it points.
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

// RootCmd builds the command; tests drive it with SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	var (
		base     string
		tokFile  string
		bodyFile string
		reason   string
		dryRun   bool
		doClose  bool
		doReopen bool
	)
	cmd := &cobra.Command{
		Use:          "ghissue <owner/repo> <number>",
		Short:        "Post a comment to a GitHub issue, and optionally close or reopen it",
		Long:         longHelp,
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, args, opts{
				base: base, tokFile: tokFile, bodyFile: bodyFile, reason: reason,
				dryRun: dryRun, doClose: doClose, doReopen: doReopen,
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&base, "base", apiBase, "API base URL (https)")
	f.StringVar(&tokFile, "token-file", "", "file holding the token (default ~/.github-token)")
	f.StringVar(&bodyFile, "body-file", "", "file holding the comment body")
	f.StringVar(&reason, "reason", "", "why it is closed: completed, not_planned or duplicate")
	f.BoolVar(&dryRun, "dry-run", false, "print what would be sent and send nothing")
	f.BoolVar(&doClose, "close", false, "close the issue after commenting")
	f.BoolVar(&doReopen, "reopen", false, "reopen the issue after commenting")
	return cmd
}

type opts struct {
	base, tokFile, bodyFile, reason string
	dryRun, doClose, doReopen       bool
}

var closeReasons = map[string]bool{"completed": true, "not_planned": true, "duplicate": true}

func run(cmd *cobra.Command, args []string, o opts) error {
	out := cmd.OutOrStdout()
	owner, repo, err := splitRepo(args[0])
	if err != nil {
		return err
	}
	num, err := strconv.Atoi(args[1])
	if err != nil || num <= 0 {
		return fmt.Errorf("issue number: %q is not a positive number", args[1])
	}
	if o.doClose && o.doReopen {
		return errors.New("--close and --reopen are opposites; give one")
	}
	if o.reason != "" {
		if !o.doClose {
			// Silently ignoring it is how a caller comes to believe a reason was
			// recorded. GitHub ignores state_reason unless the state changes, so
			// the refusal says what the API would have done quietly.
			return errors.New("--reason applies to --close; without it GitHub ignores the reason")
		}
		if !closeReasons[o.reason] {
			return fmt.Errorf("--reason %q: want completed, not_planned or duplicate", o.reason)
		}
	}
	if !strings.HasPrefix(o.base, "https://") && !isTestBase(o.base) {
		return fmt.Errorf("base %q: must be https", o.base)
	}

	var body string
	if o.bodyFile != "" {
		if body, err = readBody(o.bodyFile); err != nil {
			return err
		}
	}
	if body == "" && !o.doClose && !o.doReopen {
		return errors.New("nothing to do: give --body-file, --close or --reopen")
	}

	tok, err := readToken(o.tokFile)
	if err != nil {
		return err
	}
	c := newClient()

	// The issue is shown BEFORE anything is sent, because the number is the only
	// thing the caller typed and a wrong one is indistinguishable from a right
	// one until its title is on the screen.
	iss, err := getIssue(c, o.base, tok, owner, repo, num)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s/%s#%d [%s] %s\n", owner, repo, iss.Number, iss.State, iss.Title)
	fmt.Fprintf(out, "  %s\n", iss.HTMLURL)

	if o.dryRun {
		if body != "" {
			fmt.Fprintf(out, "\n-- comment (%d bytes), NOT sent --\n%s\n", len(body), body)
		}
		switch {
		case o.doClose:
			fmt.Fprintf(out, "would close it (reason %q)\n", o.reason)
		case o.doReopen:
			fmt.Fprintln(out, "would reopen it")
		}
		return nil
	}

	if body != "" {
		url, err := postComment(c, o.base, tok, owner, repo, num, body)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "commented: %s\n", url)
	}
	if o.doClose || o.doReopen {
		state := "closed"
		if o.doReopen {
			state = "open"
		}
		now, err := setState(c, o.base, tok, owner, repo, num, state, o.reason)
		if err != nil {
			return err
		}
		// What the API SAYS the state is now, not what we asked for: a PATCH that
		// is accepted and ignored looks exactly like one that worked.
		fmt.Fprintf(out, "state: %s\n", now)
	}
	return nil
}

func splitRepo(s string) (owner, repo string, err error) {
	owner, repo, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", fmt.Errorf("repository %q: want owner/repo", s)
	}
	return owner, repo, nil
}

// readBody reads the comment, refusing an empty one: a comment with no text is
// a notification to everyone watching that says nothing.
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

// readToken reads the token from a file that only its owner may read. The mode
// check is the point: a token in a world-readable file is already spent.
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

type issue struct {
	Number  int    `json:"number"`
	State   string `json:"state"`
	Title   string `json:"title"`
	HTMLURL string `json:"html_url"`
}

func getIssue(c *http.Client, base, tok, owner, repo string, num int) (issue, error) {
	var iss issue
	b, err := do(c, http.MethodGet, issueURL(base, owner, repo, num), tok, nil, http.StatusOK)
	if err != nil {
		return iss, err
	}
	if err := json.Unmarshal(b, &iss); err != nil {
		return iss, fmt.Errorf("issue: %w", err)
	}
	if iss.Number == 0 {
		return iss, errors.New("issue: the reply carried no number")
	}
	return iss, nil
}

func postComment(c *http.Client, base, tok, owner, repo string, num int, body string) (string, error) {
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return "", err
	}
	b, err := do(c, http.MethodPost, issueURL(base, owner, repo, num)+"/comments", tok, payload, http.StatusCreated)
	if err != nil {
		return "", err
	}
	var got struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		return "", fmt.Errorf("comment: %w", err)
	}
	if got.HTMLURL == "" {
		return "", errors.New("comment: accepted with no URL, so nothing proves it landed")
	}
	return got.HTMLURL, nil
}

func setState(c *http.Client, base, tok, owner, repo string, num int, state, reason string) (string, error) {
	m := map[string]string{"state": state}
	if reason != "" {
		m["state_reason"] = reason
	}
	payload, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	b, err := do(c, http.MethodPatch, issueURL(base, owner, repo, num), tok, payload, http.StatusOK)
	if err != nil {
		return "", err
	}
	var got issue
	if err := json.Unmarshal(b, &got); err != nil {
		return "", fmt.Errorf("state: %w", err)
	}
	if got.State == "" {
		return "", errors.New("state: the reply did not say what the state is now")
	}
	return got.State, nil
}

func issueURL(base, owner, repo string, num int) string {
	return fmt.Sprintf("%s/repos/%s/%s/issues/%d", strings.TrimRight(base, "/"), owner, repo, num)
}

// do sends one request and insists on the status the caller expects. The body
// of a failure is included: GitHub says why in it, and a bare status code sends
// the reader to the wrong cause.
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

// isTestBase allows an httptest server's http:// base, and nothing else.
func isTestBase(base string) bool {
	return strings.HasPrefix(base, "http://127.0.0.1:") || strings.HasPrefix(base, "http://[::1]:")
}
