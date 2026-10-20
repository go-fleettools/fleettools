package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func savePNG(t *testing.T, path string, img image.Image, compression png.CompressionLevel) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	enc := png.Encoder{CompressionLevel: compression}
	if err := enc.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func picture(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x * 7), uint8(y * 13), c.B, c.A})
		}
	}
	return img
}

func run(t *testing.T, args ...string) int {
	t.Helper()
	var out bytes.Buffer
	c := RootCmd()
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs(args)
	code := execute(c)
	t.Log(out.String())
	return code
}

// The reason the command exists: the same picture, encoded differently, is
// different bytes and the same pixels.
func TestSamePixelsDifferentBytesMatch(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	img := picture(40, 30, color.NRGBA{B: 90, A: 255})
	savePNG(t, filepath.Join(a, "k/x.png"), img, png.NoCompression)
	savePNG(t, filepath.Join(b, "k/x.png"), img, png.BestCompression)
	ba, _ := os.ReadFile(filepath.Join(a, "k/x.png"))
	bb, _ := os.ReadFile(filepath.Join(b, "k/x.png"))
	if bytes.Equal(ba, bb) {
		t.Fatal("the fixture should differ in bytes, or this test proves nothing")
	}
	if code := run(t, a, b); code != 0 {
		t.Errorf("exit %d for the same pixels", code)
	}
}

// Positive witnesses: one channel value off by one is seen; size, a missing
// file and a file that is not a PNG are differences.
func TestDifferencesAreSeen(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	img := picture(10, 10, color.NRGBA{B: 50, A: 255})
	off := picture(10, 10, color.NRGBA{B: 50, A: 255})
	off.Pix[4*55+2]++ // one blue value, by one
	savePNG(t, filepath.Join(a, "one.png"), img, png.DefaultCompression)
	savePNG(t, filepath.Join(b, "one.png"), off, png.DefaultCompression)
	savePNG(t, filepath.Join(a, "size.png"), img, png.DefaultCompression)
	savePNG(t, filepath.Join(b, "size.png"), picture(11, 10, color.NRGBA{A: 255}), png.DefaultCompression)
	savePNG(t, filepath.Join(a, "gone.png"), img, png.DefaultCompression)
	savePNG(t, filepath.Join(a, "broken.png"), img, png.DefaultCompression)
	if err := os.WriteFile(filepath.Join(b, "broken.png"), []byte("not a png"), 0o644); err != nil {
		t.Fatal(err)
	}

	pairs, err := Compare(a, b, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Pair{}
	for _, p := range pairs {
		got[p.Rel] = p
	}
	if p := got["one.png"]; p.Differ != 1 || p.MaxDelta != 1 {
		t.Errorf("one value off by one: %+v", p)
	}
	for _, rel := range []string{"size.png", "gone.png", "broken.png"} {
		if got[rel].Problem == "" {
			t.Errorf("%s: no problem reported", rel)
		}
	}
	if code := run(t, a, b); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	// Within tolerance 1 the off-by-one pair matches.
	pairs, _ = Compare(a, b, 1)
	for _, p := range pairs {
		if p.Rel == "one.png" && p.Problem != "" {
			t.Errorf("tolerance 1 still reports %s", p.Problem)
		}
	}
}

func TestNothingToCompareIsNotAMatch(t *testing.T) {
	if code := run(t, t.TempDir(), t.TempDir()); code != 2 {
		t.Errorf("exit %d for a tree without PNG, want 2", code)
	}
	if code := run(t, "--tolerance", "300", t.TempDir(), t.TempDir()); code != 2 {
		t.Errorf("tolerance 300 accepted")
	}
}
