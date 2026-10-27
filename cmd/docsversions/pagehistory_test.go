package main

import (
	"strings"
	"testing"
)

// The page history generator prints JSON on stdout and progress on stderr
// ("go: downloading ..."): the two must not be mixed.
func TestRunStdoutKeepsStderrApart(t *testing.T) {
	out, err := runStdout(t.TempDir(), "sh", "-c", "echo data; echo noise >&2")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "data\n" {
		t.Fatalf("stdout = %q", out)
	}
	_, err = runStdout(t.TempDir(), "sh", "-c", "echo boom >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error lacks the program's stderr: %v", err)
	}
}

func TestPageHistoryModuleIsPinned(t *testing.T) {
	if !strings.HasPrefix(pageHistoryModule, "github.com/tannevaled/hextra/cmd/pagehistory@v") {
		t.Fatalf("not a pinned module: %s", pageHistoryModule)
	}
}
