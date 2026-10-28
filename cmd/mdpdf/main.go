// Command mdpdf renders a Markdown report to PDF with the fleet's own tools:
// go-kramdown (Markdown to HTML, pipe tables included), then
// go-pdfkit/html2pdf (HTML laid out by go-webengine, written by go-pdfkit),
// styled for print.
//
//	mdpdf --in report.md --out report.pdf --title "Q3 report" --author "A. Writer"
//	mdpdf --in report.md --paper letter --css house.css
//
// Input is bounded (16 MiB of Markdown, 1 MiB of CSS). Raw HTML in the
// Markdown is passed through to the layout engine like any HTML page.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/go-kramdown/kramdown"
	"github.com/go-pdfkit/html2pdf"
	"github.com/spf13/cobra"
)

// osExit allows tests to override os.Exit.
var osExit = os.Exit

const (
	maxMarkdown = 16 << 20 // bytes of Markdown read
	maxCSS      = 1 << 20  // bytes of extra stylesheet read
)

// baseCSS styles the report for print. The @page rule (with the paper named
// by %s) makes html2pdf lay the page out 1:1, so the point sizes below print
// at their true size.
const baseCSS = `
@page { size: %s; margin: 18mm 18mm 20mm 18mm; }
body { font-family: sans-serif; font-size: 10pt; line-height: 1.45; color: #1a1a1a; }
h1 { font-family: serif; font-size: 22pt; line-height: 1.2; margin: 0 0 6pt 0; }
h2 { font-family: serif; font-size: 15pt; margin: 18pt 0 6pt 0; break-after: avoid; }
h3 { font-family: serif; font-size: 12pt; margin: 12pt 0 4pt 0; break-after: avoid; }
p { margin: 0 0 6pt 0; orphans: 2; widows: 2; }
ul, ol { margin: 0 0 6pt 0; padding-left: 16pt; }
li { margin: 0 0 3pt 0; }
code { font-family: monospace; font-size: 9pt; background: #f2f2f2; }
table { border-collapse: collapse; width: 100%%; margin: 4pt 0 8pt 0; font-size: 8.5pt; break-inside: avoid; }
th, td { border: 0.5pt solid #9a9a9a; padding: 3pt 4pt; vertical-align: top; text-align: left; }
th { background: #ececec; }
a { color: #1a4f9c; text-decoration: none; }
`

// papers maps --paper to the CSS @page size keyword.
var papers = map[string]string{"a4": "A4", "letter": "letter"}

type options struct {
	in, out, title, author, paper, css string
}

// withDefaults fills --out and --title from the input's name and trims the
// free-text fields.
func (o options) withDefaults() options {
	if o.out == "" {
		o.out = strings.TrimSuffix(o.in, filepath.Ext(o.in)) + ".pdf"
	}
	o.title = strings.TrimSpace(o.title)
	if o.title == "" {
		o.title = strings.TrimSuffix(filepath.Base(o.in), filepath.Ext(o.in))
	}
	o.author = strings.TrimSpace(o.author)
	return o
}

// RootCmd builds the command; tests drive it with SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	var o options
	cmd := &cobra.Command{
		Use:   "mdpdf --in report.md [--out report.pdf]",
		Short: "render a Markdown report to a print-styled PDF",
		Long: `mdpdf converts Markdown to HTML with go-kramdown and the HTML to a vector PDF
with go-pdfkit/html2pdf, with a built-in A4 (or US letter) print stylesheet.
--css adds a stylesheet of your own after the built-in one.

--out defaults to the input's name with a .pdf extension; --title defaults
to the input's name without extension.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o = o.withDefaults()
			if err := run(o); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), o.out)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&o.in, "in", "i", "", "input Markdown file (required)")
	f.StringVarP(&o.out, "out", "o", "", "output PDF path (default: the input with a .pdf extension)")
	f.StringVar(&o.title, "title", "", "PDF Title and <title> (default: the input's file name without extension)")
	f.StringVar(&o.author, "author", "", "PDF Author")
	f.StringVar(&o.paper, "paper", "a4", "paper size: a4 or letter")
	f.StringVar(&o.css, "css", "", "extra stylesheet file, applied after the built-in one")
	_ = cmd.MarkFlagRequired("in")
	return cmd
}

// readBounded reads path, refusing a file larger than max bytes.
func readBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s: larger than %d bytes", path, max)
	}
	return b, nil
}

// toBody turns Markdown into the HTML body fragment, with the workarounds of
// workarounds.go applied.
func toBody(md string) (string, error) {
	return markLists(kramdown.ToHTML(separateTables(md), nil))
}

// buildPage assembles the HTML document. extraCSS may be empty.
func buildPage(body, title, paper, extraCSS string) (string, error) {
	size, ok := papers[strings.ToLower(paper)]
	if !ok {
		return "", fmt.Errorf("--paper %q: want a4 or letter", paper)
	}
	// The stylesheet sits in a <style> element: a closing tag in it would end
	// the element early and turn the rest of the file into markup.
	if strings.Contains(strings.ToLower(extraCSS), "</style") {
		return "", errors.New("--css: the stylesheet contains \"</style\"")
	}
	return "<!doctype html><html><head><meta charset=\"utf-8\"><title>" +
		html.EscapeString(title) + "</title><style>" + fmt.Sprintf(baseCSS, size) +
		"</style><style>" + extraCSS + "</style></head><body>" + body + "</body></html>", nil
}

func run(o options) error {
	if o.in == "" {
		return errors.New("--in is required")
	}
	o = o.withDefaults()
	title := o.title
	src, err := readBounded(o.in, maxMarkdown)
	if err != nil {
		return err
	}
	var extra []byte
	if o.css != "" {
		if extra, err = readBounded(o.css, maxCSS); err != nil {
			return err
		}
	}
	body, err := toBody(string(src))
	if err != nil {
		return err
	}
	page, err := buildPage(body, title, o.paper, string(extra))
	if err != nil {
		return err
	}
	doc, err := html2pdf.Export(page, html2pdf.Options{Title: title, Author: o.author})
	if err != nil {
		return fmt.Errorf("html2pdf: %w", err)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		return err
	}
	return os.WriteFile(o.out, buf.Bytes(), 0o644)
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	if err := RootCmd().Execute(); err != nil {
		osExit(1)
	}
}
