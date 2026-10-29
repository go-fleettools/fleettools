// Package elftest builds minimal, REAL ELF64 files for tests.
//
// ⛔ IT EXISTS BECAUSE A PLACEHOLDER FIXTURE CANNOT WITNESS A CHECK THAT READS
// BYTES. In go-pkgx/bk, every "shared library" in the tests of `bk unlinked`
// contained the single byte "x". The suite was green, coverage was complete
// and every mutation was caught — while the shipped command counted
// `libstdc++.so.6.0.33-gdb.py` (a Python script) as a library in v0.43, and
// then refused `libfolly.so.0.58.0-dev` (an ELF shared object) in v0.44.0.
// Two opposite errors in one day, both invisible to fixtures that were not
// ELF files.
//
// The builders here are deliberately hand-assembled rather than produced by a
// linker: a test that needs a toolchain to make its input does not run on the
// lane where the input is foreign, which is exactly the lane a cross-platform
// reader has to be right on.
//
// Each builder verifies its own output with debug/elf before returning, so a
// malformed fixture fails as a fixture rather than as the thing under test.
package elftest

import (
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

const (
	ehsize   = 64
	shentsiz = 64
	symsize  = 24
)

func align8(o int64) int64 {
	if r := o % 8; r != 0 {
		return o + (8 - r)
	}
	return o
}

// header writes the ELF64 little-endian header for a shared object whose
// section table lives at shoff and holds n sections, with shstrndx naming the
// section-name string table.
func header(buf []byte, shoff int64, n, shstrndx uint16) {
	le := binary.LittleEndian
	copy(buf, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	le.PutUint16(buf[16:], uint16(elf.ET_DYN))
	le.PutUint16(buf[18:], uint16(elf.EM_X86_64))
	le.PutUint32(buf[20:], 1)
	le.PutUint64(buf[40:], uint64(shoff))
	le.PutUint16(buf[52:], ehsize)
	le.PutUint16(buf[58:], shentsiz)
	le.PutUint16(buf[60:], n)
	le.PutUint16(buf[62:], shstrndx)
}

// section writes one section header.
func section(buf []byte, shoff int64, idx int, name, typ uint32, off, size uint64, link uint32, entsize uint64) {
	le := binary.LittleEndian
	b := buf[shoff+int64(idx)*shentsiz:]
	le.PutUint32(b[0:], name)
	le.PutUint32(b[4:], typ)
	le.PutUint64(b[24:], off)
	le.PutUint64(b[32:], size)
	le.PutUint32(b[40:], link)
	le.PutUint64(b[48:], 1)
	le.PutUint64(b[56:], entsize)
}

func write(t *testing.T, path string, buf []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf, 0o755); err != nil {
		t.Fatal(err)
	}
}

// WriteNeeded writes an ELF64 whose .dynamic names the given DT_NEEDED
// entries, creating parent directories as required.
func WriteNeeded(t *testing.T, path string, needed ...string) {
	t.Helper()
	le := binary.LittleEndian

	dynstr := []byte{0}
	offs := make([]uint64, len(needed))
	for i, n := range needed {
		offs[i] = uint64(len(dynstr))
		dynstr = append(dynstr, n...)
		dynstr = append(dynstr, 0)
	}
	var dyn []byte
	put := func(tag elf.DynTag, val uint64) {
		var e [16]byte
		le.PutUint64(e[0:], uint64(tag))
		le.PutUint64(e[8:], val)
		dyn = append(dyn, e[:]...)
	}
	for _, o := range offs {
		put(elf.DT_NEEDED, o)
	}
	put(elf.DT_NULL, 0)

	shstr := []byte("\x00.dynstr\x00.dynamic\x00.shstrtab\x00")
	off := int64(ehsize)
	dynstrOff := off
	off = align8(off + int64(len(dynstr)))
	dynamicOff := off
	off += int64(len(dyn))
	shstrOff := off
	off = align8(off + int64(len(shstr)))
	shoff := off

	buf := make([]byte, shoff+4*shentsiz)
	header(buf, shoff, 4, 3)
	copy(buf[dynstrOff:], dynstr)
	copy(buf[dynamicOff:], dyn)
	copy(buf[shstrOff:], shstr)
	section(buf, shoff, 0, 0, uint32(elf.SHT_NULL), 0, 0, 0, 0)
	section(buf, shoff, 1, 1, uint32(elf.SHT_STRTAB), uint64(dynstrOff), uint64(len(dynstr)), 0, 0)
	section(buf, shoff, 2, 9, uint32(elf.SHT_DYNAMIC), uint64(dynamicOff), uint64(len(dyn)), 1, 16)
	section(buf, shoff, 3, 18, uint32(elf.SHT_STRTAB), uint64(shstrOff), uint64(len(shstr)), 0, 0)

	write(t, path, buf)

	f, err := elf.Open(path)
	if err != nil {
		t.Fatalf("crafted ELF invalid: %v", err)
	}
	defer f.Close()
	got, _ := f.DynString(elf.DT_NEEDED)
	if len(got) != len(needed) {
		t.Fatalf("fixture carries %v, want %v", got, needed)
	}
}

// WriteExports writes an ELF64 whose .dynsym DEFINES the given symbols, and
// leaves `undef` as undefined references.
//
// ⛔ THE SPLIT IS THE POINT. An undefined symbol is one the file NEEDS; a
// defined one is what it PROVIDES. A reader that does not separate them
// answers "does it use gettext" when asked "does it provide gettext", and
// those have opposite consequences for a dependency.
func WriteExports(t *testing.T, path string, defined []string, undef ...string) {
	t.Helper()
	le := binary.LittleEndian

	dynstr := []byte{0}
	type sym struct {
		nameOff uint64
		shndx   uint16
	}
	syms := []sym{{0, 0}} // the mandatory null entry
	add := func(name string, shndx uint16) {
		o := uint64(len(dynstr))
		dynstr = append(dynstr, name...)
		dynstr = append(dynstr, 0)
		syms = append(syms, sym{o, shndx})
	}
	for _, n := range defined {
		add(n, 1) // any non-SHN_UNDEF section index means "defined here"
	}
	for _, n := range undef {
		add(n, uint16(elf.SHN_UNDEF))
	}

	dynsym := make([]byte, len(syms)*symsize)
	for i, s := range syms {
		b := dynsym[i*symsize:]
		le.PutUint32(b[0:], uint32(s.nameOff))
		if i > 0 {
			// STB_GLOBAL<<4 | STT_FUNC: a binding debug/elf reports and a
			// linker would produce for an exported function.
			b[4] = byte(elf.ST_INFO(elf.STB_GLOBAL, elf.STT_FUNC))
		}
		le.PutUint16(b[6:], s.shndx)
	}

	shstr := []byte("\x00.dynstr\x00.dynsym\x00.shstrtab\x00")
	off := int64(ehsize)
	dynstrOff := off
	off = align8(off + int64(len(dynstr)))
	dynsymOff := off
	off += int64(len(dynsym))
	shstrOff := off
	off = align8(off + int64(len(shstr)))
	shoff := off

	buf := make([]byte, shoff+4*shentsiz)
	header(buf, shoff, 4, 3)
	copy(buf[dynstrOff:], dynstr)
	copy(buf[dynsymOff:], dynsym)
	copy(buf[shstrOff:], shstr)
	section(buf, shoff, 0, 0, uint32(elf.SHT_NULL), 0, 0, 0, 0)
	section(buf, shoff, 1, 1, uint32(elf.SHT_STRTAB), uint64(dynstrOff), uint64(len(dynstr)), 0, 0)
	// link = 1: .dynsym's strings live in .dynstr, and debug/elf needs the
	// edge to name anything at all.
	section(buf, shoff, 2, 9, uint32(elf.SHT_DYNSYM), uint64(dynsymOff), uint64(len(dynsym)), 1, symsize)
	section(buf, shoff, 3, 17, uint32(elf.SHT_STRTAB), uint64(shstrOff), uint64(len(shstr)), 0, 0)

	write(t, path, buf)

	f, err := elf.Open(path)
	if err != nil {
		t.Fatalf("crafted ELF invalid: %v", err)
	}
	defer f.Close()
	got, err := f.DynamicSymbols()
	if err != nil {
		t.Fatalf("crafted ELF has no readable .dynsym: %v", err)
	}
	if len(got) != len(defined)+len(undef) {
		t.Fatalf("fixture carries %d symbol(s), want %d", len(got), len(defined)+len(undef))
	}
}
