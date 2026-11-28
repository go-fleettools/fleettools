// Command ghget is the fleet's read-only GitHub REST client: it replaces
// `gh api` for GET requests, with the right token.
//
// `gh` uses its own login, which lacks scopes the fleet's token file has
// (admin:org): `gh api orgs/<org>/actions/permissions/fork-pr-contributor-approval`
// answered 403 in four organisations while ~/.github-token could read it.
//
// It can only send GET: the method is a constant in one place, there is no
// flag that sets a method or a body, and the path is refused if it is not a
// relative API path. The token comes from a 0600 file (default ~/.github-token),
// goes only in the Authorization header, and is redacted from anything printed.
// https only (http for 127.0.0.1 test servers); redirects are refused, so the
// token never follows a Location elsewhere.
//
// Exit status separates an answer from the absence of one:
//
//	0  answer read (JSON on stdout)
//	3  404: the thing is absent
//	4  401/403: UNREAD, not empty; the missing scope is printed
//	5  rate limited; the reset time is printed
//	2  usage or any other failure
//
// References (read 2026-11-28):
//
//	https://docs.github.com/en/rest/using-the-rest-api/using-pagination-in-the-rest-api   (Link header, rel="next")
//	https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api       (x-ratelimit-remaining/reset, retry-after, 403/429)
//	https://docs.github.com/rest/actions/permissions#get-fork-pr-contributor-approval-permissions-for-an-organization
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/go-fleettools/fleettools/internal/ghtoken"
	"github.com/spf13/cobra"
)

const (
	maxResponse = 64 << 20
	maxPages    = 1000
	timeout     = 60 * time.Second
	apiBase     = "https://api.github.com"
)

const longHelp = `Reads one GitHub REST path with the fleet token (GET only).

  ghget orgs/go-authn/actions/permissions/fork-pr-contributor-approval
  ghget orgs/go-authn/repos --paginate --field name

--paginate follows Link rel="next" and concatenates arrays (for an object
wrapping an array, such as {"total_count":..,"runner_groups":[..]}, the arrays
are concatenated and the other fields come from the first page).
--field a.b.c prints that member (a string without quotes) instead of the body.

Exit status: 0 answer read; 3 404 absent; 4 401/403 UNREAD (the missing scope
is printed); 5 rate limited (the reset is printed); 2 usage or other failure.
The token is read only from a file (default ~/.github-token, owner-only).`

var newClient = func(d time.Duration) *http.Client {
	return &http.Client{
		Timeout:       d,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

var readToken = ghtoken.Read

var now = time.Now

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

type opts struct {
	base, tokFile, field string
	paginate             bool
}

// RootCmd builds the command; tests drive it with SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	var o opts
	cmd := &cobra.Command{
		Use:          "ghget <path> [--paginate] [--field a.b]",
		Short:        "Read-only GitHub REST client (GET only) using the fleet token",
		Long:         longHelp,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, args[0], o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.base, "base", apiBase, "API base URL (https)")
	f.StringVar(&o.tokFile, "token-file", "", "file holding the token (default ~/.github-token)")
	f.BoolVar(&o.paginate, "paginate", false, "follow Link rel=next and concatenate arrays")
	f.StringVar(&o.field, "field", "", "print only this dotted member of the answer")
	return cmd
}

// ignoredEnv are the variables other GitHub clients take a token from. ghget
// never reads them (the token is in a file, never in the environment); it only
// names the ones that are set, never their values.
var ignoredEnv = []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_HOST"}

func warnIgnoredEnv(w io.Writer) {
	for _, k := range ignoredEnv {
		if _, ok := os.LookupEnv(k); ok {
			fmt.Fprintf(w, "ghget: warning: %s is set and is ignored; the token is read only from --token-file (default ~/.github-token)\n", k)
		}
	}
}

func isTestBase(base string) bool {
	return strings.HasPrefix(base, "http://127.0.0.1:") || strings.HasPrefix(base, "http://[::1]:")
}

// buildURL turns a user path into an absolute URL under base, refusing
// anything that is not a relative API path.
func buildURL(base, p string, paginate bool) (string, error) {
	if strings.TrimSpace(p) == "" || strings.ContainsAny(p, " \t\r\n\\") || strings.Contains(p, "://") ||
		strings.HasPrefix(p, "//") || strings.Contains(p, "#") {
		return "", fmt.Errorf("path %q: want a relative API path such as orgs/<org>/repos", p)
	}
	p = strings.TrimPrefix(p, "/")
	b, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || b.Host == "" {
		return "", fmt.Errorf("base %q: not a URL", base)
	}
	u, err := url.Parse(b.String() + "/" + p)
	if err != nil || u.Host != b.Host {
		return "", fmt.Errorf("path %q: not a relative API path", p)
	}
	if paginate {
		q := u.Query()
		if q.Get("per_page") == "" {
			q.Set("per_page", "100")
			u.RawQuery = q.Encode()
		}
	}
	return u.String(), nil
}

type redactor struct{ tok string }

func (r redactor) s(x string) string {
	if r.tok == "" {
		return x
	}
	return strings.ReplaceAll(x, r.tok, "***")
}

func run(cmd *cobra.Command, path string, o opts) error {
	out := cmd.OutOrStdout()
	errw := cmd.ErrOrStderr()
	if !strings.HasPrefix(o.base, "https://") && !isTestBase(o.base) {
		return cli.Unanswerable("base %q: https only", o.base)
	}
	u, err := buildURL(o.base, path, o.paginate)
	if err != nil {
		return cli.Unanswerable("%v", err)
	}
	warnIgnoredEnv(errw)
	tok, err := readToken(o.tokFile)
	if err != nil {
		return cli.Unanswerable("%v", err)
	}
	rd := redactor{tok}
	c := newClient(timeout)

	var pages []json.RawMessage
	for n := 0; ; n++ {
		if n >= maxPages {
			return cli.Unanswerable("more than %d pages: refusing to continue", maxPages)
		}
		body, hdr, code, err := get(c, u, tok)
		if err != nil {
			return cli.Unanswerable("%s", rd.s(err.Error()))
		}
		if code < 200 || code > 299 {
			return statusExit(errw, rd, u, code, hdr, body)
		}
		pages = append(pages, json.RawMessage(body))
		if !o.paginate {
			break
		}
		next := nextLink(hdr.Get("Link"))
		if next == "" {
			break
		}
		if u, err = sameHost(o.base, next); err != nil {
			return cli.Unanswerable("%v", err)
		}
	}
	res, err := merge(pages)
	if err != nil {
		return cli.Unanswerable("%s", rd.s(err.Error()))
	}
	if o.field != "" {
		s, err := pick(res, o.field)
		if err != nil {
			return cli.Unanswerable("%v", err)
		}
		fmt.Fprintln(out, rd.s(s))
		return nil
	}
	fmt.Fprintln(out, rd.s(string(res)))
	return nil
}

// get is the only request this program sends, and the method is a constant.
func get(c *http.Client, u, tok string) ([]byte, http.Header, int, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "ghget")
	resp, err := c.Do(req)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("GET %s: %w", u, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, nil, 0, fmt.Errorf("GET %s: %w", u, err)
	}
	if len(b) > maxResponse {
		return nil, nil, 0, fmt.Errorf("GET %s: answer over %d bytes", u, maxResponse)
	}
	return b, resp.Header, resp.StatusCode, nil
}

func statusExit(errw io.Writer, rd redactor, u string, code int, h http.Header, body []byte) error {
	msg := apiMessage(body)
	switch {
	case code == 404:
		return &cli.Exit{Code: 3, Err: fmt.Errorf("404 absent: %s", rd.s(u))}
	case code == 429 || ((code == 403 || code == 401) && (h.Get("X-RateLimit-Remaining") == "0" || h.Get("Retry-After") != "")) ||
		(code == 403 && strings.Contains(strings.ToLower(msg), "rate limit")):
		reset := "unknown"
		if v := h.Get("X-RateLimit-Reset"); v != "" {
			if sec, err := strconv.ParseInt(v, 10, 64); err == nil {
				reset = time.Unix(sec, 0).UTC().Format(time.RFC3339)
			}
		} else if v := h.Get("Retry-After"); v != "" {
			if sec, err := strconv.Atoi(v); err == nil {
				reset = now().Add(time.Duration(sec) * time.Second).UTC().Format(time.RFC3339)
			}
		}
		return &cli.Exit{Code: 5, Err: fmt.Errorf("rate limited (HTTP %d); resets at %s: %s", code, reset, rd.s(msg))}
	case code == 401 || code == 403:
		need := h.Get("X-Accepted-OAuth-Scopes")
		have := h.Get("X-OAuth-Scopes")
		return &cli.Exit{Code: 4, Err: fmt.Errorf("UNREAD (HTTP %d, not an empty answer): %s; token scopes [%s], endpoint accepts [%s]",
			code, rd.s(msg), have, need)}
	}
	return &cli.Exit{Code: 2, Err: fmt.Errorf("HTTP %d: %s", code, rd.s(msg))}
}

func apiMessage(body []byte) string {
	var m struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &m) == nil && m.Message != "" {
		return m.Message
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

var nextRe = regexp.MustCompile(`<([^>]*)>\s*;\s*rel="next"`)

func nextLink(h string) string {
	if m := nextRe.FindStringSubmatch(h); m != nil {
		return m[1]
	}
	return ""
}

// sameHost accepts a next link only on the host of base, so the token is never
// sent to another.
func sameHost(base, next string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	n, err := url.Parse(next)
	if err != nil || n.Host != b.Host || n.Scheme != b.Scheme {
		return "", fmt.Errorf("next link %q is not on %s: refusing to send the token there", next, b.Host)
	}
	return n.String(), nil
}

// merge returns one page unchanged; several pages are concatenated (arrays),
// or, for objects, their array members are, the rest coming from the first.
func merge(pages []json.RawMessage) ([]byte, error) {
	if len(pages) == 1 {
		return bytes.TrimSpace(pages[0]), nil
	}
	var first any
	if err := json.Unmarshal(pages[0], &first); err != nil {
		return nil, fmt.Errorf("page 1 is not JSON: %w", err)
	}
	switch f := first.(type) {
	case []any:
		all := f
		for i, p := range pages[1:] {
			var a []any
			if err := json.Unmarshal(p, &a); err != nil {
				return nil, fmt.Errorf("page %d is not an array like page 1: %w", i+2, err)
			}
			all = append(all, a...)
		}
		return json.Marshal(all)
	case map[string]any:
		for i, p := range pages[1:] {
			var m map[string]any
			if err := json.Unmarshal(p, &m); err != nil {
				return nil, fmt.Errorf("page %d is not an object like page 1: %w", i+2, err)
			}
			for k, v := range m {
				if arr, ok := v.([]any); ok {
					if cur, ok := f[k].([]any); ok {
						f[k] = append(cur, arr...)
					}
				}
			}
		}
		return json.Marshal(f)
	}
	return nil, fmt.Errorf("cannot concatenate pages of a scalar")
}

func pick(res []byte, field string) (string, error) {
	var v any
	if err := json.Unmarshal(res, &v); err != nil {
		return "", fmt.Errorf("--field: answer is not JSON: %w", err)
	}
	for _, k := range strings.Split(field, ".") {
		switch t := v.(type) {
		case map[string]any:
			x, ok := t[k]
			if !ok {
				return "", fmt.Errorf("--field %s: no member %q", field, k)
			}
			v = x
		case []any:
			i, err := strconv.Atoi(k)
			if err != nil || i < 0 || i >= len(t) {
				return "", fmt.Errorf("--field %s: bad index %q", field, k)
			}
			v = t[i]
		default:
			return "", fmt.Errorf("--field %s: %q is not inside an object or array", field, k)
		}
	}
	if s, ok := v.(string); ok {
		return s, nil
	}
	b, err := json.Marshal(v)
	return string(b), err
}
