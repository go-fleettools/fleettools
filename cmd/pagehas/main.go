// Command pagehas checks a PUBLISHED page: that it answers 200, and that it
// carries each of the texts it is given.
//
//	pagehas https://go-images.github.io/docs/en/modules/ "Fuzzed since v0.13.8"
//
// # WHY IT EXISTS
//
// The rule here is that a change a user can see is finished when the README,
// the landing and the docs say so, AND the published page has been looked at:
// HTTP 200 and the new text present, not only a green merge. A merge says the
// source changed; a deploy says a workflow ran. Neither says what a reader is
// served, and both have been green while the page was not:
//
//   - 2026-10-18, go-simd: a landing link to /docs/repos/highway/ was merged
//     and deployed, and answered 404, because mike serves the docs under
//     /docs/latest/. Only fetching the page showed it.
//   - the same day, go-images/docs: the Pages build was still running when the
//     deploy workflow reported success, and the page served the old text for
//     a minute longer. A check that ends at the workflow cannot see that.
//
// That check was retyped, slightly differently, in a scratchpad every time it
// was needed, and lost with the scratchpad. It lives here now.
//
// # WHAT IT REFUSES TO SAY
//
// ⛔ A page that could not be read is not a page without the text. A timeout,
// a 404 or a body cut off at the size limit is reported as what it is, with
// its own exit status, so "the text is missing" is only ever said about a
// page that was read whole.
//
// ⛔ A CDN may serve a cached page for minutes after a deploy. --fresh adds a
// query string that the cache has not seen, so the answer comes from the
// origin; without it the answer is what a reader gets right now, which is
// also worth knowing. The report says which was asked.
package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

var osExit = os.Exit

// maxBody bounds what is read. A page larger than this is reported as cut off,
// never searched in part: a text absent from the first megabytes may be in
// the rest.
const maxBody = 8 << 20

// missing is a page that was read whole and lacks at least one text (exit 1).
type missing struct{ err error }

func (m *missing) Error() string { return m.err.Error() }
func (m *missing) Unwrap() error { return m.err }

// unread is a page that could not be read whole (exit 2). It is NOT the
// missing answer: nothing can be said about text on a page nobody read.
type unread struct{ err error }

func (u *unread) Error() string { return u.err.Error() }
func (u *unread) Unwrap() error { return u.err }

const longHelp = `pagehas fetches URL and checks that it answers 200 and that its body
contains every TEXT given, as a plain substring.

--fresh adds a query string the CDN has not cached, so the answer comes from
the origin rather than from a copy that may predate the deploy.

Exit 0 when the page was read and every text is present, 1 when it was read
and a text is missing, 2 when it could not be read whole (not 200, timeout,
larger than the limit) or on a usage error. A page that could not be read is
never reported as missing a text.`

func RootCmd() *cobra.Command {
	var timeout time.Duration
	var fresh bool
	c := &cobra.Command{
		Use:           "pagehas URL TEXT...",
		Short:         "Does the published page answer 200 and carry these texts?",
		Long:          longHelp,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MinimumNArgs(2),
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if timeout <= 0 {
			return errors.New("--timeout must be positive")
		}
		target := args[0]
		if fresh {
			var err error
			if target, err = withFreshQuery(target, time.Now()); err != nil {
				return err
			}
		}
		client := &http.Client{Timeout: timeout}
		return run(cmd, client, target, args[1:], fresh)
	}
	c.Flags().DurationVarP(&timeout, "timeout", "t", 30*time.Second, "request timeout")
	c.Flags().BoolVarP(&fresh, "fresh", "f", false, "add a query string the CDN has not cached, to read the origin")
	return c
}

// withFreshQuery adds a query parameter the cache cannot have seen.
func withFreshQuery(raw string, now time.Time) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("%q is not an absolute URL", raw)
	}
	q := u.Query()
	q.Set("pagehas", strconv.FormatInt(now.UnixNano(), 36))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func run(cmd *cobra.Command, client *http.Client, target string, texts []string, fresh bool) error {
	out := cmd.OutOrStdout()
	resp, err := client.Get(target)
	if err != nil {
		return &unread{fmt.Errorf("%s: %v", target, err)}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return &unread{fmt.Errorf("%s: reading the body: %v", target, err)}
	}
	source := "as served"
	if fresh {
		source = "from the origin (--fresh)"
	}
	fmt.Fprintf(out, "%d %s, %d bytes, %s\n", resp.StatusCode, resp.Request.URL, len(body), source)
	if resp.StatusCode != http.StatusOK {
		return &unread{fmt.Errorf("%s answered %d, so no text on it can be checked", target, resp.StatusCode)}
	}
	if len(body) > maxBody {
		return &unread{fmt.Errorf("%s is larger than %d bytes; a text absent from the part read may be in the rest", target, maxBody)}
	}
	page := string(body)
	var absent []string
	for _, t := range texts {
		mark := "present"
		if !strings.Contains(page, t) {
			mark = "MISSING"
			absent = append(absent, t)
		}
		fmt.Fprintf(out, "  %-8s %q\n", mark, t)
	}
	if len(absent) > 0 {
		return &missing{fmt.Errorf("%d of %d text(s) missing from %s", len(absent), len(texts), target)}
	}
	return nil
}

func execute(c *cobra.Command) int {
	err := c.Execute()
	if err == nil {
		return 0
	}
	fmt.Fprintf(c.ErrOrStderr(), "pagehas: %v\n", err)
	var m *missing
	if errors.As(err, &m) {
		return 1
	}
	return 2
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	osExit(execute(RootCmd()))
}
