package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goodMark is a mark in the family's grammar: a 256-unit plate, a rounded
// rectangle, a two-stop gradient and a stroked glyph with round caps.
const goodMark = `<svg viewBox="0 0 256 256" xmlns="http://www.w3.org/2000/svg">
  <defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1">
    <stop offset="0" stop-color="#4F46E5"/><stop offset="1" stop-color="#1E1B4B"/></linearGradient></defs>
  <rect width="256" height="256" rx="56" fill="url(#g)"/>
  <g fill="none" stroke="#FFFFFF" stroke-width="12" stroke-linecap="round" stroke-linejoin="round">
    <path d="M40 40 H216"/>
  </g>
</svg>`

// brandDir builds a complete brand repository in a temporary directory, so a
// test can take ONE thing away and see it named.
func brandDir(t *testing.T, org string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(parts ...string) {
		path := filepath.Join(append([]string{dir}, parts...)...)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		body := []byte("x")
		if strings.HasSuffix(path, ".svg") {
			body = []byte(goodMark)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range variants {
		write("svg", v, org+".svg")
		for _, s := range sizes {
			write("png", v, s, org+".png")
		}
	}
	for _, s := range sizes {
		write("jpg", s, org+".jpg")
	}
	write("ico", org+".ico")
	write("icns", org+".icns")
	write("avatar", org+".png")
	write("social", org+".png")
	write("README.md")
	write("LICENSE")
	return dir
}

func check(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out, errOut strings.Builder
	code := run(args, &out, &errOut)
	return code, out.String() + errOut.String()
}

// ⛔ THE CONTROL. A checker that cannot pass a good repository is a checker
// everybody turns off.
func TestACompleteBrandRepository(t *testing.T) {
	code, out := check(t, "-org", "go-x", brandDir(t, "go-x"))
	if code != 0 {
		t.Errorf("a complete repository was refused:\n%s", out)
	}
	if !strings.Contains(out, "every file the README promises is here") {
		t.Errorf("it said %q", out)
	}
}

// And the other control: each missing file is NAMED, because "something is
// wrong" sends somebody to compare two directory listings by eye.
func TestEveryMissingFileIsNamed(t *testing.T) {
	dir := brandDir(t, "go-x")
	for _, gone := range []string{
		filepath.Join("png", "white", "48", "go-x.png"),
		filepath.Join("icns", "go-x.icns"),
		filepath.Join("svg", "black", "go-x.svg"),
		"LICENSE",
	} {
		if err := os.Remove(filepath.Join(dir, gone)); err != nil {
			t.Fatal(err)
		}
	}
	code, out := check(t, "-org", "go-x", dir)
	if code == 0 {
		t.Fatalf("four missing files passed:\n%s", out)
	}
	for _, want := range []string{"png/white/48/go-x.png", "icns/go-x.icns", "svg/black/go-x.svg", "LICENSE"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q is not named:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "4 file(s) missing") {
		t.Errorf("it did not count them:\n%s", out)
	}
}

// ⛔ Each departure from the family grammar is reported on its own, so that a
// mark with two problems does not read as one.
func TestWhereAMarkLeavesTheFamily(t *testing.T) {
	cases := []struct {
		name, mark, says string
	}{
		{
			"a plate that is not 256 square",
			strings.Replace(goodMark, `viewBox="0 0 256 256"`, `viewBox="0 0 512 512"`, 1),
			"256 units square",
		},
		{
			"square corners",
			strings.Replace(goodMark, ` rx="56"`, "", 1),
			"rounded rectangle",
		},
		{
			"a flat colour instead of a gradient",
			strings.Replace(goodMark, `<stop offset="1" stop-color="#1E1B4B"/>`, "", 1),
			"gradient of two",
		},
		{
			"a filled glyph",
			strings.Replace(goodMark, `fill="none" stroke="#FFFFFF" stroke-width="12" stroke-linecap="round"`,
				`fill="#FFFFFF"`, 1),
			"not stroked",
		},
		{
			"square caps",
			strings.Replace(goodMark, `stroke-linecap="round"`, `stroke-linecap="butt"`, 1),
			"caps are",
		},
	}
	for _, c := range cases {
		dir := brandDir(t, "go-x")
		if err := os.WriteFile(filepath.Join(dir, "svg", "color", "go-x.svg"), []byte(c.mark), 0o644); err != nil {
			t.Fatal(err)
		}
		code, out := check(t, "-org", "go-x", dir)
		if code == 0 {
			t.Errorf("%s: it passed:\n%s", c.name, out)
			continue
		}
		if !strings.Contains(out, c.says) {
			t.Errorf("%s: it said %q, which does not mention %q", c.name, out, c.says)
		}
	}
}

// ⛔ The stroke WIDTH is deliberately not checked against a number. Measured
// over the 312 organisation marks there is no fleet-wide one — 13 is the
// commonest, 14, 15 and 22 all appear, and 10 appears once — so a check
// against a constant would fail most of the fleet and teach everyone to
// ignore the tool.
func TestTheStrokeWidthIsNotCheckedAgainstANumber(t *testing.T) {
	dir := brandDir(t, "go-x")
	wide := strings.Replace(goodMark, `stroke-width="12"`, `stroke-width="22"`, 1)
	if err := os.WriteFile(filepath.Join(dir, "svg", "color", "go-x.svg"), []byte(wide), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := check(t, "-org", "go-x", dir); code != 0 {
		t.Errorf("a 22-unit stroke was refused, and twenty marks use one:\n%s", out)
	}
}

func TestAMarkThatIsNotThereOrDoesNotParse(t *testing.T) {
	dir := brandDir(t, "go-x")
	if err := os.Remove(filepath.Join(dir, "svg", "color", "go-x.svg")); err != nil {
		t.Fatal(err)
	}
	code, out := check(t, "-org", "go-x", dir)
	if code == 0 || !strings.Contains(out, "not readable") {
		t.Errorf("a missing mark said %d: %s", code, out)
	}

	dir = brandDir(t, "go-x")
	if err := os.WriteFile(filepath.Join(dir, "svg", "color", "go-x.svg"), []byte("<svg"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out = check(t, "-org", "go-x", dir)
	if code == 0 || !strings.Contains(out, "does not parse") {
		t.Errorf("an unparseable mark said %d: %s", code, out)
	}
}

// Without -org the organisation is the parent directory's name, because a
// brand repository is checked out as <org>/brand. CI checks out the
// repository alone, which is what -org is for.
func TestTheOrganisationComesFromTheParentDirectory(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "go-y", "brand")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	built := brandDir(t, "go-y")
	if err := os.CopyFS(dir, os.DirFS(built)); err != nil {
		t.Fatal(err)
	}
	// The parent of <...>/go-y/brand is go-y.
	if code, out := check(t, dir); code != 0 {
		t.Errorf("it did not take the organisation from the path:\n%s", out)
	}
}

func TestAFlagItDoesNotHave(t *testing.T) {
	if code, _ := check(t, "-nope"); code != 2 {
		t.Errorf("an unknown flag gave %d, want 2", code)
	}
}
