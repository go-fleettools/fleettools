package main

// The issues and actions verbs: a fork starts with issues off and its Actions
// workflows disabled; neither can be switched on by any other fleet program.
//
//	https://docs.github.com/en/rest/repos/repos#update-a-repository
//	    PATCH /repos/{o}/{r}, body has_issues; 200 with the repository, has_issues read back
//	https://docs.github.com/en/rest/actions/permissions
//	    GET|PUT /repos/{o}/{r}/actions/permissions, body {"enabled": bool}; PUT 204, GET 200 {"enabled": bool}
//	https://docs.github.com/en/rest/actions/workflows
//	    GET /repos/{o}/{r}/actions/workflows (total_count, workflows[].id|name|state);
//	    PUT /repos/{o}/{r}/actions/workflows/{id}/enable; 204; a fork's workflows are "disabled_fork"

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

const maxWorkflowPages = 20

// doIssues enables, disables or reads the issues tab; the state is read back
// from the repository the PATCH returns.
func doIssues(out io.Writer, c *http.Client, repoURL, tok string, r repoInfo, o opts, action string) error {
	if r.HasIssues == nil {
		return errors.New("issues: the repository reply carried no \"has_issues\", so nothing proves the state")
	}
	before := *r.HasIssues
	fmt.Fprintf(out, "issues: has_issues=%t\n", before)
	if action == "status" {
		return nil
	}
	want := action == "enable"
	if before == want {
		fmt.Fprintln(out, "already so; nothing to do")
		return nil
	}
	if o.dryRun {
		fmt.Fprintf(out, "would %s issues\n", action)
		return nil
	}
	got, err := patchRepo(c, repoURL, tok, map[string]any{"has_issues": want})
	if err != nil {
		return err
	}
	if got.HasIssues == nil || *got.HasIssues != want {
		return fmt.Errorf("issues: asked for has_issues=%t, the repository says otherwise", want)
	}
	fmt.Fprintf(out, "issues: has_issues=%t on %s\n", want, r.FullName)
	return nil
}

type workflow struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

func actionsEnabled(c *http.Client, repoURL, tok string) (bool, error) {
	_, b, err := do(c, http.MethodGet, repoURL+"/actions/permissions", tok, nil, http.StatusOK)
	if err != nil {
		return false, err
	}
	var p struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.Unmarshal(b, &p); err != nil || p.Enabled == nil {
		return false, errors.New("actions: the reply carried no \"enabled\", so nothing proves the state")
	}
	return *p.Enabled, nil
}

// listWorkflows reads every page; a page cap that is hit is an error, never a
// silently short list.
func listWorkflows(c *http.Client, repoURL, tok string) ([]workflow, error) {
	var all []workflow
	for page := 1; page <= maxWorkflowPages; page++ {
		_, b, err := do(c, http.MethodGet, repoURL+"/actions/workflows?per_page=100&page="+strconv.Itoa(page), tok, nil, http.StatusOK)
		if err != nil {
			return nil, err
		}
		var p struct {
			Total     *int       `json:"total_count"`
			Workflows []workflow `json:"workflows"`
		}
		if err := json.Unmarshal(b, &p); err != nil || p.Total == nil {
			return nil, errors.New("actions: the workflow list carried no \"total_count\", so nothing proves it is complete")
		}
		all = append(all, p.Workflows...)
		if len(all) >= *p.Total || len(p.Workflows) == 0 {
			if len(all) < *p.Total {
				return nil, fmt.Errorf("actions: %d workflows listed of %d", len(all), *p.Total)
			}
			return all, nil
		}
	}
	return nil, fmt.Errorf("actions: more than %d pages of workflows", maxWorkflowPages)
}

func describeWorkflows(out io.Writer, ws []workflow) {
	for _, w := range ws {
		fmt.Fprintf(out, "  workflow %d %q state=%s\n", w.ID, w.Name, w.State)
	}
}

// doActions turns Actions on for the repository and enables the workflows a
// fork left in "disabled_fork". A workflow someone disabled by hand
// ("disabled_manually") is reported, never switched on.
func doActions(out io.Writer, c *http.Client, repoURL, tok string, r repoInfo, o opts, action string) error {
	on, err := actionsEnabled(c, repoURL, tok)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "actions: enabled=%t\n", on)
	ws, err := listWorkflows(c, repoURL, tok)
	if err != nil {
		return err
	}
	describeWorkflows(out, ws)
	if action == "status" {
		return nil
	}
	if o.dryRun {
		if !on {
			fmt.Fprintln(out, "would enable Actions")
		}
		for _, w := range ws {
			if w.State == "disabled_fork" {
				fmt.Fprintf(out, "would enable workflow %d %q\n", w.ID, w.Name)
			}
		}
		return nil
	}
	if !on {
		payload, _ := json.Marshal(map[string]bool{"enabled": true})
		if _, _, err := do(c, http.MethodPut, repoURL+"/actions/permissions", tok, payload, http.StatusNoContent); err != nil {
			return err
		}
	}
	for _, w := range ws {
		if w.State != "disabled_fork" {
			continue
		}
		if _, _, err := do(c, http.MethodPut, fmt.Sprintf("%s/actions/workflows/%d/enable", repoURL, w.ID), tok, nil, http.StatusNoContent); err != nil {
			return err
		}
	}
	after, err := actionsEnabled(c, repoURL, tok)
	if err != nil {
		return err
	}
	if !after {
		return errors.New("actions: asked for enabled=true, the repository says enabled=false")
	}
	ws, err = listWorkflows(c, repoURL, tok)
	if err != nil {
		return err
	}
	for _, w := range ws {
		if w.State == "disabled_fork" {
			return fmt.Errorf("actions: workflow %d %q is still disabled_fork after the enable", w.ID, w.Name)
		}
	}
	fmt.Fprintf(out, "actions: enabled=true on %s; %d workflow(s):\n", r.FullName, len(ws))
	describeWorkflows(out, ws)
	return nil
}
