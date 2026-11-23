package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// commentServer answers the one comment endpoint, recording what it was sent.
type commentServer struct {
	body     string // what the comment holds
	gotPatch string // what the last PATCH carried
	reply    string // what the PATCH answers with; empty means "echo the new body"
	patches  int
}

func (s *commentServer) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/issues/comments/") {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{
				"id": 42, "body": s.body, "html_url": "https://example.invalid/c/42",
				"user": map[string]string{"login": "someone"},
			})
		case http.MethodPatch:
			s.patches++
			raw, _ := io.ReadAll(r.Body)
			var in struct {
				Body string `json:"body"`
			}
			json.Unmarshal(raw, &in)
			s.gotPatch = in.Body
			out := in.Body
			if s.reply != "" {
				out = s.reply
			}
			json.NewEncoder(w).Encode(map[string]any{
				"id": 42, "body": out, "html_url": "https://example.invalid/c/42",
			})
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func runGhissue(t *testing.T, args ...string) (string, error) {
	t.Helper()
	c := RootCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs(append(args[:len(args):len(args)], "--instead-of-pr", "test fixture"))
	err := c.Execute()
	return out.String(), err
}

func TestEditCommentReplacesTheBody(t *testing.T) {
	s := &commentServer{body: "the old text"}
	base := s.start(t)
	out, err := runGhissue(t, "o/r", "--edit", "--comment", "42",
		"--body-file", bodyFile(t, "the new text"),
		"--base", base, "--token-file", tokenFile(t, "x"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if s.gotPatch != "the new text" {
		t.Errorf("sent %q, want %q", s.gotPatch, "the new text")
	}
	for _, want := range []string{"comment 42 by someone", "the old text", "does not retract", "replaced"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
}

// ⛔ A 200 says the request was accepted. Only the text says it landed: a server
// that answers with something else must be a failure, not a success.
func TestEditCommentReadsTheReplyNotTheStatus(t *testing.T) {
	s := &commentServer{body: "old", reply: "something else entirely"}
	base := s.start(t)
	out, err := runGhissue(t, "o/r", "--edit", "--comment", "42",
		"--body-file", bodyFile(t, "the new text"),
		"--base", base, "--token-file", tokenFile(t, "x"))
	if err == nil {
		t.Fatalf("a reply that does not hold what was sent must fail:\n%s", out)
	}
	if !strings.Contains(err.Error(), "bytes were sent") && !strings.Contains(err.Error(), "bytes") {
		t.Errorf("the error must compare the two sizes, got: %v", err)
	}
}

func TestEditCommentDryRunSendsNothing(t *testing.T) {
	s := &commentServer{body: "old"}
	base := s.start(t)
	out, err := runGhissue(t, "o/r", "--edit", "--comment", "42", "--dry-run",
		"--body-file", bodyFile(t, "the new text"),
		"--base", base, "--token-file", tokenFile(t, "x"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if s.patches != 0 {
		t.Errorf("--dry-run sent %d PATCH(es), want 0", s.patches)
	}
	if !strings.Contains(out, "NOT sent") {
		t.Errorf("--dry-run must say nothing was sent:\n%s", out)
	}
}

func TestEditCommentRefusals(t *testing.T) {
	s := &commentServer{body: "old"}
	base := s.start(t)
	tok := tokenFile(t, "x")
	body := bodyFile(t, "text")

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"an issue number beside a comment id",
			[]string{"o/r", "7", "--edit", "--comment", "42", "--body-file", body}, "no issue number"},
		{"without --edit",
			[]string{"o/r", "--comment", "42", "--body-file", body}, "needs --edit"},
		{"with --new",
			[]string{"o/r", "--new", "--comment", "42", "--title", "t", "--body-file", body}, "--new opens an issue"},
		{"with --delete",
			[]string{"o/r", "--delete", "--comment", "42"}, "--delete destroys one"},
		{"with --close",
			[]string{"o/r", "--edit", "--comment", "42", "--close", "--body-file", body}, "act on the issue"},
		{"with a title",
			[]string{"o/r", "--edit", "--comment", "42", "--title", "t", "--body-file", body}, "a comment has no title"},
		{"with a label",
			[]string{"o/r", "--edit", "--comment", "42", "--add-label", "bug", "--body-file", body}, "carries none"},
		{"with no body file",
			[]string{"o/r", "--edit", "--comment", "42"}, "wants --body-file"},
		{"a negative id",
			[]string{"o/r", "--edit", "--comment", "-1", "--body-file", body}, "is not a comment id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runGhissue(t, append(tc.args, "--base", base, "--token-file", tok)...)
			if err == nil {
				t.Fatalf("accepted, want a refusal:\n%s", out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %q", err, tc.want)
			}
			if s.patches != 0 {
				t.Fatalf("a refusal sent %d PATCH(es)", s.patches)
			}
		})
	}
}

// The private-path gate covers this path too: a comment is published text like
// any other, and the gate lives in readBody.
func TestEditCommentIsCoveredByThePrivatePathGate(t *testing.T) {
	s := &commentServer{body: "old"}
	base := s.start(t)
	_, err := runGhissue(t, "o/r", "--edit", "--comment", "42",
		"--body-file", bodyFile(t, privateBody),
		"--base", base, "--token-file", tokenFile(t, "x"))
	if err == nil {
		t.Fatal("a body naming a private path must be refused on the comment path too")
	}
	if s.patches != 0 {
		t.Errorf("the gate let %d PATCH(es) through", s.patches)
	}
}

func TestCommentURL(t *testing.T) {
	got := commentURL("https://api.github.com/", "go-zpkg", "artifact", 6958452335)
	want := "https://api.github.com/repos/go-zpkg/artifact/issues/comments/6958452335"
	if got != want {
		t.Errorf("commentURL = %q, want %q", got, want)
	}
}
