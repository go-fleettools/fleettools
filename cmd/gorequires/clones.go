package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// The clone mode reads EVERY go.mod of a repository, nested modules included.
// ⛔ The remote mode cannot: raw.githubusercontent.com serves one named path,
// and listing a tree is one REST call per repository. A landing that pins a
// module in notebooks/go.mod (go-numerics.github.io pinned go-notebook/playground
// there, at v0.7.1 while v0.16.0 was out) is invisible to a root-only read, and
// "no root go.mod requires it" read as "nobody requires it". Here the repository
// is shallow-cloned (git, not REST: no API budget) and walked.

// skipDir mirrors modtidy's rule, which is the go command's: vendor/, testdata/
// and directories starting with "." or "_" hold no module of this repository.
func skipDir(name string) bool {
	return name == "vendor" || name == "testdata" ||
		strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// scanClone reads every go.mod under dir (dir itself is never skipped, so a
// fixture kept under testdata/ can be the subject) and returns the hits, how
// many go.mod files it read, and the ones it could not parse. An unparsable
// go.mod is reported, never counted as "does not require it".
func scanClone(dir, mod string) (hits []Hit, read int, unread []string, err error) {
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			if p != dir && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "go.mod" {
			return nil
		}
		rel, _ := filepath.Rel(dir, filepath.Dir(p))
		rel = filepath.ToSlash(rel)
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			unread = append(unread, rel+": "+rerr.Error())
			return nil
		}
		h, has, perr := Requires(b, mod)
		if perr != nil {
			unread = append(unread, rel+": "+perr.Error())
			return nil
		}
		read++
		if has {
			h.Path = rel
			hits = append(hits, h)
		}
		return nil
	})
	sort.Slice(hits, func(i, j int) bool { return hits[i].Path < hits[j].Path })
	return
}

// cloneFunc makes dest a shallow clone of org/repo's default branch, or brings
// an existing one up to date.
type cloneFunc func(org, repo, dest string) error

func gitClone(org, repo, dest string) error {
	run := func(args ...string) error {
		c := exec.Command("git", args...)
		c.Stdin = nil
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		if err := run("-C", dest, "fetch", "--depth", "1", "origin", "HEAD"); err != nil {
			return err
		}
		return run("-C", dest, "checkout", "-q", "--detach", "FETCH_HEAD")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return run("clone", "-q", "--depth", "1", "https://github.com/"+org+"/"+repo, dest)
}

func runClones(o options, stdout, stderr io.Writer, clone cloneFunc) int {
	inv, ok := readInventory(o.inventory, stderr)
	if !ok {
		return 2
	}
	var repos []string
	for _, r := range inv.Repos {
		if r.Archived && !o.archived {
			continue
		}
		if o.name != "" {
			if m, err := path.Match(o.name, r.Name); err != nil || !m {
				continue
			}
		}
		repos = append(repos, r.Org+"/"+r.Name)
	}
	type result struct {
		hits   []Hit
		read   int
		unread []string
		err    error
	}
	results := make([]result, len(repos))
	var wg sync.WaitGroup
	sem := make(chan struct{}, o.jobs)
	for i, full := range repos {
		wg.Add(1)
		go func(i int, full string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			org, name, _ := strings.Cut(full, "/")
			dest := filepath.Join(o.cloneDir, org, name)
			if err := clone(org, name, dest); err != nil {
				results[i].err = err
				return
			}
			h, n, un, err := scanClone(dest, o.module)
			results[i] = result{hits: h, read: n, unread: un, err: err}
		}(i, full)
	}
	wg.Wait()

	var unread []string
	var hitCount, goMods, withMods int
	for i, r := range results {
		full := repos[i]
		if r.err != nil {
			unread = append(unread, full+": "+r.err.Error())
			continue
		}
		for _, u := range r.unread {
			unread = append(unread, full+"/"+u)
		}
		goMods += r.read
		if r.read > 0 {
			withMods++
		}
		for _, h := range r.hits {
			if o.version != "" && h.Version != o.version {
				continue
			}
			hitCount++
			kind := "direct"
			if h.Indirect {
				kind = "indirect"
			}
			where := full
			if h.Path != "." {
				where += "/" + h.Path
			}
			fmt.Fprintf(stdout, "%-56s %-10s %-8s %s\n", where, h.Version, kind, h.Module)
		}
	}
	sort.Strings(unread)
	for _, u := range unread {
		fmt.Fprintln(stderr, "gorequires: unread:", u)
	}
	want := o.module
	if o.version != "" {
		want += "@" + o.version
	}
	fmt.Fprintf(stdout, "\n%d repositories cloned (inventory of %s), %d go.mod read in %d of them (nested modules included), %d UNREAD: %d go.mod require %s\n",
		len(repos), inv.Taken.Format("2006-01-02 15:04"), goMods, withMods, len(unread), hitCount, want)
	fmt.Fprintln(stdout, "not seen: private repositories, repositories created after the inventory, non-default branches")
	if len(unread) > 0 {
		return 2
	}
	return 0
}
