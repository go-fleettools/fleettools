package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	diskfs "github.com/diskfs/go-diskfs"
)

func TestBuildAnswerISORoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "answer.iso")
	files := map[string][]byte{"autounattend.xml": []byte("<unattend/>"), provisionName: []byte("Write-Host hi")}
	if err := buildAnswerISO(p, files); err != nil {
		t.Fatal(err)
	}
	d, err := diskfs.Open(p, diskfs.WithOpenMode(diskfs.ReadOnly), diskfs.WithSectorSize(diskfs.SectorSize(2048)))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	fsys, err := d.GetFilesystem(0)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimRight(fsys.Label(), " \x00"); got != "OSVMANSWER" {
		t.Errorf("volume label %q", got)
	}
	for name, want := range files {
		f, err := fsys.OpenFile("/"+name, os.O_RDONLY)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := io.ReadAll(f)
		f.Close()
		if err != nil || string(got) != string(want) {
			t.Errorf("%s: got %q, %v", name, got, err)
		}
	}
}

func TestBuildAnswerISORefusesExistingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.iso")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := buildAnswerISO(p, map[string][]byte{"a": []byte("b")}); err == nil {
		t.Fatal("overwrote an existing file")
	}
}

func TestBuildBootDiskRoundTrip(t *testing.T) {
	tree := t.TempDir()
	for name, body := range map[string]string{"bootmgr": "BM", "efi/boot/bootx64.efi": "EFI", "sources/boot.wim": "WIM"} {
		full := filepath.Join(tree, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(t.TempDir(), "boot.img")
	if err := buildBootDisk(p, tree, map[string][]byte{"extra.txt": []byte("E")}, 64<<20); err != nil {
		t.Fatal(err)
	}
	d, err := diskfs.Open(p, diskfs.WithOpenMode(diskfs.ReadOnly))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	fsys, err := d.GetFilesystem(1)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"/bootmgr": "BM", "/efi/boot/bootx64.efi": "EFI", "/sources/boot.wim": "WIM", "/extra.txt": "E"} {
		f, err := fsys.OpenFile(name, os.O_RDONLY)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := io.ReadAll(f)
		f.Close()
		if err != nil || string(got) != want {
			t.Errorf("%s: got %q, %v", name, got, err)
		}
	}
}

func TestExtractBootFilesRefusesNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := extractBootFiles(t.Context(), "7zz", "x.iso", dir); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("got %v", err)
	}
}

func TestAnswerISOPath(t *testing.T) {
	if got := answerISOPath("/w/disk-1/boot.img"); got != "/w/disk-1/boot-answer.iso" {
		t.Fatal(got)
	}
}
