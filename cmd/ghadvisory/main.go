// Command ghadvisory creates, lists and publishes a repository's GitHub
// security advisories (GHSA).
//
// It exists because the fleet rule is that every GitHub write goes through a
// fleet program and none did this one: `gh api repos/O/R/security-advisories`
// was the spelling left, and the body of an advisory is PROSE, which does not
// belong on a command line (see ghissue). Same transport as ghissue: the token
// comes from a 0600 file and travels only in the Authorization header, over
// https, redirects refused.
//
// References (read 2026-11-19):
//
//	https://docs.github.com/en/rest/security-advisories/repository-advisories
//	  POST  /repos/{owner}/{repo}/security-advisories  summary, description,
//	        vulnerabilities[] {package{ecosystem,name}, vulnerable_version_range,
//	        patched_versions, vulnerable_functions} are required; cve_id, cwe_ids,
//	        credits[] {login,type}, severity (critical|high|medium|low) or
//	        cvss_vector_string (not both) are optional; 201 with ghsa_id, html_url, state
//	  PATCH /repos/{owner}/{repo}/security-advisories/{ghsa_id}  "state" is
//	        published|closed|draft; 200
//	  GET   /repos/{owner}/{repo}/security-advisories  200, an array
//	https://docs.github.com/en/rest/git/refs  GET /repos/{owner}/{repo}/git/ref/tags/{tag}: 200, or 404 when absent
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
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/go-fleettools/fleettools/internal/ghtoken"
	"github.com/go-fleettools/fleettools/internal/paritytext"
	"github.com/go-fleettools/fleettools/internal/privgate"
	"github.com/spf13/cobra"
)

const (
	maxBody     = 64 << 10
	maxResponse = 4 << 20
	maxSummary  = 1024
	timeout     = 30 * time.Second
	apiBase     = "https://api.github.com"
)

const longHelp = `Creates, lists and publishes a repository's security advisories.

  ghadvisory <owner/repo> --new --summary S --body-file F --severity low|medium|high|critical
             --cwe CWE-NNN --package go:MODULE --affected "< v0.3.4" --patched v0.3.4
             [--cve-id CVE-...] [--credit login[:type]]...
  ghadvisory <owner/repo> publish GHSA-xxxx-xxxx-xxxx
  ghadvisory <owner/repo> edit GHSA-xxxx-xxxx-xxxx --body-file F [--dry-run]
  ghadvisory <owner/repo> list [--state draft|published|closed|triage]

edit REPLACES the description of a DRAFT with the file's text (private-path and
bit-parity gates as --new), after printing the advisory and a line diff of old
against new. A published advisory is refused.

--new creates a DRAFT (REST POST /repos/{owner}/{repo}/security-advisories):
nothing is public until publish, which is a separate act, because publishing
cannot be undone — a published advisory is indexed, mirrored and notified.

publish prints the advisory first, refuses unless it is a draft, and refuses
unless every patched version it names is a released TAG of that repository
(read from the API, not from memory): an advisory that says "fixed in v0.3.4"
before v0.3.4 exists sends every reader to a version they cannot install.

Versions are sent to GitHub without a leading "v" (its Go advisories read
"< 0.3.4"); the tag check puts it back. --package is ecosystem:name.
--credit takes login or login:type (default reporter; types are analyst, finder,
reporter, coordinator, remediation_developer, remediation_reviewer,
remediation_verifier, tool, sponsor, other).

The body (the advisory's "description") comes from a FILE only, passes the
private-path gate and the bit-parity gate as ghissue's does (--allow-private-path,
--parity-waiver: a reason, stamped into the published text), and the token is
read only from a file (default ~/.github-token, 0600).

Exit status: 0 done, 2 refused or failed.`

var newClient = func() *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

var readToken = ghtoken.Read

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

type opts struct {
	base, tokFile, bodyFile, summary, severity string
	pkg, affected, patched, cveID, state       string
	cwes, credits                              []string
	doNew, dryRun                              bool
	privWaiver, parityWaiver                   string
}

// RootCmd builds the command; tests drive it with SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	var o opts
	cmd := &cobra.Command{
		Use:          "ghadvisory <owner/repo> --new ... | <owner/repo> publish GHSA | <owner/repo> list",
		Short:        "Create (draft), list and publish a repository's security advisories",
		Long:         longHelp,
		Args:         cobra.RangeArgs(1, 3),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			owner, repo, err := splitRepo(args[0])
			if err != nil {
				return err
			}
			if !strings.HasPrefix(o.base, "https://") && !isTestBase(o.base) {
				return fmt.Errorf("base %q: must be https", o.base)
			}
			if o.doNew {
				if len(args) != 1 {
					return errors.New("--new takes <owner/repo> only: GitHub gives the GHSA id")
				}
				return runNew(cmd, owner, repo, o)
			}
			if len(args) < 2 {
				return errors.New("want <owner/repo> publish GHSA-..., <owner/repo> list, or --new")
			}
			switch args[1] {
			case "publish":
				if len(args) != 3 {
					return errors.New("publish wants a GHSA id: ghadvisory <owner/repo> publish GHSA-xxxx-xxxx-xxxx")
				}
				return runPublish(cmd, owner, repo, args[2], o)
			case "edit":
				if len(args) != 3 {
					return errors.New("edit wants a GHSA id: ghadvisory <owner/repo> edit GHSA-... --body-file F")
				}
				return runEdit(cmd, owner, repo, args[2], o)
			case "list":
				if len(args) != 2 {
					return errors.New("list takes no argument")
				}
				return runList(cmd, owner, repo, o)
			}
			return fmt.Errorf("verb %q: want publish or list (or --new)", args[1])
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.base, "base", apiBase, "API base URL (https)")
	f.StringVar(&o.tokFile, "token-file", "", "file holding the token (default ~/.github-token)")
	f.BoolVar(&o.doNew, "new", false, "create a DRAFT advisory")
	f.BoolVar(&o.dryRun, "dry-run", false, "print what would be sent and send nothing that writes")
	f.StringVar(&o.summary, "summary", "", "one-line summary (--new)")
	f.StringVar(&o.bodyFile, "body-file", "", "file holding the advisory description (--new)")
	f.StringVar(&o.severity, "severity", "", "low, medium, high or critical (--new)")
	f.StringArrayVar(&o.cwes, "cwe", nil, "CWE-NNN (--new; repeatable)")
	f.StringVar(&o.pkg, "package", "", "ecosystem:name, e.g. go:github.com/go-authn/krb5 (--new)")
	f.StringVar(&o.affected, "affected", "", `affected range, e.g. "< v0.3.4" (--new)`)
	f.StringVar(&o.patched, "patched", "", "first patched version, e.g. v0.3.4 (--new)")
	f.StringVar(&o.cveID, "cve-id", "", "an existing CVE id (--new)")
	f.StringArrayVar(&o.credits, "credit", nil, "login or login:type (--new; repeatable)")
	f.StringVar(&o.state, "state", "", "list: only this state")
	f.StringVar(&o.privWaiver, "allow-private-path", "", "why this text must name a private file system; STAMPED into the text")
	f.StringVar(&o.parityWaiver, "parity-waiver", "", "why this text may defer a performance change because it breaks bit identity (STAMPED)")
	return cmd
}

var (
	cweRe  = regexp.MustCompile(`^CWE-[0-9]{1,5}$`)
	cveRe  = regexp.MustCompile(`^CVE-[0-9]{4}-[0-9]{4,}$`)
	ghsaRe = regexp.MustCompile(`^GHSA(-[23456789cfghjmpqrvwx]{4}){3}$`)
	logRe  = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
)

var severities = map[string]bool{"low": true, "medium": true, "high": true, "critical": true}

var creditTypes = map[string]bool{
	"analyst": true, "finder": true, "reporter": true, "coordinator": true,
	"remediation_developer": true, "remediation_reviewer": true,
	"remediation_verifier": true, "tool": true, "sponsor": true, "other": true,
}

var ecosystems = map[string]bool{
	"rubygems": true, "npm": true, "pip": true, "maven": true, "nuget": true,
	"composer": true, "go": true, "rust": true, "erlang": true, "actions": true,
	"pub": true, "other": true, "swift": true,
}

type credit struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

type vulnPackage struct {
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
}

type vuln struct {
	Package                vulnPackage `json:"package"`
	VulnerableVersionRange string      `json:"vulnerable_version_range"`
	PatchedVersions        string      `json:"patched_versions"`
}

type request struct {
	Summary         string   `json:"summary"`
	Description     string   `json:"description"`
	CVEID           string   `json:"cve_id,omitempty"`
	CWEIDs          []string `json:"cwe_ids,omitempty"`
	Severity        string   `json:"severity"`
	Vulnerabilities []vuln   `json:"vulnerabilities"`
	Credits         []credit `json:"credits,omitempty"`
}

// build validates every flag locally, so that nothing refusable costs a request.
func build(o opts) (request, error) {
	var r request
	summary := strings.TrimSpace(o.summary)
	switch {
	case summary == "":
		return r, errors.New("--new needs --summary")
	case strings.ContainsAny(summary, "\r\n"):
		return r, errors.New("--summary: one line; the rest belongs in --body-file")
	case len([]rune(summary)) > maxSummary:
		return r, fmt.Errorf("--summary: over %d characters", maxSummary)
	}
	if err := privgate.Title(summary, o.privWaiver); err != nil {
		return r, err
	}
	if err := paritytext.GateTitle(summary, "--parity-waiver"); err != nil {
		return r, err
	}
	if o.bodyFile == "" {
		return r, errors.New("--new needs --body-file: an advisory says what, who is affected and what to do")
	}
	body, err := readBody(o.bodyFile, o.privWaiver, o.parityWaiver)
	if err != nil {
		return r, err
	}
	if !severities[o.severity] {
		return r, fmt.Errorf("--severity %q: want low, medium, high or critical", o.severity)
	}
	for _, c := range o.cwes {
		if !cweRe.MatchString(c) {
			return r, fmt.Errorf("--cwe %q: want CWE-NNN", c)
		}
	}
	if o.cveID != "" && !cveRe.MatchString(o.cveID) {
		return r, fmt.Errorf("--cve-id %q: want CVE-YYYY-NNNN", o.cveID)
	}
	eco, name, ok := strings.Cut(o.pkg, ":")
	if !ok || name == "" || !ecosystems[eco] {
		return r, fmt.Errorf("--package %q: want ecosystem:name, e.g. go:github.com/go-authn/krb5", o.pkg)
	}
	if strings.TrimSpace(o.affected) == "" || strings.TrimSpace(o.patched) == "" {
		return r, errors.New("--new needs --affected and --patched: an advisory with no fixed version tells nobody what to do")
	}
	for _, c := range o.credits {
		login, typ, _ := strings.Cut(c, ":")
		if typ == "" {
			typ = "reporter"
		}
		if !logRe.MatchString(login) || !creditTypes[typ] {
			return r, fmt.Errorf("--credit %q: want login or login:type", c)
		}
		r.Credits = append(r.Credits, credit{Login: login, Type: typ})
	}
	r.Summary, r.Description, r.Severity = summary, body, o.severity
	r.CVEID, r.CWEIDs = o.cveID, o.cwes
	r.Vulnerabilities = []vuln{{
		Package:                vulnPackage{Ecosystem: eco, Name: name},
		VulnerableVersionRange: noV(o.affected),
		PatchedVersions:        noV(o.patched),
	}}
	return r, nil
}

var vBeforeDigit = regexp.MustCompile(`\bv([0-9])`)

// noV drops the "v" before a version number: GitHub writes Go ranges without it.
func noV(s string) string { return vBeforeDigit.ReplaceAllString(strings.TrimSpace(s), "$1") }

func runNew(cmd *cobra.Command, owner, repo string, o opts) error {
	out := cmd.OutOrStdout()
	req, err := build(o)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if o.dryRun {
		fmt.Fprintf(out, "-- new DRAFT advisory on %s/%s, NOT sent --\n%s\n", owner, repo, payload)
		return nil
	}
	tok, err := readToken(o.tokFile)
	if err != nil {
		return err
	}
	c := newClient()
	b, _, err := do(c, http.MethodPost, advURL(o.base, owner, repo, ""), tok, payload, 201)
	if err != nil {
		return err
	}
	var a advisory
	if err := json.Unmarshal(b, &a); err != nil {
		return fmt.Errorf("new advisory: %w", err)
	}
	if a.GHSAID == "" || a.HTMLURL == "" {
		return errors.New("new advisory: accepted with no id or URL, so nothing proves it landed")
	}
	fmt.Fprintf(out, "created: %s [%s] %s\n", a.GHSAID, a.State, a.HTMLURL)
	if a.State != "draft" {
		return fmt.Errorf("the API reports state %q, not draft", a.State)
	}
	return nil
}

type advisory struct {
	GHSAID          string `json:"ghsa_id"`
	HTMLURL         string `json:"html_url"`
	State           string `json:"state"`
	Summary         string `json:"summary"`
	Description     string `json:"description"`
	Severity        string `json:"severity"`
	CVEID           string `json:"cve_id"`
	Vulnerabilities []struct {
		Package struct {
			Ecosystem string `json:"ecosystem"`
			Name      string `json:"name"`
		} `json:"package"`
		VulnerableVersionRange string `json:"vulnerable_version_range"`
		PatchedVersions        string `json:"patched_versions"`
	} `json:"vulnerabilities"`
}

func (a advisory) show(w io.Writer) {
	fmt.Fprintf(w, "%s [%s] %s severity=%s %s\n  %s\n", a.GHSAID, a.State, a.Summary, a.Severity, a.CVEID, a.HTMLURL)
	for _, v := range a.Vulnerabilities {
		fmt.Fprintf(w, "  %s:%s affected %q patched %q\n", v.Package.Ecosystem, v.Package.Name, v.VulnerableVersionRange, v.PatchedVersions)
	}
}

func runList(cmd *cobra.Command, owner, repo string, o opts) error {
	out := cmd.OutOrStdout()
	q := ""
	if o.state != "" {
		switch o.state {
		case "draft", "published", "closed", "triage":
		default:
			return fmt.Errorf("--state %q: want draft, published, closed or triage", o.state)
		}
		q = "?state=" + o.state
	}
	tok, err := readToken(o.tokFile)
	if err != nil {
		return err
	}
	b, _, err := do(newClient(), http.MethodGet, advURL(o.base, owner, repo, "")+q, tok, nil, 200)
	if err != nil {
		return err
	}
	var as []advisory
	if err := json.Unmarshal(b, &as); err != nil {
		return fmt.Errorf("advisories: %w", err)
	}
	if len(as) == 0 {
		fmt.Fprintf(out, "%s/%s: no advisories\n", owner, repo)
	}
	for _, a := range as {
		a.show(out)
	}
	return nil
}

// runEdit replaces the description of a DRAFT advisory. A published advisory is
// refused: it is public, mirrored and notified, and an edit there is a
// correction of something already read.
func runEdit(cmd *cobra.Command, owner, repo, id string, o opts) error {
	out := cmd.OutOrStdout()
	if !ghsaRe.MatchString(id) {
		return fmt.Errorf("%q: want GHSA-xxxx-xxxx-xxxx", id)
	}
	if o.bodyFile == "" {
		return errors.New("edit needs --body-file: the file is the NEW description")
	}
	body, err := readBody(o.bodyFile, o.privWaiver, o.parityWaiver)
	if err != nil {
		return err
	}
	tok, err := readToken(o.tokFile)
	if err != nil {
		return err
	}
	c := newClient()
	b, _, err := do(c, http.MethodGet, advURL(o.base, owner, repo, id), tok, nil, 200)
	if err != nil {
		return err
	}
	var a advisory
	if err := json.Unmarshal(b, &a); err != nil {
		return fmt.Errorf("advisory: %w", err)
	}
	if a.GHSAID != id {
		return fmt.Errorf("advisory: asked for %s, the reply is %q", id, a.GHSAID)
	}
	a.show(out)
	if a.State != "draft" {
		return fmt.Errorf("refused: %s is %s; only a draft is edited", id, a.State)
	}
	fmt.Fprintf(out, "\n-- description diff (- old, + new) --\n%s", lineDiff(a.Description, body))
	if o.dryRun {
		fmt.Fprintln(out, "NOT sent (--dry-run)")
		return nil
	}
	payload, err := json.Marshal(map[string]string{"description": body})
	if err != nil {
		return err
	}
	b, _, err = do(c, http.MethodPatch, advURL(o.base, owner, repo, id), tok, payload, 200)
	if err != nil {
		return err
	}
	var got advisory
	if err := json.Unmarshal(b, &got); err != nil {
		return fmt.Errorf("edit: %w", err)
	}
	if strings.TrimSpace(got.Description) != strings.TrimSpace(body) {
		return errors.New("edit: accepted, but the description the API reports is not the one sent")
	}
	fmt.Fprintf(out, "edited: %s [%s]\n", got.GHSAID, got.State)
	if got.State != "draft" {
		return fmt.Errorf("the API reports state %q after the edit, not draft", got.State)
	}
	return nil
}

// lineDiff is a line diff by longest common subsequence: "- " for a line only
// in old, "+ " for one only in new, "  " for a shared one.
func lineDiff(oldS, newS string) string {
	a := strings.Split(strings.TrimRight(oldS, "\n"), "\n")
	b := strings.Split(strings.TrimRight(newS, "\n"), "\n")
	l := make([][]int, len(a)+1)
	for i := range l {
		l[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				l[i][j] = l[i+1][j+1] + 1
			} else {
				l[i][j] = max(l[i+1][j], l[i][j+1])
			}
		}
	}
	var sb strings.Builder
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			sb.WriteString("  " + a[i] + "\n")
			i++
			j++
		case j < len(b) && (i == len(a) || l[i][j+1] > l[i+1][j]):
			sb.WriteString("+ " + b[j] + "\n")
			j++
		default:
			sb.WriteString("- " + a[i] + "\n")
			i++
		}
	}
	return sb.String()
}

func runPublish(cmd *cobra.Command, owner, repo, id string, o opts) error {
	out := cmd.OutOrStdout()
	if !ghsaRe.MatchString(id) {
		return fmt.Errorf("%q: want GHSA-xxxx-xxxx-xxxx", id)
	}
	tok, err := readToken(o.tokFile)
	if err != nil {
		return err
	}
	c := newClient()
	b, _, err := do(c, http.MethodGet, advURL(o.base, owner, repo, id), tok, nil, 200)
	if err != nil {
		return err
	}
	var a advisory
	if err := json.Unmarshal(b, &a); err != nil {
		return fmt.Errorf("advisory: %w", err)
	}
	if a.GHSAID != id {
		return fmt.Errorf("advisory: asked for %s, the reply is %q", id, a.GHSAID)
	}
	a.show(out)
	if a.State != "draft" && a.State != "triage" {
		return fmt.Errorf("%s is %s: only a draft or triage advisory is published", id, a.State)
	}
	if len(a.Vulnerabilities) == 0 {
		return errors.New("refused: the advisory names no vulnerability, so no patched version can be checked")
	}
	for _, v := range a.Vulnerabilities {
		pv := strings.TrimSpace(v.PatchedVersions)
		if pv == "" {
			return fmt.Errorf("refused: %s has no patched version", v.Package.Name)
		}
		for _, p := range strings.Split(pv, ",") {
			tag, err := releasedTag(c, o.base, tok, owner, repo, strings.TrimSpace(p))
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "  patched %s is the released tag %s of %s/%s\n", p, tag, owner, repo)
		}
	}
	if o.dryRun {
		fmt.Fprintln(out, "would publish it (--dry-run)")
		return nil
	}
	payload := []byte(`{"state":"published"}`)
	b, _, err = do(c, http.MethodPatch, advURL(o.base, owner, repo, id), tok, payload, 200)
	if err != nil {
		return err
	}
	var got advisory
	if err := json.Unmarshal(b, &got); err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	fmt.Fprintf(out, "state: %s\n", got.State)
	if got.State != "published" {
		return fmt.Errorf("the API reports state %q after the PATCH, not published", got.State)
	}
	return nil
}

// releasedTag looks for the tag a patched version names, with and without the
// "v" Go tags carry, and refuses when neither exists.
func releasedTag(c *http.Client, base, tok, owner, repo, ver string) (string, error) {
	ver = strings.TrimLeft(strings.TrimSpace(ver), "=> ")
	if ver == "" {
		return "", errors.New("refused: an empty patched version")
	}
	cands := []string{ver}
	if strings.HasPrefix(ver, "v") {
		cands = append(cands, strings.TrimPrefix(ver, "v"))
	} else {
		cands = []string{"v" + ver, ver}
	}
	for _, t := range cands {
		u := fmt.Sprintf("%s/repos/%s/%s/git/ref/tags/%s", strings.TrimRight(base, "/"), owner, repo, url.PathEscape(t))
		_, code, err := do(c, http.MethodGet, u, tok, nil, 200, 404)
		if err != nil {
			return "", err
		}
		if code == 200 {
			return t, nil
		}
	}
	return "", fmt.Errorf("refused: patched version %q is not a released tag of %s/%s (tried %s); publish after the release", ver, owner, repo, strings.Join(cands, ", "))
}

func advURL(base, owner, repo, id string) string {
	u := fmt.Sprintf("%s/repos/%s/%s/security-advisories", strings.TrimRight(base, "/"), owner, repo)
	if id != "" {
		u += "/" + id
	}
	return u
}

func splitRepo(s string) (owner, repo string, err error) {
	owner, repo, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", fmt.Errorf("repository %q: want owner/repo", s)
	}
	return owner, repo, nil
}

// readBody reads the description, refusing an empty one, a private path and a
// bit-parity deferral, through the same gates ghissue applies.
func readBody(path, priv, parity string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("body file: %w", err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxBody+1))
	if err != nil {
		return "", fmt.Errorf("body file: %w", err)
	}
	if len(b) > maxBody {
		return "", fmt.Errorf("body file: over %d bytes", maxBody)
	}
	if strings.TrimSpace(string(b)) == "" {
		return "", errors.New("body file: empty")
	}
	s, err := privgate.Body(string(b), priv)
	if err != nil {
		return "", err
	}
	return paritytext.Gate(s, parity, "--parity-waiver")
}

// do sends one request and insists on one of the statuses the caller expects.
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
	return nil, resp.StatusCode, fmt.Errorf("%s %s: %s: %s", method, u, resp.Status, firstLine(string(b)))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return strings.TrimSpace(s)
}

func isTestBase(base string) bool {
	return strings.HasPrefix(base, "http://127.0.0.1:") || strings.HasPrefix(base, "http://[::1]:")
}
