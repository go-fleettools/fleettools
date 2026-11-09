// Command ghrun performs the GitHub Actions writes sessions used to type with
// `gh`: dispatch a workflow, re-run a run (or only its failed jobs), cancel a
// run. It shows the target first (the workflow or the run) and sends nothing
// else under --dry-run.
//
// It exists because the standing rule is that every GitHub write goes through a
// fleet program, and `gh workflow run` (768 uses in the session record),
// `gh run rerun` (745) and `gh run cancel` (72) were the largest writes with no
// program. The HTTP and token handling is deliberately the same as ghissue's and
// is kept LOCAL (copied, not shared): the two differ in what they send, and the
// extraction would have to carry ghissue's test seams; it is a candidate for an
// internal package once a third program needs it.
//
// The token is read from a 0600 file and sent only in the Authorization header,
// over https (http only for 127.0.0.1 test servers), redirects refused.
//
// References (read 2026-11-08):
//
//	https://docs.github.com/en/rest/actions/workflows      POST /repos/{owner}/{repo}/actions/workflows/{workflow_id}/dispatches,
//	                                                       "ref" required, "inputs" at most 25 properties; documented 200 with
//	                                                       workflow_run_id, run_url, html_url. The page does not document the
//	                                                       opt-in parameter nor the older 204 No Content, so BOTH 200 and 204
//	                                                       are accepted and a reply without a run id is reported as such.
//	                                                       GET /repos/{owner}/{repo}/actions/workflows/{workflow_id}: id, name, path, state, html_url
//	https://docs.github.com/en/rest/actions/workflow-runs  GET /repos/{owner}/{repo}/actions/runs/{run_id}: id, name, head_branch, head_sha, status, conclusion, html_url
//	                                                       POST .../runs/{run_id}/rerun and .../rerun-failed-jobs: 201
//	                                                       POST .../runs/{run_id}/cancel: 202, 409 when it cannot be cancelled
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/go-fleettools/fleettools/internal/ghtoken"
	"github.com/spf13/cobra"
)

const (
	maxResponse = 1 << 20
	maxInputs   = 25       // GitHub's limit on workflow_dispatch inputs
	maxInFile   = 64 << 10 // bytes read from --inputs-file
	timeout     = 30 * time.Second
	apiBase     = "https://api.github.com"
)

const longHelp = `Writes to GitHub Actions through the REST API, after showing the target.

  ghrun <owner/repo> dispatch <workflow-file-or-id> --ref <branch> [--input k=v ...] [--inputs-file f.json]
  ghrun <owner/repo> rerun <run-id> [--failed]
  ghrun <owner/repo> cancel <run-id>

The workflow is its file name (ci.yml, not a path) or its numeric id. The target
(workflow or run: name, branch, head SHA, status, URL) is READ and printed before
anything is sent; --dry-run sends only that read.

dispatch: --input is repeatable; --inputs-file is a JSON object of scalars; an
--input overrides the same key from the file. At most 25 inputs. It prints the run
the API created when the API returns one, and says so when it does not.
rerun: re-runs every job, or only the failed ones with --failed. A run that has
not completed cannot be re-run, and --failed on a successful run has nothing to
re-run; both are refused before sending.
cancel: refuses a run that has already completed.
After rerun and cancel the run is READ again and its status printed as the API
reports it (a re-run may still say completed for a moment).

The token is read only from a file (default ~/.github-token, regular, owner-only)
and sent only in the Authorization header.

Exit status: 0 done, 2 refused or failed.`

// newClient is replaced by tests. Redirects are never followed: a redirect would
// carry the token header wherever it points.
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
	base, tokFile, ref, inputsFile string
	inputs                         []string
	dryRun, failed                 bool
}

// RootCmd builds the command; tests drive it with SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	var o opts
	cmd := &cobra.Command{
		Use:          "ghrun <owner/repo> dispatch|rerun|cancel <target> [flags]",
		Short:        "Dispatch a workflow, re-run a run, or cancel a run",
		Long:         longHelp,
		Args:         cobra.ExactArgs(3),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, args, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.base, "base", apiBase, "API base URL (https)")
	f.StringVar(&o.tokFile, "token-file", "", "file holding the token (default ~/.github-token)")
	f.StringVar(&o.ref, "ref", "", "dispatch: branch or tag to run the workflow on")
	f.StringArrayVar(&o.inputs, "input", nil, "dispatch: workflow input k=v (repeatable)")
	f.StringVar(&o.inputsFile, "inputs-file", "", "dispatch: JSON object of workflow inputs")
	f.BoolVar(&o.failed, "failed", false, "rerun: only the failed jobs")
	f.BoolVar(&o.dryRun, "dry-run", false, "print the target and send nothing else")
	return cmd
}

var workflowName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func run(cmd *cobra.Command, args []string, o opts) error {
	owner, repo, err := splitRepo(args[0])
	if err != nil {
		return err
	}
	verb, target := args[1], args[2]
	// Flags that belong to another verb are refused, not ignored: a silently
	// dropped --failed would re-run everything.
	switch verb {
	case "dispatch":
		if o.failed {
			return errors.New("--failed applies to rerun")
		}
	case "rerun", "cancel":
		if o.ref != "" || len(o.inputs) > 0 || o.inputsFile != "" {
			return errors.New("--ref, --input and --inputs-file apply to dispatch")
		}
		if verb == "cancel" && o.failed {
			return errors.New("--failed applies to rerun")
		}
	default:
		return fmt.Errorf("unknown verb %q: want dispatch, rerun or cancel", verb)
	}
	if !strings.HasPrefix(o.base, "https://") && !isTestBase(o.base) {
		return fmt.Errorf("base %q: must be https", o.base)
	}

	var (
		wf      string
		runID   int64
		inputs  map[string]string
		payload []byte
	)
	if verb == "dispatch" {
		if !workflowName.MatchString(target) {
			return fmt.Errorf("workflow %q: want a file name such as ci.yml (no path) or a numeric id", target)
		}
		wf = target
		if strings.TrimSpace(o.ref) == "" {
			return errors.New("dispatch needs --ref: the branch or tag to run on")
		}
		if inputs, err = collectInputs(o.inputs, o.inputsFile); err != nil {
			return err
		}
		m := map[string]any{"ref": o.ref, "return_run_details": true}
		if len(inputs) > 0 {
			m["inputs"] = inputs
		}
		if payload, err = json.Marshal(m); err != nil {
			return err
		}
	} else {
		runID, err = strconv.ParseInt(target, 10, 64)
		if err != nil || runID <= 0 {
			return fmt.Errorf("run id: %q is not a positive number", target)
		}
	}

	tok, err := readToken(o.tokFile)
	if err != nil {
		return err
	}
	c := newClient()
	out := cmd.OutOrStdout()
	root := fmt.Sprintf("%s/repos/%s/%s/actions", strings.TrimRight(o.base, "/"), owner, repo)

	if verb == "dispatch" {
		w, err := getWorkflow(c, root, tok, wf)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s/%s workflow %d %q (%s) [%s]\n  %s\n", owner, repo, w.ID, w.Name, w.Path, w.State, w.HTMLURL)
		fmt.Fprintf(out, "  ref: %s\n", o.ref)
		for _, k := range sortedKeys(inputs) {
			fmt.Fprintf(out, "  input %s=%s\n", k, inputs[k])
		}
		if w.State != "active" {
			return fmt.Errorf("workflow is %s: a dispatch would be refused", w.State)
		}
		if o.dryRun {
			fmt.Fprintln(out, "dry run: nothing sent")
			return nil
		}
		b, status, err := do(c, http.MethodPost, fmt.Sprintf("%s/workflows/%s/dispatches", root, url.PathEscape(wf)), tok, payload, http.StatusOK, http.StatusNoContent)
		if err != nil {
			return err
		}
		var got struct {
			RunID   int64  `json:"workflow_run_id"`
			HTMLURL string `json:"html_url"`
		}
		if status == http.StatusOK {
			_ = json.Unmarshal(b, &got)
		}
		if got.RunID == 0 {
			fmt.Fprintf(out, "dispatched (HTTP %d): the API returned no run id; list the runs of %s to find it\n", status, w.HTMLURL)
			return nil
		}
		fmt.Fprintf(out, "dispatched: run %d %s\n", got.RunID, got.HTMLURL)
		return nil
	}

	runURL := fmt.Sprintf("%s/runs/%d", root, runID)
	r, err := getRun(c, runURL, tok)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s/%s run %d %q branch %s sha %s [%s]\n  %s\n", owner, repo, r.ID, r.Name, r.HeadBranch, r.HeadSHA, r.state(), r.HTMLURL)

	var path, what string
	switch {
	case verb == "cancel":
		if r.Status == "completed" {
			return fmt.Errorf("run %d is already completed (%s): nothing to cancel", r.ID, r.Conclusion)
		}
		path, what = "/cancel", "cancel"
	case r.Status != "completed":
		return fmt.Errorf("run %d is %s: a run can be re-run only once it has completed", r.ID, r.Status)
	case o.failed:
		if r.Conclusion == "success" {
			return fmt.Errorf("run %d succeeded: it has no failed jobs to re-run", r.ID)
		}
		path, what = "/rerun-failed-jobs", "re-run failed jobs of"
	default:
		path, what = "/rerun", "re-run"
	}
	if o.dryRun {
		fmt.Fprintf(out, "dry run: would %s run %d, nothing sent\n", what, r.ID)
		return nil
	}
	want := http.StatusCreated
	if verb == "cancel" {
		want = http.StatusAccepted
	}
	if _, _, err := do(c, http.MethodPost, runURL+path, tok, nil, want); err != nil {
		return err
	}
	// The status the API reports now, not the one asked for: an accepted request
	// is not a finished one, and the re-read says where it stands.
	n, err := getRun(c, runURL, tok)
	if err != nil {
		return fmt.Errorf("sent (%s run %d), but re-reading it failed: %w", what, r.ID, err)
	}
	fmt.Fprintf(out, "sent: %s run %d; now %s\n", what, n.ID, n.state())
	return nil
}

func collectInputs(flags []string, file string) (map[string]string, error) {
	m := map[string]string{}
	if file != "" {
		f, err := os.Open(file)
		if err != nil {
			return nil, fmt.Errorf("inputs file: %w", err)
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, maxInFile+1))
		if err != nil {
			return nil, fmt.Errorf("inputs file: %w", err)
		}
		if len(b) > maxInFile {
			return nil, fmt.Errorf("inputs file: over %d bytes", maxInFile)
		}
		var raw map[string]any
		if err := json.Unmarshal(b, &raw); err != nil {
			return nil, fmt.Errorf("inputs file: want a JSON object: %w", err)
		}
		for k, v := range raw {
			switch x := v.(type) {
			case string:
				m[k] = x
			case bool:
				m[k] = strconv.FormatBool(x)
			case float64:
				m[k] = strconv.FormatFloat(x, 'f', -1, 64)
			default:
				return nil, fmt.Errorf("inputs file: %q is not a string, number or boolean", k)
			}
		}
	}
	for _, kv := range flags {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--input %q: want k=v", kv)
		}
		m[k] = v
	}
	if len(m) > maxInputs {
		return nil, fmt.Errorf("%d inputs: GitHub accepts at most %d", len(m), maxInputs)
	}
	return m, nil
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

type workflow struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
}

func getWorkflow(c *http.Client, root, tok, wf string) (workflow, error) {
	var w workflow
	b, _, err := do(c, http.MethodGet, root+"/workflows/"+url.PathEscape(wf), tok, nil, http.StatusOK)
	if err != nil {
		return w, err
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return w, fmt.Errorf("workflow: %w", err)
	}
	if w.ID == 0 {
		return w, errors.New("workflow: the reply carried no id")
	}
	return w, nil
}

type runInfo struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	HeadBranch string `json:"head_branch"`
	HeadSHA    string `json:"head_sha"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HTMLURL    string `json:"html_url"`
}

func (r runInfo) state() string {
	if r.Conclusion != "" {
		return r.Status + "/" + r.Conclusion
	}
	return r.Status
}

func getRun(c *http.Client, u, tok string) (runInfo, error) {
	var r runInfo
	b, _, err := do(c, http.MethodGet, u, tok, nil, http.StatusOK)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("run: %w", err)
	}
	if r.ID == 0 {
		return r, errors.New("run: the reply carried no id")
	}
	return r, nil
}

func splitRepo(s string) (owner, repo string, err error) {
	owner, repo, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", fmt.Errorf("repository %q: want owner/repo", s)
	}
	return owner, repo, nil
}

// readToken is ghtoken.Read: the token from a file only its owner may read.
var readToken = ghtoken.Read

// do sends one request and insists on one of the statuses the caller expects.
// A failure quotes the API's message: GitHub says why in it.
func do(c *http.Client, method, u, tok string, payload []byte, want ...int) ([]byte, int, error) {
	var rdr io.Reader
	if payload != nil {
		rdr = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, u, rdr)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return nil, 0, err
	}
	for _, w := range want {
		if resp.StatusCode == w {
			return b, w, nil
		}
	}
	return nil, resp.StatusCode, fmt.Errorf("%s %s: %s: %s", method, u, resp.Status, apiMessage(b))
}

// apiMessage prefers the "message" field GitHub sends, else the first line.
func apiMessage(b []byte) string {
	var m struct {
		Message string `json:"message"`
	}
	s := string(b)
	if json.Unmarshal(b, &m) == nil && m.Message != "" {
		s = m.Message
	}
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
