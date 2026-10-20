package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func put(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repo: v1.0.0 has the OLD look and content "one"; main has the NEW look and
// content "two"; v0.9.0 and v0.10.0 exist to test the ordering.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	put(t, dir, "hugo.yaml", "look: old\n")
	put(t, dir, "content/a.md", "one\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "v0.9")
	git(t, dir, "tag", "v0.9.0")
	git(t, dir, "tag", "v0.10.0")
	git(t, dir, "tag", "v1.0.0")
	git(t, dir, "tag", "not-a-version")
	put(t, dir, "hugo.yaml", "look: new\n")
	put(t, dir, "content/a.md", "two\n")
	put(t, dir, "content/b.md", "added on main\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "new look")
	return dir
}

// fakeHugo writes, into --destination, the look it was given and the content
// it found: what a real build would turn into HTML.
func fakeHugo(t *testing.T, fail string) {
	t.Helper()
	old := runHugo
	runHugo = func(dir, _ string, args ...string) ([]byte, error) {
		var src, dest, base string
		for i := 0; i+1 < len(args); i++ {
			switch args[i] {
			case "--source":
				src = args[i+1]
			case "--destination":
				dest = args[i+1]
			case "--baseURL":
				base = args[i+1]
			}
		}
		if fail != "" && strings.HasSuffix(base, "/"+fail+"/") {
			return nil, errors.New("hugo failed")
		}
		look, _ := os.ReadFile(filepath.Join(src, "hugo.yaml"))
		var content []string
		filepath.Walk(filepath.Join(src, "content"), func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() {
				b, _ := os.ReadFile(p)
				content = append(content, filepath.Base(p)+"="+strings.TrimSpace(string(b)))
			}
			return nil
		})
		put(t, dest, "index.html", strings.TrimSpace(string(look))+"|"+strings.Join(content, ",")+"|"+base)
		return nil, nil
	}
	t.Cleanup(func() { runHugo = old })
}

func runCmd(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	c := RootCmd()
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs(args)
	// Run first: `return out.String(), execute(c)` evaluates left to right
	// and returns the output from BEFORE the command ran.
	code := execute(c)
	return out.String(), code
}

func index(t *testing.T, out, v string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(out, v, "index.html"))
	if err != nil {
		t.Fatalf("%s: %v", v, err)
	}
	return string(b)
}

// The point of the command: an old tag keeps ITS content and gets the
// default branch's look.
func TestEveryVersionHasTheDefaultBranchLook(t *testing.T) {
	dir := repo(t)
	fakeHugo(t, "")
	out := filepath.Join(t.TempDir(), "v")
	if msg, code := runCmd(t, "build", "--repo", dir, "--url", "https://ex.org/docs/", "--out", out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, msg)
	}
	for v, want := range map[string]string{
		"development": "look: new|a.md=two,b.md=added on main|https://ex.org/docs/development/",
		"v1.0.0":      "look: new|a.md=one|https://ex.org/docs/v1.0.0/",
		"v0.10.0":     "look: new|a.md=one|https://ex.org/docs/v0.10.0/",
	} {
		if got := index(t, out, v); got != want {
			t.Errorf("%s:\n got %s\nwant %s", v, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "not-a-version")); err == nil {
		t.Error("a tag that is not vX.Y.Z was built")
	}
	// The worktree is removed afterwards.
	wt, _ := exec.Command("git", "-C", dir, "worktree", "list").Output()
	if strings.Count(string(wt), "\n") != 1 {
		t.Errorf("worktree left behind:\n%s", wt)
	}
}

// On a merge request the look AND the development content are the branch's.
func TestSkeletonPreviewsABranch(t *testing.T) {
	dir := repo(t)
	git(t, dir, "switch", "-q", "-c", "feature")
	put(t, dir, "hugo.yaml", "look: feature\n")
	put(t, dir, "content/a.md", "three\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "feature")
	fakeHugo(t, "")
	out := filepath.Join(t.TempDir(), "v")
	if msg, code := runCmd(t, "build", "--repo", dir, "--url", "https://ex.org/docs", "--out", out, "--skeleton", "HEAD", "--only", "development,v1.0.0"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, msg)
	}
	if got := index(t, out, "development"); !strings.HasPrefix(got, "look: feature|a.md=three") {
		t.Errorf("development on the branch: %s", got)
	}
	if got := index(t, out, "v1.0.0"); !strings.HasPrefix(got, "look: feature|a.md=one|") {
		t.Errorf("a release previewed with the branch's look: %s", got)
	}
	if _, code := runCmd(t, "build", "--repo", dir, "--url", "https://ex.org/docs", "--out", out, "--skeleton", "no-such-ref"); code != 1 {
		t.Errorf("an unknown skeleton: exit %d, want 1", code)
	}
}

func TestOnlyAndUnknownVersions(t *testing.T) {
	dir := repo(t)
	fakeHugo(t, "")
	out := filepath.Join(t.TempDir(), "v")
	if msg, code := runCmd(t, "build", "--repo", dir, "--url", "https://ex.org/docs", "--out", out, "--only", "development"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, msg)
	}
	if _, err := os.Stat(filepath.Join(out, "v1.0.0")); err == nil {
		t.Error("--only development built a release")
	}
	if _, code := runCmd(t, "build", "--repo", dir, "--url", "https://ex.org/docs", "--out", out, "--only", "v9.9.9"); code != 2 {
		t.Errorf("an unknown version: exit %d, want 2", code)
	}
	if _, code := runCmd(t, "build", "--repo", dir, "--out", out); code != 2 {
		t.Errorf("no --url: exit %d, want 2", code)
	}
}

// One version that does not build does not stop the others, and is exit 1.
func TestAFailingVersionIsReportedAndTheOthersBuilt(t *testing.T) {
	dir := repo(t)
	fakeHugo(t, "v0.9.0")
	out := filepath.Join(t.TempDir(), "v")
	msg, code := runCmd(t, "build", "--repo", dir, "--url", "https://ex.org/docs", "--out", out)
	if code != 1 || !strings.Contains(msg, "FAILED v0.9.0") {
		t.Fatalf("exit %d:\n%s", code, msg)
	}
	if index(t, out, "v1.0.0") == "" {
		t.Error("v1.0.0 not built after v0.9.0 failed")
	}
}

func TestSemverOrderIsNumeric(t *testing.T) {
	if !newer("v0.10.0", "v0.9.0") || newer("v0.9.0", "v0.10.0") || !newer("v1.0.0", "v0.99.99") {
		t.Error("v0.10.0 must be newer than v0.9.0, and v1.0.0 than v0.99.99")
	}
}

func TestUntarRefusesAnEntryLeavingTheTree(t *testing.T) {
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	tw.WriteHeader(&tar.Header{Name: "content/../../evil", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644})
	tw.Write([]byte("x"))
	tw.Close()
	root := t.TempDir()
	if _, err := untar(&b, root); err == nil {
		t.Fatal("an entry leaving the tree was extracted")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil")); err == nil {
		t.Fatal("the escaping file was written")
	}
}

func TestPublishUploadsEveryVersion(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "development/index.html", "dev")
	put(t, dir, "v1.0.0/index.html", "one")
	put(t, dir, "v1.0.0/css/a.css", "c")
	put(t, dir, "junk/index.html", "not a version")

	type upload struct {
		path, token string
		files       []string
	}
	var got []upload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		tr := tar.NewReader(gz)
		var files []string
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Error(err)
				return
			}
			if h.Typeflag == tar.TypeReg {
				files = append(files, h.Name)
			}
		}
		got = append(got, upload{r.URL.EscapedPath(), r.Header.Get("JOB-TOKEN"), files})
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	t.Setenv("CI_JOB_TOKEN", "job-token-value")

	msg, code := runCmd(t, "publish", "--dir", dir, "--package", "gt-docs", "--archive", "gt-docs.tar.gz", "--api", srv.URL+"/api/v4", "--project", "group/docs")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, msg)
	}
	if len(got) != 2 {
		t.Fatalf("%d uploads, want 2 (junk/ is not a version): %+v", len(got), got)
	}
	for _, u := range got {
		if u.token != "job-token-value" {
			t.Errorf("token %q", u.token)
		}
		if !strings.HasPrefix(u.path, "/api/v4/projects/group%2Fdocs/packages/generic/gt-docs/") || !strings.HasSuffix(u.path, "/gt-docs.tar.gz") {
			t.Errorf("path %s", u.path)
		}
	}
	if strings.Contains(msg, "job-token-value") {
		t.Error("the token reached the output")
	}
	if _, code := runCmd(t, "publish", "--dir", t.TempDir(), "--package", "p", "--archive", "a.tar.gz", "--api", srv.URL, "--project", "1"); code != 1 {
		t.Errorf("nothing to publish: exit %d, want 1", code)
	}
}
