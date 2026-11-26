package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
	"github.com/diskfs/go-diskfs/partition/gpt"
)

// bootPaths are the parts of a Windows installer ISO that make a UEFI boot disk:
// the boot manager, its BCD and the WinPE image Setup runs from. install.wim is
// NOT among them (it exceeds the 4 GiB FAT32 file limit): the installer ISO
// stays attached as a CD and Setup finds install.wim there.
var bootPaths = []string{"bootmgr", "bootmgr.efi", "boot", "efi", "sources/boot.wim"}

// extractBootFiles unpacks bootPaths from the ISO into dir with 7zz (a UDF
// image: the ISO9660 side of a Windows ISO holds no files). dir must be empty.
func extractBootFiles(ctx context.Context, sevenZip, iso, dir string) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(ents) != 0 {
		return fmt.Errorf("%s is not empty", dir)
	}
	args := append([]string{"x", "-y", "-bso0", "-bsp0", "-o" + dir, iso}, bootPaths...)
	out, err := exec.CommandContext(ctx, sevenZip, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", sevenZip, err, strings.TrimSpace(string(out)))
	}
	for _, need := range []string{"bootmgr.efi", "efi/boot/bootx64.efi", "efi/microsoft/boot/bcd", "boot/boot.sdi", "sources/boot.wim"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(need))); err != nil {
			return fmt.Errorf("the ISO has no %s after extraction: %w", need, err)
		}
	}
	return nil
}

// buildBootDisk writes a GPT raw disk image of size bytes holding one FAT32
// EFI system partition that contains every file under tree plus the extra
// files (name -> content) at its root. The file is created: it must not exist.
func buildBootDisk(path, tree string, extra map[string][]byte, size int64) error {
	const sector = 512
	d, err := diskfs.Create(path, size, diskfs.SectorSize512)
	if err != nil {
		return err
	}
	defer d.Close()
	first := uint64(2048)
	last := uint64(size/sector) - 34
	table := &gpt.Table{
		LogicalSectorSize: sector, PhysicalSectorSize: sector, ProtectiveMBR: true,
		Partitions: []*gpt.Partition{{Index: 1, Start: first, End: last, Type: gpt.EFISystemPartition, Name: "OSVMBOOT"}},
	}
	if err := d.Partition(table); err != nil {
		return fmt.Errorf("partition: %w", err)
	}
	fat, err := d.CreateFilesystem(disk.FilesystemSpec{Partition: 1, FSType: filesystem.TypeFat32, VolumeLabel: "OSVMBOOT"})
	if err != nil {
		return fmt.Errorf("fat32: %w", err)
	}
	defer fat.Close()
	err = filepath.WalkDir(tree, func(p string, e fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, err := filepath.Rel(tree, p)
		if err != nil || rel == "." {
			return err
		}
		dst := "/" + filepath.ToSlash(rel)
		if e.IsDir() {
			return fat.Mkdir(dst)
		}
		return copyInto(fat, dst, p)
	})
	if err != nil {
		return err
	}
	for name, body := range extra {
		f, err := fat.OpenFile("/"+name, os.O_CREATE|os.O_RDWR)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		_, werr := f.Write(body)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return fmt.Errorf("%s: %w", name, werr)
		}
	}
	return nil
}

// buildAnswerISO writes a small Joliet ISO labelled OSVMANSWER holding the files
// (name -> content) at its root. Windows Setup looks for autounattend.xml on
// the root of every CD, but not on the fixed boot disk (measured with QEMU).
// The file is created: it must not exist.
func buildAnswerISO(path string, files map[string][]byte) error {
	d, err := diskfs.Create(path, 2<<20, diskfs.SectorSize(2048))
	if err != nil {
		return err
	}
	defer d.Close()
	fsys, err := d.CreateFilesystem(disk.FilesystemSpec{Partition: 0, FSType: filesystem.TypeISO9660, VolumeLabel: "OSVMANSWER"})
	if err != nil {
		return fmt.Errorf("iso9660: %w", err)
	}
	for name, body := range files {
		f, err := fsys.OpenFile("/"+name, os.O_CREATE|os.O_RDWR)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		_, werr := f.Write(body)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return fmt.Errorf("%s: %w", name, werr)
		}
	}
	iso, ok := fsys.(*iso9660.FileSystem)
	if !ok {
		return errors.New("iso9660: unexpected file system type")
	}
	return iso.Finalize(iso9660.FinalizeOptions{Joliet: true, RockRidge: true, VolumeIdentifier: "OSVMANSWER"})
}

func copyInto(fat filesystem.FileSystem, dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := fat.OpenFile(dst, os.O_CREATE|os.O_RDWR)
	if err != nil {
		return fmt.Errorf("%s: %w", dst, err)
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("%s: %w", dst, err)
	}
	return nil
}

var errNoSevenZip = errors.New("7zz (7-Zip) is required to read the installer ISO's UDF file system; install it and put it on PATH")
