package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	page, err := buildPage("<p>x</p>", `</title><script>alert(1)</script>`, "a4", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(page, "<script>") {
		t.Errorf("title not escaped: %s", page)
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
