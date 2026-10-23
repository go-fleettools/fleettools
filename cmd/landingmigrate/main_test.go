package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A template landing as the fleet wrote ~260 of them, trimmed.
const tmpl = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="darkreader-lock">
<title>{{ .Site.Title }} — a stack</title>
<meta name="description" content="acme — pure Go, no cgo.">
<style>
  :root { --bg: #fff; --fg: #111; --muted: #555; --card: #f6f8fa; --border: #ddd; --accent: #B45309; --ready: #1a7f37; }
  :root[data-theme="dark"] { --bg: #000; --fg: #eee; --ready: #3fb950; }
  @media (prefers-color-scheme: dark) { :root:not([data-theme]) { --ready: #3fb950; } }
  body { margin: 0; }
  header { padding: 84px 0; }
  .card { border: 1px solid var(--border); }
  .status.ready { color: var(--ready); }
  @media (max-width: 600px) { .grid { display: block; } h1 { font-size: 2rem; } }
</style>
<script>
  (function(){var m=localStorage.getItem('theme');if(m&&m!=='system')document.documentElement.setAttribute('data-theme',m);})();
</script>
<link rel="icon" type="image/svg+xml" href="/favicon.svg">
</head>
<body>
  <button id="theme-toggle" class="theme-toggle" type="button"><span id="theme-label">System</span></button>
  <header class="wrap">
    <img class="logo" width="88" height="88" alt="acme" src="/img/logo.svg">
    <h1>acme</h1>
    <p class="tagline">A pure-Go stack for things.</p>
    <div class="pills">
      <span class="pill">pure Go · CGO=0</span>
      <span class="pill">stdlib only</span>
      <span class="pill">BSD-3 code · MIT artwork</span>
      <span class="pill">BSD-3-Clause</span>
    </div>
    <div class="cta">
      <a class="btn primary" href="https://github.com/acme/thing">{{ partial "icons/mark-github.html" . }}thing repo</a>
      <a class="btn ghost" href="https://acme.github.io/docs/">{{ partial "icons/book.html" . }}docs</a>
    </div>
    <div class="badges">
      <a href="https://pkg.go.dev/github.com/acme/thing"><img alt="pkg.go.dev" src="https://img.shields.io/badge/pkg.go.dev-thing-007d9c"></a>
      <img alt="License" src="https://img.shields.io/badge/license-BSD--3--Clause-blue">
    </div>
  </header>
  <main class="wrap">
    <p class="lead"><strong>acme</strong> does things.</p>
    <pre class="snippet"><code>thing.Do()</code></pre>
    <section class="grid">
      {{ range .Site.Params.repos }}<article class="card"><h3>{{ .name }}</h3><p>{{ .blurb }} Licensed BSD-3-Clause.</p></article>{{ end }}
    </section>
    <p class="principles">Every package is tested. The only package that needs cgo is none, and it says so. Everything is <strong>BSD-3-Clause</strong>.</p>
  </main>
  <footer class="wrap">
    BSD-3-Clause · <a href="https://github.com/acme">github.com/acme</a>
  </footer>
  <script>
  (function(){ var btn=document.getElementById('theme-toggle'); })();
  </script>
</body>
</html>
`

func TestRepeat(t *testing.T) {
	for s, want := range map[string]bool{
		"pure Go · CGO=0": true,
		"BSD-3-Clause":    true,
		"<code>CGO_ENABLED=0</code>, BSD-3-Clause.":    true,
		"Everything is <strong>BSD-3-Clause</strong>.": true,
		"BSD-3 code · MIT artwork":                     false, // says more
		"The only package that needs cgo is X.":        false,
		"A pure-Go stack.":                             false, // not a mention at all
		"stdlib only":                                  false,
		"BSD-3-Clause, été":                            false, // a non-ASCII letter is content
	} {
		if got := repeat(s); got != want {
			t.Errorf("repeat(%q) = %v, want %v", s, got, want)
		}
	}
}

// Wave 2 kept "BSD-3-Clause throughout." on three landings: a repeat.
func TestRepeatThroughout(t *testing.T) {
	if !repeat("BSD-3-Clause throughout.") {
		t.Error("not a repeat")
	}
}

func TestParseTemplate(t *testing.T) {
	l, err := parseTemplate(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if l.H1 != "acme" || l.Logo != "/img/logo.svg" || len(l.Pills) != 4 || len(l.CTAs) != 2 || l.CTAs[0].Href != "https://github.com/acme/thing" || l.Badges == "" || l.Footer == "" || !strings.Contains(l.Main, "snippet") {
		t.Fatalf("%+v", l)
	}
	if !strings.Contains(l.Title, "a stack") || l.Description == "" || l.Favicon == "" || l.HeadExtra != "" || len(l.Scripts) != 0 {
		t.Fatalf("head: %+v", l)
	}
	// anything unknown is a refusal, never dropped
	for name, bad := range map[string]string{
		"body":   strings.Replace(tmpl, "<main", "<div class=\"surprise\">!</div>\n<main", 1),
		"header": strings.Replace(tmpl, "<h1>acme</h1>", "<h1>acme</h1><p>extra</p>", 1),
		"style":  strings.Replace(tmpl, "<style>", "<nostyle>", 1),
		"h1":     strings.Replace(tmpl, "<h1>acme</h1>", "", 1),
		"main":   strings.Replace(tmpl, "<main", "<div", 1),
		"html":   "nothing",
		"hdr":    strings.Replace(tmpl, "<header", "<div", 1),
	} {
		if _, err := parseTemplate(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// a script that is not the theme bootstrap is kept, in head and body
	withScripts := strings.Replace(tmpl, "</head>", "<script>track()</script>\n</head>", 1)
	withScripts = strings.Replace(withScripts, "</body>", "<script>extra()</script>\n</body>", 1)
	l, err = parseTemplate(withScripts)
	if err != nil || !strings.Contains(l.HeadExtra, "track()") || len(l.Scripts) != 1 {
		t.Fatalf("%v %+v", err, l)
	}
}

func TestRenderAndRepeats(t *testing.T) {
	l, err := parseTemplate(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	index, term := render(l, "acme")
	out, frags := repeats(index)
	for _, want := range []string{
		`{{ partial "styles.html" . }}`, `{{ partial "fork-ribbon.html" "https://github.com/acme" }}`,
		`<h1 class="head">acme</h1>`, `class="btn btn-primary" href="https://github.com/acme/thing"`,
		`{{ partial "icons/github.html" . }}`, `{{ partial "hero-carousel.html" . }}`, `<main id="repos" class="legacy">`,
		ifOff + `<span class="pill">pure Go · CGO=0</span>` + end,
		ifOff + `<span class="pill">BSD-3-Clause</span>` + end,
		`<span class="pill">BSD-3 code · MIT artwork</span>`, // kept
		ifOff + `<img alt="License"`,
		ifOff + `BSD-3-Clause · ` + end + `<a href="https://github.com/acme">`,
		ifOff + `Everything is <strong>BSD-3-Clause</strong>.` + end,
		`The only package that needs cgo is none, and it says so.`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q", want)
		}
	}
	if strings.Contains(out, "mark-github") || strings.Contains(out, "snippet") {
		t.Error("the old icon name or the snippet is still in the page")
	}
	if !strings.Contains(term, "thing.Do()") || !strings.Contains(term, `class="term"`) {
		t.Errorf("terminal: %s", term)
	}
	var hidden, kept int
	for _, f := range frags {
		if f.Hidden {
			hidden++
		} else {
			kept++
		}
	}
	// the card's "{{ .blurb }} Licensed BSD-3-Clause." is listed, not
	// hidden: hiding it would hide the blurb
	if hidden != 5 || kept != 3 {
		t.Errorf("hidden %d kept %d: %+v", hidden, kept, frags)
	}
	// the legacy rules: scoped, colours read from the kit's tokens
	css := legacyCSS(l.Style)
	for _, want := range []string{".legacy{--fg:var(--ink)", "--ready:#1a7f37", `:root[data-theme="dark"] .legacy{--ready:#3fb950}`,
		".legacy .card{", ".legacy .status.ready{", "@media (max-width: 600px){\n  .legacy .grid{"} {
		if !strings.Contains(css, want) {
			t.Errorf("css lacks %q\n%s", want, css)
		}
	}
	for _, not := range []string{".legacy body", ".legacy header", ".legacy h1"} {
		if strings.Contains(css, not) {
			t.Errorf("css styles %q", not)
		}
	}
	// no snippet: the terminal says what to type
	l.Main = "<p>x</p>"
	_, term = render(l, "acme")
	if !strings.Contains(term, `printf "github.com/acme/%s" .name`) || !strings.Contains(term, `{{ else }}<span class="c-cmd">$ gh repo list</span> acme`) {
		t.Error(term)
	}
	// no [[params.repos]]: the repositories the page links to (wave 2 found
	// four landings whose terminal failed to build on `first 6 nil`)
	l.Repos = linkedRepos(`<a href="https://github.com/acme/one">1</a><a href="https://github.com/acme/brand">b</a><a href="https://github.com/other/x">x</a><a href="https://github.com/acme/two/">2</a><a href="https://github.com/acme/one">again</a>`, "acme")
	if strings.Join(l.Repos, ",") != "one,two" {
		t.Fatal(l.Repos)
	}
	_, term = render(l, "acme")
	if !strings.Contains(term, "{{ else }}<span class=\"c-cmd\">$ go get</span> github.com/acme/one@latest\n<span class=\"c-cmd\">$ go get</span> github.com/acme/two@latest\n{{ end }}") {
		t.Error(term)
	}
	l.Repos = []string{"a", "b", "c", "d", "e", "f", "g"}
	if _, term = render(l, "acme"); strings.Contains(term, "acme/g@") {
		t.Error("more than six")
	}
	l.Logo, l.Favicon, l.Footer, l.Description, l.HeadExtra, l.Scripts, l.Tagline, l.Badges, l.CTAs, l.Pills = "", "", "", "", "x", []string{"<script>s</script>"}, "", "", nil, nil
	index, _ = render(l, "acme")
	if !strings.Contains(index, `src="/img/logo.svg"`) || !strings.Contains(index, `href="/favicon.svg"`) || !strings.Contains(index, "<script>s</script>") {
		t.Error(index)
	}
}

func TestFooterShapes(t *testing.T) {
	note := func(string, string, bool) {}
	// a kit footer: spans
	got := footerSegments(`<footer><div class="wrap"><span>© acme · BSD-3-Clause</span><span>Pure Go · CGO_ENABLED=0 · builds for js/wasm</span><span>BSD-3-Clause</span></div></footer>`, note)
	for _, want := range []string{"<span>© acme" + ifOff + " · BSD-3-Clause" + end + "</span>", "<span>Pure Go · " + ifOff + "CGO_ENABLED=0 · " + end + "builds", ifOff + "<span>BSD-3-Clause</span>" + end} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q in %s", want, got)
		}
	}
	// a footer whose wrap div holds the text; one segment, says more
	got = footerSegments(`<footer><div class="wrap">BSD-3-Clause for the code, OFL for the fonts</div></footer>`, note)
	if strings.Contains(got, ifOff) {
		t.Error(got)
	}
	got = footerSegments(`<footer>BSD-3-Clause</footer>`, note)
	if !strings.Contains(got, ifOff+"BSD-3-Clause"+end) {
		t.Error(got)
	}
	// the last segment takes the dot before it
	got = footerSegments(`<footer>acme · CGO_ENABLED=0</footer>`, note)
	if !strings.Contains(got, "acme"+ifOff+" · CGO_ENABLED=0"+end) {
		t.Error(got)
	}
}

func TestSentences(t *testing.T) {
	note := func(string, string, bool) {}
	// all of it a repeat: the whole paragraph
	if got := sentences(`<p class="x">Pure Go, <code>CGO_ENABLED=0</code>. BSD-3-Clause.</p>`, "p", note); !strings.HasPrefix(got, ifOff+"<p") {
		t.Error(got)
	}
	// nested blocks are left to their own visit
	in := `<li>BSD-3-Clause<ul><li>x</li></ul></li>`
	if got := sentences(in, "li", note); got != in {
		t.Error(got)
	}
	// actions and tags are not sentence ends
	parts := splitSentences(`A {{ "x." }} b <a href="a.b">c</a>. Next one! Last`)
	if len(parts) != 3 || !strings.HasSuffix(parts[0], "</a>.") {
		t.Errorf("%q", parts)
	}
	if got, frags := repeats(`<p>No mention here.</p>`); got != `<p>No mention here.</p>` || len(frags) != 0 {
		t.Error(got, frags)
	}
}

func TestElements(t *testing.T) {
	src := `<p>a</p><pre>x</pre><p>b<p>c</p></p><p>open`
	got := eachElement(src, "<p", "p", func(s string) string { return "[" + s + "]" })
	if got != `[<p>a</p>]<pre>x</pre>[<p>b<p>c</p></p>]<p>open` {
		t.Error(got)
	}
	if _, _, ok := span("<div>", "<div", "div"); ok {
		t.Error("an unclosed element was found")
	}
	if indexOpen("<p", "<p", 0) != -1 {
		t.Error("a truncated tag")
	}
	if closeOf("<p><p>", 0, "p") != -1 {
		t.Error("never closes")
	}
}

func TestWorkflowAndConfig(t *testing.T) {
	peaceiris := "jobs:\n  build:\n    steps:\n      - uses: actions/checkout@v7\n      - name: Set up Hugo\n        uses: peaceiris/actions-hugo@v3\n        with:\n          hugo-version: \"0.168.0\"\n"
	got, ok := addSetupGo(peaceiris)
	if !ok || !strings.Contains(got, "      - uses: actions/setup-go@v7\n        with:\n          go-version-file: go.mod\n") || strings.Index(got, "setup-go") > strings.Index(got, "Set up Hugo") {
		t.Fatal(got)
	}
	again, ok := addSetupGo(got)
	if !ok || again != got {
		t.Error("not idempotent")
	}
	// the official template installs hugo BEFORE the checkout: setup-go goes
	// after the checkout, where go.mod is (CI failed on the first wave)
	deb := "    steps:\n      - name: Install Hugo CLI\n        run: |\n          wget https://github.com/gohugoio/hugo/releases/download/v1/hugo.deb\n\n      - name: Checkout\n        uses: actions/checkout@v7\n        with:\n          submodules: recursive\n\n      # pages\n      - name: Setup Pages\n        uses: actions/configure-pages@v6\n"
	got, ok = addSetupGo(deb)
	if !ok || strings.Index(got, "setup-go") < strings.Index(got, "submodules: recursive") || strings.Index(got, "setup-go") > strings.Index(got, "# pages") {
		t.Fatal(got)
	}
	// the checkout as the last step
	last := "jobs:\n  b:\n    steps:\n      - uses: peaceiris/actions-hugo@v3\n      - uses: actions/checkout@v7\n  deploy:\n    x: 1\n"
	if got, ok := addSetupGo(last); !ok || strings.Index(got, "setup-go") > strings.Index(got, "  deploy:") || strings.Index(got, "setup-go") < strings.Index(got, "checkout") {
		t.Fatal(got)
	}
	// a setup-go for another module (a playground's) does not count
	other := "    steps:\n      - uses: actions/checkout@v7\n      - uses: peaceiris/actions-hugo@v3\n      - run: hugo\n      - uses: actions/setup-go@v7\n        with:\n          go-version-file: playground/go.mod\n"
	if got, ok := addSetupGo(other); !ok || strings.Count(got, "actions/setup-go") != 2 || strings.Index(got, "go-version-file: go.mod") > strings.Index(got, "run: hugo") {
		t.Fatal(got)
	}
	if _, ok := addSetupGo("steps: []\n"); ok {
		t.Error("no hugo install found, yet patched")
	}
	if _, ok := addSetupGo("uses: peaceiris/actions-hugo@v3\n"); ok {
		t.Error("no checkout, yet patched")
	}
	if _, ok := addSetupGo("uses: peaceiris/actions-hugo@v3\nuses: actions/checkout@v7\n"); ok {
		t.Error("no step found, yet patched")
	}
	conf := addModule("baseURL = \"x\"\n\n[params]\n  a = 1\n")
	if !strings.Contains(conf, "[module]\n  [[module.imports]]\n    path = \"github.com/go-fleettools/landingkit\"\n\n[params]") {
		t.Error(conf)
	}
	if addModule(conf) != conf {
		t.Error("not idempotent")
	}
	if c := addModule("baseURL = \"x\"\n"); !strings.HasSuffix(c, "landingkit\"\n\n") {
		t.Errorf("%q", c)
	}
}

// fakeGo answers `go mod tidy` and `go run …landingkit brand`.
func fakeGo(fail string) func(string, ...string) ([]byte, error) {
	return func(dir string, args ...string) ([]byte, error) {
		if fail != "" && strings.Contains(strings.Join(args, " "), fail) {
			return nil, errors.New("go " + fail + ": failed")
		}
		if args[0] == "mod" {
			return nil, os.WriteFile(filepath.Join(dir, "go.sum"), []byte("sum\n"), 0o644)
		}
		return []byte("[params.brand]\n  base = \"#123456\"\n"), nil
	}
}

func landingDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	files := map[string]string{
		"layouts/index.html":                      tmpl,
		"layouts/partials/icons/book.html":        "<svg/>",
		"layouts/partials/icons/mark-github.html": "<svg/>",
		"layouts/partials/icons/OCTICONS-LICENSE": "MIT",
		"hugo.toml":                    "baseURL = \"x\"\n\n[[params.repos]]\n  name = \"thing\"\n",
		".github/workflows/deploy.yml": "jobs:\n  build:\n    steps:\n      - uses: actions/checkout@v7\n      - uses: peaceiris/actions-hugo@v3\n",
		".github/workflows/other.yml":  "jobs: {}\n",
		"README.md":                    "# acme\n",
	}
	for p, s := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(d, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func run(t *testing.T, args ...string) (string, int) {
	t.Helper()
	c := RootCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs(args)
	return out.String() + "", func() int { code := execute(c); return code }()
}

func runCmd(t *testing.T, args ...string) (string, int) {
	t.Helper()
	c := RootCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs(args)
	code := execute(c)
	return out.String(), code
}

func TestMigrate(t *testing.T) {
	goCmd = fakeGo("")
	d := landingDir(t)
	rep := filepath.Join(t.TempDir(), "r.json")
	out, code := runCmd(t, "migrate", d, "--org", "acme", "--kit", "v0.6.0", "--data", "fonts: OFL-1.1", "--report", rep)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"acme: template landing moved onto landingkit v0.6.0", "setup-go added to: .github/workflows/deploy.yml", "KEPT, says more: review", "hidden while the strip shows", "data chips: fonts: OFL-1.1"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q\n%s", want, out)
		}
	}
	read := func(p string) string { b, _ := os.ReadFile(filepath.Join(d, p)); return string(b) }
	if !strings.Contains(read("go.mod"), "module github.com/acme/acme.github.io") || !strings.Contains(read("go.mod"), "landingkit v0.6.0") {
		t.Error(read("go.mod"))
	}
	conf := read("hugo.toml")
	for _, want := range []string{"[module]", "[params.brand]", `items = ["LICENSE=BSD-3-Clause", "CGO_ENABLED=0", "fonts: OFL-1.1"]`} {
		if !strings.Contains(conf, want) {
			t.Errorf("hugo.toml lacks %q", want)
		}
	}
	for _, gone := range []string{"layouts/partials/icons/book.html", "layouts/partials/icons/mark-github.html", "layouts/partials/icons/OCTICONS-LICENSE"} {
		if _, err := os.Stat(filepath.Join(d, gone)); err == nil {
			t.Errorf("%s kept", gone)
		}
	}
	if !strings.Contains(read("README.md"), "landingkit") || !strings.Contains(read("kit.go"), "//go:build tools") || read("layouts/partials/hero-terminal.html") == "" {
		t.Error("README, kit.go or the terminal")
	}
	var r report
	b, _ := os.ReadFile(rep)
	if err := json.Unmarshal(b, &r); err != nil || len(r.Fragments) == 0 {
		t.Fatal(err, string(b))
	}
	// once on the kit, a second migration refuses
	if out, code := runCmd(t, "migrate", d, "--org", "acme", "--kit", "v0.6.0"); code != 1 || !strings.Contains(out, "already uses the kit") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestMigrateRefusals(t *testing.T) {
	goCmd = fakeGo("")
	if _, code := runCmd(t, "migrate", t.TempDir(), "--org", "acme", "--kit", "v0.6.0"); code != 1 {
		t.Error("no layout: not refused")
	}
	if _, code := runCmd(t, "migrate", t.TempDir()); code != 2 {
		t.Error("no --org: not a usage error")
	}
	d := landingDir(t)
	os.WriteFile(filepath.Join(d, "layouts/index.html"), []byte("<html>nothing</html>"), 0o644)
	if out, code := runCmd(t, "migrate", d, "--org", "acme", "--kit", "v0.6.0"); code != 1 || !strings.Contains(out, "a person should look") {
		t.Errorf("exit %d %s", code, out)
	}
	// no workflow installs hugo: refused before a single write
	d = landingDir(t)
	os.Remove(filepath.Join(d, ".github/workflows/deploy.yml"))
	if out, code := runCmd(t, "migrate", d, "--org", "acme", "--kit", "v0.6.0"); code != 1 || !strings.Contains(out, "how is this site built") {
		t.Errorf("exit %d %s", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(d, "layouts/index.html")); string(b) != tmpl {
		t.Error("a refusal wrote the layout")
	}
	d = landingDir(t)
	os.WriteFile(filepath.Join(d, ".github/workflows/deploy.yml"), []byte("uses: peaceiris/actions-hugo@v3\n"), 0o644)
	if _, code := runCmd(t, "migrate", d, "--org", "acme", "--kit", "v0.6.0"); code != 1 {
		t.Error("an unpatchable workflow was accepted")
	}
	for _, fail := range []string{"mod", "brand"} {
		goCmd = fakeGo(fail)
		if out, code := runCmd(t, "migrate", landingDir(t), "--org", "acme", "--kit", "v0.6.0"); code != 1 {
			t.Errorf("%s failing: exit %d %s", fail, code, out)
		}
	}
	// no logo in the header: the conventional file, then the favicon, then a
	// refusal before any write
	goCmd = fakeGo("")
	noLogo := strings.Replace(tmpl, `<img class="logo" width="88" height="88" alt="acme" src="/img/logo.svg">`, "", 1)
	d = landingDir(t)
	os.WriteFile(filepath.Join(d, "layouts/index.html"), []byte(noLogo), 0o644)
	if out, code := runCmd(t, "migrate", d, "--org", "acme", "--kit", "v0.6.0"); code != 1 || !strings.Contains(out, "no logo") {
		t.Errorf("exit %d %s", code, out)
	}
	os.MkdirAll(filepath.Join(d, "static"), 0o755)
	os.WriteFile(filepath.Join(d, "static/favicon.svg"), []byte("<svg/>"), 0o644)
	if out, code := runCmd(t, "migrate", d, "--org", "acme", "--kit", "v0.6.0"); code != 0 {
		t.Errorf("exit %d %s", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(d, "layouts/index.html")); !strings.Contains(string(b), `src="/favicon.svg"`) {
		t.Error("the favicon is not the brand glyph")
	}
	d = landingDir(t)
	os.Remove(filepath.Join(d, "hugo.toml"))
	if _, code := runCmd(t, "migrate", d, "--org", "acme", "--kit", "v0.6.0"); code != 1 {
		t.Error("no hugo.toml: not refused")
	}
}

func pageOf(t *testing.T, html string) page {
	t.Helper()
	p, err := readPage(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestJudge(t *testing.T) {
	old := pageOf(t, `<html><head><style>x{}</style><script>var a="hidden";</script></head><body><h1>acme</h1><p>Pure-Go stack with CGO_ENABLED=0 inside.</p><span>CGO_ENABLED=0</span><p>Everything is <b>BSD-3-Clause</b>.</p><a href="/x">x</a><img src="/l.svg"><img src="https://img.shields.io/badge/license-BSD-blue"></body></html>`)
	off := pageOf(t, `<body><nav>acme</nav><h1>acme</h1><p>Pure-Go stack with CGO_ENABLED=0 inside.</p><span>CGO_ENABLED=0</span><p>Everything is <b>BSD-3-Clause</b>.</p><a href="/x">x</a><img src="/l.svg"></body>`)
	strip := `<div class="mantra" role="note"><span class="mantra-row"><code class="mantra-chip">LICENSE=BSD-3-Clause</code><code class="mantra-chip">CGO_ENABLED=0</code></span></div>`
	on := pageOf(t, `<body><nav>acme`+strip+`</nav><h1>acme</h1><p>Pure-Go stack with CGO_ENABLED=0 inside.</p><a href="/x">x</a><img src="/l.svg"></body>`)
	frags := []fragment{
		{HTML: "CGO_ENABLED=0", Hidden: true},
		{HTML: "<p>Everything is <b>BSD-3-Clause</b>.</p>", Hidden: true},
		{HTML: "<img alt=License>", Hidden: true}, // a picture: no text
		{HTML: "Pure-Go stack with CGO_ENABLED=0 inside.", Hidden: false},
	}
	want := stripItems(nil)
	if v := judge(old, on, off, frags, want); !v.ok() {
		t.Fatalf("%+v", v)
	}
	// the strip as CSS generated content, as wave 3 shipped it: it looks
	// right and says nothing, so it fails (go-plot, go-ruby-zlib)
	css := pageOf(t, `<head><style>.nav::before{content:"LICENSE=BSD-3-Clause"}.nav::after{content:"CGO_ENABLED=0"}</style></head><body><nav>acme</nav><h1>acme</h1><p>Pure-Go stack with CGO_ENABLED=0 inside.</p><a href="/x">x</a><img src="/l.svg"></body>`)
	if v := judge(old, css, off, frags, want); len(v.NoStrip) != 2 || v.ok() {
		t.Errorf("a drawn strip passed: %+v", v)
	}
	// a data chip that is missing is missing
	if v := judge(old, on, off, frags, stripItems([]string{"fonts: OFL-1.1"})); len(v.NoStrip) != 1 {
		t.Errorf("%+v", v)
	}
	// text lost
	bad := pageOf(t, `<body><h1>acme</h1></body>`)
	if v := judge(old, bad, bad, frags, nil); len(v.Lost) == 0 || len(v.LostLinks) != 2 || v.ok() {
		t.Errorf("%+v", v)
	}
	// more removed than said
	on2 := pageOf(t, `<body><nav>acme</nav><h1>acme</h1><a href="/x">x</a><img src="/l.svg"></body>`)
	if v := judge(old, on2, off, frags, nil); len(v.Extra) == 0 {
		t.Errorf("%+v", v)
	}
	// a hidden repeat still shown
	if v := judge(old, off, off, frags, nil); len(v.Shown) == 0 {
		t.Errorf("%+v", v)
	}
	if _, err := readPage(errReader{}); err == nil {
		t.Error("a read error was swallowed")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("disk") }

func TestVerifyCmd(t *testing.T) {
	dir := t.TempDir()
	write := func(sub, s string) string {
		p := filepath.Join(dir, sub)
		os.MkdirAll(p, 0o755)
		os.WriteFile(filepath.Join(p, "index.html"), []byte(s), 0o644)
		return p
	}
	old := write("old", `<body><p>Hi.</p><span>BSD-3-Clause</span></body>`)
	off := write("off", `<body><p>Hi.</p><span>BSD-3-Clause</span></body>`)
	on := write("on", `<body><div class="mantra"><code class="mantra-chip">LICENSE=BSD-3-Clause</code><code class="mantra-chip">CGO_ENABLED=0</code></div><p>Hi.</p></body>`)
	drawn := write("drawn", `<head><style>.nav::before{content:"LICENSE=BSD-3-Clause"}</style></head><body><p>Hi.</p></body>`)
	rep := filepath.Join(dir, "r.json")
	writeJSON(rep, report{Fragments: []fragment{{HTML: "<span>BSD-3-Clause</span>", Hidden: true}}})
	if out, code := runCmd(t, "verify", "--old", old, "--on", on, "--off", off, "--report", rep); code != 0 || !strings.Contains(out, "VERIFIED") || !strings.Contains(out, "as text") {
		t.Fatalf("exit %d %s", code, out)
	}
	if out, code := runCmd(t, "verify", "--old", old, "--on", drawn, "--off", off, "--report", rep); code != 1 || !strings.Contains(out, "NO STRIP") {
		t.Fatalf("a drawn strip verified: exit %d %s", code, out)
	}
	// strip: a deployed page alone
	if out, code := runCmd(t, "strip", filepath.Join(on, "index.html")); code != 0 || !strings.Contains(out, "LICENSE=BSD-3-Clause | CGO_ENABLED=0") {
		t.Fatalf("exit %d %s", code, out)
	}
	if out, code := runCmd(t, "strip", filepath.Join(drawn, "index.html")); code != 1 || !strings.Contains(out, "not text on the page") {
		t.Fatalf("exit %d %s", code, out)
	}
	if _, code := runCmd(t, "strip", filepath.Join(on, "index.html"), "--data", "fonts: OFL-1.1"); code != 1 {
		t.Error("a missing data chip passed")
	}
	if _, code := runCmd(t, "strip", filepath.Join(dir, "none.html")); code != 2 {
		t.Error("a missing page")
	}
	if out, code := runCmd(t, "verify", "--old", old, "--on", off, "--off", on, "--report", rep); code != 1 || !strings.Contains(out, "LOST") {
		t.Fatalf("exit %d %s", code, out)
	}
	if _, code := runCmd(t, "verify"); code != 2 {
		t.Error("no flags: not a usage error")
	}
	if _, code := runCmd(t, "verify", "--old", old, "--on", on, "--off", off, "--report", filepath.Join(dir, "none")); code != 2 {
		t.Error("no report")
	}
	os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{"), 0o644)
	if _, code := runCmd(t, "verify", "--old", old, "--on", on, "--off", off, "--report", filepath.Join(dir, "bad.json")); code != 2 {
		t.Error("bad report")
	}
	for _, args := range [][]string{{"--old", dir}, {"--on", dir}, {"--off", dir}} {
		a := map[string]string{"--old": old, "--on": on, "--off": off}
		a[args[0]] = args[1]
		if _, code := runCmd(t, "verify", "--old", a["--old"], "--on", a["--on"], "--off", a["--off"], "--report", rep); code != 2 {
			t.Errorf("%s missing page: accepted", args[0])
		}
	}
}

func TestRepeatsCmd(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "index.html")
	os.WriteFile(p, []byte(`<p class="sub">Slides. <code>CGO_ENABLED=0</code>, BSD-3-Clause.</p>`), 0o644)
	rep := filepath.Join(d, "r.json")
	out, code := runCmd(t, "repeats", p, "--write", "--report", rep)
	b, _ := os.ReadFile(p)
	if code != 0 || !strings.Contains(string(b), ifOff+"<code>CGO_ENABLED=0</code>, BSD-3-Clause."+end) || !strings.Contains(out, "hidden while the strip shows") {
		t.Fatalf("exit %d %s\n%s", code, out, b)
	}
	if _, code := runCmd(t, "repeats", filepath.Join(d, "none")); code != 2 {
		t.Error("a missing file")
	}
}

func TestInventory(t *testing.T) {
	answer := `{"data":{"r0":{"isArchived":false,"gomod":{"text":"require github.com/go-fleettools/landingkit v0.4.0\n"},"index":null,"styles":null},` +
		`"r1":{"isArchived":false,"gomod":null,"index":{"text":` + jsonString(tmpl) + `},"styles":null},` +
		`"r2":null,` +
		`"r3":{"isArchived":true},` +
		`"r4":{"isArchived":false,"index":{"text":"<html></html>"},"styles":{"__typename":"Blob"}},` +
		`"r5":{"isArchived":false,"index":{"text":"<html></html>"}},` +
		`"r6":{"isArchived":false}}}`
	graphql = func(q string) ([]byte, error) { return []byte(answer), nil }
	orgs = func() ([]string, error) { return []string{"kit", "tmpl", "none", "old", "copy", "other", "empty"}, nil }
	out := filepath.Join(t.TempDir(), "inv.json")
	// an earlier file's migration is kept
	writeJSON(out, inventoryFile{Orgs: []entry{{Org: "tmpl", Migrated: "https://x/pull/1", Note: "n"}}})
	o, code := runCmd(t, "inventory", "--out", out, "--kit", "v0.6.0")
	if code != 0 || !strings.Contains(o, "7 organisations: 1 kit, 1 template, 3 copied, 1 no-landing, 1 archived") {
		t.Fatalf("exit %d %s", code, o)
	}
	var f inventoryFile
	b, _ := os.ReadFile(out)
	json.Unmarshal(b, &f)
	byOrg := map[string]entry{}
	for _, e := range f.Orgs {
		byOrg[e.Org] = e
	}
	if byOrg["kit"].Kit != "v0.4.0" || byOrg["tmpl"].Migrated == "" || byOrg["copy"].Note == "" || byOrg["empty"].Note != "no layouts/index.html" || byOrg["other"].Note == "" {
		t.Errorf("%+v", byOrg)
	}
	// mark records a migration
	if _, code := runCmd(t, "mark", "--inventory", out, "other", "https://x/pull/2"); code != 0 {
		t.Error("mark")
	}
	if _, code := runCmd(t, "mark", "--inventory", out, "nobody", "u"); code != 1 {
		t.Error("mark of an unknown org")
	}
	if _, code := runCmd(t, "mark", "--inventory", filepath.Join(t.TempDir(), "x"), "a", "u"); code != 2 {
		t.Error("mark without a file")
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte("{"), 0o644)
	if _, code := runCmd(t, "mark", "--inventory", bad, "a", "u"); code != 2 {
		t.Error("mark of a bad file")
	}
	// failures
	if _, code := runCmd(t, "inventory"); code != 2 {
		t.Error("no --out")
	}
	if _, code := runCmd(t, "inventory", "--out", out, "not a login!"); code != 1 {
		t.Error("a bad login")
	}
	graphql = func(string) ([]byte, error) { return []byte(`{"data":null}`), nil }
	if _, code := runCmd(t, "inventory", "--out", out, "a"); code != 1 {
		t.Error("no data")
	}
	graphql = func(string) ([]byte, error) { return []byte(`not json`), nil }
	if _, code := runCmd(t, "inventory", "--out", out, "a"); code != 1 {
		t.Error("not json")
	}
	graphql = func(string) ([]byte, error) { return nil, errors.New("HTTP 401") }
	if _, code := runCmd(t, "inventory", "--out", out, "a"); code != 1 {
		t.Error("an error")
	}
	orgs = func() ([]string, error) { return nil, errors.New("HTTP 401") }
	if _, code := runCmd(t, "inventory", "--out", out); code != 1 {
		t.Error("orgs failing")
	}
	// merge ignores a missing or broken earlier file
	if got := merge([]entry{{Org: "a"}}, bad); len(got) != 1 {
		t.Error(got)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// A landing that copied the kit's own partials keeps its layout: the copies
// go, the module comes in, and the layout's repeats hide.
func TestMigrateKitCopy(t *testing.T) {
	goCmd = fakeGo("")
	d := landingDir(t)
	layout := `<html><head>{{ partial "styles.html" . }}</head><body><p class="sub">Plots. Pure Go, <code>CGO_ENABLED=0</code>.</p><footer><div class="wrap"><span>© acme · BSD-3-Clause</span></div></footer></body></html>`
	os.WriteFile(filepath.Join(d, "layouts/index.html"), []byte(layout), 0o644)
	for _, p := range []string{"styles.html", "theme-toggle.html", "fork-ribbon.html", "icons/github.html"} {
		os.WriteFile(filepath.Join(d, "layouts/partials", p), []byte("x"), 0o644)
	}
	os.WriteFile(filepath.Join(d, "hugo.toml"), []byte("baseURL = \"x\"\n\n[params.brand]\n  base = \"#000000\"\n"), 0o644)
	// a module already there is added to, never written over
	os.WriteFile(filepath.Join(d, "go.mod"), []byte("module example.org/mine\n\ngo 1.27.3\n"), 0o644)
	out, code := runCmd(t, "migrate", d, "--org", "acme", "--kit", "v0.6.0")
	if gm, _ := os.ReadFile(filepath.Join(d, "go.mod")); !strings.HasPrefix(string(gm), "module example.org/mine") {
		t.Errorf("go.mod overwritten: %s", gm)
	}
	if code != 0 || !strings.Contains(out, "acme: kit-copy landing moved") {
		t.Fatalf("exit %d %s", code, out)
	}
	b, _ := os.ReadFile(filepath.Join(d, "layouts/index.html"))
	if !strings.Contains(string(b), ifOff+"Pure Go, <code>CGO_ENABLED=0</code>."+end) || !strings.Contains(string(b), "© acme"+ifOff+" · BSD-3-Clause"+end) {
		t.Error(string(b))
	}
	for _, p := range []string{"styles.html", "theme-toggle.html", "fork-ribbon.html", "icons/github.html", "hero-terminal.html"} {
		if _, err := os.Stat(filepath.Join(d, "layouts/partials", p)); err == nil {
			t.Errorf("%s is there", p)
		}
	}
	conf, _ := os.ReadFile(filepath.Join(d, "hugo.toml"))
	if strings.Count(string(conf), "[params.brand]") != 1 || !strings.Contains(string(conf), "[module]") {
		t.Error(string(conf))
	}
}

// With no snippet, no [[params.repos]] and no repository linked, the
// terminal lists the organisation's repositories (go-deltasync).
func TestTerminalFromTheOrganisation(t *testing.T) {
	goCmd = fakeGo("")
	old := listRepos
	listRepos = func(string) ([]string, error) { return []string{"chunk", "rdiff"}, nil }
	defer func() { listRepos = old }()
	d := landingDir(t)
	noSnippet := strings.Replace(tmpl, `<pre class="snippet"><code>thing.Do()</code></pre>`, "", 1)
	noSnippet = strings.Replace(noSnippet, `href="https://github.com/acme/thing"`, `href="https://example.org/thing"`, 1)
	os.WriteFile(filepath.Join(d, "layouts/index.html"), []byte(noSnippet), 0o644)
	if out, code := runCmd(t, "migrate", d, "--org", "acme", "--kit", "v0.6.0"); code != 0 {
		t.Fatalf("exit %d %s", code, out)
	}
	b, _ := os.ReadFile(filepath.Join(d, "layouts/partials/hero-terminal.html"))
	if !strings.Contains(string(b), "github.com/acme/chunk@latest") || !strings.Contains(string(b), "github.com/acme/rdiff@latest") {
		t.Error(string(b))
	}
}

func TestMarkSkip(t *testing.T) {
	inv := filepath.Join(t.TempDir(), "inv.json")
	writeJSON(inv, inventoryFile{Orgs: []entry{{Org: "a", State: "copied"}, {Org: "b", State: "copied"}}})
	if out, code := runCmd(t, "mark", "--inventory", inv, "--skip", "user decision 2026-10-23", "a", "b"); code != 0 {
		t.Fatalf("exit %d %s", code, out)
	}
	if _, code := runCmd(t, "mark", "--inventory", inv, "--skip", "x", "nobody"); code != 1 {
		t.Error("an unknown org")
	}
	if _, code := runCmd(t, "mark", "--inventory", inv, "a"); code != 2 {
		t.Error("mark without a URL or --skip")
	}
	// a fresh inventory keeps the decision
	got := merge([]entry{{Org: "a", State: "copied"}, {Org: "c"}}, inv)
	if got[0].Skipped != "user decision 2026-10-23" || got[1].Skipped != "" {
		t.Errorf("%+v", got)
	}
}

func TestMain_(t *testing.T) {
	code := -1
	old := osExit
	defer func() { osExit = old }()
	osExit = func(c int) { code = c }
	os.Args = []string{"landingmigrate", "--help"}
	main()
	if code != 0 {
		t.Fatalf("main exited %d", code)
	}
	_ = run
}
