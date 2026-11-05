// Command linkcheck says whether every external link a documentation source
// names still resolves:
//
//	linkcheck --skip go-wasmcontainers.github.io/docs content/ README.md
//
// It replaces the shell (grep, sed, curl, three attempts three seconds apart)
// the documentation workflows carried, which failed twice running on random
// 504s from github.com's HTML pages and passed on the third run: a check that
// fails at random gets ignored, and then it checks nothing.
//
// ⛔ A flaky host is still a failure, never a pass: the final attempt's 429 or
// 5xx is printed FLAKY, apart from a DEAD 404, and exits 2 ("could not be
// answered") where a dead link exits 1.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Reads every file under the given paths, extracts the http(s) URLs, and checks
each one: GET with retries and exponential backoff that honours Retry-After.

github.com URLs naming a repository or a path in it (/owner/repo, /blob/…,
/tree/…, /releases[/tag/T|/latest]) are decided by the GitHub REST API (repos,
contents at a ref), authenticated when --token-file is given, instead of the
HTML page, which answers 504 and 429 to a runner under load.

Never checked, and each one printed as skipped with its reason and counted:
  no host            a bare http:// or https:// (prose such as https://<host>/),
                     a scheme glued to a backtick, a shell variable as host;
  placeholder host   the reserved names of RFC 2606 and RFC 6761 and everything
                     under them: example, example.com/.org/.net, invalid, test,
                     local, localhost, and loopback addresses (127.0.0.1, ::1);
  built-in           img.shields.io and http://web/...;
  --skip             anything containing a --skip substring (the site's own
                     pages, the repository's own files on a pull request);
                     counted, not listed.
A real host is always checked.

The token is read from the file named by --token-file, never from a flag
value, the environment or a URL, and is sent to the API host only. It is never
printed.

Exit status: 0 every link resolves, 1 at least one is DEAD (404, 410, other
4xx), 2 no link was dead but some could not be checked (FLAKY: 429, 5xx,
timeout, run deadline) or the run could not be set up.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// reportSkipped prints every URL that was found and not checked, with its
// reason, then a count per reason: a skip is a decision to be seen, never a
// silent drop. --skip matches are counted but not listed (the caller wrote
// them).
func reportSkipped(out io.Writer, skipped []Skip) {
	if len(skipped) == 0 {
		return
	}
	n := map[string]int{}
	for _, s := range skipped {
		n[s.Reason]++
		if s.Reason != reasonUser {
			fmt.Fprintf(out, "  skip %-16s %s\n", s.Reason, s.URL)
		}
	}
	fmt.Fprintf(out, "skipped %d distinct URLs: %d %s, %d %s, %d %s, %d %s\n", len(skipped),
		n[reasonNoHost], reasonNoHost, n[reasonPlaceholder], reasonPlaceholder,
		n[reasonBuiltin], reasonBuiltin, n[reasonUser], reasonUser)
}

// RootCmd builds the command. Tests drive it with SetArgs, SetOut and SetErr.
func RootCmd() *cobra.Command {
	var (
		skips       []string
		tokenFile   string
		attempts    int
		backoff     time.Duration
		maxBackoff  time.Duration
		concurrency int
		deadline    time.Duration
		timeout     time.Duration
		apiURL      string
	)
	c := cli.New("linkcheck [flags] PATH...", "Does every external link of these sources resolve?", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cli.Unanswerable("no path given: nothing to read is not a clean result")
		}
		if attempts < 1 || concurrency < 1 || deadline <= 0 {
			return cli.Unanswerable("--attempts, --concurrency and --deadline must be positive")
		}
		token := ""
		if tokenFile != "" {
			b, err := os.ReadFile(tokenFile)
			if err != nil {
				return cli.Wrap(err)
			}
			if token = strings.TrimSpace(string(b)); token == "" {
				return cli.Unanswerable("--token-file %s is empty", tokenFile)
			}
		}
		out := cli.Out(cmd)
		urls, skipped, files, err := ReadURLs(args, skips)
		if err != nil {
			return cli.Wrap(err)
		}
		if len(urls) == 0 {
			return cli.Unanswerable("no links found in %d files (%d skipped): are the pages still there?", files, len(skipped))
		}
		reportSkipped(out, skipped)
		if token == "" {
			fmt.Fprintln(cli.Err(cmd), "linkcheck: no --token-file: GitHub API requests are unauthenticated (60 per hour)")
		}
		fmt.Fprintf(out, "checking %d distinct URLs from %d files\n", len(urls), files)

		ctx, cancel := context.WithTimeout(cmd.Context(), deadline)
		defer cancel()
		ck := &Checker{
			Client:     &http.Client{Timeout: timeout},
			APIBase:    strings.TrimRight(apiURL, "/"),
			Token:      token,
			Attempts:   attempts,
			Backoff:    backoff,
			MaxBackoff: maxBackoff,
		}
		dead, flaky := Report(out, ck.Run(ctx, urls, concurrency))
		fmt.Fprintf(out, "%d URLs: %d DEAD, %d FLAKY (could not be checked), %d ok\n",
			len(urls), dead, flaky, len(urls)-dead-flaky)
		switch {
		case dead > 0:
			return cli.FromStatus(1)
		case flaky > 0:
			return cli.Unanswerable("%d link(s) could not be checked after %d attempts: a flaky host, not a dead link, but not a pass either", flaky, attempts)
		}
		return nil
	}
	f := c.Flags()
	f.StringArrayVar(&skips, "skip", nil, "do not check URLs containing this substring (repeatable)")
	f.StringVar(&tokenFile, "token-file", "", "file holding a GitHub token, for API requests")
	f.IntVar(&attempts, "attempts", 4, "attempts per request")
	f.DurationVar(&backoff, "backoff", time.Second, "first wait between attempts; doubles each time")
	f.DurationVar(&maxBackoff, "max-backoff", 30*time.Second, "longest wait, Retry-After included")
	f.IntVar(&concurrency, "concurrency", 8, "requests in flight")
	f.DurationVar(&deadline, "deadline", 5*time.Minute, "total time for the run; unfinished URLs are FLAKY")
	f.DurationVar(&timeout, "timeout", 25*time.Second, "per-request timeout")
	f.StringVar(&apiURL, "api-url", "https://api.github.com", "GitHub REST API base URL")
	_ = f.MarkHidden("api-url")
	return c
}
