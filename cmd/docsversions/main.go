// Command docsversions builds every published version of a versioned Hugo
// documentation site with ONE look, and puts the builds in a GitLab package
// registry:
//
//	docsversions build   --url https://example.org/docs --out versions
//	docsversions publish --dir versions --package example-docs --archive example-docs.tar.gz
//
// A version of the documentation is its CONTENT. Everything else -- the theme
// submodule, hugo.yaml, layouts, CSS, i18n, static files, the logos -- is the
// site's look, and it comes from the DEFAULT BRANCH for every version: `build`
// checks the default branch out once (a detached worktree, submodules
// included), then for each version replaces its content directory with the
// one the version's ref holds, and runs Hugo with that version's baseURL.
// Versions are "development" (the default branch) and every strict semantic
// version tag vX.Y.Z.
//
// ⛔ It exists because the look was frozen into each release. GT-Cloud's
// /docs/ served v1.0.3, built from a release branch that predated the logo
// menu, the language icon and the theme combo: three patch releases made only
// to carry a favicon, a logo and a colour to the default page each
// re-published the OLD interface around them, and the user saw a RESINFO link
// in the wrong place. "The look should not depend on the version."
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

var osExit = os.Exit

// failure is a run that could not do its job (exit 1). Anything else out of
// the command is a usage error (exit 2).
type failure struct{ err error }

func (f *failure) Error() string { return f.err.Error() }

func failf(format string, a ...any) error { return &failure{fmt.Errorf(format, a...)} }

// semver is a release tag: strict vMAJOR.MINOR.PATCH, nothing else.
var semver = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// git and hugo are seams the tests replace.
var (
	runGit  = func(dir string, args ...string) ([]byte, error) { return run(dir, "git", args...) }
	runHugo = func(dir, hugo string, args ...string) ([]byte, error) { return run(dir, hugo, args...) }
)

func run(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin = nil
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err != nil {
		return out.Bytes(), fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, lastLines(out.String(), 15))
	}
	return out.Bytes(), nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

const longHelp = `docsversions builds every published version of a versioned Hugo
documentation site with the look of the DEFAULT BRANCH, and publishes the
builds to a GitLab generic package registry.

A version is its content directory (--content) at a ref: "development" at the
default branch, and every strict tag vX.Y.Z. Everything else -- theme, hugo.yaml,
layouts, assets, i18n, static, submodules -- comes from the default branch, so
a change of look reaches every version at its next build.

  build    writes OUT/<version>/ for each version (Hugo with
           --baseURL URL/<version>/). A version that fails to build is
           reported and the others are still built; the exit status is 1.
  publish  uploads OUT/<version>/ as a tar.gz to
           <api>/projects/<project>/packages/generic/<package>/<version>/<archive>.
           In GitLab CI the API URL, the project and the job token come from
           CI_API_V4_URL, CI_PROJECT_ID and CI_JOB_TOKEN.

Exits 0 on success, 1 when a version failed, 2 on a usage error.`

func RootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "docsversions",
		Short:         "Build every version of a documentation site with the default branch's look",
		Long:          longHelp,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(buildCmd(), publishCmd())
	return root
}

func execute(c *cobra.Command) int {
	err := c.Execute()
	if err == nil {
		return 0
	}
	fmt.Fprintf(c.ErrOrStderr(), "docsversions: %v\n", err)
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

// ---- build ----

type buildOptions struct {
	repo, content, branch, url, out, development, hugo string
	only                                               []string
	hugoArgs                                           []string
}

func buildCmd() *cobra.Command {
	var o buildOptions
	c := &cobra.Command{
		Use:   "build --url URL [flags]",
		Short: "Build development and every vX.Y.Z tag with the default branch's look",
		Args:  cobra.NoArgs,
	}
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		if o.url == "" {
			return errors.New("--url is required, e.g. https://gt-cloud.resinfo.org/docs")
		}
		u, err := url.Parse(o.url)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return fmt.Errorf("--url %q is not an http(s) URL", o.url)
		}
		if filepath.IsAbs(o.content) || strings.Contains(filepath.ToSlash(o.content), "..") || o.content == "" {
			return fmt.Errorf("--content %q must be a directory inside the repository", o.content)
		}
		return build(cmd.OutOrStdout(), o)
	}
	f := c.Flags()
	f.StringVar(&o.repo, "repo", ".", "the documentation repository")
	f.StringVar(&o.content, "content", "content", "the directory a version consists of")
	f.StringVar(&o.branch, "branch", "main", "the default branch: its tree is the look of every version")
	f.StringVar(&o.url, "url", "", "where the versions are served; version V gets baseURL URL/V/")
	f.StringVar(&o.out, "out", "versions", "directory the builds are written to, one subdirectory per version")
	f.StringVar(&o.development, "development", "development", "name of the default branch's version")
	f.StringVar(&o.hugo, "hugo", "hugo", "the hugo binary")
	f.StringSliceVar(&o.only, "only", nil, "build only these versions (e.g. --only development on a merge request)")
	f.StringSliceVar(&o.hugoArgs, "hugo-arg", []string{"--gc", "--minify", "--panicOnWarning"}, "arguments passed to every hugo build")
	return c
}

type version struct{ name, ref string }

// versions lists development and every strict semver tag, newest tag first.
func versions(o buildOptions, skeleton string) ([]version, error) {
	out, err := runGit(o.repo, "tag", "--list")
	if err != nil {
		return nil, err
	}
	var tags []string
	for _, t := range strings.Fields(string(out)) {
		if semver.MatchString(t) {
			tags = append(tags, t)
		}
	}
	sort.Slice(tags, func(i, j int) bool { return newer(tags[i], tags[j]) })
	vs := []version{{o.development, skeleton}}
	for _, t := range tags {
		vs = append(vs, version{t, "refs/tags/" + t})
	}
	if len(o.only) > 0 {
		want := map[string]bool{}
		for _, v := range o.only {
			want[v] = true
		}
		var kept []version
		for _, v := range vs {
			if want[v.name] {
				kept = append(kept, v)
				delete(want, v.name)
			}
		}
		if len(want) > 0 {
			var missing []string
			for v := range want {
				missing = append(missing, v)
			}
			sort.Strings(missing)
			return nil, fmt.Errorf("--only names versions that do not exist: %s", strings.Join(missing, ", "))
		}
		vs = kept
	}
	return vs, nil
}

// newer compares two strict semver tags as numbers: v0.10.0 is newer than
// v0.9.0, which string order gets wrong.
func newer(a, b string) bool {
	pa, pb := semver.FindStringSubmatch(a), semver.FindStringSubmatch(b)
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x > y
		}
	}
	return false
}

// skeletonRef is the default branch as the remote has it when there is one:
// in CI the local branch may not exist, and locally it may be behind.
func skeletonRef(o buildOptions) (string, error) {
	for _, ref := range []string{"refs/remotes/origin/" + o.branch, "refs/heads/" + o.branch} {
		if _, err := runGit(o.repo, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err == nil {
			return ref, nil
		}
	}
	return "", fmt.Errorf("no branch %q, neither origin/%s nor a local one", o.branch, o.branch)
}

func build(w io.Writer, o buildOptions) error {
	skeleton, err := skeletonRef(o)
	if err != nil {
		return failf("%v", err)
	}
	vs, err := versions(o, skeleton)
	if err != nil {
		return err
	}
	out, err := filepath.Abs(o.out)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "docsversions-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	tree := filepath.Join(tmp, "site")

	// The look, once: the default branch in a detached worktree, with its
	// submodules (the theme, the logos).
	if _, err := runGit(o.repo, "worktree", "add", "--detach", tree, skeleton); err != nil {
		return failf("checking out the default branch: %v", err)
	}
	defer runGit(o.repo, "worktree", "remove", "--force", tree)
	if _, err := runGit(tree, "submodule", "update", "--init", "--recursive"); err != nil {
		return failf("submodules of the default branch: %v", err)
	}
	// Creation dates and authors, when the theme provides the script.
	if _, err := os.Stat(filepath.Join(tree, "themes", "hextra", "scripts", "page-history.sh")); err == nil {
		if hist, err := run(tree, "sh", "themes/hextra/scripts/page-history.sh"); err == nil {
			if err := os.MkdirAll(filepath.Join(tree, "data"), 0o755); err == nil {
				_ = os.WriteFile(filepath.Join(tree, "data", "pagehistory.json"), hist, 0o644)
			}
		} else {
			fmt.Fprintf(w, "page history not generated: %v\n", err)
		}
	}

	var failed []string
	for _, v := range vs {
		if err := buildOne(tree, out, o, v); err != nil {
			failed = append(failed, v.name)
			fmt.Fprintf(w, "FAILED %-12s %v\n", v.name, err)
			continue
		}
		fmt.Fprintf(w, "built  %-12s %s/%s/  (content of %s)\n", v.name, strings.TrimRight(o.url, "/"), v.name, strings.TrimPrefix(v.ref, "refs/"))
	}
	fmt.Fprintf(w, "%d of %d versions built with the look of %s\n", len(vs)-len(failed), len(vs), strings.TrimPrefix(skeleton, "refs/"))
	if len(failed) > 0 {
		return failf("%d version(s) did not build: %s", len(failed), strings.Join(failed, ", "))
	}
	return nil
}

// buildOne puts the version's content in the skeleton and runs Hugo.
func buildOne(tree, out string, o buildOptions, v version) error {
	contentDir := filepath.Join(tree, filepath.FromSlash(o.content))
	if err := os.RemoveAll(contentDir); err != nil {
		return err
	}
	archive, err := runGit(o.repo, "archive", "--format=tar", v.ref, "--", o.content)
	if err != nil {
		return err
	}
	n, err := untar(bytes.NewReader(archive), tree)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%s holds no file under %s", v.ref, o.content)
	}
	dest := filepath.Join(out, v.name)
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	args := append([]string{"--source", tree, "--destination", dest,
		"--baseURL", strings.TrimRight(o.url, "/") + "/" + v.name + "/"}, o.hugoArgs...)
	_, err = runHugo(tree, o.hugo, args...)
	return err
}

// untar extracts regular files and directories under root. A name that would
// land outside root after cleaning is refused, before anything is written
// for it; links are skipped -- a link is the other half of a traversal.
func untar(r io.Reader, root string) (int, error) {
	tr := tar.NewReader(r)
	n := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		target := filepath.Join(root, filepath.FromSlash(h.Name))
		if rel, err := filepath.Rel(root, target); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return n, fmt.Errorf("archive entry %q leaves the tree", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return n, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return n, err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return n, err
			}
			if _, err := io.Copy(f, io.LimitReader(tr, 1<<30)); err != nil {
				f.Close()
				return n, err
			}
			if err := f.Close(); err != nil {
				return n, err
			}
			n++
		}
	}
}

// ---- publish ----

type publishOptions struct {
	dir, pkg, archive, api, project, tokenEnv string
	dryRun                                    bool
}

func publishCmd() *cobra.Command {
	var o publishOptions
	c := &cobra.Command{
		Use:   "publish --package NAME --archive FILE [flags]",
		Short: "Upload each built version to a GitLab generic package registry",
		Args:  cobra.NoArgs,
	}
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		if o.pkg == "" || o.archive == "" {
			return errors.New("--package and --archive are required")
		}
		if strings.ContainsAny(o.archive, "/\\") {
			return fmt.Errorf("--archive %q must be a file name", o.archive)
		}
		if o.api == "" {
			o.api = os.Getenv("CI_API_V4_URL")
		}
		if o.project == "" {
			o.project = os.Getenv("CI_PROJECT_ID")
		}
		if !o.dryRun && (o.api == "" || o.project == "") {
			return errors.New("--api and --project are required outside GitLab CI (CI_API_V4_URL, CI_PROJECT_ID)")
		}
		return publish(cmd.OutOrStdout(), o)
	}
	f := c.Flags()
	f.StringVar(&o.dir, "dir", "versions", "the directory `build` wrote")
	f.StringVar(&o.pkg, "package", "", "generic package name")
	f.StringVar(&o.archive, "archive", "", "file name of each version's archive")
	f.StringVar(&o.api, "api", "", "GitLab API URL (default $CI_API_V4_URL)")
	f.StringVar(&o.project, "project", "", "project id or path (default $CI_PROJECT_ID)")
	f.StringVar(&o.tokenEnv, "token-env", "CI_JOB_TOKEN", "environment variable holding the CI job token, sent as JOB-TOKEN")
	f.BoolVar(&o.dryRun, "dry-run", false, "list what would be uploaded")
	return c
}

func publish(w io.Writer, o publishOptions) error {
	entries, err := os.ReadDir(o.dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && (e.Name() == "development" || semver.MatchString(e.Name())) {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return failf("%s holds no version directory: nothing to publish", o.dir)
	}
	token := os.Getenv(o.tokenEnv)
	if !o.dryRun && token == "" {
		return fmt.Errorf("$%s is empty", o.tokenEnv)
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	var failed []string
	for _, v := range names {
		u := fmt.Sprintf("%s/projects/%s/packages/generic/%s/%s/%s",
			strings.TrimRight(o.api, "/"), url.PathEscape(o.project), url.PathEscape(o.pkg), url.PathEscape(v), url.PathEscape(o.archive))
		if o.dryRun {
			fmt.Fprintf(w, "would upload %s/ to %s\n", filepath.Join(o.dir, v), u)
			continue
		}
		body, err := targz(filepath.Join(o.dir, v))
		if err != nil {
			return err
		}
		req, err := http.NewRequest(http.MethodPut, u, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("JOB-TOKEN", token)
		resp, err := client.Do(req)
		if err != nil {
			failed = append(failed, v)
			fmt.Fprintf(w, "FAILED %-12s %v\n", v, err)
			continue
		}
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			failed = append(failed, v)
			fmt.Fprintf(w, "FAILED %-12s HTTP %d %s\n", v, resp.StatusCode, strings.TrimSpace(string(msg)))
			continue
		}
		fmt.Fprintf(w, "published %-12s %d bytes\n", v, len(body))
	}
	if len(failed) > 0 {
		return failf("%d version(s) not published: %s", len(failed), strings.Join(failed, ", "))
	}
	return nil
}

// targz archives a directory's contents, paths relative to it, as `tar -C dir
// -czf - .` would -- the layout the showcase's deploy script extracts.
func targz(dir string) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	err := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "." {
			return err
		}
		if !fi.Mode().IsRegular() && !fi.IsDir() {
			return nil // links and devices are not part of a built site
		}
		h, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		h.Name = "./" + filepath.ToSlash(rel)
		if fi.IsDir() {
			h.Name += "/"
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
