package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The two pages as the real sites serve them, cut down to what matters.
const (
	mkdocsPage = `<!doctype html><html><head><link rel="stylesheet" href="assets/stylesheets/main.css">
<link rel="canonical" href="https://go-pkgx.github.io/docs/latest/"></head>
<body dir="ltr" data-md-color-scheme="default"><div data-md-component="skip"></div>
<header class="md-header" data-md-component="header"><nav class="md-header__inner md-grid">
<div class="md-nav__title">Finding packages</div></nav></header></body></html>`

	hextraPage = `<!doctype html><html><head><link rel="stylesheet" href="assets/css/compiled/main.css">
<link rel="canonical" href="https://go-pkgx.github.io/docs/main/"></head>
<body class="hextra-container"><header class="hx-sticky hx-top-0"><nav class="hx-mx-auto hx-flex">
<span class="hx-text-xl">Finding packages</span></nav></header></body></html>`

	// ⛔ THE PAGE THAT FOOLED ME, as a fixture: the two builds' SHARED
	// artefacts and nothing else. mkdocs-material and Hugo both write
	// assets/, 404.html, search/ and sitemap.xml.gz, so a listing of them
	// cannot name a generator and this must come back "none" rather than
	// picking one.
	sharedArtefactsOnly = `<html><body><ul>
<li><a href="assets/">assets/</a></li><li><a href="404.html">404.html</a></li>
<li><a href="search/">search/</a></li><li><a href="sitemap.xml.gz">sitemap.xml.gz</a></li>
</ul></body></html>`

	mikeRedirect = `<!doctype html><html><head><meta http-equiv="refresh" content="0; url=./latest/">
</head><body>Redirecting...</body></html>`
)

// ⛔ THE WHOLE CLAIM OF THIS COMMAND is that these markers separate the two.
// If they ever stop doing so, every verdict it has ever printed is worthless,
// so the claim is a test and not a comment.
func TestTheMarkersSeparateTheTwoGenerators(t *testing.T) {
	if p := judge(mkdocsPage); p.verdict != "mkdocs-material" {
		t.Errorf("the mkdocs-material page judged %q (counts %v)", p.verdict, p.counts)
	}
	if p := judge(hextraPage); p.verdict != "hextra" {
		t.Errorf("the Hextra page judged %q (counts %v)", p.verdict, p.counts)
	}
	// And each must be BLIND to the other, or a page is named by whichever
	// marker list happens to be checked first.
	if n := judge(mkdocsPage).counts["hextra"]; n != 0 {
		t.Errorf("the mkdocs-material page scored %d Hextra marker(s)", n)
	}
	if n := judge(hextraPage).counts["mkdocs-material"]; n != 0 {
		t.Errorf("the Hextra page scored %d mkdocs-material marker(s)", n)
	}
}

// ⛔ THE MISTAKE THIS COMMAND EXISTS BECAUSE OF, pinned so it cannot come
// back: shared artefacts name no generator. A tool that answers "hextra" here
// is the reasoning that withdrew a correct bug report.
func TestSharedArtefactsNameNoGenerator(t *testing.T) {
	if p := judge(sharedArtefactsOnly); p.verdict != none {
		t.Errorf("a listing of artefacts BOTH generators emit judged %q, want %q", p.verdict, none)
	}
}

func TestARedirectStubIsNotAGenerator(t *testing.T) {
	if p := judge(mikeRedirect); p.verdict != redirect {
		t.Errorf("mike's redirect stub judged %q, want %q", p.verdict, redirect)
	}
	if isGenerator(redirect) {
		t.Error("the redirect verdict is being treated as a generator")
	}
}

// ⛔ BOTH IS NOT A TIE TO BREAK. It means the markers are not exclusive, and
// a command that silently picked one would be reporting on a claim it has
// just disproved.
func TestAPageMatchingBothIsReportedAndNeverCompared(t *testing.T) {
	p := judge(mkdocsPage + hextraPage)
	if !strings.HasPrefix(p.verdict, "BOTH(") {
		t.Errorf("a page carrying both generators' markers judged %q", p.verdict)
	}
	if isGenerator(p.verdict) {
		t.Errorf("%q is being compared as a generator", p.verdict)
	}
}

// site serves the three paths docscheck asks for.
func site(t *testing.T, root, latest, main string) string {
	t.Helper()
	h := http.NewServeMux()
	serve := func(path, body string) {
		h.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if body == "" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(body))
		})
	}
	serve("/docs/", root)
	serve("/docs/latest/", latest)
	serve("/docs/main/", main)
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s.URL + "/docs"
}

func runOn(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	c := RootCmd()
	var out, errb bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errb)
	c.SetArgs(args)
	return execute(c), out.String(), errb.String()
}

// The real finding, end to end over HTTP: both URLs answer 200 and they are
// different sites.
func TestASupersededEntryPointIsFoundThroughRealHttp(t *testing.T) {
	base := site(t, mikeRedirect, mkdocsPage, hextraPage)
	code, out, _ := runOn(t, base)
	if code != 1 {
		t.Errorf("exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "the entry point serves mkdocs-material while the tip is hextra") {
		t.Errorf("the finding is not in the output:\n%s", out)
	}
	if !strings.Contains(out, "root=redirect") {
		t.Errorf("the redirect stub was not recognised:\n%s", out)
	}

	// ⛔ THE CONTROL: the same shape with the SAME generator at both ends is
	// clean, or the finding above is a verdict this command always returns.
	ok := site(t, mikeRedirect, hextraPage, hextraPage)
	if code, out, _ := runOn(t, ok); code != 0 {
		t.Errorf("a site consistent at both ends exited %d, want 0\n%s", code, out)
	}
}

// ⛔ go-plot's SHAPE, and the reason the comparison is narrow: it has no
// main/, and it is the site that was migrated BY HAND as the reference for
// every other. A tool that compares "hextra" with "HTTP 404" reports the one
// site that is right as the one that is wrong.
func TestASiteWithNoTipIsNotAMismatch(t *testing.T) {
	base := site(t, mikeRedirect, hextraPage, "")
	code, out, errb := runOn(t, base)
	if code == 1 {
		t.Errorf("a missing tip was reported as a superseded entry point:\n%s", out)
	}
	// It is not clean either: nothing was compared, and that is exit 2.
	if code != 2 {
		t.Errorf("exit %d, want 2 (nothing comparable)\n%s%s", code, out, errb)
	}
	if !strings.Contains(errb, "nothing was compared") {
		t.Errorf("stderr does not say nothing was compared: %q", errb)
	}

	// ⛔ AND IT MUST NOT POISON A REAL SWEEP: beside a genuine mismatch, the
	// uncomparable site is skipped and the finding still lands.
	bad := site(t, mikeRedirect, mkdocsPage, hextraPage)
	code, out, _ = runOn(t, base, bad)
	if code != 1 {
		t.Errorf("exit %d, want 1 — the finding beside the skipped site\n%s", code, out)
	}
	if !strings.Contains(out, "1 site(s) compared, 1 serving a superseded build") {
		t.Errorf("the tally does not exclude the uncomparable site:\n%s", out)
	}
}

// ⛔ A SCAN THAT READ NOTHING IS NOT A CLEAN SCAN. This fleet has published a
// zero that meant "could not read".
func TestAnUnreachableSiteIsNotReportedClean(t *testing.T) {
	code, out, _ := runOn(t, "--timeout", "100ms", "http://127.0.0.1:1/docs")
	if code != 2 {
		t.Errorf("an unreachable site exited %d, want 2\n%s", code, out)
	}
	if !strings.Contains(out, "unreachable") {
		t.Errorf("the output does not say it could not reach it:\n%s", out)
	}
}

func TestUsageErrors(t *testing.T) {
	if code, _, _ := runOn(t); code != 2 {
		t.Errorf("no argument = %d, want 2", code)
	}
	if code, _, errb := runOn(t, "--timeout", "0", "https://example.invalid"); code != 2 {
		t.Errorf("--timeout 0 = %d, want 2 (%s)", code, errb)
	}
}

// --counts prints the evidence, because a reader who disagrees with a verdict
// has no way to argue with a bare label.
func TestCountsPrintTheEvidence(t *testing.T) {
	base := site(t, mikeRedirect, mkdocsPage, hextraPage)
	_, out, _ := runOn(t, "--counts", base)
	if !strings.Contains(out, "mkdocs-material:") || !strings.Contains(out, "hextra:") {
		t.Errorf("--counts printed no marker counts:\n%s", out)
	}
}

// The command must answer -h without doing anything, which internal/fleet's
// gate also asserts across every command; this keeps the failure local.
func TestHelpIsAnswered(t *testing.T) {
	c := RootCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs([]string{"-h"})
	if code := execute(c); code != 0 {
		t.Errorf("-h exited %d, want 0: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "--timeout") {
		t.Errorf("-h does not print the flags:\n%s", out.String())
	}
}
