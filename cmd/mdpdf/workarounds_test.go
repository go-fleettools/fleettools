package main

import (
	"strings"
	"testing"
)

func TestSeparateTables(t *testing.T) {
	for in, want := range map[string]string{
		"- item\n\n| a |\n| --- |\n": "- item\n\n^\n\n| a |\n| --- |\n",
		"- item\n| a |\n| --- |\n":   "- item\n\n^\n\n| a |\n| --- |\n",
		"text\n| a |\n| --- |\n":     "text\n\n| a |\n| --- |\n",
		"text\n\n| a |\n| --- |\n":   "text\n\n| a |\n| --- |\n",
		"| a |\n| --- |\n| 1 |\n":    "| a |\n| --- |\n| 1 |\n",
	} {
		if got := separateTables(in); got != want {
			t.Errorf("separateTables(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMarkLists(t *testing.T) {
	got, err := markLists("<ol start=\"3\"><li><p>a</p></li><li>b</li></ol><ul><li>c</li><li>\n<p>d</p></li><li>[x] e</li><li>[ ] f</li></ul>")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<p>3. a</p>", "<li>4. b</li>", "<li>•  c</li>", "<p>•  d</p>", "<li>☑ e</li>", "<li>☐ f</li>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
}

// TestMarkListsNested: markers reach items of a list nested in an item.
func TestMarkListsNested(t *testing.T) {
	got, err := markLists("<ul><li>a<ol><li>b</li></ol></li></ul>")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"•  a", "1. b"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
}

// TestDeepNestingIsBounded: a pathological nesting is refused with an error
// (the HTML parser's own 512-element wall) rather than recursed into.
func TestDeepNestingIsBounded(t *testing.T) {
	deep := strings.Repeat("<ul><li>", 5000) + "x" + strings.Repeat("</li></ul>", 5000)
	if _, err := markLists(deep); err == nil {
		t.Fatal("5000 nested lists were accepted")
	}
}

// FuzzPreprocess feeds arbitrary Markdown through the pre-processing
// (separateTables) and the whole Markdown-to-HTML step (toBody: kramdown, then
// markLists). Properties: no panic; separateTables only ever adds
// blank lines and "^" lines, never touches or reorders a line of the input,
// and is idempotent.
func FuzzPreprocess(f *testing.F) {
	for _, s := range []string{
		"", "- a\n\n| x |\n| - |\n", "1. a\n2. b\n| t |\n", "text\n| a |\n| --- |\n| 1 |\n",
		"- [x] done\n- [ ] todo\n", "* a\n  * b\n\n| c |\n", "|\n", "\n\n\n", "^\n- a\n|",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, md string) {
		if len(md) > 1<<16 {
			t.Skip()
		}
		sep := separateTables(md)
		if got := separateTables(sep); got != sep {
			t.Fatalf("not idempotent:\n in  %q\n 1st %q\n 2nd %q", md, sep, got)
		}
		// Dropping the added lines must give the original back.
		orig := strings.Split(md, "\n")
		i := 0
		for _, l := range strings.Split(sep, "\n") {
			if i < len(orig) && l == orig[i] {
				i++
				continue
			}
			if l != "" && l != "^" {
				t.Fatalf("separateTables inserted %q into %q", l, md)
			}
		}
		if i != len(orig) {
			t.Fatalf("separateTables lost or reordered lines of %q: %q", md, sep)
		}
		// An error (nesting past the HTML parser's wall) is a clean refusal;
		// only a panic or a hang is a finding.
		_, _ = toBody(md)
	})
}
