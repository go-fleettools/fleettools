package copycheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ⛔ THE PLANTED POSITIVE AGAINST REAL CODE. The synthetic fixtures show the
// gate fires on code written to be caught; this shows it fires on code nobody
// wrote for the test. At test time the pinned FFmpeg n9.0.2 tree is fetched
// (or taken from the cache, hash-verified either way: a mismatch fails the
// test), one real function and one real table are cut out of it, rewritten
// mechanically from C into Go-ish text, and fed to the gate as "our" source.
// No copyleft text is committed: only the pin, the names of what to cut, and
// the regexps of the rewrite.
//
// There is no skip. A test that skips when its reference is absent passes on
// the machine that has none, which is the failure this gate exists to prevent;
// if the network or the cache is missing the test FAILS, and CI has network.
const (
	ffmpegURL  = "https://github.com/FFmpeg/FFmpeg/archive/946fcce07b6dcd0331c8cc609192aeff5e1924f8.tar.gz"
	ffmpegPin  = "946fcce07b6dcd0331c8cc609192aeff5e1924f8" // tag n9.0.2
	ffmpegTree = "3d63f40bbbc2d28af3d54ed5136f6efe7f442fad246163a30253c5e27d9ac3d1"
)

func realFFmpeg(t *testing.T) (Reference, *Fetcher, string) {
	t.Helper()
	cache, err := DefaultCacheDir()
	if err != nil {
		t.Fatalf("no cache directory, and this test has no skip: %v", err)
	}
	f := &Fetcher{CacheDir: cache, Limits: DefaultLimits()}
	ref := Reference{Name: "ffmpeg-n9.0.2", URL: ffmpegURL, Pin: ffmpegPin, Tree: ffmpegTree,
		License: "LGPL-2.1-or-later", Role: RoleStudied, Paths: []string{"libavcodec/flacdec.c", "libavcodec/mpegaudiodata.c"}}
	dir, _, err := f.Fetch(ref)
	if err != nil {
		t.Fatalf("the pinned FFmpeg could not be fetched and verified (no skip): %v", err)
	}
	return ref, f, dir
}

// cutFunc returns the C function starting at header, braces matched.
func cutFunc(t *testing.T, src, header string) string {
	t.Helper()
	i := strings.Index(src, header)
	if i < 0 {
		t.Fatalf("%q is not in the reference", header)
	}
	j := strings.Index(src[i:], "{")
	depth := 0
	for k := i + j; k < len(src); k++ {
		switch src[k] {
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return src[i : k+1]
			}
		}
	}
	t.Fatal("unbalanced braces")
	return ""
}

var (
	reFor    = regexp.MustCompile(`\bfor \((.*)\) \{`)
	reIf     = regexp.MustCompile(`\bif \((.*)\) \{`)
	reWhile  = regexp.MustCompile(`\bwhile \((.*)\) \{`)
	reTypes  = regexp.MustCompile(`\b(u?int\d+_t|unsigned|signed|const|static|int|long|short|char|float|double)\b\s+`)
	reSemi   = regexp.MustCompile(`;[ \t]*$`)
	reHeader = regexp.MustCompile(`^static int (\w+)\((.*)$`)
)

// cToGo is the mechanical rewrite a hurried porter does by search and replace:
// `->` becomes `.`, loop and condition parentheses go, type words go, the
// semicolons at the ends of lines go, NULL becomes nil, the header becomes func.
func cToGo(c string) string {
	var out []string
	for _, l := range strings.Split(c, "\n") {
		l = reHeader.ReplaceAllString(l, "func $1($2")
		l = strings.ReplaceAll(l, "->", ".")
		l = reFor.ReplaceAllString(l, "for $1 {")
		l = reIf.ReplaceAllString(l, "if $1 {")
		l = reWhile.ReplaceAllString(l, "for $1 {")
		l = reTypes.ReplaceAllString(l, "")
		l = reSemi.ReplaceAllString(l, "")
		l = strings.ReplaceAll(l, "NULL", "nil")
		out = append(out, l)
	}
	return "package fixture\n\n" + strings.Join(out, "\n") + "\n"
}

func real(t *testing.T, ours map[string][]byte) *Report {
	t.Helper()
	ref, f, _ := realFFmpeg(t)
	m := &Manifest{Version: 1, Notice: "NOTICE", References: []Reference{ref}}
	rep, err := Check(Options{Root: writeTree(t, ours), Manifest: m, Fetcher: f, Thresholds: DefaultThresholds()})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestRealFFmpegTranslationFires(t *testing.T) {
	_, _, dir := realFFmpeg(t)
	flac, err := os.ReadFile(filepath.Join(dir, "libavcodec/flacdec.c"))
	if err != nil {
		t.Fatal(err)
	}
	mpa, err := os.ReadFile(filepath.Join(dir, "libavcodec/mpegaudiodata.c"))
	if err != nil {
		t.Fatal(err)
	}
	goFunc := cToGo(cutFunc(t, string(flac), "static int decode_residuals("))

	// the table: the same numbers, re-spelled in decimal under another name
	tab := cutFunc(t, string(mpa), "const int ff_mpa_quant_steps[17] = {")
	var vals []string
	for _, tk := range Lex([]byte(tab[strings.Index(tab, "{"):])) {
		if tk.Kind == KNum {
			c, _ := Canon(tk.Text)
			vals = append(vals, c)
		}
	}
	if len(vals) != 17 {
		t.Fatalf("cut %d numbers, want 17", len(vals))
	}
	goTab := fmt.Sprintf("package fixture\n\nvar levels = []int{%s}\n", strings.Join(vals, ", "))

	rep := real(t, map[string][]byte{"residual.go": []byte(goFunc), "levels.go": []byte(goTab)})
	var shape, table *Finding
	for i := range rep.Findings {
		switch f := &rep.Findings[i]; f.Kind {
		case KindShape:
			shape = f
		case KindTable:
			table = f
		}
	}
	if shape == nil || shape.Theirs.File != "libavcodec/flacdec.c" || shape.Ours.File != "residual.go" {
		t.Fatalf("the mechanical translation of a REAL ffmpeg function was not caught: %+v", rep.Findings)
	}
	if table == nil || table.Theirs.File != "libavcodec/mpegaudiodata.c" || table.Numbers != 17 {
		t.Fatalf("the re-spelled REAL ffmpeg table was not caught: %+v", rep.Findings)
	}
	t.Logf("REAL planted shape: %d tokens, similarity %.0f%%, ours %s:%d-%d, theirs %s:%d-%d",
		shape.Tokens, shape.Similarity*100, shape.Ours.File, shape.Ours.Start, shape.Ours.End, shape.Theirs.File, shape.Theirs.Start, shape.Theirs.End)
	t.Logf("REAL planted table: %d numbers, ours %s:%d-%d, theirs %s:%d-%d",
		table.Numbers, table.Ours.File, table.Ours.Start, table.Ours.End, table.Theirs.File, table.Theirs.Start, table.Theirs.End)
}

// The converse on real code: the same job, written from the format specification,
// against the same real ffmpeg function. It must stay quiet.
func TestRealFFmpegIndependentFLACIsQuiet(t *testing.T) {
	rep := real(t, map[string][]byte{"residual.go": fixture(t, "ours_flac_independent.go.txt")})
	if len(rep.Findings) != 0 {
		t.Fatalf("false positive against real ffmpeg: %+v", rep.Findings)
	}
}
