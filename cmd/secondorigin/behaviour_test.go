package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/mxschmitt/playwright-go"
)

// ---- the released go-slides frame (v0.1.0), mutated as the reviewer did -------

// realShell serves the shell of go-slides-usercontent v0.1.0 (testdata), the
// placeholders rendered for https://acme.github.io, with mutate applied.
func realShell(t *testing.T, mutate func(string) string) (int, string) {
	t.Helper()
	b, err := os.ReadFile("testdata/gitframe-v0.1.0-shell.js")
	if err != nil {
		t.Fatal(err)
	}
	h, _ := os.ReadFile("testdata/gitframe-v0.1.0-shell.html")
	js := strings.NewReplacer("@@ALLOW@@", `["https://acme.github.io"]`, "@@GITLAB_CLIENT_ID@@", "").Replace(string(b))
	if mutate != nil {
		js = mutate(js)
	}
	f, srv := newFake(t)
	f.body["/shell.html"] = strings.Replace(string(h), "wasm_exec.js", "shell.js", 1) // one script file is enough for a static reading
	f.body["/shell.js"] = js
	csp := `default-src 'none'; script-src 'self' 'wasm-unsafe-eval'; connect-src 'self' https://api.github.com https://gitlab.com; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'`
	return verifyRun(t, srv, decl(t, func(d *Decl) { d.CSP = csp }))
}

// The three mutations of fleettools#243, on the real shell: each passed before.
func TestTheReleasedFrameAndTheReviewersMutations(t *testing.T) {
	const guard = "if (ALLOW.indexOf(e.origin) < 0 || e.source !== window.parent) { return; }"
	if code, out := realShell(t, nil); code != 0 {
		t.Fatalf("the released shell does not verify (static): code %d\n%s", code, out)
	}
	for name, mut := range map[string]func(string) string{
		"the origin test dropped from the guard": func(s string) string {
			return strings.Replace(s, guard, "if (e.source !== window.parent) { return; }", 1)
		},
		"the guard removed": func(s string) string { return strings.Replace(s, guard, "", 1) },
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(realMarker(t), guard) {
				t.Fatal("the fixture no longer holds the guard these mutations remove")
			}
			code, out := realShell(t, mut)
			if code != 1 || !strings.Contains(out, "FAIL       shell /shell.html origin check") {
				t.Fatalf("code %d, want 1:\n%s", code, out)
			}
		})
	}
}

func realMarker(t *testing.T) string {
	b, err := os.ReadFile("testdata/gitframe-v0.1.0-shell.js")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ---- the run: a real browser ----------------------------------------------------

func needBrowser(t *testing.T, name string) {
	t.Helper()
	pw, err := playwright.Run()
	if err != nil {
		t.Skip("no playwright driver: ", err)
	}
	defer pw.Stop()
	bt := pw.Chromium
	if name == "firefox" {
		bt = pw.Firefox
	}
	b, err := bt.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)})
	if err != nil {
		t.Skip("no "+name+": ", err)
	}
	_ = b.Close()
}

// shellServer serves the skeleton shell with js, as an origin of its own.
func shellServer(t *testing.T, mutate func(string) string) (string, Decl) {
	t.Helper()
	d := Decl{Org: org, Repo: org + ".github.io", Parents: []string{parent}, Shells: []string{"/shell.html"}, CSP: SkeletonCSP}
	sk := skeleton(d)
	js := sk["shell.js"]
	if mutate != nil {
		m := mutate(js)
		if m == js {
			t.Fatal("the mutation changed nothing")
		}
		js = m
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/shell.html":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(sk["shell.html"]))
		case "/shell.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write([]byte(js))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/shell.html", d
}

// verdicts maps each scenario to its verdict.
func verdicts(fs []finding) (pass map[string]bool, failed []string, other []string) {
	pass = map[string]bool{}
	for _, f := range fs {
		switch f.v {
		case 0:
			pass[f.check] = true
		case 1:
			failed = append(failed, f.check+": "+f.detail)
		default:
			other = append(other, f.check+": "+f.detail)
		}
	}
	return
}

func runBrowser(t *testing.T, browser string, mutate func(string) string) (passed map[string]bool, failed, other []string) {
	t.Helper()
	needBrowser(t, browser)
	url, d := shellServer(t, mutate)
	return verdicts(runBehaviour(d, url, options{browsers: []string{browser}}))
}

func TestTheRunPassesTheSkeletonAndEveryScenarioRan(t *testing.T) {
	for _, bn := range []string{"chromium", "firefox"} {
		t.Run(bn, func(t *testing.T) {
			passed, failed, other := runBrowser(t, bn, nil)
			if len(failed) > 0 || len(other) > 0 {
				t.Fatalf("failed %v, not run %v", failed, other)
			}
			// the scenarios that make it a test: the parent answered, and six refusals
			if len(passed) != 7 {
				t.Fatalf("%d scenarios passed, want 7: %v", len(passed), passed)
			}
		})
	}
}

// Each mutation leaves the shell looking right to a reader and breaks ONE thing
// the browser can see. Each must fail the scenario that sees it.
func TestTheRunFailsWhatEachMutationBreaks(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(string) string
		want   string // a scenario whose failure is the point
	}{
		{"the origin test dropped (fleettools#243, 1)", func(s string) string {
			return strings.Replace(s, "if (ALLOW.indexOf(e.origin) < 0 || e.source !== window.parent) { return; }", "if (e.source !== window.parent) { return; }", 1)
		}, "a foreign origin framing the shell gets nothing"},
		{"the whole guard removed (fleettools#243, 2)", func(s string) string {
			return strings.Replace(s, "if (ALLOW.indexOf(e.origin) < 0 || e.source !== window.parent) { return; }", "", 1)
		}, "a foreign origin framing the shell gets nothing"},
		{"the source test dropped", func(s string) string {
			return strings.Replace(s, "if (ALLOW.indexOf(e.origin) < 0 || e.source !== window.parent) { return; }", "if (ALLOW.indexOf(e.origin) < 0) { return; }", 1)
		}, "a sibling frame of the declared parent's origin gets nothing"},
		{"the nonce test dropped", func(s string) string {
			return strings.Replace(s, "m.nonce !== nonce || ", "", 1)
		}, "a hello with the wrong nonce gets nothing"},
		{"the hello gate dropped", func(s string) string {
			return strings.Replace(s, "if (from !== e.origin || typeof m.id !== \"string\") { return; }", "if (typeof m.id !== \"string\") { return; }\n    from = from || e.origin;", 1)
		}, "a command with no hello gets no reply"},
		// not visible to a reader of the guard at all: a second path, no guard
		{"an unguarded onmessage next to a good guard", func(s string) string {
			return strings.Replace(s, "})();", "  window.onmessage = function (e) { e.source.postMessage({ t: \"leak\", id: \"p1\" }, e.origin); };\n})();", 1)
		}, "a foreign origin framing the shell gets nothing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, failed, other := runBrowser(t, "chromium", tc.mutate)
			if len(other) > 0 {
				t.Fatalf("not run: %v", other)
			}
			hit := false
			for _, f := range failed {
				if strings.Contains(f, tc.want) {
					hit = true
				}
			}
			if !hit {
				t.Fatalf("no failure of %q; failed: %v", tc.want, failed)
			}
		})
	}
}

// Without a browser the run is NOT VERIFIED, never a pass.
func TestNoBrowserIsNotVerified(t *testing.T) {
	url, d := shellServer(t, nil)
	fs := runBehaviour(d, url, options{browsers: []string{"netscape"}})
	if len(fs) != 1 || fs[0].v != unverified && !strings.Contains(fs[0].detail, "unknown browser") {
		t.Fatalf("%+v", fs)
	}
	if status(fs) == 0 {
		t.Fatal("an unrun check passed")
	}
}
