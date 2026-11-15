package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestABrokenGoModIsUnreadNotSkipped pins the distinction the skip could have
// erased. A go.mod holding garbage has no .go file beside it either, so "no Go
// source" alone would have called it skipped -- a CLEAN answer reached without
// reading the file that is wrong. UNREAD is the honest one, and it outranks
// everything.
func TestABrokenGoModIsUnreadNotSkipped(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "")
	write(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.20\n")
	write(t, filepath.Join(root, "a.go"), "package root\n")
	write(t, filepath.Join(root, "broken", "go.mod"), "this is not a go.mod\n")

	code, out, _ := run(t, root)
	if code != 2 {
		t.Fatalf("exit = %d, want 2\n%s", code, out)
	}
	if !strings.Contains(out, "UNREAD  broken") {
		t.Fatalf("a broken go.mod must be UNREAD:\n%s", out)
	}
	if strings.Contains(out, "skipped broken") {
		t.Fatalf("a broken go.mod was skipped, which reads as clean:\n%s", out)
	}
}
