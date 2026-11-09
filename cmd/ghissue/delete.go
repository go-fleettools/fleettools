package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// ⛔⛔ WHY THIS SUBCOMMAND EXISTS. On 2026-11-09 three issues had to be deleted
// and re-created, because their bodies had published a private file system and
// an edit does not undo that — the forge keeps the original under "edited" for
// anyone who opens the history. No fleet program did the act, so guard-bash
// refused the hand-typed GraphQL mutation and named what was missing:
//
//	"no fleet program does this act yet. Do not type it by hand and do not route
//	around it: stop, tell the user which act is missing, and ask for a program to
//	be added."
//
// That is the standing directive — a missing tool is ADDED, never worked around
// with another spelling. This is the program.
//
// ⛔ REST cannot do it. There is no DELETE endpoint for an issue; the only way
// is the GraphQL `deleteIssue` mutation, which takes a node ID and requires
// admin rights on the repository. So this is the one place in this command that
// speaks GraphQL, and it reads the node ID from the REST issue first rather than
// resolving it in the mutation, so that the issue it is about to delete is
// printed — number, title, state — before anything is destroyed.

// deleteMutation is sent with the node ID as a variable rather than
// interpolated: a GraphQL document assembled by string concatenation is an
// injection waiting for an id that contains a quote.
const deleteMutation = `mutation($id: ID!) { deleteIssue(input: {issueId: $id}) { repository { nameWithOwner } } }`

// runDelete deletes one issue, permanently.
//
// ⛔ It demands --confirm <owner/repo#number>, retyped. Deleting an issue cannot
// be undone and there is no second chance to notice a slipped digit — the
// comments, the cross-references and the edit history all go with it. A boolean
// --yes would be one keystroke; retyping the reference is the only confirmation
// that cannot be given without looking at WHICH issue is being deleted.
func runDelete(cmd *cobra.Command, args []string, o opts) error {
	out := cmd.OutOrStdout()
	if len(args) != 2 {
		return errors.New("--delete wants <owner/repo> <number>")
	}
	if o.doClose || o.doReopen || o.reason != "" || o.bodyFile != "" ||
		o.title != "" || len(o.addLabels)+len(o.removeLabels) > 0 {
		return errors.New("--delete destroys the issue; there is nothing left to comment on, close, retitle or label")
	}
	owner, repo, err := splitRepo(args[0])
	if err != nil {
		return err
	}
	num, err := strconv.Atoi(args[1])
	if err != nil || num <= 0 {
		return fmt.Errorf("issue number: %q is not a positive number", args[1])
	}
	want := fmt.Sprintf("%s/%s#%d", owner, repo, num)
	if o.confirm == "" {
		return fmt.Errorf("--delete is permanent and takes no second chance: the comments, the cross-references and the edit history go with the issue. Retype the reference to confirm: --confirm %s", want)
	}
	if strings.TrimSpace(o.confirm) != want {
		return fmt.Errorf("--confirm %q does not name the issue being deleted (%s)", o.confirm, want)
	}
	if !strings.HasPrefix(o.base, "https://") && !isTestBase(o.base) {
		return fmt.Errorf("base %q: must be https", o.base)
	}
	tok, err := readToken(o.tokFile)
	if err != nil {
		return err
	}
	c := newClient()

	// Read it first, and print it: the only protection against deleting the
	// wrong number is seeing what is about to go.
	iss, err := getIssue(c, o.base, tok, owner, repo, num)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s  %s  [%s]  %s\n", want, iss.HTMLURL, iss.State, firstLine(iss.Title))
	// ⛔ A pull request is an issue to REST and is not deletable: the mutation
	// fails, and a reader would blame the token. Say which it is, here.
	if iss.PullRequest != nil {
		return fmt.Errorf("%s is a pull request; GitHub does not delete those (close it instead)", want)
	}
	if iss.NodeID == "" {
		return errors.New("issue: the reply carried no node_id, which the deletion needs")
	}
	if o.dryRun {
		fmt.Fprintf(out, "dry run: would delete %s permanently; nothing was sent\n", want)
		return nil
	}
	if err := deleteIssue(c, o.base, tok, iss.NodeID); err != nil {
		return err
	}
	fmt.Fprintf(out, "deleted %s permanently\n", want)
	// The reason this act exists at all is that an edit does not undo a
	// disclosure. Say what remains to be done, or a deletion meant to be
	// followed by a clean re-filing quietly becomes a lost report.
	fmt.Fprintln(out, "the report it carried is gone with it: re-file it from a body that passes `privscan`")
	return nil
}

// deleteIssue sends the mutation and reads GraphQL's own errors.
//
// ⛔ A GraphQL refusal arrives as HTTP 200 with an `errors` array. Reading past
// that is how a refused query becomes a successful one in a caller's eyes — the
// same trap this repository has recorded in priorart, where it turned a surface
// that was never read into a clean gate.
func deleteIssue(c *http.Client, base, tok, nodeID string) error {
	payload, err := json.Marshal(map[string]any{
		"query":     deleteMutation,
		"variables": map[string]string{"id": nodeID},
	})
	if err != nil {
		return err
	}
	url := strings.TrimRight(base, "/") + "/graphql"
	b, err := do(c, http.MethodPost, url, tok, payload, http.StatusOK)
	if err != nil {
		return err
	}
	var reply struct {
		Data struct {
			DeleteIssue *struct {
				Repository struct {
					NameWithOwner string `json:"nameWithOwner"`
				} `json:"repository"`
			} `json:"deleteIssue"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(b, &reply); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	if len(reply.Errors) > 0 {
		var msgs []string
		for _, e := range reply.Errors {
			if e.Type != "" {
				msgs = append(msgs, e.Type+": "+e.Message)
				continue
			}
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("delete refused: %s", strings.Join(msgs, "; "))
	}
	// ⛔ No errors AND no payload is not a success. The mutation returns the
	// repository it deleted from; a reply carrying neither means the document
	// did not run, and treating that as done is how a tool reports work it never
	// did.
	if reply.Data.DeleteIssue == nil {
		return errors.New("delete: the reply carried neither an error nor a result; the issue may still exist")
	}
	return nil
}
