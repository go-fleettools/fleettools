package parityrepo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// floaty makes a repository a floating-point one (FloatMin words).
const floaty = "Works on float64 and float32 with FMA, checked against NumPy and SciPy to within 1 ULP.\n"

const contract = "## Accuracy contract\n\n" +
	"Bound: relative RMS error <= c*eps*log2(n), c measured per function\n" +
	"Oracle: double-double (math/big for small n)\n" +
	"Exact: Options.Exact keeps the bit-exact arithmetic\n"

const boundTest = "package p\n\nimport \"testing\"\n\nfunc TestAccuracyBoundFFT(t *testing.T) {}\n"

// repo writes files (path -> content) under a fresh directory.
func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, c := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func check(t *testing.T, files map[string]string) *Report {
	t.Helper()
	r, err := Check(repo(t, files))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNoStatementPasses(t *testing.T) {
	r := check(t, map[string]string{"README.md": floaty + "A fast FFT.\n"})
	if r.Verdict != Pass || !strings.Contains(r.Reason, "no bit-identity") {
		t.Errorf("%+v", r)
	}
}

func TestStatementWithoutBoundFails(t *testing.T) {
	r := check(t, map[string]string{"README.md": floaty + "\nOutput is bit-identical to the reference.\n"})
	if r.Verdict != Fail || len(r.Statements) != 1 || r.Statements[0].Line != 3 || r.Statements[0].File != "README.md" {
		t.Errorf("%+v", r)
	}
}

// TestMutationRemovingTheBoundMarkerFails is the mutation the rule asks for:
// the passing fixture, with its Bound line removed, fails.
func TestMutationRemovingTheBoundMarkerFails(t *testing.T) {
	passing := map[string]string{
		"README.md":   floaty + "\nOutput is bit-identical to the reference in exact mode.\nThe kernels are bit-for-bit equal to the oracle.\n\n" + contract,
		"fft_test.go": boundTest,
	}
	if r := check(t, passing); r.Verdict != Pass || !r.Marker.Complete() {
		t.Fatalf("the fixture must pass: %+v", r)
	}
	mutated := map[string]string{
		"README.md":   strings.Replace(passing["README.md"], "Bound: relative RMS error <= c*eps*log2(n), c measured per function\n", "", 1),
		"fft_test.go": boundTest,
	}
	r := check(t, mutated)
	if r.Verdict != Fail || r.Marker.Bound || len(r.Statements) == 0 {
		t.Errorf("without the Bound line it must FAIL: %+v", r)
	}
}

func TestEachPartOfTheMarkerIsRequired(t *testing.T) {
	stmt := floaty + "\nThe output is bit-identical to the reference.\n\n"
	for name, files := range map[string]map[string]string{
		"no oracle":  {"README.md": stmt + strings.Replace(contract, "Oracle: double-double (math/big for small n)\n", "", 1), "a_test.go": boundTest},
		"no exact":   {"README.md": stmt + strings.Replace(contract, "Exact: Options.Exact keeps the bit-exact arithmetic\n", "", 1), "a_test.go": boundTest},
		"no test":    {"README.md": stmt + contract},
		"wrong test": {"README.md": stmt + contract, "a_test.go": "package p\n\nfunc TestOther(t *testing.T) {}\n"},
	} {
		if r := check(t, files); r.Verdict != Fail {
			t.Errorf("%s: %+v", name, r)
		}
	}
}

func TestPythonTestConvention(t *testing.T) {
	r := check(t, map[string]string{
		"README.md":       floaty + "\nBit-identical to the reference.\n\n" + contract,
		"tests/test_a.py": "def test_accuracy_bound_fft():\n    pass\n",
	})
	if r.Verdict != Pass || r.Marker.Test != filepath.Join("tests", "test_a.py") {
		t.Errorf("%+v", r)
	}
	r = check(t, map[string]string{
		"README.md":    floaty + "\nBit-identical to the reference.\n\n" + contract,
		"a_test.py":    "def test_accuracy_bound():\n    pass\n",
		"b_test.go":    boundTest,
		"c_test.go":    boundTest,
		"vendor/x.go":  "package x\n",
		"notes.txt":    "bit-identical float64 float64 float64 float64 float64",
		"sub/other.md": "bit-identical float64 float64 float64 float64 float64",
	})
	if r.Verdict != Pass {
		t.Errorf("%+v", r)
	}
}

func TestExactDomainPasses(t *testing.T) {
	r := check(t, map[string]string{"README.md": floaty + "\nThe digest is bit-identical to the reference.\n\n## Accuracy contract\n\nDomain: exact - results are integers\n"})
	if r.Verdict != Pass || !r.Marker.ExactDomain || !strings.Contains(r.Reason, "Domain: exact") {
		t.Errorf("%+v", r)
	}
	r = check(t, map[string]string{"README.md": floaty + "\nThe digest is bit-identical to the reference.\n\n## Accuracy contract\n\nDomain: approximate\n"})
	if r.Verdict != Fail {
		t.Errorf("a Domain other than exact is not a waiver: %+v", r)
	}
}

func TestIntegerRepositoriesAreNotFloatingPoint(t *testing.T) {
	r := check(t, map[string]string{"README.md": "Output is byte-identical to the standard library. Uses float64 once.\n"})
	if r.Verdict != Pass || len(r.Statements) != 0 || r.FloatCues != 1 {
		t.Errorf("%+v", r)
	}
}

func TestNegatedAndOptionStatementsAreNotContracts(t *testing.T) {
	r := check(t, map[string]string{"README.md": floaty + `
Bit identity is not a goal.
Bit-exact mode is opt-in.
We never promise bit-for-bit results.
The option Exact gives bit-identical output.
It was a step: bit-identical to the reference while porting.
`})
	if r.Verdict != Pass {
		t.Errorf("%+v", r)
	}
}

func TestDocsAndGodocAreSurfaces(t *testing.T) {
	r := check(t, map[string]string{
		"docs/design.md": floaty + "\nThe kernels are bit-identical to the oracle.\n",
	})
	if r.Verdict != Fail || r.Statements[0].File != filepath.Join("docs", "design.md") {
		t.Errorf("docs/: %+v", r)
	}
	r = check(t, map[string]string{
		"a.go": "// Package p is bit-for-bit identical to the reference.\n" +
			"// " + strings.TrimSpace(floaty) + "\npackage p\n\n/* block\n   comment bit-identical too float64 */\nvar s = \"// not a comment bit-identical\"\n",
	})
	if r.Verdict != Fail || len(r.Statements) < 1 || r.Statements[0].Line != 1 {
		t.Errorf("godoc: %+v", r)
	}
	// A _test.go file and a non-doc markdown file are not surfaces.
	r = check(t, map[string]string{
		"a_test.go": "// bit-identical float64 float64 float64 float64 float64\npackage p\n",
		"NOTES.md":  "bit-identical float64 float64 float64 float64 float64\n",
	})
	if r.Verdict != Pass {
		t.Errorf("%+v", r)
	}
}

func TestMarkerInGodoc(t *testing.T) {
	r := check(t, map[string]string{
		"doc.go": "// Package p does things.\n//\n// " + strings.TrimSpace(floaty) + "\n// Bit-identical to the reference.\n//\n" +
			"// # Accuracy contract\n//\n// Bound: 4 ulp\n// Oracle: double-double\n// Exact: Options.Exact\n//\n// # Other\n//\npackage p\n",
		"p_test.go": boundTest,
	})
	if r.Verdict != Pass || r.Marker.File != "doc.go" {
		t.Errorf("%+v", r)
	}
	r = check(t, map[string]string{
		"doc.go":    "/*\nPackage p. " + strings.TrimSpace(floaty) + "\n\n# Accuracy contract\n\nBound: 4 ulp\nOracle: double-double\nExact: Options.Exact\n*/\npackage p\n",
		"p_test.go": boundTest,
	})
	if r.Verdict != Pass {
		t.Errorf("block comment: %+v", r)
	}
}

func TestSectionBoundaries(t *testing.T) {
	// The section ends at the next heading of the same or a higher level;
	// a sub-heading and a fenced "#" line stay inside it.
	text := "# Top\n\nIntro.\n\n## Accuracy contract\n\nBound: x\n\n### Detail\n\n```\n# not a heading\nBound: fenced\n```\n\nOracle: y\n\n## After\n\nOutput is bit-identical.\n"
	sec, rest := section(text)
	if len(sec) == 0 || !strings.Contains(strings.Join(sec, "\n"), "Oracle: y") || strings.Contains(strings.Join(sec, "\n"), "bit-identical") {
		t.Errorf("section: %q", sec)
	}
	if !strings.Contains(rest, "bit-identical") || strings.Contains(rest, "Oracle") || strings.Count(rest, "\n") != strings.Count(text, "\n") {
		t.Errorf("rest: %q", rest)
	}
	if sec, rest := section("no section here\n"); sec != nil || rest != "no section here\n" {
		t.Errorf("absent: %q %q", sec, rest)
	}
	// A section closed by a higher-level heading.
	sec, _ = section("## Accuracy contract\nBound: x\n# Up\nBound: y\n")
	if strings.Contains(strings.Join(sec, "\n"), "Bound: y") {
		t.Errorf("a higher heading did not end the section: %q", sec)
	}
	// Statements inside the section are not statements.
	r := check(t, map[string]string{"README.md": floaty + "\n## Accuracy contract\n\nBound: none yet, bit-identical is the contract\n"})
	if r.Verdict != Pass {
		t.Errorf("%+v", r)
	}
}

func TestBestMarkerWins(t *testing.T) {
	r := check(t, map[string]string{
		"README.md":    floaty + "\nBit-identical.\n\n## Accuracy contract\n\nBound: x\n",
		"docs/more.md": "## Accuracy contract\n\nBound: x\nOracle: y\nExact: z\n",
		"docs/less.md": "## Accuracy contract\n\nBound: x\n",
		"t_test.go":    boundTest,
	})
	if r.Verdict != Pass || r.Marker.File != filepath.Join("docs", "more.md") {
		t.Errorf("%+v", r)
	}
}

func TestUnreadIsNotPass(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	root := repo(t, map[string]string{"README.md": floaty + "\nBit-identical.\n", "docs/a.md": "x\n"})
	if err := os.Chmod(filepath.Join(root, "README.md"), 0); err != nil {
		t.Fatal(err)
	}
	r, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != Unread || len(r.Unread) != 1 || !strings.HasPrefix(r.Unread[0], "README.md") {
		t.Errorf("%+v", r)
	}
	// An unreadable directory is UNREAD too, not skipped.
	root = repo(t, map[string]string{"README.md": "Fine.\n", "docs/a.md": "x\n"})
	if err := os.Chmod(filepath.Join(root, "docs"), 0); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(root, "docs")
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	if r, err = Check(root); err != nil || r.Verdict != Unread {
		t.Errorf("%+v %v", r, err)
	}
	// A complete marker answers PASS whatever else could not be read.
	root = repo(t, map[string]string{"README.md": floaty + contract, "t_test.go": boundTest, "docs/a.md": "x\n"})
	os.Chmod(filepath.Join(root, "docs", "a.md"), 0)
	if r, err = Check(root); err != nil || r.Verdict != Pass || len(r.Unread) != 1 {
		t.Errorf("%+v %v", r, err)
	}
}

func TestOversizeFileIsUnread(t *testing.T) {
	root := repo(t, map[string]string{"README.md": "x"})
	if err := os.WriteFile(filepath.Join(root, "docs.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "big.md"), []byte(strings.Repeat("a", MaxFile+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Check(root)
	if err != nil || r.Verdict != Unread || !strings.Contains(r.Unread[0], "over") {
		t.Errorf("%+v %v", r, err)
	}
}

func TestSymlinksAreNotFollowed(t *testing.T) {
	root := repo(t, map[string]string{"README.md": "Fine.\n"})
	outside := repo(t, map[string]string{"README.md": floaty + "\nBit-identical.\n"})
	if err := os.Symlink(filepath.Join(outside, "README.md"), filepath.Join(root, "README.rst")); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "docs")); err != nil {
		t.Skip(err)
	}
	r, err := Check(root)
	if err != nil || r.Verdict != Pass || r.Files != 1 {
		t.Errorf("%+v %v", r, err)
	}
}

func TestReadBoundedRefusesWhatCannotBeRead(t *testing.T) {
	if _, err := readBounded(t.TempDir()); err == nil {
		t.Error("a directory is not a file")
	}
	if _, err := readBounded(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("an absent file")
	}
}

func TestBadRoot(t *testing.T) {
	if _, err := Check(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("an absent clone must be an error")
	}
	f := filepath.Join(t.TempDir(), "file")
	os.WriteFile(f, []byte("x"), 0o644)
	if _, err := Check(f); err == nil {
		t.Error("a file is not a clone")
	}
}

func TestClassify(t *testing.T) {
	for p, want := range map[string]kindOf{
		"README.md": prose, "readme": prose, "sub/README.rst": prose, "docs/a.mdx": prose, "docs/x/a.txt": prose,
		"docs/a.png": none, "NOTES.md": none, "a.go": goSource, "a_test.go": testFile, "test_a.py": testFile, "a_test.py": testFile,
		"a.py": none, "README.png": none,
	} {
		if got := classify(p); got != want {
			t.Errorf("%s: %d, want %d", p, got, want)
		}
	}
}

func TestGoCommentsKeepLines(t *testing.T) {
	src := "package p\n\n// one\nvar x = 1 // two\n/* three\n four */\nvar s = `// not`\n"
	got := strings.Split(goComments([]byte(src)), "\n")
	if len(got) < 6 || got[2] != "one" || got[3] != "two" || got[4] != "three" || got[5] != "four" {
		t.Errorf("%q", got)
	}
}
