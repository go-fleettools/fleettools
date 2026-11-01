package main

import (
	"archive/tar"
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A planted xz bomb: a valid stream whose LZMA2 block header declares a
// 1.5 GiB dictionary (property byte 37) over a few bytes of data. A decoder that
// allocates the dictionary from the header allocates 1.5 GiB for this input
// (ulikunitz/xz v0.5.17: 1,610,663,976 bytes for a 44-byte stream); the decoder
// in use must allocate kilobytes.
//
// Run it under GOMEMLIMIT and ulimit -v (see .github/workflows/ci.yml), never
// against the memory of the whole host: a regression would allocate gigabytes.

func xzVLI(v uint64) []byte {
	var b []byte
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func xzCRC(b []byte) []byte { return binary.LittleEndian.AppendUint32(nil, crc32.ChecksumIEEE(b)) }

// plantedXz wraps plain (at most 64 KiB) in one LZMA2 block of a stored chunk
// (CRC32 check) whose header declares the dictionary property dictProp (37 is
// 1.5 GiB).
func plantedXz(plain []byte, dictProp byte) []byte {
	const check = 1 // CRC32
	comp := []byte{1, byte((len(plain) - 1) >> 8), byte(len(plain) - 1)}
	comp = append(comp, plain...)
	comp = append(comp, 0)

	// Block header: sizes present, one filter (LZMA2) with its property.
	body := []byte{0x40 | 0x80}
	body = append(body, xzVLI(uint64(len(comp)))...)
	body = append(body, xzVLI(uint64(len(plain)))...)
	body = append(body, 0x21, 1, dictProp)
	n := 1 + len(body) + 4
	for n%4 != 0 {
		body = append(body, 0)
		n++
	}
	bh := append([]byte{byte(n/4 - 1)}, body...)
	bh = append(bh, xzCRC(bh)...)

	blk := append(append([]byte(nil), bh...), comp...)
	unpadded := uint64(len(blk)) + 4
	for len(blk)%4 != 0 {
		blk = append(blk, 0)
	}
	blk = append(blk, xzCRC(plain)...)

	idx := append([]byte{0, 1}, xzVLI(unpadded)...)
	idx = append(idx, xzVLI(uint64(len(plain)))...)
	for len(idx)%4 != 0 {
		idx = append(idx, 0)
	}
	idx = append(idx, xzCRC(idx)...)

	hdr := append([]byte{0xFD, '7', 'z', 'X', 'Z', 0, 0, check}, xzCRC([]byte{0, check})...)
	fbody := binary.LittleEndian.AppendUint32(nil, uint32(len(idx)/4-1))
	fbody = append(fbody, 0, check)
	foot := append(xzCRC(fbody), fbody...)
	foot = append(foot, 'Y', 'Z')

	s := append(hdr, blk...)
	s = append(s, idx...)
	return append(s, foot...)
}

// TestTarXzDeclaredDictionaryIsNotAllocated: a .tar.xz whose block header
// declares 1.5 GiB over a small tar extracts, and costs far less than 1 MiB.
func TestTarXzDeclaredDictionaryIsNotAllocated(t *testing.T) {
	var tarball bytes.Buffer
	tw := tar.NewWriter(&tarball)
	body := "fn main() {}"
	if err := tw.WriteHeader(&tar.Header{Name: "a/main.rs", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "bomb.tar.xz")
	if err := os.WriteFile(src, plantedXz(tarball.Bytes(), 37), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	files, _, err := extractTar(src, dest, kindTarXz)
	runtime.ReadMemStats(&after)
	if err != nil || files != 1 {
		t.Fatalf("files=%d err=%v", files, err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "a", "main.rs")); err != nil || string(b) != body {
		t.Fatalf("%q %v", b, err)
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 1<<20 {
		t.Fatalf("%d bytes allocated for a 1.5 GiB declared dictionary", alloc)
	}
}
