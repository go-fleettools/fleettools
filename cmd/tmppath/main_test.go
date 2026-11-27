package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

// tree writes files (path -> content) under a fresh directory and returns it.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func drive(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out, errb strings.Builder
	code := cli.Drive(RootCmd(), args, &out, &errb)
	return code, out.String() + errb.String()
}

// one runs the command over a single file and returns status and output.
func one(t *testing.T, src string) (int, string) {
	t.Helper()
	return drive(t, tree(t, map[string]string{"a.go": src}))
}

func TestEachPatternIsFlagged(t *testing.T) {
	cases := map[string]string{
		"tmp literal":       "package a\nvar p = \"/tmp/ext4_log\"\n",
		"bare /tmp":         "package a\nvar p = \"/tmp\"\n",
		"var tmp literal":   "package a\nvar p = \"/var/tmp/x\"\n",
		"raw literal":       "package a\nvar p = `/tmp/x`\n",
		"os.TempDir":        "package a\nimport \"os\"\nvar p = os.TempDir()\n",
		"ioutil.TempFile":   "package a\nimport \"io/ioutil\"\nfunc f() { ioutil.TempFile(\"\", \"x\") }\n",
		"ioutil.TempDir":    "package a\nimport \"io/ioutil\"\nfunc f() { ioutil.TempDir(\"\", \"x\") }\n",
		"aliased import":    "package a\nimport o \"os\"\nvar p = o.TempDir()\n",
		"taken as value":    "package a\nimport \"os\"\nvar p = os.TempDir\n",
		"dot import os":     "package a\nimport . \"os\"\nvar p = TempDir()\n",
		"dot import ioutil": "package a\nimport . \"io/ioutil\"\nvar p = 1\n",
	}
	for name, src := range cases {
		code, out := one(t, src)
		if code != 1 || !strings.Contains(out, "a.go:") || !strings.Contains(out, "findings=1") {
			t.Errorf("%s: status %d:\n%s", name, code, out)
		}
	}
}

func TestTheLineIsReported(t *testing.T) {
	code, out := one(t, "package a\n\nvar x = 1\nvar p = \"/tmp/y\"\n")
	if code != 1 || !strings.Contains(out, "a.go:4: ") {
		t.Errorf("status %d:\n%s", code, out)
	}
}

func TestWhatIsSafeIsNotFlagged(t *testing.T) {
	cases := map[string]string{
		"CreateTemp":         "package a\nimport \"os\"\nfunc f() { os.CreateTemp(\"\", \"x\") }\n",
		"MkdirTemp":          "package a\nimport \"os\"\nfunc f() { os.MkdirTemp(\"\", \"x\") }\n",
		"slash-less prefix":  "package a\nvar p = \"/tmpl/x\"\n",
		"not at the start":   "package a\nvar p = \"x/tmp/y\"\n",
		"local named os":     "package a\nimport \"os\"\ntype t struct{}\nfunc (t) TempDir() string { return \"\" }\nfunc f() { os := t{}; _ = os.TempDir() }\n",
		"parameter named os": "package a\nimport \"os\"\ntype t struct{}\nfunc (t) TempDir() string { return \"\" }\nfunc f(os t) { _ = os.TempDir() }\n",
		"chained selector":   "package a\nimport \"os\"\ntype t struct{ os struct{ TempDir func() } }\nfunc f(x t) { _ = os.Getpid; x.os.TempDir() }\n",
		"undeclared package": "package a\nvar p = foo.TempDir()\n",
		"blank import":       "package a\nimport _ \"os\"\n",
		"other package":      "package a\nimport \"example.com/os\"\nvar p = os.TempDir()\n",
	}
	for name, src := range cases {
		if code, out := one(t, src); code != 0 {
			t.Errorf("%s: status %d:\n%s", name, code, out)
		}
	}
}

func TestACommentNeverMatches(t *testing.T) {
	src := "package a\n\n// uses os.TempDir() and \"/tmp/x\" in a comment\n/* ioutil.TempFile and /var/tmp */\nvar x = 1 // os.TempDir()\n"
	if code, out := one(t, src); code != 0 {
		t.Errorf("status %d:\n%s", code, out)
	}
}

func TestAnAllowCommentNeedsAReason(t *testing.T) {
	with := "package a\nvar p = \"/tmp/.veracrypt_aux_mnt1\" //tmppath:ok VeraCrypt's own path inside the test VM\n"
	if code, out := one(t, with); code != 0 {
		t.Errorf("with a reason: status %d:\n%s", code, out)
	}
	for name, c := range map[string]string{"empty": "//tmppath:ok", "blank": "//tmppath:ok   "} {
		code, out := one(t, "package a\nvar p = \"/tmp/x\" "+c+"\n")
		if code != 1 || !strings.Contains(out, "no reason") || !strings.Contains(out, "findings=2") {
			t.Errorf("%s: status %d:\n%s", name, code, out)
		}
	}
	// the reason accepts only its own line
	code, out := one(t, "package a\nvar a = \"/tmp/x\" //tmppath:ok reason\nvar b = \"/tmp/y\"\n")
	if code != 1 || !strings.Contains(out, "a.go:3: ") || strings.Contains(out, "a.go:2: ") {
		t.Errorf("other line: status %d:\n%s", code, out)
	}
	// a different word is not the marker
	if code, _ := one(t, "package a\nvar p = \"/tmp/x\" //tmppath:okay yes\n"); code != 1 {
		t.Errorf("tmppath:okay accepted a finding")
	}
}

func TestWhatIsNotWalked(t *testing.T) {
	bad := "package a\nvar p = \"/tmp/x\"\n"
	root := tree(t, map[string]string{
		"a_test.go":         bad,
		"testdata/b.go":     bad,
		".hidden/c.go":      bad,
		"sub/testdata/d.go": bad,
		"notes.txt":         "/tmp/x",
		"ok.go":             "package a\n",
	})
	code, out := drive(t, root)
	if code != 0 || !strings.Contains(out, "files=1 ") {
		t.Errorf("status %d:\n%s", code, out)
	}
	// but a nested directory is
	root = tree(t, map[string]string{"sub/deep/e.go": bad})
	if code, out := drive(t, root); code != 1 || !strings.Contains(out, filepath.Join("sub", "deep", "e.go")+":2: ") {
		t.Errorf("nested: status %d:\n%s", code, out)
	}
}

func TestAParseErrorIsUnreadNeverClean(t *testing.T) {
	root := tree(t, map[string]string{"bad.go": "package a\nfunc (", "ok.go": "package a\n"})
	code, out := drive(t, root)
	if code != 2 || !strings.Contains(out, "UNREAD ") || !strings.Contains(out, "unread=1") {
		t.Errorf("status %d:\n%s", code, out)
	}
	// unread outranks a finding: the answer is not known
	root = tree(t, map[string]string{"bad.go": "package a\nfunc (", "f.go": "package a\nvar p = \"/tmp/x\"\n"})
	if code, out := drive(t, root); code != 2 || !strings.Contains(out, "f.go:2: ") {
		t.Errorf("unread and finding: status %d:\n%s", code, out)
	}
}

func TestAMissingDirectoryIsUnread(t *testing.T) {
	code, out := drive(t, filepath.Join(t.TempDir(), "absent"))
	if code != 2 || !strings.Contains(out, "named directory") || !strings.Contains(out, "unread=1") {
		t.Errorf("status %d:\n%s", code, out)
	}
}

func TestAnUnreadableSubdirectoryIsUnread(t *testing.T) {
	root := tree(t, map[string]string{"locked/a.go": "package a\n", "ok.go": "package a\n"})
	locked := filepath.Join(root, "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, err := os.ReadDir(locked); err == nil {
		t.Skip("directory permissions are not enforced here (root)")
	}
	code, out := drive(t, root)
	if code != 2 || !strings.Contains(out, "UNREAD "+locked) || !strings.Contains(out, "files=1 ") {
		t.Errorf("status %d:\n%s", code, out)
	}
}

func TestSeveralDirectoriesAreAllRead(t *testing.T) {
	a := tree(t, map[string]string{"a.go": "package a\nvar p = \"/tmp/x\"\n"})
	b := tree(t, map[string]string{"b.go": "package b\nimport \"os\"\nvar p = os.TempDir()\n"})
	code, out := drive(t, a, b)
	if code != 1 || !strings.Contains(out, "findings=2") {
		t.Errorf("status %d:\n%s", code, out)
	}
}

func TestDefaultIsTheCurrentDirectory(t *testing.T) {
	root := tree(t, map[string]string{"a.go": "package a\nvar p = \"/tmp/x\"\n"})
	t.Chdir(root)
	if code, out := drive(t); code != 1 || !strings.Contains(out, "a.go:2: ") {
		t.Errorf("status %d:\n%s", code, out)
	}
}

func TestHelpIsNotAScan(t *testing.T) {
	root := tree(t, map[string]string{"a.go": "package a\nvar p = \"/tmp/x\"\n"})
	t.Chdir(root)
	if code, out := drive(t, "--help"); code != 0 || strings.Contains(out, "SUMMARY") {
		t.Errorf("status %d:\n%s", code, out)
	}
	c := RootCmd()
	c.InitDefaultHelpFlag()
	if err := c.Flags().Set("help", "true"); err != nil {
		t.Fatal(err)
	}
	if err := c.RunE(c, nil); err != nil {
		t.Errorf("RunE with --help set: %v", err)
	}
}
