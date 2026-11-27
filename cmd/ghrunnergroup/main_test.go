package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fake struct {
	*httptest.Server
	reqs   []string
	bodies []string
	groups string // JSON array
	badPut bool
}

func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{groups: `[{"id":1,"name":"Default","visibility":"all","default":true}]`}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.reqs = append(f.reqs, r.Method+" "+r.URL.Path)
		f.bodies = append(f.bodies, string(b))
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/orgs/o/actions/runner-groups":
			n := strings.Count(f.groups, `"id"`)
			fmt.Fprintf(w, `{"total_count":%d,"runner_groups":%s}`, n, f.groups)
		case r.Method == "GET" && r.URL.Path == "/repos/o/r1":
			_, _ = io.WriteString(w, `{"id":11}`)
		case r.Method == "GET" && r.URL.Path == "/repos/o/noid":
			_, _ = io.WriteString(w, `{}`)
		case r.Method == "GET" && r.URL.Path == "/repos/o/bad":
			_, _ = io.WriteString(w, `not json`)
		case r.Method == "POST":
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"id":7,"name":"g","visibility":"selected"}`)
		case r.Method == "PUT":
			if f.badPut {
				w.WriteHeader(422)
				_, _ = io.WriteString(w, "nope\nmore")
				return
			}
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func exec(t *testing.T, f *fake, args ...string) (string, error) {
	t.Helper()
	old := readToken
	readToken = func(string) (string, error) { return "tok", nil }
	t.Cleanup(func() { readToken = old })
	cmd := RootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append(args, "--base", f.URL))
	err := cmd.Execute()
	return out.String(), err
}

func TestList(t *testing.T) {
	f := newFake(t)
	out, err := exec(t, f, "o", "list")
	if err != nil || !strings.Contains(out, "1\tDefault\tvisibility=all\tdefault=true") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestEnsureCreates(t *testing.T) {
	f := newFake(t)
	out, err := exec(t, f, "o", "ensure", "g", "--repos", "r1", "--allow-public")
	if err != nil || !strings.Contains(out, "created group 7") {
		t.Fatalf("%q %v", out, err)
	}
	last := f.bodies[len(f.bodies)-1]
	for _, w := range []string{`"selected_repository_ids":[11]`, `"visibility":"selected"`, `"allows_public_repositories":true`} {
		if !strings.Contains(last, w) {
			t.Errorf("body %s lacks %s", last, w)
		}
	}
}

func TestEnsureDryRun(t *testing.T) {
	f := newFake(t)
	out, err := exec(t, f, "o", "ensure", "g", "--repos", "r1", "--dry-run")
	if err != nil || !strings.Contains(out, "would create") {
		t.Fatalf("%q %v", out, err)
	}
	for _, r := range f.reqs {
		if strings.HasPrefix(r, "POST") || strings.HasPrefix(r, "PUT") {
			t.Fatalf("dry run wrote: %s", r)
		}
	}
}

func TestEnsureExistingAddsRepos(t *testing.T) {
	f := newFake(t)
	f.groups = `[{"id":5,"name":"g","visibility":"selected","allows_public_repositories":true}]`
	out, err := exec(t, f, "o", "ensure", "g", "--repos", "r1")
	if err != nil || !strings.Contains(out, "r1 added") || !strings.Contains(out, "not changed") {
		t.Fatalf("%q %v", out, err)
	}
	if f.reqs[len(f.reqs)-1] != "PUT /orgs/o/actions/runner-groups/5/repositories/11" {
		t.Fatalf("%v", f.reqs)
	}
	out, err = exec(t, f, "o", "ensure", "g", "--repos", "r1", "--allow-public", "--dry-run")
	if err != nil || !strings.Contains(out, "would add r1") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestEnsureRefusals(t *testing.T) {
	f := newFake(t)
	f.groups = `[{"id":5,"name":"open","visibility":"all"},{"id":6,"name":"sel","visibility":"selected"}]`
	cases := [][]string{
		{"o", "ensure", "open", "--repos", "r1"},   // visibility all
		{"o", "ensure", "g"},                       // no repos
		{"o", "ensure", "g", "--repos", "a/b"},     // bad repo
		{"o", "ensure"},                            // no name
		{"o", "ensure", " ", "--repos", "r1"},      // blank name
		{"o", "list", "x"},                         // extra arg
		{"o", "frob"},                              // verb
		{"o/x", "list"},                            // org
		{"o", "ensure", "g", "--repos", "noid"},    // no id
		{"o", "ensure", "g", "--repos", "bad"},     // bad json
		{"o", "ensure", "g", "--repos", "missing"}, // 404
	}
	cmd := RootCmd()
	cmd.SetArgs([]string{"o", "list", "--base", "http://example.com"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "https only") {
		t.Errorf("http base: %v", err)
	}
	for _, c := range cases {
		if _, err := exec(t, f, c...); err == nil {
			t.Errorf("%v: want an error", c)
		}
	}
	f.badPut = true
	if _, err := exec(t, f, "o", "ensure", "sel", "--repos", "r1"); err == nil || !strings.Contains(err.Error(), "422") || strings.Contains(err.Error(), "more") {
		t.Errorf("put failure: %v", err)
	}
}

func TestTokenAndTransportErrors(t *testing.T) {
	f := newFake(t)
	old := readToken
	defer func() { readToken = old }()
	readToken = func(string) (string, error) { return "", fmt.Errorf("no token") }
	cmd := RootCmd()
	cmd.SetArgs([]string{"o", "list", "--base", f.URL})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err == nil {
		t.Fatal("want token error")
	}
	readToken = func(string) (string, error) { return "wrong", nil }
	if _, err := listGroups(newClient(timeout), f.URL, "wrong", "o"); err == nil {
		t.Fatal("want 401")
	}
	f.Close()
	if _, err := listGroups(newClient(timeout), f.URL, "tok", "o"); err == nil {
		t.Fatal("want transport error")
	}
	if _, err := do(newClient(timeout), "GET", "http://[bad", "t", nil, 200); err == nil {
		t.Fatal("want url error")
	}
}

func TestListPagesAndMalformed(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = io.WriteString(w, `{"total_count":2,"runner_groups":[{"id":1,"name":"a"}]}`)
		case "2":
			_, _ = io.WriteString(w, `{"total_count":2,"runner_groups":[{"id":2,"name":"b"}]}`)
		default:
			_, _ = io.WriteString(w, `{`)
		}
	}))
	defer srv.Close()
	g, err := listGroups(newClient(timeout), srv.URL, "t", "o")
	if err != nil || len(g) != 2 || calls != 2 {
		t.Fatalf("%v %v %d", g, err, calls)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{`) }))
	defer bad.Close()
	if _, err := listGroups(newClient(timeout), bad.URL, "t", "o"); err == nil {
		t.Fatal("want decode error")
	}
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"total_count":9,"runner_groups":[]}`)
	}))
	defer empty.Close()
	if g, err := listGroups(newClient(timeout), empty.URL, "t", "o"); err != nil || len(g) != 0 {
		t.Fatalf("%v %v", g, err)
	}
}

func TestRedirectRefusedAndHelpers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/", 302)
	}))
	defer srv.Close()
	if _, err := do(newClient(timeout), "GET", srv.URL, "t", nil, 200); err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatalf("%v", err)
	}
	if firstLine(strings.Repeat("x", 400)) == strings.Repeat("x", 400) {
		t.Fatal("not truncated")
	}
}

func TestCreateDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/"):
			_, _ = io.WriteString(w, `{"id":3}`)
		case r.Method == "POST":
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{`)
		default:
			_, _ = io.WriteString(w, `{"total_count":0,"runner_groups":[]}`)
		}
	}))
	defer srv.Close()
	old := readToken
	readToken = func(string) (string, error) { return "t", nil }
	defer func() { readToken = old }()
	cmd := RootCmd()
	cmd.SetArgs([]string{"o", "ensure", "g", "--repos", "r", "--base", srv.URL})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err == nil {
		t.Fatal("want decode error")
	}
}
