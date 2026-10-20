// Command docscheck asks which GENERATOR a published documentation URL was
// built with, and whether a site's default entry point serves the same one as
// its development tip.
//
//	docscheck https://go-pkgx.github.io/docs https://go-images.github.io/docs
//
// # WHY IT EXISTS
//
// ⛔ BECAUSE I WITHDREW A CORRECT BUG REPORT ON BAD EVIDENCE. The question
// was whether `/docs/latest/` was the old MkDocs site or the new Hextra one,
// and I answered it from a FILE LISTING — assets/, 404.html, search/,
// sitemap.xml.gz — concluded Hextra, and closed the report. mkdocs-material
// produces every one of those. Shared artefacts cannot separate two
// candidates; only a marker that exactly one of them emits can.
//
// mkdocs-material namespaces its classes `md-` (md-header, md-nav); Hextra
// carries its own name (hextra, hx-). Measured on the same page that fooled
// the listing: 66 mkdocs-material markers and 0 Hextra ones under `latest/`,
// against 0 and 22 under `main/`.
//
// ⛔ AND NO STATUS CODE SAYS IT. Both URLs answer 200. A site whose entry
// point is built by a different generator from its tip is not "a version
// behind", it is the SUPERSEDED SITE still being served — on go-pkgx that
// entry point tells a reader to type `pkgx ls`, which since pkgx#113 runs GNU
// ls, so the published documentation now contradicts the shipped tool rather
// than merely lagging it.
//
// # WHAT IT MEASURED
//
// 2026-10-20, four sites: go-pkgx, go-images and go-filesystems each serve
// mkdocs-material at `latest/` and Hextra at `main/`; go-plot serves Hextra
// at `latest/` and has no `main/`, and it is the one that was migrated by
// hand as the reference for the others.
//
// ⛔ THAT FOURTH SITE IS WHY THE COMPARISON IS NARROW. An HTTP 404, a
// timeout and a redirect stub are each a real answer and NONE of them is a
// generator: comparing them as if they were reports go-plot — the one site
// that is right — as a mismatch. Two pages are compared only when both named
// a generator, and what could not be read is counted and reported apart,
// because a scan that read nothing must never print the same verdict as a
// scan that read everything and found it clean.
package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

var osExit = os.Exit

// failure is a scan that found a mismatch or could not read anything (exit 1
// and 2 respectively); anything else out of the command is a usage error.
type failure struct{ err error }

func (f *failure) Error() string { return f.err.Error() }
func (f *failure) Unwrap() error { return f.err }

func failf(format string, a ...any) error { return &failure{fmt.Errorf(format, a...)} }

// unread is a scan that read nothing at all. It is NOT the clean answer, and
// it is not the mismatch answer either, so it carries its own status.
type unread struct{ err error }

func (u *unread) Error() string { return u.err.Error() }
func (u *unread) Unwrap() error { return u.err }

// The markers, and the whole claim this command rests on: each string is
// emitted by exactly one of the two generators and by neither the other's
// templates.
var markers = []struct {
	name string
	any  []string
}{
	{"mkdocs-material", []string{"md-header", "md-nav", "mkdocs"}},
	{"hextra", []string{"hextra", "hx-"}},
}

// A verdict is either a generator's name or one of these. None of them is a
// generator, and the difference is what keeps a 404 from reading as a
// mismatch.
const (
	redirect    = "redirect"
	none        = "none"
	unreachable = "unreachable"
	unreadable  = "unreadable"
)

func isGenerator(v string) bool {
	for _, m := range markers {
		if v == m.name {
			return true
		}
	}
	return false
}

const longHelp = `docscheck fetches BASE/, BASE/latest/ and BASE/main/ for each base URL and
says which documentation generator built each one, by markers that exactly one
generator emits: mkdocs-material namespaces its classes md-, Hextra carries its
own name.

It reports a site whose entry point was built by a DIFFERENT generator from its
tip -- the superseded site still being served, which no status code reveals
because both answer 200. A page that is a redirect stub, a 404 or unreachable
is not a generator and is never compared as one.

Exit 0 every site agreed, 1 at least one disagreed, 2 nothing could be read or
a usage error.`

func RootCmd() *cobra.Command {
	var timeout time.Duration
	var verbose bool
	c := &cobra.Command{
		Use:           "docscheck BASE-URL...",
		Short:         "Is the published entry point the same site as the tip?",
		Long:          longHelp,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MinimumNArgs(1),
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if timeout <= 0 {
			return errors.New("--timeout must be positive")
		}
		return run(cmd, args, timeout, verbose)
	}
	c.Flags().DurationVarP(&timeout, "timeout", "t", 20*time.Second, "per-request timeout")
	c.Flags().BoolVarP(&verbose, "counts", "c", false, "print each page's marker counts, the evidence behind its verdict")
	return c
}

func execute(c *cobra.Command) int {
	err := c.Execute()
	if err == nil {
		return 0
	}
	fmt.Fprintf(c.ErrOrStderr(), "docscheck: %v\n", err)
	var u *unread
	if errors.As(err, &u) {
		return 2
	}
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

// page is one fetched URL: its verdict, and the evidence for it.
type page struct {
	verdict string
	counts  map[string]int
}

func run(cmd *cobra.Command, bases []string, timeout time.Duration, verbose bool) error {
	out := cmd.OutOrStdout()
	client := &http.Client{Timeout: timeout}
	var bad, compared int
	for _, base := range bases {
		base = strings.TrimSuffix(base, "/")
		root := fetch(client, base+"/")
		latest := fetch(client, base+"/latest/")
		tip := fetch(client, base+"/main/")
		fmt.Fprintf(out, "%-44s root=%-16s latest=%-16s main=%s\n",
			base, root.verdict, latest.verdict, tip.verdict)
		if verbose {
			for _, p := range []struct {
				at string
				p  page
			}{{"", root}, {"latest/", latest}, {"main/", tip}} {
				fmt.Fprintf(out, "%-44s   %-8s %v\n", "", p.at, p.p.counts)
			}
		}
		// ⛔ ONLY TWO GENERATORS ARE COMPARABLE. A 404 or a timeout is an
		// answer, not a build, and comparing it reports the one site that is
		// right as the one that is wrong.
		if !isGenerator(latest.verdict) || !isGenerator(tip.verdict) {
			continue
		}
		compared++
		if latest.verdict != tip.verdict {
			fmt.Fprintf(out, "%-44s   ⛔ the entry point serves %s while the tip is %s\n",
				"", latest.verdict, tip.verdict)
			bad++
		}
	}
	if compared == 0 {
		return &unread{fmt.Errorf("no site had both an entry point and a tip built by a known generator, so nothing was compared")}
	}
	fmt.Fprintf(out, "\n%d site(s) compared, %d serving a superseded build\n", compared, bad)
	if bad > 0 {
		return failf("%d of %d site(s) serve a different generator at their entry point than at their tip", bad, compared)
	}
	return nil
}

func fetch(client *http.Client, url string) page {
	resp, err := client.Get(url)
	if err != nil {
		return page{unreachable, nil}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return page{fmt.Sprintf("HTTP %d", resp.StatusCode), nil}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return page{unreadable, nil}
	}
	return judge(string(b))
}

// judge is the marker test, split out so a test can hand it a page instead of
// a server.
func judge(body string) page {
	counts := map[string]int{}
	var hits []string
	for _, m := range markers {
		n := 0
		for _, s := range m.any {
			n += strings.Count(body, s)
		}
		counts[m.name] = n
		if n > 0 {
			hits = append(hits, m.name)
		}
	}
	switch len(hits) {
	case 0:
		// A redirect stub is a real answer and not a generator: mike writes a
		// 374-byte index.html whose whole job is to send the reader on.
		if strings.Contains(body, "Redirecting") || strings.Contains(body, "http-equiv=\"refresh\"") {
			return page{redirect, counts}
		}
		return page{none, counts}
	case 1:
		return page{hits[0], counts}
	default:
		// ⛔ BOTH IS NOT A TIE TO BREAK SILENTLY. It means the markers are not
		// as exclusive as this file claims, and that claim is what every
		// answer here rests on. It is reported, and it is not a generator, so
		// it is never compared as one.
		return page{"BOTH(" + strings.Join(hits, "+") + ")", counts}
	}
}
