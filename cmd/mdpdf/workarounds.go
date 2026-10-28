package main

// This file holds the one remaining workaround for a defect in the tools
// mdpdf is built on. Delete it (and its call in toBody) when the issue is
// closed and a release carrying the fix is required in go.mod.
//
//	swapCheckboxes  go-webengine/engine#322  a disabled <input type=checkbox>
//	                paints nothing
//
// No longer worked around:
//   - list markers, CSS escapes in content and counters: painted and decoded
//     by go-webengine v0.18.0 / go-pdfkit/html2pdf v0.3.0 (engine#320, #321);
//   - a pipe table after a list (go-kramdown/kramdown#12, PR #14, v0.3.1);
//   - "[x]" / "[ ]" task items (go-kramdown/kramdown#13, closed as not a bug:
//     task lists are GFM syntax, rendered under Options.Input = "GFM",
//     byte-identical to kramdown-parser-gfm).

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// swapCheckboxes replaces the disabled checkbox of a task item (kramdown GFM:
// <li class="task-list-item"><input type="checkbox" disabled [checked]>) with
// a ballot box character, because the engine lays the control out but does
// not paint it (go-webengine/engine#322).
func swapCheckboxes(fragment string) (string, error) {
	nodes, err := parseFragment(fragment)
	if err != nil {
		return "", err
	}
	for _, n := range nodes {
		swapIn(n, 0)
	}
	return renderNodes(nodes)
}

func parseFragment(fragment string) ([]*html.Node, error) {
	ctx := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	return html.ParseFragment(strings.NewReader(fragment), ctx)
}

func renderNodes(nodes []*html.Node) (string, error) {
	var b bytes.Buffer
	for _, n := range nodes {
		if err := html.Render(&b, n); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// maxDepth bounds the recursion of the tree walks. The HTML parser caps
// nesting itself at 512 open elements, so this is a second wall.
const maxDepth = 1024

func swapIn(n *html.Node, depth int) {
	if depth > maxDepth {
		return
	}
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode && c.DataAtom == atom.Input {
			if typ, _ := attr(c, "type"); typ == "checkbox" {
				box := "☐ "
				if _, checked := attr(c, "checked"); checked {
					box = "☑ "
				}
				n.InsertBefore(&html.Node{Type: html.TextNode, Data: box}, c)
				n.RemoveChild(c)
			}
		} else {
			swapIn(c, depth+1)
		}
		c = next
	}
}

func attr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}
