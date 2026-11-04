// Command bzcomment posts one comment to a Bugzilla bug through the REST API,
// after showing which bug and which account it is about to speak for.
//
// It exists so that a session can contribute to an upstream bug (first use:
// bugzilla.mozilla.org bug 1870699) without the API key ever passing through a
// URL, an argument vector, the environment or a log: the key is read from a
// 0600 file and sent only in the X-BUGZILLA-API-KEY header.
//
// References (read 2026-11-04):
//
//	https://bmo.readthedocs.io/en/latest/api/core/v1/comment.html  POST /rest/bug/(id)/comment, GET /rest/bug/comment/(id)
//	https://bmo.readthedocs.io/en/latest/api/core/v1/general.html  API-key authentication, X-BUGZILLA-API-KEY
//	https://bmo.readthedocs.io/en/latest/api/core/v1/user.html     GET /rest/whoami
//	https://bmo.readthedocs.io/en/latest/api/core/v1/bug.html      GET /rest/bug/(id), include_fields
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
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const (
	maxComment  = 64 << 10 // bytes in the comment file
	maxBugChars = 65535    // Bugzilla error 114 "Comment Too Long"
	maxResponse = 1 << 20  // bytes read from any response
	maxKeyFile  = 4 << 10
	timeout     = 30 * time.Second
	defaultBase = "https://bugzilla.mozilla.org"
)

const longHelp = `Posts a comment to a Bugzilla bug through the REST API.

The API key is read only from a file (default ~/.config/bugzilla/api-key, which
must be a regular file readable by its owner alone) and is sent only in the
X-BUGZILLA-API-KEY header. The base URL must be https.

--whoami prints the account the key belongs to. --dry-run prints the bug (id,
status, summary) and the exact comment, and posts nothing. A real post prints
the bug first, then the new comment id and its URL.

Exit status: 0 done, 2 refused or failed.`

// newClient is replaced by tests, which need to trust an httptest TLS server.
// Redirects are never followed: a redirect would carry the key header to
// wherever it points.
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

// RootCmd builds the command; tests drive it through run.
func RootCmd() *cobra.Command {
	var (
		base    string
		keyFile string
		dryRun  bool
		whoami  bool
	)
	c := cli.New("bzcomment [flags] BUG_ID COMMENT_FILE",
		"Post a comment to a Bugzilla bug", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if keyFile == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return cli.Unanswerable("no home directory, give --key-file: %v", err)
			}
			keyFile = filepath.Join(home, ".config", "bugzilla", "api-key")
		}
		return cli.Wrap(do(cli.Out(cmd), base, keyFile, dryRun, whoami, args))
	}
	f := c.Flags()
	f.StringVar(&base, "base", defaultBase, "Bugzilla base URL (https only)")
	f.StringVar(&keyFile, "key-file", "", "file holding the API key (default ~/.config/bugzilla/api-key)")
	f.BoolVar(&dryRun, "dry-run", false, "show the bug and the comment, post nothing")
	f.BoolVar(&whoami, "whoami", false, "print the account the key belongs to, and stop")
	return c
}

func run(args []string, out, errOut io.Writer) int {
	return cli.Drive(RootCmd(), args, out, errOut)
}

// readKey reads the key file, refusing anything but a regular file that only
// its owner can read. It opens first and stats the open file, so the checked
// file is the one read.
func readKey(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("key file: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("key file %s: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("key file %s is not a regular file", path)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("key file %s has mode %04o; it must not be readable by group or others (chmod 600)", path, fi.Mode().Perm())
	}
	b, err := io.ReadAll(io.LimitReader(f, maxKeyFile+1))
	if err != nil {
		return "", fmt.Errorf("key file %s: %w", path, err)
	}
	if len(b) > maxKeyFile {
		return "", fmt.Errorf("key file %s is larger than %d bytes", path, maxKeyFile)
	}
	key := strings.TrimSpace(string(b))
	if key == "" {
		return "", fmt.Errorf("key file %s is empty", path)
	}
	if strings.ContainsAny(key, "\r\n\x00") {
		return "", fmt.Errorf("key file %s holds more than one line", path)
	}
	return key, nil
}

func readComment(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("comment file: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("comment file %s is not a regular file", path)
	}
	if fi.Size() > maxComment {
		return "", fmt.Errorf("comment file %s is %d bytes; the limit is %d", path, fi.Size(), maxComment)
	}
	b, err := io.ReadAll(io.LimitReader(f, maxComment+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxComment {
		return "", fmt.Errorf("comment file %s is larger than %d bytes", path, maxComment)
	}
	if !utf8.Valid(b) {
		return "", fmt.Errorf("comment file %s is not valid UTF-8", path)
	}
	s := string(b)
	if strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("comment file %s is empty", path)
	}
	if n := utf8.RuneCountInString(s); n > maxBugChars {
		return "", fmt.Errorf("comment is %d characters; Bugzilla refuses more than %d", n, maxBugChars)
	}
	return s, nil
}

// api talks to one Bugzilla. The key lives only here.
type api struct {
	base *url.URL
	key  string
	hc   *http.Client
}

// redact removes the key from any text that will be shown, including text a
// server chose to echo back.
func (a *api) redact(s string) string {
	if a.key == "" {
		return s
	}
	return strings.ReplaceAll(s, a.key, "[redacted]")
}

// call performs one request and decodes the JSON answer into out. A failure
// names the status and the server's own "message" field if the body is JSON;
// a body that is not JSON is never echoed.
func (a *api) call(method, path string, body any, out any) error {
	u := *a.base
	path, u.RawQuery, _ = strings.Cut(path, "?")
	u.Path = strings.TrimRight(u.Path, "/") + path
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, u.String(), rd)
	if err != nil {
		return errors.New(a.redact(err.Error()))
	}
	req.Header.Set("X-BUGZILLA-API-KEY", a.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("%s %s: %s", method, path, a.redact(err.Error()))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return fmt.Errorf("%s %s: reading the response: %s", method, path, a.redact(err.Error()))
	}
	if len(raw) > maxResponse {
		return fmt.Errorf("%s %s: response larger than %d bytes, refused", method, path, maxResponse)
	}
	var e struct {
		Error   bool   `json:"error"`
		Message string `json:"message"`
		Code    int    `json:"code"`
	}
	isJSON := json.Unmarshal(raw, &e) == nil
	if resp.StatusCode < 200 || resp.StatusCode > 299 || (isJSON && e.Error) {
		msg := "the body is not JSON"
		if isJSON {
			msg = fmt.Sprintf("code %d: %s", e.Code, a.redact(e.Message))
		}
		return fmt.Errorf("%s %s: HTTP %d, %s", method, path, resp.StatusCode, msg)
	}
	if !isJSON {
		return fmt.Errorf("%s %s: the response is not JSON", method, path)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%s %s: unexpected response shape", method, path)
		}
	}
	return nil
}

func parseBase(s string) (*url.URL, error) {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("--base %q is not a URL", s)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("--base must be https, got %q", u.Scheme)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("--base must be a plain https URL, without credentials, query or fragment")
	}
	return u, nil
}

func do(out io.Writer, baseStr, keyFile string, dryRun, whoami bool, args []string) error {
	base, err := parseBase(baseStr)
	if err != nil {
		return err
	}
	if whoami {
		if len(args) != 0 {
			return errors.New("--whoami takes no operands")
		}
	} else if len(args) != 2 {
		return errors.New("usage: bzcomment [flags] BUG_ID COMMENT_FILE")
	}
	var id uint64
	var comment string
	if !whoami {
		id, err = strconv.ParseUint(args[0], 10, 31)
		if err != nil || id == 0 {
			return fmt.Errorf("BUG_ID %q is not a positive integer", args[0])
		}
		if comment, err = readComment(args[1]); err != nil {
			return err
		}
	}
	key, err := readKey(keyFile)
	if err != nil {
		return err
	}
	a := &api{base: base, key: key, hc: newClient()}

	var who struct {
		Name     string `json:"name"`
		RealName string `json:"real_name"`
	}
	if err := a.call("GET", "/rest/whoami", nil, &who); err != nil {
		return err
	}
	if whoami {
		fmt.Fprintln(out, a.redact(who.Name))
		return nil
	}

	var bugs struct {
		Bugs []struct {
			ID      int    `json:"id"`
			Summary string `json:"summary"`
			Status  string `json:"status"`
		} `json:"bugs"`
	}
	if err := a.call("GET", fmt.Sprintf("/rest/bug/%d?include_fields=id,summary,status", id), nil, &bugs); err != nil {
		return err
	}
	if len(bugs.Bugs) != 1 || uint64(bugs.Bugs[0].ID) != id {
		return fmt.Errorf("bug %d: the answer does not describe that bug", id)
	}
	b := bugs.Bugs[0]
	fmt.Fprintf(out, "account: %s\nbug %d [%s] %s\n", a.redact(who.Name), b.ID, a.redact(b.Status), a.redact(b.Summary))
	if dryRun {
		fmt.Fprintf(out, "--- comment (dry run, nothing posted) ---\n%s", comment)
		if !strings.HasSuffix(comment, "\n") {
			fmt.Fprintln(out)
		}
		fmt.Fprintln(out, "--- end ---")
		return nil
	}

	var posted struct {
		ID int `json:"id"`
	}
	if err := a.call("POST", fmt.Sprintf("/rest/bug/%d/comment", id), map[string]any{"comment": comment}, &posted); err != nil {
		return err
	}
	fmt.Fprintf(out, "comment id %d\n", posted.ID)

	// The comment number local to the bug is what the page anchor uses.
	var got struct {
		Comments map[string]struct {
			Count int `json:"count"`
		} `json:"comments"`
	}
	link := fmt.Sprintf("%s/show_bug.cgi?id=%d", strings.TrimRight(base.String(), "/"), id)
	if err := a.call("GET", fmt.Sprintf("/rest/bug/comment/%d?include_fields=count", posted.ID), nil, &got); err == nil {
		if c, ok := got.Comments[strconv.Itoa(posted.ID)]; ok {
			link += fmt.Sprintf("#c%d", c.Count)
		}
	}
	fmt.Fprintln(out, link)
	return nil
}
