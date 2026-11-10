package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/copycheck"
)

const refC = "static const int t[] = {3, 17, 5, 99, 42, 8, 61, 23, 77, 14, 90, 31, 6, 52, 85, 12, 71, 29, 44, 9};\n"
const ourOK = "package p\n\nvar x = 1\n"
const ourCopy = "package p\n\nvar t = []int{3, 17, 5, 99, 42, 8, 61, 23, 77, 14, 90, 31, 6, 52, 85, 12, 71, 29, 44, 9}\n"

func tarOf(t *testing.T, name, body string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write([]byte(body))
	tw.Close()
	gz.Close()
	return b.Bytes()
}

// repo sets up a checked repository, a TLS server for the reference, and a
// manifest whose tree hash was computed by the same code `copycheck pin` uses.
func repo(t *testing.T, our string, role, license string) (root, manifest, cache string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ref-v1.tar.gz" {
			http.NotFound(w, r)
			return
		}
		w.Write(tarOf(t, "ref-v1/lib/t.c", refC))
	}))
	t.Cleanup(srv.Close)
	testClient = srv.Client()
	t.Cleanup(func() { testClient = nil })

	cache = filepath.Join(t.TempDir(), "cache")
	f := &copycheck.Fetcher{Client: srv.Client(), CacheDir: filepath.Join(t.TempDir(), "other"), Limits: copycheck.DefaultLimits()}
	ref := copycheck.Reference{Name: "ref", URL: srv.URL + "/ref-v1.tar.gz", Pin: "v1", License: license, Role: role}
	_, tree, err := f.Fetch(ref)
	if err != nil {
		t.Fatal(err)
	}
	root = t.TempDir()
	os.WriteFile(filepath.Join(root, "p.go"), []byte(our), 0o644)
	manifest = filepath.Join(root, copycheck.DefaultManifest)
	y := fmt.Sprintf("version: 1\nreferences:\n  - name: ref\n    url: %s\n    pin: v1\n    tree_sha256: %s\n    license: %s\n    role: %s\n", ref.URL, tree, license, role)
	os.WriteFile(manifest, []byte(y), 0o644)
	return
}

func drive(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := cli.Drive(RootCmd(), args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestExitCodes(t *testing.T) {
	root, _, cache := repo(t, ourOK, "studied", "GPL-2.0-only")
	if code, out, _ := drive("--root", root, "--cache-dir", cache); code != 0 || !strings.Contains(out, "0 finding(s)") {
		t.Errorf("clean: %d %s", code, out)
	}

	root, _, cache = repo(t, ourCopy, "studied", "GPL-2.0-only")
	code, out, _ := drive("--root", root, "--cache-dir", cache)
	if code != 1 || !strings.Contains(out, "ours:   p.go:") || !strings.Contains(out, "theirs: ref lib/t.c:") {
		t.Errorf("copy: %d %s", code, out)
	}

	// --json is valid, carries both ends and the thresholds used
	code, out, _ = drive("--root", root, "--cache-dir", cache, "--json")
	var rep copycheck.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil || code != 1 || len(rep.Findings) != 1 || rep.Findings[0].Theirs == nil || rep.Thresholds.TableK != 8 {
		t.Errorf("json: %d %v %s", code, err, out)
	}

	// copying permitted by role and licence, and noticed: clean
	root, _, cache = repo(t, ourCopy, "tables-from", "Apache-2.0")
	os.WriteFile(filepath.Join(root, "NOTICE"), []byte("ref\n"), 0o644)
	if code, out, _ := drive("--root", root, "--cache-dir", cache); code != 0 {
		t.Errorf("attributed: %d %s", code, out)
	}
}

func TestUnanswerableIsTwoNeverClean(t *testing.T) {
	root, manifest, cache := repo(t, ourOK, "studied", "MIT")
	if code, _, e := drive("--root", root, "--cache-dir", cache, "--manifest", filepath.Join(root, "missing.yml")); code != 2 {
		t.Errorf("missing manifest: %d %s", code, e)
	}
	b, _ := os.ReadFile(manifest)
	os.WriteFile(manifest, bytes.Replace(b, []byte("tree_sha256: "), []byte("tree_sha256: 0"), 1), 0o644)
	if code, _, _ := drive("--root", root, "--cache-dir", cache); code != 2 {
		t.Errorf("bad hash: %d", code)
	}
	os.WriteFile(manifest, bytes.Replace(b, []byte("/ref-v1.tar.gz"), []byte("/gone-v1.tar.gz"), 1), 0o644)
	if code, _, e := drive("--root", root, "--cache-dir", cache); code != 2 || !strings.Contains(e, "404") {
		t.Errorf("unreachable reference: %d %s", code, e)
	}
	os.WriteFile(manifest, b, 0o644)
	// a cache inside a git tree is refused
	os.Mkdir(filepath.Join(root, ".git"), 0o755)
	if code, _, e := drive("--root", root, "--cache-dir", filepath.Join(root, "c")); code != 2 || !strings.Contains(e, "git") {
		t.Errorf("cache in git tree: %d %s", code, e)
	}
}

func TestPinPrintsAnEntryThatTheManifestAccepts(t *testing.T) {
	_, manifest, cache := repo(t, ourOK, "studied", "MIT")
	b, _ := os.ReadFile(manifest)
	url := strings.TrimSpace(strings.TrimPrefix(strings.Split(string(b), "\n")[3], "    url:"))
	code, out, e := drive("pin", "--name", "ref", "--url", url, "--pin", "v1", "--license", "MIT", "--role", "studied", "--path", "lib", "--cache-dir", cache)
	if code != 0 {
		t.Fatalf("%d %s", code, e)
	}
	y := "version: 1\nreferences:\n" + out
	if _, err := copycheck.ParseManifest([]byte(y)); err != nil {
		t.Fatalf("%v\n%s", err, y)
	}
	if code, _, _ := drive("pin", "--name", "ref", "--url", "http://x/v1", "--pin", "v1"); code != 2 {
		t.Error("http url accepted")
	}
}

func TestNothingOnStdoutBeforeRunning(t *testing.T) {
	code, out, e := drive("--help")
	if code != 0 || out != "" || !strings.Contains(e, "usage") {
		t.Errorf("help must go to stderr: %d %q", code, out)
	}
}
