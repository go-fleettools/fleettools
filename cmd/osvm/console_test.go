package main

import (
	"strings"
	"testing"
)

func TestConsoleCommand(t *testing.T) {
	f := newFake(t)
	f.console = "line one\r\nline two\n\n\n"
	out, err := run(cloudArg, "console", "--server=vm", "--lines=5")
	if err != nil {
		t.Fatal(err)
	}
	if want := "line one\r\nline two\n"; out != want {
		t.Fatalf("out = %q, want %q (trailing blank lines trimmed to one newline)", out, want)
	}
}

func TestConsoleCommandCloudFailures(t *testing.T) {
	for name, key := range map[string]string{
		"auth":    "POST /v3/auth/tokens",
		"listing": "GET /compute/v2.1/servers/detail",
		"console": "POST /compute/v2.1/servers/s1/action",
	} {
		f := newFake(t)
		f.console = "boot"
		f.fail[key] = 0
		out, err := run(cloudArg, "console", "--server=vm")
		if err == nil || out != "" {
			t.Errorf("%s: err = %v, out = %q, want a refusal and no output", name, err, out)
			continue
		}
		// The listing failure must be reported as the server's 500, not
		// disguised as "want exactly one, got 0" by the check that follows it.
		if name != "auth" && !strings.Contains(err.Error(), "500") {
			t.Errorf("%s: err = %v, want the server's 500", name, err)
		}
	}
}

func TestConsoleCommandRefusals(t *testing.T) {
	f := newFake(t)
	f.console = "boot"
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"no cloud":    {[]string{"console", "--server=vm"}, "--cloud is required"},
		"no server":   {[]string{cloudArg, "console"}, "--server is required"},
		"zero lines":  {[]string{cloudArg, "console", "--server=vm", "--lines=0"}, "--lines must be 1..10000"},
		"huge lines":  {[]string{cloudArg, "console", "--server=vm", "--lines=10001"}, "--lines must be 1..10000"},
		"unknown vm":  {[]string{cloudArg, "console", "--server=nope"}, "want exactly one, got 0"},
		"extra  args": {[]string{cloudArg, "console", "--server=vm", "x"}, "unknown command"},
	} {
		_, err := run(c.args...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to contain %q", name, err, c.want)
		}
	}
}
