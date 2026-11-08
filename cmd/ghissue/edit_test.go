package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// editServer holds issue 7 with labels bug and docs and records every request.
func editServer(t *testing.T) *server {
	t.Helper()
	s := &server{}
	labels := []string{"bug", "docs"}
	title := "a title"
	render := func() string {
		var sb strings.Builder
		for i, l := range labels {
			if i > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(`{"name":"` + l + `"}`)
		}
		return "[" + sb.String() + "]"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/issues/7/labels", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if !strings.Contains(s.bodies[len(s.bodies)-1], "newlab") {
			t.Errorf("unexpected label body %q", s.bodies[len(s.bodies)-1])
		}
		labels = append(labels, "newlab")
		_, _ = io.WriteString(w, render())
	})
	mux.HandleFunc("/repos/o/r/issues/7/labels/bug", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		kept := []string{}
		for _, l := range labels {
			if l != "bug" {
				kept = append(kept, l)
			}
		}
		labels = kept
		_, _ = io.WriteString(w, render())
	})
	mux.HandleFunc("/repos/o/r/issues/7", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if r.Method == http.MethodPatch && strings.Contains(s.bodies[len(s.bodies)-1], "New title") {
			title = "New title"
		}
		_, _ = io.WriteString(w, `{"number":7,"state":"open","title":"`+title+`","html_url":"https://github.com/o/r/issues/7","labels":`+render()+`}`)
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func TestEditSendsTitleBodyAndLabelsAndReportsTheReply(t *testing.T) {
	s := editServer(t)
	out, err := exec(t, "o/r", "7", "--edit", "--base", s.URL, "--token-file", tokenFile(t, "SEKRIT"),
		"--title", "New title", "--body-file", bodyFile(t, "fresh body"),
		"--add-label", "newlab", "--remove-label", "bug")
	if err != nil {
		t.Fatal(err)
	}
	wantM := []string{"GET", "PATCH", "POST", "DELETE"}
	wantP := []string{"/repos/o/r/issues/7", "/repos/o/r/issues/7", "/repos/o/r/issues/7/labels", "/repos/o/r/issues/7/labels/bug"}
	for i := range wantM {
		if s.methods[i] != wantM[i] || s.paths[i] != wantP[i] {
			t.Errorf("request %d: %s %s, want %s %s", i, s.methods[i], s.paths[i], wantM[i], wantP[i])
		}
	}
	if s.bodies[1] != `{"body":"fresh body","title":"New title"}` {
		t.Errorf("PATCH body %q", s.bodies[1])
	}
	if s.bodies[2] != `{"labels":["newlab"]}` {
		t.Errorf("label body %q", s.bodies[2])
	}
	for i, a := range s.auth {
		if a != "Bearer SEKRIT" {
			t.Errorf("request %d auth %q", i, a)
		}
	}
	for _, want := range []string{"labels: bug, docs", "title: New title", "body: replaced", "labels: docs, newlab"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestEditDryRunSendsOnlyReads(t *testing.T) {
	s := editServer(t)
	out, err := exec(t, "o/r", "7", "--edit", "--dry-run", "--base", s.URL, "--token-file", tokenFile(t, "x"),
		"--title", "New title", "--add-label", "newlab")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.methods) != 1 || s.methods[0] != "GET" {
		t.Errorf("dry run sent %v", s.methods)
	}
	if !strings.Contains(out, "would set title") || !strings.Contains(out, "would add labels: newlab") {
		t.Errorf("output:\n%s", out)
	}
}

func TestEditRefusals(t *testing.T) {
	s := editServer(t)
	tf := tokenFile(t, "x")
	cases := map[string][]string{
		"nothing to edit":     {"o/r", "7", "--edit"},
		"both added and rem":  {"o/r", "7", "--edit", "--add-label", "A", "--remove-label", "a"},
		"close":               {"o/r", "7", "--edit", "--title", "t", "--close"},
		"--new":               {"o/r", "--new", "--edit", "--title", "t"},
		"labels without edit": {"o/r", "7", "--add-label", "x"},
		"title without edit":  {"o/r", "7", "--title", "x"},
		"multi-line label":    {"o/r", "7", "--edit", "--add-label", "a\nb"},
		"two-line title":      {"o/r", "7", "--edit", "--title", "a\nb"},
		"no number":           {"o/r", "--edit", "--title", "t"},
		"absent label":        {"o/r", "7", "--edit", "--remove-label", "nope"},
	}
	for name, a := range cases {
		before := 0
		for _, m := range s.methods {
			if m != "GET" {
				before++
			}
		}
		_, err := exec(t, append(a, "--base", s.URL, "--token-file", tf)...)
		if err == nil {
			t.Errorf("%s: want a refusal", name)
		}
		after := 0
		for _, m := range s.methods {
			if m != "GET" {
				after++
			}
		}
		if after != before {
			t.Errorf("%s: a write was sent before the refusal", name)
		}
	}
}

func TestEditRemoveLabelIsEscapedInThePath(t *testing.T) {
	mux := http.NewServeMux()
	var raw string
	mux.HandleFunc("/repos/o/r/issues/7", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"number":7,"state":"open","title":"t","html_url":"u","labels":[{"name":"good first/issue"}]}`)
	})
	mux.HandleFunc("/repos/o/r/issues/7/labels/", func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.EscapedPath()
		_, _ = io.WriteString(w, `[]`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if _, err := exec(t, "o/r", "7", "--edit", "--remove-label", "good first/issue", "--base", srv.URL, "--token-file", tokenFile(t, "x")); err != nil {
		t.Fatal(err)
	}
	if raw != "/repos/o/r/issues/7/labels/good%20first%2Fissue" {
		t.Errorf("path %q", raw)
	}
}
