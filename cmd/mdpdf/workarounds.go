package main

// This file holds the four workarounds for defects in the tools mdpdf is
// built on. Each names the upstream issue; delete the function (and its call
// in toBody) when the issue is closed and a release carrying the fix is
// required in go.mod.
//
//	separateTables   go-kramdown/kramdown#12  a table after a list is swallowed
//	markLists        go-webengine/engine#320  no list markers are painted
//	                 go-webengine/engine#321  no CSS escapes in content, no counter()
//	task items       go-kramdown/kramdown#13  "[x]" / "[ ]" stay literal text
//	                 (handled inside markLists/prepend)

import (
	"bytes"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// separateTables ends the block before a pipe table that starts a new
// block: a blank line after plain text, and kramdown's end-of-block marker
// ("^" on its own line) after a list, which go-kramdown otherwise extends
// over the table even across a blank line.
//
// Workaround for go-kramdown/kramdown#12.
func separateTables(md string) string {
	lines := strings.Split(md, "\n")
	out := make([]string, 0, len(lines)+8)
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "|") && i > 0 && !strings.HasPrefix(strings.TrimSpace(lines[i-1]), "|") {
			prev := lastNonBlank(lines[:i])
			if isListItem(prev) {
				if strings.TrimSpace(lines[i-1]) != "" {
					out = append(out, "")
				}
				out = append(out, "^", "")
			} else if strings.TrimSpace(lines[i-1]) != "" {
				out = append(out, "")
			}
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func lastNonBlank(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}

// isListItem reports a line that opens a list item ("- ", "* ", "+ ", "1. ").
func isListItem(t string) bool {
	if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") || strings.HasPrefix(t, "+ ") {
		return true
	}
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	return i > 0 && strings.HasPrefix(t[i:], ". ")
}

// markLists writes each list item's marker as text at the start of the
// item ("• " in a <ul>, "1. " in an <ol>, honouring start=), because the
// layout engine paints no list markers (go-webengine/engine#320) and supports
// neither CSS escapes in content nor counter() (go-webengine/engine#321). It
// also turns task items ("[x] …", "[ ] …") into ballot boxes, which kramdown
// leaves as text (go-kramdown/kramdown#13).
func markLists(fragment string) (string, error) {
	ctx := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(fragment), ctx)
	if err != nil {
		return "", err
	}
	for _, n := range nodes {
		walk(n, 0)
	}
	var b bytes.Buffer
	for _, n := range nodes {
		if err := html.Render(&b, n); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// maxDepth bounds the recursion of walk. The HTML parser caps nesting itself
// at 512 open elements, so this is a second wall against a pathological tree.
const maxDepth = 1024

func walk(n *html.Node, depth int) {
	if depth > maxDepth {
		return
	}
	if n.Type == html.ElementNode && (n.DataAtom == atom.Ul || n.DataAtom == atom.Ol) {
		num := 1
		for _, a := range n.Attr {
			if a.Key == "start" {
				if v, err := strconv.Atoi(a.Val); err == nil {
					num = v
				}
			}
		}
		for li := n.FirstChild; li != nil; li = li.NextSibling {
			if li.Type != html.ElementNode || li.DataAtom != atom.Li {
				continue
			}
			marker := "•  "
			if n.DataAtom == atom.Ol {
				marker = strconv.Itoa(num) + ". "
				num++
			}
			prepend(li, marker)
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, depth+1)
	}
}

// taskBoxes maps a task item's prefix to its ballot box. A slice, not a map:
// the order is fixed.
var taskBoxes = []struct{ prefix, box string }{
	{"[x] ", "☑ "}, {"[X] ", "☑ "}, {"[ ] ", "☐ "},
}

// prepend puts the marker before the item's first text, inside its first
// paragraph when the item starts with one, so the marker shares that line.
func prepend(li *html.Node, marker string) {
	target := li
	c := li.FirstChild
	for c != nil && c.Type == html.TextNode && strings.TrimSpace(c.Data) == "" {
		c = c.NextSibling
	}
	if c != nil && c.Type == html.ElementNode && c.DataAtom == atom.P {
		target = c
	}
	// A task item ("[x] …", "[ ] …") gets a box instead of the bullet.
	if first := target.FirstChild; first != nil && first.Type == html.TextNode {
		for _, tb := range taskBoxes {
			if strings.HasPrefix(first.Data, tb.prefix) {
				first.Data = tb.box + first.Data[len(tb.prefix):]
				return
			}
		}
	}
	t := &html.Node{Type: html.TextNode, Data: marker}
	target.InsertBefore(t, target.FirstChild)
}
