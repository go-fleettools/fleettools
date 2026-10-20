// Command glmerge merges a GitLab merge request only when it is safe to:
//
//	glmerge --token-file F PROJECT IID SHA
//
// open, mergeable, its head is SHA -- the commit that was reviewed and tested
// -- and its head pipeline succeeded ON that commit; and then the merge itself
// is pinned to SHA, so a push that lands between the check and the merge makes
// GitLab refuse instead of merging something nobody looked at. It is prmerge's
// rule (mergeable AND green) for the GitLab side of the fleet, where plmlab
// hosts the RESINFO working groups.
//
// ⛔ It replaces a shell function retyped in three sessions, which lost its
// file twice when /tmp was cleaned and once could not run at all.
//
// The token is read from a file and sent in the PRIVATE-TOKEN header only:
// never printed, never in a URL, an argument or the environment.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

var (
	osExit = os.Exit
	sleep  = time.Sleep
	// apiBase is replaced by tests with an httptest server.
	apiBase = func(host string) string { return "https://" + host + "/api/v4" }
)

type failure struct{ err error }

func (f *failure) Error() string { return f.err.Error() }

func refuse(format string, a ...any) error { return &failure{fmt.Errorf(format, a...)} }

const longHelp = `glmerge merges a GitLab merge request when, and only when:

  - it is open and GitLab calls it mergeable (it waits while GitLab is still
    "checking", up to a minute);
  - its head is SHA, the full commit you reviewed and tested;
  - its head pipeline succeeded on SHA (--no-pipeline for a project with no
    CI at all, and then that is said in the output).

The merge is pinned to SHA (merge commit, no squash, source branch kept): a
push landing after the check makes GitLab refuse rather than merge it.

The token comes from --token-file and goes in a header; it is never printed.

Exits 0 when merged, 1 when it refused or GitLab did, 2 on a usage error.`

var shaRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

type mergeRequest struct {
	State   string `json:"state"`
	Status  string `json:"detailed_merge_status"`
	SHA     string `json:"sha"`
	Target  string `json:"target_branch"`
	Merged  string `json:"merge_commit_sha"`
	Message any    `json:"message"`
	Head    *struct {
		Status string `json:"status"`
		SHA    string `json:"sha"`
	} `json:"head_pipeline"`
}

func RootCmd() *cobra.Command {
	var tokenFile, host string
	var noPipeline bool
	c := &cobra.Command{
		Use:           "glmerge PROJECT IID SHA --token-file F [flags]",
		Short:         "Merge a GitLab merge request when it is mergeable, green, and still the commit tested",
		Long:          longHelp,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ExactArgs(3),
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if tokenFile == "" {
			return errors.New("--token-file is required")
		}
		if !shaRE.MatchString(args[2]) {
			return fmt.Errorf("SHA must be the full 40-hex commit, got %q", args[2])
		}
		raw, err := os.ReadFile(tokenFile)
		if err != nil {
			return err
		}
		token := strings.TrimSpace(string(raw))
		if token == "" {
			return fmt.Errorf("%s is empty", tokenFile)
		}
		return run(cmd.OutOrStdout(), apiBase(host), token, args[0], args[1], args[2], noPipeline)
	}
	c.Flags().StringVarP(&tokenFile, "token-file", "f", "", "file holding a GitLab token with api scope")
	c.Flags().StringVar(&host, "host", "plmlab.math.cnrs.fr", "GitLab host")
	c.Flags().BoolVar(&noPipeline, "no-pipeline", false, "the project has no CI: do not require a head pipeline")
	return c
}

func execute(c *cobra.Command) int {
	err := c.Execute()
	if err == nil {
		return 0
	}
	fmt.Fprintf(c.ErrOrStderr(), "glmerge: %v\n", err)
	var f *failure
	if errors.As(err, &f) {
		return 1
	}
	return 2
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	osExit(execute(RootCmd()))
}

func call(method, u, token string) (mergeRequest, int, error) {
	var mr mergeRequest
	req, err := http.NewRequest(method, u, nil)
	if err != nil {
		return mr, 0, err
	}
	req.Header.Set("PRIVATE-TOKEN", token)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return mr, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return mr, resp.StatusCode, err
	}
	if err := json.Unmarshal(body, &mr); err != nil {
		return mr, resp.StatusCode, fmt.Errorf("HTTP %d, not JSON", resp.StatusCode)
	}
	return mr, resp.StatusCode, nil
}

func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func run(out io.Writer, base, token, project, iid, want string, noPipeline bool) error {
	u := fmt.Sprintf("%s/projects/%s/merge_requests/%s", base, url.PathEscape(project), url.PathEscape(iid))
	var mr mergeRequest
	for i := 0; ; i++ {
		var code int
		var err error
		mr, code, err = call(http.MethodGet, u, token)
		if err != nil {
			return refuse("reading the merge request: %v", err)
		}
		if code != http.StatusOK {
			return refuse("reading the merge request: HTTP %d %v", code, mr.Message)
		}
		if (mr.Status != "checking" && mr.Status != "unchecked" && mr.Status != "preparing") || i == 15 {
			break
		}
		sleep(4 * time.Second)
	}
	head := "none"
	if mr.Head != nil {
		head = mr.Head.Status + "@" + short(mr.Head.SHA)
	}
	fmt.Fprintf(out, "%s !%s: %s, %s, head %s, pipeline %s, into %s\n", project, iid, mr.State, mr.Status, short(mr.SHA), head, mr.Target)
	switch {
	case mr.State != "opened":
		return refuse("not open (%s)", mr.State)
	case mr.Status != "mergeable":
		return refuse("not mergeable (%s)", mr.Status)
	case mr.SHA != want:
		return refuse("its head is %s, not the commit asked for, %s", short(mr.SHA), short(want))
	case noPipeline:
		fmt.Fprintln(out, "no pipeline required (--no-pipeline)")
	case mr.Head == nil:
		return refuse("no pipeline ran on it (use --no-pipeline only for a project without CI)")
	case mr.Head.SHA != want || mr.Head.Status != "success":
		return refuse("its pipeline is %s on %s, not success on %s", mr.Head.Status, short(mr.Head.SHA), short(want))
	}
	q := url.Values{"sha": {want}, "squash": {"false"}, "should_remove_source_branch": {"false"}}
	done, code, err := call(http.MethodPut, u+"/merge?"+q.Encode(), token)
	if err != nil {
		return refuse("merging: %v", err)
	}
	if code != http.StatusOK || done.State != "merged" {
		return refuse("GitLab did not merge: HTTP %d, state %q, %v", code, done.State, done.Message)
	}
	fmt.Fprintf(out, "merged: %s\n", done.Merged)
	return nil
}
