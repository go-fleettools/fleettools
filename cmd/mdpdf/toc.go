package main

import (
	"fmt"
	htmlpkg "html"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// maxTOCEntries bounds the table of contents.
const maxTOCEntries = 2000

// insertTOC adds a table of contents, built from the h2 and h3 headings of the
// HTML fragment, in front of the first of them: after the title block (the h1
// and whatever follows it up to the first section). Each entry is a link to
// the heading's id (one is assigned when kramdown gave none) and carries its
// page number through CSS: `a::after { content: target-counter(attr(href
// url), page) }`, which go-pdfkit/html2pdf resolves (see css).
// A fragment with no h2/h3 comes back unchanged.
func insertTOC(fragment, title string) (string, error) {
	nodes, err := parseFragment(fragment)
	if err != nil {
		return "", err
	}
	type entry struct {
		level int
		id    string
		text  string
	}
	var (
		entries []entry
		first   *html.Node
		auto    int
	)
	var visit func(n *html.Node, depth int)
	visit = func(n *html.Node, depth int) {
		if depth > maxDepth || len(entries) >= maxTOCEntries {
			return
		}
		if n.Type == html.ElementNode && (n.DataAtom == atom.H2 || n.DataAtom == atom.H3) {
			text := strings.Join(strings.Fields(textOf(n, 0)), " ")
			if text != "" {
				id, ok := attr(n, "id")
				if !ok || id == "" {
					auto++
					id = fmt.Sprintf("toc-%d", auto)
					n.Attr = append(n.Attr, html.Attribute{Key: "id", Val: id})
				}
				level := 2
				if n.DataAtom == atom.H3 {
					level = 3
				}
				entries = append(entries, entry{level, id, text})
				if first == nil {
					first = n
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c, depth+1)
		}
	}
	for _, n := range nodes {
		visit(n, 0)
	}
	if len(entries) == 0 {
		return fragment, nil
	}

	var b strings.Builder
	// A two-column table: the title, and an empty link whose ::after is the
	// page number (CSS), right-aligned by the cell. The layout engine does not
	// float an inline's generated content, and a cell aligns it for free.
	b.WriteString(`<div class="toc"><p class="toc-title">` + htmlpkg.EscapeString(title) + `</p><table>`)
	for _, e := range entries {
		id := htmlpkg.EscapeString(e.id)
		fmt.Fprintf(&b, `<tr class="toc-item toc-l%d"><td class="toc-text"><a href="#%s">%s</a></td><td class="toc-num"><a href="#%s"></a></td></tr>`,
			e.level, id, htmlpkg.EscapeString(e.text), id)
	}
	b.WriteString(`</table></div>`)
	toc, err := parseFragment(b.String())
	if err != nil {
		return "", err
	}
	if first.Parent == nil {
		// A top-level heading: the nodes slice is the parent.
		out := make([]*html.Node, 0, len(nodes)+len(toc))
		for _, n := range nodes {
			if n == first {
				out = append(out, toc...)
			}
			out = append(out, n)
		}
		nodes = out
	} else {
		for _, n := range toc {
			first.Parent.InsertBefore(n, first)
		}
	}
	return renderNodes(nodes)
}

// textOf returns the text content of n.
func textOf(n *html.Node, depth int) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	if depth > maxDepth {
		return ""
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(textOf(c, depth+1))
	}
	return b.String()
}
