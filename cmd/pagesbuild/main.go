// Command pagesbuild waits until GitHub Pages has BUILT the gh-pages commit a
// workflow just pushed, and asks for a new build when it did not:
//
//	pagesbuild --token-file F owner/repo <gh-pages commit sha>
//
// Exit 0 when a build of exactly that commit reports "built"; non-zero, with a
// sentence, when it never does.
//
// Bibliography (opened 2026-11-06, docs.github.com/en/rest/pages/pages):
//
//	POST /repos/{owner}/{repo}/pages/builds       "Request a GitHub Pages
//	    build": 201 Created, body {url, status}. One concurrent build per
//	    repository and per requester; a request made while another build runs is
//	    queued until it finishes.
//	GET  /repos/{owner}/{repo}/pages/builds       "List GitHub Pages builds":
//	    per_page (max 100, default 30), page; an array of Page Build objects.
//	GET  /repos/{owner}/{repo}/pages/builds/latest, .../builds/{build_id}: the
//	    same object.
//	Page Build: url, status (string), error{message}, pusher, commit (string),
//	    duration, created_at, updated_at.
//
// What that page does NOT say, and this command therefore does not assume:
// the status values are not enumerated (only "built" and "errored" are acted
// on; every other value, "queued" and "building" among them, means "not
// finished"), and it names no token permission for these endpoints. In Actions
// the job's GITHUB_TOKEN with `pages: write` is what was measured to work for
// the POST (go-redfish/docs, 2026-11); the read endpoints list the classic
// `repo` scope for a personal token.
//
// ⛔ IT EXISTS BECAUSE A GREEN PUSH IS NOT A PUBLISHED SITE. A site that
// publishes by pushing to gh-pages is deployed by GitHub's legacy "pages build
// and deployment", which sometimes stalls in `updating_pages` and fails with
// "Timeout reached, aborting!" after 10 minutes (go-redfish/docs: 3 times in
// two days, about 10 successes; runs 42058763350 and 42252993462). Re-running
// the failed deploy does not help, and re-running the workflow pushes nothing
// (identical site). POSTing a new build and polling it reached "built" within
// about 3 minutes every time. The workflow was green throughout, because the
// push succeeded; only the site was stale.
//
// Builds of OTHER commits are ignored: a build of an older gh-pages commit
// that is "built" says nothing about the one we pushed. A build is looked for
// for --grace; none by then counts like an errored one. Every failure path
// ends non-zero (exit 1), never a quiet pass.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Waits (bounded) for a GitHub Pages build of exactly the given gh-pages commit
to report "built", and exits 0. When the build is "errored", is still not
finished after --timeout, or none appears for that commit within --grace, it
requests a new one (POST /repos/{owner}/{repo}/pages/builds) and waits again,
at most --retries times. If it is still not built it exits 1 with the reason,
so a stalled deploy is red instead of silent.

Builds of other commits are ignored.

The token is read from the file named by --token-file, never from a flag value
or a URL, sent to the API host only and never printed. In Actions the job needs
` + "`pages: write`" + `.

Exit status: 0 built, 1 not built after the retries, 2 could not be set up or
the API could not be read.`

var (
	repoRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	shaRE  = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
)

type build struct {
	URL    string `json:"url"`
	Status string `json:"status"`
	Commit string `json:"commit"`
	Error  struct {
		Message *string `json:"message"`
	} `json:"error"`
}

type opts struct {
	api, repo, sha, token string
	timeout, grace, poll  time.Duration
	retries               int
	client                *http.Client
	dead                  map[string]bool // errored builds already acted on, by URL
	out                   io.Writer
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it with SetArgs, SetOut and SetErr.
func RootCmd() *cobra.Command {
	var (
		tokenFile string
		o         opts
		apiURL    string
	)
	c := cli.New("pagesbuild [flags] OWNER/REPO SHA", "Wait until GitHub Pages has built a gh-pages commit; request a build if it did not", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) != 2 {
			return cli.Unanswerable("usage: pagesbuild [flags] OWNER/REPO SHA")
		}
		if !repoRE.MatchString(args[0]) {
			return cli.Unanswerable("%q is not OWNER/REPO", args[0])
		}
		if !shaRE.MatchString(args[1]) {
			return cli.Unanswerable("%q is not a commit SHA (7 to 64 lowercase hex digits)", args[1])
		}
		if o.retries < 0 || o.timeout <= 0 || o.grace <= 0 || o.poll <= 0 {
			return cli.Unanswerable("--retries must be >= 0 and --timeout, --grace, --poll positive")
		}
		if tokenFile == "" {
			return cli.Unanswerable("--token-file is required: requesting a Pages build needs a token")
		}
		b, err := os.ReadFile(tokenFile)
		if err != nil {
			return cli.Wrap(err)
		}
		if o.token = strings.TrimSpace(string(b)); o.token == "" {
			return cli.Unanswerable("--token-file %s is empty", tokenFile)
		}
		o.api, o.repo, o.sha = strings.TrimRight(apiURL, "/"), args[0], args[1]
		o.client = &http.Client{Timeout: 30 * time.Second}
		o.dead = map[string]bool{}
		o.out = cli.Out(cmd)
		return run(cmd.Context(), &o)
	}
	f := c.Flags()
	f.StringVar(&tokenFile, "token-file", "", "file holding a GitHub token (pages: write in Actions)")
	f.DurationVar(&o.timeout, "timeout", 5*time.Minute, "longest wait for one build to finish")
	f.DurationVar(&o.grace, "grace", 2*time.Minute, "how long to wait for a build of the commit to appear")
	f.DurationVar(&o.poll, "poll", 10*time.Second, "time between reads")
	f.IntVar(&o.retries, "retries", 2, "new builds to request after the first wait fails")
	f.StringVar(&apiURL, "api-url", "https://api.github.com", "GitHub REST API base URL")
	_ = f.MarkHidden("api-url")
	return c
}

func (o *opts) do(ctx context.Context, method, path string, body io.Reader) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, o.api+path, body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+o.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return b, resp.StatusCode, err
}

// verdict of one wait.
type verdict int

const (
	built verdict = iota
	errored
	absent
	stalled
)

// wait polls the list of builds until one of the commit is built or errored,
// or the clock runs out. "built" anywhere among the commit's builds wins; the
// newest build of the commit decides errored.
func (o *opts) wait(ctx context.Context) (verdict, string, error) {
	start := time.Now()
	for {
		b, code, err := o.do(ctx, "GET", "/repos/"+o.repo+"/pages/builds?per_page=100", nil)
		if err != nil {
			return 0, "", err
		}
		if code != http.StatusOK {
			return 0, "", fmt.Errorf("GET pages/builds: HTTP %d: %s", code, snippet(b))
		}
		var list []build
		if err := json.Unmarshal(b, &list); err != nil {
			return 0, "", fmt.Errorf("GET pages/builds: %w", err)
		}
		var mine []build // newest first, as the API lists them
		for _, x := range list {
			if strings.HasPrefix(x.Commit, o.sha) {
				mine = append(mine, x)
			}
		}
		for _, x := range mine {
			if x.Status == "built" {
				return built, "", nil
			}
		}
		// An errored build we already acted on stays in the list, and would
		// read as the answer to the build we requested after it.
		fresh := mine[:0:0]
		for _, x := range mine {
			if !o.dead[x.URL] {
				fresh = append(fresh, x)
			}
		}
		mine = fresh
		if len(mine) > 0 && mine[0].Status == "errored" {
			o.dead[mine[0].URL] = true
			msg := "no message"
			if mine[0].Error.Message != nil && *mine[0].Error.Message != "" {
				msg = *mine[0].Error.Message
			}
			return errored, msg, nil
		}
		el := time.Since(start)
		switch {
		case len(mine) == 0 && el >= o.grace:
			return absent, fmt.Sprintf("no build of %s among the last %d within %s", o.sha, len(list), o.grace), nil
		case el >= o.timeout:
			st := "none"
			if len(mine) > 0 {
				st = mine[0].Status
			}
			return stalled, fmt.Sprintf("status %q after %s", st, o.timeout), nil
		}
		select {
		case <-ctx.Done():
			return 0, "", ctx.Err()
		case <-time.After(o.poll):
		}
	}
}

func run(ctx context.Context, o *opts) error {
	for attempt := 0; ; attempt++ {
		v, why, err := o.wait(ctx)
		if err != nil {
			return cli.Unanswerable("%v", err)
		}
		if v == built {
			fmt.Fprintf(o.out, "pagesbuild: %s: commit %s is built (after %d requested build(s))\n", o.repo, o.sha, attempt)
			return nil
		}
		fmt.Fprintf(o.out, "pagesbuild: %s: commit %s not built: %s\n", o.repo, o.sha, why)
		if attempt >= o.retries {
			// Found is not printed by cli.Status (the command is expected to
			// have said it), so the verdict goes to the output here; the
			// ::error:: form is an annotation in Actions.
			fmt.Fprintf(o.out, "::error::pagesbuild: %s: commit %s was not built by GitHub Pages after %d requested build(s); last: %s\n", o.repo, o.sha, attempt, why)
			return cli.Found("%s: commit %s was not built by GitHub Pages after %d requested build(s); last: %s", o.repo, o.sha, attempt, why)
		}
		b, code, err := o.do(ctx, "POST", "/repos/"+o.repo+"/pages/builds", bytes.NewReader(nil))
		if err != nil {
			return cli.Unanswerable("POST pages/builds: %v", err)
		}
		if code != http.StatusCreated {
			return cli.Unanswerable("POST pages/builds: HTTP %d: %s", code, snippet(b))
		}
		fmt.Fprintf(o.out, "pagesbuild: %s: requested a new build (%d of %d)\n", o.repo, attempt+1, o.retries)
	}
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
