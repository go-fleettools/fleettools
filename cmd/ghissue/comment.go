package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"
)

// commentURL is the comments endpoint, which is keyed by the COMMENT's id and
// not by the issue's: a comment id is unique within the repository, so the
// issue number is not part of the address and asking for one would invite a
// number that does not match the id.
func commentURL(base, owner, repo string, id int64) string {
	return fmt.Sprintf("%s/repos/%s/%s/issues/comments/%d", strings.TrimRight(base, "/"), owner, repo, id)
}

type ghComment struct {
	ID      int64  `json:"id"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	User    struct {
		Login string `json:"login"`
	} `json:"user"`
}

// runEditComment replaces the body of one existing comment.
//
// ⛔ IT SHOWS WHAT IS THERE FIRST, AND SAYS WHAT AN EDIT DOES NOT DO. GitHub
// keeps every previous version under "edited", so correcting a comment is a
// correction and never a retraction: text that should not have been published
// stays readable to anyone who opens that menu. The command prints that line
// every time rather than leaving the caller to remember it.
func runEditComment(cmd *cobra.Command, args []string, o opts) error {
	out := cmd.OutOrStdout()
	owner, repo, err := splitRepo(args[0])
	if err != nil {
		return err
	}
	if len(args) != 1 {
		return errors.New("--comment addresses a comment by its own id: give <owner/repo> and --comment <id>, with no issue number")
	}
	if o.commentID <= 0 {
		return fmt.Errorf("--comment: %d is not a comment id", o.commentID)
	}
	if !o.edit {
		return errors.New("--comment edits an EXISTING comment and needs --edit; a new comment is posted with <owner/repo> <number> --body-file")
	}
	if o.doClose || o.doReopen || o.reason != "" {
		return errors.New("--comment changes a comment's text; --close, --reopen and --reason act on the issue")
	}
	if o.title != "" {
		return errors.New("--title applies to an issue; a comment has no title")
	}
	if len(o.addLabels)+len(o.removeLabels) > 0 {
		return errors.New("--add-label and --remove-label apply to an issue; a comment carries none")
	}
	if o.bodyFile == "" {
		return errors.New("--comment --edit wants --body-file: the new text REPLACES the old one")
	}
	body, err := readBody(o.bodyFile, o.waivers())
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

	before, err := getComment(c, o.base, tok, owner, repo, o.commentID)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s/%s comment %d by %s\n", owner, repo, before.ID, before.User.Login)
	fmt.Fprintf(out, "  %s\n  now: %s\n", before.HTMLURL, firstLine(before.Body))
	fmt.Fprintln(out, "⚠ an edit does not retract: GitHub keeps the previous version under \"edited\"")

	if o.dryRun {
		fmt.Fprintf(out, "\n-- would REPLACE the body (%d bytes), NOT sent --\n%s\n", len(body), body)
		return nil
	}

	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return err
	}
	b, err := do(c, http.MethodPatch, commentURL(o.base, owner, repo, o.commentID), tok, payload, http.StatusOK)
	if err != nil {
		return err
	}
	var now ghComment
	if err := json.Unmarshal(b, &now); err != nil {
		return fmt.Errorf("comment: %w", err)
	}
	// ⛔ The reply is read rather than the status: a 200 says the request was
	// accepted, and what the comment HOLDS is the only thing that says it landed.
	if now.ID == 0 {
		return errors.New("comment: accepted with no comment in the reply, so nothing proves it landed")
	}
	if now.Body != body {
		return fmt.Errorf("comment %d: accepted, but the API reports a body of %d bytes where %d were sent",
			now.ID, len(now.Body), len(body))
	}
	fmt.Fprintf(out, "body: replaced (%d bytes sent)\n", len(body))
	return nil
}

func getComment(c *http.Client, base, tok, owner, repo string, id int64) (ghComment, error) {
	var cm ghComment
	b, err := do(c, http.MethodGet, commentURL(base, owner, repo, id), tok, nil, http.StatusOK)
	if err != nil {
		return cm, err
	}
	if err := json.Unmarshal(b, &cm); err != nil {
		return cm, fmt.Errorf("comment: %w", err)
	}
	if cm.ID == 0 {
		return cm, errors.New("comment: the reply carried no id")
	}
	return cm, nil
}
