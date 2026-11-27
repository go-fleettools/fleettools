package main

import (
	"fmt"
	"io/fs"
	"os"
	"time"
)

// maxErrsKept bounds the unreadable-path list that is stored; the COUNT is
// always exact, so a bound on the list never turns into a smaller number.
const maxErrsKept = 1000

// row is one reported entry of a scan.
type row struct {
	path   string
	bytes  int64     // allocated bytes (st_blocks*512), the whole subtree
	newest time.Time // newest mtime anywhere inside, the entry itself included
	isDir  bool
}

// scan walks a tree once. It never follows a symlink (a link is counted as
// the link, by Lstat), never leaves the filesystem of the root, counts a file
// with several hard links once, and RECORDS every path it could not read:
// a scan that could not see a directory must not report it as empty.
type scan struct {
	rootDev uint64
	seen    map[inoKey]struct{}

	files, dirs int64
	unreadable  int64    // exact count
	errs        []string // first maxErrsKept of them
	otherFS     []string // directories not entered because they are another filesystem
}

type inoKey struct{ dev, ino uint64 }

func newScan() *scan { return &scan{seen: map[inoKey]struct{}{}} }

func (s *scan) fail(path string, err error) {
	s.unreadable++
	if len(s.errs) < maxErrsKept {
		s.errs = append(s.errs, fmt.Sprintf("%s: %v", path, err))
	}
}

// run scans root. With maxDepth >= 1 and rows != nil it appends the reported
// entries: those exactly maxDepth below root, plus every shallower entry that
// has nothing to descend into (a file, an empty or unreadable directory), so
// that every allocated byte under root belongs to exactly one row.
// It returns the root's total.
func (s *scan) run(root string, maxDepth int, rows *[]row) (int64, time.Time, error) {
	fi, err := os.Lstat(root)
	if err != nil {
		return 0, time.Time{}, err
	}
	st := statOf(fi)
	s.rootDev = st.dev
	b, n := s.walk(root, fi, 0, maxDepth, rows)
	return b, n, nil
}

func (s *scan) walk(path string, fi fs.FileInfo, depth, maxDepth int, rows *[]row) (int64, time.Time) {
	st := statOf(fi)
	newest := fi.ModTime()
	if !fi.IsDir() {
		s.files++
		var b int64
		if st.nlink > 1 {
			k := inoKey{st.dev, st.ino}
			if _, dup := s.seen[k]; !dup {
				s.seen[k] = struct{}{}
				b = st.alloc
			}
		} else {
			b = st.alloc
		}
		if rows != nil && depth >= 1 && depth <= maxDepth {
			*rows = append(*rows, row{path: path, bytes: b, newest: newest})
		}
		return b, newest
	}
	s.dirs++
	total := st.alloc
	ents, err := os.ReadDir(path)
	if err != nil {
		s.fail(path, err)
	}
	children := 0
	for _, e := range ents {
		cp := path + string(os.PathSeparator) + e.Name()
		if path == string(os.PathSeparator) {
			cp = path + e.Name()
		}
		cfi, err := os.Lstat(cp)
		if err != nil {
			s.fail(cp, err)
			continue
		}
		if cfi.IsDir() && statOf(cfi).dev != s.rootDev {
			s.otherFS = append(s.otherFS, cp)
			continue
		}
		children++
		cb, cn := s.walk(cp, cfi, depth+1, maxDepth, rows)
		total += cb
		if cn.After(newest) {
			newest = cn
		}
	}
	if rows != nil && depth >= 1 && (depth == maxDepth || children == 0) {
		*rows = append(*rows, row{path: path, bytes: total, newest: newest, isDir: true})
	}
	return total, newest
}

// human prints a byte count in IEC units.
func human(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// age prints how long ago t was, coarsely.
func age(now, t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	default:
		return fmt.Sprintf("%dd%02dh", int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour))
	}
}
