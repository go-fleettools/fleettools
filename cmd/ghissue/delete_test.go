package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// delServer answers the REST read and the GraphQL mutation, and records both.
type delServer struct {
	*httptest.Server
	paths   []string
	queries []string
	vars    []map[string]string
	reply   string // what /graphql answers
	issue   string // what the REST read answers
}

func newDelServer(t *testing.T) *delServer {
	t.Helper()
	s := &delServer{
		issue: `{"number":735,"state":"open","title":"engine: a report","node_id":"I_abc",` +
			`"html_url":"https://github.com/o/r/issues/735"}`,
		reply: `{"data":{"deleteIssue":{"repository":{"nameWithOwner":"o/r"}}}}`,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/issues/735", func(w http.ResponseWriter, r *http.Request) {
		s.paths = append(s.paths, r.URL.Path)
		_, _ = io.WriteString(w, s.issue)
	})
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		s.paths = append(s.paths, r.URL.Path)
		var in struct {
			Query     string            `json:"query"`
			Variables map[string]string `json:"variables"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &in)
		s.queries = append(s.queries, in.Query)
		s.vars = append(s.vars, in.Variables)
		_, _ = io.WriteString(w, s.reply)
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func del(t *testing.T, s *delServer, extra ...string) (string, error) {
	t.Helper()
	args := append([]string{"o/r", "735", "--delete", "--base", s.URL,
		"--token-file", tokenFile(t, "k")}, extra...)
	return exec(t, args...)
}

// ⛔⛔ THE ACT guard-bash NAMED AS MISSING on 2026-11-09, when three issues had
// to be deleted and re-created because an edit does not undo a published path.
// A missing tool is added, never worked around with another spelling.
func TestDeleteSendsTheMutationWithTheNodeIDAsAVariable(t *testing.T) {
	s := newDelServer(t)
	out, err := del(t, s, "--confirm", "o/r#735")
	if err != nil {
		t.Fatalf("delete failed: %v\n%s", err, out)
	}
	if len(s.queries) != 1 {
		t.Fatalf("sent %d mutations, want 1", len(s.queries))
	}
	if !strings.Contains(s.queries[0], "deleteIssue") {
		t.Errorf("query = %q", s.queries[0])
	}
	// ⛔ The id travels as a VARIABLE, not interpolated into the document: a
	// GraphQL document built by concatenation is an injection waiting for an id
	// with a quote in it.
	if s.vars[0]["id"] != "I_abc" {
		t.Errorf("variables = %v", s.vars[0])
	}
	if strings.Contains(s.queries[0], "I_abc") {
		t.Errorf("the id was interpolated into the document: %q", s.queries[0])
	}
	// The issue is shown BEFORE it goes: the only protection against deleting
	// the wrong number is seeing what is about to be destroyed.
	if !strings.Contains(out, "o/r#735") || !strings.Contains(out, "engine: a report") {
		t.Errorf("the issue was not shown before deletion:\n%s", out)
	}
	if !strings.Contains(out, "re-file it") {
		t.Errorf("nothing said what remains to be done:\n%s", out)
	}
}

// ⛔ Permanent, with no undo: the confirmation is the reference RETYPED, because
// a boolean --yes is one keystroke and cannot be given without looking at which
// issue is being deleted.
func TestDeleteRefusesWithoutTheRetypedReference(t *testing.T) {
	s := newDelServer(t)
	out, err := del(t, s)
	if err == nil {
		t.Fatalf("deleted without confirmation:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--confirm o/r#735") {
		t.Errorf("the refusal does not say how to confirm: %v", err)
	}
	if len(s.paths) != 0 {
		t.Errorf("something was sent before the refusal: %v", s.paths)
	}
}

// A slipped digit is the failure the retyping exists to catch.
func TestDeleteRefusesAConfirmationForAnotherIssue(t *testing.T) {
	s := newDelServer(t)
	if _, err := del(t, s, "--confirm", "o/r#736"); err == nil {
		t.Error("a confirmation naming another issue was accepted")
	}
	if len(s.paths) != 0 {
		t.Errorf("something was sent: %v", s.paths)
	}
}

// ⛔⛔ A GraphQL REFUSAL ARRIVES AS HTTP 200 WITH AN `errors` ARRAY. Reading
// past that turns a refused mutation into a reported success — the same trap
// this repository recorded in priorart, where it made an unread surface look
// like a clean gate.
func TestDeleteReadsGraphQLErrorsBehindA200(t *testing.T) {
	s := newDelServer(t)
	s.reply = `{"data":{"deleteIssue":null},"errors":[{"type":"FORBIDDEN","message":"Must have admin access"}]}`
	out, err := del(t, s, "--confirm", "o/r#735")
	if err == nil {
		t.Fatalf("a refused mutation was reported as done:\n%s", out)
	}
	if !strings.Contains(err.Error(), "FORBIDDEN") || !strings.Contains(err.Error(), "admin access") {
		t.Errorf("the refusal does not quote GitHub: %v", err)
	}
	if strings.Contains(out, "deleted") {
		t.Errorf("it claimed the deletion:\n%s", out)
	}
}

// ⛔ No error AND no result is not a success either: the mutation returns the
// repository it deleted from, so a reply with neither means the document never
// ran, and reporting it as done is reporting work that did not happen.
func TestDeleteRefusesAnEmptyReply(t *testing.T) {
	s := newDelServer(t)
	s.reply = `{"data":{}}`
	if _, err := del(t, s, "--confirm", "o/r#735"); err == nil {
		t.Error("an empty reply was read as a deletion")
	}
}

// A pull request is an issue to REST and is not deletable. Saying so here stops
// the reader blaming their token for a refusal GitHub was always going to give.
func TestDeleteRefusesAPullRequest(t *testing.T) {
	s := newDelServer(t)
	s.issue = `{"number":735,"state":"open","title":"a pull","node_id":"I_abc",` +
		`"pull_request":{"url":"https://api.github.com/repos/o/r/pulls/735"}}`
	_, err := del(t, s, "--confirm", "o/r#735")
	if err == nil {
		t.Fatal("a pull request was sent to deleteIssue")
	}
	if !strings.Contains(err.Error(), "pull request") {
		t.Errorf("the refusal does not say which: %v", err)
	}
	for _, p := range s.paths {
		if p == "/graphql" {
			t.Error("the mutation was sent anyway")
		}
	}
}

func TestDeleteDryRunSendsOnlyTheRead(t *testing.T) {
	s := newDelServer(t)
	out, err := del(t, s, "--confirm", "o/r#735", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range s.paths {
		if p == "/graphql" {
			t.Error("--dry-run sent the mutation")
		}
	}
	if !strings.Contains(out, "nothing was sent") {
		t.Errorf("--dry-run did not say so:\n%s", out)
	}
}

// --delete beside another act is a line whose author meant one of them.
func TestDeleteWithAnotherActIsRefused(t *testing.T) {
	s := newDelServer(t)
	for _, extra := range [][]string{
		{"--confirm", "o/r#735", "--close"},
		{"--confirm", "o/r#735", "--body-file", bodyFile(t, "x")},
		{"--confirm", "o/r#735", "--title", "t"},
	} {
		if _, err := del(t, s, extra...); err == nil {
			t.Errorf("--delete was accepted with %v", extra)
		}
	}
	if _, err := exec(t, "o/r", "--new", "--delete", "--title", "t"); err == nil {
		t.Error("--new and --delete together were accepted")
	}
	if len(s.paths) != 0 {
		t.Errorf("something was sent: %v", s.paths)
	}
}
