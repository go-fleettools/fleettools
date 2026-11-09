package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func bigFile(t *testing.T, path string, n int, seed byte) {
	t.Helper()
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7) ^ seed
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture is go-quake1/engine#79 in small: main squash-merged and clean, a pull
// ref whose history added a 2 MiB binary and removed it, and a 2 MiB file a
// branch still holds (not residue: a clone carries it anyway).
func fixture(t *testing.T) (dir, residueID, keptID, addedBy string) {
	t.Helper()
	dir = t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bigFile(t, filepath.Join(dir, "kept.bin"), 2<<20, 1)
	gitIn(t, dir, "add", "README.md", "kept.bin")
	gitIn(t, dir, "commit", "-qm", "first")
	keptID = gitIn(t, dir, "rev-parse", "HEAD:kept.bin")

	gitIn(t, dir, "switch", "-q", "-c", "pr")
	bigFile(t, filepath.Join(dir, "quake-wasmbox"), 2<<20, 2)
	gitIn(t, dir, "add", "quake-wasmbox")
	gitIn(t, dir, "commit", "-qm", "E2 with the build output")
	addedBy = gitIn(t, dir, "rev-parse", "HEAD")
	residueID = gitIn(t, dir, "rev-parse", "HEAD:quake-wasmbox")
	gitIn(t, dir, "rm", "-q", "quake-wasmbox")
	gitIn(t, dir, "commit", "-qm", "remove it")
	head := gitIn(t, dir, "rev-parse", "HEAD")
	// What GitHub keeps after a squash merge and the branch's deletion.
	gitIn(t, dir, "update-ref", "refs/pull/79/head", head)
	gitIn(t, dir, "switch", "-q", "main")
	gitIn(t, dir, "branch", "-q", "-D", "pr")
	return dir, residueID, keptID, addedBy
}

func runCmd(args ...string) (string, string, int) {
	var out, errb bytes.Buffer
	code := cli.Drive(RootCmd(), args, &out, &errb)
	return out.String(), errb.String(), code
}

func TestScanFindsOnlyThePullOnlyBlob(t *testing.T) {
	dir, residueID, keptID, addedBy := fixture(t)
	rs, pulls, objects, err := scan(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if pulls != 1 || objects == 0 {
		t.Fatalf("pulls %d objects %d", pulls, objects)
	}
	if len(rs) != 1 || rs[0].ID != residueID || rs[0].Added != addedBy || rs[0].Size != 2<<20 ||
		len(rs[0].PRs) != 1 || rs[0].PRs[0] != 79 || rs[0].Paths[0] != "quake-wasmbox" {
		t.Fatalf("residue %+v (kept %s must not be there)", rs, keptID)
	}

	out, _, code := runCmd("scan", dir)
	if code != 1 || !strings.Contains(out, residueID) || !strings.Contains(out, "PR #79") || strings.Contains(out, keptID) {
		t.Fatalf("scan: exit %d\n%s", code, out)
	}
	// Above the blob's size: nothing to report, and that is an answer (0).
	out, _, code = runCmd("scan", "--min", "4MiB", dir)
	if code != 0 || !strings.Contains(out, "no blob") {
		t.Fatalf("scan --min 4MiB: exit %d\n%s", code, out)
	}
}

func TestScanWithoutPullRefsIsUnanswerable(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "first")
	_, errOut, code := runCmd("scan", dir)
	if code != 2 || !strings.Contains(errOut, "--mirror") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if _, _, code := runCmd("scan", "/nonexistent-pullresidue"); code != 2 {
		t.Fatalf("a missing directory: exit %d", code)
	}
	if _, _, code := runCmd("scan", "--min", "lots", dir); code != 2 {
		t.Fatalf("a bad --min: exit %d", code)
	}
	if _, _, code := runCmd(); code != 2 {
		t.Fatalf("no subcommand: exit %d", code)
	}
}

// server answers HEAD /repos/o/r/git/blobs/<id>: 200 for the ids in live, 404
// otherwise, and records what it was sent.
func server(t *testing.T, live map[string]bool) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Clone(r.Context()))
		id := r.URL.Path[strings.LastIndexByte(r.URL.Path, '/')+1:]
		switch {
		case id == strings.Repeat("e", 40):
			w.WriteHeader(http.StatusInternalServerError)
		case live[id]:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	return s, &seen
}

func useServer(t *testing.T, s *httptest.Server) {
	t.Helper()
	old := newProberFn
	t.Cleanup(func() { newProberFn = old })
	newProberFn = func(string) (prober, error) {
		return prober{base: s.URL, token: "tok-secret", client: s.Client()}, nil
	}
}

func TestLetterAsksOnlyForWhatIsServed(t *testing.T) {
	dir, residueID, _, addedBy := fixture(t)
	s, seen := server(t, map[string]bool{residueID: true})
	useServer(t, s)
	var opened string
	oldOpen := openURL
	t.Cleanup(func() { openURL = oldOpen })
	openURL = func(u string) error { opened = u; return nil }

	out, errOut, code := runCmd("letter", "--repo", "go-quake1/engine", "--open", dir)
	if code != 1 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"go-quake1/engine", "quake-wasmbox", "#79", addedBy,
		"GET /repos/go-quake1/engine/git/blobs/" + residueID, "2.0 MiB"} {
		if !strings.Contains(out, want) {
			t.Errorf("the letter lacks %q:\n%s", want, out)
		}
	}
	if opened != supportURL {
		t.Errorf("opened %q", opened)
	}
	r := (*seen)[0]
	if r.Method != http.MethodHead || r.Header.Get("Authorization") != "Bearer tok-secret" ||
		strings.Contains(r.URL.String(), "tok-secret") {
		t.Errorf("request %s %s, auth %q", r.Method, r.URL, r.Header.Get("Authorization"))
	}
	if strings.Contains(out+errOut, "tok-secret") {
		t.Error("the token was printed")
	}

	// Collected: nothing left to ask.
	s2, _ := server(t, nil)
	useServer(t, s2)
	out, _, code = runCmd("letter", "--repo", "go-quake1/engine", dir)
	if code != 0 || !strings.Contains(out, "nothing to ask") {
		t.Fatalf("after collection: exit %d\n%s", code, out)
	}
	if _, _, code := runCmd("letter", "--repo", "../etc", dir); code != 2 {
		t.Fatalf("a bad --repo: exit %d", code)
	}
}

func TestCheckSaysServedOrGone(t *testing.T) {
	live, gone, broken := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("e", 40)
	s, _ := server(t, map[string]bool{live: true})
	useServer(t, s)
	out, _, code := runCmd("check", "--repo", "o/r", live, gone)
	if code != 1 || !strings.Contains(out, live+"  served") || !strings.Contains(out, gone+"  gone") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if out, _, code := runCmd("check", "--repo", "o/r", gone); code != 0 || !strings.Contains(out, "gone") {
		t.Fatalf("all gone: exit %d\n%s", code, out)
	}
	if _, _, code := runCmd("check", "--repo", "o/r", broken); code != 2 {
		t.Fatalf("a 500: exit %d", code)
	}
	if _, _, code := runCmd("check", "--repo", "o/r", "not-a-sha"); code != 2 {
		t.Fatalf("a bad id: exit %d", code)
	}
	if _, _, code := runCmd("check", "--repo", "o/r"); code != 2 {
		t.Fatalf("no ids: exit %d", code)
	}
}

func TestReadTokenWantsAnOwnerOnlyFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tok")
	if err := os.WriteFile(p, []byte(" secret \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readToken(p); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("a 0644 token file: %v", err)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if tok, err := readToken(p); err != nil || tok != "secret" {
		t.Fatalf("%q, %v", tok, err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readToken(empty); err == nil {
		t.Fatal("an empty token file was accepted")
	}
	if _, err := readToken(dir); err == nil {
		t.Fatal("a directory was accepted")
	}
	// The command reports a bad token file as unanswerable, never as clean.
	old := newProberFn
	t.Cleanup(func() { newProberFn = old })
	newProberFn = func(string) (prober, error) { return prober{}, errors.New("token file: missing") }
	if _, _, code := runCmd("check", "--repo", "o/r", strings.Repeat("a", 40)); code != 2 {
		t.Fatalf("exit %d", code)
	}
}

func TestSmallHelpers(t *testing.T) {
	for in, want := range map[string]bool{"o/r": true, "go-quake1/engine": true, "o/r.go": true,
		"o": false, "/r": false, "o/": false, "../x": false, "o/..": false, "o/r/x": false, "o r/x": false} {
		if validRepo(in) != want {
			t.Errorf("validRepo(%q) = %v", in, !want)
		}
	}
	for in, want := range map[string]int64{"1234": 1234, "1KiB": 1024, "1MiB": 1 << 20, "2M": 2 << 20, "1G": 1 << 30} {
		if got, err := parseSize(in); err != nil || got != want {
			t.Errorf("parseSize(%q) = %d, %v", in, got, err)
		}
	}
	if _, err := parseSize("-1"); err == nil {
		t.Error("a negative size was accepted")
	}
	for in, want := range map[string]bool{"refs/pull/7/head": true, "refs/pull/7/merge": true,
		"refs/pull/x/head": false, "refs/pull/0/head": false, "refs/pull/7/other": false, "refs/heads/main": false} {
		if _, ok := pullNumber(in); ok != want {
			t.Errorf("pullNumber(%q) ok = %v", in, ok)
		}
	}
	if human(512) != "0.5 KiB" || short("") != "?" || short("abc") != "abc" || servedGone.String() != "gone" {
		t.Error("formatting")
	}
}
