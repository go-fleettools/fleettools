// Command docmd prints a Word (.docx), PowerPoint (.pptx) or Excel (.xlsx)
// file as Markdown on standard output, with the fleet's own readers:
// go-ooxml/ooxml (the package into a go-richdoc document) and
// go-richdoc/markdown (the document into Markdown).
//
//	docmd report.docx > report.md
//	docmd deck.pptx
//	cat book.xlsx | docmd -
//
// The format is read from the CONTENT, never the extension: the content type
// the package declares for its main part says whether it is WordprocessingML,
// PresentationML or SpreadsheetML. An encrypted package (a Compound File,
// which is what Office writes when a password is set), a file that is not a
// ZIP, and a ZIP that is not Office Open XML are refused with an error.
// Input is bounded by go-ooxml/opc's default limits (256 MiB of package,
// 384 MiB uncompressed, 8192 parts).
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/go-ooxml/ooxml"
	"github.com/go-ooxml/opc"
	"github.com/go-richdoc/markdown"
	"github.com/go-richdoc/richdoc"
	"github.com/spf13/cobra"
)

// osExit allows tests to override os.Exit.
var osExit = os.Exit

// maxInput is opc's default package bound; the file is never read past it.
const maxInput = 256 << 20

// cfbMagic opens an OLE Compound File: an encrypted OOXML package, or a legacy
// .doc/.xls/.ppt.
var cfbMagic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// format is what the package's main part says it is.
type format int

const (
	word format = iota + 1
	presentation
	workbook
)

// detect names the format from the main part's declared content type.
func detect(data []byte) (format, error) {
	if bytes.HasPrefix(data, cfbMagic) {
		return 0, errors.New("not a readable Office Open XML package: it is an OLE compound file (an encrypted document, or a legacy .doc/.xls/.ppt); remove the password or save it as .docx/.pptx/.xlsx")
	}
	p, err := opc.Open(data, opc.Limits{})
	if err != nil {
		return 0, fmt.Errorf("not an Office Open XML package: %w", err)
	}
	main, err := p.Main()
	if err != nil {
		return 0, fmt.Errorf("not an Office Open XML package: %w", err)
	}
	ct := p.ContentType(main)
	switch {
	case strings.HasPrefix(ct, "application/vnd.openxmlformats-officedocument.wordprocessingml."):
		return word, nil
	case strings.HasPrefix(ct, "application/vnd.openxmlformats-officedocument.presentationml."):
		return presentation, nil
	case strings.HasPrefix(ct, "application/vnd.openxmlformats-officedocument.spreadsheetml."):
		return workbook, nil
	}
	return 0, fmt.Errorf("not a Word, PowerPoint or Excel package: the main part %q is declared as %q", main, ct)
}

// ⛔ NAMED WORKAROUND, to be deleted when go-richdoc/markdown#32 and the
// go-ooxml/ooxml#16 close. richdoc.Cell says a producer that sets
// Blocks MUST also set Inlines (the flattened words), and markdown.Write reads
// Inlines only. ooxml.ParseWord sets Blocks alone, so without this every table
// of a .docx reaches the Markdown as a grid of empty cells.

// flattenCells fills the Inlines of every cell that has Blocks and no Inlines,
// in tables at any depth of lists and quotes.
func flattenCells(blocks []richdoc.Block) {
	for i, b := range blocks {
		switch n := b.(type) {
		case richdoc.Table:
			n.Header = flattenRow(n.Header)
			for r := range n.Rows {
				n.Rows[r] = flattenRow(n.Rows[r])
			}
			blocks[i] = n
		case richdoc.List:
			for _, it := range n.Items {
				flattenCells(it.Blocks)
			}
		case richdoc.BlockQuote:
			flattenCells(n.Blocks)
		}
	}
}

func flattenRow(cells []richdoc.Cell) []richdoc.Cell {
	for i, c := range cells {
		if len(c.Inlines) > 0 || len(c.Blocks) == 0 {
			continue
		}
		var in []richdoc.Inline
		for _, b := range c.Blocks {
			text := richdoc.PlainText(&richdoc.Document{Blocks: []richdoc.Block{b}})
			if text == "" {
				continue
			}
			if len(in) > 0 {
				in = append(in, richdoc.Br())
			}
			in = append(in, richdoc.Txt(text))
		}
		cells[i].Inlines = in
	}
	return cells
}

// convert turns a package's bytes into Markdown.
func convert(data []byte) ([]byte, error) {
	f, err := detect(data)
	if err != nil {
		return nil, err
	}
	var doc *richdoc.Document
	switch f {
	case word:
		doc, err = ooxml.ParseWord(data, opc.Limits{})
	case presentation:
		doc, err = ooxml.ParsePresentation(data, opc.Limits{})
	default:
		doc, err = ooxml.ParseWorkbook(data, opc.Limits{})
	}
	if err != nil {
		return nil, err
	}
	return convertDoc(doc)
}

// convertDoc writes a parsed document as Markdown, after the workaround above.
func convertDoc(doc *richdoc.Document) ([]byte, error) {
	flattenCells(doc.Blocks)
	return markdown.Write(doc)
}

// readBounded reads r, refusing more than max bytes.
func readBounded(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("input is larger than %d bytes", max)
	}
	return b, nil
}

// RootCmd builds the command; tests drive it with SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docmd FILE",
		Short: "print a .docx, .pptx or .xlsx file as Markdown",
		Long: `docmd reads a Word, PowerPoint or Excel file and prints it as Markdown on
standard output. FILE is a path, or - for standard input.

The format comes from the package's content types, not the file name. An
encrypted package, a file that is not a ZIP and a ZIP that is not Office
Open XML are refused.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				data []byte
				err  error
			)
			if args[0] == "-" {
				data, err = readBounded(cmd.InOrStdin(), maxInput)
			} else {
				var f *os.File
				if f, err = os.Open(args[0]); err != nil {
					return err
				}
				defer f.Close()
				data, err = readBounded(f, maxInput)
			}
			if err != nil {
				return err
			}
			out, err := convert(data)
			if err != nil {
				return fmt.Errorf("%s: %w", args[0], err)
			}
			_, err = cmd.OutOrStdout().Write(out)
			return err
		},
	}
	return cmd
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	if err := RootCmd().Execute(); err != nil {
		osExit(1)
	}
}
