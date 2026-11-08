package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// runEdit changes an existing issue's title, body and labels. Everything that
// can be refused locally is refused before the token is read; the issue is
// shown (with its labels) before anything is sent, and what is printed after is
// what the API reports, not what was asked for.
func runEdit(cmd *cobra.Command, args []string, o opts) error {
	out := cmd.OutOrStdout()
	owner, repo, err := splitRepo(args[0])
	if err != nil {
		return err
	}
	num, err := strconv.Atoi(args[1])
	if err != nil || num <= 0 {
		return fmt.Errorf("issue number: %q is not a positive number", args[1])
	}
	if o.doClose || o.doReopen || o.reason != "" {
		return errors.New("--edit changes title, body and labels; --close, --reopen and --reason are a separate pass")
	}
	var title string
	if o.title != "" {
		if title, err = checkTitle(o.title); err != nil {
			return err
		}
	}
	var body string
	if o.bodyFile != "" {
		if body, err = readBody(o.bodyFile); err != nil {
			return err
		}
	}
	add, err := cleanLabels("--add-label", o.addLabels)
	if err != nil {
		return err
	}
	rem, err := cleanLabels("--remove-label", o.removeLabels)
	if err != nil {
		return err
	}
	for _, a := range add {
		for _, r := range rem {
			if strings.EqualFold(a, r) {
				return fmt.Errorf("label %q is both added and removed", a)
			}
		}
	}
	if title == "" && body == "" && len(add) == 0 && len(rem) == 0 {
		return errors.New("nothing to edit: give --title, --body-file, --add-label or --remove-label")
	}
	if !strings.HasPrefix(o.base, "https://") && !isTestBase(o.base) {
		return fmt.Errorf("base %q: must be https", o.base)
	}
	tok, err := readToken(o.tokFile)
	if err != nil {
		return err
	}
	c := newClient()

	iss, err := getIssue(c, o.base, tok, owner, repo, num)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s/%s#%d [%s] %s\n", owner, repo, iss.Number, iss.State, iss.Title)
	fmt.Fprintf(out, "  %s\n  labels: %s\n", iss.HTMLURL, labelList(iss.labelNames()))

	// A removal GitHub would answer with a 404 is refused here, with the
	// labels the issue does carry, so the cause is not guessed from a status.
	have := iss.labelNames()
	for _, r := range rem {
		if !containsFold(have, r) {
			return fmt.Errorf("--remove-label %q: the issue carries %s", r, labelList(have))
		}
	}

	if o.dryRun {
		if title != "" {
			fmt.Fprintf(out, "\n-- would set title: %s\n", title)
		}
		if body != "" {
			fmt.Fprintf(out, "\n-- would REPLACE the body (%d bytes), NOT sent --\n%s\n", len(body), body)
		}
		if len(add) > 0 {
			fmt.Fprintf(out, "would add labels: %s\n", labelList(add))
		}
		if len(rem) > 0 {
			fmt.Fprintf(out, "would remove labels: %s\n", labelList(rem))
		}
		return nil
	}

	now := iss
	if title != "" || body != "" {
		m := map[string]string{}
		if title != "" {
			m["title"] = title
		}
		if body != "" {
			m["body"] = body
		}
		payload, err := json.Marshal(m)
		if err != nil {
			return err
		}
		b, err := do(c, http.MethodPatch, issueURL(o.base, owner, repo, num), tok, payload, http.StatusOK)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &now); err != nil {
			return fmt.Errorf("edit: %w", err)
		}
		if now.Number == 0 {
			return errors.New("edit: accepted with no issue in the reply, so nothing proves it landed")
		}
		if title != "" && now.Title != title {
			return fmt.Errorf("edit: asked for title %q, the API reports %q", title, now.Title)
		}
		fmt.Fprintf(out, "title: %s\n", now.Title)
		if body != "" {
			fmt.Fprintf(out, "body: replaced (%d bytes sent)\n", len(body))
		}
	}
	labels := now.labelNames()
	lbase := issueURL(o.base, owner, repo, num) + "/labels"
	if len(add) > 0 {
		payload, err := json.Marshal(map[string][]string{"labels": add})
		if err != nil {
			return err
		}
		b, err := do(c, http.MethodPost, lbase, tok, payload, http.StatusOK)
		if err != nil {
			return err
		}
		if labels, err = parseLabels(b); err != nil {
			return err
		}
		for _, a := range add {
			if !containsFold(labels, a) {
				return fmt.Errorf("--add-label %q: accepted, but the API's label list does not hold it: %s", a, labelList(labels))
			}
		}
	}
	for _, r := range rem {
		b, err := do(c, http.MethodDelete, lbase+"/"+url.PathEscape(r), tok, nil, http.StatusOK)
		if err != nil {
			return err
		}
		if labels, err = parseLabels(b); err != nil {
			return err
		}
		if containsFold(labels, r) {
			return fmt.Errorf("--remove-label %q: accepted, but the API's label list still holds it: %s", r, labelList(labels))
		}
	}
	if len(add)+len(rem) > 0 {
		fmt.Fprintf(out, "labels: %s\n", labelList(labels))
	}
	return nil
}

func (i issue) labelNames() []string {
	var n []string
	for _, l := range i.Labels {
		n = append(n, l.Name)
	}
	return n
}

func parseLabels(b []byte) ([]string, error) {
	var ls []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(b, &ls); err != nil {
		return nil, fmt.Errorf("labels: %w", err)
	}
	n := []string{}
	for _, l := range ls {
		n = append(n, l.Name)
	}
	return n, nil
}

func labelList(n []string) string {
	if len(n) == 0 {
		return "(none)"
	}
	return strings.Join(n, ", ")
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// cleanLabels refuses an empty or multi-line label and drops a repeat.
func cleanLabels(flag string, in []string) ([]string, error) {
	var out []string
	for _, l := range in {
		l = strings.TrimSpace(l)
		if l == "" || strings.ContainsAny(l, "\r\n") || len([]rune(l)) > 50 {
			return nil, fmt.Errorf("%s %q: want one non-empty line of at most 50 characters", flag, l)
		}
		if !containsFold(out, l) {
			out = append(out, l)
		}
	}
	return out, nil
}
