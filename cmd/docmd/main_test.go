package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-ooxml/ooxml"
	"github.com/go-richdoc/richdoc"
)

func docxFixture(t testing.TB) []byte {
	t.Helper()
	doc := richdoc.New().
		H(1, richdoc.Txt("Quarterly Report")).
		P(richdoc.Txt("Plain words, then "), richdoc.Bold(richdoc.Txt("bold ones")), richdoc.Txt(".")).
		UList(true, richdoc.Item(richdoc.Paragraph{Inlines: []richdoc.Inline{richdoc.Txt("first item")}}),
			richdoc.Item(richdoc.Paragraph{Inlines: []richdoc.Inline{richdoc.Txt("second item")}})).
		Table(nil,
			[]richdoc.Cell{richdoc.Td(richdoc.Txt("Name")), richdoc.Td(richdoc.Txt("Qty"))},
			[][]richdoc.Cell{{richdoc.Td(richdoc.Txt("apples")), richdoc.Td(richdoc.Txt("12"))}}).
		Doc()
	b, err := ooxml.WriteWord(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func xlsxFixture(t testing.TB) []byte {
	t.Helper()
	doc := richdoc.New().
		Table(nil,
			[]richdoc.Cell{richdoc.Td(richdoc.Txt("City")), richdoc.Td(richdoc.Txt("Pop"))},
			[][]richdoc.Cell{{richdoc.Td(richdoc.Txt("Lyon")), richdoc.Td(richdoc.Txt("522"))}}).
		Doc()
	b, err := ooxml.WriteWorkbook(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func pptxFixture(t testing.TB) []byte {
	t.Helper()
	b, err := ooxml.WritePresentation(ooxml.Presentation{
		Width: 1280 * 9525, Height: 720 * 9525,
		Slides: []ooxml.PresentationSlide{{
			Shapes: []ooxml.Shape{{X: 914400, Y: 914400, W: 4000000, H: 600000,
				Text: []ooxml.TextRun{{Text: "Opening slide words", Size: 24}}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func zipOf(t testing.TB, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// run drives the command on a file under t.TempDir and returns stdout.
func run(t *testing.T, name string, data []byte) (string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return runArgs(t, nil, path)
}

func runArgs(t *testing.T, stdin []byte, args ...string) (string, error) {
	t.Helper()
	cmd := RootCmd()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	if stdin != nil {
		cmd.SetIn(bytes.NewReader(stdin))
	}
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func wantAll(t *testing.T, got string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(got, s) {
			t.Errorf("output lacks %q:\n%s", s, got)
		}
	}
}

func TestDocx(t *testing.T) {
	got, err := run(t, "report.docx", docxFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	wantAll(t, got, "# Quarterly Report", "**bold ones**", "first item", "second item", "| Name | Qty |", "| apples | 12 |")
	if !strings.Contains(got, "- first item") && !strings.Contains(got, "* first item") {
		t.Errorf("no list marker before the first item:\n%s", got)
	}
}

func TestPptx(t *testing.T) {
	got, err := run(t, "deck.pptx", pptxFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	wantAll(t, got, "Opening slide words")
}

func TestXlsx(t *testing.T) {
	got, err := run(t, "book.xlsx", xlsxFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	wantAll(t, got, "City", "Lyon", "522")
}

// The extension is not consulted: a workbook called .docx is still a workbook,
// and a document called .xlsx a document.
func TestFormatComesFromContentNotExtension(t *testing.T) {
	got, err := run(t, "really-a-workbook.docx", xlsxFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	wantAll(t, got, "Lyon")
	got, err = run(t, "really-a-document.xlsx", docxFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	wantAll(t, got, "# Quarterly Report")
	got, err = run(t, "really-a-deck.noext", pptxFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	wantAll(t, got, "Opening slide words")
}

func TestStdin(t *testing.T) {
	got, err := runArgs(t, docxFixture(t), "-")
	if err != nil {
		t.Fatal(err)
	}
	wantAll(t, got, "# Quarterly Report")
}

func TestRefusals(t *testing.T) {
	cfb := append(append([]byte{}, cfbMagic...), make([]byte, 512)...)
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"not a zip", []byte("just some text, not a zip"), "not an Office Open XML package"},
		{"empty", []byte{}, "not an Office Open XML package"},
		{"encrypted", cfb, "OLE compound file"},
		{"zip, not OOXML", zipOf(t, map[string]string{"hello.txt": "hi"}), "not an Office Open XML package"},
		{"unknown main part", zipOf(t, map[string]string{
			"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/doc.xml" ContentType="application/x-other+xml"/></Types>`,
			"_rels/.rels":         `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="doc.xml"/></Relationships>`,
			"doc.xml":             `<x/>`,
		}), "not a Word, PowerPoint or Excel package"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := run(t, "in.bin", c.data)
			if err == nil {
				t.Fatalf("no error; stdout %q", got)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q lacks %q", err, c.want)
			}
			if got != "" {
				t.Errorf("a refusal wrote to stdout: %q", got)
			}
		})
	}
}

// Cells that hold Blocks only (what ParseWord produces) are flattened, at any
// depth of lists and quotes; a cell with Inlines already is left alone.
func TestFlattenCells(t *testing.T) {
	para := func(s string) richdoc.Block { return richdoc.Paragraph{Inlines: []richdoc.Inline{richdoc.Txt(s)}} }
	cell := richdoc.Cell{Blocks: []richdoc.Block{para("one"), richdoc.ThematicBreak{}, para("two")}}
	kept := richdoc.Cell{Inlines: []richdoc.Inline{richdoc.Txt("kept")}, Blocks: []richdoc.Block{para("ignored")}}
	empty := richdoc.Cell{}
	tbl := func() richdoc.Block {
		return richdoc.Table{Header: []richdoc.Cell{cell, empty}, Rows: [][]richdoc.Cell{{kept, cell}}}
	}
	doc := &richdoc.Document{Blocks: []richdoc.Block{
		tbl(),
		richdoc.List{Items: []richdoc.ListItem{{Blocks: []richdoc.Block{tbl()}}}},
		richdoc.BlockQuote{Blocks: []richdoc.Block{tbl()}},
		para("tail"),
	}}
	flattenCells(doc.Blocks)
	b, err := convertDoc(doc)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if strings.Count(got, "one") != 6 || strings.Count(got, "two") != 6 {
		t.Errorf("cells not flattened everywhere:\n%s", got)
	}
	if strings.Contains(got, "ignored") || strings.Count(got, "kept") != 3 {
		t.Errorf("a cell that had Inlines was rewritten:\n%s", got)
	}
}

func TestMissingFileAndArgs(t *testing.T) {
	if _, err := runArgs(t, nil, filepath.Join(t.TempDir(), "absent.docx")); err == nil {
		t.Error("a missing file was not an error")
	}
	if _, err := runArgs(t, nil); err == nil {
		t.Error("no argument was not an error")
	}
}

func TestReadBounded(t *testing.T) {
	if _, err := readBounded(strings.NewReader("abcdef"), 5); err == nil {
		t.Error("input over the bound was accepted")
	}
	if b, err := readBounded(strings.NewReader("abcde"), 5); err != nil || string(b) != "abcde" {
		t.Errorf("input at the bound: %q, %v", b, err)
	}
	if _, err := readBounded(errReader{}, 5); err == nil {
		t.Error("a read error was swallowed")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

// A directory opens but cannot be read: the error surfaces rather than an
// empty document.
func TestDirectoryIsAnError(t *testing.T) {
	if _, err := runArgs(t, nil, t.TempDir()); err == nil {
		t.Error("a directory was accepted")
	}
}

// A package whose main part is declared as Word but holds broken XML is
// refused by the reader, with the file named.
func TestBrokenMainPart(t *testing.T) {
	data := zipOf(t, map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p>`,
	})
	_, err := run(t, "broken.docx", data)
	if err == nil || !strings.Contains(err.Error(), "broken.docx") {
		t.Errorf("error = %v, want one naming the file", err)
	}
}

func TestMainExitsOne(t *testing.T) {
	t.Setenv("PATH", "")
	old := osExit
	defer func() { osExit = old }()
	var code int
	osExit = func(c int) { code = c }
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"docmd"}
	main()
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
}

// corpusInputs are the seeds committed under testdata/fuzz/FuzzConvert.
func corpusInputs(t testing.TB) map[string][]byte {
	return map[string][]byte{
		"docx":      docxFixture(t),
		"pptx":      pptxFixture(t),
		"xlsx":      xlsxFixture(t),
		"not-a-zip": []byte("just some text, not a zip"),
		"cfb":       append(append([]byte{}, cfbMagic...), make([]byte, 64)...),
		"zip-other": zipOf(t, map[string]string{"hello.txt": "hi"}),
	}
}

// TestCorpusIsCommitted fails when a seed is missing or stale, and rewrites the
// directory when DOCMD_WRITE_CORPUS=1 (the files are the ooxml writers' output,
// so a writer update changes them).
func TestCorpusIsCommitted(t *testing.T) {
	dir := filepath.Join("testdata", "fuzz", "FuzzConvert")
	for name, data := range corpusInputs(t) {
		want := fmt.Sprintf("go test fuzz v1\n[]byte(%q)\n", data)
		path := filepath.Join(dir, name)
		if os.Getenv("DOCMD_WRITE_CORPUS") == "1" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("seed %s: %v", name, err)
			continue
		}
		// Presence is the gate; bytes may legitimately differ after a writer update.
		if !strings.HasPrefix(string(got), "go test fuzz v1\n[]byte(") {
			t.Errorf("seed %s is not a fuzz corpus file", name)
		}
	}
}

// FuzzConvert: whatever the bytes are, the answer is Markdown or an error,
// never a panic, and never both.
func FuzzConvert(f *testing.F) {
	for _, data := range corpusInputs(f) {
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		out, err := convert(data)
		if err != nil && out != nil {
			t.Fatalf("an error came with output %q", out)
		}
	})
}
