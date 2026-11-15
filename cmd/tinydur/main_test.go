package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

const planted = `package srv

import (
	"testing"
	"time"
)

func TestStale(t *testing.T) {
	cfg := Config{Refresh: Duration(time.Nanosecond)}
	_ = cfg
}
`

func TestScan(t *testing.T) {
	cases := []struct {
		name  string
		file  string
		src   string
		lines []int
	}{
		{"planted positive: Nanosecond as Refresh", "srv_test.go", planted, []int{9}},
		{"Microsecond in a var", "a_test.go", "package a\nimport \"time\"\nvar ttl = 5 * time.Microsecond\n", []int{3}},
		{"two uses", "a_test.go", "package a\nimport \"time\"\nvar a, b = time.Nanosecond, time.Microsecond\n", []int{3, 3}},
		{"aliased import is followed", "a_test.go", "package a\nimport tm \"time\"\nvar d = tm.Nanosecond\n", []int{3}},
		{"normal durations", "a_test.go", "package a\nimport \"time\"\nvar d = time.Minute + time.Millisecond\n", nil},
		{"non-test file is not flagged", "srv.go", planted, nil},
		{"another package named time", "a_test.go", "package a\nimport time \"example.com/time\"\nvar d = time.Nanosecond\n", nil},
		{"waiver with reason", "a_test.go", "package a\nimport \"time\"\nvar d = time.Nanosecond //fleet:allow-tiny-duration measuring clock resolution itself\n", nil},
		{"waiver without reason is ignored", "a_test.go", "package a\nimport \"time\"\nvar d = time.Nanosecond //fleet:allow-tiny-duration\n", []int{3}},
		{"waiver on another line does not cover", "a_test.go", "package a\nimport \"time\"\n//fleet:allow-tiny-duration reason\nvar d = time.Nanosecond\n", []int{4}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Scan(c.file, []byte(c.src))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(c.lines) {
				t.Fatalf("got %d findings %v, want lines %v", len(got), got, c.lines)
			}
			for i, f := range got {
				if f.Line != c.lines[i] {
					t.Errorf("finding %d on line %d, want %d", i, f.Line, c.lines[i])
				}
			}
		})
	}
}

func TestScanRefusesUnparseable(t *testing.T) {
	if _, err := Scan("a_test.go", []byte("package")); err == nil {
		t.Fatal("an unparseable file must be an error, not a clean result")
	}
}

func drive(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := cli.Drive(RootCmd(), args, &out, &errb)
	return code, out.String() + errb.String(), errb.String()
}

func TestRunStatuses(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "srv_test.go"), []byte(planted), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if got := run([]string{dir}, &out, &errb); got != 1 {
		t.Fatalf("planted positive: status %d, want 1\n%s%s", got, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "srv_test.go:9:") || !strings.Contains(out.String(), "deterministically") {
		t.Errorf("output lacks file:line and reason:\n%s", out.String())
	}

	clean := t.TempDir()
	if err := os.WriteFile(filepath.Join(clean, "a_test.go"), []byte("package a\nimport \"time\"\nvar d = time.Minute\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if got := run([]string{clean}, &out, &errb); got != 0 {
		t.Fatalf("clean: status %d, want 0\n%s", got, out.String())
	}

	empty := t.TempDir()
	if got := run([]string{empty}, &out, &errb); got != 2 {
		t.Fatalf("no test file read: status %d, want 2", got)
	}
	if got := run([]string{filepath.Join(empty, "missing")}, &out, &errb); got != 2 {
		t.Fatalf("missing path: status %d, want 2", got)
	}
}

func TestRootCmdNoArgsAndHelp(t *testing.T) {
	if code, _, _ := drive(t); code != 2 {
		t.Errorf("no args: status %d, want 2", code)
	}
	code, out, _ := drive(t, "--help")
	if code != 0 || !strings.Contains(out, "//fleet:allow-tiny-duration") {
		t.Errorf("help: status %d, out %q", code, out)
	}
}
