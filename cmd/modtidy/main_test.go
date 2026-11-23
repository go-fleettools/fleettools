package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mkRepo builds a root module and a nested bench/ module. The nested module
// requires example.com/dep, resolved offline through a replace, and imports
// nothing from it: untidy until the require is dropped.
func mkRepo(t *testing.T, nestedUntidy bool) string {
	t.Helper()
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "")
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.20\n")
	write(t, filepath.Join(root, "a.go"), "package root\n")
	write(t, filepath.Join(root, "dep", "go.mod"), "module example.com/dep\n\ngo 1.20\n")
	write(t, filepath.Join(root, "dep", "d.go"), "package dep\n")
	nested := "module example.com/bench\n\ngo 1.20\n"
	if nestedUntidy {
		nested += "\nrequire example.com/dep v1.0.0\n\nreplace example.com/dep => ../dep\n"
	}
	write(t, filepath.Join(root, "bench", "go.mod"), nested)
	write(t, filepath.Join(root, "bench", "b.go"), "package bench\n")
	return root
}

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var o, e bytes.Buffer
	code := cli.Drive(RootCmd(), args, &o, &e)
	return code, o.String(), e.String()
}

func TestUntidyNestedModuleExits1(t *testing.T) {
	root := mkRepo(t, true)
	// The witness: the root alone is tidy, so only the nested module can fail.
	c := exec.Command("go", "mod", "tidy", "-diff")
	c.Dir = root
	c.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("root must be tidy on its own: %v\n%s", err, out)
	}
	code, out, _ := run(t, root)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "untidy  bench") || !strings.Contains(out, "tidy    .") {
		t.Fatalf("report does not name bench as untidy and root as tidy:\n%s", out)
	}
	if !strings.Contains(out, "-require example.com/dep") && !strings.Contains(out, "example.com/dep") {
		t.Fatalf("no diff shown:\n%s", out)
	}
}

func TestTidiedExits0(t *testing.T) {
	root := mkRepo(t, false)
	code, out, _ := run(t, root)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
}

func TestBrokenModuleExits2(t *testing.T) {
	root := mkRepo(t, false)
	write(t, filepath.Join(root, "bench", "go.mod"), "this is not a go.mod\n")
	code, out, _ := run(t, root)
	if code != 2 || !strings.Contains(out, "UNREAD  bench") {
		t.Fatalf("exit = %d, want 2 with UNREAD bench\n%s", code, out)
	}
}

func TestUnreadOutranksUntidy(t *testing.T) {
	root := mkRepo(t, true)
	write(t, filepath.Join(root, "broken", "go.mod"), "garbage\n")
	if code, out, _ := run(t, root); code != 2 {
		t.Fatalf("exit = %d, want 2\n%s", code, out)
	}
}

func TestNoModuleIsNotClean(t *testing.T) {
	if code, _, _ := run(t, t.TempDir()); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestSkippedDirectories(t *testing.T) {
	root := mkRepo(t, false)
	for _, d := range []string{"vendor/x", "testdata/y", ".hidden", "_under"} {
		write(t, filepath.Join(root, d, "go.mod"), "garbage\n")
	}
	if code, out, _ := run(t, root); code != 0 {
		t.Fatalf("skipped dirs were read: exit %d\n%s", code, out)
	}
}

// TestDiscoveryFindsNestedModules is the one a mutation of findModules breaks
// (stop at the first go.mod, or skip every subdirectory): it asserts the exact
// set, not a count.
func TestDiscoveryFindsNestedModules(t *testing.T) {
	root := mkRepo(t, false)
	write(t, filepath.Join(root, "bench", "deep", "go.mod"), "module example.com/deep\n\ngo 1.20\n")
	got, err := findModules(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		root,
		filepath.Join(root, "bench"),
		filepath.Join(root, "bench", "deep"),
		filepath.Join(root, "dep"),
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("modules = %v, want %v", got, want)
	}
}

// ---- hook ----

func hookRun(t *testing.T, in string, env ...string) (string, string) {
	t.Helper()
	for i := 0; i+1 < len(env); i += 2 {
		t.Setenv(env[i], env[i+1])
	}
	var o, e bytes.Buffer
	runHook(strings.NewReader(in), &o, &e)
	return o.String(), e.String()
}

func payloadFor(t *testing.T, cwd, command string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"tool_name": "Bash", "cwd": cwd,
		"tool_input": map[string]string{"command": command},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func gitInit(t *testing.T, root string) {
	t.Helper()
	c := exec.Command("git", "init", "-q", root)
	c.Env = append(os.Environ(), "GITSAFE_ALLOW_VOLATILE=1")
	if out, err := c.CombinedOutput(); err != nil {
		t.Skipf("git init unavailable: %v %s", err, out)
	}
}

func TestHookDeniesGitpushInUntidyClone(t *testing.T) {
	root := mkRepo(t, true)
	gitInit(t, root)
	for _, cmd := range []string{
		"gitpush origin main",
		"~/.local/bin/gitpush -u origin x",
		"git push origin main",
		"git -c a=b push",
		"cd /somewhere && gitpush",
		"echo hi\ngitpush",
	} {
		out, _ := hookRun(t, payloadFor(t, root, strings.Replace(cmd, "/somewhere", root, 1)))
		if !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "bench") {
			t.Errorf("%q: want deny naming bench, got %s", cmd, out)
		}
		if !strings.Contains(out, "go mod tidy") {
			t.Errorf("%q: the fix is not named: %s", cmd, out)
		}
	}
}

func TestHookDashCSelectsTheClone(t *testing.T) {
	root := mkRepo(t, true)
	gitInit(t, root)
	other := t.TempDir()
	out, _ := hookRun(t, payloadFor(t, other, "git -C "+root+" push"))
	if !strings.Contains(out, "deny") {
		t.Fatalf("-C ignored: %s", out)
	}
}

func TestHookAllowsProseAndOtherCommands(t *testing.T) {
	root := mkRepo(t, true)
	gitInit(t, root)
	for _, cmd := range []string{
		`echo "run gitpush now"`,
		`git commit -m "fix; gitpush later"`,
		"cat <<'EOF'\ngitpush\nEOF",
		"# gitpush\nls",
		"git status",
		"grep gitpush README.md",
		"ls gitpush",
	} {
		out, _ := hookRun(t, payloadFor(t, root, cmd))
		if strings.TrimSpace(out) != "{}" {
			t.Errorf("%q: want allow, got %s", cmd, out)
		}
	}
}

func TestHookAllowsTidyClone(t *testing.T) {
	root := mkRepo(t, false)
	gitInit(t, root)
	if out, _ := hookRun(t, payloadFor(t, root, "gitpush")); strings.TrimSpace(out) != "{}" {
		t.Fatalf("tidy clone refused: %s", out)
	}
}

func TestHookFailsOpen(t *testing.T) {
	for _, in := range []string{"", "garbage", "{", `{"tool_name":"Bash"}`, `{"tool_name":"Bash","tool_input":{"command":7}}`, `[1,2]`} {
		if out, _ := hookRun(t, in); strings.TrimSpace(out) != "{}" {
			t.Errorf("%q: want {}, got %s", in, out)
		}
	}
	// A clone that cannot be read (broken module) does not block.
	root := mkRepo(t, true)
	gitInit(t, root)
	write(t, filepath.Join(root, "bench", "go.mod"), "garbage\n")
	if out, e := hookRun(t, payloadFor(t, root, "gitpush")); strings.TrimSpace(out) != "{}" || !strings.Contains(e, "UNREAD") {
		t.Fatalf("unread module must allow and say so: %s / %s", out, e)
	}
	// Not a repository: nothing to judge.
	if out, _ := hookRun(t, payloadFor(t, t.TempDir(), "gitpush")); strings.TrimSpace(out) != "{}" {
		t.Fatalf("non-repo: %s", out)
	}
}

func TestHookWaiver(t *testing.T) {
	root := mkRepo(t, true)
	gitInit(t, root)
	out, e := hookRun(t, payloadFor(t, root, `MODTIDY_WAIVE="bench is generated" gitpush`))
	if !strings.Contains(out, `"permissionDecision":"allow"`) || !strings.Contains(out, "bench is generated") || !strings.Contains(e, "bench is generated") {
		t.Fatalf("waiver not honoured/printed: %s / %s", out, e)
	}
	out, _ = hookRun(t, payloadFor(t, root, "gitpush"), "MODTIDY_WAIVE", "from env")
	if !strings.Contains(out, "allow") || !strings.Contains(out, "from env") {
		t.Fatalf("env waiver: %s", out)
	}
	// An empty reason is not a waiver.
	out, _ = hookRun(t, payloadFor(t, root, `MODTIDY_WAIVE= gitpush`), "MODTIDY_WAIVE", "")
	if !strings.Contains(out, "deny") {
		t.Fatalf("empty waiver allowed: %s", out)
	}
}

func TestHookModeFromRawArgv(t *testing.T) {
	for _, a := range []string{"--hook", "--hook=1", "--hook=yes", "--hook=TRUE"} {
		if !hookMode([]string{a}) {
			t.Errorf("%s not hook mode", a)
		}
	}
	for _, a := range []string{"--hook=false", "--hook=0", "dir", "--other"} {
		if hookMode([]string{a}) {
			t.Errorf("%s is hook mode", a)
		}
	}
	// A malformed flag under --hook still exits 0 with an allow.
	var o, e bytes.Buffer
	driveHook([]string{"--hook", "--bogus"}, &o, &e)
	if strings.TrimSpace(o.String()) != "{}" {
		t.Fatalf("bad flag under --hook: %q", o.String())
	}
}

// `<<<` is a here-string, not a heredoc: it has no body, so the next line is a
// command. Reading its last two `<` as a heredoc start swallowed that line and
// hid the push behind it.
func TestAHereStringIsNotAHeredoc(t *testing.T) {
	var parts []string
	for _, s := range segments("cat <<< x\ngitpush\n") {
		parts = append(parts, strings.Join(s, "|"))
	}
	if got, want := strings.Join(parts, ";"), "cat|<<<|x;gitpush"; got != want {
		t.Fatalf("segments = %q, want %q", got, want)
	}
	root := mkRepo(t, true)
	gitInit(t, root)
	out, _ := hookRun(t, payloadFor(t, root, "cat <<< x\ngitpush"))
	if !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Errorf("a push after a here-string was not seen: %s", out)
	}
}

func TestSegments(t *testing.T) {
	got := segments(`A=1 gitpush "a b" && echo 'x;y' | cat; (ls)`)
	var parts []string
	for _, s := range got {
		parts = append(parts, strings.Join(s, "|"))
	}
	want := "A=1|gitpush|a b;echo|x;y;cat;ls"
	if strings.Join(parts, ";") != want {
		t.Fatalf("segments = %q, want %q", strings.Join(parts, ";"), want)
	}
}
