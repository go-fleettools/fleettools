package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

func TestHits(t *testing.T) {
	var tr tree
	for _, p := range []string{".file.go.atomicwrite-2262724758", "a/.x.go.atomicwrite-1", "a.go", "x.atomicwrite-1", ".x.atomicwrite-", ".x.atomicwrite-1.bak"} {
		tr.Tree = append(tr.Tree, struct {
			Path string `json:"path"`
		}{p})
	}
	got := hits(tr)
	if len(got) != 2 || got[0] != ".file.go.atomicwrite-2262724758" || got[1] != "a/.x.go.atomicwrite-1" {
		t.Errorf("got %v", got)
	}
}

// drive runs the command with the seams replaced and returns status and output.
func drive(t *testing.T, gh func(args ...string) ([]byte, error), wait func() error, args ...string) (int, string) {
	t.Helper()
	oldGH, oldWait := runGH, waitBudget
	runGH, waitBudget = gh, wait
	t.Cleanup(func() { runGH, waitBudget = oldGH, oldWait })
	var out, errb strings.Builder
	code := cli.Drive(RootCmd(), args, &out, &errb)
	return code, out.String() + errb.String()
}

const list = `[{"name":"old","pushedAt":"2026-01-01T00:00:00Z"},{"name":"dirty","pushedAt":"2026-11-20T00:00:00Z"},{"name":"clean","pushedAt":"2026-11-21T00:00:00Z"}]`

func fakeGH(trees map[string]string) func(args ...string) ([]byte, error) {
	return func(args ...string) ([]byte, error) {
		if args[0] == "repo" {
			return []byte(list), nil
		}
		for repo, body := range trees {
			if strings.Contains(args[1], repo+"/") {
				if body == "" {
					return nil, errors.New("HTTP 404")
				}
				return []byte(body), nil
			}
		}
		return nil, errors.New("unexpected " + args[1])
	}
}

func ok() error { return nil }

func TestFindsAHitAndSkipsOldRepositories(t *testing.T) {
	gh := fakeGH(map[string]string{
		"o/dirty": `{"tree":[{"path":"pkg/.a.go.atomicwrite-5"}]}`,
		"o/clean": `{"tree":[{"path":"a.go"}]}`,
	})
	code, out := drive(t, gh, ok, "--since", "2026-11-15", "o")
	if code != 1 || !strings.Contains(out, "HIT o/dirty pkg/.a.go.atomicwrite-5") || !strings.Contains(out, "read=2 hits=1 unread=0") || strings.Contains(out, "o/old") {
		t.Errorf("status %d:\n%s", code, out)
	}
}

func TestCleanExitsZero(t *testing.T) {
	gh := fakeGH(map[string]string{"o/dirty": `{"tree":[]}`, "o/clean": `{"tree":[]}`})
	if code, out := drive(t, gh, ok, "--since", "2026-11-15", "o"); code != 0 {
		t.Errorf("status %d:\n%s", code, out)
	}
}

func TestAnUnreadRepositoryIsNeverClean(t *testing.T) {
	cases := map[string]map[string]string{
		"failed call": {"o/dirty": "", "o/clean": `{"tree":[]}`},
		"not JSON":    {"o/dirty": "nope", "o/clean": `{"tree":[]}`},
		"truncated":   {"o/dirty": `{"truncated":true,"tree":[]}`, "o/clean": `{"tree":[]}`},
	}
	for name, trees := range cases {
		code, out := drive(t, fakeGH(trees), ok, "--since", "2026-11-15", "o")
		if code != 2 || !strings.Contains(out, "UNREAD o/dirty") || !strings.Contains(out, "unread=1") {
			t.Errorf("%s: status %d:\n%s", name, code, out)
		}
	}
	code, out := drive(t, fakeGH(map[string]string{"o/dirty": `{"tree":[]}`, "o/clean": `{"tree":[]}`}),
		func() error { return errors.New("timeout") }, "--since", "2026-11-15", "o")
	if code != 2 || !strings.Contains(out, "UNREAD o/dirty budget wait") {
		t.Errorf("budget wait: status %d:\n%s", code, out)
	}
}

func TestAnUnreadableOrganisationIsUnread(t *testing.T) {
	gh := func(args ...string) ([]byte, error) { return nil, errors.New("boom") }
	if code, out := drive(t, gh, ok, "--since", "2026-11-15", "o"); code != 2 || !strings.Contains(out, "UNREAD-ORG o") {
		t.Errorf("status %d:\n%s", code, out)
	}
	bad := func(args ...string) ([]byte, error) { return []byte("{"), nil }
	if code, _ := drive(t, bad, ok, "--since", "2026-11-15", "o"); code != 2 {
		t.Errorf("unparseable list: status %d", code)
	}
}

func TestRefusesAQuestionThatCannotBePut(t *testing.T) {
	for _, args := range [][]string{{"o"}, {"--since", "yesterday", "o"}, {"--since", "2026-11-15"}} {
		if code, out := drive(t, fakeGH(nil), ok, args...); code != 2 {
			t.Errorf("%v: status %d:\n%s", args, code, out)
		}
	}
}
