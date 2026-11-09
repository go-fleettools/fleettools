package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// residue is one large blob that only pull-request refs reach.
type residue struct {
	ID    string
	Size  int64
	Paths []string // the paths rev-list named it under
	PRs   []int    // the pull requests whose refs reach it
	Added string   // the first commit, on a pull ref, that introduced it
}

// git runs git in dir and returns its standard output; its standard error goes
// into the error, which is where git says what went wrong.
func git(dir string, stdin string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out, nil
}

func lines(b []byte) []string {
	s := strings.TrimRight(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// pullNumber reads N from refs/pull/N/head or refs/pull/N/merge.
func pullNumber(ref string) (int, bool) {
	rest, ok := strings.CutPrefix(ref, "refs/pull/")
	if !ok {
		return 0, false
	}
	num, kind, ok := strings.Cut(rest, "/")
	if !ok || (kind != "head" && kind != "merge") {
		return 0, false
	}
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// scan returns the blobs of at least min bytes that some refs/pull/* reaches and
// no branch or tag does, largest first, with the number of pull refs and of
// objects it read: a scan that read nothing has not looked, and the caller says
// so instead of reporting zero.
func scan(dir string, min int64) (res []residue, pullRefs, objects int, err error) {
	out, err := git(dir, "", "for-each-ref", "--format=%(refname)", "refs/pull")
	if err != nil {
		return nil, 0, 0, err
	}
	type found struct {
		paths []string
		prs   []int
		ref   string // a pull ref that reaches it, for --find-object
	}
	byID := map[string]*found{}
	var order []string
	for _, ref := range lines(out) {
		n, ok := pullNumber(ref)
		if !ok {
			continue
		}
		pullRefs++
		objs, err := git(dir, "", "rev-list", "--objects", ref, "--not", "--branches", "--tags")
		if err != nil {
			return nil, pullRefs, objects, err
		}
		for _, l := range lines(objs) {
			id, path, _ := strings.Cut(l, " ")
			f := byID[id]
			if f == nil {
				f = &found{ref: ref}
				byID[id] = f
				order = append(order, id)
			}
			if path != "" && !contains(f.paths, path) {
				f.paths = append(f.paths, path)
			}
			if !containsInt(f.prs, n) {
				f.prs = append(f.prs, n)
			}
		}
	}
	objects = len(order)
	if objects == 0 {
		return nil, pullRefs, 0, nil
	}
	check, err := git(dir, strings.Join(order, "\n")+"\n", "cat-file", "--batch-check=%(objecttype) %(objectname) %(objectsize)")
	if err != nil {
		return nil, pullRefs, objects, err
	}
	for _, l := range lines(check) {
		f := strings.Fields(l)
		if len(f) != 3 || f[0] != "blob" {
			continue
		}
		size, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil || size < min {
			continue
		}
		fd := byID[f[1]]
		r := residue{ID: f[1], Size: size, Paths: fd.paths, PRs: fd.prs}
		sort.Ints(r.PRs)
		added, err := git(dir, "", "log", "--format=%H", "--reverse", "--find-object="+f[1], fd.ref, "--not", "--branches", "--tags")
		if err != nil {
			return nil, pullRefs, objects, err
		}
		if ls := lines(added); len(ls) > 0 {
			r.Added = ls[0]
		}
		res = append(res, r)
	}
	sort.Slice(res, func(i, j int) bool {
		if res[i].Size != res[j].Size {
			return res[i].Size > res[j].Size
		}
		return res[i].ID < res[j].ID
	})
	return res, pullRefs, objects, nil
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// human prints a size in KiB or MiB with one decimal.
func human(n int64) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
}

// parseSize reads 1234, 512KiB, 16MiB (or K, M, G as the same powers of 1024).
func parseSize(s string) (int64, error) {
	mult := int64(1)
	for _, u := range []struct {
		suffix string
		m      int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10}} {
		if strings.HasSuffix(s, u.suffix) {
			s, mult = strings.TrimSuffix(s, u.suffix), u.m
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("size %q: want a byte count such as 1048576 or 1MiB", s)
	}
	return n * mult, nil
}
