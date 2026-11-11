package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	org    = "acme-usercontent"
	parent = "https://acme.github.io"
)

// fake is GitHub and the second origin's Pages site in one server.
type fake struct {
	mu    sync.Mutex
	orgs  map[string]string // path -> body, status in codes
	codes map[string]int
	body  map[string]string
	head  map[string]map[string]string
	hits  map[string]int
}

func newFake(t *testing.T) (*fake, *httptest.Server) {
	t.Helper()
	f := &fake{codes: map[string]int{}, body: map[string]string{}, head: map[string]map[string]string{}, hits: map[string]int{}}
	f.body["/orgs/"+org] = `{"type":"Organization"}`
	f.body["/repos/"+org+"/"+org+".github.io"] = `{"private":false,"archived":false,"has_pages":true}`
	sk := skeleton(Decl{Org: org, Repo: org + ".github.io", Parents: []string{parent}, Shells: []string{"/shell.html"}, CSP: SkeletonCSP})
	f.body["/shell.html"] = sk["shell.html"]
	f.body["/shell.js"] = sk["shell.js"]
	f.body["/orgs/"+org+"/repos"] = `[{"name":"` + org + `.github.io","has_pages":true,"private":false}]`
	// no browser in these tests: the run of the frame has its own (behaviour_test.go)
	oldB := behaviour
	behaviour = func(Decl, string, options) []finding { return []finding{{pass, "stub run", "no browser here"}} }
	t.Cleanup(func() { behaviour = oldB })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.hits[r.URL.Path]++
		for k, v := range f.head[r.URL.Path] {
			w.Header().Set(k, v)
		}
		if loc, ok := f.head[r.URL.Path]["Location"]; ok && loc != "" {
			w.WriteHeader(302)
			return
		}
		if c := f.codes[r.URL.Path]; c != 0 {
			w.WriteHeader(c)
			return
		}
		b, ok := f.body[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(b))
	}))
	t.Cleanup(srv.Close)
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = old })
	return f, srv
}

func decl(t *testing.T, mut func(*Decl)) string {
	t.Helper()
	d := Decl{Org: org, Repo: org + ".github.io", Parents: []string{parent}, Shells: []string{"/shell.html"}, CSP: SkeletonCSP}
	if mut != nil {
		mut(&d)
	}
	b, _ := json.Marshal(d)
	p := filepath.Join(t.TempDir(), DeclFile)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func verifyRun(t *testing.T, srv *httptest.Server, file string, extra ...string) (int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	args := append([]string{"verify", "--file", file, "--live-url", srv.URL, "--wait", "1ms"}, extra...)
	code := run(args, &out, &errb)
	return code, out.String() + errb.String()
}

func TestVerifyPasses(t *testing.T) {
	_, srv := newFake(t)
	code, out := verifyRun(t, srv, decl(t, nil))
	if code != 0 || !strings.Contains(out, "verified") || strings.Contains(out, "FAIL") || strings.Contains(out, "UNVERIFIED") {
		t.Fatalf("code %d:\n%s", code, out)
	}
	for _, want := range []string{"PASS       org", "PASS       repo", "PASS       shell /shell.html:", "csp", "parents"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
}

// Every drift below is a negative control: the same fixture with ONE thing
// changed must be exit 1 and name that thing.
func TestVerifyDrift(t *testing.T) {
	sk := func(f *fake) string { return f.body["/shell.js"] }
	cases := []struct {
		name string
		mut  func(*fake)
		d    func(*Decl)
		want string
	}{
		{"org gone", func(f *fake) { f.codes["/orgs/"+org] = 404 }, nil, "org: " + org + " does not exist"},
		{"org is a user", func(f *fake) { f.body["/orgs/"+org] = `{"type":"User"}` }, nil, "not an organisation"},
		{"repo gone", func(f *fake) { f.codes["/repos/"+org+"/"+org+".github.io"] = 404 }, nil, "does not exist"},
		{"repo private", func(f *fake) {
			f.body["/repos/"+org+"/"+org+".github.io"] = `{"private":true,"archived":false,"has_pages":true}`
		}, nil, "is private"},
		{"repo archived", func(f *fake) {
			f.body["/repos/"+org+"/"+org+".github.io"] = `{"private":false,"archived":true,"has_pages":true}`
		}, nil, "is archived"},
		{"no pages", func(f *fake) {
			f.body["/repos/"+org+"/"+org+".github.io"] = `{"private":false,"archived":false,"has_pages":false}`
		}, nil, "no Pages site"},
		{"shell 404", func(f *fake) { delete(f.body, "/shell.html") }, nil, "404: nothing is served"},
		{"csp differs", func(f *fake) {
			f.body["/shell.html"] = strings.Replace(f.body["/shell.html"], "script-src 'self'", "script-src 'self' https://evil.example", 1)
		}, nil, "csp: served"},
		{"csp missing", func(f *fake) {
			f.body["/shell.html"] = strings.Replace(f.body["/shell.html"], `<meta http-equiv="Content-Security-Policy"`, `<meta name="x"`, 1)
		}, nil, "sets no Content-Security-Policy"},
		{"csp header disagrees", func(f *fake) {
			f.head["/shell.html"] = map[string]string{"Content-Security-Policy": "default-src *"}
		}, nil, "csp: served"},
		{"another origin accepted", func(f *fake) {
			f.body["/shell.js"] = strings.Replace(sk(f), `["https://acme.github.io"]`, `["https://acme.github.io", "https://evil.example"]`, 1)
		}, nil, "undeclared [https://evil.example]"},
		{"declared parent not named", nil, func(d *Decl) { d.Parents = append(d.Parents, "https://other.github.io") }, "missing [https://other.github.io]"},
		{"wrong parent", func(f *fake) {
			f.body["/shell.js"] = strings.Replace(sk(f), `https://acme.github.io`, `https://acme.github.com`, 1)
		}, nil, "missing [https://acme.github.io]"},
		{"wildcard target", func(f *fake) {
			f.body["/shell.js"] = sk(f) + "\nwindow.parent.postMessage({t:'x'}, '*');\n"
		}, nil, `sent to "*"`},
		{"listener without a guard", func(f *fake) {
			f.body["/shell.js"] = `const ALLOW = ["https://acme.github.io"]; addEventListener("message", e => 1);`
		}, nil, "message listener with no body"},
		// fleettools#243: the shell MENTIONS .origin everywhere, and the one test
		// that matters is gone. Each of these passed before.
		{"guard without the origin test", func(f *fake) {
			f.body["/shell.js"] = strings.Replace(sk(f), "if (ALLOW.indexOf(e.origin) < 0 || e.source !== window.parent)", "if (e.source !== window.parent)", 1)
		}, nil, "guard does not test the message's origin"},
		{"guard without the source test", func(f *fake) {
			f.body["/shell.js"] = strings.Replace(sk(f), "if (ALLOW.indexOf(e.origin) < 0 || e.source !== window.parent)", "if (ALLOW.indexOf(e.origin) < 0)", 1)
		}, nil, "guard does not test the message's source"},
		{"guard removed", func(f *fake) {
			f.body["/shell.js"] = strings.Replace(sk(f), "    if (ALLOW.indexOf(e.origin) < 0 || e.source !== window.parent) { return; }\n", "    const unused = e.origin;\n", 1)
		}, nil, "first statement is not an origin guard"},
		{"guard that does not return", func(f *fake) {
			f.body["/shell.js"] = strings.Replace(sk(f), "if (ALLOW.indexOf(e.origin) < 0 || e.source !== window.parent) { return; }", "if (ALLOW.indexOf(e.origin) < 0 || e.source !== window.parent) { console.log(1); }", 1)
		}, nil, "guard does not return"},
		{"a second listener without a guard", func(f *fake) {
			f.body["/shell.js"] = sk(f) + "\nwindow.addEventListener('message', function (e) { e.source.postMessage({t:'x'}, e.origin); });\n"
		}, nil, "first statement is not an origin guard"},
		{"another public repository in the organisation", func(f *fake) {
			f.body["/orgs/"+org+"/repos"] = `[{"name":"` + org + `.github.io","has_pages":true,"private":false},{"name":"notes","has_pages":false,"private":false}]`
		}, nil, "only public repository"},
		{"another Pages site in the organisation", func(f *fake) {
			f.body["/orgs/"+org+"/repos"] = `[{"name":"` + org + `.github.io","has_pages":true,"private":false},{"name":"site","has_pages":true,"private":true}]`
		}, nil, "another Pages site is served on the frame's origin: site (a Pages site)"},
		{"members may create Pages sites", func(f *fake) {
			f.body["/orgs/"+org] = `{"type":"Organization","members_can_create_pages":true,"members_can_create_public_pages":true}`
		}, nil, "members may create Pages sites"},
		{"placeholder left", func(f *fake) {
			f.body["/shell.js"] = sk(f) + "\nvar X = '@@ALLOW@@'; // e.origin\n"
		}, nil, "placeholder was never rendered"},
		{"no script", func(f *fake) {
			f.body["/shell.html"] = strings.Replace(f.body["/shell.html"], `<script src="shell.js" defer></script>`, "", 1)
		}, nil, "has no script"},
		{"script from another origin", func(f *fake) {
			f.body["/shell.html"] = strings.Replace(f.body["/shell.html"], `src="shell.js"`, `src="https://cdn.example/x.js"`, 1)
		}, nil, "outside the second origin"},
		{"script gone", func(f *fake) { delete(f.body, "/shell.js") }, nil, "script shell.js: 404"},
		{"shell redirected elsewhere", func(f *fake) {
			f.head["/shell.html"] = map[string]string{"Location": "https://elsewhere.example/shell.html"}
		}, nil, "redirected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, srv := newFake(t)
			if tc.mut != nil {
				tc.mut(f)
			}
			code, out := verifyRun(t, srv, decl(t, tc.d))
			want := 1
			if tc.name == "shell redirected elsewhere" {
				want = 2 // a redirect off the origin is not a reading of it
			}
			if code != want || !strings.Contains(out, tc.want) {
				t.Fatalf("code %d, want %d with %q:\n%s", code, want, tc.want, out)
			}
			if strings.HasSuffix(strings.TrimSpace(out), "verified") && !strings.Contains(out, "not verified") {
				t.Fatalf("a drift ended as verified:\n%s", out)
			}
		})
	}
}

// Anything unreadable is not a pass.
func TestVerifyUnreadableIsNotVerified(t *testing.T) {
	for _, status := range []int{500, 502, 503, 429, 403} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f, srv := newFake(t)
			f.codes["/orgs/"+org] = status
			code, out := verifyRun(t, srv, decl(t, nil))
			if code != 2 || !strings.Contains(out, "UNVERIFIED") {
				t.Fatalf("code %d:\n%s", code, out)
			}
		})
	}
	t.Run("garbage", func(t *testing.T) {
		f, srv := newFake(t)
		f.body["/repos/"+org+"/"+org+".github.io"] = `<html>`
		if code, out := verifyRun(t, srv, decl(t, nil)); code != 2 {
			t.Fatalf("code %d:\n%s", code, out)
		}
	})
	t.Run("shell 503", func(t *testing.T) {
		f, srv := newFake(t)
		f.codes["/shell.html"] = 503
		if code, out := verifyRun(t, srv, decl(t, nil)); code != 2 {
			t.Fatalf("code %d:\n%s", code, out)
		}
	})
	t.Run("connection refused", func(t *testing.T) {
		_, srv := newFake(t)
		url := srv.URL
		srv.Close()
		var out, e bytes.Buffer
		if code := run([]string{"verify", "--file", decl(t, nil), "--live-url", url}, &out, &e); code != 2 {
			t.Fatalf("code %d:\n%s", code, out.String())
		}
	})
	t.Run("a fail beats an unverified", func(t *testing.T) {
		f, srv := newFake(t)
		f.codes["/orgs/"+org] = 503
		f.codes["/repos/"+org+"/"+org+".github.io"] = 404
		if code, _ := verifyRun(t, srv, decl(t, nil)); code != 1 {
			t.Fatalf("code %d", code)
		}
	})
}

func TestVerifyRetriesOnlyWhatIsUnverified(t *testing.T) {
	f, srv := newFake(t)
	f.codes["/orgs/"+org] = 503
	old := sleep
	sleep = func(time.Duration) {
		f.mu.Lock()
		delete(f.codes, "/orgs/"+org) // the API recovers
		f.mu.Unlock()
	}
	defer func() { sleep = old }()
	if code, out := verifyRun(t, srv, decl(t, nil), "--retry", "2"); code != 0 {
		t.Fatalf("code %d:\n%s", code, out)
	}
	// A drift is not retried: the second run would only wait.
	f2, srv2 := newFake(t)
	delete(f2.body, "/shell.html")
	n := 0
	sleep = func(time.Duration) { n++ }
	if code, _ := verifyRun(t, srv2, decl(t, nil), "--retry", "3"); code != 1 || n != 0 {
		t.Fatalf("code %d, slept %d times", code, n)
	}
}

func TestVerifyDeclaration(t *testing.T) {
	_, srv := newFake(t)
	bad := map[string]string{
		"misspelt key":     `{"org":"acme-usercontent","repo":"acme-usercontent.github.io","parent":["https://acme.github.io"],"shells":["/shell.html"],"csp":"default-src 'none'"}`,
		"no parents":       `{"org":"acme-usercontent","repo":"acme-usercontent.github.io","parents":[],"shells":["/shell.html"],"csp":"default-src 'none'"}`,
		"not usercontent":  `{"org":"acme","repo":"acme.github.io","parents":["https://acme.github.io"],"shells":["/shell.html"],"csp":"default-src 'none'"}`,
		"wrong repo":       `{"org":"acme-usercontent","repo":"other","parents":["https://acme.github.io"],"shells":["/shell.html"],"csp":"default-src 'none'"}`,
		"parent with path": `{"org":"acme-usercontent","repo":"acme-usercontent.github.io","parents":["https://acme.github.io/x"],"shells":["/shell.html"],"csp":"default-src 'none'"}`,
		"parent http":      `{"org":"acme-usercontent","repo":"acme-usercontent.github.io","parents":["http://acme.github.io"],"shells":["/shell.html"],"csp":"default-src 'none'"}`,
		"parent is itself": `{"org":"acme-usercontent","repo":"acme-usercontent.github.io","parents":["https://acme-usercontent.github.io"],"shells":["/shell.html"],"csp":"default-src 'none'"}`,
		"shell traversal":  `{"org":"acme-usercontent","repo":"acme-usercontent.github.io","parents":["https://acme.github.io"],"shells":["/../x"],"csp":"default-src 'none'"}`,
		"no csp":           `{"org":"acme-usercontent","repo":"acme-usercontent.github.io","parents":["https://acme.github.io"],"shells":["/shell.html"],"csp":""}`,
		"trailing":         `{"org":"acme-usercontent","repo":"acme-usercontent.github.io","parents":["https://acme.github.io"],"shells":["/shell.html"],"csp":"default-src 'none'"} {}`,
		"not json":         `org: acme`,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), DeclFile)
			_ = os.WriteFile(p, []byte(body), 0o644)
			if code, out := verifyRun(t, srv, p); code != 2 || !strings.Contains(out, "declaration is unreadable") {
				t.Fatalf("code %d:\n%s", code, out)
			}
		})
	}
	if code, _ := verifyRun(t, srv, filepath.Join(t.TempDir(), "missing.json")); code != 2 {
		t.Fatalf("a missing declaration: code %d", code)
	}
}

func TestCSPNormalisation(t *testing.T) {
	a := "default-src 'none';  script-src 'self' https://b https://a ; style-src 'unsafe-inline'"
	b := "style-src 'unsafe-inline'; Script-Src https://a 'self' https://b; default-src 'none';"
	if normCSP(a) != normCSP(b) {
		t.Errorf("%q != %q", normCSP(a), normCSP(b))
	}
	if normCSP(a) == normCSP(a+"; frame-src *") {
		t.Error("an added directive is not a difference")
	}
	if normCSP("") != "" || normCSP(" ; ;") != "" {
		t.Error("an empty policy is not empty")
	}
}

func TestStringLiterals(t *testing.T) {
	src := "// see \"https://spec.example\"\n" +
		"/* 'https://c.example' */ var a = \"https://a.example\"; var b = 'it\\'s'; var c = `https://t.example/x`;\n" +
		"var d = \"unterminated\nvar e = \"https://e.example\"; // trailing \"https://z.example\""
	var got []string
	for _, l := range stringLiterals(src) {
		if o, ok := originOf(l); ok {
			got = append(got, o)
		}
	}
	want := "https://a.example https://t.example https://e.example"
	if strings.Join(got, " ") != want {
		t.Errorf("got %v want %s", got, want)
	}
	if _, ok := originOf("https://x.example is a place"); ok {
		t.Error("prose is not an origin")
	}
	if _, ok := originOf("not a url"); ok {
		t.Error("prose is not an origin")
	}
}

// ---- init ----

func initRun(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out, e bytes.Buffer
	code := run(append([]string{"init"}, args...), &out, &e)
	return code, out.String() + e.String()
}

// The skeleton init writes is a shell that verify accepts: the two tools agree.
func TestInitWritesWhatVerifyAccepts(t *testing.T) {
	f, srv := newFake(t)
	dir, decl := filepath.Join(t.TempDir(), "site"), filepath.Join(t.TempDir(), DeclFile)
	code, out := initRun(t, "--org", org, "--parent", parent, "--dir", dir, "--decl", decl)
	if code != 0 {
		t.Fatalf("code %d:\n%s", code, out)
	}
	for _, n := range []string{"index.html", "shell.html", "shell.js", "README.md", ".github/workflows/pages.yml"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%s: %v", n, err)
		}
	}
	// serve exactly what was written
	for _, n := range []string{"shell.html", "shell.js"} {
		b, _ := os.ReadFile(filepath.Join(dir, n))
		f.body["/"+n] = string(b)
	}
	if code, out := verifyRun(t, srv, decl); code != 0 {
		t.Fatalf("verify of the skeleton: code %d:\n%s", code, out)
	}
	// and refuses to overwrite
	if code, out := initRun(t, "--org", org, "--parent", parent, "--dir", dir, "--decl", ""); code != 2 || !strings.Contains(out, "does not overwrite") {
		t.Fatalf("second run: code %d:\n%s", code, out)
	}
}

func TestInitWithoutTheOrganisationSaysWhatToCreate(t *testing.T) {
	f, _ := newFake(t)
	f.codes["/orgs/"+org] = 404
	dir := filepath.Join(t.TempDir(), "site")
	old := runGhnew
	runGhnew = func([]string, io.Writer) error { t.Error("ghnew ran for a missing organisation"); return nil }
	defer func() { runGhnew = old }()
	code, out := initRun(t, "--org", org, "--parent", parent, "--dir", dir, "--create")
	if code != 1 {
		t.Fatalf("code %d:\n%s", code, out)
	}
	for _, want := range []string{"does not exist", "organizations/new", "Organization account name: " + org, "does not create"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("files were written for a missing organisation")
	}
}

func TestInitCreatesTheRepositoryWithGhnew(t *testing.T) {
	f, _ := newFake(t)
	delete(f.body, "/repos/"+org+"/"+org+".github.io")
	var got []string
	old := runGhnew
	runGhnew = func(args []string, _ io.Writer) error { got = args; return nil }
	defer func() { runGhnew = old }()
	dir := filepath.Join(t.TempDir(), "site")
	if code, out := initRun(t, "--org", org, "--parent", parent, "--dir", dir, "--decl", "", "--create"); code != 0 {
		t.Fatalf("code %d:\n%s", code, out)
	}
	if len(got) != 3 || got[0] != "-public" || got[1] != org+"/"+org+".github.io" {
		t.Fatalf("ghnew args %v", got)
	}
}

func TestInitRefusesWhatIsNotAnOrigin(t *testing.T) {
	newFake(t)
	for _, args := range [][]string{
		{"--org", "acme", "--parent", parent, "--dir", t.TempDir() + "/x"},
		{"--org", org, "--parent", "http://acme.github.io", "--dir", t.TempDir() + "/x"},
		{"--org", org, "--dir", t.TempDir() + "/x"},
		{"--org", org, "--parent", parent},
		{"--org", org, "--parent", parent, "--dir", t.TempDir() + "/x", "--shell", "/index.html"},
	} {
		if code, out := initRun(t, args...); code != 2 {
			t.Errorf("%v: code %d:\n%s", args, code, out)
		}
	}
}

func TestRootNeedsASubcommand(t *testing.T) {
	var out, e bytes.Buffer
	if code := run(nil, &out, &e); code != 2 {
		t.Fatalf("code %d", code)
	}
	if out.Len() != 0 {
		t.Errorf("stdout is the answer and was written to without one: %q", out.String())
	}
}
