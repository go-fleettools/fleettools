package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"debug/buildinfo"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
)

// A release that ships archives -- goreleaser's fileshare_v0.30.0_linux_amd64.tar.gz
// -- has its Go binaries one level down. Checking the archive's sum and
// attestation alone says the archive is the one the workflow made, not that
// the program in it is the tag's code: v0.15.0 reported such a release
// "verified" without reading a single build info. So the members are read,
// in memory, and each Go binary among them is held to the same build checks
// as a bare one.

// Limits against an archive built to exhaust the reader: one member, and
// everything a compressed stream may expand to.
var (
	maxMember   int64 = 512 << 20
	maxExpanded int64 = 4 << 30
)

// A member is one Go binary inside an archive.
type member struct {
	name string
	bi   *debug.BuildInfo
	data []byte
}

// isArchive says whether a published file is an archive this reads.
func isArchive(name string) bool {
	return strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz") || strings.HasSuffix(name, ".zip")
}

// goBinariesIn is every Go binary inside an archive.
func goBinariesIn(path string) ([]member, error) {
	if strings.HasSuffix(path, ".zip") {
		return zipMembers(path)
	}
	return tarMembers(path)
}

func tarMembers(path string) ([]member, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("not a gzip stream: %w", err)
	}
	expanded := &limited{r: gz, left: maxExpanded}
	tr := tar.NewReader(expanded)
	var out []member
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("reading the archive: %w", err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if h.Size > maxMember {
			return nil, fmt.Errorf("%s is %d bytes, more than the %d this reads", h.Name, h.Size, maxMember)
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxMember))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", h.Name, err)
		}
		if m, ok := goBinary(h.Name, data); ok {
			out = append(out, m)
		}
	}
}

func zipMembers(path string) ([]member, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("not a zip archive: %w", err)
	}
	defer zr.Close()
	var out []member
	var total int64
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() || !zf.Mode().IsRegular() {
			continue
		}
		// The header's size is the archive's claim; the read below is
		// limited whatever it says.
		if zf.UncompressedSize64 > uint64(maxMember) {
			return nil, fmt.Errorf("%s says it is %d bytes, more than the %d this reads", zf.Name, zf.UncompressedSize64, maxMember)
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, fmt.Errorf("opening %s: %w", zf.Name, err)
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxMember+1))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", zf.Name, err)
		}
		if int64(len(data)) > maxMember {
			return nil, fmt.Errorf("%s expands past the %d bytes this reads", zf.Name, maxMember)
		}
		if total += int64(len(data)); total > maxExpanded {
			return nil, fmt.Errorf("the archive expands past %d bytes", maxExpanded)
		}
		if m, ok := goBinary(zf.Name, data); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func goBinary(name string, data []byte) (member, bool) {
	bi, err := buildinfo.Read(bytes.NewReader(data))
	if err != nil {
		return member{}, false
	}
	return member{name: name, bi: bi, data: data}, true
}

// limited is an io.Reader that fails, rather than stopping quietly, once it
// has given left bytes: a stream cut short at the limit would read as a
// complete archive with members missing.
type limited struct {
	r    io.Reader
	left int64
}

func (l *limited) Read(p []byte) (int, error) {
	if l.left <= 0 {
		return 0, fmt.Errorf("the archive expands past %d bytes", maxExpanded)
	}
	if int64(len(p)) > l.left {
		p = p[:l.left]
	}
	n, err := l.r.Read(p)
	l.left -= int64(n)
	return n, err
}
