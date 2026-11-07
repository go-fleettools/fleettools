package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔ THIS REPOSITORY'S OWN WORKFLOWS CALLED ITS OWN COMMANDS WITH ONE DASH.
// Found 2026-11-07, relayed through two sessions' lease notes: `vuln.yml` had
// SEVEN such call sites — `/tmp/vulngate -go`, and `/tmp/vulnscan` with
// `-selftest` twice, `-list`, `-go`, `-state`, `-json`.
//
// Every command here moved to cobra in v0.53.0. The single-dash spelling still
// works, through the compatibility shim in internal/cli — which prints a
// warning on every run and announces its own removal. So the lane was GREEN and
// carried a warning nobody reads, and the day the shim goes it turns red for a
// reason unrelated to anything it measures. The same fix had already been made
// in go-gitsafe/gitsafe#86, which is what makes this the second occurrence and
// therefore a gate rather than another note.
//
// It reads the workflow TEXT, because that is where the defect lives: the
// commands themselves are fine.
func TestNoWorkflowCallsOurOwnCommandsWithASingleDash(t *testing.T) {
	cmds := ourCommands(t)
	if len(cmds) < 20 {
		// A detector that found no commands would clear every workflow.
		t.Fatalf("only %d commands found under cmd/; the scan would pass vacuously", len(cmds))
	}

	files, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no workflow read: nothing was checked, which is not the same as nothing being wrong")
	}

	var bad []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			for _, flag := range singleDashAfterCommand(line, cmds) {
				bad = append(bad, filepath.Base(f)+":"+itoa(i+1)+" "+flag)
			}
		}
	}
	if len(bad) > 0 {
		t.Errorf("%d call site(s) spell a cobra flag with one dash, so the shim warns inside a green lane:\n  %s",
			len(bad), strings.Join(bad, "\n  "))
	}
}

// ⛔ The planted positive. A guard nobody has seen fire is a guard that may not
// fire, and this one is a text scan over files that are currently clean — so
// without this it would pass for ever, including after somebody re-adds exactly
// the call it exists to stop.
func TestTheSingleDashGateCanFire(t *testing.T) {
	cmds := map[string]bool{"vulnscan": true, "vulngate": true}
	for _, c := range []struct {
		line string
		want int
		why  string
	}{
		{`        run: /tmp/vulnscan -selftest`, 1, "the exact shape found in vuln.yml"},
		{`        run: /tmp/vulnscan -list x.txt -go "$(go env GOVERSION)" -json /tmp/o.json`, 3, "every flag of a line, not just the first"},
		{`          /tmp/vulngate -go "$(go env GOVERSION)" /tmp/vuln.json`, 1, "a bare run line, no `run:` prefix"},

		{`        run: /tmp/vulnscan --selftest`, 0, "the corrected spelling"},
		{`          go build -o /tmp/vulnscan ./cmd/vulnscan`, 0, "⛔ -o belongs to `go build` and PRECEDES the command: scanning the whole line would fail this"},
		{`        run: /tmp/vulnscan --list .github/vulnscan-smoke.txt`, 0, "a path containing a command name is not a second call"},
		{`          gh pr list --json number -L 5`, 0, "another tool's flags are not ours"},
		{`        run: /tmp/vulnscan --json - --list x`, 0, "a bare dash is stdin, not a flag"},
		{"      # `vulnscan … || vulnscan … -retry-unread` -- a hole since removed", 0,
			"⛔ prose in a COMMENT: the first version of this flagged vuln.yml:233 for exactly this"},
	} {
		if got := len(singleDashAfterCommand(c.line, cmds)); got != c.want {
			t.Errorf("%d single-dash flag(s), want %d — %s\n  %s", got, c.want, c.why, c.line)
		}
	}
}

// ourCommands enumerates cmd/ rather than naming the commands. A hand-written
// list inherits the blind spot of whoever wrote it: the flag-to-cobra migration
// itself was costed against a list of fifteen names in a repository that had
// sixteen.
func ourCommands(t *testing.T) map[string]bool {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join("..", "..", "cmd", "*"))
	if err != nil {
		t.Fatal(err)
	}
	cmds := map[string]bool{}
	for _, d := range dirs {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			cmds[filepath.Base(d)] = true
		}
	}
	return cmds
}

// singleDashAfterCommand returns the single-dash flags a line passes to one of
// our commands. It scans only AFTER the command's own token, because the flags
// before it belong to whatever is running it — `go build -o /tmp/vulnscan` is
// not a call to vulnscan at all.
func singleDashAfterCommand(line string, cmds map[string]bool) []string {
	// ⛔ A COMMENT IS NOT A CALL SITE. The first version of this flagged
	// vuln.yml:233, which is prose inside a comment describing a hole its
	// author had already removed: "`vulnscan … || vulnscan … -retry-unread` --
	// is a hole I wrote and then removed". Refusing something the protected
	// thing accepts is a defect of the same rank as missing one, so the scan
	// stops where YAML's and the shell's comments start.
	if t := strings.TrimLeft(line, " \t"); strings.HasPrefix(t, "#") {
		return nil
	}
	fields := strings.Fields(line)
	start := -1
	for i, f := range fields {
		// `/tmp/vulnscan`, `./vulnscan`, `vulnscan` — the last path element.
		if cmds[filepath.Base(strings.TrimSuffix(f, ":"))] && !strings.HasSuffix(f, ".go") {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	var out []string
	for _, f := range fields[start+1:] {
		if len(f) > 1 && f[0] == '-' && f[1] != '-' {
			out = append(out, f)
		}
	}
	return out
}

// (`itoa` is the package's own, in vintage_reach_test.go — a second copy of a
// helper this package already had is how two spellings of one thing start.)
