package main

import (
	"fmt"
	htmlpkg "html"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/text/unicode/norm"
)

// maxTOCEntries bounds the table of contents.
const maxTOCEntries = 2000

// maxListedHeadings bounds the headings an "unknown heading" error names.
const maxListedHeadings = 40

// headingLevel returns 1..6 for an h1..h6 element, else 0.
func headingLevel(n *html.Node) int {
	if n.Type != html.ElementNode {
		return 0
	}
	switch n.DataAtom {
	case atom.H1:
		return 1
	case atom.H2:
		return 2
	case atom.H3:
		return 3
	case atom.H4:
		return 4
	case atom.H5:
		return 5
	case atom.H6:
		return 6
	}
	return 0
}

// normText trims, collapses white space and NFC-normalises s.
func normText(s string) string {
	return norm.NFC.String(strings.Join(strings.Fields(s), " "))
}

// slug lower-cases s and joins its letters and digits with single hyphens.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(normText(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		} else {
			dash = true
		}
	}
	return b.String()
}

// insertTOC adds a table of contents, built from the h2 and h3 headings of the
// HTML fragment. Each entry is a link to the heading's id (one is assigned
// when kramdown gave none) and carries its page number through CSS:
// `a::after { content: target-counter(attr(href url), page) }`, which
// go-pdfkit/html2pdf resolves.
//
// Where it goes, in order of precedence:
//  1. after != "": at the end of the section headed after (the heading whose
//     NFC-normalised text equals it, case-sensitively; else one whose slug or
//     id equals it): just before the next heading of the same or a higher
//     level, or at the end of the parent when there is none. An unknown
//     heading is an error naming the headings there are.
//  2. a `<!-- toc -->` comment in the fragment: in its place.
//  3. in front of the first h2/h3, after the title block.
//
// The entries always list every h2 and h3, the named heading included. A
// fragment with no h2/h3 comes back unchanged.
func insertTOC(fragment, title, after string) (string, error) {
	nodes, err := parseFragment(fragment)
	if err != nil {
		return "", err
	}
	// A synthetic parent, so top-level and nested nodes are handled alike.
	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	for _, n := range nodes {
		root.AppendChild(n)
	}

	type entry struct {
		level int
		id    string
		text  string
	}
	var (
		entries  []entry
		first    *html.Node   // first h2/h3
		headings []*html.Node // every h1..h6, document order
		marker   *html.Node
		auto     int
	)
	var visit func(n *html.Node, depth int)
	visit = func(n *html.Node, depth int) {
		if depth > maxDepth {
			return
		}
		if n.Type == html.CommentNode && marker == nil && strings.EqualFold(strings.TrimSpace(n.Data), "toc") {
			marker = n
		}
		if lv := headingLevel(n); lv > 0 {
			headings = append(headings, n)
			text := normText(textOf(n, 0))
			if (lv == 2 || lv == 3) && text != "" && len(entries) < maxTOCEntries {
				id, ok := attr(n, "id")
				if !ok || id == "" {
					auto++
					id = fmt.Sprintf("toc-%d", auto)
					n.Attr = append(n.Attr, html.Attribute{Key: "id", Val: id})
				}
				entries = append(entries, entry{lv, id, text})
				if first == nil {
					first = n
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c, depth+1)
		}
	}
	visit(root, 0)

	// Resolve the named heading first, so an unknown one is an error even
	// when the document has nothing to list.
	var named *html.Node
	if after != "" {
		want := normText(after)
		for _, h := range headings {
			if normText(textOf(h, 0)) == want {
				named = h
				break
			}
		}
		if named == nil {
			ws := slug(after)
			for _, h := range headings {
				id, _ := attr(h, "id")
				if ws != "" && (slug(textOf(h, 0)) == ws || id == after) {
					named = h
					break
				}
			}
		}
		if named == nil {
			var names []string
			for i, h := range headings {
				if i == maxListedHeadings {
					names = append(names, "…")
					break
				}
				names = append(names, fmt.Sprintf("%q", normText(textOf(h, 0))))
			}
			return "", fmt.Errorf("--toc-after %q: no such heading; the headings are: %s", after, strings.Join(names, ", "))
		}
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

	switch {
	case named != nil:
		// Before the next heading of the same or a higher level, among the
		// named heading's siblings; at the end of its parent otherwise.
		parent, lv := named.Parent, headingLevel(named)
		var before *html.Node
		for s := named.NextSibling; s != nil; s = s.NextSibling {
			if l := headingLevel(s); l > 0 && l <= lv {
				before = s
				break
			}
		}
		for _, n := range toc {
			parent.InsertBefore(n, before) // a nil before appends
		}
	case marker != nil:
		for _, n := range toc {
			marker.Parent.InsertBefore(n, marker)
		}
		marker.Parent.RemoveChild(marker)
	default:
		for _, n := range toc {
			first.Parent.InsertBefore(n, first)
		}
	}
	return renderNodes(childrenOf(root))
}

func childrenOf(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		out = append(out, c)
	}
	return out
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
