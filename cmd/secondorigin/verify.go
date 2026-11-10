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
	"github.com/go-fleettools/fleettools/internal/ghtoken"
	"github.com/spf13/cobra"
)

// DeclFile is where a parent repository declares its second origin.
const DeclFile = ".second-origin.json"

const (
	maxDecl = 64 << 10
	maxBody = 1 << 20 // a shell is a page and a script; a megabyte is generous
	maxJS   = 8       // scripts followed from one shell
)

// apiBase is the GitHub REST API; a test replaces it.
var apiBase = "https://api.github.com"

var sleep = time.Sleep

// Decl is a parent repository's declaration of its second origin.
type Decl struct {
	Org     string   `json:"org"`
	Repo    string   `json:"repo"`
	Parents []string `json:"parents"`
	Shells  []string `json:"shells"`
	CSP     string   `json:"csp"`
}

var (
	orgRE   = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?-usercontent$`)
	hostRE  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	shellRE = regexp.MustCompile(`^/[A-Za-z0-9._/-]*$`)
)

// Origin is the second origin itself.
func (d Decl) Origin() string { return "https://" + strings.ToLower(d.Org) + ".github.io" }

// checkParent accepts a canonical origin: https, lower-case host, no path, no
// port. With local, a loopback http origin with a port, for tests.
func checkParent(o string, local bool) error {
	u, err := url.Parse(o)
	if err != nil || u.Opaque != "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("parent %q is not an origin", o)
	}
	if u.String() != o {
		return fmt.Errorf("parent %q is not written canonically (%q)", o, u.String())
	}
	h := u.Hostname()
	switch {
	case u.Scheme == "https" && u.Port() == "" && hostRE.MatchString(h):
		return nil
	case local && u.Scheme == "http" && u.Port() != "" && (h == "127.0.0.1" || h == "localhost"):
		return nil
	}
	return fmt.Errorf("parent %q: want https://host (lower case, no path, no port)", o)
}

// Validate says whether the declaration is one.
func (d Decl) Validate(local bool) error {
	if !orgRE.MatchString(d.Org) {
		return fmt.Errorf("org %q: want <org>-usercontent", d.Org)
	}
	if !strings.EqualFold(d.Repo, d.Org+".github.io") {
		return fmt.Errorf("repo %q: a second origin is the org's own Pages site, %s.github.io", d.Repo, d.Org)
	}
	if len(d.Parents) == 0 {
		return errors.New("parents: at least one parent origin")
	}
	seen := map[string]bool{}
	for _, p := range d.Parents {
		if err := checkParent(p, local); err != nil {
			return err
		}
		if seen[p] {
			return fmt.Errorf("parent %q declared twice", p)
		}
		seen[p] = true
		if p == d.Origin() {
			return fmt.Errorf("parent %q is the second origin itself: that is no isolation", p)
		}
	}
	if len(d.Shells) == 0 {
		return errors.New("shells: at least one shell path")
	}
	for _, s := range d.Shells {
		if !shellRE.MatchString(s) || strings.Contains(s, "..") {
			return fmt.Errorf("shell %q: want an absolute path like /shell.html", s)
		}
	}
	if normCSP(d.CSP) == "" {
		return errors.New("csp: the expected policy of the shell")
	}
	return nil
}

// LoadDecl reads and validates a declaration; unknown keys are an error, since
// a misspelt "parents" would otherwise declare nothing and verify nothing.
func LoadDecl(path string, local bool) (Decl, error) {
	st, err := os.Stat(path)
	if err != nil {
		return Decl{}, err
	}
	if !st.Mode().IsRegular() || st.Size() > maxDecl {
		return Decl{}, fmt.Errorf("%s: not a regular file under %d bytes", path, maxDecl)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Decl{}, err
	}
	var d Decl
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return Decl{}, fmt.Errorf("%s: %w", path, err)
	}
	if dec.More() {
		return Decl{}, fmt.Errorf("%s: trailing data", path)
	}
	if err := d.Validate(local); err != nil {
		return Decl{}, fmt.Errorf("%s: %w", path, err)
	}
	return d, nil
}

// Verdicts of one check.
type verdict int

const (
	pass verdict = iota
	fail
	unverified
)

func (v verdict) String() string { return [...]string{"PASS", "FAIL", "UNVERIFIED"}[v] }

type finding struct {
	v      verdict
	check  string
	detail string
}

type options struct {
	file      string
	local     bool
	live      string // replaces https://<org>.github.io, for a test or a preview
	tokenFile string
	retry     int
	wait      time.Duration
	timeout   time.Duration
}

func verifyCmd() *cobra.Command {
	var o options
	c := cli.New("verify [flags]", "Check the published second origin against .second-origin.json",
		"Reads "+DeclFile+" and checks the published second origin against it. Exit 0 verified, 1 drifted or gone, 2 not verified.")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return cli.Unanswerable("verify takes no operand, got %q", args[0])
		}
		return runVerify(cli.Out(cmd), cli.Err(cmd), o)
	}
	f := c.Flags()
	f.StringVar(&o.file, "file", DeclFile, "the declaration")
	f.BoolVar(&o.local, "local", false, "allow loopback http parents (tests)")
	f.StringVar(&o.live, "live-url", "", "read the site from this URL instead of https://<org>.github.io (tests, previews)")
	f.StringVar(&o.tokenFile, "token-file", "", "send the GitHub token in this file with API reads, against the rate limit (\"default\" is ~/.github-token; none by default: the reads are public)")
	f.IntVar(&o.retry, "retry", 0, "run again up to N times while the verdict is NOT VERIFIED (never while it is a FAIL)")
	f.DurationVar(&o.wait, "wait", 20*time.Second, "time between runs")
	f.DurationVar(&o.timeout, "timeout", 20*time.Second, "timeout of one request")
	return c
}

func runVerify(out, errOut io.Writer, o options) error {
	if o.retry < 0 || o.wait < 0 || o.timeout <= 0 {
		return cli.Unanswerable("--retry and --wait must not be negative, --timeout must be positive")
	}
	d, err := LoadDecl(o.file, o.local)
	if err != nil {
		return cli.Unanswerable("not verified: the declaration is unreadable: %v", err)
	}
	token := ""
	if o.tokenFile != "" {
		path := o.tokenFile
		if path == "default" {
			path = ""
		}
		if token, err = ghtoken.Read(path); err != nil {
			return cli.Unanswerable("not verified: %v", err)
		}
	}
	var fs []finding
	for attempt := 0; ; attempt++ {
		fs = verify(d, o, token)
		if code := status(fs); code != 2 || attempt >= o.retry {
			break
		}
		fmt.Fprintf(errOut, "secondorigin: not verified yet (attempt %d of %d), waiting %v\n", attempt+1, o.retry+1, o.wait)
		sleep(o.wait)
	}
	fmt.Fprintf(out, "second origin %s, parents %s\n", d.Origin(), strings.Join(d.Parents, ", "))
	for _, f := range fs {
		fmt.Fprintf(out, "%-10s %s: %s\n", f.v, f.check, f.detail)
	}
	switch status(fs) {
	case 0:
		fmt.Fprintln(out, "verified")
		return nil
	case 1:
		return cli.Found("the second origin drifted or is gone")
	}
	return cli.Unanswerable("not verified: an answer could not be read")
}

// status: 1 when anything failed, else 2 when anything is unverified, else 0.
func status(fs []finding) int {
	code := 0
	for _, f := range fs {
		switch f.v {
		case fail:
			return 1
		case unverified:
			code = 2
		}
	}
	if len(fs) == 0 {
		return 2 // nothing checked is not a pass
	}
	return code
}

// reply is one HTTP answer.
type reply struct {
	code   int
	header http.Header
	body   []byte
	err    error
}

// get reads one URL, refusing to follow a redirect to another host, and
// reading at most maxBody bytes.
func get(target string, o options, token string, bust bool) reply {
	u, err := url.Parse(target)
	if err != nil {
		return reply{err: err}
	}
	if bust {
		q := u.Query()
		q.Set("nocache", strconv.FormatInt(time.Now().UnixNano(), 10))
		u.RawQuery = q.Encode()
	}
	host := u.Host
	c := &http.Client{Timeout: o.timeout, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if r.URL.Host != host || len(via) > 5 {
			return fmt.Errorf("redirected to %s", r.URL.Host)
		}
		return nil
	}}
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return reply{err: err}
	}
	req.Header.Set("User-Agent", "secondorigin")
	if token != "" && strings.HasPrefix(target, apiBase) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return reply{err: err}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return reply{err: err}
	}
	if len(b) > maxBody {
		return reply{err: fmt.Errorf("answer larger than %d bytes", maxBody)}
	}
	return reply{code: resp.StatusCode, header: resp.Header, body: b}
}

// verify runs every check. A check that cannot read its answer is UNVERIFIED.
func verify(d Decl, o options, token string) []finding {
	var fs []finding
	add := func(v verdict, check, format string, a ...any) {
		fs = append(fs, finding{v, check, fmt.Sprintf(format, a...)})
	}

	// 1. the organisation
	r := get(apiBase+"/orgs/"+d.Org, o, token, false)
	switch {
	case r.err != nil:
		add(unverified, "org", "%v", r.err)
	case r.code == 404:
		add(fail, "org", "%s does not exist", d.Org)
	case r.code != 200:
		add(unverified, "org", "the API answered %d", r.code)
	default:
		var v struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(r.body, &v); err != nil || v.Type == "" {
			add(unverified, "org", "unreadable answer")
		} else if v.Type != "Organization" {
			add(fail, "org", "%s is a %s, not an organisation", d.Org, v.Type)
		} else {
			add(pass, "org", "%s exists", d.Org)
		}
	}

	// 2. the Pages repository
	r = get(apiBase+"/repos/"+d.Org+"/"+d.Repo, o, token, false)
	switch {
	case r.err != nil:
		add(unverified, "repo", "%v", r.err)
	case r.code == 404:
		add(fail, "repo", "%s/%s does not exist (or is private)", d.Org, d.Repo)
	case r.code != 200:
		add(unverified, "repo", "the API answered %d", r.code)
	default:
		var v struct {
			Private  *bool `json:"private"`
			Archived *bool `json:"archived"`
			HasPages *bool `json:"has_pages"`
		}
		switch err := json.Unmarshal(r.body, &v); {
		case err != nil || v.Private == nil || v.Archived == nil || v.HasPages == nil:
			add(unverified, "repo", "unreadable answer")
		case *v.Private:
			add(fail, "repo", "%s/%s is private", d.Org, d.Repo)
		case *v.Archived:
			add(fail, "repo", "%s/%s is archived", d.Org, d.Repo)
		case !*v.HasPages:
			add(fail, "repo", "%s/%s has no Pages site", d.Org, d.Repo)
		default:
			add(pass, "repo", "%s/%s is public, with Pages enabled", d.Org, d.Repo)
		}
	}

	// 3. each shell, as served
	base := d.Origin()
	if o.live != "" {
		base = strings.TrimSuffix(o.live, "/")
	}
	for _, s := range d.Shells {
		fs = append(fs, verifyShell(d, base, s, o, token)...)
	}
	return fs
}

// verifyShell reads one shell and the scripts it loads.
func verifyShell(d Decl, base, path string, o options, token string) []finding {
	var fs []finding
	name := "shell " + path
	add := func(v verdict, check, format string, a ...any) {
		fs = append(fs, finding{v, check, fmt.Sprintf(format, a...)})
	}
	r := get(base+path, o, token, true)
	switch {
	case r.err != nil:
		return append(fs, finding{unverified, name, r.err.Error()})
	case r.code == 404:
		return append(fs, finding{fail, name, "404: nothing is served there"})
	case r.code != 200:
		v := unverified
		if r.code == 403 || r.code == 410 {
			v = fail
		}
		return append(fs, finding{v, name, "answered " + strconv.Itoa(r.code)})
	}
	add(pass, name, "answers 200")

	page := parseShell(r.body)

	// CSP: the meta tag is what a Pages site can set; a header, if one is ever
	// served, must agree.
	want := normCSP(d.CSP)
	var got []string
	for _, c := range page.csp {
		got = append(got, normCSP(c))
	}
	if h := r.header.Values("Content-Security-Policy"); len(h) > 0 {
		for _, c := range h {
			got = append(got, normCSP(c))
		}
	}
	switch {
	case len(got) == 0:
		add(fail, name+" csp", "the shell sets no Content-Security-Policy")
	default:
		bad := ""
		for _, g := range got {
			if g != want {
				bad = g
			}
		}
		if bad != "" {
			add(fail, name+" csp", "served %q, declared %q", bad, want)
		} else {
			add(pass, name+" csp", "equals the declaration")
		}
	}

	// The scripts: the page's own inline ones and the same-origin files it loads.
	srcs := append([]string{}, page.inline...)
	var fetchFail bool
	if len(page.scripts) > maxJS {
		add(fail, name+" scripts", "%d external scripts, more than the %d this command follows", len(page.scripts), maxJS)
		fetchFail = true
	}
	for i, s := range page.scripts {
		if i >= maxJS {
			break
		}
		bu, _ := url.Parse(base + path)
		su, err := bu.Parse(s)
		if err != nil || su.Scheme+"://"+su.Host != bu.Scheme+"://"+bu.Host {
			add(fail, name+" scripts", "loads a script from outside the second origin: %q", s)
			fetchFail = true
			continue
		}
		sr := get(su.String(), o, token, true)
		switch {
		case sr.err != nil:
			add(unverified, name+" script "+s, "%v", sr.err)
			fetchFail = true
		case sr.code == 404:
			add(fail, name+" script "+s, "404")
			fetchFail = true
		case sr.code != 200:
			add(unverified, name+" script "+s, "answered %d", sr.code)
			fetchFail = true
		default:
			srcs = append(srcs, string(sr.body))
		}
	}
	if fetchFail {
		return fs
	}
	if len(srcs) == 0 {
		return append(fs, finding{fail, name + " origin check", "the shell has no script: nothing checks the parent's origin"})
	}

	found := map[string]bool{}
	var wild, unrendered, reads bool
	for _, src := range srcs {
		if wildcardPost.MatchString(src) {
			wild = true
		}
		if readsOrigin.MatchString(src) {
			reads = true
		}
		for _, lit := range stringLiterals(src) {
			if strings.Contains(lit, "{{") || strings.Contains(lit, "@@") {
				unrendered = true
			}
			if og, ok := originOf(lit); ok {
				found[og] = true
			}
		}
	}
	delete(found, d.Origin()) // the shell may name itself
	if o.live != "" {
		delete(found, base)
	}
	if unrendered {
		add(fail, name+" origin check", "a template placeholder was never rendered")
	}
	if wild {
		add(fail, name+" origin check", `postMessage is sent to "*"`)
	}
	if !reads {
		add(fail, name+" origin check", "no script reads event.origin")
	}
	declared := map[string]bool{}
	for _, p := range d.Parents {
		declared[p] = true
	}
	var missing, extra []string
	for p := range declared {
		if !found[p] {
			missing = append(missing, p)
		}
	}
	for p := range found {
		if !declared[p] {
			extra = append(extra, p)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	switch {
	case len(missing) > 0 || len(extra) > 0:
		add(fail, name+" parents", "missing %v, undeclared %v: the shell must name exactly %v", missing, extra, d.Parents)
	case !unrendered && !wild && reads:
		add(pass, name+" parents", "names exactly %v, no other origin, no \"*\"", d.Parents)
	}
	return fs
}
