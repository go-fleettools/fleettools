package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// withTree points one repository at a temporary checkout and undoes it after.
func withTree(t *testing.T, repo string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := (treeFlag{}).Set(repo + "=" + root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { delete(trees, repo) })
	return root
}

// TestFileReadsTheTree is the test that would have caught the defect: the
// landing's cards exist ONLY in the checkout, and file must return them.
func TestFileReadsTheTree(t *testing.T) {
	withTree(t, "go-filesystems/go-filesystems.github.io", map[string]string{
		"hugo.toml":     "title = 'go-filesystems'\n",
		"data/fs.yaml":  "- name: cpio\n- name: xar\n",
		"data/team.yml": "- who: nobody\n",
	})

	got := file("go-filesystems/go-filesystems.github.io", "data/fs.yaml")
	if !strings.Contains(got, "cpio") {
		t.Errorf("file did not read the checkout: %q", got)
	}
	names, ok := treeDir("go-filesystems/go-filesystems.github.io", "data")
	if !ok {
		t.Fatal("treeDir says this repository has no tree")
	}
	if want := []string{"fs.yaml", "team.yml"}; !slices.Equal(names, want) {
		t.Errorf("dir = %v, want %v", names, want)
	}
}

// TestAbsentInATreeIsAbsent: the tree answers for the whole repository, so a
// missing file is missing — not a read that failed and might have been there.
// The surface then counts as one this organisation does not publish, which is
// the same answer the API gives for a 404.
func TestAbsentInATreeIsAbsent(t *testing.T) {
	withTree(t, "o/r", map[string]string{"hugo.toml": "x = 1\n"})

	body, ok := treeFile("o/r", "profile/README.md")
	if !ok {
		t.Fatal("a repository with a tree reported none")
	}
	if body != "" {
		t.Errorf("absent file returned %q", body)
	}
	if _, ok := treeDir("o/r", "data"); !ok {
		t.Error("treeDir must claim the repository even when the directory is absent")
	}
}

// TestOtherRepositoriesStillGoThroughTheAPI — the other direction, which is the
// one a guard tuned on a single input gets wrong. A tree for the landing must
// not make the profile and the docs read as empty: they belong to OTHER
// repositories and must fall through.
func TestOtherRepositoriesStillGoThroughTheAPI(t *testing.T) {
	withTree(t, "o/o.github.io", map[string]string{"hugo.toml": "x = 1\n"})

	if _, ok := treeFile("o/.github", "profile/README.md"); ok {
		t.Error("the profile was answered from the landing's tree")
	}
	if _, ok := treeDir("o/docs", "docs"); ok {
		t.Error("the docs were answered from the landing's tree")
	}
}

// TestSetRefusesWhatItCannotRead. A silent fallback to the API would restore
// the defect and hide it: the run would pass, and nothing would say the branch
// had never been read.
func TestSetRefusesWhatItCannotRead(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	afile := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(afile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, arg string }{
		{"no equals sign", "owner/repo"},
		{"empty repository", "=/tmp"},
		{"empty path", "owner/repo="},
		{"directory is not there", "owner/repo=" + missing},
		{"path is a file", "owner/repo=" + afile},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := (treeFlag{}).Set(c.arg); err == nil {
				delete(trees, "owner/repo")
				t.Fatalf("Set(%q) was accepted", c.arg)
			}
			if _, ok := trees["owner/repo"]; ok {
				delete(trees, "owner/repo")
				t.Error("a refused tree was registered anyway")
			}
		})
	}
}
