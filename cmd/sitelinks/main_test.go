package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

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

func run(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	c := RootCmd()
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs(args)
	code := execute(c)
	return out.String(), code
}

// A site that links correctly, every way a built site does: root-absolute
// under the base, relative, minified unquoted, a directory, srcset, a fragment,
// external links -- and an inline script whose text LOOKS like an href, which
// is how the regular-expression version reported two false links per page.
func site(t *testing.T) string {
	root := t.TempDir()
	put(t, root, "index.html", `<!doctype html><html><head>
<link rel=stylesheet href=/docs/dev/css/main.css>
<script>var a=e.docsRoot+t.version+"/";x.href=a;document.write('<a href="/nowhere">')</script>
</head><body>
<a href="./guide/">guide</a> <a href=guide/page/#part>part</a> <a href="#top">top</a>
<img src="img/logo.png" srcset="img/logo.png 1x, img/logo@2x.png 2x">
<a href="https://example.org/">ext</a> <a href="mailto:x@y.z">mail</a>
</body></html>`)
	put(t, root, "css/main.css", "")
	put(t, root, "img/logo.png", "")
	put(t, root, "img/logo@2x.png", "")
	put(t, root, "guide/index.html", `<a href="../">home</a><a href="page/">page</a><a href="/docs/dev/img/logo.png">logo</a>`)
	put(t, root, "guide/page/index.html", `<a href="../../index.html">home</a>`)
	return root
}

func TestACorrectSiteHasNoBrokenLink(t *testing.T) {
	out, code := run(t, "--base", "/docs/dev/", site(t))
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	r, err := Check(site(t), "/docs/dev/")
	if err != nil {
		t.Fatal(err)
	}
	if r.Pages != 3 || r.Links != 10 {
		t.Errorf("pages %d, links %d — want 3 pages and 10 local links read", r.Pages, r.Links)
	}
}

// Positive witnesses: a missing file, a directory without index.html, and a
// relative link that climbs out of the page's directory to nothing.
func TestBrokenLinksAreSeen(t *testing.T) {
	root := site(t)
	put(t, root, "bad.html", `<a href="missing.html">a</a><a href="emptydir/">b</a><img src="../../x.png">`)
	if err := os.MkdirAll(filepath.Join(root, "emptydir"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := Check(root, "/docs/dev/")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"missing.html", "emptydir/"} {
		if _, ok := r.Broken[want]; !ok {
			t.Errorf("%s not reported broken: %v", want, r.Broken)
		}
	}
	// ../../x.png from /docs/dev/ climbs to /x.png: outside the base, counted
	// and not judged.
	if _, ok := r.OutsideLinks["../../x.png"]; !ok {
		t.Errorf("a link outside the base was not counted as outside: %v", r.OutsideLinks)
	}
	if _, code := run(t, "--base", "/docs/dev/", root); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
}

func TestNothingCheckedIsExitTwo(t *testing.T) {
	if _, code := run(t, t.TempDir()); code != 2 {
		t.Errorf("exit %d for a build with no page, want 2", code)
	}
	if _, code := run(t, "--base", "docs", t.TempDir()); code != 2 {
		t.Errorf("a base without slashes was accepted")
	}
}
