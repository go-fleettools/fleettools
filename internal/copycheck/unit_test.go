package copycheck

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonNormalisesSpelling(t *testing.T) {
	same := [][]string{
		{"16", "0x10", "0X10", "020", "0b10000", "0o20", "16.0", "16.0f", "1.6e1", "1_6", "16u", "16UL", "0x10u"},
		{"0", "0x0", "0.0", "0.f", "00"},
		{"0.5", "0.5f", ".5", "5e-1", "0.50", "0.5L"},
		{"0.70710678", "0.7071068f", "0.70710678118654752"},
	}
	for _, g := range same {
		want, ok := Canon(g[0])
		if !ok {
			t.Fatalf("%q not a number", g[0])
		}
		for _, s := range g[1:] {
			if got, ok := Canon(s); !ok || got != want {
				t.Errorf("Canon(%q) = %q,%v; want %q (same as %q)", s, got, ok, want, g[0])
			}
		}
	}
	for _, bad := range []string{"", "019", "0x", "1f", "3i", "0b2", "e5", "1.2.3", "0xg"} {
		if c, ok := Canon(bad); ok {
			t.Errorf("Canon(%q) = %q, want not a number", bad, c)
		}
	}
	if a, _ := Canon("1"); a == "" {
		t.Fatal()
	}
	if x, _ := Canon("1"); func() string { y, _ := Canon("1.001"); return y }() == x {
		t.Error("1 and 1.0000001 are different numbers")
	}
	if c, ok := Canon("18446744073709551616"); !ok || !strings.HasPrefix(c, "big:") {
		t.Errorf("wider than 64 bits keeps its spelling: %q %v", c, ok)
	}
}

func TestLexBasics(t *testing.T) {
	src := "#include <x.h>\n#define N 0x10 /* c */\nint a[] = {1, -2.5f, 3}; // tail\ns := \"a\\\"b\" + `raw\nline`\nx->y\n"
	var got []string
	for _, tk := range Lex([]byte(src)) {
		got = append(got, tk.Text)
	}
	want := "N 0x10 int a [ ] = { 1 , - 2.5f , 3 } ; s := \"\" + `` x -> y"
	if strings.Join(got, " ") != want {
		t.Errorf("got  %q\nwant %q", strings.Join(got, " "), want)
	}
	toks := Lex([]byte(src))
	if last := toks[len(toks)-1]; last.Line != 6 {
		t.Errorf("line of the last token = %d, want 6 (raw string spans a line)", last.Line)
	}
}

func TestNumRunsSegmentation(t *testing.T) {
	src := "t[] = {1, -2, 3, 4, 5, 6, 7, 8}; u = 1 - 2 - 3 - 4 - 5 - 6 - 7 - 8; v = {FRAC_CONST(0.5), COEF(1.0), 2, 3, 4, 5, 6, 7};"
	runs := NumRuns(Lex([]byte(src)), 8)
	if len(runs) != 2 {
		t.Fatalf("want the table and the macro table, not the arithmetic: %+v", runs)
	}
	if runs[0].Vals[1] != "-2" {
		t.Errorf("unary minus: %v", runs[0].Vals)
	}
	if runs[1].Vals[0] != "0.5" || runs[1].Vals[1] != "1" {
		t.Errorf("macro-wrapped values: %v", runs[1].Vals)
	}
}

func TestWinnowGuarantee(t *testing.T) {
	// any shared substring of at least k+w-1 is found, wherever it sits
	k, w := 5, 4
	shared := strings.Fields("a b c d e f g h")
	if len(shared) < k+w-1 {
		t.Fatal("test setup")
	}
	for pre := 0; pre < 9; pre++ {
		x := append(strings.Fields(strings.Repeat("p ", pre)), shared...)
		y := append(append(strings.Fields("q r s"), shared...), "z", "y")
		hx, hy := map[uint64]bool{}, map[uint64]bool{}
		for _, f := range Winnow(x, k, w, nil) {
			hx[f.Hash] = true
		}
		hit := false
		for _, f := range Winnow(y, k, w, nil) {
			hy[f.Hash] = true
			hit = hit || hx[f.Hash]
		}
		if !hit {
			t.Errorf("pre=%d: shared run of %d not detected", pre, len(shared))
		}
	}
}

func TestInformativeRefusesCoincidentalProgressions(t *testing.T) {
	opus := strings.Fields("0 2 4 6 8 10 12 14 16 20 24 28 32 40 48 56")
	if informative(opus, 4) {
		t.Error("a 16-number band-edge ramp is not evidence")
	}
	rnd := strings.Fields("3 17 5 99 42 8 61 23 77 14 90 31 6 52 85 12")
	if !informative(rnd, 4) {
		t.Error("an irregular table is evidence")
	}
	if informative(strings.Fields("7 7 7 7 7 7 7 7 7 7 7 7 7 7 7 7"), 4) {
		t.Error("a constant is not a table")
	}
}

func TestShapeIsLanguageNeutral(t *testing.T) {
	c := Shape(Lex([]byte("static int f(const uint8_t *p, int n) { for (i = 0; i < n; i++) { s->sample_rate = p[i]; } }")))
	g := Shape(Lex([]byte("func f(p []byte, n int) { for i := 0; i < n; i++ { s.sampleRate = int(p[i]) } }")))
	txt := func(x []ST) string {
		var o []string
		for _, t := range x {
			o = append(o, t.Text)
		}
		return strings.Join(o, " ")
	}
	t.Logf("c: %s\ng: %s", txt(c), txt(g))
	for _, w := range []string{"loop", "samplerate", "s . samplerate"} {
		if !strings.Contains(txt(c), w) || !strings.Contains(txt(g), w) {
			t.Errorf("both must contain %q", w)
		}
	}
}

func TestLicenceClasses(t *testing.T) {
	cases := map[string]Class{
		"MIT": Permissive, "Apache-2.0": Permissive, "BSD-3-Clause": Permissive, "ISC": Permissive, "0BSD": Permissive, "Zlib": Permissive,
		"CC0-1.0": Permissive, "Unlicense": Permissive,
		"GPL-2.0-only": Copyleft, "GPL-2.0-or-later": Copyleft, "GPL-3.0+": Copyleft, "LGPL-2.1-or-later": Copyleft,
		"LGPL-3.0-only": Copyleft, "AGPL-3.0-or-later": Copyleft, "EUPL-1.2": Copyleft, "CC-BY-SA-4.0": Copyleft,
		"MPL-2.0": FileCopyleft, "EPL-2.0": FileCopyleft, "CDDL-1.0": FileCopyleft,
		"MIT OR GPL-2.0-only":                       Permissive,
		"MIT AND GPL-2.0-only":                      Copyleft,
		"(MIT OR Apache-2.0) AND ISC":               Permissive,
		"GPL-2.0-only WITH Classpath-exception-2.0": Copyleft,
		"LicenseRef-3GPP-copyright":                 Unknown,
		"LicenseRef-FDK-AAC":                        Unknown,
		"":                                          Unknown, "MIT OR": Unknown, "(MIT": Unknown, "FooBar-1.0": Unknown,
		"mit": Permissive, "gpl-2.0-only": Copyleft,
	}
	for in, want := range cases {
		if got := ClassOf(in); got != want {
			t.Errorf("ClassOf(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPolicyOnUnknownLicenceDependsOnRole(t *testing.T) {
	mk := func(role string) *Manifest {
		return &Manifest{References: []Reference{{Name: "x", License: "LicenseRef-3GPP-copyright", Role: role}}}
	}
	for _, role := range []string{RoleStudied, RoleOracle} {
		if p := CheckPolicy(mk(role)); len(p) != 0 {
			t.Errorf("%s of an unlisted licence takes nothing: %+v", role, p)
		}
	}
	for _, role := range []string{RoleTablesFrom, RolePortedFrom} {
		if p := CheckPolicy(mk(role)); len(p) != 1 {
			t.Errorf("%s from an unlisted licence must be refused: %+v", role, p)
		}
	}
}

func goodManifest() string {
	return "version: 1\nreferences:\n  - name: a\n    url: https://h/a-v1.tar.gz\n    pin: v1\n    tree_sha256: " + strings.Repeat("0", 64) + "\n    license: MIT\n    role: studied\n"
}

func TestManifestValidation(t *testing.T) {
	if _, err := ParseManifest([]byte(goodManifest())); err != nil {
		t.Fatal(err)
	}
	bad := map[string]string{
		"role":           strings.Replace(goodManifest(), "role: studied", "role: copied", 1),
		"http":           strings.Replace(goodManifest(), "https://", "http://", 1),
		"pin missing":    strings.Replace(goodManifest(), "    pin: v1\n", "", 1),
		"branch pin":     strings.Replace(strings.Replace(goodManifest(), "pin: v1", "pin: main", 1), "a-v1", "a-main", 1),
		"pin not in url": strings.Replace(goodManifest(), "pin: v1", "pin: v2", 1),
		"hash":           strings.Replace(goodManifest(), strings.Repeat("0", 64), "abc", 1),
		"no licence":     strings.Replace(goodManifest(), "license: MIT", "license: ''", 1),
		"unknown key":    goodManifest() + "    tree_sha: x\n",
		"version":        strings.Replace(goodManifest(), "version: 1", "version: 2", 1),
		"no refs":        "version: 1\nreferences: []\n",
		"path ..":        goodManifest() + "    paths: ['../x']\n",
		"dup":            goodManifest() + goodManifest()[strings.Index(goodManifest(), "  - name"):],
	}
	for name, y := range bad {
		if _, err := ParseManifest([]byte(y)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// ---- fetching: the archive is untrusted ----

func lim() Limits { return DefaultLimits() }

func extractBytes(t *testing.T, arch []byte, paths []string, l Limits) error {
	t.Helper()
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	if err := os.WriteFile(a, arch, 0o600); err != nil {
		t.Fatal(err)
	}
	return Extract(a, filepath.Join(dir, "out"), paths, 1, l)
}

func rawTar(t *testing.T, hs []tar.Header, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range hs {
		h := h
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(body))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write(body)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestExtractRefusesHostileArchives(t *testing.T) {
	reg := func(n string) tar.Header { return tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: 0o644} }
	cases := map[string][]tar.Header{
		"traversal":      {reg("r/../../evil.c")},
		"traversal deep": {reg("r/a/b/../../../../evil.c")},
		"absolute":       {reg("/etc/evil.c")},
		"symlink":        {{Name: "r/l.c", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}},
		"hardlink":       {{Name: "r/h.c", Typeflag: tar.TypeLink, Linkname: "r/x.c"}},
		"device":         {{Name: "r/d.c", Typeflag: tar.TypeChar}},
		"duplicate":      {reg("r/a.c"), reg("r/a.c")},
	}
	for name, hs := range cases {
		if err := extractBytes(t, rawTar(t, hs, []byte("int x;")), nil, lim()); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// a hostile name OUTSIDE the selected paths is still refused
	if err := extractBytes(t, rawTar(t, []tar.Header{reg("r/ok/a.c"), reg("r/../x.c")}, []byte("int x;")), []string{"ok"}, lim()); err == nil {
		t.Error("traversal outside the selected paths was ignored")
	}
}

func TestExtractCaps(t *testing.T) {
	reg := func(n string) tar.Header { return tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: 0o644} }
	body := bytes.Repeat([]byte("x"), 1000)
	arch := rawTar(t, []tar.Header{reg("r/a.c"), reg("r/b.c"), reg("r/c.c")}, body)
	l := lim()
	l.Files = 2
	if err := extractBytes(t, arch, nil, l); err == nil || !strings.Contains(err.Error(), "files") {
		t.Errorf("file count cap: %v", err)
	}
	l = lim()
	l.FileBytes = 500
	if err := extractBytes(t, arch, nil, l); err == nil {
		t.Error("per-file cap")
	}
	l = lim()
	l.TotalBytes = 2500
	if err := extractBytes(t, arch, nil, l); err == nil {
		t.Error("total cap")
	}
	l = lim()
	l.Entries = 2
	if err := extractBytes(t, arch, nil, l); err == nil {
		t.Error("entry cap")
	}
	// a bomb: the stream decompresses to far more than the limit, in entries we do not keep
	big := bytes.Repeat([]byte{0}, 4<<20)
	bomb := rawTar(t, []tar.Header{reg("r/skip.bin"), reg("r/skip2.bin")}, big)
	l = lim()
	l.StreamBytes = 1 << 20
	if err := extractBytes(t, bomb, []string{"nothing-here"}, l); err == nil || !strings.Contains(err.Error(), "bomb") {
		t.Errorf("decompression bomb: %v", err)
	}
}

func TestExtractZipAndNothingToKeep(t *testing.T) {
	z := zipOf(t, map[string][]byte{"r/lib/a.c": []byte("int a;"), "r/lib/readme.txt": []byte("x"), "r/other/b.c": []byte("int b;")})
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	os.WriteFile(a, z, 0o600)
	out := filepath.Join(dir, "out")
	if err := Extract(a, out, []string{"lib"}, 1, lim()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "lib/a.c")); err != nil {
		t.Error("source under the path must be kept")
	}
	if _, err := os.Stat(filepath.Join(out, "lib/readme.txt")); err == nil {
		t.Error("only source files are kept")
	}
	if _, err := os.Stat(filepath.Join(out, "other/b.c")); err == nil {
		t.Error("files outside the paths are kept")
	}
	if err := Extract(a, filepath.Join(dir, "out2"), []string{"wrong"}, 1, lim()); err == nil {
		t.Error("wrong paths keep nothing: that is an error, not a clean reference")
	}
	os.WriteFile(a, []byte("not an archive at all"), 0o600)
	if err := Extract(a, filepath.Join(dir, "out3"), nil, 1, lim()); err == nil {
		t.Error("garbage accepted")
	}
}

func TestFetchVerifiesTheHash(t *testing.T) {
	files := map[string][]byte{"r-v1/lib/a.c": []byte("int a;\n")}
	srv, cl := serve(t, map[string][]byte{"/r-v1.tar.gz": tarGz(t, files)})
	f := newFetcher(t, cl)
	ref := Reference{Name: "r", URL: srv.URL + "/r-v1.tar.gz", Pin: "v1", License: "MIT", Role: RoleStudied}
	ref = pinned(t, f, ref)

	// served again, verified: ok
	if _, _, err := f.Fetch(ref); err != nil {
		t.Fatal(err)
	}
	// a cache that was tampered with is noticed and replaced by a fresh download
	dir, _, _ := f.Fetch(ref)
	if err := os.WriteFile(filepath.Join(dir, "lib/a.c"), []byte("int evil;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.Fetch(ref); err != nil {
		t.Fatalf("tampered cache must be refetched: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "lib/a.c"))
	if string(b) != "int a;\n" {
		t.Errorf("cache still holds %q", b)
	}
	// the archive at the URL changes: refuse, name both hashes
	srv2, cl2 := serve(t, map[string][]byte{"/r-v1.tar.gz": tarGz(t, map[string][]byte{"r-v1/lib/a.c": []byte("int changed;\n")})})
	f2 := newFetcher(t, cl2)
	ref2 := ref
	ref2.URL = srv2.URL + "/r-v1.tar.gz"
	_, _, err := f2.Fetch(ref2)
	if err == nil || !strings.Contains(err.Error(), "mismatch") || !strings.Contains(err.Error(), ref.Tree) {
		t.Fatalf("want a mismatch naming the expected hash, got %v", err)
	}
}

func TestDownloadCap(t *testing.T) {
	big := tarGz(t, map[string][]byte{"r-v1/a.c": bytes.Repeat([]byte("int x;\n"), 1000)})
	srv, cl := serve(t, map[string][]byte{"/r-v1.tar.gz": big})
	f := newFetcher(t, cl)
	f.Limits.ArchiveBytes = 100
	_, _, err := f.Fetch(Reference{Name: "r", URL: srv.URL + "/r-v1.tar.gz", Pin: "v1"})
	if err == nil {
		t.Fatal("download over the cap accepted")
	}
}

func TestCacheDirRefusesAGitTree(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckCacheDir(filepath.Join(root, "sub", "cache")); err == nil {
		t.Error("a cache inside a git working tree must be refused")
	}
	if _, err := CheckCacheDir(filepath.Join(t.TempDir(), "cache")); err != nil {
		t.Error(err)
	}
}

// ---- fuzzing: the lexer reads bytes nobody here controls ----

func FuzzLex(f *testing.F) {
	for _, s := range []string{
		"", "\"", "`", "'", "/*", "//", "#define", "#define X \\\n 1", "0x", "1e+", "0x1p-", ".5.5.5",
		"a->b := c <<= 1 ... &^= 2", "\\\n\\\r\n", "\xff\xfe\x00", "'\\", "\"\\", "/* */ */", "int a[] = {1, 2, 3};",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		toks := Lex(b)
		line := 1
		for _, tk := range toks {
			if tk.Line < line {
				t.Fatalf("lines go backwards: %d after %d", tk.Line, line)
			}
			line = tk.Line
			if tk.Text == "" {
				t.Fatal("empty token")
			}
			if tk.Kind == KNum {
				Canon(tk.Text)
			}
		}
		if len(toks) > len(b) {
			t.Fatalf("%d tokens from %d bytes", len(toks), len(b))
		}
		Shape(toks)
		NumRuns(toks, 1)
		NewShapeFile("f", b, 4, 2)
		NewTableFile("f", b, 8, 4)
	})
}

func FuzzCanon(f *testing.F) {
	for _, s := range []string{"0", "0x10", "1e5", "1_000", "0b1", "0o7", "1.f", ".5", "0x1p3", "99999999999999999999", "1e999", "0x", "-"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		c, ok := Canon(s)
		if ok && c == "" {
			t.Fatalf("Canon(%q) ok with an empty value", s)
		}
		if ok {
			if c2, ok2 := Canon(c); ok2 && c2 != c && !strings.HasPrefix(c, "big:") {
				t.Fatalf("Canon not idempotent: %q -> %q -> %q", s, c, c2)
			}
		}
	})
}

func FuzzParseManifest(f *testing.F) {
	f.Add([]byte(goodManifest()))
	f.Add([]byte("version: 1\nreferences: [{}]\n"))
	f.Add([]byte("\x00"))
	f.Fuzz(func(t *testing.T, b []byte) { ParseManifest(b) })
}

func FuzzClassOf(f *testing.F) {
	for _, s := range []string{"MIT", "(MIT OR GPL-2.0-only) AND ISC", "((", "))", "OR AND", "A WITH", "(((((MIT)))))"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) { ClassOf(s) })
}
