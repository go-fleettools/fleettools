package main

import (
	"strings"
	"testing"
)

func TestSwapCheckboxes(t *testing.T) {
	got, err := swapCheckboxes(`<ul><li><input type="checkbox" disabled="disabled" checked="checked" />a</li><li><p><input type="checkbox" disabled="disabled" />b</p></li><li><input type="text">c</li></ul>`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<li>☑ a</li>", "<p>☐ b</p>", `<input type="text"/>c`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
}

// TestNoListMarkersInTheHTML: since html2pdf v0.3.0 the engine paints the
// markers, so the HTML must carry none of its own (they would print twice).
func TestNoListMarkersInTheHTML(t *testing.T) {
	body, err := toBody("- a\n- b\n\n1. one\n2. two\n", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "•") || strings.Contains(body, "1. one") {
		t.Errorf("the HTML carries its own list markers:\n%s", body)
	}
}

// TestTaskItems: kramdown's GFM output (v0.3.1) is a disabled checkbox; the
// engine paints none, so it becomes a ballot box and the item gets no bullet.
func TestTaskItems(t *testing.T) {
	body, err := toBody("- plain\n- [x] done\n- [ ] todo\n", false, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"plain", "☑ done", "☐ todo"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %s", want, body)
		}
	}
	if strings.Contains(body, "<input") || strings.Contains(body, "[x]") {
		t.Errorf("checkbox or literal task marker left in %s", body)
	}
}

// TestTableAfterListIsATable: go-kramdown/kramdown#12, fixed in v0.3.1 — no
// workaround any more (PR #14), and this keeps it that way.
func TestTableAfterListIsATable(t *testing.T) {
	body, err := toBody("- item\n\n| a | b |\n| - | - |\n| 1 | 2 |\n", false, "")
	if err != nil {
		t.Fatal(err)
	}
	i, j := strings.Index(body, "</ul>"), strings.Index(body, "<table")
	if i < 0 || j < i {
		t.Errorf("the table is not after the list:\n%s", body)
	}
}

// TestDeepNestingIsBounded: a pathological nesting is refused with an error
// (the HTML parser's own 512-element wall) rather than recursed into.
func TestDeepNestingIsBounded(t *testing.T) {
	deep := strings.Repeat("<ul><li>", 5000) + "x" + strings.Repeat("</li></ul>", 5000)
	if _, err := swapCheckboxes(deep); err == nil {
		t.Fatal("5000 nested lists were accepted")
	}
}

func TestInsertTOC(t *testing.T) {
	src := "<h1 id=\"t\">Title</h1>\n<p>By me.</p>\n<h2 id=\"a\">Alpha</h2>\n<h3 id=\"b\">Beta <em>x</em></h3>\n<h2>No id &amp; more</h2>\n"
	got, err := insertTOC(src, "Contents", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<p class="toc-title">Contents</p>`,
		`<a href="#a">Alpha</a>`, `<tr class="toc-item toc-l3"><td class="toc-text"><a href="#b">Beta x</a></td><td class="toc-num"><a href="#b"></a>`,
		`<a href="#toc-1">No id &amp; more</a>`, `<h2 id="toc-1">`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	// After the title block (h1 and its paragraph), before the first section.
	if !(strings.Index(got, "By me.") < strings.Index(got, `class="toc"`) && strings.Index(got, `class="toc"`) < strings.Index(got, `<h2 id="a">`)) {
		t.Errorf("the table of contents is not between the title block and the first section:\n%s", got)
	}
	if strings.Contains(got, `href="#t"`) {
		t.Errorf("the h1 is listed:\n%s", got)
	}
}

func TestInsertTOCNoHeadings(t *testing.T) {
	src := "<h1>T</h1><p>x</p>"
	if got, err := insertTOC(src, "Contents", ""); err != nil || got != src {
		t.Errorf("insertTOC = %q, %v; want the input unchanged", got, err)
	}
}

func TestInsertTOCFirstHeadingNested(t *testing.T) {
	got, err := insertTOC(`<div><h2 id="a">A</h2></div>`, "Contents", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(got, `class="toc"`) > strings.Index(got, `<h2 id="a">`) || !strings.Contains(got, `<div><div class="toc">`) {
		t.Errorf("nested heading: %s", got)
	}
}

const tocDoc = `<h1 id="t">Title</h1><p>intro</p>` +
	`<h2 id="summary">Summary</h2><p>S1</p><h3 id="s-sub">Sub</h3><p>S2</p>` +
	`<h2 id="alpha">Alpha</h2><p>A1</p><h2 id="beta">Béta</h2><p>B1</p>`

// order reports whether the markers appear in the given order in s.
func order(s string, markers ...string) bool {
	at := -1
	for _, m := range markers {
		i := strings.Index(s, m)
		if i < 0 || i < at {
			return false
		}
		at = i
	}
	return true
}

func TestInsertTOCAfterNamedSection(t *testing.T) {
	// After "Summary" and its h3, before the next h2; every h2/h3 is listed,
	// Summary included.
	got, err := insertTOC(tocDoc, "Contents", "Summary")
	if err != nil {
		t.Fatal(err)
	}
	if !order(got, "<p>S2</p>", `class="toc"`, `<h2 id="alpha">`) {
		t.Errorf("not at the end of the Summary section:\n%s", got)
	}
	for _, want := range []string{`<a href="#summary">Summary</a>`, `<a href="#s-sub">Sub</a>`, `<a href="#alpha">Alpha</a>`} {
		if !strings.Contains(got, want) {
			t.Errorf("the entries lack %s", want)
		}
	}
	// An h3 section ends at the next heading of its level or higher.
	got, err = insertTOC(tocDoc, "Contents", "Sub")
	if err != nil || !order(got, "<p>S2</p>", `class="toc"`, `<h2 id="alpha">`) {
		t.Errorf("after Sub: %v\n%s", err, got)
	}
	// The last section: at the end.
	got, err = insertTOC(tocDoc, "Contents", "Béta")
	if err != nil || !order(got, "<p>B1</p>", `class="toc"`) {
		t.Errorf("after the last section: %v\n%s", err, got)
	}
}

func TestInsertTOCAfterMatching(t *testing.T) {
	// NFC: "Béta" typed decomposed matches; case-sensitive; slug and id fall back.
	for after, ok := range map[string]bool{
		"  Summary ": true, "Béta": true, "summary": true /* slug */, "s-sub": true, "alpha": true,
		"Nope": false, "": true,
	} {
		_, err := insertTOC(tocDoc, "Contents", after)
		if (err == nil) != ok {
			t.Errorf("after %q: err = %v, want ok=%v", after, err, ok)
		}
	}
	// Exact text wins over a slug match: "Alpha" is the heading, even though
	// its slug "alpha" is also the id of another.
	got, err := insertTOC(`<h2 id="x">Alpha</h2><p>1</p><h2 id="alpha">Other</h2><p>2</p>`, "C", "Alpha")
	if err != nil || !order(got, "<p>1</p>", `class="toc"`, `<h2 id="alpha">`) {
		t.Errorf("exact text did not win: %v\n%s", err, got)
	}
}

func TestInsertTOCUnknownHeadingNamesTheOthers(t *testing.T) {
	_, err := insertTOC(tocDoc, "Contents", "Missing")
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{`"Missing"`, `"Title"`, `"Summary"`, `"Sub"`, `"Alpha"`, `"Béta"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not name %s", err, want)
		}
	}
}

func TestInsertTOCMarker(t *testing.T) {
	src := `<h1>T</h1><p>intro</p><!-- toc --><h2 id="a">A</h2><p>x</p><h2 id="b">B</h2>`
	got, err := insertTOC(src, "Contents", "")
	if err != nil {
		t.Fatal(err)
	}
	if !order(got, "<p>intro</p>", `class="toc"`, `<h2 id="a">`) || strings.Contains(got, "<!-- toc -->") {
		t.Errorf("the marker was not replaced in place:\n%s", got)
	}
	// A marker after the first section is honoured where it stands.
	src = `<h2 id="a">A</h2><p>x</p><!--toc--><h2 id="b">B</h2>`
	if got, err = insertTOC(src, "Contents", ""); err != nil || !order(got, "<p>x</p>", `class="toc"`, `<h2 id="b">`) {
		t.Errorf("marker mid-document: %v\n%s", err, got)
	}
	// --toc-after wins over the marker.
	src = `<h2 id="a">A</h2><p>x</p><!-- toc --><h2 id="b">B</h2><p>y</p>`
	got, err = insertTOC(src, "Contents", "B")
	if err != nil || !order(got, "<p>y</p>", `class="toc"`) {
		t.Errorf("--toc-after did not win over the marker: %v\n%s", err, got)
	}
}

// FuzzPreprocess feeds arbitrary Markdown through the whole Markdown-to-HTML
// step (kramdown, TOC, list markers). It must not panic or hang; an error
// (nesting past the HTML parser's wall) is a clean refusal.
func FuzzPreprocess(f *testing.F) {
	for _, s := range []string{
		"", "- a\n\n| x |\n| - |\n", "1. a\n2. b\n| t |\n", "text\n| a |\n| --- |\n| 1 |\n",
		"- [x] done\n- [ ] todo\n", "* a\n  * b\n\n| c |\n", "|\n", "\n\n\n", "^\n- a\n|",
		"# T\n\n## A\n\n### B\n\n## C {#c}\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, md string) {
		if len(md) > 1<<16 {
			t.Skip()
		}
		_, _ = toBody(md, false, "")
		_, _ = toBody(md, true, "Contents")
		_, _ = toBodyAfter(md, true, "Contents", "A")
	})
}
