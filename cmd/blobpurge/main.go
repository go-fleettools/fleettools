// blobpurge removes blobs from a repository's history BY ID, and proves what
// the rewrite did before anybody pushes it.
//
//	blobpurge scan [-min 512KiB] <git-dir>
//	blobpurge strip -blob <id> [-blob <id> ...] -to <new-git-dir> <git-dir>
//
// scan lists every blob at least -min bytes that some branch or tag reaches and
// that HEAD's tree no longer holds: a binary committed by mistake and deleted
// since, still cloned by everybody. Beside each one it names the TAGS whose
// trees hold it, because those are the released trees a rewrite will change.
//
// strip writes the rewritten history into a NEW repository, so the original is
// untouched whatever happens, and then checks every branch and tag against it:
// the same refs, the same number of commits, the same authors, committers,
// dates and messages, and a tree equal to the old one minus exactly the
// stripped blobs. It does not push. Pushing a rewritten main is an act somebody
// takes on purpose, after reading what this printed.
//
// It strips by blob ID, never by path, because a path names every version of a
// file: stripping `bridge` would also strip a later, legitimate file of that
// name. And it refuses a blob HEAD still holds: delete it in a reviewed commit
// first, so the current tree is already right before history is touched. A
// tree that does not hold the blob comes out byte-identical, which is what
// keeps the module hash of every release made after the deletion unchanged.
//
// It replaces a `git filter-branch --index-filter` recipe whose filter was a
// shell script, ran once per commit, and whose only check was whatever its
// operator remembered to run afterwards.
//
// The history is read through `git fast-export --show-original-ids`, which
// names every blob by its original ID, and written by `git fast-import`. Both
// ship with git. Commit signatures cannot survive a rewrite, since a signature
// covers the parent IDs, so they are stripped and the count is reported.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

func main() {
	fleet.WarnIfStale(os.Stderr)
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage:
  blobpurge scan [-min 512KiB] <git-dir>
  blobpurge strip -blob <id> [-blob <id> ...] -to <new-git-dir> <git-dir>`

// run is the whole program, so a test can drive it and read what it says.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "scan":
		return runScan(args[1:], stdout, stderr)
	case "strip":
		return runStrip(args[1:], stdout, stderr)
	}
	fmt.Fprintln(stderr, usage)
	return 2
}

// git runs git in dir and returns its standard output. Its standard error
// goes into the returned error, which is where git says what went wrong.
func git(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
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

// refs lists the branches and tags, the refs a rewrite carries over. A mirror
// also holds refs/pull/*, which nobody but GitHub can write, so they are out.
func refs(dir string) ([]string, error) {
	out, err := git(dir, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/tags")
	return lines(out), err
}

// treeBlobs maps every blob ID in rev's tree to its paths.
func treeBlobs(dir, rev string) (map[string][]string, error) {
	out, err := git(dir, "ls-tree", "-r", "-z", rev)
	if err != nil {
		return nil, err
	}
	m := map[string][]string{}
	for _, e := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		// <mode> SP <type> SP <id> TAB <path>
		meta, path, ok := strings.Cut(e, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			continue
		}
		if f[1] == "blob" {
			m[f[2]] = append(m[f[2]], path)
		}
	}
	return m, nil
}

type blob struct {
	id    string
	size  int64
	paths []string
	tags  []string
}

// candidates returns the blobs at least min bytes that a branch or tag reaches
// and HEAD's tree does not hold, largest first, and how many blobs it read: a
// scan that read none has not looked, and says so instead of reporting zero.
func candidates(dir string, min int64) ([]blob, int, error) {
	head, err := treeBlobs(dir, "HEAD")
	if err != nil {
		return nil, 0, err
	}
	list := exec.Command("git", "-C", dir, "rev-list", "--objects", "--branches", "--tags")
	check := exec.Command("git", "-C", dir, "cat-file", "--batch-check=%(objecttype) %(objectname) %(objectsize) %(rest)")
	pipe, err := list.StdoutPipe()
	if err != nil {
		return nil, 0, err
	}
	check.Stdin = pipe
	var listErr, checkErr bytes.Buffer // apart: two processes writing one buffer is a race
	list.Stderr, check.Stderr = &listErr, &checkErr
	if err := list.Start(); err != nil {
		return nil, 0, err
	}
	out, cerr := check.Output()
	if werr := list.Wait(); werr != nil || cerr != nil {
		return nil, 0, fmt.Errorf("reading %s's objects: %v %v: %s", dir, werr, cerr, strings.TrimSpace(listErr.String()+" "+checkErr.String()))
	}
	byID := map[string]*blob{}
	read := 0
	for _, l := range lines(out) {
		f := strings.SplitN(l, " ", 4)
		if len(f) < 3 || f[0] != "blob" {
			continue
		}
		read++
		size, _ := strconv.ParseInt(f[2], 10, 64)
		if size < min || head[f[1]] != nil {
			continue
		}
		b := byID[f[1]]
		if b == nil {
			b = &blob{id: f[1], size: size}
			byID[f[1]] = b
		}
		if len(f) == 4 && f[3] != "" {
			b.paths = appendNew(b.paths, f[3])
		}
	}
	if len(byID) > 0 {
		tags, err := git(dir, "for-each-ref", "--format=%(refname:short)", "--sort=v:refname", "refs/tags")
		if err != nil {
			return nil, read, err
		}
		for _, t := range lines(tags) {
			tb, err := treeBlobs(dir, t+"^{tree}")
			if err != nil {
				return nil, read, err
			}
			for id, b := range byID {
				if tb[id] != nil {
					b.tags = append(b.tags, t)
				}
			}
		}
	}
	var bs []blob
	for _, b := range byID {
		bs = append(bs, *b)
	}
	sort.Slice(bs, func(i, j int) bool {
		if bs[i].size != bs[j].size {
			return bs[i].size > bs[j].size
		}
		return bs[i].id < bs[j].id
	})
	return bs, read, nil
}

func appendNew(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func runScan(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("blobpurge scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	minFlag := fs.String("min", "512KiB", "report blobs of at least this size")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	min, err := parseSize(*minFlag)
	if err != nil {
		fmt.Fprintln(stderr, "blobpurge:", err)
		return 2
	}
	dir := fs.Arg(0)
	bs, read, err := candidates(dir, min)
	if err != nil {
		fmt.Fprintln(stderr, "blobpurge:", err)
		return 1
	}
	if read == 0 {
		fmt.Fprintf(stderr, "blobpurge: read no blobs in %s: nothing was scanned\n", dir)
		return 1
	}
	var total int64
	for _, b := range bs {
		total += b.size
		fmt.Fprintf(stdout, "%s %10d  %s\n", b.id, b.size, strings.Join(b.paths, ", "))
		switch len(b.tags) {
		case 0:
			fmt.Fprintln(stdout, "    in no tag's tree")
		case 1:
			fmt.Fprintf(stdout, "    in the tree of 1 tag: %s\n", b.tags[0])
		default:
			fmt.Fprintf(stdout, "    in the trees of %d tags: %s .. %s\n", len(b.tags), b.tags[0], b.tags[len(b.tags)-1])
		}
	}
	fmt.Fprintf(stdout, "%d blobs read, %d of at least %d bytes gone from HEAD: %d bytes\n", read, len(bs), min, total)
	return 0
}

// idList is a repeatable -blob flag.
type idList []string

func (l *idList) String() string { return strings.Join(*l, ",") }
func (l *idList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

func runStrip(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("blobpurge strip", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var ids idList
	fs.Var(&ids, "blob", "a blob ID to strip (repeatable)")
	to := fs.String("to", "", "the new repository to write; must not exist")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 || len(ids) == 0 || *to == "" {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	src := fs.Arg(0)
	if _, err := os.Stat(*to); !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stderr, "blobpurge: %s already exists: strip writes a NEW repository\n", *to)
		return 2
	}
	strip, err := resolve(src, ids)
	if err != nil {
		fmt.Fprintln(stderr, "blobpurge:", err)
		return 2
	}
	signed, err := signedCommits(src)
	if err != nil {
		fmt.Fprintln(stderr, "blobpurge:", err)
		return 1
	}
	stats, err := rewrite(src, *to, strip)
	if err != nil {
		fmt.Fprintln(stderr, "blobpurge:", err)
		return 1
	}
	rep, err := verify(src, *to, strip)
	if err != nil {
		fmt.Fprintln(stderr, "blobpurge: THE REWRITE IS WRONG, do not push it:", err)
		return 1
	}
	if _, err := git(*to, "gc", "--quiet", "--prune=now"); err != nil {
		fmt.Fprintln(stderr, "blobpurge:", err)
		return 1
	}
	before, _ := packSize(src)
	after, _ := packSize(*to)
	fmt.Fprintf(stdout, "stripped %d blobs (%d file entries removed); %d commit signatures dropped, as any rewrite must\n",
		stats.blobs, stats.entries, signed)
	fmt.Fprintf(stdout, "checked %d refs, %d commits: metadata identical\n", len(rep.refs), rep.commits)
	fmt.Fprintf(stdout, "%d trees unchanged; %d changed, each by exactly the stripped blobs:\n", rep.unchanged, len(rep.changed))
	for _, c := range rep.changed {
		fmt.Fprintf(stdout, "    %s\n", c)
	}
	fmt.Fprintf(stdout, "pack: %s -> %s\n", before, after)
	fmt.Fprintf(stdout, "nothing was pushed. %s holds the result.\n", *to)
	return 0
}

// resolve checks every requested ID names a blob in src that HEAD does not
// hold, and expands abbreviations.
func resolve(src string, ids []string) (map[string]bool, error) {
	head, err := treeBlobs(src, "HEAD")
	if err != nil {
		return nil, err
	}
	strip := map[string]bool{}
	for _, id := range ids {
		out, err := git(src, "rev-parse", "--verify", "--quiet", id+"^{blob}")
		if err != nil {
			return nil, fmt.Errorf("%s is not a blob in %s", id, src)
		}
		full := strings.TrimSpace(string(out))
		if p := head[full]; p != nil {
			return nil, fmt.Errorf("%s is still in HEAD's tree (%s): delete it in a commit first, then strip what history kept", id, strings.Join(p, ", "))
		}
		strip[full] = true
	}
	return strip, nil
}

// packSize is what the object store holds, loose and packed together: a
// mirror cloned from a local path keeps its objects loose, and its packs
// alone read as zero.
func packSize(dir string) (string, error) {
	out, err := git(dir, "count-objects", "-v")
	if err != nil {
		return "", err
	}
	var kib int64
	for _, l := range lines(out) {
		k, v, _ := strings.Cut(l, ": ")
		if k == "size" || k == "size-pack" {
			n, _ := strconv.ParseInt(v, 10, 64)
			kib += n
		}
	}
	if kib >= 1024 {
		return fmt.Sprintf("%.1f MiB", float64(kib)/1024), nil
	}
	return fmt.Sprintf("%d KiB", kib), nil
}

type stripStats struct{ blobs, entries int }

// signedCommits counts the commits a branch or tag reaches that carry a
// signature header: what the rewrite will drop, said before it is gone.
func signedCommits(dir string) (int, error) {
	list := exec.Command("git", "-C", dir, "rev-list", "--branches", "--tags")
	cat := exec.Command("git", "-C", dir, "cat-file", "--batch")
	pipe, err := list.StdoutPipe()
	if err != nil {
		return 0, err
	}
	cat.Stdin = pipe
	if err := list.Start(); err != nil {
		return 0, err
	}
	out, cerr := cat.StdoutPipe()
	if cerr != nil {
		return 0, cerr
	}
	if err := cat.Start(); err != nil {
		return 0, err
	}
	r := bufio.NewReader(out)
	n := 0
	for {
		hdr, err := r.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
		f := strings.Fields(hdr) // <id> <type> <size>
		if len(f) != 3 {
			return 0, fmt.Errorf("cat-file: %q", hdr)
		}
		size, _ := strconv.Atoi(f[2])
		body := make([]byte, size+1) // and its trailing LF
		if _, err := io.ReadFull(r, body); err != nil {
			return 0, err
		}
		header, _, _ := bytes.Cut(body, []byte("\n\n"))
		if bytes.Contains(header, []byte("\ngpgsig ")) || bytes.Contains(header, []byte("\ngpgsig-sha256 ")) {
			n++
		}
	}
	if err := list.Wait(); err != nil {
		return 0, err
	}
	return n, cat.Wait()
}

// rewrite pipes src's branches and tags through the filter into a new bare
// repository at dst.
func rewrite(src, dst string, strip map[string]bool) (stripStats, error) {
	if out, err := exec.Command("git", "init", "--quiet", "--bare", dst).CombinedOutput(); err != nil {
		return stripStats{}, fmt.Errorf("git init %s: %v: %s", dst, err, out)
	}
	exp := exec.Command("git", "-C", src, "fast-export",
		"--branches", "--tags",
		"--show-original-ids", "--reencode=yes", "--use-done-feature",
		"--signed-tags=strip", "--signed-commits=strip",
		"--tag-of-filtered-object=rewrite")
	imp := exec.Command("git", "-C", dst, "fast-import", "--quiet")
	var expErr, impErr bytes.Buffer
	exp.Stderr, imp.Stderr = &expErr, &impErr
	r, err := exp.StdoutPipe()
	if err != nil {
		return stripStats{}, err
	}
	w, err := imp.StdinPipe()
	if err != nil {
		return stripStats{}, err
	}
	if err := exp.Start(); err != nil {
		return stripStats{}, err
	}
	if err := imp.Start(); err != nil {
		exp.Process.Kill()
		return stripStats{}, err
	}
	f := newFilter(strip)
	ferr := f.run(bufio.NewReaderSize(r, 1<<20), w)
	w.Close()
	if ferr != nil {
		exp.Process.Kill()
	}
	eerr, ierr := exp.Wait(), imp.Wait()
	switch {
	case ferr != nil:
		return f.stats, ferr
	case eerr != nil:
		return f.stats, fmt.Errorf("fast-export: %v: %s", eerr, strings.TrimSpace(expErr.String()))
	case ierr != nil:
		return f.stats, fmt.Errorf("fast-import: %v: %s", ierr, strings.TrimSpace(impErr.String()))
	}
	for id := range strip {
		if !f.found[id] {
			return f.stats, fmt.Errorf("%s never appeared in the export: no branch or tag reaches it, so stripping it changes nothing here", id)
		}
	}
	// The new repository's HEAD names the same branch as the old one's.
	if out, err := git(src, "symbolic-ref", "HEAD"); err == nil {
		if _, err := git(dst, "symbolic-ref", "HEAD", strings.TrimSpace(string(out))); err != nil {
			return f.stats, err
		}
	}
	return f.stats, nil
}

// filter copies a fast-export stream, leaving out the stripped blobs and every
// file entry that points at one. Everything after a `data <n>` line is n raw
// bytes and is copied without being looked at: a commit message may well hold
// a line that reads like a command.
type filter struct {
	strip   map[string]bool
	dropped map[string]bool // marks of stripped blobs
	found   map[string]bool
	stats   stripStats
}

func newFilter(strip map[string]bool) *filter {
	return &filter{strip: strip, dropped: map[string]bool{}, found: map[string]bool{}}
}

func dataLen(line string) (int64, bool, error) {
	v, ok := strings.CutPrefix(strings.TrimSuffix(line, "\n"), "data ")
	if !ok {
		return 0, false, nil
	}
	if strings.HasPrefix(v, "<<") {
		return 0, true, fmt.Errorf("delimited data is not supported: %q", line)
	}
	n, err := strconv.ParseInt(v, 10, 64)
	return n, true, err
}

func (f *filter) run(r *bufio.Reader, out io.Writer) error {
	w := bufio.NewWriterSize(out, 1<<20)
	for {
		line, err := r.ReadString('\n')
		if err == io.EOF && line == "" {
			return w.Flush()
		}
		if err != nil && err != io.EOF {
			return err
		}
		if line == "blob\n" {
			if err := f.blob(r, w); err != nil {
				return err
			}
			continue
		}
		if n, ok, err := dataLen(line); ok {
			if err != nil {
				return err
			}
			if _, err := w.WriteString(line); err != nil {
				return err
			}
			if _, err := io.CopyN(w, r, n); err != nil {
				return fmt.Errorf("data %d: %v", n, err)
			}
			continue
		}
		if strings.HasPrefix(line, "M ") {
			// M <mode> <dataref> <path>
			if p := strings.SplitN(line, " ", 4); len(p) == 4 && (f.dropped[p[2]] || f.strip[p[2]]) {
				f.stats.entries++
				continue
			}
		}
		if ref, ok := strings.CutPrefix(strings.TrimSuffix(line, "\n"), "from "); ok && f.dropped[ref] {
			return fmt.Errorf("a ref or tag points straight at a stripped blob (%s)", ref)
		}
		if _, err := w.WriteString(line); err != nil {
			return err
		}
	}
}

// blob reads one blob command after its `blob` line and copies it, unless its
// original ID is one to strip.
func (f *filter) blob(r *bufio.Reader, w *bufio.Writer) error {
	head := []string{"blob\n"}
	var mark, oid string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return fmt.Errorf("a blob command ended early: %v", err)
		}
		n, ok, err := dataLen(line)
		if err != nil {
			return err
		}
		if !ok {
			head = append(head, line)
			if v, ok := strings.CutPrefix(strings.TrimSuffix(line, "\n"), "mark "); ok {
				mark = v
			}
			if v, ok := strings.CutPrefix(strings.TrimSuffix(line, "\n"), "original-oid "); ok {
				oid = v
			}
			continue
		}
		if f.strip[oid] {
			if mark == "" {
				return fmt.Errorf("blob %s carries no mark, so the entries naming it cannot be found", oid)
			}
			if _, err := io.CopyN(io.Discard, r, n); err != nil {
				return err
			}
			if b, err := r.Peek(1); err == nil && b[0] == '\n' {
				r.ReadByte()
			}
			f.dropped[mark], f.found[oid] = true, true
			f.stats.blobs++
			return nil
		}
		for _, h := range append(head, line) {
			if _, err := w.WriteString(h); err != nil {
				return err
			}
		}
		_, err = io.CopyN(w, r, n)
		return err
	}
}

type report struct {
	refs      []string
	commits   int
	unchanged int
	changed   []string
}

// verify compares every branch and tag of src and dst. It is the reason the
// tool exists, so it trusts nothing the rewrite reported about itself.
func verify(src, dst string, strip map[string]bool) (report, error) {
	var rep report
	a, err := refs(src)
	if err != nil {
		return rep, err
	}
	b, err := refs(dst)
	if err != nil {
		return rep, err
	}
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		return rep, fmt.Errorf("refs differ:\n  before %v\n  after  %v", a, b)
	}
	if len(a) == 0 {
		return rep, errors.New("no branch or tag to compare: nothing was checked")
	}
	rep.refs = a
	for _, ref := range a {
		// Metadata, in an order fixed by the graph alone. The hashes differ by
		// design; everything a person wrote must not.
		format := "--format=%an%x00%ae%x00%ad%x00%cn%x00%ce%x00%cd%x00%P%x00%B%x01"
		la, err := git(src, "log", "--topo-order", "--date=raw", format, ref)
		if err != nil {
			return rep, err
		}
		lb, err := git(dst, "log", "--topo-order", "--date=raw", format, ref)
		if err != nil {
			return rep, err
		}
		ca, cb := bytes.Split(la, []byte{1}), bytes.Split(lb, []byte{1})
		if len(ca) != len(cb) {
			return rep, fmt.Errorf("%s: %d commits before, %d after", ref, len(ca)-1, len(cb)-1)
		}
		for i := range ca {
			if !bytes.Equal(dropParents(ca[i]), dropParents(cb[i])) {
				return rep, fmt.Errorf("%s: commit %d of %d differs in what a person wrote:\n  before %q\n  after  %q", ref, i+1, len(ca)-1, ca[i], cb[i])
			}
			if parents(ca[i]) != parents(cb[i]) {
				return rep, fmt.Errorf("%s: commit %d has %d parents before, %d after", ref, i+1, parents(ca[i]), parents(cb[i]))
			}
		}
		rep.commits += len(ca) - 1
		ta, err := treeBlobs(src, ref+"^{tree}")
		if err != nil {
			return rep, err
		}
		tb, err := treeBlobs(dst, ref+"^{tree}")
		if err != nil {
			return rep, err
		}
		var removed []string
		for id, paths := range ta {
			if strip[id] {
				removed = append(removed, paths...)
				delete(ta, id)
			}
		}
		if d := diffTrees(ta, tb); d != "" {
			return rep, fmt.Errorf("%s: the tree is not the old one minus the stripped blobs: %s", ref, d)
		}
		ida, _ := git(src, "rev-parse", ref+"^{tree}")
		idb, _ := git(dst, "rev-parse", ref+"^{tree}")
		if len(removed) == 0 {
			if !bytes.Equal(ida, idb) {
				return rep, fmt.Errorf("%s: no stripped blob in its tree, yet the tree ID moved", ref)
			}
			rep.unchanged++
		} else {
			sort.Strings(removed)
			rep.changed = append(rep.changed, fmt.Sprintf("%s: -%s", strings.TrimPrefix(strings.TrimPrefix(ref, "refs/tags/"), "refs/heads/"), strings.Join(removed, ", -")))
		}
	}
	for id := range strip {
		if exec.Command("git", "-C", dst, "cat-file", "-e", id).Run() == nil {
			return rep, fmt.Errorf("%s is still in the new repository", id)
		}
	}
	return rep, nil
}

// The parent field holds commit IDs, which change by design; only how many
// there are is compared.
func dropParents(c []byte) []byte {
	f := bytes.SplitN(c, []byte{0}, 8)
	if len(f) == 8 {
		f[6] = nil
	}
	return bytes.Join(f, []byte{0})
}

func parents(c []byte) int {
	f := bytes.SplitN(c, []byte{0}, 8)
	if len(f) < 8 {
		return -1
	}
	return len(bytes.Fields(f[6]))
}

func diffTrees(a, b map[string][]string) string {
	var d []string
	for id, p := range a {
		if strings.Join(p, "\x00") != strings.Join(b[id], "\x00") {
			d = append(d, fmt.Sprintf("%s at %v before, %v after", id, p, b[id]))
		}
	}
	for id, p := range b {
		if a[id] == nil {
			d = append(d, fmt.Sprintf("%s at %v appeared", id, p))
		}
	}
	sort.Strings(d)
	return strings.Join(d, "; ")
}
