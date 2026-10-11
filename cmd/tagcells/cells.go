package main

import (
	"regexp"
	"strings"
)

// Cell is one place a page states a module's version.
type Cell struct {
	Line   int    // 1-based
	Module string // the repository name, which is the module's last element
	Tag    string // what the page says
	start  int    // byte offsets of the tag in the file
	end    int
}

// mdRowRE is a Markdown table row that links the module's repository and puts
// its tag in the next cell, as go-widgets/docs' modules page does in every
// language:
//
//	| [`window`](https://github.com/go-widgets/window) | v0.89.0 | ... |
var mdRowRE = regexp.MustCompile("\\| \\[`([A-Za-z0-9._-]+)`\\]\\(https://github\\.com/([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)\\) \\| (v[0-9]+\\.[0-9]+\\.[0-9]+) \\|")

// tomlNameRE and tomlTagRE find a landing page's module block:
//
//	[[params.modules]]
//	  name = "window"
//	  layer = "surface"
//	  tag = "v0.89.0"
//
// A tag belongs to the nearest name above it inside the same block; a new
// [[...]] header ends the block, so a tag is never credited to the wrong name.
var (
	tomlHeaderRE = regexp.MustCompile(`^\s*\[\[`)
	tomlNameRE   = regexp.MustCompile(`^\s*name\s*=\s*"([A-Za-z0-9._-]+)"`)
	tomlTagRE    = regexp.MustCompile(`^(\s*tag\s*=\s*")(v[0-9]+\.[0-9]+\.[0-9]+)(")`)
)

// Find returns every version cell in a file, for repositories of org.
// A Markdown row naming another organisation's repository is not this
// organisation's to update, and is left alone.
func Find(src, org string, toml bool) []Cell {
	if toml {
		return findTOML(src)
	}
	var cells []Cell
	for _, m := range mdRowRE.FindAllStringSubmatchIndex(src, -1) {
		name, owner, repo := src[m[2]:m[3]], src[m[4]:m[5]], src[m[6]:m[7]]
		if owner != org || repo != name {
			continue
		}
		cells = append(cells, Cell{
			Line: strings.Count(src[:m[0]], "\n") + 1, Module: name,
			Tag: src[m[8]:m[9]], start: m[8], end: m[9],
		})
	}
	return cells
}

func findTOML(src string) []Cell {
	var cells []Cell
	name, off := "", 0
	for i, line := range strings.SplitAfter(src, "\n") {
		switch {
		case tomlHeaderRE.MatchString(line):
			name = ""
		case tomlNameRE.MatchString(line):
			name = tomlNameRE.FindStringSubmatch(line)[1]
		case name != "" && tomlTagRE.MatchString(line):
			m := tomlTagRE.FindStringSubmatchIndex(line)
			cells = append(cells, Cell{Line: i + 1, Module: name, Tag: line[m[4]:m[5]], start: off + m[4], end: off + m[5]})
			name = ""
		}
		off += len(line)
	}
	return cells
}

// Rewrite returns src with each cell set to want[cell.Module], when that is
// known and different. Offsets are applied from the end so earlier ones stay
// valid.
func Rewrite(src string, cells []Cell, want map[string]string) string {
	out := src
	for i := len(cells) - 1; i >= 0; i-- {
		c := cells[i]
		if t, ok := want[c.Module]; ok && t != "" && t != c.Tag {
			out = out[:c.start] + t + out[c.end:]
		}
	}
	return out
}
