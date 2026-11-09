package ghtoken

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path string, b []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, b, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil { // the umask must not decide
		t.Fatal(err)
	}
}

func TestRead(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	write(t, good, []byte("  ghp_secret\n"), 0o600)
	if tok, err := Read(good); err != nil || tok != "ghp_secret" {
		t.Fatalf("%q, %v", tok, err)
	}

	for name, c := range map[string]struct {
		b    []byte
		mode os.FileMode
		want string
	}{
		"group-readable": {[]byte("x"), 0o640, "0600"},
		"world-readable": {[]byte("x"), 0o604, "0600"},
		"empty":          {nil, 0o600, "empty"},
		"blank":          {[]byte(" \n\t"), 0o600, "empty"},
		"too large":      {make([]byte, MaxBytes+1), 0o600, "over"},
	} {
		p := filepath.Join(dir, strings.ReplaceAll(name, " ", "-"))
		write(t, p, c.b, c.mode)
		if _, err := Read(p); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error naming %q", name, err, c.want)
		}
	}
	// Exactly the bound is accepted.
	edge := filepath.Join(dir, "edge")
	write(t, edge, []byte(strings.Repeat("a", MaxBytes)), 0o600)
	if tok, err := Read(edge); err != nil || len(tok) != MaxBytes {
		t.Errorf("a token of exactly MaxBytes: %d, %v", len(tok), err)
	}
	if _, err := Read(dir); err == nil || !strings.Contains(err.Error(), "regular") {
		t.Errorf("a directory: %v", err)
	}
	if _, err := Read(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing file was accepted")
	}
}

func TestReadDefaultsToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, filepath.Join(home, ".github-token"), []byte("from-home\n"), 0o600)
	if tok, err := Read(""); err != nil || tok != "from-home" {
		t.Fatalf("%q, %v", tok, err)
	}
}
