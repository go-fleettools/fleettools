package main

import (
	"strings"
	"testing"
)

func TestParseGoVersionRefusesTheValuesThisToolReplaces(t *testing.T) {
	// Accepting one of these as -version would let gopin write back exactly
	// what it was built to remove.
	for _, s := range []string{"stable", "oldstable", "1.x", "1", "", "1.27.1.2", "v1.27.1", "1.-1", "1.027"} {
		if _, err := parseGoVersion(s); err == nil {
			t.Errorf("parseGoVersion(%q) = nil error; want a refusal", s)
		}
	}
}

func TestParseGoVersionAcceptsBothShapes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want goVersion
	}{
		{"1.27", goVersion{1, 27, 0}},
		{"1.27.1", goVersion{1, 27, 1}},
		{"1.26.4", goVersion{1, 26, 4}},
	} {
		got, err := parseGoVersion(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("parseGoVersion(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
}

// Numeric, not lexical: 1.27.10 is newer than 1.27.9 and string order says
// the opposite. That is the comparison bug this whole exercise is about.
func TestOlderThanIsNumeric(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"1.26.4", "1.27.1", true},
		{"1.27.1", "1.26.4", false},
		{"1.27.9", "1.27.10", true},
		{"1.27.10", "1.27.9", false},
		{"1.27", "1.27.0", false}, // go1.27 and go1.27.0 are the same release
		{"1.27.1", "1.27.1", false},
		{"2.0.0", "1.99.99", false},
	} {
		if got := mustParse(tc.a).olderThan(mustParse(tc.b)); got != tc.want {
			t.Errorf("%s olderThan %s = %v; want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// A go.mod with no directive, or one this cannot read, must NOT be rewritten:
// there is nothing to raise and guessing would touch a line nobody wrote.
func TestOlderGoModIsSilentOnWhatItCannotRead(t *testing.T) {
	target := mustParse("1.27.1")
	for _, s := range []string{"", "stable", "garbage", "1"} {
		if olderGoMod(s, target) {
			t.Errorf("olderGoMod(%q) = true; want false", s)
		}
	}
	if !olderGoMod("1.26.4", target) {
		t.Error("olderGoMod(1.26.4) = false; want true")
	}
	if olderGoMod("1.28.0", target) {
		t.Error("olderGoMod(1.28.0) = true; want false — ahead is not behind")
	}
}

// A rewrite must not change anything the diff did not ask for. The first
// pilot repository came out with `\ No newline at end of file` on go.mod,
// because `\s*$` ate it.
func TestGoDirectiveRewriteKeepsTheFileByteForByteOtherwise(t *testing.T) {
	for _, in := range []string{
		"module x\n\ngo 1.26.4\n",
		"module x\n\ngo 1.26.4\n\nrequire (\n\tx v1.0.0\n)\n",
		"module x\n\ngo 1.26.4",    // genuinely no trailing newline: keep it that way
		"module x\n\ngo 1.26.4 \n", // trailing space before the newline
	} {
		got := reGoDirective.ReplaceAllString(in, "go 1.27.1")
		if !strings.Contains(got, "go 1.27.1") {
			t.Errorf("not rewritten: %q", in)
			continue
		}
		wantTrailing := strings.HasSuffix(in, "\n")
		if strings.HasSuffix(got, "\n") != wantTrailing {
			t.Errorf("trailing newline changed for %q -> %q", in, got)
		}
		// Everything before and after the directive line must be untouched.
		if strings.Count(got, "\n") != strings.Count(in, "\n") {
			t.Errorf("line count changed: %q -> %q", in, got)
		}
	}
}
