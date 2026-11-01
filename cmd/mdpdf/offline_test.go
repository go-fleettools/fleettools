package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// A Markdown file from elsewhere (a third-party README, a pasted report) can
// carry a remote image or raw HTML with a stylesheet link. By default mdpdf
// renders offline: a loopback server receives nothing. With --allow-network
// the same file reaches it, which is also the control that proves the test
// can see a fetch.
func TestOfflineByDefault(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/css")
		_, _ = w.Write([]byte(`p{color:#000}`))
	}))
	defer srv.Close()
	md := "# Report\n\n<link rel=\"stylesheet\" href=\"" + srv.URL + "/s.css\">\n\n![logo](" + srv.URL + "/logo.png)\n\nText.\n"
	dir := t.TempDir()
	in := write(t, dir, "r.md", md)

	out := filepath.Join(dir, "offline.pdf")
	if err, msg := exec(t, "--in", in, "--out", out); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	open(t, out)
	if n := hits.Load(); n != 0 {
		t.Fatalf("default run made %d request(s): mdpdf must render offline", n)
	}

	out = filepath.Join(dir, "online.pdf")
	if err, msg := exec(t, "--in", in, "--out", out, "--allow-network"); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	if hits.Load() == 0 {
		t.Fatal("control: --allow-network fetched nothing, so this test cannot see a fetch")
	}
}
