package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// recorder stubs ghJSON, answers the reads, and records every write so the
// sequence and the payloads can be asserted. The write path is the part that
// touches other people's repositories, so it is the part worth pinning.
type recorder struct {
	files map[string]string // path -> body, as the branch holds it
	calls []string
	puts  map[string]string // path -> decoded body written
}

func newRecorder(files map[string]string) *recorder {
	return &recorder{files: files, puts: map[string]string{}}
}

func (r *recorder) install(t *testing.T) func() {
	t.Helper()
	prev := ghJSON
	ghJSON = func(args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		r.calls = append(r.calls, joined)
		switch {
		case strings.HasSuffix(joined, "--jq .default_branch"):
			return []byte("main\n"), nil
		case strings.Contains(joined, "git/ref/heads/"):
			return []byte("basesha\n"), nil
		case strings.Contains(joined, "-X POST") && strings.Contains(joined, "git/refs"):
			return []byte("{}"), nil
		case strings.Contains(joined, "-X PUT") && strings.Contains(joined, "--input"):
			// Decode what gopin is actually sending.
			var path string
			for _, a := range args {
				if p, ok := strings.CutPrefix(a, "repos/o/r/contents/"); ok {
					path = p
				}
			}
			idx := -1
			for i, a := range args {
				if a == "--input" {
					idx = i + 1
				}
			}
			if idx < 0 || idx >= len(args) {
				return nil, fmt.Errorf("no --input")
			}
			raw, err := os.ReadFile(args[idx])
			if err != nil {
				return nil, err
			}
			var payload struct{ Content, Message, Branch, SHA string }
			if err := json.Unmarshal(raw, &payload); err != nil {
				return nil, err
			}
			dec, err := base64.StdEncoding.DecodeString(payload.Content)
			if err != nil {
				return nil, err
			}
			r.puts[path] = string(dec)
			if !strings.Contains(payload.Message, "Co-Authored-By: Claude Opus 5") {
				return nil, fmt.Errorf("commit message lacks the attribution line: %q", payload.Message)
			}
			r.files[path] = string(dec)
			return []byte("{}"), nil
		case strings.Contains(joined, "-X POST") && strings.Contains(joined, "/pulls"):
			return []byte("{}"), nil
		}
		for path, body := range r.files {
			if strings.Contains(joined, "contents/"+path+"?ref=") {
				return []byte(base64.StdEncoding.EncodeToString([]byte(body)) + "\n" + "filesha"), nil
			}
		}
		return nil, fmt.Errorf("404 Not Found: %s", joined)
	}
	return func() { ghJSON = prev }
}

func TestOpenWritesTheWorkflowAndTheGoDirective(t *testing.T) {
	rec := newRecorder(map[string]string{
		".github/workflows/ci.yml": "        with: { go-version: stable }\n          go-version: stable\n",
		"go.mod":                   "module x\n\ngo 1.26.4\n",
	})
	defer rec.install(t)()

	f := finding{Repo: "o/r", Files: []string{"ci.yml"}, Aliases: 2, GoMod: "1.26.4"}
	if err := open("o/r", f, "1.27.1"); err != nil {
		t.Fatalf("open: %v", err)
	}
	wf := rec.puts[".github/workflows/ci.yml"]
	if strings.Contains(wf, "stable") {
		t.Errorf("an alias survived:\n%s", wf)
	}
	if strings.Count(wf, "'1.27.1'") != 2 {
		t.Errorf("want both occurrences pinned:\n%s", wf)
	}
	if got := rec.puts["go.mod"]; !strings.Contains(got, "go 1.27.1") {
		t.Errorf("go.mod = %q", got)
	}
	// The pull request is the last call, so a failure half-way leaves no PR
	// claiming a change that was not fully written.
	if last := rec.calls[len(rec.calls)-1]; !strings.Contains(last, "/pulls") {
		t.Errorf("last call = %q; want the pull request", last)
	}
}

// A go.mod already at or beyond the target must not be touched. Rewriting it
// to the same value still produces a commit, and a commit that changes
// nothing is noise in 258 repositories.
func TestOpenLeavesAGoModThatIsAlreadyCurrent(t *testing.T) {
	rec := newRecorder(map[string]string{
		".github/workflows/ci.yml": "          go-version: stable\n",
		"go.mod":                   "module x\n\ngo 1.28.0\n",
	})
	defer rec.install(t)()
	if err := open("o/r", finding{Repo: "o/r", Files: []string{"ci.yml"}, Aliases: 1, GoMod: "1.28.0"}, "1.27.1"); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, wrote := rec.puts["go.mod"]; wrote {
		t.Error("go.mod was rewritten although it is ahead of the target")
	}
}

// An existing branch from an earlier run is not a failure: the tool is run
// again after a partial sweep, and refusing there would strand the repository.
func TestOpenToleratesABranchThatAlreadyExists(t *testing.T) {
	rec := newRecorder(map[string]string{".github/workflows/ci.yml": "          go-version: stable\n"})
	defer rec.install(t)()
	prev := ghJSON
	ghJSON = func(args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "git/refs") && strings.Contains(strings.Join(args, " "), "-X POST") {
			return nil, fmt.Errorf("HTTP 422: Reference already exists")
		}
		return prev(args...)
	}
	if err := open("o/r", finding{Repo: "o/r", Files: []string{"ci.yml"}, Aliases: 1}, "1.27.1"); err != nil {
		t.Fatalf("open: %v", err)
	}
	if !strings.Contains(rec.puts[".github/workflows/ci.yml"], "'1.27.1'") {
		t.Error("the file was not written after the branch already existed")
	}
}
