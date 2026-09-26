package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// trees maps owner/repo to a checked-out working tree on disk.
//
// ⛔ WITHOUT THIS THE CHECK ANSWERS THE WRONG QUESTION. Every surface is read
// over the API, which serves the DEFAULT BRANCH — so a pull_request run in an
// organisation's landing repository judges main, not the branch under review.
//
// Measured on go-filesystems.github.io#15: the pull request added ten cards and
// the check still reported all ten "only on profile", because it never read
// them. The false red is the harmless half. The same blindness means a pull
// request DELETING every card would have passed, and that is the reason to fix
// it rather than to re-run it after merging.
//
// A caller that checks out its own tree and names it here is judged on what it
// proposes. Every other surface still comes from the API, which is right: the
// question is whether the organisation's surfaces name the organisation's
// repositories, and only one of those surfaces is under review.
var trees = map[string]string{}

// treeFlag collects repeated -tree owner/repo=path.
type treeFlag struct{}

func (treeFlag) String() string { return "" }

// Set REFUSES a tree it cannot read rather than falling back to the API.
//
// ⛔ A silent fallback would restore the exact defect this flag removes, and
// hide it better: the check would pass, the caller would believe its branch had
// been read, and nothing in the log would say otherwise.
func (treeFlag) Set(v string) error {
	repo, path, ok := strings.Cut(v, "=")
	if !ok || repo == "" || path == "" {
		return fmt.Errorf("-tree wants owner/repo=path, got %q", v)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("-tree %s: %w", repo, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("-tree %s: %s is not a directory", repo, abs)
	}
	trees[repo] = abs
	return nil
}

// treeFile reads one path from a checked-out tree.
//
// ok is false when this repository has no tree, which is a different answer
// from the file being absent — and in a tree, absent is ABSENT: there is no
// failed read to confuse it with, unlike the API path in file().
func treeFile(repo, path string) (string, bool) {
	root, ok := trees[repo]
	if !ok {
		return "", false
	}
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return "", true
	}
	return string(b), true
}

// treeDir is the names in a directory of a checked-out tree.
func treeDir(repo, path string) ([]string, bool) {
	root, ok := trees[repo]
	if !ok {
		return nil, false
	}
	ents, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return nil, true
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		out = append(out, e.Name())
	}
	// The API lists in its own order; sorting here keeps a tree run and an API
	// run comparable, which is what makes the two readable side by side.
	sort.Strings(out)
	return out, true
}
