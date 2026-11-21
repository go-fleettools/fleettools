package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
	"golang.org/x/tools/go/packages"
)

// drive runs the command and returns stdout, stderr and the exit status.
func drive(t *testing.T, stdin string, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	c := RootCmd()
	c.SetIn(strings.NewReader(stdin))
	code := cli.Drive(c, args, &out, &errb)
	return out.String(), errb.String(), code
}

var findingRE = regexp.MustCompile(`(?m)^(\S+\.go):(\d+): (painter-primitive|layout-arithmetic|hit-test): `)

// lines maps file -> line -> detectors, from a report.
func lines(out string) map[string]map[int][]string {
	m := map[string]map[int][]string{}
	for _, x := range findingRE.FindAllStringSubmatch(out, -1) {
		n, _ := strconv.Atoi(x[2])
		if m[x[1]] == nil {
			m[x[1]] = map[int][]string{}
		}
		m[x[1]][n] = append(m[x[1]][n], x[3])
	}
	return m
}

func has(ds []string, d string) bool {
	for _, x := range ds {
		if x == d {
			return true
		}
	}
	return false
}

func fileLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(b), "\n")
}

// ⛔ A POSITIVE AND A NEGATIVE FOR EACH DETECTOR, line by line. pos.go marks each
// statement that must fire with `// WANT <detector>`: that detector must report
// on that line, and no other line of the file may report at all. Removing a
// detector makes its WANT line fail; widening one makes a NEG line fire.
func TestDetectorsOnFixtures(t *testing.T) {
	out, _, code := drive(t, "", "--repo", "testdata/app")
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	got := lines(out)
	want := map[int]string{}
	for i, ln := range fileLines(t, "testdata/app/pos/pos.go") {
		if _, d, ok := strings.Cut(ln, "// WANT "); ok && !strings.HasPrefix(ln, "//") {
			want[i+1] = strings.TrimSpace(d)
		}
	}
	if len(want) != 8 {
		t.Fatalf("fixture drifted: %d WANT markers, want 8", len(want))
	}
	// the report is ordered by file, then line
	type at struct {
		f string
		n int
	}
	var prev at
	for _, x := range findingRE.FindAllStringSubmatch(out, -1) {
		n, _ := strconv.Atoi(x[2])
		cur := at{x[1], n}
		if cur.f < prev.f || (cur.f == prev.f && cur.n < prev.n) {
			t.Errorf("report out of order: %v after %v", cur, prev)
		}
		prev = cur
	}
	pos := got["pos/pos.go"]
	for n, d := range want {
		if !has(pos[n], d) {
			t.Errorf("pos.go:%d: want a %s finding, got %v", n, d, pos[n])
		}
	}
	for n, ds := range pos {
		if _, ok := want[n]; !ok {
			t.Errorf("pos.go:%d: unexpected finding %v", n, ds)
		}
	}
	for f, m := range got {
		if f != "pos/pos.go" && !strings.HasPrefix(f, "agentscope/") && f != "shapes/pos.go" {
			t.Errorf("%s fired %v: the negatives and test files must stay silent", f, m)
		}
	}
	for _, d := range []string{detPainter, detArith, detHit} {
		if !strings.Contains(out, advice[d]) {
			t.Errorf("no advice printed for %s", d)
		}
	}
}

// ⛔ ONE FIXTURE PER SHAPE of fleettools#387, each with a twin in neg.go. Every
// `// WANT <detector>` line of shapes/pos.go must report that detector, no other
// line of the file may report, and neg.go stays silent (TestNegativesStaySilent).
// Removing a rule turns its WANT line red.
func TestShapeFixtures(t *testing.T) {
	out, _, code := drive(t, "", "--repo", "testdata/app")
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	want := map[int]string{}
	for i, ln := range fileLines(t, "testdata/app/shapes/pos.go") {
		if _, d, ok := strings.Cut(ln, "// WANT "); ok && !strings.HasPrefix(ln, "//") {
			want[i+1] = strings.TrimSpace(d)
		}
	}
	if len(want) != 32 {
		t.Fatalf("fixture drifted: %d WANT markers, want 32", len(want))
	}
	got := lines(out)["shapes/pos.go"]
	for n, d := range want {
		if !has(got[n], d) {
			t.Errorf("shapes/pos.go:%d: want a %s finding, got %v", n, d, got[n])
		}
		if len(got[n]) != 1 {
			t.Errorf("shapes/pos.go:%d: want exactly one finding, got %v (a site reported twice)", n, got[n])
		}
	}
	for n, ds := range got {
		if _, ok := want[n]; !ok {
			t.Errorf("shapes/pos.go:%d: unexpected finding %v", n, ds)
		}
	}
}

// The painter-backend exemption by module path, and the names of the primitives.
func TestBackendPathAndPrimitiveNames(t *testing.T) {
	for p, want := range map[string]bool{
		"github.com/go-widgets/gpupainter":     true,
		"github.com/go-widgets/softpainter":    true,
		"github.com/go-widgets/painter":        false,
		"github.com/go-widgets/toolkit":        false,
		"github.com/other/gpupainter":          false,
		"github.com/go-widgets/gpupainter/sub": false,
		"github.com/go-widgets/app":            false,
	} {
		if got := isBackendPath(p); got != want {
			t.Errorf("isBackendPath(%q) = %v, want %v", p, got, want)
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/go-widgets/gpupainter\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, why, err := moduleKind(dir); err != nil || !strings.Contains(why, "painter backend") {
		t.Errorf("moduleKind = %q, %v: want a painter backend exemption", why, err)
	}
	for _, n := range []string{"FillRect", "StrokeRect", "DrawImage", "BlitMask", "PutPixel", "Text", "ImageAt", "GlyphRun",
		"Line", "Lines", "LineTo", "Clear", "SetPixel", "Rect", "Rects", "Arc", "Circle", "Ellipse", "Polygon", "Polyline"} {
		if !primitiveName.MatchString(n) {
			t.Errorf("%s is a primitive and does not match", n)
		}
	}
	for _, n := range []string{"LineHeight", "Size", "Bounds", "Rectangle", "Linear", "Contains", "Width"} {
		if primitiveName.MatchString(n) {
			t.Errorf("%s matched as a primitive", n)
		}
	}
}

// Each negative on its own, so a mutation that widens a detector names the
// pattern: none of these may report.
func TestNegativesStaySilent(t *testing.T) {
	out, _, _ := drive(t, "", "--repo", "testdata/app")
	for f := range lines(out) {
		if strings.HasPrefix(f, "neg/") || f == "shapes/neg.go" {
			t.Errorf("negative fixture %s fired:\n%s", f, out)
		}
	}
}

// The agentscope v0.10.4 sites named in fleettools#384 are detected: each of the
// four functions holds at least one finding.
func TestAgentscopeSites(t *testing.T) {
	out, _, code := drive(t, "", "--repo", "testdata/app")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	got := lines(out)["agentscope/vsplit.go"]
	src := fileLines(t, "testdata/app/agentscope/vsplit.go")
	for _, site := range []string{"sessionsShare", "panelLayout", "updateDetailChars", "updateGuardChars"} {
		from, to := 0, 0
		for i, ln := range src {
			if strings.HasPrefix(ln, "func (s *Scene) "+site+"(") {
				from = i + 1
			}
			if from > 0 && to == 0 && ln == "}" {
				to = i + 1
			}
		}
		if from == 0 || to == 0 {
			t.Fatalf("site %s not found in the fixture", site)
		}
		n := 0
		for l := from; l <= to; l++ {
			n += len(got[l])
		}
		if n == 0 {
			t.Errorf("site %s (lines %d-%d): not detected", site, from, to)
		}
	}
	// the arithmetic on the application's own sizes alone is not a finding
	for l, ds := range got {
		if strings.Contains(src[l-1], "func ") {
			t.Errorf("finding on a declaration line %d: %v", l, ds)
		}
	}
}

func TestRepoKinds(t *testing.T) {
	for _, tc := range []struct {
		name, dir string
		code      int
		want      string
	}{
		{"toolkit itself is exempt", "testdata/toolkitself", 0, "exempt: the module is github.com/go-widgets/toolkit itself"},
		{"not a consumer", "testdata/plain", 0, "not a go-widgets consumer"},
		{"no go.mod is unread, not clean", "testdata/stubs", 2, "UNREAD: go.mod"},
		{"a painter backend is exempt", "testdata/backend", 0, "exempt: the module implements painter.Painter (example.com/backend.Soft)"},
		{"a build-tag-excluded file is unread, not clean", "testdata/tagged", 2, "b.go: excluded by build constraints, not read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _, code := drive(t, "", "--repo", tc.dir)
			if code != tc.code || !strings.Contains(out, tc.want) {
				t.Errorf("exit %d, out %q; want exit %d containing %q", code, out, tc.code, tc.want)
			}
		})
	}
}

func TestNothingToDo(t *testing.T) {
	if _, _, code := drive(t, ""); code != 2 {
		t.Errorf("no mode: exit %d, want 2", code)
	}
}

// ---- diff, waiver and hook, on a real throwaway repository ------------------

func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "core.hooksPath=/dev/null", "-c", "user.name=t", "-c", "user.email=t@t.invalid", "-c", "commit.gpgsign=false"}, args...)
	c := exec.Command("git", full...)
	c.Dir = dir
	c.Stdin = nil
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.Walk(from, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if fi.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// repo builds <tmp>/app (module, git) beside <tmp>/stubs, with the negatives
// committed as the remote's main (origin/main), and returns the app directory.
func repo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	copyTree(t, "testdata/stubs", filepath.Join(root, "stubs"))
	app := filepath.Join(root, "app")
	copyTree(t, "testdata/app", app)
	if err := os.RemoveAll(filepath.Join(app, "pos")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(app, "agentscope")); err != nil {
		t.Fatal(err)
	}
	sh(t, app, "init", "-q", "-b", "main")
	sh(t, app, "add", "-A")
	sh(t, app, "commit", "-q", "-m", "base")
	sh(t, app, "update-ref", "refs/remotes/origin/main", "HEAD")
	return app
}

func addPos(t *testing.T, app, msg string) {
	t.Helper()
	copyTree(t, "testdata/app/pos", filepath.Join(app, "pos"))
	sh(t, app, "add", "-A")
	sh(t, app, "commit", "-q", "-m", msg)
}

func TestDiffReportsOnlyAddedLines(t *testing.T) {
	app := repo(t)
	// a base that already holds the findings: they are the backlog, not the diff
	addPos(t, app, "legacy hand layout")
	sh(t, app, "update-ref", "refs/remotes/origin/main", "HEAD")
	base := sh(t, app, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(app, "neg", "more.go"), []byte("package neg\n\nfunc More(i int) int { return i + 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, app, "add", "-A")
	sh(t, app, "commit", "-q", "-m", "plain arithmetic")
	out, _, code := drive(t, "", "--diff", base, "--dir", app)
	if code != 0 || !strings.Contains(out, "clean") {
		t.Errorf("a diff adding no hand layout: exit %d, out %q", code, out)
	}
	if _, _, code := drive(t, "", "--repo", app); code != 1 {
		t.Errorf("the backlog audit must still see the legacy findings: exit %d", code)
	}
}

func TestDiffRefusesAdditionAndWaiverAllowsWithReason(t *testing.T) {
	app := repo(t)
	base := sh(t, app, "rev-parse", "HEAD")
	addPos(t, app, "paint by hand")
	out, _, code := drive(t, "", "--diff", base, "--dir", app)
	if code != 1 || !strings.Contains(out, "pos/pos.go:") || !strings.Contains(out, "handlayout-waiver") {
		t.Fatalf("exit %d, out:\n%s", code, out)
	}
	if got := lines(out); len(got["pos/pos.go"]) == 0 || len(got["neg/neg.go"]) != 0 {
		t.Errorf("findings in the wrong files: %v", got)
	}
	sh(t, app, "commit", "-q", "--allow-empty", "-m", "justify\n\nhandlayout-waiver: toolkit#999 lands next week; remove then")
	out, _, code = drive(t, "", "--diff", base, "--dir", app)
	if code != 0 || !strings.Contains(out, "WAIVED") || !strings.Contains(out, "toolkit#999 lands next week; remove then") {
		t.Errorf("waived: exit %d, out:\n%s", code, out)
	}
}

func TestWaiverNeedsAReason(t *testing.T) {
	app := repo(t)
	base := sh(t, app, "rev-parse", "HEAD")
	addPos(t, app, "paint\n\nhandlayout-waiver:   ")
	if _, _, code := drive(t, "", "--diff", base, "--dir", app); code != 1 {
		t.Errorf("an empty waiver reason must not waive: exit %d", code)
	}
}

func payloadFor(cmd, cwd string) string {
	b, _ := json.Marshal(map[string]any{"tool_name": "Bash", "cwd": cwd, "tool_input": map[string]string{"command": cmd}})
	return string(b)
}

func decisionOf(t *testing.T, out string) (string, string) {
	t.Helper()
	if strings.TrimSpace(out) == "" {
		return "", ""
	}
	var d struct {
		H struct {
			Decision string `json:"permissionDecision"`
			Reason   string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("hook output is not JSON: %q", out)
	}
	return d.H.Decision, d.H.Reason
}

func TestHook(t *testing.T) {
	app := repo(t)
	addPos(t, app, "paint by hand")
	waived := repo(t)
	addPos(t, waived, "paint by hand\n\nhandlayout-waiver: carried until toolkit#999")
	clean := repo(t)

	norepo := t.TempDir()
	plain := t.TempDir()
	copyTree(t, "testdata/plain", plain)
	sh(t, plain, "init", "-q", "-b", "main")
	sh(t, plain, "add", "-A")
	sh(t, plain, "commit", "-q", "-m", "x")

	noremote := t.TempDir()
	copyTree(t, "testdata/stubs", filepath.Join(noremote, "stubs"))
	copyTree(t, "testdata/app", filepath.Join(noremote, "app"))
	sh(t, filepath.Join(noremote, "app"), "init", "-q", "-b", "main")
	sh(t, filepath.Join(noremote, "app"), "add", "-A")
	sh(t, filepath.Join(noremote, "app"), "commit", "-q", "-m", "x")

	for _, tc := range []struct {
		name, stdin  string
		decision     string // "", allow, deny
		reasonHas    string
		stderrNotice bool
	}{
		{"gitpush adding hand layout is denied", payloadFor("gitpush origin feat/x", app), "deny", "refuses this push", false},
		{"git push is denied too", payloadFor("git push -u origin feat/x", app), "deny", "pos/pos.go", false},
		{"through cd and &&", payloadFor("cd "+app+" && </dev/null gitpush origin x", t.TempDir()), "deny", "layout-arithmetic", false},
		{"the refusal names the toolkit piece", payloadFor("gitpush origin x", app), "deny", "add the widget", false},
		{"a waiver allows and prints the reason", payloadFor("gitpush origin x", waived), "allow", "carried until toolkit#999", true},
		{"a clean range is silent", payloadFor("gitpush origin x", clean), "", "", false},
		{"not a push is silent", payloadFor("go test ./...", app), "", "", false},
		{"another tool is silent", `{"tool_name":"Edit","cwd":"` + app + `","tool_input":{}}`, "", "", false},
		{"not a repository: allow with a notice", payloadFor("gitpush origin x", norepo), "allow", "not counted as clean", true},
		{"not a consumer is silent", payloadFor("gitpush origin x", plain), "", "", false},
		{"no remote branch to measure from: allow, say so", payloadFor("gitpush origin x", filepath.Join(noremote, "app")), "allow", "not counted as clean", true},
		{"an undecodable payload allows with a notice", "{not json", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, code := drive(t, tc.stdin, "--hook")
			if code != 0 {
				t.Fatalf("--hook exits %d, want 0 always", code)
			}
			d, r := decisionOf(t, out)
			if d != tc.decision || !strings.Contains(r, tc.reasonHas) {
				t.Errorf("decision %q reason %q; want %q containing %q", d, r, tc.decision, tc.reasonHas)
			}
			if (errOut != "") != tc.stderrNotice {
				t.Errorf("stderr %q, want notice=%v", errOut, tc.stderrNotice)
			}
		})
	}
}

func TestPushDir(t *testing.T) {
	for _, tc := range []struct {
		cmd, dir string
		push     bool
	}{
		{"gitpush origin x", "/w", true},
		{"~/.local/bin/gitpush origin x", "/w", true},
		{"FOO=1 gitpush origin x", "/w", true},
		{"git push", "/w", true},
		{"git -C /r push origin x", "/r", true},
		{"cd /r && gitpush", "/r", true},
		{"cd sub; git push", "/w/sub", true},
		{"git status", "/w", false},
		{"echo gitpush", "/w", false},
		{"git log --oneline", "/w", false},
	} {
		d, ok := pushDir(tc.cmd, "/w")
		if ok != tc.push || (ok && d != tc.dir) {
			t.Errorf("%q: got (%q, %v), want (%q, %v)", tc.cmd, d, ok, tc.dir, tc.push)
		}
	}
}

func TestParseAdded(t *testing.T) {
	d := "diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -3,0 +4,2 @@\n+a\n+b\n@@ -9 +11 @@\n-c\n+d\n@@ -20,2 +22,0 @@\n-e\n-f\n--- a/gone.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-z\n"
	got := parseAdded(d)
	if len(got) != 1 || len(got["x.go"]) != 2 || got["x.go"][0] != (span{4, 5}) || got["x.go"][1] != (span{11, 11}) {
		t.Errorf("parseAdded = %v", got)
	}
}

// consumer writes a module that depends on the stubbed toolkit, with the given
// extra files and go.mod ("" for the usual one), beside a copy of the stubs.
func consumer(t *testing.T, gomod string, files map[string]string, withGit bool) string {
	t.Helper()
	root := t.TempDir()
	copyTree(t, "testdata/stubs", filepath.Join(root, "stubs"))
	app := filepath.Join(root, "app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if gomod == "" {
		b, err := os.ReadFile("testdata/app/go.mod")
		if err != nil {
			t.Fatal(err)
		}
		gomod = string(b)
	}
	files["go.mod"] = gomod
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(app, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if withGit {
		sh(t, app, "init", "-q", "-b", "main")
		sh(t, app, "add", "-A")
		sh(t, app, "commit", "-q", "-m", "x")
		sh(t, app, "update-ref", "refs/remotes/origin/main", "HEAD")
	}
	return app
}

// ⛔ WHAT IT CANNOT READ IS REPORTED, NEVER COUNTED AS CLEAN, and never takes the
// rest down with it.
func TestUnreadIsReportedNotClean(t *testing.T) {
	const tk = "package x\n\nimport \"github.com/go-widgets/toolkit\"\n\n"
	broken := consumer(t, "", map[string]string{"x.go": tk + "func F() int { return undefinedThing + toolkit.TextWidth(\"a\") }\n\nfunc G(w int) int { return w * toolkit.StatusbarH }\n"}, false)
	out, _, code := drive(t, "", "--repo", broken)
	if code != 1 || !strings.Contains(out, "UNREAD:") || !strings.Contains(out, "layout-arithmetic") {
		t.Errorf("a package that does not type-check: exit %d (want 1: the readable part still reports), out:\n%s", code, out)
	}
	bad := consumer(t, "this is not a go.mod\n", map[string]string{}, false)
	if out, _, code := drive(t, "", "--repo", bad); code != 2 || !strings.Contains(out, "UNREAD: go.mod") {
		t.Errorf("unparseable go.mod: exit %d, out %q", code, out)
	}
	// a non-consumer is never loaded, so what it holds cannot make it unread
	nc := consumer(t, "module example.com/nc\n\ngo 1.22\n", map[string]string{"x.go": "package x\n\nfunc F( {\n"}, false)
	if out, _, code := drive(t, "", "--repo", nc); code != 0 || !strings.Contains(out, "not a go-widgets consumer") {
		t.Errorf("non-consumer with broken Go: exit %d, out %q", code, out)
	}
	// no go command: the load reports it per package
	t.Setenv("PATH", t.TempDir())
	ok := consumer(t, "", map[string]string{"x.go": tk + "func F() {}\n"}, false)
	if out, _, code := drive(t, "", "--repo", ok); code != 2 || !strings.Contains(out, "UNREAD:") {
		t.Errorf("no go tool: exit %d, out %q", code, out)
	}
	// the driver itself failing
	old := loadPackages
	loadPackages = func(*packages.Config, ...string) ([]*packages.Package, error) { return nil, io.ErrUnexpectedEOF }
	t.Cleanup(func() { loadPackages = old })
	if out, _, code := drive(t, "", "--repo", ok); code != 2 || !strings.Contains(out, "UNREAD: go list: "+io.ErrUnexpectedEOF.Error()) || strings.Contains(out, "no packages") {
		t.Errorf("driver failure: exit %d, out %q", code, out)
	}
}

func TestDiffWithoutAReadableBase(t *testing.T) {
	app := repo(t)
	addPos(t, app, "paint")
	if _, errOut, code := drive(t, "", "--diff", "no-such-ref", "--dir", app); code != 2 || !strings.Contains(errOut, "no-such-ref") {
		t.Errorf("unknown base: exit %d, stderr %q", code, errOut)
	}
	// a repository that is not a consumer is not asked for its base at all
	plain := t.TempDir()
	copyTree(t, "testdata/plain", plain)
	if out, _, code := drive(t, "", "--diff", "no-such-ref", "--dir", plain); code != 0 || !strings.Contains(out, "not a go-widgets consumer") {
		t.Errorf("non-consumer: exit %d, out %q", code, out)
	}
	// a broken go.mod and nothing else is unread: exit 2, not 0
	bad := consumer(t, "this is not a go.mod\n", map[string]string{}, true)
	base := sh(t, bad, "rev-parse", "HEAD")
	if out, _, code := drive(t, "", "--diff", base, "--dir", bad); code != 2 || !strings.Contains(out, "UNREAD: go.mod") {
		t.Errorf("unreadable go.mod: exit %d, out %q", code, out)
	}
}

// failing makes the git subcommands named in fail run as an error.
func failing(t *testing.T, fail ...string) {
	t.Helper()
	old := git
	git = func(dir string, args ...string) (string, error) {
		for _, f := range fail {
			if len(args) > 0 && args[0] == f {
				return "", io.ErrUnexpectedEOF
			}
		}
		return old(dir, args...)
	}
	t.Cleanup(func() { git = old })
}

func TestGitFailuresAreErrorsNotVerdicts(t *testing.T) {
	for _, sub := range []string{"rev-parse", "diff", "log", "merge-base"} {
		t.Run(sub, func(t *testing.T) {
			app := repo(t)
			base := sh(t, app, "rev-parse", "HEAD")
			addPos(t, app, "paint")
			failing(t, sub)
			if sub != "merge-base" { // --diff does not ask for the merge base
				if _, _, code := drive(t, "", "--diff", base, "--dir", app); code != 2 {
					t.Errorf("--diff with git %s failing: exit %d, want 2", sub, code)
				}
			}
			d, r := decisionOf(t, hookOut(t, payloadFor("gitpush origin x", app)))
			if d != "allow" || !strings.Contains(r, "not counted as clean") {
				t.Errorf("hook with git %s failing: %q %q, want an allowance saying so", sub, d, r)
			}
		})
	}
}

func hookOut(t *testing.T, stdin string) string {
	t.Helper()
	out, _, _ := drive(t, stdin, "--hook")
	return out
}

func TestHookUnreadCases(t *testing.T) {
	for _, tc := range []struct{ name, cwd, want string }{
		{"not a repository", t.TempDir(), "could not find the repository"},
		{"no branch to measure from", func() string {
			a := consumer(t, "", map[string]string{"x.go": "package x\n"}, false)
			sh(t, a, "init", "-q", "-b", "main")
			sh(t, a, "add", "-A")
			sh(t, a, "commit", "-q", "-m", "x")
			return a
		}(), "origin/main"},
		{"unreadable go.mod", consumer(t, "this is not a go.mod\n", map[string]string{}, true), "could not read go.mod"},
		{"a package that does not type-check", consumer(t, "", map[string]string{"x.go": "package x\n\nvar v int = \"s\"\n"}, true), "could not read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, r := decisionOf(t, hookOut(t, payloadFor("gitpush origin x", tc.cwd)))
			if d != "allow" || !strings.Contains(r, tc.want) || !strings.Contains(r, "not counted as clean") {
				t.Errorf("%q %q; want an allowance naming %q", d, r, tc.want)
			}
		})
	}
}

func TestHookOtherToolWithAPushCommand(t *testing.T) {
	app := repo(t)
	addPos(t, app, "paint")
	b, _ := json.Marshal(map[string]any{"tool_name": "Edit", "cwd": app, "tool_input": map[string]string{"command": "gitpush origin x"}})
	if out := hookOut(t, string(b)); strings.TrimSpace(out) != "" {
		t.Errorf("only Bash is judged: %q", out)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestHookUnreadablePayload(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := hookMode(&out, &errOut, failingReader{}); code != 0 || out.Len() != 0 || !strings.Contains(errOut.String(), "could not read the hook payload") {
		t.Errorf("code %d out %q err %q", code, out.String(), errOut.String())
	}
}

func FuzzHook(f *testing.F) {
	f.Add(`{"tool_name":"Bash","cwd":"/nonexistent","tool_input":{"command":"gitpush"}}`)
	f.Add(`{"tool_name":"Bash"}`)
	f.Add(`[]`)
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		if code := hookMode(io.Discard, io.Discard, strings.NewReader(s)); code != 0 {
			t.Fatalf("exit %d", code)
		}
	})
}
