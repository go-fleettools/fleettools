package copycheck

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// tarGz builds an archive the way GitHub does: everything under one top
// directory.
func tarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(files[n])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write(files[n])
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func zipOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, b := range files {
		w, _ := zw.Create(n)
		w.Write(b)
	}
	zw.Close()
	return buf.Bytes()
}

// serve serves path -> body over TLS and returns the server and a client that
// trusts it.
func serve(t *testing.T, routes map[string][]byte) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

// newFetcher uses a cache outside the repository: t.TempDir is not inside a
// git working tree.
func newFetcher(t *testing.T, c *http.Client) *Fetcher {
	t.Helper()
	return &Fetcher{Client: c, CacheDir: filepath.Join(t.TempDir(), "cache"), Limits: DefaultLimits()}
}

// pinned fills in the tree hash by fetching once, as `copycheck pin` does.
func pinned(t *testing.T, f *Fetcher, r Reference) Reference {
	t.Helper()
	_, tree, err := f.Fetch(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Tree = tree
	return r
}

func writeTree(t *testing.T, files map[string][]byte) string {
	t.Helper()
	root := t.TempDir()
	for n, b := range files {
		p := filepath.Join(root, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
