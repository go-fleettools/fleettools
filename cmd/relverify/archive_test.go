package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// goExe is a real Go binary: this test's own executable. It carries build
// information, no module version and, built by go test, no vcs stamp -- so a
// correct check refuses it, which is the point of having a real one.
func goExe(t *testing.T) []byte {
	t.Helper()
	if testing.Short() {
		t.Skip("reads the test binary")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type entry struct {
	name string
	data []byte
}

func writeTarGz(t *testing.T, path string, members ...entry) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, m := range members {
		if err := tw.WriteHeader(&tar.Header{Name: m.name, Mode: 0o755, Size: int64(len(m.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(m.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeZip(t *testing.T, path string, members ...entry) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, m := range members {
		w, err := zw.Create(m.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(m.data); err != nil {
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

// release lays out a release directory: the files, and a SHA256SUMS that
// lists every one of them correctly, so that what a test sees is the archive
// check alone.
func release(t *testing.T, build func(dir string)) string {
	t.Helper()
	dir := t.TempDir()
	build(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var sums strings.Builder
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		sums.WriteString(sumOf(b) + "  " + e.Name() + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeGH(t, func([]string) ([]byte, error) { return nil, nil })
	return dir
}

// ⛔ The defect: v0.15.0 called a goreleaser release "verified" from the
// archives' sums and attestations, without reading the program inside. The
// Go binary in each archive is now held to the build checks, and named.
func TestTheGoBinaryInsideAnArchiveIsHeldToTheBuildChecks(t *testing.T) {
	exe := goExe(t)
	dir := release(t, func(dir string) {
		writeTarGz(t, filepath.Join(dir, "tool_v1.2.3_linux_amd64.tar.gz"),
			entry{"LICENSE", []byte("BSD")}, entry{"tool", exe})
		writeZip(t, filepath.Join(dir, "tool_v1.2.3_windows_amd64.zip"),
			entry{"README.md", []byte("# tool")}, entry{"tool.exe", exe})
	})
	var out bytes.Buffer
	files, err := checkFiles(&out, "o/r", "v1.2.3", commit, dir, skipScan)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("%d files: %+v", len(files), files)
	}
	for _, f := range files {
		if !f.goBinary || len(f.inner) != 1 || f.ok() {
			t.Errorf("%s: goBinary=%v inner=%v ok=%v; want the binary inside found and refused", f.name, f.goBinary, f.inner, f.ok())
			continue
		}
		if !strings.Contains(f.build, f.inner[0]+": build info: module version") {
			t.Errorf("%s: %q does not name the member and its version", f.name, f.build)
		}
	}
}

func TestAnArchiveWithNoGoBinaryIsChecked(t *testing.T) {
	dir := release(t, func(dir string) {
		writeTarGz(t, filepath.Join(dir, "docs.tar.gz"), entry{"index.html", []byte("<html>")})
	})
	var out bytes.Buffer
	files, err := checkFiles(&out, "o/r", "v1.2.3", commit, dir, skipScan)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || !files[0].ok() || files[0].goBinary {
		t.Errorf("%+v", files)
	}
	if !strings.Contains(out.String(), "no Go binary inside") {
		t.Errorf("the report does not say the archive holds no Go binary:\n%s", out.String())
	}
}

// A file named as an archive that is not one fails: nobody can install it.
func TestAnArchiveThatCannotBeReadFails(t *testing.T) {
	dir := release(t, func(dir string) {
		if err := os.WriteFile(filepath.Join(dir, "tool.tar.gz"), []byte("not gzip"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "tool.zip"), []byte("not zip"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	files, err := checkFiles(&bytes.Buffer{}, "o/r", "v1.2.3", commit, dir, skipScan)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.ok() || !strings.HasPrefix(f.build, "archive: ") {
			t.Errorf("%s: ok=%v build=%q", f.name, f.ok(), f.build)
		}
	}
}

// Members past the limit are refused, by the tar header and by the bytes a
// zip entry actually expands to.
func TestAnArchiveBuiltToExhaustTheReaderIsRefused(t *testing.T) {
	old := maxMember
	maxMember = 1024
	t.Cleanup(func() { maxMember = old })
	big := bytes.Repeat([]byte{0}, 4096) // compresses to almost nothing
	dir := t.TempDir()
	writeTarGz(t, filepath.Join(dir, "a.tar.gz"), entry{"big", big})
	writeZip(t, filepath.Join(dir, "a.zip"), entry{"big", big})
	for _, name := range []string{"a.tar.gz", "a.zip"} {
		if _, err := goBinariesIn(filepath.Join(dir, name)); err == nil || !strings.Contains(err.Error(), "1024") {
			t.Errorf("%s: %v, want a refusal naming the limit", name, err)
		}
	}
}

func TestIsHost(t *testing.T) {
	os, arch := runtime.GOOS, runtime.GOARCH
	for name, want := range map[string]bool{
		"tool-" + os + "-" + arch:                    true,
		"tool-" + os + "-" + arch + ".exe":           true,
		"tool_v1.2.3_" + os + "_" + arch + ".tar.gz": true,
		"tool_v1.2.3_" + os + "_" + arch + "_v2.zip": true,
		"tool_" + os + "_" + arch + "v3.tar.gz":      false, // amd64v3 is not amd64
		"tool_" + os + "_" + arch + "64.tar.gz":      false, // arm64 is not arm
		"tool_" + os + "x_" + arch + ".tar.gz":       false,
		"tool_" + arch + "_" + os + ".tar.gz":        false,
		"SHA256SUMS":                                 false,
	} {
		if got := isHost(name); got != want {
			t.Errorf("isHost(%q) = %v, want %v", name, got, want)
		}
	}
}

// ⛔ Two runs on one release: the second finishing must not take the files
// out from under the first, which is what sharing one directory did.
func TestTwoRunsOnOneReleaseDoNotShareFiles(t *testing.T) {
	final := filepath.Join(t.TempDir(), "o", "r", "v1.2.3")
	first, err := runDir(final)
	if err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(first, "tool.tar.gz")
	if err := os.WriteFile(asset, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A second run starts, downloads and finishes while the first checks.
	second, err := runDir(final)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("both runs were given the same directory")
	}
	if err := os.WriteFile(filepath.Join(second, "tool.tar.gz"), []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := publishRun(second, final); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(asset); err != nil || string(b) != "first" {
		t.Errorf("the first run's file after the second finished: %q, %v", b, err)
	}
	// And the first finishing replaces final with its own complete set.
	if err := publishRun(first, final); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(final, "tool.tar.gz")); err != nil || string(b) != "first" {
		t.Errorf("final after both: %q, %v", b, err)
	}
	if left, _ := filepath.Glob(final + ".run-*"); len(left) != 0 {
		t.Errorf("run directories left behind: %v", left)
	}
}

// The host binary read out of an archive lands under its base name, in the
// download's own .host directory, whatever its name in the archive says.
func TestTheHostBinaryFromAnArchiveStaysInTheDownload(t *testing.T) {
	exe := goExe(t)
	dir := t.TempDir()
	f := file{
		name:     "tool_v1.2.3_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz",
		listed:   true,
		goBinary: true,
		inner:    []string{"../../escaped"},
		hostName: "../../escaped",
		hostData: exe,
	}
	runHostBinary(&bytes.Buffer{}, dir, "v1.2.3", []file{f})
	if _, err := os.Stat(filepath.Join(dir, ".host", "escaped")); err != nil {
		t.Errorf("not written under .host: %v", err)
	}
	// .host/../../escaped is dir's PARENT: where an unsanitised name lands.
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escaped")); err == nil {
		t.Error("written above the download, where the archive's name pointed")
	}
}
