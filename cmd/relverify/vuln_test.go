package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
)

// skipScan is for the tests of the other checks: they hold files to sums,
// attestations and build info, and must not reach any database.
var skipScan = vulnOptions{skip: true}

// testDB is the recorded-schema database under testdata/vulndb, as a file://
// URL. Its three advisories are synthetic (GO-2099-*: no real ID can collide)
// and written to the v1 layout of https://go.dev/security/vuln/database:
//
//	GO-2099-0001 encoding/json.Unmarshal, every version: PLANTED POSITIVE --
//	             every relverify test binary holds that symbol.
//	GO-2099-0002 encoding/json.NoSuchFunctionAnywhere: the module is affected,
//	             the symbol is absent -> module/package-level findings only.
//	GO-2099-0003 encoding/json.Unmarshal, fixed in 1.2.0: the symbol is
//	             present, the version is not affected.
//
// ⛔ WHY SYNTHETIC AND NOT A REAL ADVISORY. A real entry changes under the
// test: it is amended, its fixed version moves, it is withdrawn. And a real
// positive needs a binary built with a really vulnerable dependency or
// toolchain -- a download in every test run. The binary scanned here is the
// test binary itself, so the planted symbol is present by construction, and
// the two negatives prove the scan reads the symbol and the version range
// rather than reporting every advisory the database lists.
func testDB(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "vulndb"))
	if err != nil {
		t.Fatal(err)
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
}

// testBinary copies the running test binary -- a real Go binary, with a symbol
// table -- into dir under name, and returns its path and bytes.
func testBinary(t *testing.T, dir, name string) (string, []byte) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o755); err != nil {
		t.Fatal(err)
	}
	return p, b
}

var planted struct {
	mu    sync.Mutex
	built map[string][]byte // by -ldflags
}

// plantedSourceFiles is a module that calls json.Unmarshal: GO-2099-0001 is
// reachable in it, GO-2099-0002 (absent symbol) and GO-2099-0003 (fixed
// version) are not.
var plantedSourceFiles = map[string]string{
	"go.mod":  "module planted\n\ngo 1.22\n",
	"main.go": "package main\n\nimport (\n\t\"encoding/json\"\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc main() {\n\tvar v any\n\tfmt.Println(json.Unmarshal([]byte(os.Args[0]), &v))\n}\n",
}

func plantedSource(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	for n, body := range plantedSourceFiles {
		if err := os.WriteFile(filepath.Join(src, n), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

// strippedBinary is the planted program built the way this fleet's release
// workflows build: -trimpath -ldflags="-s -w" (authnd release.yml).
func strippedBinary(t *testing.T, dir, name string) (string, []byte) {
	return buildPlanted(t, dir, name, "-s -w")
}

func plantedBinary(t *testing.T, dir, name string) (string, []byte) {
	return buildPlanted(t, dir, name, "")
}

// plantedBinary builds, once, a small program that calls json.Unmarshal and
// writes it into dir under name.
//
// ⛔ NOT THE TEST BINARY. The first version scanned os.Executable(), and the
// planted negative GO-2099-0002 -- a symbol no program has -- was REPORTED.
// `go test` links its binaries without a symbol table, and for a binary with
// none govulncheck reports every vulnerable symbol of an affected module
// (vulncheck/binary.go: "The binary exe is stripped ... we report
// vulnerabilities at the go.mod-level precision"). A release build keeps its
// symbols, so this builds one the way a release does. Standard library only,
// GOTOOLCHAIN=local: no download.
func buildPlanted(t *testing.T, dir, name, ldflags string) (string, []byte) {
	t.Helper()
	planted.mu.Lock()
	data, ok := planted.built[ldflags]
	if !ok {
		src := plantedSource(t)
		out := filepath.Join(src, "planted")
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags="+ldflags, "-o", out, ".")
		cmd.Dir = src
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "CGO_ENABLED=0", "GOFLAGS=")
		if b, err := cmd.CombinedOutput(); err != nil {
			planted.mu.Unlock()
			t.Fatalf("go build: %v\n%s", err, b)
		}
		var err error
		if data, err = os.ReadFile(out); err != nil {
			planted.mu.Unlock()
			t.Fatal(err)
		}
		if planted.built == nil {
			planted.built = map[string][]byte{}
		}
		planted.built[ldflags] = data
	}
	planted.mu.Unlock()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return p, data
}

// The stripped case is detected through govulncheck's own extract mode, on a
// binary built the way releases are -- and the unstripped build is the
// control.
func TestExtractSeesWhetherABinaryHasSymbols(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries")
	}
	dir := t.TempDir()
	for name, want := range map[string]bool{"with": true, "stripped": false} {
		var p string
		if want {
			p, _ = plantedBinary(t, dir, name)
		} else {
			p, _ = strippedBinary(t, dir, name)
		}
		got, err := govulncheckExtract(context.Background(), p)
		if err != nil || got != want {
			t.Errorf("%s: has symbols = %v, %v; want %v", name, got, err, want)
		}
	}
}

// ⛔ A STRIPPED BINARY IS JUDGED BY ITS SOURCE. The planted program built with
// -s -w: govulncheck reports GO-2099-0002, a symbol no program has, as if
// present. Unreachable in source, it is reported as module-level only and
// does not fail; reachable, it fails; a source scan that cannot run is
// "could not be checked".
func TestAStrippedBinaryIsJudgedBySourceReachability(t *testing.T) {
	if testing.Short() {
		t.Skip("scans a binary")
	}
	fakeGH(t, func([]string) ([]byte, error) { return nil, nil })
	t.Cleanup(func() { scanSource = govulncheckSource })
	for name, c := range map[string]struct {
		reach    map[string]bool
		err      error
		fail     bool
		moduleOK int
	}{
		"nothing reachable":      {map[string]bool{}, nil, false, 2},
		"GO-2099-0002 reachable": {map[string]bool{"GO-2099-0002": true}, nil, true, 1},
		"source scan fails":      {nil, errors.New("go mod download: not found"), true, 0},
	} {
		t.Run(name, func(t *testing.T) {
			var asked []sourceTarget
			scanSource = func(_ context.Context, tg sourceTarget, _ string) (map[string]bool, error) {
				asked = append(asked, tg)
				return c.reach, c.err
			}
			dir := t.TempDir()
			_, b := strippedBinary(t, dir, "tool-linux-amd64")
			write(t, dir, "SHA256SUMS", sumOf(b)+"  tool-linux-amd64\n")
			var out bytes.Buffer
			files, err := checkFiles(&out, "o/r", "v1.2.3", commit, dir, vulnOptions{db: testDB(t), cache: &sourceCache{}})
			if c.err != nil {
				if !errors.Is(err, errUnverifiable) || !strings.Contains(err.Error(), "source-mode scan") {
					t.Fatalf("%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			f := files[0]
			if (len(f.vulns) > 0) != c.fail || len(f.moduleOnly) != c.moduleOK {
				t.Fatalf("vulns %v moduleOnly %v\n%s", f.vulns, f.moduleOnly, out.String())
			}
			if c.fail && !strings.Contains(out.String(), "REACHABLE in source at v1.2.3") {
				t.Errorf("%s", out.String())
			}
			if !strings.Contains(out.String(), "module-level only") {
				t.Errorf("the unreachable findings went silent:\n%s", out.String())
			}
			if len(asked) != 1 || asked[0].Module != "planted" || asked[0].Version != "v1.2.3" || asked[0].Package != "planted" ||
				asked[0].GOOS == "" || asked[0].GoVersion != runtime.Version() {
				t.Errorf("source scan asked for %+v", asked)
			}
		})
	}
}

// The real source-mode scan, offline, over the planted module and the
// planted database: reachability, not presence.
func TestTheSourceScanReportsWhatIsReachable(t *testing.T) {
	if testing.Short() {
		t.Skip("loads packages")
	}
	reach, err := scanSourceDir(context.Background(), plantedSource(t),
		sourceTarget{Package: "planted", GoVersion: runtime.Version()}, testDB(t))
	if err != nil {
		t.Fatal(err)
	}
	if !reach["GO-2099-0001"] || reach["GO-2099-0002"] || reach["GO-2099-0003"] || len(reach) != 1 {
		t.Fatalf("%v", reach)
	}
}

func TestTheScanFindsThePlantedSymbolAndOnlyIt(t *testing.T) {
	if testing.Short() {
		t.Skip("scans a binary")
	}
	path, _ := plantedBinary(t, t.TempDir(), "tool")
	vs, err := govulncheckBinary(context.Background(), path, testDB(t))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, v := range vs {
		ids = append(ids, v.ID)
	}
	if !slices.Equal(ids, []string{"GO-2099-0001"}) {
		t.Fatalf("found %v, want only GO-2099-0001 (0002: symbol absent, 0003: version fixed)", vs)
	}
	if !slices.Contains(vs[0].Symbols, "encoding/json.Unmarshal") {
		t.Errorf("symbols %v", vs[0].Symbols)
	}
}

// A found vulnerability fails the file; --allow-vuln accepts the one it names,
// reports it, and no other.
func TestAVulnerabilityFailsTheFileUnlessAllowed(t *testing.T) {
	if testing.Short() {
		t.Skip("scans a binary")
	}
	fakeGH(t, func([]string) ([]byte, error) { return nil, nil })
	for _, c := range []struct {
		name  string
		allow map[string]bool
		fail  bool
	}{
		{"nothing allowed", nil, true},
		{"another ID allowed", map[string]bool{"GO-2099-9999": true}, true},
		{"this ID allowed", map[string]bool{"GO-2099-0001": true}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			_, b := plantedBinary(t, dir, "tool-linux-amd64")
			write(t, dir, "SHA256SUMS", sumOf(b)+"  tool-linux-amd64\n")
			var out bytes.Buffer
			files, err := checkFiles(&out, "o/r", "v1.2.3", commit, dir, vulnOptions{db: testDB(t), allow: c.allow})
			if err != nil {
				t.Fatal(err)
			}
			f := files[0]
			if got := len(f.vulns) > 0; got != c.fail {
				t.Fatalf("vulns %v, want failing=%v\n%s", f.vulns, c.fail, out.String())
			}
			if c.fail && !strings.Contains(out.String(), "FAIL") || !strings.Contains(out.String(), "GO-2099-0001") {
				t.Errorf("the report does not name the vulnerability:\n%s", out.String())
			}
			if !c.fail && !strings.Contains(out.String(), "accepted by --allow-vuln") {
				t.Errorf("an allowed vulnerability went silent:\n%s", out.String())
			}
			if !strings.Contains(out.String(), runtime.Version()) {
				t.Errorf("the toolchain is not reported:\n%s", out.String())
			}
		})
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The database unreachable is "could not be checked", never "verified": both
// with nothing listening and with a server that answers HEAD and then fails
// the fetch.
func TestAnUnreachableDatabaseCannotVerify(t *testing.T) {
	if testing.Short() {
		t.Skip("scans a binary")
	}
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			return // 200: the client takes it for a v1 database
		}
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	fakeGH(t, func([]string) ([]byte, error) { return nil, nil })
	for name, db := range map[string]string{"fetch fails": failing.URL, "nothing listening": closedURL} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			_, b := testBinary(t, dir, "tool-linux-amd64")
			write(t, dir, "SHA256SUMS", sumOf(b)+"  tool-linux-amd64\n")
			_, err := checkFiles(&bytes.Buffer{}, "o/r", "v1.2.3", commit, dir, vulnOptions{db: db})
			if !errors.Is(err, errUnverifiable) || !strings.Contains(err.Error(), "could not be checked") {
				t.Fatalf("%v", err)
			}
		})
	}
}

// The whole command, through cobra: an unreachable database exits 2 and does
// not print "verified".
func TestTheCommandExitsTwoWhenTheDatabaseIsUnreachable(t *testing.T) {
	if testing.Short() {
		t.Skip("scans a binary")
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	db := srv.URL
	srv.Close()
	fakeRelease(t)
	var out, errb bytes.Buffer
	c := RootCmd()
	c.SetOut(&out)
	c.SetErr(&errb)
	code := execute(c, []string{"--norun", "--dir", filepath.Join(t.TempDir(), "rel"), "--vuln-db", db, "o/r", "v1.2.3"})
	if code != 2 || strings.Contains(out.String(), "verified") {
		t.Fatalf("exit %d\n%s\n%s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "could not be checked") {
		t.Errorf("stderr: %s", errb.String())
	}
}

// fakeRelease answers gh for a release of one file: the test binary.
func fakeRelease(t *testing.T) {
	t.Helper()
	src := t.TempDir()
	_, b := testBinary(t, src, "tool-linux-amd64")
	fakeGH(t, func(args []string) ([]byte, error) {
		switch {
		case args[0] == "api" && strings.Contains(args[1], "/git/ref/tags/"):
			return fmt.Appendf(nil, `{"object":{"type":"commit","sha":%q}}`, commit), nil
		case args[0] == "api" && strings.Contains(args[1], "/actions/runs"):
			return fmt.Appendf(nil, `{"workflow_runs":[{"name":"release","status":"completed","conclusion":"success","head_branch":"v1.2.3","head_sha":%q}]}`, commit), nil
		case args[0] == "release" && args[1] == "view":
			return []byte(`{"tagName":"v1.2.3"}`), nil
		case args[0] == "release" && args[1] == "download":
			d := args[len(args)-1]
			write(t, d, "SHA256SUMS", sumOf(b)+"  tool-linux-amd64\n")
			return nil, os.WriteFile(filepath.Join(d, "tool-linux-amd64"), b, 0o755)
		}
		return nil, nil
	})
}

// --no-vuln skips the scan and says so; it never reaches the database.
func TestNoVulnSkipsAndSaysSo(t *testing.T) {
	fakeRelease(t)
	scanned := false
	scanBinary = func(context.Context, string, string) ([]vuln, error) { scanned = true; return nil, nil }
	t.Cleanup(func() { scanBinary = govulncheckBinary })
	var out bytes.Buffer
	c := RootCmd()
	c.SetOut(&out)
	c.SetErr(&bytes.Buffer{})
	execute(c, []string{"--norun", "--no-vuln", "--dir", filepath.Join(t.TempDir(), "rel"), "o/r", "v1.2.3"})
	if scanned || !strings.Contains(out.String(), "NOT scanned (--no-vuln)") {
		t.Fatalf("scanned=%v\n%s", scanned, out.String())
	}
}

// The caveat of binary mode is printed, once.
func TestTheBinaryModeCaveatIsPrintedOnce(t *testing.T) {
	fakeRelease(t)
	scanBinary = func(context.Context, string, string) ([]vuln, error) { return nil, nil }
	t.Cleanup(func() { scanBinary = govulncheckBinary })
	var out bytes.Buffer
	c := RootCmd()
	c.SetOut(&out)
	c.SetErr(&bytes.Buffer{})
	execute(c, []string{"--norun", "--dir", filepath.Join(t.TempDir(), "rel"), "o/r", "v1.2.3"})
	if n := strings.Count(out.String(), "PRESENT in the binary"); n != 1 {
		t.Fatalf("caveat printed %d times\n%s", n, out.String())
	}
}

// A file that passes with an allowed vulnerability does not say "no known
// vulnerability": it names what was accepted, on its own line and in the
// verdict.
func TestAnAllowedVulnerabilityIsNeverSilent(t *testing.T) {
	var out bytes.Buffer
	report(&out, file{name: "t", listed: true, goBinary: true, scanned: true, toolchains: []string{"go1.27.3"},
		allowed: []string{"GO-2026-5932 (golang.org/x/crypto/openpgp/*)"}})
	s := out.String()
	if !strings.Contains(s, "allow t") || !strings.Contains(s, "GO-2026-5932") ||
		!strings.Contains(s, "but the 1 allowed") || strings.Contains(s, "no known vulnerability") {
		t.Fatalf("%s", s)
	}
}

// ⛔ SCAN FIRST, RUN LAST: a binary whose only fault is a vulnerability is not
// executed.
func TestAVulnerableHostBinaryIsNotRun(t *testing.T) {
	name := "tool-" + runtime.GOOS + "-" + runtime.GOARCH
	f := file{name: name, listed: true, goBinary: true, vulns: []string{"GO-2099-0001 (encoding/json.Unmarshal)"}}
	var out bytes.Buffer
	if runHostBinary(&out, t.TempDir(), "v1.2.3", []file{f}) || !strings.Contains(out.String(), "not run") {
		t.Fatalf("%s", out.String())
	}
	f.vulns, f.toolchain = nil, "built with go1.20, older than --min-go go1.27.3"
	out.Reset()
	if runHostBinary(&out, t.TempDir(), "v1.2.3", []file{f}) || !strings.Contains(out.String(), "not run") {
		t.Fatalf("an old toolchain was run: %s", out.String())
	}
}

// ⛔ UNREAD IS NOT CLEAN: a stream without the SBOM message never read the
// binary, whatever it says after.
func TestAStreamThatNeverReadTheBinaryIsAnError(t *testing.T) {
	cfg := `{"config":{"protocol_version":"v1.0.0","scan_mode":"binary"}}`
	sbom := `{"SBOM":{"go_version":"go1.27.2"}}`
	sym := `{"finding":{"osv":"GO-2026-6809","fixed_version":"v1.27.3","trace":[{"module":"stdlib","version":"v1.27.2","package":"crypto/x509","function":"Verify","receiver":"*Certificate"}]}}`
	mod := `{"finding":{"osv":"GO-2026-0001","trace":[{"module":"stdlib","version":"v1.27.2"}]}}`
	pkg := `{"finding":{"osv":"GO-2026-0002","trace":[{"module":"stdlib","version":"v1.27.2","package":"net/http"}]}}`

	if _, err := parseScan(strings.NewReader(cfg + sym)); err == nil {
		t.Error("a stream with no SBOM was believed")
	}
	if _, err := parseScan(strings.NewReader(cfg + sbom + "{not json")); err == nil {
		t.Error("a broken stream was believed")
	}
	vs, err := parseScan(strings.NewReader(cfg + sbom + mod + pkg + sym + sym))
	if err != nil {
		t.Fatal(err)
	}
	// Module- and package-level findings are not in the binary; the symbol one
	// is, once, with its receiver.
	if len(vs) != 1 || vs[0].ID != "GO-2026-6809" || !slices.Equal(vs[0].Symbols, []string{"crypto/x509.Certificate.Verify"}) || vs[0].Fixed != "v1.27.3" {
		t.Fatalf("%+v", vs)
	}
	// A whole-package advisory, as govulncheck v1.9.0 printed it for
	// go-authn/authnd v0.11.1: the wildcard split at its first dot.
	wild := `{"finding":{"osv":"GO-2026-5932","trace":[{"module":"golang.org/x/crypto","version":"v0.58.0","package":"golang.org/x/crypto/openpgp","function":"org/x/crypto/openpgp/*","receiver":"golang"}]}}`
	vs, err = parseScan(strings.NewReader(cfg + sbom + wild))
	if err != nil || len(vs) != 1 || !slices.Equal(vs[0].Symbols, []string{"golang.org/x/crypto/openpgp/*"}) {
		t.Fatalf("%+v %v", vs, err)
	}
}

// go1.27.10 is newer than go1.27.3: a string comparison says the opposite.
func TestToolchainVersionsCompareAsVersions(t *testing.T) {
	for _, c := range []struct {
		built, min string
		old        bool
	}{
		{"go1.27.3", "go1.27.3", false},
		{"go1.27.10", "go1.27.3", false}, // ⛔ lexically "go1.27.10" < "go1.27.3"
		{"go1.27.2", "go1.27.3", true},   // authnd v0.11.1, GO-2026-6809
		{"go1.26.9", "go1.27.3", true},
		{"go1.28.0", "go1.27.3", false},
		{"go1.28rc1", "go1.27.3", false},
		{"go1.27", "go1.27.3", true}, // the language version sorts before its releases
		{"go1.27.3 X:nodwarf5", "go1.27.3", false},
		{"devel go1.28-abcdef Sun Oct 25 2026", "go1.27.3", true}, // not a release: refused
		{"", "go1.27.3", true},
		{"go1.20", "", false}, // no --min-go: nothing is refused
	} {
		if got := olderThan(c.built, c.min) != ""; got != c.old {
			t.Errorf("olderThan(%q, %q) refused=%v, want %v", c.built, c.min, got, c.old)
		}
	}
	for in, want := range map[string]string{"1.27.3": "go1.27.3", "go1.27.3": "go1.27.3", " go1.28 ": "go1.28"} {
		if got, err := normGo(in); err != nil || got != want {
			t.Errorf("normGo(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "latest", "1.x", "v1.27.3"} {
		if _, err := normGo(bad); err == nil {
			t.Errorf("normGo(%q) accepted", bad)
		}
	}
}

// --min-go refuses a real binary whose toolchain is older, read from its own
// build information, and accepts it at its own version. The binary is the
// test binary: building with an older toolchain would download one in every
// run, and the comparison it would exercise is the table above.
func TestMinGoRefusesAnOlderToolchain(t *testing.T) {
	if testing.Short() {
		t.Skip("reads a binary")
	}
	fakeGH(t, func([]string) ([]byte, error) { return nil, nil })
	for min, refused := range map[string]bool{"go1.99.0": true, runtime.Version(): false} {
		dir := t.TempDir()
		_, b := testBinary(t, dir, "tool-linux-amd64")
		write(t, dir, "SHA256SUMS", sumOf(b)+"  tool-linux-amd64\n")
		var out bytes.Buffer
		files, err := checkFiles(&out, "o/r", "v1.2.3", commit, dir, vulnOptions{skip: true, minGo: min})
		if err != nil {
			t.Fatal(err)
		}
		if got := files[0].toolchain != ""; got != refused {
			t.Errorf("--min-go %s: refused=%v, want %v\n%s", min, got, refused, out.String())
		}
	}
}

// The v0.22.0 spellings keep working for one release, with a note; the
// new ones are untouched, and nothing after "--" is rewritten.
func TestTheSingleDashSpellingsStillWork(t *testing.T) {
	var warn bytes.Buffer
	got := legacyArgs([]string{"-wait", "40m", "-dir=/x", "-norun", "-n", "--min-go", "go1.27.3", "--", "-wait"}, &warn)
	want := []string{"--wait", "40m", "--dir=/x", "--norun", "-n", "--min-go", "go1.27.3", "--", "-wait"}
	if !slices.Equal(got, want) {
		t.Fatalf("%q", got)
	}
	if strings.Count(warn.String(), "deprecated") != 3 {
		t.Errorf("%s", warn.String())
	}
	c := RootCmd()
	c.SetArgs(legacyArgs([]string{"-wait", "40m", "-norun", "o/r", "v1"}, &bytes.Buffer{}))
	c.RunE = func(*cobra.Command, []string) error { return nil }
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	if w, _ := c.Flags().GetDuration("wait"); w.Minutes() != 40 {
		t.Errorf("wait %v", w)
	}
}

func TestUsageAndHelpExitCodes(t *testing.T) {
	for args, want := range map[string]int{
		"-h":                       0,
		"--help":                   0,
		"onlyone":                  2,
		"norepo v1":                2,
		"--allow-vuln CVE-1 o/r v": 2,
		"--min-go banana o/r v":    2,
	} {
		c := RootCmd()
		c.SetOut(&bytes.Buffer{})
		c.SetErr(&bytes.Buffer{})
		if got := execute(c, strings.Fields(args)); got != want {
			t.Errorf("relverify %s: exit %d, want %d", args, got, want)
		}
	}
}
