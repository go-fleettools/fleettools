package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

func tidyStub(t *testing.T) *[]string {
	t.Helper()
	var dirs []string
	prev := runner
	runner = func(_ context.Context, dir, _ string) (string, string, int, error) {
		dirs = append(dirs, dir)
		return "", "", 0, nil
	}
	t.Cleanup(func() { runner = prev })
	return &dirs
}

// main in hook mode answers on stdout and never reaches cobra's exit path.
func TestMainInHookModeAnswersOnStdout(t *testing.T) {
	oldArgs, oldStdin, oldStdout, oldStderr := os.Args, stdin, os.Stdout, os.Stderr
	d := t.TempDir()
	f, err := os.Create(filepath.Join(d, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	g, err := os.Create(filepath.Join(d, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Args, stdin, os.Stdout, os.Stderr = oldArgs, oldStdin, oldStdout, oldStderr
		f.Close()
		g.Close()
	})
	os.Args = []string{"modtidy", "--hook"}
	stdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`)
	os.Stdout, os.Stderr = f, g
	main()
	b, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "{}\n" {
		t.Errorf("hook answered %q, want {}", b)
	}
	// The staleness note is a network call, which a hook must not make: nothing
	// reaches stderr.
	if e, _ := os.ReadFile(g.Name()); len(e) != 0 {
		t.Errorf("hook mode wrote %q to stderr", e)
	}
}

// An unfinished retry loop is a hang, not a failure: the runner refuses to be
// asked a third time, so a loop that forgets it has retried goes red.
func TestTheRetryHappensOnceAndItsAnswerStands(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/x\n\ngo 1.99.9\n")
	write(t, filepath.Join(root, "a.go"), "package x\n")
	floor := "go.mod requires go >= 1.99.9 (running go 1.1; GOTOOLCHAIN=local)"
	var tcs []string
	prev := runner
	t.Cleanup(func() { runner = prev })
	runner = func(_ context.Context, _, tc string) (string, string, int, error) {
		tcs = append(tcs, tc)
		if len(tcs) > 2 {
			return "", "", 0, nil
		}
		return "", floor, 1, nil
	}
	code, out, _ := run(t, root)
	if code != 2 || !strings.Contains(out, "UNREAD  . (GOTOOLCHAIN=go1.99.9): "+floor) {
		t.Errorf("code %d out %q", code, out)
	}
	if !reflect.DeepEqual(tcs, []string{"local", "go1.99.9"}) {
		t.Errorf("toolchains tried %v", tcs)
	}
}

// Only the module's own sources count: a .go file under a skipped directory
// does not turn a Hugo-style module into one that `go mod tidy` may judge.
func TestGoFilesUnderSkippedDirectoriesDoNotCount(t *testing.T) {
	for _, d := range []string{"vendor", "testdata", ".hidden", "_under"} {
		root := t.TempDir()
		write(t, filepath.Join(root, "go.mod"), "module example.com/x\n\ngo 1.20\n")
		write(t, filepath.Join(root, d, "x.go"), "package x\n")
		if ok, err := hasGoSource(root); ok || err != nil {
			t.Errorf("%s: hasGoSource = %v, %v; want false", d, ok, err)
		}
	}
}

// main outside hook mode warns about staleness, runs the command and exits with
// its status: a directory with no go.mod is "could not be read", status 2.
func TestMainRunsTheCommandAndExitsWithItsStatus(t *testing.T) {
	oldArgs, oldExit := os.Args, cli.OsExit
	t.Cleanup(func() { os.Args, cli.OsExit = oldArgs, oldExit })
	exited := -1
	cli.OsExit = func(code int) { exited = code }
	os.Args = []string{"modtidy", t.TempDir()}
	main()
	if exited != 2 {
		t.Errorf("exit = %d, want 2", exited)
	}
}

// A hook failure of the command itself (a refused flag) is an allow, with the
// status named on stderr.
func TestDriveHookTurnsAFailureIntoAnAllow(t *testing.T) {
	var o, e bytes.Buffer
	driveHook([]string{"--hook", "--bogus"}, &o, &e)
	if o.String() != "{}\n" {
		t.Errorf("stdout = %q", o.String())
	}
	if !strings.Contains(e.String(), "modtidy: allowing: the command itself failed (status 2)") {
		t.Errorf("stderr = %q", e.String())
	}
}

func TestRootCmdHookFlagReadsThePayloadFromStdin(t *testing.T) {
	old := stdin
	t.Cleanup(func() { stdin = old })
	stdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`)
	code, out, _ := run(t, "--hook")
	if code != 0 || out != "{}\n" {
		t.Errorf("code %d out %q", code, out)
	}
}

func TestMoreThanOneDirectoryIsAUsageError(t *testing.T) {
	code, out, e := run(t, "a", "b")
	if code != 2 || !strings.Contains(out+e, "usage: modtidy [dir]") {
		t.Errorf("code %d out %q err %q", code, out, e)
	}
}

func TestAnUnwalkableRootIsUnanswerable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	noPanic(t, "run on a missing root", func() {
		code, out, e := run(t, missing)
		if code != 2 || !strings.Contains(out+e, "modtidy: cannot walk "+missing) {
			t.Errorf("code %d out %q err %q", code, out, e)
		}
	})
	noPanic(t, "findModules", func() {
		if _, err := findModules(missing); err == nil {
			t.Error("findModules of a missing root succeeded")
		}
	})
	noPanic(t, "hasGoSource", func() {
		if ok, err := hasGoSource(missing); ok || err == nil {
			t.Errorf("hasGoSource of a missing dir = %v, %v", ok, err)
		}
	})
}

// noPanic turns a crash into an assertion naming what crashed: the guards on
// these errors protect a dereference, so removing one would otherwise panic.
func noPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
		}
	}()
	f()
}

// report tolerates a nil context (a command run outside cobra).
func TestReportWithoutAContextStillRuns(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/x\n\ngo 1.20\n")
	var nilCtx context.Context
	var b bytes.Buffer
	if err := report(nilCtx, &b, root); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(b.String(), "skipped .: no Go source") || !strings.HasSuffix(b.String(), "1 module(s): 0 untidy, 0 UNREAD, 1 skipped\n") {
		t.Errorf("report = %q", b.String())
	}
}

// A walk that fails while looking for Go source is UNREAD, with the error.
func TestASourceWalkErrorIsUnread(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/x\n\ngo 1.20\n")
	prev := sourceOf
	t.Cleanup(func() { sourceOf = prev })
	sourceOf = func(string) (bool, error) { return false, errors.New("boom") }
	calls := stubRunner(t, func(_, _ string) (string, string, int, error) { return "", "", 0, nil })
	res, err := check(context.Background(), root)
	if err != nil || len(res) != 1 || res[0].state != unread || res[0].text != "boom" {
		t.Fatalf("res = %+v err %v", res, err)
	}
	if len(*calls) != 0 {
		t.Errorf("go ran %d time(s)", len(*calls))
	}
}

// A runner that cannot start go at all is UNREAD with its error, not tidy.
func TestARunnerErrorIsUnread(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/x\n\ngo 1.20\n")
	write(t, filepath.Join(root, "a.go"), "package x\n")
	stubRunner(t, func(_, _ string) (string, string, int, error) { return "", "", 0, errors.New("no go binary") })
	code, out, _ := run(t, root)
	if code != 2 || !strings.Contains(out, "UNREAD  .: no go binary") {
		t.Errorf("code %d out %q", code, out)
	}
}

// A floor error whose go.mod names no directive cannot be retried: UNREAD stays.
func TestAFloorWithNoDirectiveStaysUnread(t *testing.T) {
	floor := "go.mod requires go >= 1.99.9 (running go 1.1; GOTOOLCHAIN=local)"
	for name, gomod := range map[string]string{
		"no go line": "module example.com/x\n",
		"unparsable": "garbage\n",
	} {
		root := t.TempDir()
		write(t, filepath.Join(root, "go.mod"), gomod)
		write(t, filepath.Join(root, "a.go"), "package x\n")
		calls := stubRunner(t, func(_, _ string) (string, string, int, error) { return "", floor, 1, nil })
		var code int
		var out string
		noPanic(t, "directive of "+name, func() { code, out, _ = run(t, root) })
		if code != 2 || !strings.Contains(out, "UNREAD  .: "+floor+" (exit 1)") || strings.Contains(out, "GOTOOLCHAIN=go") {
			t.Errorf("%s: code %d out %q", name, code, out)
		}
		if !reflect.DeepEqual(*calls, []string{"local"}) {
			t.Errorf("%s: toolchains tried %v, want only local", name, *calls)
		}
	}
	noPanic(t, "directive of a directory with no go.mod", func() {
		if d := directive(t.TempDir()); d != "" {
			t.Errorf("directive of a directory with no go.mod = %q", d)
		}
	})
}

// A module whose go.mod cannot be read is not parsable, so it is never skipped.
func TestParsableGoModRefusesAMissingFile(t *testing.T) {
	if parsableGoMod(t.TempDir()) {
		t.Error("a directory with no go.mod is parsable")
	}
}

// config/_default is a Hugo configuration directory, and is named as such.
func TestHugoConfigDirectoryIsNamed(t *testing.T) {
	dir := t.TempDir()
	if got := hugoConfig(dir); got != "" {
		t.Errorf("empty dir: %q", got)
	}
	write(t, filepath.Join(dir, "config", "_default", "hugo.toml"), "title = \"x\"\n")
	if got := hugoConfig(dir); got != "config/_default" {
		t.Errorf("hugoConfig = %q", got)
	}
	if !strings.HasSuffix(noSourceReason(dir), " (Hugo module site: config/_default)") {
		t.Errorf("reason = %q", noSourceReason(dir))
	}
}

// ---- hook ----

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("disk on fire") }

func TestHookAllowsAndSaysWhenThePayloadCannotBeRead(t *testing.T) {
	var o, e bytes.Buffer
	runHook(failingReader{}, &o, &e)
	if o.String() != "{}\n" || e.String() != "modtidy: allowing: unreadable payload\n" {
		t.Errorf("out %q err %q", o.String(), e.String())
	}
}

// Without a cwd in the payload the hook judges the clone it runs in.
func TestHookWithoutACwdUsesTheWorkingDirectory(t *testing.T) {
	root := mkRepo(t, false)
	gitInit(t, root)
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(old) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	dirs := tidyStub(t)
	out, _ := hookRun(t, `{"tool_name":"Bash","tool_input":{"command":"gitpush"}}`)
	if out != "{}\n" {
		t.Errorf("out %q", out)
	}
	found := false
	for _, d := range *dirs {
		if r, _ := filepath.EvalSymlinks(d); r == want {
			found = true
		}
	}
	if !found {
		t.Errorf("the working directory's clone %s was not judged: %v", want, *dirs)
	}
}

// A clone that cannot be walked is allowed, and the reason names it.
func TestHookAllowsACloneItCannotWalk(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	root := mkRepo(t, false)
	gitInit(t, root)
	locked := filepath.Join(root, "locked")
	write(t, filepath.Join(locked, "x"), "x")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	out, e := hookRun(t, payloadFor(t, root, "gitpush"))
	top := toplevel(context.Background(), root)
	if out != "{}\n" || e != "modtidy: allowing: cannot walk "+top+"\n" {
		t.Errorf("out %q err %q", out, e)
	}
}

func TestFindPushesResolvesDirectories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dirOf := func(cmd string) []string {
		var ds []string
		for _, p := range findPushes(cmd, "/c") {
			ds = append(ds, p.dir)
		}
		return ds
	}
	cases := []struct {
		cmd  string
		want []string
	}{
		{"cd ~/x && gitpush", []string{filepath.Join(home, "x")}},
		{"cd ~ && gitpush", []string{home}},
		{"cd sub && gitpush", []string{"/c/sub"}},
		{"cd /abs && gitpush", []string{"/abs"}},
		{"cd && gitpush", []string{"/c"}},
		{"git -C ~/y push", []string{filepath.Join(home, "y")}},
		{"gitpush -C rel", []string{"/c/rel"}},
		{"gitpush -C", []string{"/c"}},
		{"command gitpush", []string{"/c"}},
		{"exec git push", []string{"/c"}},
		{"time env gitpush", []string{"/c"}},
		{"git --git-dir x --no-pager push", []string{"/c"}},
		{"git --no-pager status", nil},
		{"git -C", nil},
		{"A=1", nil},
		{"A=1\ngitpush", []string{"/c"}},
	}
	for _, c := range cases {
		if got := dirOf(c.cmd); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: dirs %v, want %v", c.cmd, got, c.want)
		}
	}
	// With no home directory a tilde stays literal, relative to the base.
	t.Setenv("HOME", "")
	if got := dirOf("cd ~/x && gitpush"); !reflect.DeepEqual(got, []string{"/c/~/x"}) {
		t.Errorf("no home: %v", got)
	}
}

func TestFindPushesCarriesTheWaiverOfItsOwnSegment(t *testing.T) {
	got := findPushes("MODTIDY_WAIVE=why gitpush; gitpush; OTHER=1 gitpush", "/c")
	var ws []string
	for _, p := range got {
		ws = append(ws, p.waiver)
	}
	if !reflect.DeepEqual(ws, []string{"why", "", ""}) {
		t.Errorf("waivers = %q", ws)
	}
}

func TestSegmentsQuotingAndHeredocs(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		{`echo a\;b`, "echo|a;b"},
		{"echo a\\\nb", "echo|ab"},
		{`echo "a\"b" "c\\d"`, `echo|a"b|c\d`},
		{`echo 'a\b'`, `echo|a\b`},
		{`echo "unclosed gitpush`, "echo|unclosed gitpush"},
		{"cat <<EOF\ngitpush\nEOF\nls", "cat;ls"},
		{"cat <<-EOF\ngitpush\n\tEOF\nls", "cat;ls"},
		{"cat << EOF\ngitpush\nEOF\nls", "cat;ls"},
		{"cat <<\"EOF\"\ngitpush\nEOF\nls", "cat;ls"},
		{"cat <<'E\\OF'\ngitpush\nEOF\nls", "cat;ls"},
		{"cat <<EOF; echo x\ngitpush\nEOF\nls", "cat;echo|x;ls"},
		{"cat <<EOF\ngitpush", "cat"},
		{"cat <<", "cat|<<"},
		{"echo a#b # c\nls", "echo|a#b;ls"},
	}
	for _, c := range cases {
		var parts []string
		for _, s := range segments(c.cmd) {
			parts = append(parts, strings.Join(s, "|"))
		}
		if got := strings.Join(parts, ";"); got != c.want {
			t.Errorf("%q: %q, want %q", c.cmd, got, c.want)
		}
	}
}
