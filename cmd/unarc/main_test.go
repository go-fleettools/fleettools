package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
)

// writeZip builds an archive whose entry names are given exactly, so a test can
// state a name no writer would normally produce.
func writeZip(t *testing.T, path string, names map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTarGz(t *testing.T, path string, names map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range names {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []func() error{tw.Close, gz.Close} {
		if err := c(); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runIn(t *testing.T, root string, extra ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := run(append([]string{"-root", root}, extra...), &out, &out)
	return code, out.String()
}

// TestADecomposedNameIsAddressable is the reason this tool exists.
//
// ⛔ On macOS an accented character in a filename is stored DECOMPOSED -- "e"
// followed by U+0300 -- and a name written in composed form does not match it.
// Three of sixty-five archives failed under a shell loop with
// "cannot find or open", and the name in the error looked right. os.ReadDir
// hands over the exact bytes, so the question does not arise.
func TestADecomposedNameIsAddressable(t *testing.T) {
	root := t.TempDir()
	// "e" + COMBINING ACUTE ACCENT, which is what the filesystem stores.
	decomposed := "Logo IP Paris 6 écoles vertical web.zip"
	writeZip(t, filepath.Join(root, decomposed), map[string]string{"logo.svg": "<svg/>"})

	code, out := runIn(t, root)
	if code != 0 {
		t.Fatalf("code %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "EXTRACTED") {
		t.Errorf("the decomposed name was not extracted:\n%s", out)
	}
	got := filepath.Join(root, "extracted", "Logo IP Paris 6 écoles vertical web", "logo.svg")
	if _, err := os.Stat(got); err != nil {
		t.Errorf("%s: %v", got, err)
	}
}

// TestAnEntryThatNamesAPathOutsideIsRefused.
//
// ⛔ filepath.Join CLEANS its result, so "a/../../b" becomes "../b" and stays
// outside the destination -- Join neutralises a leading slash and not this. And
// the refusal happens before anything is written, because a refusal halfway
// through leaves a directory that looks extracted.
func TestAnEntryThatNamesAPathOutsideIsRefused(t *testing.T) {
	for _, name := range []string{
		"../escaped.txt",
		"a/../../escaped.txt",
		"/absolute.txt",
		"deep/../../../escaped.txt",
	} {
		root := t.TempDir()
		writeZip(t, filepath.Join(root, "bad.zip"), map[string]string{
			"innocent.txt": "fine",
			name:           "not fine",
		})
		code, out := runIn(t, root)
		if code == 0 {
			t.Errorf("%q: code 0, want a refusal\n%s", name, out)
		}
		if !strings.Contains(out, "REFUSED") {
			t.Errorf("%q: not refused:\n%s", name, out)
		}
		// Nothing written, not even the entry that was fine.
		if _, err := os.Stat(filepath.Join(root, "extracted", "bad", "innocent.txt")); err == nil {
			t.Errorf("%q: the archive was partly extracted before the refusal", name)
		}
		// And nothing landed outside.
		if _, err := os.Stat(filepath.Join(root, "escaped.txt")); err == nil {
			t.Errorf("%q: a file was written outside the destination", name)
		}
	}
}

func TestATarGzExtracts(t *testing.T) {
	root := t.TempDir()
	writeTarGz(t, filepath.Join(root, "src-1.2.3.tar.gz"), map[string]string{
		"src/main.go": "package main",
		"src/go.mod":  "module x",
	})
	code, out := runIn(t, root)
	if code != 0 {
		t.Fatalf("code %d\n%s", code, out)
	}
	for _, p := range []string{"src/main.go", "src/go.mod"} {
		if _, err := os.Stat(filepath.Join(root, "extracted", "src-1.2.3", p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

// TestATarEntryThatEscapesIsRefused: tar names are not cleaned by the reader,
// so this is the same check on the other format.
func TestATarEntryThatEscapesIsRefused(t *testing.T) {
	root := t.TempDir()
	writeTarGz(t, filepath.Join(root, "bad.tar.gz"), map[string]string{"../out.txt": "no"})
	code, out := runIn(t, root)
	if code == 0 || !strings.Contains(out, "REFUSED") {
		t.Errorf("code %d, out:\n%s", code, out)
	}
}

// TestAPopulatedTargetIsLeftAlone: extracting again must not overwrite work,
// and the archive is never deleted either way.
func TestAPopulatedTargetIsLeftAlone(t *testing.T) {
	root := t.TempDir()
	writeZip(t, filepath.Join(root, "a.zip"), map[string]string{"f.txt": "new"})
	target := filepath.Join(root, "extracted", "a")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "f.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := runIn(t, root)
	if code != 0 || !strings.Contains(out, "SKIPPED") {
		t.Errorf("code %d, out:\n%s", code, out)
	}
	b, err := os.ReadFile(filepath.Join(target, "f.txt"))
	if err != nil || string(b) != "old" {
		t.Errorf("the existing file was overwritten: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.zip")); err != nil {
		t.Errorf("the archive was removed: %v", err)
	}
}

// TestWhatItCannotDoIsSaidSeparately.
//
// ⛔ A count that mixes "already done" with "cannot do" reads as success. 7z,
// rar and zstd need a decompressor this module does not carry, and they are
// reported in their own line and their own total.
//
// ⛔ .tar.xz used to be in this list. It is read natively now, so a case that
// still named it would assert behaviour this tool no longer has -- and its
// fixture, eight bytes of text, would count as a FAILED xz rather than an
// external one.
func TestWhatItCannotDoIsSaidSeparately(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"x.7z", "y.rar", "w.tar.zst"} {
		if err := os.WriteFile(filepath.Join(root, n), []byte("not read"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, out := runIn(t, root)
	if code != 0 {
		t.Errorf("code %d, want 0 -- these are not failures\n%s", code, out)
	}
	if n := strings.Count(out, "EXTERNAL"); n != 3 {
		t.Errorf("%d EXTERNAL lines, want 3:\n%s", n, out)
	}
	if !strings.Contains(out, "needs an external tool 3") {
		t.Errorf("the tally does not separate them:\n%s", out)
	}
	if strings.Contains(out, "skipped 4") {
		t.Error("they were counted as skipped")
	}
}

// TestABareRootEntryIsNotAnEscape: some writers emit a "/" entry, which is an
// artefact rather than an absolute path to anywhere.
func TestABareRootEntryIsNotAnEscape(t *testing.T) {
	root := t.TempDir()
	writeZip(t, filepath.Join(root, "catalogue.zip"), map[string]string{
		"/":          "",
		"sheet.xlsx": "data",
	})
	code, out := runIn(t, root)
	if code != 0 || !strings.Contains(out, "EXTRACTED") {
		t.Errorf("code %d, out:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(root, "extracted", "catalogue", "sheet.xlsx")); err != nil {
		t.Errorf("sheet.xlsx: %v", err)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	writeZip(t, filepath.Join(root, "a.zip"), map[string]string{"f.txt": "x"})
	code, out := runIn(t, root, "-n")
	if code != 0 || !strings.Contains(out, "WOULD") {
		t.Errorf("code %d, out:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(root, "extracted", "a", "f.txt")); err == nil {
		t.Error("-n wrote a file")
	}
}

func TestStem(t *testing.T) {
	for in, want := range map[string]string{
		"a.zip":                "a",
		"b.tar.gz":             "b",
		"c.tgz":                "c",
		"d.tar.bz2":            "d",
		"e.TAR.GZ":             "e",
		"f.7z":                 "f",
		"g":                    "g",
		"h.tar.gz.zip":         "h.tar.gz",
		"i.name.with.dots.zip": "i.name.with.dots",
	} {
		if got := stem(in); got != want {
			t.Errorf("stem(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestALatin1EntryNameIsReEncodedAndSaidSo.
//
// ⛔ APFS refuses a filename that is not valid UTF-8, with EILSEQ. Three real
// archives of logos could not be extracted at all because their entries carry
// a lone 0xE9 for "é" -- Latin-1, not the CP437 the zip specification names for
// an entry whose UTF-8 flag is clear.
//
// ⛔ And it is REPORTED: a renamed file is a file the caller cannot find by the
// name they read in the archive.
func TestALatin1EntryNameIsReEncodedAndSaidSo(t *testing.T) {
	root := t.TempDir()
	latin1 := "Logo IP Paris 6 \xe9coles jpg 300 DPI.jpg"
	writeZip(t, filepath.Join(root, "logos.zip"), map[string]string{
		latin1:       "jpeg",
		"README.txt": "fine",
	})

	code, out := runIn(t, root)
	if code != 0 {
		t.Fatalf("code %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "1 name(s) re-encoded from Latin-1") {
		t.Errorf("the re-encoding was not reported:\n%s", out)
	}
	want := filepath.Join(root, "extracted", "logos", "Logo IP Paris 6 écoles jpg 300 DPI.jpg")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("%s: %v", want, err)
	}
	// The entry that was already valid is untouched and not counted.
	if _, err := os.Stat(filepath.Join(root, "extracted", "logos", "README.txt")); err != nil {
		t.Errorf("README.txt: %v", err)
	}
}

func TestUsable(t *testing.T) {
	for _, c := range []struct {
		in      string
		want    string
		changed bool
	}{
		{"plain.txt", "plain.txt", false},
		{"déjà.txt", "déjà.txt", false},       // already UTF-8
		{"écoles.txt", "écoles.txt", false}, // decomposed, still valid
		{"\xe9coles.txt", "écoles.txt", true}, // Latin-1
		{"a\xffb", "aÿb", true},
	} {
		got, changed := usable(c.in)
		if got != c.want || changed != c.changed {
			t.Errorf("usable(%q) = %q,%v; want %q,%v", c.in, got, changed, c.want, c.changed)
		}
	}
}

// TestATarXzExtracts: three of the four archives this tool could not read were
// .tar.xz, so the capability was added rather than reported. A positive witness
// is what separates "handled" from "no longer mentioned".
func TestATarXzExtracts(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	xw, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(xw)
	body := "fn main() {}"
	if err := tw.WriteHeader(&tar.Header{
		Name: "typst/src/main.rs", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	for _, c := range []func() error{tw.Close, xw.Close} {
		if err := c(); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "typst-aarch64.tar.xz"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := runIn(t, root)
	if code != 0 {
		t.Fatalf("code %d\n%s", code, out)
	}
	if strings.Contains(out, "EXTERNAL") {
		t.Errorf("a .tar.xz was still reported as external:\n%s", out)
	}
	got := filepath.Join(root, "extracted", "typst-aarch64", "typst/src/main.rs")
	if b, err := os.ReadFile(got); err != nil || string(b) != body {
		t.Errorf("%s: %q, %v", got, b, err)
	}
}
