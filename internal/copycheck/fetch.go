package copycheck

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Limits bound what an archive may make us do. A reference is untrusted input:
// a URL in a manifest can point at anything, and an archive's declared sizes
// are numbers the archive chose.
type Limits struct {
	ArchiveBytes int64 // the download, as sent
	StreamBytes  int64 // the archive after decompression, everything in it
	TotalBytes   int64 // what is kept
	FileBytes    int64 // one kept file
	Files        int   // kept files
	Entries      int   // entries read, kept or not
}

// DefaultLimits are above the largest reference we meant to read (a pinned
// frameworks/base is about 1 GiB unpacked) and far below a bomb.
func DefaultLimits() Limits {
	return Limits{ArchiveBytes: 2 << 30, StreamBytes: 8 << 30, TotalBytes: 512 << 20, FileBytes: 32 << 20, Files: 50000, Entries: 2_000_000}
}

// Fetcher downloads, extracts and verifies references into a durable cache.
type Fetcher struct {
	Client   *http.Client
	CacheDir string
	Limits   Limits
}

// sourceExt are the extensions that are kept: what the lexer reads.
var sourceExt = map[string]bool{
	".c": true, ".h": true, ".cc": true, ".cpp": true, ".cxx": true, ".hpp": true,
	".inc": true, ".go": true, ".s": true,
}

func isSource(name string) bool { return sourceExt[strings.ToLower(path.Ext(name))] }

// DefaultCacheDir is under os.UserCacheDir and refuses to be inside a git
// working tree: a downloaded reference committed by `git add -A` is a copyleft
// source tree in our history.
func DefaultCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return CheckCacheDir(filepath.Join(base, "copycheck"))
}

// CheckCacheDir refuses a directory under a `.git` parent.
func CheckCacheDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for d := abs; ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return "", fmt.Errorf("cache directory %s is inside the git working tree %s: references must never sit in a repository", abs, d)
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	return abs, nil
}

// TreeHash is the hash a manifest records: over the sorted relative paths and
// the SHA-256 of each kept file. It does not depend on how the archive was
// compressed, which a host may change without changing one byte of source (a
// GitHub archive's own hash is not promised stable).
func TreeHash(root string) (string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink", p)
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", p)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			return "", err
		}
		s := sha256.Sum256(b)
		fmt.Fprintf(h, "%s\x00%s\n", f, hex.EncodeToString(s[:]))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Fetch makes the reference available under the cache and returns its
// directory. When ref.Tree is empty the hash is not checked and is returned
// (that is `copycheck pin`); otherwise a mismatch is an error, never a
// fallback.
func (f *Fetcher) Fetch(ref Reference) (dir, tree string, err error) {
	if f.CacheDir == "" {
		return "", "", errors.New("no cache directory")
	}
	slot := filepath.Join(f.CacheDir, safeName(ref.Name+"-"+ref.Pin))
	if ref.Tree != "" {
		// a cached copy is verified every time: the cache is just another
		// place for bytes to differ
		if _, err := os.Stat(slot); err == nil {
			if got, err := TreeHash(slot); err == nil && got == ref.Tree {
				return slot, got, nil
			}
			if err := os.RemoveAll(slot); err != nil {
				return "", "", err
			}
		}
	}
	if err := os.MkdirAll(f.CacheDir, 0o755); err != nil {
		return "", "", err
	}
	stage, err := os.MkdirTemp(f.CacheDir, ".partial-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(stage)
	arch := filepath.Join(stage, "archive")
	if err := f.download(ref.URL, arch); err != nil {
		return "", "", fmt.Errorf("%s: %w", ref.Name, err)
	}
	tree = filepath.Join(stage, "tree")
	if err := os.Mkdir(tree, 0o755); err != nil {
		return "", "", err
	}
	strip := 1 // GitHub wraps everything in <repo>-<rev>/
	if ref.Strip != nil {
		strip = *ref.Strip
	}
	if err := Extract(arch, tree, ref.Paths, strip, f.Limits); err != nil {
		return "", "", fmt.Errorf("%s: %w", ref.Name, err)
	}
	got, err := TreeHash(tree)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", ref.Name, err)
	}
	if ref.Tree != "" && got != ref.Tree {
		return "", "", fmt.Errorf("%s: tree hash mismatch: manifest says %s, the archive at %s gives %s (refusing: a reference that changed is not the one that was read)", ref.Name, ref.Tree, ref.URL, got)
	}
	if err := os.RemoveAll(slot); err != nil {
		return "", "", err
	}
	if err := os.Rename(tree, slot); err != nil {
		return "", "", err
	}
	return slot, got, nil
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
}

func (f *Fetcher) download(url, to string) error {
	cl := f.Client
	if cl == nil {
		cl = http.DefaultClient
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	if resp.ContentLength > f.Limits.ArchiveBytes {
		return fmt.Errorf("GET %s: declares %d bytes, over the %d limit", url, resp.ContentLength, f.Limits.ArchiveBytes)
	}
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(resp.Body, f.Limits.ArchiveBytes+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > f.Limits.ArchiveBytes {
		return fmt.Errorf("GET %s: more than %d bytes", url, f.Limits.ArchiveBytes)
	}
	if n == 0 {
		return fmt.Errorf("GET %s: empty body", url)
	}
	return nil
}

// cleanEntry validates an archive member name and strips the first path
// component (GitHub wraps everything in `<repo>-<rev>/`). Traversal anywhere in
// the name is REFUSED, not stripped: a name that no longer says where it came
// from is how a file lands somewhere nobody asked for.
func cleanEntry(name string, strip int) (rel string, err error) {
	if strings.ContainsRune(name, 0) || strings.Contains(name, "\\") {
		return "", fmt.Errorf("entry %q has a NUL or a backslash", name)
	}
	if strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("entry %q is absolute", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return "", fmt.Errorf("entry %q escapes the archive", name)
		}
	}
	name = strings.TrimPrefix(path.Clean(name), "./")
	if name == "." {
		return "", nil
	}
	for ; strip > 0; strip-- {
		i := strings.IndexByte(name, '/')
		if i < 0 {
			return "", nil // the top directory itself, or a loose file at the root: nothing to keep
		}
		name = name[i+1:]
	}
	return name, nil
}

func under(rel string, paths []string) bool {
	if len(paths) == 0 {
		return true
	}
	for _, p := range paths {
		p = strings.TrimSuffix(p, "/")
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}

type extractor struct {
	strip int
	dst   string
	paths []string
	lim   Limits
	files int
	total int64
	seen  map[string]bool
}

func (x *extractor) keep(rel string, size int64, r io.Reader) error {
	if x.files >= x.lim.Files {
		return fmt.Errorf("more than %d kept files", x.lim.Files)
	}
	if size > x.lim.FileBytes {
		return fmt.Errorf("%s declares %d bytes, over the %d per-file limit", rel, size, x.lim.FileBytes)
	}
	if x.total+size > x.lim.TotalBytes {
		return fmt.Errorf("kept files exceed %d bytes", x.lim.TotalBytes)
	}
	if x.seen[rel] {
		return fmt.Errorf("%s appears twice in the archive", rel)
	}
	x.seen[rel] = true
	target := filepath.Join(x.dst, filepath.FromSlash(rel))
	if r2, err := filepath.Rel(x.dst, target); err != nil || strings.HasPrefix(r2, "..") {
		return fmt.Errorf("%s escapes the destination", rel)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	// the declared size is not trusted: read one byte past it
	n, err := io.Copy(out, io.LimitReader(r, x.lim.FileBytes+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > x.lim.FileBytes {
		return fmt.Errorf("%s is longer than the %d per-file limit", rel, x.lim.FileBytes)
	}
	x.files++
	x.total += n
	return nil
}

// Extract unpacks the source files of a tar.gz or zip under paths into dst.
// Symlinks, hard links and devices inside the selected paths are refused;
// every name is checked, selected or not.
func Extract(archive, dst string, paths []string, strip int, lim Limits) error {
	fh, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer fh.Close()
	var magic [4]byte
	if _, err := io.ReadFull(fh, magic[:]); err != nil {
		return fmt.Errorf("archive too short to be one: %w", err)
	}
	if _, err := fh.Seek(0, io.SeekStart); err != nil {
		return err
	}
	x := &extractor{strip: strip, dst: dst, paths: paths, lim: lim, seen: map[string]bool{}}
	switch {
	case magic[0] == 0x1f && magic[1] == 0x8b:
		return x.tarGz(fh)
	case magic[0] == 'P' && magic[1] == 'K':
		st, err := fh.Stat()
		if err != nil {
			return err
		}
		return x.zip(fh, st.Size())
	}
	return errors.New("archive is neither gzip nor zip")
}

type capReader struct {
	r io.Reader
	n int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.n <= 0 {
		return 0, errors.New("decompressed stream exceeds the limit (bomb?)")
	}
	if int64(len(p)) > c.n {
		p = p[:c.n]
	}
	n, err := c.r.Read(p)
	c.n -= int64(n)
	return n, err
}

func (x *extractor) tarGz(r io.Reader) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(&capReader{r: gz, n: x.lim.StreamBytes})
	entries := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if entries++; entries > x.lim.Entries {
			return fmt.Errorf("more than %d entries", x.lim.Entries)
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		rel, err := cleanEntry(h.Name, x.strip)
		if err != nil {
			return err
		}
		if rel == "" || !under(rel, x.paths) {
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
		case tar.TypeReg:
			if isSource(rel) {
				if err := x.keep(rel, h.Size, tr); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("entry %q is a link or special file (type %q), which a reference must not contain", h.Name, h.Typeflag)
		}
	}
	if x.files == 0 {
		return errors.New("no source files under the declared paths (wrong paths, or an empty archive)")
	}
	return nil
}

func (x *extractor) zip(ra io.ReaderAt, size int64) error {
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return err
	}
	if len(zr.File) > x.lim.Entries {
		return fmt.Errorf("more than %d entries", x.lim.Entries)
	}
	for _, zf := range zr.File {
		rel, err := cleanEntry(zf.Name, x.strip)
		if err != nil {
			return err
		}
		if rel == "" || !under(rel, x.paths) {
			continue
		}
		mode := zf.Mode()
		if mode&fs.ModeSymlink != 0 || (!mode.IsRegular() && !mode.IsDir()) {
			return fmt.Errorf("entry %q is a link or special file", zf.Name)
		}
		if mode.IsDir() || !isSource(rel) {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		err = x.keep(rel, int64(zf.UncompressedSize64), rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	if x.files == 0 {
		return errors.New("no source files under the declared paths (wrong paths, or an empty archive)")
	}
	return nil
}
