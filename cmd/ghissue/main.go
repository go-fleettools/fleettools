// Command ghissue posts one comment to a GitHub issue or pull request, and can
// close or reopen it in the same pass, after showing which issue and which
// account it is about to speak for. With --new it opens an issue instead.
//
// --new exists because `gh issue create` was the one tracker write the fleet
// had no program for, so on 2026-11-08 three issues were opened by hand while
// the standing rule said every GitHub write goes through a fleet program. A
// rule with a hole in its toolbox is followed until the hole is reached.
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
//	                                                 POST /repos/{owner}/{repo}/issues, "title" (required) and "body"; 201 with "number" and "html_url"
//	                                                 PATCH also takes "title" and "body" (read 2026-11-08), which --edit uses
//	https://docs.github.com/en/rest/issues/labels    POST /repos/{owner}/{repo}/issues/{n}/labels adds (200, returns the labels); DELETE .../labels/{name} removes one (200, 404 if absent, returns the remaining labels) (read 2026-11-08)
//	https://docs.github.com/en/rest/repos/repos     GET /repos/{owner}/{repo}, "full_name", "html_url", "has_issues"
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
	maxTitle    = 256 // GitHub's own limit on an issue title, in characters
	timeout     = 30 * time.Second
	apiBase     = "https://api.github.com"
)

const longHelp = `Posts a comment to a GitHub issue or pull request, and can close or reopen it.
With --new, opens an issue instead:

  ghissue <owner/repo> --new --title "<title>" --body-file <file>

It shows the repository before sending, refuses one whose issues are turned
off, and prints the new issue's number and URL as GitHub reports them.
--new is a flag rather than "leave the number out" on purpose: a forgotten
number must not turn a comment into a new issue.

The token is read only from a file (default ~/.github-token, which must be a
regular file readable by its owner alone) and is sent only in the Authorization
header — never in a URL, an argument or the environment.

The comment body comes from a FILE. A report is prose, and prose on a command
line is the shell's business before it is GitHub's.

--close marks the issue closed; --reason sets why (completed, not_planned or
duplicate; GitHub ignores it unless the state changes). --reopen is its inverse.
Either may be given without a body file, which changes the state and says
nothing. --dry-run prints the issue and the exact comment and sends nothing.

--edit changes an existing issue instead of commenting on it:

  ghissue <owner/repo> <number> --edit [--title T] [--body-file F]
          [--add-label L]... [--remove-label L]...

--body-file REPLACES the whole body (the comment path appends a comment; here
the file is the new body, so start from the current text). It shows the issue
first, with its labels, and prints the title and labels the API reports after.
Refused: --edit with --close/--reopen/--reason/--new, nothing to change, a
label both added and removed, a label to remove that the issue does not carry.
Adding a label the repository lacks creates it; adding one already present is
a no-op on GitHub's side.

--delete destroys an issue PERMANENTLY, which REST cannot do — it is GitHub's
deleteIssue mutation, and it needs admin rights on the repository:

  ghissue <owner/repo> <number> --delete --confirm <owner/repo#number>

The reference is retyped because there is no undo and no second chance to see
the number: the comments, the cross-references and the edit history go with the
issue. It shows what it is about to delete first, refuses a pull request (GitHub
does not delete those), and reads GraphQL's errors, which arrive behind an HTTP
200. It exists because an edit does NOT undo a published disclosure.

⛔ THE GATE. Every body and title this command sends is scanned first, and one
that names a person's own file system — a home directory, the account name
inside it, or a project or document name taken from the path, each of them also
on its own — is REFUSED before the token is read. Three issues were filed on
2026-11-09 carrying all four, and editing them afterwards did not undo it: the
forge keeps the original under "edited". Fix the file with privscan --fix,
look at it with privscan --show, or, if the path IS the subject, pass
--allow-private-path "<why>" — a reason, never a boolean, and it is APPENDED to
what gets published, so a waiver is made in public. No waiver covers a TITLE: a
title cannot carry the reason and is republished in every listing and
notification an edit never reaches.

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
		title    string
		dryRun   bool
		doClose  bool
		doReopen bool
		doNew    bool
		doEdit   bool
		addLab   []string
		remLab   []string
		waiver   string
		doDelete bool
		confirm  string
	)
	cmd := &cobra.Command{
		Use:          "ghissue <owner/repo> <number> | <owner/repo> --new --title <t> --body-file <f>",
		Short:        "Comment on, close or reopen a GitHub issue, or open a new one",
		Long:         longHelp,
		Args:         cobra.RangeArgs(1, 2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o := opts{
				base: base, tokFile: tokFile, bodyFile: bodyFile, reason: reason,
				title: title, dryRun: dryRun, doClose: doClose, doReopen: doReopen,
				addLabels: addLab, removeLabels: remLab,
				edit: doEdit, waiver: waiver, confirm: confirm,
			}
			// ⛔ The three acts are named separately and checked against each
			// other rather than ordered by precedence: --delete beside --edit is
			// a line whose author meant one of them, and guessing which is how a
			// destructive act happens by default.
			if n := count(doNew, doEdit, doDelete); n > 1 {
				return errors.New("--new opens an issue, --edit changes one and --delete destroys one; give one")
			}
			if doDelete {
				return runDelete(cmd, args, o)
			}
			if doNew {
				if len(addLab)+len(remLab) > 0 {
					return errors.New("--add-label and --remove-label apply to --edit")
				}
				return runNew(cmd, args, o)
			}
			if doEdit {
				if len(args) != 2 {
					return errors.New("--edit wants <owner/repo> <number>")
				}
				return runEdit(cmd, args, o)
			}
			if title != "" {
				return errors.New("--title applies to --new and --edit; a comment has no title")
			}
			if len(addLab)+len(remLab) > 0 {
				return errors.New("--add-label and --remove-label apply to --edit")
			}
			if len(args) != 2 {
				return errors.New("want <owner/repo> <number>, or --new to open an issue")
			}
			return run(cmd, args, o)
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
	f.BoolVar(&doNew, "new", false, "open a new issue (with --title and --body-file)")
	f.StringVar(&title, "title", "", "title of the new issue (--new) or the new title (--edit)")
	f.BoolVar(&doEdit, "edit", false, "edit an existing issue: --title, --body-file (REPLACES the body), --add-label, --remove-label")
	f.StringArrayVar(&addLab, "add-label", nil, "label to add (--edit; repeatable)")
	f.StringArrayVar(&remLab, "remove-label", nil, "label to remove (--edit; repeatable)")
	// ⛔ A REASON, not a boolean, and no default. A boolean waiver is one
	// keystroke and gets passed by reflex; a reason has to be written, and
	// writing it is the moment to notice there is nothing to write. It also
	// STAMPS — the reason is appended to the published text, so a waiver is a
	// statement made in public rather than a switch flicked in private.
	f.StringVar(&waiver, "allow-private-path", "",
		"why this text must name a private file system; the reason is APPENDED to what is published")
	f.BoolVar(&doDelete, "delete", false,
		"delete the issue PERMANENTLY (with --confirm <owner/repo#number>)")
	f.StringVar(&confirm, "confirm", "",
		"the reference retyped, which --delete requires: there is no undo and no second chance to see the number")
	return cmd
}

type opts struct {
	base, tokFile, bodyFile, reason, title string
	dryRun, doClose, doReopen              bool
	addLabels, removeLabels                []string
	edit                                   bool
	// waiver is the reason given for publishing a reference to a private file
	// system. Empty means the gate refuses; see privgate.go.
	waiver string
	// confirm is the reference retyped to authorise --delete; see delete.go.
	confirm string
}

// count says how many of the acts were asked for.
func count(bs ...bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

// runNew opens one issue. Everything that can be refused locally is refused
// before the token is read, and the repository is shown before the POST: the
// repository name is the only thing typed that decides who gets notified.
func runNew(cmd *cobra.Command, args []string, o opts) error {
	out := cmd.OutOrStdout()
	if len(args) != 1 {
		return errors.New("--new takes <owner/repo> only: the number is GitHub's to give")
	}
	if o.doClose || o.doReopen || o.reason != "" {
		return errors.New("--new opens an issue; --close, --reopen and --reason act on an existing one")
	}
	owner, repo, err := splitRepo(args[0])
	if err != nil {
		return err
	}
	title, err := checkTitle(o.title, o.waiver)
	if err != nil {
		return err
	}
	if o.bodyFile == "" {
		// An issue with no body is a title nobody can act on; the comment path
		// refuses an empty body for the same reason.
		return errors.New("--new needs --body-file: an issue says what and why")
	}
	body, err := readBody(o.bodyFile, o.waiver)
	if err != nil {
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
	if !r.HasIssues {
		return fmt.Errorf("%s has issues turned off", r.FullName)
	}
	if o.dryRun {
		fmt.Fprintf(out, "\n-- new issue, NOT sent --\ntitle: %s\n\n%s\n", title, body)
		return nil
	}
	iss, err := postIssue(c, o.base, tok, owner, repo, title, body)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "created: %s#%d %s\n", r.FullName, iss.Number, iss.HTMLURL)
	return nil
}

// checkTitle refuses a title GitHub would refuse, and one that spans lines: a
// newline in a title is a body pasted into the wrong flag.
func checkTitle(t, waiver string) (string, error) {
	t = strings.TrimSpace(t)
	switch {
	case t == "":
		return "", errors.New("--new needs --title")
	case strings.ContainsAny(t, "\r\n"):
		return "", errors.New("--title: one line; the rest belongs in --body-file")
	case len([]rune(t)) > maxTitle:
		return "", fmt.Errorf("--title: over %d characters", maxTitle)
	}
	if err := gateTitle(t, waiver); err != nil {
		return "", err
	}
	return t, nil
}

type repoInfo struct {
	FullName  string `json:"full_name"`
	HTMLURL   string `json:"html_url"`
	HasIssues bool   `json:"has_issues"`
}

func getRepo(c *http.Client, base, tok, owner, repo string) (repoInfo, error) {
	var r repoInfo
	url := fmt.Sprintf("%s/repos/%s/%s", strings.TrimRight(base, "/"), owner, repo)
	b, err := do(c, http.MethodGet, url, tok, nil, http.StatusOK)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("repository: %w", err)
	}
	if r.FullName == "" {
		return r, errors.New("repository: the reply carried no name")
	}
	return r, nil
}

func postIssue(c *http.Client, base, tok, owner, repo, title, body string) (issue, error) {
	var iss issue
	payload, err := json.Marshal(map[string]string{"title": title, "body": body})
	if err != nil {
		return iss, err
	}
	url := fmt.Sprintf("%s/repos/%s/%s/issues", strings.TrimRight(base, "/"), owner, repo)
	b, err := do(c, http.MethodPost, url, tok, payload, http.StatusCreated)
	if err != nil {
		return iss, err
	}
	if err := json.Unmarshal(b, &iss); err != nil {
		return iss, fmt.Errorf("new issue: %w", err)
	}
	if iss.Number == 0 || iss.HTMLURL == "" {
		return iss, errors.New("new issue: accepted with no number or URL, so nothing proves it landed")
	}
	return iss, nil
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
		if body, err = readBody(o.bodyFile, o.waiver); err != nil {
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
// a notification to everyone watching that says nothing — and refusing one that
// names a person's own file system, which is privgate.go.
//
// ⛔ The waiver is a PARAMETER and not a package variable: every path through
// this command reads its text here, so a body cannot be read without the
// compiler asking the caller what to do about a disclosure. A gate reached by
// remembering to call it is not a gate.
func readBody(path, waiver string) (string, error) {
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
	return gateBody(string(b), waiver)
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
	// NodeID is what --delete needs: REST has no endpoint that deletes an
	// issue, and the GraphQL mutation that does takes a node ID.
	NodeID      string `json:"node_id"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
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
