package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeUnlisted(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, UnlistedFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { delete(unlisted, "o") })
	return root
}

func TestAReasonIsRead(t *testing.T) {
	root := writeUnlisted(t, "# a comment\n\nis  a placeholder: a README with a banner and a heading, no code\n")
	if err := readUnlisted("o", root); err != nil {
		t.Fatal(err)
	}
	why, ok := unlisted["o"]["is"]
	if !ok {
		t.Fatal("the entry was not read")
	}
	if want := "a placeholder: a README with a banner and a heading, no code"; why != want {
		t.Errorf("reason = %q, want %q", why, want)
	}
}

// TestANameWithoutAReasonIsRefused. An allowance that can be written silently
// is the check switched off one line at a time.
func TestANameWithoutAReasonIsRefused(t *testing.T) {
	for _, body := range []string{"is\n", "is   \n", "ok fine\nis\n"} {
		root := writeUnlisted(t, body)
		err := readUnlisted("o", root)
		if err == nil {
			t.Errorf("%q was accepted", body)
			continue
		}
		if _, silent := unlisted["o"]["is"]; silent {
			t.Errorf("%q registered the name anyway", body)
		}
	}
}

// TestNoFileAllowsNothing — absence is not permission.
func TestNoFileAllowsNothing(t *testing.T) {
	if err := readUnlisted("o", t.TempDir()); err != nil {
		t.Fatalf("a missing file is not an error: %v", err)
	}
	if len(unlisted["o"]) != 0 {
		t.Errorf("a missing file allowed %v", unlisted["o"])
	}
}

// TestAnUnreadableFileIsAnError, because a permission that cannot be read must
// not read as no permissions asked for — that is the quiet direction again.
func TestAnUnreadableFileIsAnError(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, UnlistedFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := readUnlisted("o", root); err == nil {
		t.Error("a directory in place of the file was accepted")
	}
}

func TestOrgOf(t *testing.T) {
	for in, want := range map[string]string{
		"go-composites/go-composites.github.io": "go-composites",
		"nolash":                                "nolash",
		"":                                      "",
	} {
		if got := orgOf(in); got != want {
			t.Errorf("orgOf(%q) = %q, want %q", in, got, want)
		}
	}
}
