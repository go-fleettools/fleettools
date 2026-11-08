// Command ghrel writes to a GitHub release that ALREADY EXISTS: edit its title
// and notes, upload assets, or delete the release (never its tag).
//
// It exists because the fleet's guard refuses a hand-written `gh release
// edit|upload|delete` and asks for a program, and the only release program,
// gitsafe's ghrelease, creates releases and nothing else. Release CREATION
// stays there: `ghrelease --release-only [owner/repo] <tag>` creates the
// release of an existing tag (its --help: "create the GitHub release of an
// EXISTING tag, and nothing else"). ghrel does not duplicate it.
//
// Same model as ghissue: the token is read from a 0600 file and sent only in
// the Authorization header; https only (http for 127.0.0.1 test servers);
// redirects are refused; responses are bounded; the release is shown before
// anything is sent; what is printed after is what the API reports.
//
// References (read 2026-11-08):
//
//	https://docs.github.com/en/rest/releases/releases  GET /repos/{o}/{r}/releases/tags/{tag} (200|404, published releases only);
//	                                                   PATCH /repos/{o}/{r}/releases/{id}, optional "name" and "body", 200;
//	                                                   DELETE /repos/{o}/{r}/releases/{id}, 204 (the page does not say what happens to the tag:
//	                                                   ghrel never calls a tag endpoint and says so); "upload_url" is the upload endpoint
//	https://docs.github.com/en/rest/releases/assets    POST {upload_url}?name=&label=, host uploads.github.com, raw body, Content-Type required,
//	                                                   201, reply has "name", "size", "state"; a duplicate file name is 422 (delete the
//	                                                   old asset first: DELETE /repos/{o}/{r}/releases/assets/{id}, 204); GitHub may
//	                                                   rename a file with special characters; a 502 may leave an empty asset in state "starter"
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const (
	maxNotes    = 125000  // bytes read from the notes file (GitHub's release body limit, in characters)
	maxTitle    = 256     // characters
	maxAsset    = 2 << 30 // GitHub's per-asset limit: 2 GiB
	maxResponse = 1 << 20 // bytes read from any response
	maxTokFile  = 4 << 10
	timeout     = 30 * time.Second
	uploadTime  = 30 * time.Minute
	apiBase     = "https://api.github.com"
	uploadHost  = "uploads.github.com"
)

const longHelp = `Writes to a GitHub release that already exists.

  ghrel <owner/repo> <tag> edit   [--title T] [--notes-file F]
  ghrel <owner/repo> <tag> upload <file>... [--clobber]
  ghrel <owner/repo> <tag> delete --confirm <tag>

It shows the release first (id, title, state, assets) and prints what the API
reports afterwards. --dry-run sends only reads.

CREATING a release is not here: use gitsafe's
  ghrelease --release-only [owner/repo] <tag>
which creates the release of an existing tag. ghrel only acts on a release
that is already published; a draft is not found by tag.

edit    replaces the title and/or the notes (the notes file IS the new body).
upload  streams each file to the uploads host named by the release's own
        upload_url (https://uploads.github.com only). A file whose name is
        already an asset is REFUSED unless --clobber, which deletes that
        asset first (so a failed upload leaves the name empty, and the output
        says so). The reply's name, state and size are checked against the
        file. Files over 2 GiB, empty, or not regular are refused.
delete  deletes the RELEASE, never the git tag: no tag endpoint is called, and
        the tag stays. --confirm must repeat the tag exactly.

The token is read only from a file (default ~/.github-token, owner-only).

Exit status: 0 done, 2 refused or failed.`

// newClient is replaced by tests. Redirects are never followed: a redirect
// would carry the token header wherever it points.
var newClient = func(d time.Duration) *http.Client {
	return &http.Client{
		Timeout: d,
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
	base, tokFile, notesFile, title, confirm string
	dryRun, clobber                          bool
}

// RootCmd builds the command; tests drive it with SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	var o opts
	cmd := &cobra.Command{
		Use:          "ghrel <owner/repo> <tag> edit|upload|delete [flags] [file...]",
		Short:        "Edit, upload assets to, or delete an existing GitHub release",
		Long:         longHelp,
		Args:         cobra.MinimumNArgs(3),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, args, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.base, "base", apiBase, "API base URL (https)")
	f.StringVar(&o.tokFile, "token-file", "", "file holding the token (default ~/.github-token)")
	f.StringVar(&o.title, "title", "", "edit: the new release title")
	f.StringVar(&o.notesFile, "notes-file", "", "edit: file holding the new release notes (replaces them)")
	f.BoolVar(&o.clobber, "clobber", false, "upload: replace an asset of the same name (deletes it first)")
	f.StringVar(&o.confirm, "confirm", "", "delete: repeat the tag to confirm")
	f.BoolVar(&o.dryRun, "dry-run", false, "send only reads; print what would be written")
	return cmd
}

type release struct {
	ID         int64   `json:"id"`
	TagName    string  `json:"tag_name"`
	Name       string  `json:"name"`
	Body       string  `json:"body"`
	HTMLURL    string  `json:"html_url"`
	UploadURL  string  `json:"upload_url"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []asset `json:"assets"`
}

type asset struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	State string `json:"state"`
}

func run(cmd *cobra.Command, args []string, o opts) error {
	out := cmd.OutOrStdout()
	owner, repo, err := splitRepo(args[0])
	if err != nil {
		return err
	}
	tag := strings.TrimSpace(args[1])
	if tag == "" || strings.ContainsAny(tag, "/ \r\n?#") {
		return fmt.Errorf("tag %q: want a plain tag name", args[1])
	}
	verb, rest := args[2], args[3:]

	// Refuse locally, before the token is read.
	if err := checkFlags(verb, rest, tag, o); err != nil {
		return err
	}
	var title, notes string
	var files []upFile
	switch verb {
	case "edit":
		if o.title != "" {
			if title, err = checkTitle(o.title); err != nil {
				return err
			}
		}
		if o.notesFile != "" {
			if notes, err = readNotes(o.notesFile); err != nil {
				return err
			}
		}
	case "upload":
		if files, err = checkFiles(rest); err != nil {
			return err
		}
	}
	if !strings.HasPrefix(o.base, "https://") && !isTestBase(o.base) {
		return fmt.Errorf("base %q: must be https", o.base)
	}
	tok, err := readToken(o.tokFile)
	if err != nil {
		return err
	}
	c := newClient(timeout)

	rel, err := getRelease(c, o.base, tok, owner, repo, tag)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s/%s %s  id %d  %q%s\n  %s\n", owner, repo, rel.TagName, rel.ID, rel.Name, flagsOf(rel), rel.HTMLURL)
	for _, a := range rel.Assets {
		fmt.Fprintf(out, "  asset %d  %s  %d bytes  [%s]\n", a.ID, a.Name, a.Size, a.State)
	}

	switch verb {
	case "edit":
		return doEdit(out, c, o, tok, owner, repo, rel, title, notes)
	case "upload":
		return doUpload(out, o, tok, owner, repo, rel, files)
	default:
		return doDelete(out, c, o, tok, owner, repo, rel)
	}
}

func checkFlags(verb string, rest []string, tag string, o opts) error {
	switch verb {
	case "edit", "upload", "delete":
	default:
		return fmt.Errorf("verb %q: want edit, upload or delete (to CREATE a release use: ghrelease --release-only [owner/repo] <tag>)", verb)
	}
	if verb != "edit" && (o.title != "" || o.notesFile != "") {
		return errors.New("--title and --notes-file apply to edit")
	}
	if verb != "upload" && o.clobber {
		return errors.New("--clobber applies to upload")
	}
	if verb != "delete" && o.confirm != "" {
		return errors.New("--confirm applies to delete")
	}
	if verb != "upload" && len(rest) > 0 {
		return fmt.Errorf("%s takes no file arguments: %v", verb, rest)
	}
	switch verb {
	case "edit":
		if o.title == "" && o.notesFile == "" {
			return errors.New("nothing to edit: give --title or --notes-file")
		}
	case "upload":
		if len(rest) == 0 {
			return errors.New("upload: give at least one file")
		}
	case "delete":
		if o.confirm != tag {
			return fmt.Errorf("delete removes the release, not the tag; repeat the tag to confirm: --confirm %s", tag)
		}
	}
	return nil
}

func flagsOf(r release) string {
	s := ""
	if r.Draft {
		s += " [draft]"
	}
	if r.Prerelease {
		s += " [prerelease]"
	}
	return s
}

func doEdit(out io.Writer, c *http.Client, o opts, tok, owner, repo string, rel release, title, notes string) error {
	if o.dryRun {
		if title != "" {
			fmt.Fprintf(out, "\n-- would set title: %s\n", title)
		}
		if notes != "" {
			fmt.Fprintf(out, "\n-- would REPLACE the notes (%d bytes; now %d), NOT sent --\n%s\n", len(notes), len(rel.Body), notes)
		}
		return nil
	}
	m := map[string]string{}
	if title != "" {
		m["name"] = title
	}
	if notes != "" {
		m["body"] = notes
	}
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	b, err := do(c, http.MethodPatch, relURL(o.base, owner, repo, rel.ID), tok, payload, http.StatusOK)
	if err != nil {
		return err
	}
	var got release
	if err := json.Unmarshal(b, &got); err != nil {
		return fmt.Errorf("edit: %w", err)
	}
	if got.ID != rel.ID {
		return errors.New("edit: the reply is not this release, so nothing proves it landed")
	}
	if title != "" && got.Name != title {
		return fmt.Errorf("edit: asked for title %q, the API reports %q", title, got.Name)
	}
	if notes != "" && norm(got.Body) != norm(notes) {
		return fmt.Errorf("edit: the API reports %d bytes of notes, differing from the %d sent", len(got.Body), len(notes))
	}
	fmt.Fprintf(out, "edited: title %q, notes %d bytes (as the API reports)\n", got.Name, len(got.Body))
	return nil
}

func norm(s string) string { return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n")) }

type upFile struct {
	path, name, ctype string
	size              int64
}

// checkFiles refuses, before any request, what GitHub would refuse after a
// long upload: empty, over-size, non-regular files and repeated names.
func checkFiles(paths []string) ([]upFile, error) {
	var fs []upFile
	seen := map[string]bool{}
	for _, p := range paths {
		st, err := os.Lstat(p)
		if err != nil {
			return nil, fmt.Errorf("upload: %w", err)
		}
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("upload %s: not a regular file", p)
		}
		if st.Size() == 0 {
			return nil, fmt.Errorf("upload %s: empty", p)
		}
		if st.Size() > maxAsset {
			return nil, fmt.Errorf("upload %s: %d bytes, over GitHub's %d limit", p, st.Size(), int64(maxAsset))
		}
		name := filepath.Base(p)
		if seen[name] {
			return nil, fmt.Errorf("upload: two files are named %q", name)
		}
		seen[name] = true
		ct := mime.TypeByExtension(filepath.Ext(name))
		if ct == "" {
			ct = "application/octet-stream"
		}
		fs = append(fs, upFile{path: p, name: name, ctype: ct, size: st.Size()})
	}
	return fs, nil
}

func doUpload(out io.Writer, o opts, tok, owner, repo string, rel release, files []upFile) error {
	upBase, err := uploadBase(rel.UploadURL, o.base)
	if err != nil {
		return err
	}
	existing := map[string]asset{}
	for _, a := range rel.Assets {
		existing[a.Name] = a
	}
	for _, f := range files {
		if a, ok := existing[f.name]; ok && !o.clobber {
			return fmt.Errorf("upload: %s already has asset %d named %q; --clobber deletes it first", rel.TagName, a.ID, f.name)
		}
	}
	if o.dryRun {
		for _, f := range files {
			if a, ok := existing[f.name]; ok {
				fmt.Fprintf(out, "would DELETE asset %d (%s), then upload %s (%d bytes, %s)\n", a.ID, a.Name, f.name, f.size, f.ctype)
			} else {
				fmt.Fprintf(out, "would upload %s (%d bytes, %s) to %s\n", f.name, f.size, f.ctype, upBase)
			}
		}
		return nil
	}
	api := newClient(timeout)
	up := newClient(uploadTime)
	for _, f := range files {
		if a, ok := existing[f.name]; ok {
			if _, err := do(api, http.MethodDelete, fmt.Sprintf("%s/repos/%s/%s/releases/assets/%d", strings.TrimRight(o.base, "/"), owner, repo, a.ID), tok, nil, http.StatusNoContent); err != nil {
				return err
			}
			fmt.Fprintf(out, "clobber: deleted asset %d (%s, %d bytes); the name is empty until the upload below succeeds\n", a.ID, a.Name, a.Size)
		}
		got, err := uploadOne(up, upBase, tok, f)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "uploaded: asset %d  %s  %d bytes  [%s]\n", got.ID, got.Name, got.Size, got.State)
	}
	return nil
}

// uploadBase turns the release's upload_url template into a base URL and
// insists it is https://uploads.github.com: the token must not be sent to
// whatever host a reply names. A test server is accepted only when it is the
// very origin of --base.
func uploadBase(tmpl, apiBase string) (string, error) {
	if i := strings.IndexByte(tmpl, '{'); i >= 0 {
		tmpl = tmpl[:i]
	}
	u, err := url.Parse(tmpl)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("upload_url %q: not a URL", tmpl)
	}
	switch {
	case u.Scheme == "https" && u.Host == uploadHost:
	case isTestBase(apiBase) && isTestBase(u.Scheme+"://"+u.Host+"/") && strings.HasPrefix(apiBase, u.Scheme+"://"+u.Host):
	default:
		return "", fmt.Errorf("upload_url %q: the token goes only to https://%s", tmpl, uploadHost)
	}
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

func uploadOne(c *http.Client, upBase, tok string, f upFile) (asset, error) {
	var got asset
	fh, err := os.Open(f.path)
	if err != nil {
		return got, err
	}
	defer fh.Close()
	u := upBase + "?" + url.Values{"name": {f.name}}.Encode()
	req, err := http.NewRequest(http.MethodPost, u, fh) // streamed, never read into memory
	if err != nil {
		return got, err
	}
	req.ContentLength = f.size
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", f.ctype)
	resp, err := c.Do(req)
	if err != nil {
		return got, fmt.Errorf("upload %s: %w", f.name, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return got, err
	}
	if resp.StatusCode != http.StatusCreated {
		return got, fmt.Errorf("POST %s: %s: %s", upBase, resp.Status, firstLine(string(b)))
	}
	if err := json.Unmarshal(b, &got); err != nil {
		return got, fmt.Errorf("upload %s: %w", f.name, err)
	}
	if got.Size != f.size {
		return got, fmt.Errorf("upload %s: sent %d bytes, the API reports %d (asset %d, state %q): delete it and retry", f.name, f.size, got.Size, got.ID, got.State)
	}
	if got.State != "uploaded" {
		return got, fmt.Errorf("upload %s: asset %d is in state %q, not \"uploaded\"", f.name, got.ID, got.State)
	}
	if got.Name != f.name {
		fmt.Fprintf(os.Stderr, "note: GitHub renamed %q to %q\n", f.name, got.Name)
	}
	return got, nil
}

func doDelete(out io.Writer, c *http.Client, o opts, tok, owner, repo string, rel release) error {
	if o.dryRun {
		fmt.Fprintf(out, "would delete release %d (%s) and its %d asset(s); the tag %s is NOT touched\n", rel.ID, rel.TagName, len(rel.Assets), rel.TagName)
		return nil
	}
	if _, err := do(c, http.MethodDelete, relURL(o.base, owner, repo, rel.ID), tok, nil, http.StatusNoContent); err != nil {
		return err
	}
	// Ask again: a 204 that did nothing looks like one that worked.
	if _, err := getRelease(c, o.base, tok, owner, repo, rel.TagName); err == nil {
		return fmt.Errorf("delete: accepted, but release %s is still there", rel.TagName)
	} else if !strings.Contains(err.Error(), "404") {
		return fmt.Errorf("delete: accepted; checking it is gone failed: %w", err)
	}
	fmt.Fprintf(out, "deleted release %s (id %d); it is gone. The git tag %s was NOT touched.\n", rel.TagName, rel.ID, rel.TagName)
	return nil
}

func relURL(base, owner, repo string, id int64) string {
	return fmt.Sprintf("%s/repos/%s/%s/releases/%d", strings.TrimRight(base, "/"), owner, repo, id)
}

func getRelease(c *http.Client, base, tok, owner, repo, tag string) (release, error) {
	var r release
	u := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", strings.TrimRight(base, "/"), owner, repo, url.PathEscape(tag))
	b, err := do(c, http.MethodGet, u, tok, nil, http.StatusOK)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("release: %w", err)
	}
	if r.ID == 0 {
		return r, errors.New("release: the reply carried no id")
	}
	return r, nil
}

func checkTitle(t string) (string, error) {
	t = strings.TrimSpace(t)
	switch {
	case t == "":
		return "", errors.New("--title: empty")
	case strings.ContainsAny(t, "\r\n"):
		return "", errors.New("--title: one line; the rest belongs in --notes-file")
	case len([]rune(t)) > maxTitle:
		return "", fmt.Errorf("--title: over %d characters", maxTitle)
	}
	return t, nil
}

func splitRepo(s string) (owner, repo string, err error) {
	owner, repo, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", fmt.Errorf("repository %q: want owner/repo", s)
	}
	return owner, repo, nil
}

func readNotes(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("notes file: %w", err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxNotes+1))
	if err != nil {
		return "", fmt.Errorf("notes file: %w", err)
	}
	if len(b) > maxNotes {
		return "", fmt.Errorf("notes file: over %d bytes", maxNotes)
	}
	if strings.TrimSpace(string(b)) == "" {
		return "", errors.New("notes file: empty")
	}
	return string(b), nil
}

// readToken reads the token from a file that only its owner may read.
func readToken(path string) (string, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, ".github-token")
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("token file %s: not a regular file", path)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("token file %s: mode %04o, want owner-only (0600)", path, st.Mode().Perm())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	if len(b) > maxTokFile {
		return "", fmt.Errorf("token file %s: over %d bytes", path, maxTokFile)
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", fmt.Errorf("token file %s: empty", path)
	}
	return tok, nil
}

// do sends one API request and insists on the status the caller expects; the
// body of a failure is quoted because GitHub says why in it.
func do(c *http.Client, method, u, tok string, payload []byte, want int) ([]byte, error) {
	var rdr io.Reader
	if payload != nil {
		rdr = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, u, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != want {
		return nil, fmt.Errorf("%s %s: %s: %s", method, u, resp.Status, firstLine(string(b)))
	}
	return b, nil
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

// isTestBase allows an httptest server's http:// base, and nothing else.
func isTestBase(base string) bool {
	return strings.HasPrefix(base, "http://127.0.0.1:") || strings.HasPrefix(base, "http://[::1]:")
}
