package fleet_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The magic numbers of the executable formats a Go build can produce. Only
// executables: a PNG or a PDF in testdata is legitimate, a compiled command
// is not, and a test that cannot tell them apart gets switched off.
var executableMagic = []struct {
	name  string
	magic []byte
}{
	{"ELF", []byte{0x7f, 'E', 'L', 'F'}},
	{"Mach-O 64-bit little-endian", []byte{0xcf, 0xfa, 0xed, 0xfe}},
	{"Mach-O 32-bit little-endian", []byte{0xce, 0xfa, 0xed, 0xfe}},
	{"Mach-O big-endian", []byte{0xfe, 0xed, 0xfa, 0xcf}},
	{"Mach-O universal", []byte{0xca, 0xfe, 0xba, 0xbe}},
	{"PE/COFF", []byte{'M', 'Z'}},
	{"WebAssembly", []byte{0x00, 'a', 's', 'm'}},
}

// No compiled binary may be tracked. A 3.8 MB arm64 Mach-O executable --
// `docscan`, from `go build ./cmd/docscan/` in the repository root -- was
// tracked on `main` from #11 until the commit that added this test, because
// there was no .gitignore at all and `git add -A` does not ask.
//
// The .gitignore fixed in the same commit is an allow-list, so it cannot go
// stale at a rename. This test is the other half: it says so for the whole
// tree rather than for the root, and it fails with the path and the format
// rather than with a size.
func TestNoTrackedFileIsACompiledBinary(t *testing.T) {
	root := ".."
	for i := 0; i < 3; i++ {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		root = filepath.Join(root, "..")
	}

	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("git ls-files: %v (not a checkout?)", err)
	}
	names := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")

	var checked int
	var found []string
	for _, n := range names {
		if n == "" {
			continue
		}
		f, err := os.Open(filepath.Join(root, n))
		if err != nil {
			continue // a path tracked but not checked out here
		}
		head := make([]byte, 4)
		k, _ := f.Read(head)
		f.Close()
		checked++
		for _, m := range executableMagic {
			if k >= len(m.magic) && bytes.HasPrefix(head, m.magic) {
				found = append(found, n+" ("+m.name+")")
				break
			}
		}
	}

	// A sweep that could not read reports zero: prove this one read something.
	if checked < 10 {
		t.Fatalf("read %d tracked files, so this test is measuring itself", checked)
	}
	if len(found) > 0 {
		t.Fatalf("%d tracked file(s) are compiled binaries: %v\n"+
			"`go build ./cmd/<name>/` writes ./<name> in the repository root; the\n"+
			"allow-list in .gitignore keeps it out, so this one was added before it\n"+
			"existed or past it with `git add -f`.", len(found), found)
	}
	t.Logf("%d tracked files, none is an executable", checked)
}
