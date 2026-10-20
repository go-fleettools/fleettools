package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// site writes a built site with one page in it.
func site(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "guide"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"index.html":       "<html>home</html>",
		"guide/index.html": "<html>guide</html>",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestPublishingTheFirstVersion(t *testing.T) {
	tmp := t.TempDir()
	built := site(t, filepath.Join(tmp, "public"))
	pages := filepath.Join(tmp, "gh-pages")
	if err := os.MkdirAll(pages, 0o755); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := run([]string{"main", built, pages}, &out); err != nil {
		t.Fatal(err)
	}
	// ⛔ THE VERSION SITS AT THE BRANCH ROOT, not under docs/: a repository
	// called `docs` is already served at /docs/, and the mistake this command
	// exists for put the site at /docs/docs/main/.
	for _, want := range []string{
		filepath.Join("main", "index.html"),
		filepath.Join("main", "guide", "index.html"),
		"versions.json",
		"index.html",
		".nojekyll",
	} {
		if _, err := os.Stat(filepath.Join(pages, want)); err != nil {
			t.Errorf("%s is missing", want)
		}
	}
	if _, err := os.Stat(filepath.Join(pages, "docs")); err == nil {
		t.Error("it wrote a docs/ directory, which is the defect it exists to fix")
	}
	// latest points at main while no release is published.
	target, err := os.Readlink(filepath.Join(pages, "latest"))
	if err != nil || target != "main" {
		t.Errorf("latest -> %q, %v", target, err)
	}
	if !strings.Contains(out.String(), "latest is main") {
		t.Errorf("it said %q", out.String())
	}
}

// ⛔ 0.10 IS NEWER THAN 0.9, which string order gets wrong — and a site that
// offers "latest" has to mean it.
func TestWhichVersionIsLatest(t *testing.T) {
	tmp := t.TempDir()
	built := site(t, filepath.Join(tmp, "public"))
	pages := filepath.Join(tmp, "gh-pages")
	for _, v := range []string{"0.9", "0.10", "main"} {
		if err := os.MkdirAll(filepath.Join(pages, v), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := run([]string{"0.11", built, pages}, io.Discard); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(pages, "latest"))
	if err != nil || target != "0.11" {
		t.Fatalf("latest -> %q, %v", target, err)
	}

	b, err := os.ReadFile(filepath.Join(pages, "versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	// ⛔ The test declares the shape it expects rather than reusing the
	// producer's struct. Decoding with the writer's own type agrees with it by
	// construction: a renamed JSON tag would still round-trip, and the version
	// selector that actually reads this file would be the one to find out.
	var list []struct {
		Version    string          `json:"version"`
		Title      string          `json:"title"`
		Aliases    []string        `json:"aliases"`
		Properties map[string]bool `json:"properties,omitempty"`
	}
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, e := range list {
		order = append(order, e.Version)
	}
	// Newest release first, main last — the order mike wrote and the selector
	// reads.
	want := []string{"0.11", "0.10", "0.9", "main"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("versions.json lists %v, want %v", order, want)
	}
	if len(list[0].Aliases) != 1 || list[0].Aliases[0] != "latest" {
		t.Errorf("the newest release does not carry the latest alias: %+v", list[0])
	}
	// main is marked as development, which is how the selector greys it.
	if !list[3].Properties["development"] {
		t.Errorf("main is not marked development: %+v", list[3])
	}
	// ⛔ And the versions that were already there are UNTOUCHED: publishing
	// 0.11 must not disturb 0.9.
	if _, err := os.Stat(filepath.Join(pages, "0.9")); err != nil {
		t.Error("publishing a new version removed an old one")
	}
}

// ⛔ The one-off cleanup: the mistaken workflow left a docs/ directory holding
// version directories. It is removed — but only when it is that shape.
func TestTheMisplacedDirectoryIsCleanedUpOnce(t *testing.T) {
	t.Run("the shape the mistake made", func(t *testing.T) {
		tmp := t.TempDir()
		built := site(t, filepath.Join(tmp, "public"))
		pages := filepath.Join(tmp, "gh-pages")
		if err := os.MkdirAll(filepath.Join(pages, "docs", "main", "guide"), 0o755); err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		if err := run([]string{"main", built, pages}, &out); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(pages, "docs")); err == nil {
			t.Error("the misplaced directory is still there")
		}
		if !strings.Contains(out.String(), "one level too deep") {
			t.Errorf("it removed it without saying so: %q", out.String())
		}
	})

	t.Run("a docs page of somebody's own is left alone", func(t *testing.T) {
		tmp := t.TempDir()
		built := site(t, filepath.Join(tmp, "public"))
		pages := filepath.Join(tmp, "gh-pages")
		if err := os.MkdirAll(filepath.Join(pages, "docs"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pages, "docs", "index.html"), []byte("mine"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := run([]string{"main", built, pages}, io.Discard); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(pages, "docs", "index.html")); err != nil {
			t.Error("it deleted a page that was not the misplacement")
		}
	})

	t.Run("a docs directory holding something else is left alone", func(t *testing.T) {
		tmp := t.TempDir()
		built := site(t, filepath.Join(tmp, "public"))
		pages := filepath.Join(tmp, "gh-pages")
		if err := os.MkdirAll(filepath.Join(pages, "docs", "assets"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := run([]string{"main", built, pages}, io.Discard); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(pages, "docs", "assets")); err != nil {
			t.Error("it deleted a directory whose contents it does not recognise")
		}
	})
}

// A version directory that is a symlink (an alias) is replaced by a real
// directory, and what it pointed at survives.
func TestPublishingOverAnAlias(t *testing.T) {
	tmp := t.TempDir()
	built := site(t, filepath.Join(tmp, "public"))
	pages := filepath.Join(tmp, "gh-pages")
	if err := os.MkdirAll(filepath.Join(pages, "0.1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pages, "0.1", "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("0.1", filepath.Join(pages, "0.2")); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"0.2", built, pages}, io.Discard); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(filepath.Join(pages, "0.2"))
	if err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("0.2 is still a link: %v, %v", fi, err)
	}
	if _, err := os.Stat(filepath.Join(pages, "0.1", "keep.txt")); err != nil {
		t.Error("replacing the alias took what it pointed at with it")
	}
}

func TestWhatItRefuses(t *testing.T) {
	tmp := t.TempDir()
	built := site(t, filepath.Join(tmp, "public"))
	pages := filepath.Join(tmp, "gh-pages")
	if err := os.MkdirAll(pages, 0o755); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(tmp, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		args []string
		says string
	}{
		{"no arguments", nil, "usage"},
		{"a version that is not one", []string{"v0.16.0", built, pages}, "neither MAJOR.MINOR nor main"},
		{"three-part version", []string{"0.16.1", built, pages}, "neither MAJOR.MINOR nor main"},
		// ⛔ The build has to exist BEFORE anything is replaced: publishing an
		// empty directory over a live version cannot be undone from the
		// branch.
		{"a site that was never built", []string{"main", empty, pages}, "build the site first"},
		{"a gh-pages path that is not a directory", []string{"main", built, filepath.Join(tmp, "nope")}, "not a directory"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := run(c.args, io.Discard)
			if err == nil {
				t.Fatal("it was accepted")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the refusal says %q, which does not mention %q", err, c.says)
			}
		})
	}
}
