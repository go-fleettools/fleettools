package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-pdfkit/extract"
	"github.com/go-pdfkit/reader"
)

const sample = `# Quarterly report

A paragraph with **bold** text.

- first
- [x] done
- [ ] todo

| a | b |
| --- | --- |
| 1 | 2 |

1. one
2. two
`

// exec runs the command and returns its error and combined output.
func exec(t *testing.T, args ...string) (error, string) {
	t.Helper()
	cmd := RootCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return err, buf.String()
}

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func open(t *testing.T, path string) *reader.Document {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := reader.Open(data)
	if err != nil {
		t.Fatalf("output does not parse as a PDF: %v", err)
	}
	return doc
}

// infoField reads one text entry of the PDF information dictionary.
func infoField(t *testing.T, doc *reader.Document, key string) string {
	t.Helper()
	obj, ok := doc.Trailer()["Info"]
	if !ok {
		t.Fatal("no /Info in trailer")
	}
	res, err := doc.Resolve(obj)
	if err != nil {
		t.Fatal(err)
	}
	info, ok := reader.ToDict(res)
	if !ok {
		t.Fatal("/Info is not a dictionary")
	}
	v, err := doc.Resolve(info[reader.Name(key)])
	if err != nil {
		t.Fatal(err)
	}
	s, _ := reader.TextString(v)
	return s
}

// mediaBox returns the width and height, in points, of page 1.
func mediaBox(t *testing.T, doc *reader.Document) (w, h float64) {
	t.Helper()
	page, err := doc.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	res, err := doc.Resolve(page["MediaBox"])
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := reader.ToArray(res)
	if !ok || len(arr) != 4 {
		t.Fatalf("MediaBox = %v", res)
	}
	var v [4]float64
	for i := range v {
		n, err := doc.Resolve(arr[i])
		if err != nil {
			t.Fatal(err)
		}
		v[i], _ = reader.ToFloat(n)
		if iv, ok := reader.ToInt(n); ok {
			v[i] = float64(iv)
		}
	}
	return v[2] - v[0], v[3] - v[1]
}

func TestRenderMetadataAndPages(t *testing.T) {
	dir := t.TempDir()
	in := write(t, dir, "report.md", sample)
	out := filepath.Join(dir, "report.pdf")
	err, msg := exec(t, "--in", in, "--out", out, "--title", "Quarterly report", "--author", "Ada Lovelace")
	if err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	if strings.TrimSpace(msg) != out {
		t.Errorf("stdout = %q, want the output path", msg)
	}
	doc := open(t, out)
	if got := infoField(t, doc, "Title"); got != "Quarterly report" {
		t.Errorf("/Title = %q", got)
	}
	if got := infoField(t, doc, "Author"); got != "Ada Lovelace" {
		t.Errorf("/Author = %q", got)
	}
	if n := doc.PageCount(); n != 1 {
		t.Errorf("PageCount = %d, want 1", n)
	}
	// A4 is 595 x 842 pt.
	if w, h := mediaBox(t, doc); w < 594 || w > 596 || h < 841 || h > 843 {
		t.Errorf("MediaBox = %.0f x %.0f, want A4", w, h)
	}
}

func TestLongDocumentPaginates(t *testing.T) {
	dir := t.TempDir()
	in := write(t, dir, "long.md", strings.Repeat("A paragraph of the report, long enough to fill some of the page width and then wrap.\n\n", 150))
	out := filepath.Join(dir, "long.pdf")
	if err, msg := exec(t, "--in", in, "--out", out); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	if n := open(t, out).PageCount(); n < 2 {
		t.Errorf("PageCount = %d, want at least 2", n)
	}
}

func TestPaperLetter(t *testing.T) {
	dir := t.TempDir()
	in := write(t, dir, "r.md", sample)
	out := filepath.Join(dir, "r.pdf")
	if err, msg := exec(t, "--in", in, "--out", out, "--paper", "letter"); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	// US letter is 612 x 792 pt.
	if w, h := mediaBox(t, open(t, out)); w < 611 || w > 613 || h < 791 || h > 793 {
		t.Errorf("MediaBox = %.0f x %.0f, want letter", w, h)
	}
}

func TestDefaultsFromInput(t *testing.T) {
	dir := t.TempDir()
	in := write(t, dir, "my-report.md", sample)
	if err, msg := exec(t, "--in", in); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	doc := open(t, filepath.Join(dir, "my-report.pdf"))
	if got := infoField(t, doc, "Title"); got != "my-report" {
		t.Errorf("default /Title = %q, want my-report", got)
	}
}

func TestExtraCSS(t *testing.T) {
	dir := t.TempDir()
	in := write(t, dir, "r.md", sample)
	css := write(t, dir, "x.css", "h1 { color: red; }")
	out := filepath.Join(dir, "r.pdf")
	if err, msg := exec(t, "--in", in, "--out", out, "--css", css); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	open(t, out)
}

func TestErrors(t *testing.T) {
	dir := t.TempDir()
	in := write(t, dir, "r.md", sample)
	badCSS := write(t, dir, "bad.css", "a{} </STYLE><script>x</script>")
	for name, args := range map[string][]string{
		"no --in":         {},
		"missing input":   {"--in", filepath.Join(dir, "nope.md")},
		"bad paper":       {"--in", in, "--paper", "a3"},
		"missing css":     {"--in", in, "--css", filepath.Join(dir, "nope.css")},
		"css closes tag":  {"--in", in, "--css", badCSS},
		"positional args": {"--in", in, "extra"},
		"unwritable out":  {"--in", in, "--out", filepath.Join(dir, "no", "dir", "o.pdf")},
		"unknown flag":    {"--nosuchflag"},
	} {
		if err, _ := exec(t, args...); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestInputIsBounded(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.md")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxMarkdown + 1); err != nil { // sparse
		t.Fatal(err)
	}
	f.Close()
	if err, _ := exec(t, "--in", big, "--out", filepath.Join(dir, "o.pdf")); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("oversized input: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "o.pdf")); err == nil {
		t.Error("an output file was written for a refused input")
	}
}

func TestBuildPageEscapesTitle(t *testing.T) {
	page, err := buildPage("<p>x</p>", `</title><script>alert(1)</script>`, "a4", "", true, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(page, "<script>") {
		t.Errorf("title not escaped: %s", page)
	}
}

// pageText returns the text of page n with all whitespace removed.
func pageText(t *testing.T, doc *reader.Document, n int) string {
	t.Helper()
	s, err := extract.Text(doc, n)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(s), "")
}

func TestPageNumbersDefaultOnAndOptOut(t *testing.T) {
	dir := t.TempDir()
	long := "# T\n\n" + strings.Repeat("A paragraph of the report, long enough to fill some of the page width and then wrap.\n\n", 150)
	in := write(t, dir, "p.md", long)
	out := filepath.Join(dir, "p.pdf")
	if err, msg := exec(t, "--in", in, "--out", out); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	doc := open(t, out)
	n := doc.PageCount()
	if n < 2 {
		t.Fatalf("PageCount = %d, want at least 2", n)
	}
	for p := 1; p <= n; p++ {
		want := strconv.Itoa(p) + "/" + strconv.Itoa(n)
		if got := pageText(t, doc, p); !strings.HasSuffix(got, want) {
			t.Errorf("page %d text ends %q, want the footer %q", p, got[max(0, len(got)-12):], want)
		}
	}

	off := filepath.Join(dir, "off.pdf")
	if err, msg := exec(t, "--in", in, "--out", off, "--page-numbers=false"); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	if got := pageText(t, open(t, off), 1); strings.HasSuffix(got, "1/"+strconv.Itoa(n)) {
		t.Errorf("--page-numbers=false still printed a footer: %q", got)
	}
}

func TestTOCListsHeadingsWithTheirPages(t *testing.T) {
	dir := t.TempDir()
	fill := strings.Repeat("A paragraph of the report, long enough to fill some of the page width and then wrap.\n\n", 70)
	md := "# Report\n\nBy me.\n\n## Alpha\n\n" + fill + "## Beta\n\n### Beta sub\n\n" + fill + "## Gamma\n\nend\n"
	in := write(t, dir, "r.md", md)
	out := filepath.Join(dir, "r.pdf")
	if err, msg := exec(t, "--in", in, "--out", out, "--toc"); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	doc := open(t, out)
	if doc.PageCount() < 3 {
		t.Fatalf("PageCount = %d, want at least 3", doc.PageCount())
	}
	first := pageText(t, doc, 1)
	if !strings.Contains(first, "Contents") {
		t.Fatalf("page 1 has no table of contents: %q", first)
	}
	// A heading's own line is exactly its text (a table of contents line also
	// carries its number); the page it is on must be the number printed on
	// page 1 after its entry.
	pageOf := func(h string) int {
		for p := 1; p <= doc.PageCount(); p++ {
			s, err := extract.Text(doc, p)
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(s, "\n") {
				if strings.Join(strings.Fields(line), "") == h {
					return p
				}
			}
		}
		return 0
	}
	for _, h := range []string{"Alpha", "Beta", "Betasub", "Gamma"} {
		p := pageOf(h)
		if p == 0 {
			t.Fatalf("heading %q not found on any page", h)
		}
		if want := h + strconv.Itoa(p); !strings.Contains(first, want) {
			t.Errorf("the table of contents lacks %q (heading is on page %d): %q", want, p, first)
		}
	}
	if pageOf("Gamma") < 2 {
		t.Errorf("the fixture does not spread the headings over pages")
	}
}

// TestListMarkersPaintedExactlyOnce: the engine paints the markers
// (go-webengine v0.18.0); none of ours may double them.
func TestListMarkersPaintedExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	in := write(t, dir, "l.md", "- alpha\n- beta\n\ntext between\n\n- [x] gamma\n\n1. one\n2. two\n3. three\n")
	out := filepath.Join(dir, "l.pdf")
	if err, msg := exec(t, "--in", in, "--out", out, "--page-numbers=false"); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	got := pageText(t, open(t, out), 1)
	if n := strings.Count(got, "•"); n != 2 {
		t.Errorf("%d bullets in %q, want 2 (the task item has a box instead)", n, got)
	}
	for _, m := range []string{"1.one", "2.two", "3.three"} {
		if strings.Count(got, m) != 1 {
			t.Errorf("%q appears %d times in %q, want once", m, strings.Count(got, m), got)
		}
	}
	if strings.Count(got, "☑") != 1 {
		t.Errorf("task box count in %q, want 1", got)
	}
}

// headingPage returns the first page that has a line made of exactly h (a
// table of contents line also carries its number, so it does not match).
func headingPage(t *testing.T, doc *reader.Document, h string) int {
	t.Helper()
	for p := 1; p <= doc.PageCount(); p++ {
		s, err := extract.Text(doc, p)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(s, "\n") {
			if strings.Join(strings.Fields(line), "") == h {
				return p
			}
		}
	}
	return 0
}

// TestTOCAfterSectionHasRightPageNumbers: the table of contents sits after a
// long Summary section and takes room itself, so the headings after it move
// down; the numbers it prints must be those of the final layout.
func TestTOCAfterSectionHasRightPageNumbers(t *testing.T) {
	dir := t.TempDir()
	fill := strings.Repeat("A paragraph of the report, long enough to fill some of the page width and then wrap.\n\n", 45)
	md := "# Report\n\nBy me.\n\n## Summary\n\n" + fill + "## Alpha\n\n" + fill + "## Beta\n\n" + fill + "## Gamma\n\nend\n"
	in := write(t, dir, "r.md", md)
	out := filepath.Join(dir, "r.pdf")
	if err, msg := exec(t, "--in", in, "--out", out, "--toc-after", "Summary"); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	doc := open(t, out)
	tocPage := 0
	for p := 1; p <= doc.PageCount(); p++ {
		if strings.Contains(pageText(t, doc, p), "Contents") {
			tocPage = p
			break
		}
	}
	if tocPage < 2 {
		t.Fatalf("the table of contents is on page %d: it should follow the long Summary section", tocPage)
	}
	if s := headingPage(t, doc, "Summary"); s != 1 {
		t.Errorf("Summary on page %d, want 1", s)
	}
	text := pageText(t, doc, tocPage)
	for _, h := range []string{"Summary", "Alpha", "Beta", "Gamma"} {
		p := headingPage(t, doc, h)
		if p == 0 {
			t.Fatalf("heading %q not found", h)
		}
		if want := h + strconv.Itoa(p); !strings.Contains(text, want) {
			t.Errorf("the table of contents lacks %q (heading is on page %d): %q", want, p, text)
		}
	}
	if headingPage(t, doc, "Alpha") < tocPage {
		t.Errorf("Alpha is before the table of contents")
	}
}

func TestTOCAfterUnknownHeading(t *testing.T) {
	dir := t.TempDir()
	in := write(t, dir, "r.md", "# T\n\n## One\n\nx\n\n## Two\n\ny\n")
	err, _ := exec(t, "--in", in, "--out", filepath.Join(dir, "o.pdf"), "--toc-after", "Three")
	if err == nil || !strings.Contains(err.Error(), `"One"`) || !strings.Contains(err.Error(), `"Two"`) {
		t.Errorf("err = %v, want one naming the headings", err)
	}
	if _, serr := os.Stat(filepath.Join(dir, "o.pdf")); serr == nil {
		t.Error("an output was written for an unknown heading")
	}
}

func TestTOCMarkerInMarkdown(t *testing.T) {
	dir := t.TempDir()
	fill := strings.Repeat("A paragraph of the report, long enough to fill some of the page width and then wrap.\n\n", 45)
	md := "# R\n\n## Summary\n\n" + fill + "<!-- toc -->\n\n## Alpha\n\n" + fill + "## Beta\n\nend\n"
	in := write(t, dir, "m.md", md)
	out := filepath.Join(dir, "m.pdf")
	if err, msg := exec(t, "--in", in, "--out", out, "--toc"); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	doc := open(t, out)
	tocPage := 0
	for p := 1; p <= doc.PageCount(); p++ {
		if strings.Contains(pageText(t, doc, p), "Contents") {
			tocPage = p
			break
		}
	}
	if tocPage < 2 {
		t.Errorf("the marker was ignored: the table of contents is on page %d", tocPage)
	}
}

func TestTOCTitleAndLanguage(t *testing.T) {
	dir := t.TempDir()
	in := write(t, dir, "r.md", "# T\n\n## Un\n\ntexte\n\n## Deux\n\ntexte\n")
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"fr default":       {[]string{"--lang", "fr"}, "Tabledesmatières"},
		"fr-CA default":    {[]string{"--lang", "fr-CA"}, "Tabledesmatières"},
		"en default":       {[]string{"--lang", "en"}, "Contents"},
		"no lang":          {nil, "Contents"},
		"de":               {[]string{"--lang", "de"}, "Inhaltsverzeichnis"},
		"explicit wins":    {[]string{"--lang", "fr", "--toc-title", "Sommaire"}, "Sommaire"},
		"unknown lang":     {[]string{"--lang", "ja"}, "Contents"},
		"explicit no lang": {[]string{"--toc-title", "Plan"}, "Plan"},
	} {
		out := filepath.Join(dir, "o.pdf")
		args := append([]string{"--in", in, "--out", out, "--toc"}, tc.args...)
		if err, msg := exec(t, args...); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, msg)
		}
		if got := pageText(t, open(t, out), 1); !strings.Contains(got, tc.want) {
			t.Errorf("%s: page 1 text %q lacks %q", name, got, tc.want)
		}
	}
}

func TestLangIsValidatedAndSetOnHTML(t *testing.T) {
	for _, bad := range []string{`fr"><script>`, "f", "fr_FR", "x y"} {
		if checkLang(bad) == nil {
			t.Errorf("checkLang(%q) accepted", bad)
		}
	}
	for _, good := range []string{"", "fr", "en-GB", "zh-Hant-TW"} {
		if err := checkLang(good); err != nil {
			t.Errorf("checkLang(%q) = %v", good, err)
		}
	}
	page, err := buildPage("<p>x</p>", "t", "a4", "", false, "fr")
	if err != nil || !strings.Contains(page, `<html lang="fr">`) {
		t.Errorf("page = %q, %v; want <html lang=\"fr\">", page, err)
	}
	dir := t.TempDir()
	in := write(t, dir, "r.md", "x")
	if err, _ := exec(t, "--in", in, "--out", filepath.Join(dir, "o.pdf"), "--lang", `fr"><x`); err == nil {
		t.Error("an invalid --lang was accepted")
	}
}

func TestMainExit(t *testing.T) {
	// WarnIfStale shells out to gh; with an empty PATH it says it could not tell.
	t.Setenv("PATH", "")
	old := osExit
	defer func() { osExit = old }()
	var code int
	osExit = func(c int) { code = c }
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"mdpdf"}
	main()
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
}
