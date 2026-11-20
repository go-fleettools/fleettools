package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const goodSpec = `environment: local
exec: [tart, exec, vm, sudo]
ca_cert: /root.crt
pages:
  - {url: https://login.test/, expect: 200}
  - {url: https://login.test/nope, expect: 404}
turn:
  turnd: /usr/local/bin/turnd
  turnprobe: /usr/local/bin/turnprobe
  secret_file: /etc/turnd/turn.secret
  realm: turn.test
  servers: ["turn:turn.test:3478?transport=udp", "turns:turn.test:443?transport=tcp"]
commands:
  - {name: bridge check, run: [authn-bridge, check]}
`

// target is a fake machine: it answers by the program's name.
type target struct {
	calls    [][]string
	statuses map[string]string // URL -> http code
	probeOut string
	probeRC  int
	mintRC   int
	startErr error
}

func (t *target) runner(prefix []string, quoteRemote bool) runner {
	return func(argv []string) (string, string, int, error) {
		t.calls = append(t.calls, argv)
		if t.startErr != nil {
			return "", "", -1, t.startErr
		}
		switch {
		case argv[0] == "curl":
			return t.statuses[argv[len(argv)-1]], "", 0, nil
		case len(argv) > 1 && argv[1] == "mint-credentials":
			if t.mintRC != 0 {
				return "", "boom", t.mintRC, nil
			}
			return "1795172065:ab.envcheck\n", "", 0, nil
		case strings.HasSuffix(argv[0], "turnprobe"):
			return t.probeOut, "", t.probeRC, nil
		}
		return "ok\n", "", 0, nil
	}
}

func okTarget() *target {
	return &target{
		statuses: map[string]string{"https://login.test/": "200", "https://login.test/nope": "404"},
		probeOut: "PASS turn:turn.test:3478?transport=udp mapped=1.2.3.4:5\nPASS turns:turn.test:443?transport=tcp mapped=1.2.3.4:6\n",
	}
}

func drive(t *testing.T, tg *target, specText string, extra ...string) (code int, stdout, stderr, report string) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "checks.yml")
	if err := os.WriteFile(file, []byte(specText), 0o644); err != nil {
		t.Fatal(err)
	}
	oldR, oldN, oldH := newRunner, now, randHex
	newRunner = tg.runner
	now = func() time.Time { return time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC) }
	randHex = func() string { return "0011223344556677" }
	t.Cleanup(func() { newRunner, now, randHex = oldR, oldN, oldH })
	var o, e bytes.Buffer
	code = run(append(append([]string{}, extra...), file), &o, &e)
	rep, _ := os.ReadFile(filepath.Join(dir, "VALIDATION.md"))
	return code, o.String(), e.String(), string(rep)
}

func TestAllPassWritesReportAndKeepsCredentialsOut(t *testing.T) {
	tg := okTarget()
	code, out, errOut, rep := drive(t, tg, goodSpec)
	if code != 0 {
		t.Fatalf("code %d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{"all 4 checks passed", "PASS GET https://login.test/nope expects 404"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	for _, want := range []string{"# Validation of `local`", "2026-11-20 12:00 UTC", "tart exec vm sudo", "all 4 checks passed", "mapped=1.2.3.4:6"} {
		if !strings.Contains(rep, want) {
			t.Errorf("report lacks %q:\n%s", want, rep)
		}
	}
	for _, leak := range []string{"ab.envcheck", "envcheck-0011223344556677.pw"} {
		if strings.Contains(rep, leak) || strings.Contains(out, leak) {
			t.Errorf("%q leaked into the output or the report", leak)
		}
	}
	// The password file is removed at the end, by the same name it was minted under.
	var removed bool
	for _, c := range tg.calls {
		if c[0] == "rm" && c[2] == "/run/envcheck-0011223344556677.pw" {
			removed = true
		}
	}
	if !removed {
		t.Errorf("the password file was never removed: %v", tg.calls)
	}
}

func TestWrongStatusFailsWithExit1(t *testing.T) {
	tg := okTarget()
	tg.statuses["https://login.test/nope"] = "200"
	code, out, _, rep := drive(t, tg, goodSpec)
	if code != 1 {
		t.Fatalf("code %d, want 1\n%s", code, out)
	}
	if !strings.Contains(rep, "FAIL GET https://login.test/nope") || !strings.Contains(rep, "got 200, expected 404") {
		t.Errorf("report does not say why:\n%s", rep)
	}
}

func TestProbeExitZeroWithAMissingLineIsNotAPass(t *testing.T) {
	tg := okTarget()
	tg.probeOut = "PASS turn:turn.test:3478?transport=udp mapped=1.2.3.4:5\n"
	code, out, _, _ := drive(t, tg, goodSpec)
	if code != 1 || !strings.Contains(out, "1 PASS and 0 FAIL lines for 2 servers") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestFailedProbeStillRemovesThePasswordFile(t *testing.T) {
	tg := okTarget()
	tg.probeOut, tg.probeRC = "FAIL turn:x\n", 1
	code, _, _, _ := drive(t, tg, goodSpec)
	if code != 1 {
		t.Fatalf("code %d", code)
	}
	if c := tg.calls[len(tg.calls)-2]; c[0] != "rm" {
		t.Errorf("rm did not follow the probe: %v", tg.calls)
	}
}

func TestMintFailureIsAFailureNotACrash(t *testing.T) {
	tg := okTarget()
	tg.mintRC = 1
	code, out, _, rep := drive(t, tg, goodSpec)
	if code != 1 || !strings.Contains(out, "mint-credentials exited 1") || !strings.Contains(rep, "boom") {
		t.Fatalf("code %d\n%s\n%s", code, out, rep)
	}
}

func TestProtectedEnvironmentNeedsConfirmAndRunsNothingWithout(t *testing.T) {
	spec := strings.Replace(goodSpec, "environment: local", "environment: production\nprotected: true", 1)
	tg := okTarget()
	code, _, errOut, _ := drive(t, tg, spec)
	if code != 2 || !strings.Contains(errOut, "--confirm production") {
		t.Fatalf("code %d: %s", code, errOut)
	}
	if len(tg.calls) != 0 {
		t.Fatalf("a protected environment was touched without confirmation: %v", tg.calls)
	}
	if code, _, _, _ = drive(t, okTarget(), spec, "--confirm", "staging"); code != 2 {
		t.Errorf("--confirm with the wrong name gave %d", code)
	}
	if code, _, _, _ = drive(t, okTarget(), spec, "--confirm", "production"); code != 0 {
		t.Errorf("--confirm production gave %d", code)
	}
}

func TestACommandThatCannotStartIsExit2(t *testing.T) {
	tg := okTarget()
	tg.startErr = errors.New("no such program")
	code, _, errOut, _ := drive(t, tg, goodSpec)
	if code != 2 || !strings.Contains(errOut, "no such program") {
		t.Fatalf("code %d: %s", code, errOut)
	}
}

func TestSpecErrorsAreExit2(t *testing.T) {
	for name, text := range map[string]string{
		"unknown key": "environment: x\npages: [{url: https://a/, expct: 200}]\n",
		"no checks":   "environment: x\n",
		"no env":      "pages: [{url: https://a/, expect: 200}]\n",
		"bad status":  "environment: x\npages: [{url: https://a/, expect: 7}]\n",
		"turn gaps":   "environment: x\nturn: {turnd: /t}\n",
		"bad ttl":     "environment: x\nturn: {turnd: a, turnprobe: b, secret_file: c, servers: [d], ttl: soon}\n",
	} {
		tg := okTarget()
		if code, _, errOut, _ := drive(t, tg, text); code != 2 || errOut == "" || len(tg.calls) != 0 {
			t.Errorf("%s: code %d, stderr %q, calls %v", name, code, errOut, tg.calls)
		}
	}
}

func TestCommandExpectedExit(t *testing.T) {
	spec := "environment: x\ncommands:\n  - {name: refuses, run: [false], expect_exit: 0}\n"
	tg := okTarget()
	tg.startErr = nil
	code, out, _, _ := drive(t, tg, spec)
	if code != 0 { // the fake returns 0 for everything else
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestMissingFileIsExit2(t *testing.T) {
	var o, e bytes.Buffer
	if code := run([]string{filepath.Join(t.TempDir(), "absent.yml")}, &o, &e); code != 2 {
		t.Fatalf("code %d", code)
	}
}

func TestDisplayRedactsCredentialFlags(t *testing.T) {
	got := display([]string{"turnprobe", "--username", "u:1", "--password-file", "/run/x.pw", "--server", "turn:a"})
	if got != "turnprobe --username <redacted> --password-file <redacted> --server turn:a" {
		t.Errorf("got %q", got)
	}
}

func TestQuoteSurvivesAShell(t *testing.T) {
	got := quote([]string{"turn:a?transport=udp", "it's", "a b"})
	want := `'turn:a?transport=udp' 'it'\''s' 'a b'`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestExecRunnerReportsExitAndStartFailure(t *testing.T) {
	r := execRunner(nil, false)
	if out, _, code, err := r([]string{"sh", "-c", "echo hi; exit 3"}); err != nil || code != 3 || out != "hi\n" {
		t.Errorf("out %q code %d err %v", out, code, err)
	}
	if _, _, _, err := r([]string{"/no/such/program-envcheck"}); err == nil {
		t.Error("a program that does not exist must be an error, not an exit code")
	}
	rq := execRunner([]string{"sh", "-c"}, true)
	if out, _, _, _ := rq([]string{"echo", "a b?"}); out != "a b?\n" {
		t.Errorf("quote_remote lost an argument: %q", out)
	}
}
