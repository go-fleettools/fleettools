// Copyright (c) 2026, the go-fleettools authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"fmt"
	"strings"
)

// A navEntry is one line of an MkDocs nav: a title and the page it names, or a
// title and the entries under it.
type navEntry struct {
	Title    string
	File     string
	Children []navEntry
}

// A site is what mkdocs.yml says, reduced to the parts a Hugo site needs.
type site struct {
	Name        string
	Description string
	URL         string
	RepoURL     string
	RepoName    string
	Nav         []navEntry
	Mike        bool // the site was versioned with mike
	EditURI     string
}

// readMkdocs reads the fields this converter uses out of an mkdocs.yml.
//
// ⛔ IT IS NOT A YAML PARSER, and that is a decision rather than a shortcut.
// The 85 files in this fleet are hand-written, two levels deep, and the only
// structure that matters is the nav; a real parser would pull in a dependency
// and still need this much code to interpret the result. What it CANNOT read
// it says so about — see the report — rather than filling in a default and
// looking finished.
func readMkdocs(src string) site {
	var s site
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "site_name:"):
			s.Name = unquote(after(line, "site_name:"))
		case strings.HasPrefix(line, "site_description:"):
			s.Description = unquote(after(line, "site_description:"))
		case strings.HasPrefix(line, "site_url:"):
			s.URL = unquote(after(line, "site_url:"))
		case strings.HasPrefix(line, "repo_url:"):
			s.RepoURL = unquote(after(line, "repo_url:"))
		case strings.HasPrefix(line, "repo_name:"):
			s.RepoName = unquote(after(line, "repo_name:"))
		case strings.HasPrefix(line, "edit_uri:"):
			s.EditURI = unquote(after(line, "edit_uri:"))
		case trimmed == "provider: mike":
			s.Mike = true
		case strings.HasPrefix(line, "nav:"):
			s.Nav = readNav(lines[i+1:])
		}
	}
	return s
}

// readNav reads the indented list under `nav:` until the next top-level key.
func readNav(lines []string) []navEntry {
	type level struct {
		indent  int
		entries *[]navEntry
	}
	var root []navEntry
	stack := []level{{indent: -1, entries: &root}}

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			break // a new top-level key: the nav is over
		}
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		for len(stack) > 1 && indent <= stack[len(stack)-1].indent {
			stack = stack[:len(stack)-1]
		}
		item := strings.TrimPrefix(trimmed, "- ")
		title, target, hasTarget := splitEntry(item)

		parent := stack[len(stack)-1].entries
		switch {
		case !hasTarget:
			// `- page.md` with no title: the page's own heading names it.
			*parent = append(*parent, navEntry{File: title})
		case target == "":
			// `- Section:` with children on the lines below.
			*parent = append(*parent, navEntry{Title: title})
			last := &(*parent)[len(*parent)-1]
			stack = append(stack, level{indent: indent, entries: &last.Children})
		default:
			*parent = append(*parent, navEntry{Title: title, File: target})
		}
	}
	return root
}

// splitEntry separates a nav item's title from the file it names.
//
// ⛔ THE TITLE MAY CONTAIN A COLON, and a real site is what said so:
// go-asmgen's nav has `- "Second architecture: riscv64": asmgen/riscv64.md`
// five times over. Cutting at the first colon gave the title `"Second
// architecture` and the file `riscv64": asmgen/riscv64.md`, so five pages were
// reported missing while sitting in docs/ — a converter reporting a defect in
// the thing it is converting, which is the worst way to be wrong.
//
// The quoting is YAML's: a title that needs a colon is quoted, so the closing
// quote is where to look for the separator.
func splitEntry(item string) (title, target string, hasTarget bool) {
	if len(item) > 0 && (item[0] == '"' || item[0] == '\'') {
		q := item[0]
		if end := strings.IndexByte(item[1:], q); end >= 0 {
			title = item[1 : 1+end]
			rest := strings.TrimSpace(item[2+end:])
			if after, ok := strings.CutPrefix(rest, ":"); ok {
				return title, unquote(strings.TrimSpace(after)), true
			}
			return title, "", false
		}
	}
	t, f, ok := strings.Cut(item, ":")
	return unquote(strings.TrimSpace(t)), unquote(strings.TrimSpace(f)), ok
}

func after(line, key string) string { return strings.TrimSpace(strings.TrimPrefix(line, key)) }

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

// hugoConfig writes the hugo.yaml for a converted site, modelled on
// go-plot/docs — the fleet's reference Hextra site.
//
// ⛔ THE VERSION IN THE baseURL IS THE POINT. 82 of the 85 MkDocs sites used
// mike, which publishes each documented version under its own path, and a
// migration that quietly dropped that would strand every link anybody has to
// /docs/0.3/. The CI reads params.docs.version and passes it to --baseURL, so
// a site that documents a release keeps a path per release and "main" until
// the first one.
func hugoConfig(s site, version string) string {
	base := strings.TrimSuffix(s.URL, "/")
	if base == "" {
		base = "https://example.github.io/docs"
	}
	title := s.Name
	if title == "" {
		title = "docs"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `# %s documentation — Hugo + Hextra, built like go-plot/docs and
# go-ndarray/docs.
#
# Each documentation version is a separate build under
# %s/<version>/, where <version> is the minor version of the
# code the pages describe, or "main" while no release is tagged. The CI sets it
# through --baseURL, from params.docs.version below; this default is the same
# build.
#
# ⛔ What this replaced was MkDocs Material plus mike, which needed a Python
# toolchain, a requirements.txt and a pip install in CI to render a static site
# for a pure-Go organisation.
baseURL: "%s/%s/"
title: "%s"
# ⛔ No languageCode: deprecated in Hugo 0.158 in favour of locale, and the
# languages block below already says which language this is. The second
# deprecation the pilot build refused, after cascade._target — both inherited
# from copying an older site in this fleet.

# themes/hextra is the tannevaled/hextra fork, pinned at a release tag as a
# submodule: upstream Hextra plus opt-in features (partial menu items, combos,
# related-sites menu, page subtitle and history, darkreaderLock).
theme: hextra

defaultContentLanguage: en
defaultContentLanguageInSubdir: false

languages:
  en:
    label: "English"
    contentDir: content/en
    weight: 1

enableRobotsTXT: true
# Git history dates every page: "Last updated on" at the foot of each one.
enableGitInfo: true

markup:
  goldmark:
    renderer:
      unsafe: true
    parser:
      attribute:
        title: true
  highlight:
    noClasses: false

# ⛔ The content is NOT under content/en/docs. This site is served AT /docs/,
# so a "docs" section would make every page /docs/docs/… . Hextra picks its
# documentation layout from the section being called "docs", so with the
# content at the root the type is cascaded instead.
# ⛔ "target", not "_target": the underscored spelling was deprecated in Hugo
# 0.156 and the fleet's older Hextra sites still carry it, so copying one of
# them is how a converted site arrives with a deprecation. The build runs with
# --panicOnWarning, which is what caught this on the first pilot.
cascade:
  - target:
      path: "/**"
    params:
      type: docs

params:
  docs:
    version: "%s"
  description: "%s"
  navbar:
    displayTitle: true
    displayLogo: false
  theme:
    default: system
    displayToggle: true
  footer:
    enable: true
    displayCopyright: false
    displayPoweredBy: false
  page:
    width: normal
`, title, base, base, version, title, version, s.Description)

	if s.RepoURL != "" {
		fmt.Fprintf(&b, `  editURL:
    enable: true
    base: "%s/edit/main/content/en"
`, strings.TrimSuffix(s.RepoURL, "/"))
	}
	return b.String()
}

// frontMatter is a converted page's header.
func frontMatter(title string, weight int) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %q\n", title)
	if weight > 0 {
		fmt.Fprintf(&b, "weight: %d\n", weight)
	}
	b.WriteString("---\n\n")
	return b.String()
}
