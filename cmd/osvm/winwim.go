package main

import (
	"context"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf16"
)

// wimEditions returns the image names (the /IMAGE/NAME that an answer file's
// InstallFrom/MetaData selects) listed in the XML data of a WIM or ESD file.
// Layout (WIM file format, header of 208 bytes): "MSWIM\0\0\0" at 0, the XML
// data resource descriptor at 0x48: 7 bytes of size, 1 byte of flags, 8 bytes
// of offset, 8 bytes of original size. The XML is UTF-16LE; in a Windows
// install image it is stored uncompressed.
func wimEditions(r io.ReaderAt) ([]string, error) {
	const (
		xmlDesc  = 0x48
		compress = 0x04
		maxXML   = 16 << 20
	)
	hdr := make([]byte, xmlDesc+24)
	if _, err := r.ReadAt(hdr, 0); err != nil {
		return nil, fmt.Errorf("wim header: %w", err)
	}
	if string(hdr[:5]) != "MSWIM" {
		return nil, errors.New("not a WIM file (no MSWIM signature)")
	}
	d := hdr[xmlDesc:]
	var s8 [8]byte
	copy(s8[:], d[:7])
	size := binary.LittleEndian.Uint64(s8[:])
	flags := d[7]
	off := binary.LittleEndian.Uint64(d[8:16])
	if flags&compress != 0 {
		return nil, errors.New("the WIM's XML data is compressed: cannot list its images")
	}
	if size == 0 || size > maxXML || size%2 != 0 {
		return nil, fmt.Errorf("the WIM's XML data has an implausible size of %d bytes", size)
	}
	raw := make([]byte, size)
	if _, err := r.ReadAt(raw, int64(off)); err != nil {
		return nil, fmt.Errorf("wim xml: %w", err)
	}
	u := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		u = append(u, binary.LittleEndian.Uint16(raw[i:]))
	}
	if len(u) > 0 && u[0] == 0xFEFF {
		u = u[1:]
	}
	text := string(utf16.Decode(u))
	var doc struct {
		Images []struct {
			Name string `xml:"NAME"`
		} `xml:"IMAGE"`
	}
	dec := xml.NewDecoder(strings.NewReader(text))
	dec.CharsetReader = func(string, io.Reader) (io.Reader, error) { return strings.NewReader(text), nil }
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("wim xml: %w", err)
	}
	var names []string
	for _, im := range doc.Images {
		names = append(names, im.Name)
	}
	return names, nil
}

// checkEdition extracts the install image from the installer ISO into a new
// directory under work and refuses an edition that is not one of its image
// names, before a three-hour build is started (cloudbase's imaging tools make
// the same check before applying an image).
func checkEdition(ctx context.Context, sevenZip, iso, work, edition string, w io.Writer) error {
	if _, err := exec.LookPath(sevenZip); err != nil {
		return errNoSevenZip
	}
	dir, err := os.MkdirTemp(work, "wim-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	var last error
	for _, name := range []string{"install.wim", "install.esd"} {
		out, err := exec.CommandContext(ctx, sevenZip, "e", "-y", "-bso0", "-bsp0", "-o"+dir, iso, "sources/"+name).CombinedOutput()
		f, oerr := os.Open(filepath.Join(dir, name))
		if oerr != nil {
			last = fmt.Errorf("%s: sources/%s: %v %s", sevenZip, name, err, strings.TrimSpace(string(out)))
			continue
		}
		names, werr := wimEditions(f)
		f.Close()
		if werr != nil {
			return fmt.Errorf("sources/%s: %w", name, werr)
		}
		fmt.Fprintf(w, "installer images: %q\n", names)
		if !slices.Contains(names, edition) {
			return fmt.Errorf("--edition %q is not an image of the ISO's %s (it has %q)", edition, name, names)
		}
		return nil
	}
	return fmt.Errorf("the ISO has neither sources/install.wim nor sources/install.esd: %w", last)
}
