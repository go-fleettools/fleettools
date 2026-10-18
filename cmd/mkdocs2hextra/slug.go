// Copyright (c) 2026, the go-fleettools authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Heading anchors, which are a URL and therefore not ours to change.
//
// ⛔ THE TWO SLUG RULES DISAGREE, and a site's own links are written against
// MkDocs'. Measured on go-ruby-cgi/docs, heading
// "### The HTML-escaper rewrite — how Go overtakes the C extension":
//
//	MkDocs  the-html-escaper-rewrite-how-go-overtakes-the-c-extension
//	Hugo    the-html-escaper-rewrite--how-go-overtakes-the-c-extension
//
// Python-Markdown drops the em dash and then collapses the run of spaces it
// leaves into ONE hyphen; goldmark keeps one hyphen per space. So every
// in-page `#anchor` on that site pointed at nothing, and so would every
// bookmark and every link from outside.
//
// ⛔ THE ANSWER IS TO KEEP THE OLD ANCHOR, not to rewrite the links. A
// converted page carries an explicit id — `## Heading {#the-mkdocs-slug}` —
// so the URL somebody saved last year still arrives at the same heading. The
// alternative, rewriting every link to Hugo's spelling, fixes the pages and
// breaks everything outside them.
//
// This needs `markup.goldmark.parser.attribute.block` in hugo.yaml, which is
// why that is set there.

var (
	headingRE = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*$`)
	hasID     = regexp.MustCompile(`\{#[^}]+\}\s*$`)
	// Inline markup that is not part of the heading's text.
	mdLinkTextRE = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdMarkersRE  = regexp.MustCompile("[*_`~]")
	htmlTagRE    = regexp.MustCompile(`<[^>]+>`)
	runsRE       = regexp.MustCompile(`[-\s]+`)
	dropRE       = regexp.MustCompile(`[^\w\s-]`)
)

// mkdocsSlug is Python-Markdown's slugify, which is what MkDocs Material uses
// for its table of contents and therefore what every hand-written anchor on
// these sites was written against:
//
//	normalise to NFKD and drop what is not ASCII, remove anything that is not
//	a word character, a space or a hyphen, lowercase, then collapse every run
//	of hyphens and spaces into one hyphen.
func mkdocsSlug(heading string) string {
	text := mdLinkTextRE.ReplaceAllString(heading, "$1")
	text = htmlTagRE.ReplaceAllString(text, "")
	text = mdMarkersRE.ReplaceAllString(text, "")

	// NFKD and "drop what is not ASCII", which is what makes an em dash
	// disappear rather than become a hyphen.
	var ascii strings.Builder
	for _, r := range text {
		if r < unicode.MaxASCII {
			ascii.WriteRune(r)
		}
	}
	s := strings.ToLower(strings.TrimSpace(dropRE.ReplaceAllString(ascii.String(), "")))
	return strings.Trim(runsRE.ReplaceAllString(s, "-"), "-")
}

// anchors gives every heading in a page the id MkDocs gave it.
//
// ⛔ A REPEATED HEADING IS NUMBERED THE WAY MkDocs NUMBERS IT: the second
// "## Options" is `options_1`, not `options-1` (goldmark's spelling). The
// numbering is part of the URL too.
func anchors(lines []string) []string {
	seen := map[string]int{}
	out := make([]string, len(lines))
	fence := ""
	for i, line := range lines {
		out[i] = line
		if fence != "" {
			if strings.HasPrefix(strings.TrimSpace(line), fence) {
				fence = ""
			}
			continue
		}
		if m := fenceRE.FindStringSubmatch(line); m != nil {
			fence = m[1]
			continue
		}
		m := headingRE.FindStringSubmatch(line)
		if m == nil || hasID.MatchString(line) {
			continue
		}
		slug := mkdocsSlug(m[2])
		if slug == "" {
			continue
		}
		if n := seen[slug]; n > 0 {
			seen[slug] = n + 1
			slug = fmt.Sprintf("%s_%d", slug, n)
		} else {
			seen[slug] = 1
		}
		out[i] = fmt.Sprintf("%s %s {#%s}", m[1], m[2], slug)
	}
	return out
}
