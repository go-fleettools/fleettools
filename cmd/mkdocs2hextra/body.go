// Copyright (c) 2026, the go-fleettools authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"fmt"
	"regexp"
	"strings"
)

// A conversion is what one page's body cost: what was carried over, and what
// was dropped. It is counted rather than described, because across 575 pages a
// description is unreadable and a count is checkable.
type conversion struct {
	Callouts  int
	Details   int
	Tabs      int
	Links     int
	AttrLists int
	Snippets  int
	Mermaid   int
}

func (c *conversion) add(o conversion) {
	c.Callouts += o.Callouts
	c.Details += o.Details
	c.Tabs += o.Tabs
	c.Links += o.Links
	c.AttrLists += o.AttrLists
	c.Snippets += o.Snippets
	c.Mermaid += o.Mermaid
}

func (c conversion) String() string {
	return fmt.Sprintf("%d callouts, %d details, %d tab sets, %d links, %d attribute lists dropped, %d snippets left, %d mermaid",
		c.Callouts, c.Details, c.Tabs, c.Links, c.AttrLists, c.Snippets, c.Mermaid)
}

var (
	// MkDocs Material: `!!! note "A title"`, and the collapsible `???`/`???+`.
	admonitionRE = regexp.MustCompile(`^(\s*)(!!!|\?\?\?\+?)\s+([a-zA-Z-]+)\s*(?:"([^"]*)")?\s*$`)
	// A content tab opens with `=== "label"`.
	tabRE = regexp.MustCompile(`^(\s*)===\s+"([^"]*)"\s*$`)
	// An attribute list is appended to a block: `{: .class #id }`.
	attrListRE = regexp.MustCompile(`\s*\{:\s*[^}]*\}\s*$`)
	// A link to another page of the same site, which Hugo does not serve under
	// that name.
	mdLinkRE = regexp.MustCompile(`\]\(([^)\s]+?\.md)(#[^)\s]*)?\)`)
	// A fence, so that none of the above is applied inside one.
	fenceRE = regexp.MustCompile("^\\s*(```|~~~)")
)

// calloutType maps MkDocs Material's admonition kinds onto the three Hextra
// has.
//
// ⛔ THE MAP IS NOT A GUESS AT WHICH KINDS EXIST: it is the census of all 85
// sites in this fleet — note 124, success 41, warning 33, info 5, tip 3,
// example 2, danger 1, plus 10 collapsible ones. Every kind that occurs is
// here, and anything else falls back to "info" rather than vanishing, because
// an unknown kind is still a box the author wanted.
//
// Hextra has info, warning and error. "success" has no colour of its own there,
// and it is the second commonest kind in the fleet — 41 of 209 — so it becomes
// info rather than being invented: a green box that renders as a grey one is a
// cosmetic loss, while a shortcode nobody defines is a build failure.
var calloutType = map[string]string{
	"note":      "info",
	"info":      "info",
	"tip":       "info",
	"hint":      "info",
	"success":   "info",
	"check":     "info",
	"question":  "info",
	"example":   "info",
	"abstract":  "info",
	"quote":     "info",
	"warning":   "warning",
	"caution":   "warning",
	"attention": "warning",
	"danger":    "error",
	"error":     "error",
	"bug":       "error",
	"failure":   "error",
}

// convertBody turns one MkDocs page's Markdown into Hextra's.
//
// ⛔ IT IS LINE-BASED AND FENCE-AWARE, and that pairing is the whole design. A
// Markdown-to-AST-to-Markdown round trip would reformat all 575 pages of this
// fleet — tables realigned, emphasis respelled, line breaks moved — and the
// diff of a migration has to be readable or nobody will review it. So every
// line that is not one of the five constructs below goes through UNTOUCHED,
// and a line inside a fenced code block is never one of the five: a shell
// example containing `=== "x"` is an example, not a tab.
func convertBody(src string) (string, conversion) {
	var (
		out   []string
		stats conversion
		fence string
	)
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")

	for i := 0; i < len(lines); {
		line := lines[i]

		// Inside a fence, nothing is converted, and the fence's own marker is
		// what closes it: a ``` does not close a ~~~.
		if fence != "" {
			out = append(out, line)
			if strings.HasPrefix(strings.TrimSpace(line), fence) {
				fence = ""
			}
			i++
			continue
		}
		if m := fenceRE.FindStringSubmatch(line); m != nil {
			fence = m[1]
			if strings.Contains(line, "mermaid") {
				stats.Mermaid++
			}
			out = append(out, line)
			i++
			continue
		}

		if m := admonitionRE.FindStringSubmatch(line); m != nil {
			block, next := indentedBlock(lines, i+1, m[1])
			converted, s := callout(m[2], m[3], m[4], block)
			out = append(out, converted...)
			stats.add(s)
			i = next
			continue
		}
		if tabRE.MatchString(line) {
			converted, s, next := tabSet(lines, i)
			out = append(out, converted...)
			stats.add(s)
			i = next
			continue
		}

		converted, s := convertInline(line)
		out = append(out, converted)
		stats.add(s)
		i++
	}
	return strings.Join(out, "\n"), stats
}

// convertInline is the per-line work: the links, and the attribute lists.
func convertInline(line string) (string, conversion) {
	var s conversion
	// ⛔ A LINK TO page.md BECOMES A relref, not a guessed path. Hugo serves
	// /page/ rather than /page.md, so every one of these 1 252 links in the
	// fleet is broken by the move — and rewriting them to a bare path would
	// move the breakage somewhere nobody looks. `relref` RESOLVES at build
	// time and FAILS the build when the target is not there, which is the only
	// version of this a migration can trust.
	line = mdLinkRE.ReplaceAllStringFunc(line, func(m string) string {
		parts := mdLinkRE.FindStringSubmatch(m)
		target, anchor := parts[1], parts[2]
		if strings.HasPrefix(target, "http") || strings.HasPrefix(target, "/") {
			return m
		}
		s.Links++
		// index.md is a section's own page, and it is called _index.md in Hugo.
		if target == "index.md" || strings.HasSuffix(target, "/index.md") {
			target = strings.TrimSuffix(target, "index.md") + "_index.md"
		}
		return fmt.Sprintf(`]({{< relref "%s%s" >}})`, target, anchor)
	})
	if attrListRE.MatchString(line) && !strings.HasPrefix(strings.TrimSpace(line), "|") {
		// ⛔ Dropped and COUNTED. attr_list is 84 of the 85 sites' extensions,
		// and what it carries is a class for a stylesheet that does not come
		// with the pages. Leaving the braces would print them.
		line = attrListRE.ReplaceAllString(line, "")
		s.AttrLists++
	}
	return line, s
}

// indentedBlock collects the lines belonging to a block opened at the given
// indent, and says where the block ends.
//
// ⛔ A BLANK LINE DOES NOT END IT. MkDocs admonitions routinely hold several
// paragraphs, and a reader that stopped at the first blank line would leave the
// rest of the box outside it — the words would still be on the page, in the
// wrong box, which is worse than losing them.
func indentedBlock(lines []string, from int, indent string) ([]string, int) {
	var block []string
	i := from
	for ; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			block = append(block, "")
			continue
		}
		if !strings.HasPrefix(line, indent+" ") && !strings.HasPrefix(line, indent+"\t") {
			break
		}
		block = append(block, strings.TrimPrefix(strings.TrimPrefix(line, indent), "    "))
	}
	// Trailing blank lines belong after the box, not inside it.
	for len(block) > 0 && block[len(block)-1] == "" {
		block = block[:len(block)-1]
		i--
	}
	return block, i
}

// callout renders an admonition as Hextra's shortcode, or as its details
// shortcode when the MkDocs marker was the collapsible `???`.
func callout(marker, kind, title string, block []string) ([]string, conversion) {
	var s conversion
	body := make([]string, 0, len(block))
	for _, line := range block {
		converted, inner := convertInline(line)
		body = append(body, converted)
		s.add(inner)
	}

	if strings.HasPrefix(marker, "???") {
		s.Details++
		if title == "" {
			title = strings.ToUpper(kind[:1]) + kind[1:]
		}
		// ⛔ The PERCENT form, `{{% %}}`, because the body is Markdown and has
		// to be rendered as such. With `{{< >}}` Hextra receives the text
		// verbatim and a list inside a collapsible comes out as one paragraph
		// of hyphens.
		open := fmt.Sprintf(`{{%% details title="%s" %%}}`, title)
		if strings.HasSuffix(marker, "+") {
			open = fmt.Sprintf(`{{%% details title="%s" closed="false" %%}}`, title)
		}
		return append(append([]string{open, ""}, body...), "", `{{% /details %}}`), s
	}

	s.Callouts++
	t, ok := calloutType[strings.ToLower(kind)]
	if !ok {
		t = "info"
	}
	open := fmt.Sprintf(`{{< callout type="%s" >}}`, t)
	if title != "" {
		// Hextra's callout has no title of its own, so the title becomes the
		// first line of the box, in bold — which is what Material draws.
		body = append([]string{"**" + title + "**", ""}, body...)
	}
	return append(append([]string{open}, body...), `{{< /callout >}}`), s
}

// tabSet turns a run of `=== "label"` blocks into one Hextra tabs shortcode.
func tabSet(lines []string, from int) ([]string, conversion, int) {
	var (
		s      conversion
		labels []string
		bodies [][]string
	)
	i := from
	for i < len(lines) {
		m := tabRE.FindStringSubmatch(lines[i])
		if m == nil {
			break
		}
		block, next := indentedBlock(lines, i+1, m[1])
		var body []string
		for _, line := range block {
			converted, inner := convertInline(line)
			body = append(body, converted)
			s.add(inner)
		}
		labels = append(labels, m[2])
		bodies = append(bodies, body)
		i = next
		// A blank line between two tabs is part of neither.
		for i < len(lines) && strings.TrimSpace(lines[i]) == "" && i+1 < len(lines) && tabRE.MatchString(lines[i+1]) {
			i++
		}
	}
	s.Tabs++
	// ⛔ "name" ON EACH TAB, not "items" on the set. The fork accepts items and
	// calls warnf about it — and the build runs with --panicOnWarning, so a
	// deprecation IS a failure. The first site with content tabs
	// (cloud-boot/docs, six sets) refused to build and nothing was committed
	// for it, which is the gate working rather than a setback.
	out := []string{`{{< tabs >}}`}
	for n, body := range bodies {
		out = append(out, "", fmt.Sprintf(`{{%% tab name="%s" %%}}`, labels[n]), "")
		out = append(out, body...)
		out = append(out, "", `{{% /tab %}}`)
	}
	return append(out, "", `{{< /tabs >}}`), s, i
}
