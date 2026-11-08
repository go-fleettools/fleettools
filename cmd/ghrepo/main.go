// Command ghrepo does the repository-level GitHub writes sessions used to type
// with `gh`: fork, edit settings (description, homepage, topics), rename,
// archive, delete, sync a fork with its upstream, and set an Actions secret.
// Creating a repository stays with ghnew.
//
// Every verb shows the repository first (full name, archived, fork and parent,
// default branch, URL) and reports the state the API returns afterwards, not
// the one asked for. The destructive verbs (archive, delete, rename) need
// --confirm repeating owner/repo exactly; there is no default.
//
// The token is read from a 0600 file and sent only in the Authorization header,
// over https, with redirects refused. A secret's value comes only from a 0600
// file or stdin, is sealed with the repository's Actions public key and is
// never printed.
//
// References (read 2026-11-08):
//
//	https://docs.github.com/en/rest/repos/forks            POST /repos/{o}/{r}/forks, body organization, name; 202, asynchronous
//	https://docs.github.com/en/rest/repos/repos            PATCH /repos/{o}/{r} (name, description, homepage, archived; 200, 307);
//	                                                       DELETE /repos/{o}/{r} (204, needs delete_repo scope; 403/409/307);
//	                                                       PUT /repos/{o}/{r}/topics, names array REPLACES the set (200)
//	https://docs.github.com/en/rest/branches/branches      POST /repos/{o}/{r}/merge-upstream, body branch; 200 merge_type, 409 conflict, 422
//	https://docs.github.com/en/rest/actions/secrets        GET .../actions/secrets/public-key (key_id, key);
//	                                                       PUT .../actions/secrets/{name}, encrypted_value + key_id; 201 created, 204 updated
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/nacl/box"
)

const (
	maxDesc     = 64 << 10
	maxResponse = 1 << 20
	maxTokFile  = 4 << 10
	maxSecret   = 48 << 10 // GitHub's limit on a secret's size
	timeout     = 30 * time.Second
	apiBase     = "https://api.github.com"
)

const longHelp = `Repository-level GitHub writes, each after showing the repository.

  ghrepo <owner/repo> fork [--org O] [--name N]
  ghrepo <owner/repo> edit [--description-file F | --description D] [--homepage U] [--topic T ...]
  ghrepo <owner/repo> rename <new-name> --confirm <owner/repo>
  ghrepo <owner/repo> archive --confirm <owner/repo>
  ghrepo <owner/repo> delete --confirm <owner/repo>
  ghrepo <owner/repo> sync [--branch B]
  ghrepo <owner/repo> secret-set <NAME> (--value-file F | --value-stdin)

--topic REPLACES the whole topic set (GitHub's endpoint does); repeat it for
several, and run edit without it to leave topics alone.
archive, delete and rename refuse without --confirm repeating owner/repo
exactly. A secret's value comes only from a file that only its owner may read,
or from stdin; one trailing newline is removed. It is sealed with the
repository's Actions public key (libsodium sealed box) and never printed.
--dry-run sends only reads. The token is read from a file (default
~/.github-token, mode 0600), sent only in the Authorization header.

Exit status: 0 done, 2 refused or failed.`

// newClient is replaced by tests. Redirects are never followed: they would
// carry the token header wherever they point.
var newClient = func() *http.Client {
	return &http.Client{
		Timeout: timeout,
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
	base, tokFile, confirm, org, name, desc, descFile, homepage, branch, valueFile string
	topics                                                                         []string
	dryRun, valueStdin                                                             bool
	descSet, homeSet, topicSet                                                     bool
}

// RootCmd builds the command; tests drive it with SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	var o opts
	cmd := &cobra.Command{
		Use:          "ghrepo <owner/repo> <fork|edit|rename|archive|delete|sync|secret-set> [arg]",
		Short:        "Fork, edit, rename, archive, delete, sync a GitHub repository, or set an Actions secret",
		Long:         longHelp,
		Args:         cobra.RangeArgs(2, 3),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fl := cmd.Flags()
			o.descSet = fl.Changed("description")
			o.homeSet = fl.Changed("homepage")
			o.topicSet = fl.Changed("topic")
			return run(cmd, args, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.base, "base", apiBase, "API base URL (https)")
	f.StringVar(&o.tokFile, "token-file", "", "file holding the token (default ~/.github-token)")
	f.BoolVar(&o.dryRun, "dry-run", false, "send only reads")
	f.StringVar(&o.confirm, "confirm", "", "owner/repo repeated exactly (archive, delete, rename)")
	f.StringVar(&o.org, "org", "", "fork into this organization")
	f.StringVar(&o.name, "name", "", "name of the fork")
	f.StringVar(&o.desc, "description", "", "new description")
	f.StringVar(&o.descFile, "description-file", "", "file holding the new description")
	f.StringVar(&o.homepage, "homepage", "", "new homepage URL (empty clears it)")
	f.StringArrayVar(&o.topics, "topic", nil, "topic; repeat; REPLACES the whole set")
	f.StringVar(&o.branch, "branch", "", "branch to sync (default: the default branch)")
	f.StringVar(&o.valueFile, "value-file", "", "file (mode 0600) holding the secret value")
	f.BoolVar(&o.valueStdin, "value-stdin", false, "read the secret value from stdin")
	return cmd
}

type repoInfo struct {
	FullName      string   `json:"full_name"`
	HTMLURL       string   `json:"html_url"`
	Archived      bool     `json:"archived"`
	Fork          bool     `json:"fork"`
	DefaultBranch string   `json:"default_branch"`
	Description   string   `json:"description"`
	Homepage      string   `json:"homepage"`
	Topics        []string `json:"topics"`
	Parent        *struct {
		FullName string `json:"full_name"`
	} `json:"parent"`
}

func (r repoInfo) show() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s\n", r.FullName, r.HTMLURL)
	fmt.Fprintf(&b, "  archived=%t fork=%t default_branch=%s", r.Archived, r.Fork, r.DefaultBranch)
	if r.Parent != nil {
		fmt.Fprintf(&b, " parent=%s", r.Parent.FullName)
	}
	b.WriteByte('\n')
	return b.String()
}

var secretName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func run(cmd *cobra.Command, args []string, o opts) error {
	out := cmd.OutOrStdout()
	owner, repo, err := splitRepo(args[0])
	if err != nil {
		return err
	}
	verb := args[1]
	var arg string
	if len(args) == 3 {
		arg = args[2]
	}
	if !strings.HasPrefix(o.base, "https://") && !isTestBase(o.base) {
		return fmt.Errorf("base %q: must be https", o.base)
	}

	// Everything refusable locally is refused before the token is read.
	var desc string
	switch verb {
	case "fork", "sync", "archive", "delete":
		if arg != "" {
			return fmt.Errorf("%s takes no argument, got %q", verb, arg)
		}
	case "rename":
		if arg == "" || strings.ContainsAny(arg, "/ \t\r\n") {
			return errors.New("rename: want one <new-name> (no slash, no spaces)")
		}
	case "edit":
		if arg != "" {
			return fmt.Errorf("edit takes no argument, got %q", arg)
		}
		if o.descSet && o.descFile != "" {
			return errors.New("--description and --description-file are exclusive")
		}
		if o.descFile != "" {
			if desc, err = readText(o.descFile, maxDesc); err != nil {
				return fmt.Errorf("description file: %w", err)
			}
			desc = strings.TrimRight(desc, "\r\n")
			o.descSet = true
		} else {
			desc = o.desc
		}
		if !o.descSet && !o.homeSet && !o.topicSet {
			return errors.New("edit: nothing to change; give --description, --description-file, --homepage or --topic")
		}
		for _, t := range o.topics {
			if !topicOK(t) {
				return fmt.Errorf("--topic %q: lowercase letters, digits and hyphens, at most 50 characters", t)
			}
		}
	case "secret-set":
		if !secretName.MatchString(arg) || strings.HasPrefix(strings.ToUpper(arg), "GITHUB_") {
			return fmt.Errorf("secret name %q: letters, digits and underscores, not starting with a digit or GITHUB_", arg)
		}
	default:
		return fmt.Errorf("unknown verb %q: want fork, edit, rename, archive, delete, sync or secret-set", verb)
	}
	if o.org != "" || o.name != "" {
		if verb != "fork" {
			return errors.New("--org and --name apply to fork only")
		}
	}
	if verb == "fork" && o.name != "" && strings.ContainsAny(o.name, "/ \t\r\n") {
		return fmt.Errorf("--name %q: no slash, no spaces", o.name)
	}
	if verb == "archive" || verb == "delete" || verb == "rename" {
		if o.confirm != args[0] {
			return fmt.Errorf("%s needs --confirm %s, repeated exactly", verb, args[0])
		}
	} else if o.confirm != "" {
		return fmt.Errorf("--confirm applies to archive, delete and rename, not %s", verb)
	}
	var secret []byte
	if verb == "secret-set" {
		if (o.valueFile == "") == !o.valueStdin {
			return errors.New("secret-set: give exactly one of --value-file and --value-stdin")
		}
		if o.valueFile != "" {
			secret, err = readSecretFile(o.valueFile)
		} else {
			secret, err = readSecretReader(cmd.InOrStdin())
		}
		if err != nil {
			return err
		}
	}

	tok, err := readToken(o.tokFile)
	if err != nil {
		return err
	}
	c := newClient()
	r, err := getRepo(c, o.base, tok, owner, repo)
	if err != nil {
		return err
	}
	fmt.Fprint(out, r.show())

	repoURL := fmt.Sprintf("%s/repos/%s/%s", strings.TrimRight(o.base, "/"), owner, repo)
	switch verb {
	case "fork":
		return doFork(out, c, repoURL, tok, o)
	case "edit":
		return doEdit(out, c, repoURL, tok, r, o, desc)
	case "rename":
		return doRename(out, c, repoURL, tok, r, o, arg)
	case "archive":
		if r.Archived {
			return fmt.Errorf("%s is already archived", r.FullName)
		}
		if o.dryRun {
			fmt.Fprintln(out, "would archive it")
			return nil
		}
		got, err := patchRepo(c, repoURL, tok, map[string]any{"archived": true})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "archived: %t  %s\n", got.Archived, got.FullName)
		return nil
	case "delete":
		return doDelete(out, c, repoURL, tok, r, o)
	case "sync":
		return doSync(out, c, repoURL, tok, r, o)
	default:
		return doSecret(out, c, repoURL, tok, r, o, arg, secret)
	}
}

func doFork(out io.Writer, c *http.Client, repoURL, tok string, o opts) error {
	m := map[string]string{}
	if o.org != "" {
		m["organization"] = o.org
	}
	if o.name != "" {
		m["name"] = o.name
	}
	if o.dryRun {
		fmt.Fprintf(out, "would fork it (organization %q, name %q)\n", o.org, o.name)
		return nil
	}
	payload, _ := json.Marshal(m)
	_, b, err := do(c, http.MethodPost, repoURL+"/forks", tok, payload, http.StatusAccepted)
	if err != nil {
		return err
	}
	var got repoInfo
	if err := json.Unmarshal(b, &got); err != nil || got.FullName == "" {
		return errors.New("fork: accepted, but the reply carried no repository, so nothing proves it exists")
	}
	fmt.Fprintf(out, "forked (asynchronous: objects may take a moment): %s  %s\n", got.FullName, got.HTMLURL)
	return nil
}

func doEdit(out io.Writer, c *http.Client, repoURL, tok string, r repoInfo, o opts, desc string) error {
	m := map[string]any{}
	if o.descSet {
		m["description"] = desc
	}
	if o.homeSet {
		m["homepage"] = o.homepage
	}
	if o.dryRun {
		if len(m) > 0 {
			fmt.Fprintf(out, "would set %s\n", describeKeys(m))
		}
		if o.topicSet {
			fmt.Fprintf(out, "would REPLACE topics %v with %v\n", r.Topics, nonNil(o.topics))
		}
		return nil
	}
	if len(m) > 0 {
		got, err := patchRepo(c, repoURL, tok, m)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "description: %q\nhomepage: %q\n", got.Description, got.Homepage)
	}
	if o.topicSet {
		payload, _ := json.Marshal(map[string][]string{"names": nonNil(o.topics)})
		_, b, err := do(c, http.MethodPut, repoURL+"/topics", tok, payload, http.StatusOK)
		if err != nil {
			return err
		}
		var got struct {
			Names []string `json:"names"`
		}
		if err := json.Unmarshal(b, &got); err != nil {
			return fmt.Errorf("topics: %w", err)
		}
		fmt.Fprintf(out, "topics: %v\n", nonNil(got.Names))
	}
	return nil
}

func doRename(out io.Writer, c *http.Client, repoURL, tok string, r repoInfo, o opts, newName string) error {
	if o.dryRun {
		fmt.Fprintf(out, "would rename it to %q\n", newName)
		return nil
	}
	got, err := patchRepo(c, repoURL, tok, map[string]any{"name": newName})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "renamed: %s  %s\n", got.FullName, got.HTMLURL)
	return nil
}

func doDelete(out io.Writer, c *http.Client, repoURL, tok string, r repoInfo, o opts) error {
	if o.dryRun {
		fmt.Fprintln(out, "would DELETE it, irreversibly")
		return nil
	}
	if _, _, err := do(c, http.MethodDelete, repoURL, tok, nil, http.StatusNoContent); err != nil {
		return err
	}
	// 204 is the API's word; ask again, and say what it answers.
	st, _, err := do(c, http.MethodGet, repoURL, tok, nil, http.StatusNotFound, http.StatusOK, http.StatusMovedPermanently)
	switch {
	case err != nil:
		fmt.Fprintf(out, "deleted (204); could not confirm: %v\n", err)
	case st == http.StatusNotFound:
		fmt.Fprintf(out, "deleted: %s (204, and the repository now answers 404)\n", r.FullName)
	default:
		fmt.Fprintf(out, "deleted (204) but the repository still answers %d: check it\n", st)
	}
	return nil
}

func doSync(out io.Writer, c *http.Client, repoURL, tok string, r repoInfo, o opts) error {
	if !r.Fork {
		return fmt.Errorf("%s is not a fork: there is no upstream to sync with", r.FullName)
	}
	branch := o.branch
	if branch == "" {
		branch = r.DefaultBranch
	}
	if branch == "" {
		return errors.New("sync: no --branch and the repository reports no default branch")
	}
	if o.dryRun {
		fmt.Fprintf(out, "would sync branch %q with its upstream\n", branch)
		return nil
	}
	payload, _ := json.Marshal(map[string]string{"branch": branch})
	_, b, err := do(c, http.MethodPost, repoURL+"/merge-upstream", tok, payload, http.StatusOK)
	if err != nil {
		return err
	}
	var got struct {
		Message    string `json:"message"`
		MergeType  string `json:"merge_type"`
		BaseBranch string `json:"base_branch"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		return fmt.Errorf("sync: %w", err)
	}
	fmt.Fprintf(out, "synced: branch=%s merge_type=%s  %s\n", got.BaseBranch, got.MergeType, got.Message)
	return nil
}

func doSecret(out io.Writer, c *http.Client, repoURL, tok string, r repoInfo, o opts, name string, value []byte) error {
	_, b, err := do(c, http.MethodGet, repoURL+"/actions/secrets/public-key", tok, nil, http.StatusOK)
	if err != nil {
		return err
	}
	var pk struct {
		KeyID string `json:"key_id"`
		Key   string `json:"key"`
	}
	if err := json.Unmarshal(b, &pk); err != nil || pk.KeyID == "" || pk.Key == "" {
		return errors.New("public key: the reply carried no key_id and key")
	}
	raw, err := base64.StdEncoding.DecodeString(pk.Key)
	if err != nil || len(raw) != 32 {
		return errors.New("public key: not a base64 32-byte Curve25519 key")
	}
	var pub [32]byte
	copy(pub[:], raw)
	sealed, err := box.SealAnonymous(nil, value, &pub, rand.Reader)
	if err != nil {
		return fmt.Errorf("sealing: %w", err)
	}
	if o.dryRun {
		fmt.Fprintf(out, "would set secret %s (%d bytes sealed with key %s)\n", name, len(value), pk.KeyID)
		return nil
	}
	payload, _ := json.Marshal(map[string]string{
		"encrypted_value": base64.StdEncoding.EncodeToString(sealed),
		"key_id":          pk.KeyID,
	})
	st, _, err := do(c, http.MethodPut, repoURL+"/actions/secrets/"+name, tok, payload, http.StatusCreated, http.StatusNoContent)
	if err != nil {
		return err
	}
	verb := "created"
	if st == http.StatusNoContent {
		verb = "updated"
	}
	fmt.Fprintf(out, "secret %s %s on %s (HTTP %d)\n", name, verb, r.FullName, st)
	return nil
}

func patchRepo(c *http.Client, repoURL, tok string, m map[string]any) (repoInfo, error) {
	var got repoInfo
	payload, err := json.Marshal(m)
	if err != nil {
		return got, err
	}
	_, b, err := do(c, http.MethodPatch, repoURL, tok, payload, http.StatusOK)
	if err != nil {
		return got, err
	}
	if err := json.Unmarshal(b, &got); err != nil || got.FullName == "" {
		return got, errors.New("update: accepted, but the reply carried no repository, so nothing proves what the state is")
	}
	return got, nil
}

func getRepo(c *http.Client, base, tok, owner, repo string) (repoInfo, error) {
	var r repoInfo
	url := fmt.Sprintf("%s/repos/%s/%s", strings.TrimRight(base, "/"), owner, repo)
	// A renamed or transferred repository answers 301; it is reported, not
	// followed, so the caller sees the new name before writing anything.
	_, b, err := do(c, http.MethodGet, url, tok, nil, http.StatusOK)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("repository: %w", err)
	}
	if r.FullName == "" {
		return r, errors.New("repository: the reply carried no name")
	}
	return r, nil
}

func splitRepo(s string) (owner, repo string, err error) {
	owner, repo, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", fmt.Errorf("repository %q: want owner/repo", s)
	}
	return owner, repo, nil
}

func topicOK(t string) bool {
	if t == "" || len(t) > 50 || t[0] == '-' {
		return false
	}
	for _, c := range t {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func describeKeys(m map[string]any) string {
	var parts []string
	for _, k := range []string{"description", "homepage"} {
		if v, ok := m[k]; ok {
			parts = append(parts, fmt.Sprintf("%s=%q", k, v))
		}
	}
	return strings.Join(parts, " ")
}

func readText(path string, limit int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return "", err
	}
	if len(b) > limit {
		return "", fmt.Errorf("over %d bytes", limit)
	}
	return string(b), nil
}

// readSecretFile insists on a regular file only its owner can read: a secret
// in a world-readable file is already spent.
func readSecretFile(path string) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("value file: %w", err)
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("value file %s: not a regular file", path)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("value file %s: mode %04o, want owner-only (0600)", path, st.Mode().Perm())
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("value file: %w", err)
	}
	defer f.Close()
	return readSecretReader(f)
}

func readSecretReader(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxSecret+2))
	if err != nil {
		return nil, fmt.Errorf("value: %w", err)
	}
	b = bytes.TrimSuffix(b, []byte("\n"))
	b = bytes.TrimSuffix(b, []byte("\r"))
	if len(b) > maxSecret {
		return nil, fmt.Errorf("value: over %d bytes, GitHub's limit", maxSecret)
	}
	if len(b) == 0 {
		return nil, errors.New("value: empty")
	}
	return b, nil
}

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

// do sends one request and insists on one of the statuses the caller expects,
// returning the status that came. A failure quotes the API; a redirect names
// where it points, since it is never followed.
func do(c *http.Client, method, url, tok string, payload []byte, want ...int) (int, []byte, error) {
	var rdr io.Reader
	if payload != nil {
		rdr = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	for _, w := range want {
		if resp.StatusCode == w {
			return resp.StatusCode, b, nil
		}
	}
	msg := firstLine(string(b))
	if loc := resp.Header.Get("Location"); loc != "" && resp.StatusCode >= 300 && resp.StatusCode < 400 {
		msg += " (moved, not followed: " + loc + ")"
	}
	return resp.StatusCode, nil, fmt.Errorf("%s %s: %s: %s", method, url, resp.Status, msg)
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
