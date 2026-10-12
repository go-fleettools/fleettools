package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

// ⛔ THE FIXTURES ARE REAL BYTES. Both of these were fetched from
// https://go-ooxml.github.io/ on 2026-10-12: the first while the Pages source
// was still a branch, the second a minute after it was set to the workflow.
// A matcher written from the SOURCE of the Hugo template would have matched
// neither, because `hugo --minify` drops the quotes around an attribute name
// -- which is the defect this tool exists to catch, so the fixture has to be
// the shape the server sends.
const (
	jekyllServed = `<!DOCTYPE html>
<html lang="en-US">
  <head>
    <meta charset="UTF-8">
<!-- Begin Jekyll SEO tag v2.8.0 -->
<title>go-ooxml landing | go-ooxml</title>
<meta name="generator" content="Jekyll v3.10.0" />
<meta property="og:title" content="go-ooxml landing" />
<!-- End Jekyll SEO tag -->
  </head>
</html>`

	hugoServed = `<!doctype html><html lang=en><head><meta charset=utf-8>` +
		`<title>go-ooxml — Word, Excel and PowerPoint in Go, with no C anywhere</title>` +
		`<meta name=generator content="Hugo 0.167.0">` +
		`<link rel=icon type=image/svg+xml href=/favicon.svg></head><body></body></html>`
)

func TestGeneratorOfReadsBothSpellings(t *testing.T) {
	for _, tc := range []struct {
		name, page, want string
	}{
		{"jekyll, quoted as a server writes it", jekyllServed, "Jekyll v3.10.0"},
		{"hugo, minified, the attribute name bare", hugoServed, "Hugo 0.167.0"},
		{"a page with no generator at all", `<html><head><title>x</title></head></html>`, ""},
		{"a page with no meta at all", `<html><body>hello</body></html>`, ""},
		{"single quotes", `<meta name='generator' content='MkDocs 1.6.1'>`, "MkDocs 1.6.1"},
		{"upper case attribute names", `<META NAME="Generator" CONTENT="Hugo 0.1">`, "Hugo 0.1"},
		{"another meta first", `<meta charset=utf-8><meta name=generator content="Hugo 0.2">`, "Hugo 0.2"},
		{"a valueless attribute before it", `<meta async name=generator content="Hugo 0.3">`, "Hugo 0.3"},
		{"a meta that never closes", `<meta name=generator content="Hugo`, ""},
		{"whitespace around the value", `<meta name="generator" content="  Hugo 0.4  ">`, "Hugo 0.4"},
	} {
		if got := generatorOf(tc.page); got != tc.want {
			t.Errorf("%s: generatorOf = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// ⛔ The negative control for the whole tool: the pattern must NOT match the
// Jekyll page as Hugo, or the sweep reports every broken site as built.
func TestTheTwoFixturesAreNotConfusedForOneAnother(t *testing.T) {
	if g := generatorOf(jekyllServed); strings.Contains(g, "Hugo") {
		t.Fatalf("the Jekyll page read as Hugo: %q", g)
	}
	if g := generatorOf(hugoServed); strings.Contains(g, "Jekyll") {
		t.Fatalf("the Hugo page read as Jekyll: %q", g)
	}
}

func TestAttrsReadsBareAndQuotedValues(t *testing.T) {
	got := attrs(` name=generator content="Hugo 0.167.0" async data-x='y' `)
	want := map[string]string{"name": "generator", "content": "Hugo 0.167.0", "async": "", "data-x": "y"}
	if len(got) != len(want) {
		t.Fatalf("attrs = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("attrs[%q] = %q, want %q", k, got[k], v)
		}
	}
}

// judge is the rule, so it is tested as a rule: one case per reason, and the
// reason has to be in the words it prints.
func TestJudge(t *testing.T) {
	withConfig := func(full string) bool { return strings.HasSuffix(full, "/jekyll-site") }
	for _, tc := range []struct {
		name, full, buildType, source, deflt, generator, want, says string
	}{
		{
			name: "the go-ooxml defect", full: "go-ooxml/go-ooxml.github.io",
			buildType: "legacy", source: "main", deflt: "main", generator: "Jekyll v3.10.0",
			want: drift, says: "README.md",
		},
		{
			// ⛔ The SECOND repair, measured on fifteen go-ruby landings: the
			// setting was right and the page was still Jekyll's, because a
			// setting publishes nothing — a deployment does.
			name: "the setting is right and no deployment is served", full: "go-ruby-hocon/go-ruby-hocon.github.io",
			buildType: "workflow", source: "main", deflt: "main", generator: "Jekyll v3.10.0",
			want: drift, says: "re-run the deploy workflow",
		},
		{
			name: "a Jekyll site somebody wrote", full: "someone/jekyll-site",
			buildType: "legacy", source: "main", deflt: "main", generator: "Jekyll v3.10.0",
			want: ok, says: "_config.yml",
		},
		{
			name: "built and published as it should be", full: "go-pdfkit/go-pdfkit.github.io",
			buildType: "workflow", source: "main", deflt: "main", generator: "Hugo 0.167.0",
			want: ok, says: "workflow",
		},
		{
			name: "an app with no generator meta, published from the workflow", full: "go-pdfkit/app",
			buildType: "workflow", source: "main", deflt: "main", generator: "",
			want: ok, says: "no generator meta",
		},
		{
			// ⛔ The case that would make this tool unusable if it were DRIFT:
			// mkdocs and mike push a built site to gh-pages, and that is the
			// published artefact under another name.
			name: "mike pushing a built site to gh-pages", full: "go-richdoc/docs",
			buildType: "legacy", source: "gh-pages", deflt: "main", generator: "mkdocs-1.6.1, mkdocs-material-9.6.1",
			want: ok, says: "gh-pages",
		},
		{
			name: "the source branch itself is served, generator or not", full: "x/y",
			buildType: "legacy", source: "main", deflt: "main", generator: "",
			want: watch, says: "its output is discarded",
		},
		{
			name: "Hugo output served from the source branch", full: "x/y",
			buildType: "legacy", source: "main", deflt: "main", generator: "Hugo 0.167.0",
			want: watch, says: "the branch the source lives on",
		},
		{
			name: "a build type with no source at all", full: "x/y",
			buildType: "legacy", source: "", deflt: "main", generator: "Hugo 0.167.0",
			want: watch, says: "cannot interpret",
		},
	} {
		// hasJekyllConfig is the only thing judge asks the network, and only
		// in the Jekyll case: the stub proves that too, because every other
		// case here would fail if it were consulted.
		restore := stubConfig(t, withConfig)
		verdict, note := judge(tc.full, tc.buildType, tc.source, tc.deflt, tc.generator)
		restore()
		if verdict != tc.want {
			t.Errorf("%s: verdict = %s, want %s (%s)", tc.name, verdict, tc.want, note)
		}
		if !strings.Contains(note, tc.says) {
			t.Errorf("%s: note = %q, want it to say %q", tc.name, note, tc.says)
		}
	}
}

// stubConfig replaces the one call judge can make.
func stubConfig(t *testing.T, has func(string) bool) func() {
	t.Helper()
	old := api
	api = func(args ...string) ([]byte, error) {
		if len(args) == 2 && strings.HasSuffix(args[1], "/contents/_config.yml") {
			full := strings.TrimSuffix(strings.TrimPrefix(args[1], "repos/"), "/contents/_config.yml")
			if has(full) {
				return []byte(`{"name":"_config.yml"}`), nil
			}
			return nil, errors.New("gh: Not Found (HTTP 404)")
		}
		t.Fatalf("judge made a call it should not: %v", args)
		return nil, nil
	}
	return func() { api = old }
}

// TestASweepReportsThePageAndNotTheWorkflow walks the whole tool with the
// network stubbed: two organisations, one of them serving the defect.
func TestASweepReportsThePageAndNotTheWorkflow(t *testing.T) {
	oldRepos, oldAPI, oldFetch := repos, api, fetch
	defer func() { repos, api, fetch = oldRepos, oldAPI, oldFetch }()

	repos = func(org string) ([]fleet.Repo, error) {
		switch org {
		case "go-ooxml":
			return []fleet.Repo{
				{FullName: "go-ooxml/go-ooxml.github.io", DefaultBranch: "main", HasPages: true},
				{FullName: "go-ooxml/opc", DefaultBranch: "main"}, // no page: not a site, and not an error
			}, nil
		case "go-pdfkit":
			return []fleet.Repo{{FullName: "go-pdfkit/go-pdfkit.github.io", DefaultBranch: "main", HasPages: true}}, nil
		}
		return nil, errors.New("gh: Not Found (HTTP 404)")
	}
	api = func(args ...string) ([]byte, error) {
		switch {
		case strings.HasSuffix(args[1], "go-ooxml.github.io/pages"):
			return []byte(`{"build_type":"legacy","html_url":"https://go-ooxml.github.io/","source":{"branch":"main","path":"/"}}`), nil
		case strings.HasSuffix(args[1], "go-pdfkit.github.io/pages"):
			return []byte(`{"build_type":"workflow","html_url":"https://go-pdfkit.github.io/","source":{"branch":"main","path":"/"}}`), nil
		case strings.HasSuffix(args[1], "/contents/_config.yml"):
			return nil, errors.New("gh: Not Found (HTTP 404)")
		}
		return nil, errors.New("unexpected call: " + strings.Join(args, " "))
	}
	fetch = func(url string) (string, error) {
		if strings.Contains(url, "ooxml") {
			return jekyllServed, nil
		}
		return hugoServed, nil
	}

	var out bytes.Buffer
	code := run([]string{"-orgs", "go-ooxml,go-pdfkit", "-fail-on-drift"}, &out, &out)
	got := out.String()
	if code != 1 {
		t.Errorf("exit = %d, want 1: a page serving something else is a failure under -fail-on-drift", code)
	}
	for _, want := range []string{
		"DRIFT",
		"go-ooxml/go-ooxml.github.io",
		"README.md",
		"2 page(s): 1 serving something else",
		"https://go-pdfkit.github.io/",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report does not say %q:\n%s", want, got)
		}
	}
	// ⛔ A repository with no page is not a finding. It was in the listing and
	// must not be in the report, or every organisation reads as broken.
	if strings.Contains(got, "go-ooxml/opc") {
		t.Errorf("a repository with no page was reported:\n%s", got)
	}
}

// Without -fail-on-drift the sweep still reports and exits zero: a watcher
// that fails is a watcher somebody turns off.
func TestDriftIsReportedWithoutFailingUnlessAsked(t *testing.T) {
	oldRepos, oldAPI, oldFetch := repos, api, fetch
	defer func() { repos, api, fetch = oldRepos, oldAPI, oldFetch }()
	repos = func(string) ([]fleet.Repo, error) {
		return []fleet.Repo{{FullName: "o/o.github.io", DefaultBranch: "main", HasPages: true}}, nil
	}
	api = func(args ...string) ([]byte, error) {
		if strings.HasSuffix(args[1], "/pages") {
			return []byte(`{"build_type":"legacy","html_url":"https://o.github.io/","source":{"branch":"main"}}`), nil
		}
		return nil, errors.New("gh: Not Found (HTTP 404)")
	}
	fetch = func(string) (string, error) { return jekyllServed, nil }

	var out bytes.Buffer
	if code := run([]string{"-orgs", "o"}, &out, &out); code != 0 {
		t.Errorf("exit = %d, want 0 without -fail-on-drift", code)
	}
	if !strings.Contains(out.String(), "DRIFT") {
		t.Errorf("the drift was not reported:\n%s", out.String())
	}
}

func TestWhatCannotBeReadIsSaidRatherThanCountedAsFine(t *testing.T) {
	oldRepos, oldAPI, oldFetch := repos, api, fetch
	defer func() { repos, api, fetch = oldRepos, oldAPI, oldFetch }()

	for _, tc := range []struct {
		name  string
		repos func(string) ([]fleet.Repo, error)
		api   func(...string) ([]byte, error)
		fetch func(string) (string, error)
		says  string
	}{
		{
			name:  "the organisation cannot be listed",
			repos: func(string) ([]fleet.Repo, error) { return nil, errors.New("gh: Bad credentials") },
			says:  "Bad credentials",
		},
		{
			name:  "the Pages configuration is unreadable",
			repos: func(string) ([]fleet.Repo, error) { return []fleet.Repo{{FullName: "o/r", HasPages: true}}, nil },
			api:   func(...string) ([]byte, error) { return nil, errors.New("gh: Not Found (HTTP 404)\nsecond line") },
			says:  "the Pages configuration is unreadable",
		},
		{
			name:  "the Pages configuration does not parse",
			repos: func(string) ([]fleet.Repo, error) { return []fleet.Repo{{FullName: "o/r", HasPages: true}}, nil },
			api:   func(...string) ([]byte, error) { return []byte("not json"), nil },
			says:  "does not parse",
		},
		{
			name:  "Pages is on and names no URL",
			repos: func(string) ([]fleet.Repo, error) { return []fleet.Repo{{FullName: "o/r", HasPages: true}}, nil },
			api:   func(...string) ([]byte, error) { return []byte(`{"build_type":"workflow"}`), nil },
			says:  "names no URL",
		},
		{
			name:  "the page itself does not answer",
			repos: func(string) ([]fleet.Repo, error) { return []fleet.Repo{{FullName: "o/r", HasPages: true}}, nil },
			api: func(...string) ([]byte, error) {
				return []byte(`{"build_type":"workflow","html_url":"https://o.github.io/"}`), nil
			},
			fetch: func(string) (string, error) { return "", errors.New("404 Not Found") },
			says:  "the page is not readable",
		},
	} {
		repos = tc.repos
		api = tc.api
		fetch = tc.fetch
		var out bytes.Buffer
		run([]string{"-orgs", "o"}, &out, &out)
		if !strings.Contains(out.String(), tc.says) {
			t.Errorf("%s: the report does not say %q:\n%s", tc.name, tc.says, out.String())
		}
		if !strings.Contains(out.String(), "1 unreadable") {
			t.Errorf("%s: an unreadable page was not counted as unreadable:\n%s", tc.name, out.String())
		}
	}
}

func TestArgumentsAreRead(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"-nonsense"}, &out, &errOut); code != 2 {
		t.Errorf("exit = %d, want 2 for a flag that does not exist", code)
	}
	if !strings.Contains(errOut.String(), "nonsense") {
		t.Errorf("the refusal does not name the flag: %q", errOut.String())
	}
	if _, err := organisations(" , "); err == nil {
		t.Error("-orgs naming nothing was accepted")
	}
	if got, err := organisations("a, b"); err != nil || len(got) != 2 || got[1] != "b" {
		t.Errorf("organisations(%q) = %v, %v", "a, b", got, err)
	}
}

// ⛔ There is deliberately NO test of the whole-fleet default. It calls
// fleet.Orgs, and a test that exercised it would sweep 294 real organisations
// against the hourly budget Renovate shares -- which is exactly how `redscan
// -h` once started a real sweep. Every test here names its organisations.

// ⛔ The real fetch, over a real socket, because the seam the other tests use
// cannot show that it reads a HEAD and stops: a page in this fleet can carry a
// megabyte of base64, and a sweep that pulled every one whole would spend its
// time on bytes it never looks at.
func TestFetchReadsAHeadAndRefusesWhatIsNotServed(t *testing.T) {
	big := hugoServed + strings.Repeat("x", 2*headBytes)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gone":
			http.Error(w, "no", http.StatusNotFound)
		default:
			io.WriteString(w, big)
		}
	}))
	defer srv.Close()

	got, err := fetch(srv.URL + "/")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(got) != headBytes {
		t.Errorf("fetch read %d bytes, want the first %d", len(got), headBytes)
	}
	if generatorOf(got) != "Hugo 0.167.0" {
		t.Errorf("the generator was not in what it read: %q", generatorOf(got))
	}
	if _, err := fetch(srv.URL + "/gone"); err == nil {
		t.Error("a 404 was read as a page")
	}
	if _, err := fetch("http://127.0.0.1:0/"); err == nil {
		t.Error("an unreachable host was read as a page")
	}
}

// ⛔ A documentation root is a REDIRECT, and reading it alone reported "no
// generator" for a site MkDocs had built. This is the fixture mike writes.
const mikeStub = `<!DOCTYPE html><html><head>` +
	`<meta http-equiv="refresh" content="0; url=./latest/">` +
	`<link rel="canonical" href="https://go-richdoc.github.io/docs/latest/">` +
	`</head><body>Redirecting...</body></html>`

func TestARedirectAtTheRootIsFollowedOnce(t *testing.T) {
	if got := refreshTarget(mikeStub, "https://go-richdoc.github.io/docs/"); got != "https://go-richdoc.github.io/docs/latest/" {
		t.Errorf("refreshTarget = %q", got)
	}
	for _, tc := range []struct{ name, page string }{
		{"a page that is not a redirect", hugoServed},
		{"a refresh with no url in its content", `<meta http-equiv="refresh" content="5">`},
		{"a meta that never closes", `<meta http-equiv="refresh" content="0; url=/x`},
		{"no meta at all", `<html><body>x</body></html>`},
		{"a target that is not a URL", `<meta http-equiv="refresh" content="0; url=http://%zz">`},
	} {
		if got := refreshTarget(tc.page, "https://x/"); got != "" {
			t.Errorf("%s: refreshTarget = %q, want nothing", tc.name, got)
		}
	}
	// A page's own URL comes from the API and should always parse; this is the
	// branch that says so rather than assuming it.
	if got := refreshTarget(mikeStub, "https://x/\x7f"); got != "" {
		t.Errorf("an unparsable base gave %q", got)
	}

	// And through the tool: the verdict must come from the page the redirect
	// leads to, not from the stub.
	oldRepos, oldAPI, oldFetch := repos, api, fetch
	defer func() { repos, api, fetch = oldRepos, oldAPI, oldFetch }()
	repos = func(string) ([]fleet.Repo, error) {
		return []fleet.Repo{{FullName: "go-richdoc/docs", DefaultBranch: "main", HasPages: true}}, nil
	}
	api = func(args ...string) ([]byte, error) {
		return []byte(`{"build_type":"legacy","html_url":"https://go-richdoc.github.io/docs/","source":{"branch":"gh-pages"}}`), nil
	}
	fetch = func(url string) (string, error) {
		if strings.HasSuffix(url, "/latest/") {
			return `<meta name="generator" content="mkdocs-1.6.1, mkdocs-material-9.6.1">`, nil
		}
		return mikeStub, nil
	}
	var out bytes.Buffer
	if code := run([]string{"-orgs", "go-richdoc", "-fail-on-drift"}, &out, &out); code != 0 {
		t.Errorf("exit = %d, want 0: a built site on gh-pages is not drift", code)
	}
	for _, want := range []string{"mkdocs-material", "gh-pages", "/docs/latest/"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the report does not say %q:\n%s", want, out.String())
		}
	}
}

func TestOrgsNamingNothingIsRefusedByRun(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"-orgs", " , "}, &out, &errOut); code != 2 {
		t.Errorf("exit = %d, want 2 when -orgs names nothing", code)
	}
	if !strings.Contains(errOut.String(), "names nothing") {
		t.Errorf("the refusal does not say why: %q", errOut.String())
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("a\nb"); got != "a" {
		t.Errorf("firstLine = %q, want %q", got, "a")
	}
	if got := firstLine("  only  "); got != "only" {
		t.Errorf("firstLine = %q, want %q", got, "only")
	}
}

func TestWorkersBelowOneIsOne(t *testing.T) {
	oldRepos := repos
	defer func() { repos = oldRepos }()
	repos = func(string) ([]fleet.Repo, error) { return nil, nil }
	if got := sweep([]string{"a", "b"}, 0); len(got) != 0 {
		t.Errorf("sweep with no sites returned %v", got)
	}
}
