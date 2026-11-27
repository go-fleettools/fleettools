package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
)

// fakeWIM builds the 208-byte header and an XML resource the way a Windows
// install image lays them out: UTF-16LE with a BOM, after the header.
func fakeWIM(xmlText string, flags byte) []byte {
	u := utf16.Encode([]rune(xmlText))
	body := []byte{0xFF, 0xFE}
	for _, c := range u {
		body = binary.LittleEndian.AppendUint16(body, c)
	}
	hdr := make([]byte, 208)
	copy(hdr, "MSWIM\x00\x00\x00")
	d := hdr[0x48:]
	size := uint64(len(body))
	for i := 0; i < 7; i++ {
		d[i] = byte(size >> (8 * i))
	}
	d[7] = flags
	binary.LittleEndian.PutUint64(d[8:], 208)
	binary.LittleEndian.PutUint64(d[16:], size)
	return append(hdr, body...)
}

func TestWimEditions(t *testing.T) {
	x := `<WIM><TOTALBYTES>1</TOTALBYTES>` +
		`<IMAGE INDEX="1"><NAME>Windows 11 Home</NAME><DISPLAYNAME>x</DISPLAYNAME></IMAGE>` +
		`<IMAGE INDEX="6"><NAME>Windows 11 Pro</NAME></IMAGE></WIM>`
	got, err := wimEditions(bytes.NewReader(fakeWIM(x, 0)))
	if err != nil || !slices.Equal(got, []string{"Windows 11 Home", "Windows 11 Pro"}) {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestWimEditionsRefusals(t *testing.T) {
	for name, c := range map[string]struct {
		data []byte
		want string
	}{
		"empty":      {nil, "wim header"},
		"signature":  {make([]byte, 300), "MSWIM"},
		"compressed": {fakeWIM("<WIM/>", 0x04), "compressed"},
		"bad xml":    {fakeWIM("<WIM><IMAGE", 0), "wim xml"},
	} {
		if _, err := wimEditions(bytes.NewReader(c.data)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

// fakeSevenZip is a shell script that "extracts" wim as sources/install.wim (or
// sources/install.esd when esd is set) into its -o directory, and fails for the
// other name.
func fakeSevenZip(t *testing.T, wim []byte, esd bool) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stand-in for 7z")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.wim")
	if err := os.WriteFile(src, wim, 0o600); err != nil {
		t.Fatal(err)
	}
	name := "install.wim"
	if esd {
		name = "install.esd"
	}
	body := "#!/bin/sh\nfor a in \"$@\"; do case \"$a\" in -o*) d=${a#-o};; esac; done\n" +
		"case \"$*\" in *sources/" + name + "*) cp '" + src + "' \"$d/" + name + "\";; *) exit 1;; esac\n"
	p := filepath.Join(dir, "7z")
	if err := os.WriteFile(p, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckEdition(t *testing.T) {
	x := `<WIM><IMAGE INDEX="1"><NAME>Windows 11 Pro</NAME></IMAGE></WIM>`
	for _, esd := range []bool{false, true} {
		var w bytes.Buffer
		sz := fakeSevenZip(t, fakeWIM(x, 0), esd)
		if err := checkEdition(t.Context(), sz, "w.iso", t.TempDir(), "Windows 11 Pro", &w); err != nil {
			t.Fatalf("esd=%v: %v", esd, err)
		}
		if !strings.Contains(w.String(), "Windows 11 Pro") {
			t.Fatalf("no listing: %q", w.String())
		}
		err := checkEdition(t.Context(), sz, "w.iso", t.TempDir(), "Windows 11 Enterprise", &w)
		if err == nil || !strings.Contains(err.Error(), "is not an image of the ISO") {
			t.Fatalf("esd=%v: err = %v", esd, err)
		}
	}
	// a broken WIM, no 7z, an ISO without an install image
	sz := fakeSevenZip(t, []byte("junk"), false)
	if err := checkEdition(t.Context(), sz, "w.iso", t.TempDir(), "x", &bytes.Buffer{}); err == nil {
		t.Fatal("a junk WIM was accepted")
	}
	if err := checkEdition(t.Context(), "/nonexistent/7z", "w.iso", t.TempDir(), "x", &bytes.Buffer{}); err != errNoSevenZip {
		t.Fatalf("err = %v", err)
	}
	none := filepath.Join(t.TempDir(), "7z")
	if err := os.WriteFile(none, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := checkEdition(t.Context(), none, "w.iso", t.TempDir(), "x", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "neither") {
		t.Fatalf("err = %v", err)
	}
}

// wimWithDescriptor is a header whose XML descriptor holds size and offset.
func wimWithDescriptor(size uint64, off uint64, tail int) []byte {
	hdr := make([]byte, 208+tail)
	copy(hdr, "MSWIM\x00\x00\x00")
	d := hdr[0x48:]
	for i := 0; i < 7; i++ {
		d[i] = byte(size >> (8 * i))
	}
	binary.LittleEndian.PutUint64(d[8:], off)
	return hdr
}

func TestWimEditionsSizeBounds(t *testing.T) {
	for name, c := range map[string][]byte{
		"zero":      wimWithDescriptor(0, 208, 64),
		"odd":       wimWithDescriptor(7, 208, 64),
		"too large": wimWithDescriptor(16<<20+2, 208, 64),
	} {
		if _, err := wimEditions(bytes.NewReader(c)); err == nil || !strings.Contains(err.Error(), "implausible") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// the largest accepted size passes the bound and fails on the missing bytes
	if _, err := wimEditions(bytes.NewReader(wimWithDescriptor(16<<20, 208, 64))); err == nil || !strings.Contains(err.Error(), "wim xml") {
		t.Errorf("at the bound: err = %v", err)
	}
	// a descriptor that points past the end of the file
	if _, err := wimEditions(bytes.NewReader(wimWithDescriptor(8, 1<<20, 64))); err == nil || !strings.Contains(err.Error(), "wim xml") {
		t.Errorf("past the end: err = %v", err)
	}
}

func TestCheckEditionRefusals(t *testing.T) {
	sz := fakeSevenZip(t, []byte("junk"), false)
	err := checkEdition(t.Context(), sz, "w.iso", t.TempDir(), "x", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "sources/install.wim: ") {
		t.Fatalf("a junk WIM: err = %v", err)
	}
	// a work directory that does not exist
	err = checkEdition(t.Context(), sz, "w.iso", filepath.Join(t.TempDir(), "none"), "x", &bytes.Buffer{})
	if err == nil || err == errNoSevenZip || strings.Contains(err.Error(), "install.wim") {
		t.Fatalf("a missing work dir: err = %v", err)
	}
}

// A XML resource that runs past the end of the file is refused, even when the
// bytes that are there parse (the zeros the short read leaves are ignored by
// the XML decoder after the root element).
func TestWimEditionsShortRead(t *testing.T) {
	full := fakeWIM(`<WIM><IMAGE INDEX="1"><NAME>Windows 11 Pro</NAME></IMAGE></WIM>  `, 0)
	if _, err := wimEditions(bytes.NewReader(full)); err != nil {
		t.Fatal(err)
	}
	if _, err := wimEditions(bytes.NewReader(full[:len(full)-4])); err == nil || !strings.Contains(err.Error(), "wim xml") {
		t.Fatalf("err = %v", err)
	}
}
