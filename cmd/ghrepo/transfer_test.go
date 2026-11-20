package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// transferServer serves o/r, answers 202 on its transfer, and serves the target
// after `after` GETs of it (-1: never; target exists from the start: exists).
type transferServer struct {
	*httptest.Server
	mu           sync.Mutex
	reqs         []string
	bodies       []string
	targetGets   int
	after        int
	exists       bool
	transferCode int
}

func newTransferServer(t *testing.T, target string) *transferServer {
	t.Helper()
	s := &transferServer{transferCode: http.StatusAccepted}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		s.rec(r)
		_, _ = io.WriteString(w, `{"full_name":"o/r","html_url":"https://github.com/o/r","default_branch":"main"}`)
	})
	mux.HandleFunc("/repos/o/r/transfer", func(w http.ResponseWriter, r *http.Request) {
		s.rec(r)
		w.WriteHeader(s.transferCode)
		_, _ = io.WriteString(w, `{"full_name":"o/r"}`)
	})
	mux.HandleFunc("/repos/"+target, func(w http.ResponseWriter, r *http.Request) {
		s.rec(r)
		s.mu.Lock()
		s.targetGets++
		n := s.targetGets
		s.mu.Unlock()
		if s.exists || (s.after >= 0 && n > s.after) {
			_, _ = io.WriteString(w, `{"full_name":"`+target+`","html_url":"https://github.com/`+target+`"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func (s *transferServer) rec(r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, r.Method+" "+r.URL.Path)
	s.bodies = append(s.bodies, string(b))
}

func (s *transferServer) posts() int {
	n := 0
	for _, q := range s.reqs {
		if strings.HasPrefix(q, "POST") {
			n++
		}
	}
	return n
}

func fast(t *testing.T, wait time.Duration) {
	t.Helper()
	oe, ow := transferPollEvery, transferWait
	transferPollEvery, transferWait = time.Millisecond, wait
	t.Cleanup(func() { transferPollEvery, transferWait = oe, ow })
}

func (s *transferServer) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return exec(t, "", append([]string{"--base", s.URL, "--token-file", tokenFile(t, "SEKRIT")}, args...)...)
}

func TestTransferPollsUntilTheTargetAnswers(t *testing.T) {
	fast(t, 5*time.Second)
	s := newTransferServer(t, "org2/r")
	s.after = 3 // 1 existence check + 2 polls answer 404
	out, err := s.run(t, "o/r", "transfer", "org2", "--confirm", "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if s.posts() != 1 || s.bodies[2] != `{"new_owner":"org2"}` {
		t.Errorf("reqs %v bodies %q", s.reqs, s.bodies)
	}
	if s.targetGets != 4 {
		t.Errorf("target read %d times, want 4", s.targetGets)
	}
	for _, w := range []string{"o/r  https://github.com/o/r", "transferred: org2/r  https://github.com/org2/r", "redirects the old URL"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
}

func TestTransferWithNewName(t *testing.T) {
	fast(t, 5*time.Second)
	s := newTransferServer(t, "org2/nr")
	s.after = 1
	out, err := s.run(t, "o/r", "transfer", "org2", "--name", "nr", "--confirm", "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if s.bodies[2] != `{"new_name":"nr","new_owner":"org2"}` {
		t.Errorf("body %q", s.bodies[2])
	}
	if !strings.Contains(out, "transferred: org2/nr") {
		t.Errorf("out: %s", out)
	}
}

func TestTransferTimesOut(t *testing.T) {
	fast(t, 30*time.Millisecond)
	s := newTransferServer(t, "org2/r")
	s.after = -1
	_, err := s.run(t, "o/r", "transfer", "org2", "--confirm", "o/r")
	if err == nil || !strings.Contains(err.Error(), "did not answer 200") || !strings.Contains(err.Error(), "accepted") {
		t.Fatalf("err = %v", err)
	}
	if s.posts() != 1 {
		t.Errorf("posts %d", s.posts())
	}
}

func TestTransferRefusesAnExistingTarget(t *testing.T) {
	s := newTransferServer(t, "org2/r")
	s.exists = true
	_, err := s.run(t, "o/r", "transfer", "org2", "--confirm", "o/r")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v", err)
	}
	if s.posts() != 0 {
		t.Error("sent the transfer anyway")
	}
}

func TestTransferDryRunSendsOnlyReads(t *testing.T) {
	s := newTransferServer(t, "org2/r")
	s.after = -1
	out, err := s.run(t, "o/r", "transfer", "org2", "--confirm", "o/r", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if s.posts() != 0 || !strings.Contains(out, "would transfer o/r to org2/r") {
		t.Errorf("reqs %v out %s", s.reqs, out)
	}
}

func TestTransferRefusals(t *testing.T) {
	s := newTransferServer(t, "org2/r")
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no confirm", []string{"o/r", "transfer", "org2"}, "needs --confirm o/r"},
		{"wrong confirm", []string{"o/r", "transfer", "org2", "--confirm", "o/x"}, "needs --confirm o/r"},
		{"no owner", []string{"o/r", "transfer", "--confirm", "o/r"}, "want one <new-owner>"},
		{"slash owner", []string{"o/r", "transfer", "a/b", "--confirm", "o/r"}, "want one <new-owner>"},
		{"bad name", []string{"o/r", "transfer", "org2", "--name", "a b", "--confirm", "o/r"}, "no slash, no spaces"},
		{"name on edit", []string{"o/r", "edit", "--name", "x", "--description", "d"}, "--name applies to fork and transfer only"},
		{"org on transfer", []string{"o/r", "transfer", "org2", "--org", "x", "--confirm", "o/r"}, "--org applies to fork only"},
		{"confirm on sync", []string{"o/r", "sync", "--confirm", "o/r"}, "rename and transfer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.run(t, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if len(s.reqs) != 0 {
		t.Errorf("a refusal reached the network: %v", s.reqs)
	}
}

func TestTransferRejectedByTheAPI(t *testing.T) {
	s := newTransferServer(t, "org2/r")
	s.after = -1
	s.transferCode = http.StatusUnprocessableEntity
	_, err := s.run(t, "o/r", "transfer", "org2", "--confirm", "o/r")
	if err == nil || !strings.Contains(err.Error(), "422") {
		t.Fatalf("err = %v", err)
	}
}
