// Command pagetext says whether a PUBLISHED page contains the text we wrote:
//
//	pagetext [flags] URL want... [-- URL want...]
//
// It is the last step of "every user-visible change updates the README, the
// landing page and the docs, and the published page is checked". It is not
// pagescheck, which asks whether a Pages site serves what its repository builds.
//
// ⛔ The first check after a deploy said the new text was MISSING. It was not:
// GitHub Pages' CDN was still serving the previous build, and the same URL with
// a cache-busting query showed the text. A shared cache keys a response on the
// target URI, query included (RFC 9111, section 2), so a changed query string is
// a different entry and reaches the origin. So a query parameter is added by
// default (-no-cache-bust turns it off), and -retry N -wait D polls, bounded,
// until the deploy is visible.
//
// The match is on the page's TEXT: Hugo escapes and minifies, so "--output" can
// be split by tags or written with entities. -raw matches the bytes instead.
//
// Reading a page is not a GitHub API call and costs nothing against the budget
// Renovate shares.
package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Fetches each URL and prints its status and, for each string, whether the page
contains it.

    pagetext https://example.org "a title" "a sentence"
    pagetext --retry 6 URL want -- URL want

Flags go BEFORE the first URL, and -- separates one URL's group from the next.
Flag parsing stops at the first URL, so a -- after it is a separator and not a
terminator.

By default the match is on the page's TEXT (tags dropped, entities decoded,
whitespace collapsed, script and style ignored, and the typographic punctuation
a Markdown renderer substitutes folded back on BOTH sides, so that a want
holding an apostrophe matches the curly one Hugo prints); --raw matches the raw
bytes. A
cache-busting query parameter is added unless --no-cache-bust, because Pages'
CDN can serve the previous build for a while after a deploy. --retry N polls up
to N more times, --wait apart, until the page is 200 and has every string.

Exit status: 0 every page is 200 and has every string, 1 one does not, 2 a usage
error.`

// maxBody bounds what is read from one response: a page with a data URL in it
// can be megabytes, and a hostile or broken server can send forever.
const maxBody = 16 << 20

// sleep and now are the seams a test replaces.
var (
	sleep = time.Sleep
	now   = time.Now
)

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it through run, below.
func RootCmd() *cobra.Command {
	var (
		o      options
		noBust bool
	)
	c := cli.New("pagetext [flags] URL want... [-- URL want...]",
		"Whether each page is 200 and contains each string", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		o.bust = !noBust
		return cli.FromStatus(checkAll(cli.Out(cmd), cli.Err(cmd), o, args))
	}
	f := c.Flags()
	f.BoolVar(&o.raw, "raw", false, "match the raw bytes instead of the page's text")
	f.BoolVar(&noBust, "no-cache-bust", false, "do not add the nocache=<time> query parameter")
	f.IntVar(&o.retry, "retry", 0,
		"poll up to N more times until the page is 200 and has every string")
	f.DurationVar(&o.wait, "wait", 20*time.Second, "time between polls")
	f.DurationVar(&o.timeout, "timeout", 20*time.Second, "timeout of one request")
	return c
}

// run keeps the signature this command's tests were written against.
func run(args []string, out, errOut io.Writer) int {
	return cli.Drive(RootCmd(), args, out, errOut)
}

type options struct {
	raw     bool
	bust    bool
	retry   int
	wait    time.Duration
	timeout time.Duration
}

// checkAll is the command's work. The name avoids `check`, which this package
// already uses for one page.
func checkAll(out, errOut io.Writer, o options, args []string) int {
	if o.retry < 0 || o.wait < 0 || o.timeout <= 0 {
		fmt.Fprintln(errOut, "pagetext: --retry and --wait must not be negative, --timeout must be positive")
		return 2
	}
	groups := split(args)
	if len(groups) == 0 {
		fmt.Fprintln(errOut, longHelp)
		return 2
	}
	for _, g := range groups {
		u, err := url.Parse(g[0])
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			fmt.Fprintf(errOut, "pagetext: %q is not an http(s) URL\n", g[0])
			return 2
		}
	}

	failed := false
	for _, g := range groups {
		if !report(g[0], g[1:], o, out, errOut) {
			failed = true
		}
	}
	if failed {
		return 1
	}
	return 0
}

// split cuts the operands at "--" into groups of URL followed by its strings.
func split(args []string) [][]string {
	var out [][]string
	cur := []string{}
	for _, a := range args {
		if a == "--" {
			if len(cur) > 0 {
				out = append(out, cur)
			}
			cur = []string{}
			continue
		}
		cur = append(cur, a)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// result is one fetch judged against its strings.
type result struct {
	status int
	final  string // the URL after redirects, without the cache-buster
	err    error
	found  []bool
}

func (r result) ok() bool {
	if r.err != nil || r.status != http.StatusOK {
		return false
	}
	for _, f := range r.found {
		if !f {
			return false
		}
	}
	return true
}

// report fetches, retrying while the page is not yet right, prints the last
// reading, and says whether it was right.
func report(target string, wants []string, o options, out, errOut io.Writer) bool {
	var r result
	for attempt := 0; ; attempt++ {
		r = check(target, wants, o)
		if r.ok() || attempt >= o.retry {
			break
		}
		fmt.Fprintf(errOut, "pagetext: %s not right yet (%s); attempt %d of %d, waiting %v\n",
			target, why(r), attempt+1, o.retry+1, o.wait)
		sleep(o.wait)
	}
	if r.err != nil {
		fmt.Fprintf(out, "FAIL %s: %v\n", target, r.err)
		return false
	}
	line := fmt.Sprintf("%d %s", r.status, target)
	if r.final != "" && r.final != target {
		line += " -> " + r.final
	}
	fmt.Fprintln(out, line)
	for i, w := range wants {
		fmt.Fprintf(out, "  %v %q\n", r.found[i], w)
	}
	return r.ok()
}

func why(r result) string {
	switch {
	case r.err != nil:
		return r.err.Error()
	case r.status != http.StatusOK:
		return "status " + strconv.Itoa(r.status)
	}
	n := 0
	for _, f := range r.found {
		if !f {
			n++
		}
	}
	return fmt.Sprintf("%d string(s) missing", n)
}

// check is one fetch.
func check(target string, wants []string, o options) result {
	u, _ := url.Parse(target)
	if o.bust {
		q := u.Query()
		q.Set("nocache", strconv.FormatInt(now().UnixNano(), 10))
		u.RawQuery = q.Encode()
	}
	c := &http.Client{Timeout: o.timeout}
	resp, err := c.Get(u.String())
	if err != nil {
		return result{err: err}
	}
	defer resp.Body.Close()
	r := result{status: resp.StatusCode, final: unbust(resp.Request.URL, o.bust)}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return result{err: err}
	}
	if len(body) > maxBody {
		return result{err: fmt.Errorf("the page is larger than %d bytes; refusing to read on", maxBody)}
	}
	hay := string(body)
	if !o.raw {
		hay = plain(textOf(body))
	}
	for _, w := range wants {
		if !o.raw {
			w = plain(collapse(w))
		}
		r.found = append(r.found, strings.Contains(hay, w))
	}
	return r
}

// plain folds the punctuation a Markdown renderer SUBSTITUTES back to the
// characters we typed.
//
// ⛔ IT COST SIX RETRIES ON A PAGE THAT WAS ALREADY CORRECT, on 2026-11-01. The
// landing said `262 of this fleet's own`, Hugo printed `fleet’s` with a
// typographic apostrophe, and this command -- which decodes entities and drops
// tags -- reported the string MISSING and kept polling until it gave up. A
// verification tool answering "not published yet" about something published is
// worse than no tool: the next person reads a real failure the same way.
//
// Goldmark's typographer, on by default in Hugo, substitutes the apostrophe and
// both quote pairs, and `--`/`---` become en and em dashes, so ANY English
// possessive or contraction in a want string was a false negative.
//
// Both sides are folded, which deliberately makes an em dash match a hyphen:
// this is a presence check over prose, and conflating two dashes is the price of
// matching what the renderer prints. `--raw` stays exact for anything that must
// be compared byte for byte.
var plainer = strings.NewReplacer(
	"‘", "'", "’", "'", // ‘ ’
	"‚", "'", "‛", "'", // ‚ ‛
	"“", `"`, "”", `"`, // “ ”
	"„", `"`, "‟", `"`, // „ ‟
	"′", "'", "″", `"`, // ′ ″ (primes, which some pages use for quotes)
	"‐", "-", "‑", "-", // ‐ ‑
	"‒", "-", "–", "-", "—", "-", "―", "-", // ‒ – — ―
	"…", "...", // …
)

func plain(s string) string { return plainer.Replace(s) }

// unbust shows a URL without the parameter this command added.
func unbust(u *url.URL, bust bool) string {
	c := *u
	if bust {
		q := c.Query()
		q.Del("nocache")
		c.RawQuery = q.Encode()
	}
	return c.String()
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// breaks are the elements that end a line of text; every other tag is inline
// and joins its neighbours, so `<code>--</code><code>output</code>` reads as
// "--output" the way a browser shows it.
var breaks = map[string]bool{
	"p": true, "div": true, "br": true, "li": true, "ul": true, "ol": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"tr": true, "td": true, "th": true, "table": true, "pre": true,
	"section": true, "article": true, "header": true, "footer": true,
	"nav": true, "main": true, "aside": true, "dt": true, "dd": true,
	"blockquote": true, "hr": true, "title": true, "body": true,
}

// textOf is the text a reader sees: entities decoded, script and style left
// out, whitespace collapsed.
func textOf(page []byte) string {
	var sb strings.Builder
	z := html.NewTokenizer(bytes.NewReader(page))
	skip := ""
	for {
		switch z.Next() {
		case html.ErrorToken:
			return collapse(sb.String())
		case html.TextToken:
			if skip == "" {
				sb.Write(z.Text()) // Text decodes entities
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			n := string(name)
			if skip == "" && (n == "script" || n == "style" || n == "template") {
				skip = n
			}
			if breaks[n] {
				sb.WriteByte(' ')
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			n := string(name)
			if n == skip {
				skip = ""
			}
			if breaks[n] {
				sb.WriteByte(' ')
			}
		}
	}
}
