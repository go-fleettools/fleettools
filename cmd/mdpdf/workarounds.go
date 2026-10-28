package main

// This file holds the workarounds for defects in the tools mdpdf is built
// on. Each names the upstream issue; delete the code (and its call in toBody)
// when the issue is closed and a release carrying the fix is required in
// go.mod.
//
//	markLists  go-webengine/engine#320  no list markers are painted
//	           go-webengine/engine#321  no CSS escapes in content, no counter()
//	task boxes go-webengine/engine#322  a disabled <input type=checkbox> paints
//	           nothing
//
// No longer worked around: a pipe table after a list (go-kramdown/kramdown#12,
// fixed by PR #14 in v0.3.1) and "[x]" / "[ ]" task items (go-kramdown/
// kramdown#13, closed as not a bug: task lists are GFM syntax, which kramdown
// renders under Options.Input = "GFM", byte-identical to kramdown-parser-gfm).

import (
	"bytes"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// markLists writes each list item's marker as text at the start of the
// item ("• " in a <ul>, "1. " in an <ol>, honouring start=), because the
// layout engine paints no list markers (go-webengine/engine#320) and supports
// neither CSS escapes in content nor counter() (go-webengine/engine#321). A
// task item (kramdown GFM: <li class="task-list-item"><input type="checkbox"
// disabled [checked]>) gets a ballot box in place of the checkbox, which the
// engine lays out but does not paint, and no bullet.
func markLists(fragment string) (string, error) {
	nodes, err := parseFragment(fragment)
	if err != nil {
		return "", err
	}
	for _, n := range nodes {
		walk(n, 0)
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

func attr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

// prepend puts the marker before the item's first content, inside its first
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
	// A task item: swap the checkbox for a ballot box.
	if in := target.FirstChild; in != nil && in.Type == html.ElementNode && in.DataAtom == atom.Input {
		if typ, _ := attr(in, "type"); typ == "checkbox" {
			box := "☐ "
			if _, checked := attr(in, "checked"); checked {
				box = "☑ "
			}
			target.InsertBefore(&html.Node{Type: html.TextNode, Data: box}, in)
			target.RemoveChild(in)
			return
		}
	}
	target.InsertBefore(&html.Node{Type: html.TextNode, Data: marker}, target.FirstChild)
}
