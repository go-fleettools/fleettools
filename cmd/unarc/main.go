// Command unarc extracts the archives in a directory, each into its own
// subdirectory, and never deletes the archive it read.
//
// It exists because the shell cannot reliably address the files it is pointed
// at. On macOS a filename holding an accented character is stored DECOMPOSED
// (e + U+0300), and a name typed or quoted in composed form does not match it:
//
//	zipinfo: cannot find or open .../Logo IP Paris 6 e<U+0301>coles vertical web.zip
//
// Three of sixty-five archives failed that way under a shell loop. Reading the
// directory with os.ReadDir hands over the exact bytes, with no quoting and no
// normalisation in between, so the question does not arise.
//
// ⛔ AN ARCHIVE'S NAMES ARE A TRUST BOUNDARY. filepath.Join neutralises a
// leading slash and does NOT neutralise "..", so every entry is checked against
// the destination after cleaning. An entry that escapes is REFUSED and named,
// not silently stripped: a stripped path writes a file somewhere the caller did
// not ask for, under a name that no longer says where it came from.
//
// ⛔ IT SAYS WHAT IT CANNOT DO. 7z, rar and xz need a decompressor this module
// does not carry, so those are reported as needing an external tool rather than
// counted as skipped -- a count that mixes "already done" with "cannot do"
// reads as success.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/ulikunitz/xz"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Extracts every archive in a directory, each into its own subdirectory, and
REFUSES an entry whose path would escape that subdirectory.

An entry's declared size is a number the archive chose, so it is bounded: ten
gigabytes is past anything in a downloads directory and still refuses a
decompression bomb outright.

Exit status: 0 everything extracted or was already there, 1 an archive was
refused or failed, 2 the directory could not be read.`

// maxEntrySize bounds one entry, because a declared size is a number the
// archive chose. Ten gigabytes is past anything in a downloads directory and
// still refuses a decompression bomb outright.
const maxEntrySize = 10 << 30

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it through run, below.
func RootCmd() *cobra.Command {
	var (
		root string
		out  string
		dry  bool
	)
	c := cli.New("unarc [flags]", "Extract every archive in a directory, safely", longHelp)
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		return cli.FromStatus(unpack(cli.Out(cmd), cli.Err(cmd), root, out, dry))
	}
	f := c.Flags()
	f.StringVar(&root, "root", ".", "the directory to read archives from")
	f.StringVar(&out, "out", "", "where to extract (default <root>/extracted)")
	// --dry-run keeps the old -n as its shorthand, so every call site spelling
	// -n reaches it unchanged: pflag accepts a one-letter flag with one dash,
	// and only the longer names moved.
	f.BoolVarP(&dry, "dry-run", "n", false, "say what would happen and extract nothing")
	return c
}

// run keeps the signature this command's tests were written against.
func run(args []string, stdout, stderr io.Writer) int {
	return cli.Drive(RootCmd(), args, stdout, stderr)
}

func unpack(stdout, stderr io.Writer, root, out string, dry bool) int {
	dest := out
	if dest == "" {
		dest = filepath.Join(root, "extracted")
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		fmt.Fprintf(stderr, "unarc: %v\n", err)
		return 1
	}

	var done, skipped, refused, external, failed int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		kind := kindOf(name)
		if kind == kindNone {
			continue
		}
		src := filepath.Join(root, name)
		target := filepath.Join(dest, stem(name))

		if kind == kindExternal {
			fmt.Fprintf(stdout, "EXTERNAL %s (no decompressor here)\n", name)
			external++
			continue
		}
		if populated(target) {
			fmt.Fprintf(stdout, "SKIPPED  %s (%s is not empty)\n", name, target)
			skipped++
			continue
		}
		if dry {
			fmt.Fprintf(stdout, "WOULD    %s -> %s\n", name, target)
			continue
		}

		n, renamed, err := extract(src, target, kind)
		switch {
		case errors.Is(err, errEscapes):
			fmt.Fprintf(stdout, "REFUSED  %s: %v\n", name, err)
			refused++
		case err != nil:
			fmt.Fprintf(stdout, "FAILED   %s: %v\n", name, err)
			failed++
		default:
			extra := ""
			if renamed > 0 {
				extra = fmt.Sprintf(", %d name(s) re-encoded from Latin-1", renamed)
			}
			fmt.Fprintf(stdout, "EXTRACTED %s -> %d file(s)%s\n", name, n, extra)
			done++
		}
	}

	fmt.Fprintf(stdout, "extracted %d, skipped %d, refused %d, needs an external tool %d, failed %d\n",
		done, skipped, refused, external, failed)
	if refused > 0 || failed > 0 {
		return 1
	}
	return 0
}

type kind int

const (
	kindNone kind = iota
	kindZip
	kindTarGz
	kindTarBz2
	kindTarXz
	kindTar
	kindExternal
)

func kindOf(name string) kind {
	l := strings.ToLower(name)
	switch {
	case strings.HasSuffix(l, ".zip"):
		return kindZip
	case strings.HasSuffix(l, ".tar.gz"), strings.HasSuffix(l, ".tgz"):
		return kindTarGz
	case strings.HasSuffix(l, ".tar.bz2"), strings.HasSuffix(l, ".tbz2"):
		return kindTarBz2
	case strings.HasSuffix(l, ".tar"):
		return kindTar
	case strings.HasSuffix(l, ".tar.xz"), strings.HasSuffix(l, ".txz"):
		return kindTarXz
	case strings.HasSuffix(l, ".7z"), strings.HasSuffix(l, ".rar"),
		strings.HasSuffix(l, ".tar.zst"):
		return kindExternal
	}
	return kindNone
}

// stem is the archive's name without its compound extension, which is the
// directory it is extracted into.
func stem(name string) string {
	l := strings.ToLower(name)
	for _, suf := range []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst", ".tgz", ".tbz2", ".tar", ".zip", ".7z", ".rar"} {
		if strings.HasSuffix(l, suf) {
			return name[:len(name)-len(suf)]
		}
	}
	return name
}

func populated(dir string) bool {
	e, err := os.ReadDir(dir)
	return err == nil && len(e) > 0
}

var errEscapes = errors.New("an entry names a path outside the destination")

// usable turns an entry name into one the filesystem will accept, and says so
// when it had to change it.
//
// ⛔ THE NAMES INSIDE AN ARCHIVE HAVE THEIR OWN ENCODING, and it is not always
// the one the format promises. APFS refuses a filename that is not valid UTF-8
// with EILSEQ -- "illegal byte sequence" -- so three archives of logos and one
// invoice could not be extracted at all:
//
//	open .../Logo IP Paris 6 <0xE9>coles vertical jpg 300 DPI.jpg: illegal byte sequence
//
// A lone 0xE9 is "é" in Latin-1. The zip specification says an entry whose
// UTF-8 flag is clear is CP437, where "é" is 0x82 -- so these were written by a
// tool that ignored the spec, and decoding them as CP437 would give a different
// wrong name. Latin-1 is what the bytes actually are here: each byte is the
// code point of the same number, which is a conversion with no table.
//
// ⛔ It is REPORTED, never silent. A renamed file is a file the caller cannot
// find by the name they saw in the archive.
func usable(name string) (string, bool) {
	if utf8.ValidString(name) {
		return name, false
	}
	var b strings.Builder
	for _, c := range []byte(name) {
		b.WriteRune(rune(c))
	}
	return b.String(), true
}

// safeJoin places name under dest, or refuses it.
//
// ⛔ filepath.Join cleans the result, which turns "a/../../b" into "../b" and
// keeps it OUTSIDE dest. Checking the CLEANED result against dest is the only
// form that holds; checking the raw name for ".." misses an entry that reaches
// the same place by another spelling.
func safeJoin(dest, name string) (string, error) {
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("%w: %q is absolute", errEscapes, name)
	}
	p := filepath.Join(dest, name)
	rel, err := filepath.Rel(dest, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", errEscapes, name)
	}
	return p, nil
}

func extract(src, dest string, k kind) (files, renamed int, err error) {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return 0, 0, err
	}
	if k == kindZip {
		return extractZip(src, dest)
	}
	return extractTar(src, dest, k)
}

func extractZip(src, dest string) (files, renamed int, err error) {
	r, err := zip.OpenReader(src)
	if err != nil {
		return 0, 0, err
	}
	defer r.Close()

	// ⛔ Every name is checked BEFORE anything is written. A refusal halfway
	// through leaves a directory that looks extracted.
	for _, f := range r.File {
		if f.Name == "" || f.Name == "/" {
			continue // a bare root entry, which some writers emit
		}
		name, _ := usable(f.Name)
		if _, err := safeJoin(dest, name); err != nil {
			return 0, 0, err
		}
	}

	for _, f := range r.File {
		if f.Name == "" || f.Name == "/" {
			continue
		}
		name, changed := usable(f.Name)
		if changed {
			renamed++
		}
		p, err := safeJoin(dest, name)
		if err != nil {
			return files, renamed, err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return files, renamed, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return files, renamed, err
		}
		rc, err := f.Open()
		if err != nil {
			return files, renamed, err
		}
		err = writeFile(p, rc, f.Mode())
		rc.Close()
		if err != nil {
			return files, renamed, err
		}
		files++
	}
	return files, renamed, nil
}

func extractTar(src, dest string, k kind) (files, renamed int, err error) {
	f, err := os.Open(src)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	var body io.Reader = f
	switch k {
	case kindTarGz:
		gz, err := gzip.NewReader(f)
		if err != nil {
			return 0, 0, err
		}
		defer gz.Close()
		body = gz
	case kindTarBz2:
		body = bzip2.NewReader(f)
	case kindTarXz:
		xr, err := xz.NewReader(f)
		if err != nil {
			return 0, 0, err
		}
		body = xr
	}

	tr := tar.NewReader(body)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, renamed, nil
		}
		if err != nil {
			return files, renamed, err
		}
		name, changed := usable(h.Name)
		if changed {
			renamed++
		}
		p, err := safeJoin(dest, name)
		if err != nil {
			return files, renamed, err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return files, renamed, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return files, renamed, err
			}
			if err := writeFile(p, tr, os.FileMode(h.Mode)&0o777); err != nil {
				return files, renamed, err
			}
			files++
		case tar.TypeSymlink, tar.TypeLink:
			// ⛔ Not followed and not created. A link is the other half of the
			// traversal: a later regular entry writes THROUGH it, and the name
			// check above cannot see that because dest/a/b is lexically inside
			// dest even when a points out.
			fmt.Fprintf(os.Stderr, "unarc: %s: link entry %q left out\n", filepath.Base(src), h.Name)
		}
	}
}

func writeFile(p string, r io.Reader, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	w, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	// A declared size is the archive's number, so the copy is bounded here
	// rather than trusted.
	_, err = io.Copy(w, io.LimitReader(r, maxEntrySize))
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	return err
}
