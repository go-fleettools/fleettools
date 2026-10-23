package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// page is what a reader gets from a built page: its visible text, as text
// nodes and as one string, and the links and pictures it points at.
type page struct {
	Nodes []string
	Text  string
	Strip []string // the text of the mantra strip's chips, kept apart
	Hrefs map[string]bool
	Srcs  map[string]bool
}

// readPage tokenises a built page. Scripts, styles and the template's own
// comments are not text. The mantra strip (landingkit v0.7.0, an element of
// class "mantra") IS text, and must be: its chips are collected apart, so
// "said once" is measured on the rest of the page and the strip itself is
// judged on its own. Up to v0.6.0 the strip was CSS generated content,
// which this reads as no strip at all, exactly as a reader's tools did.
func readPage(r io.Reader) (page, error) {
	p := page{Hrefs: map[string]bool{}, Srcs: map[string]bool{}}
	z := html.NewTokenizer(r)
	skip := 0
	strip := 0 // element depth inside the strip; 0 outside it
	var all strings.Builder
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if z.Err() == io.EOF {
				p.Text = norm(all.String())
				return p, nil
			}
			return p, z.Err()
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			if (t.Data == "script" || t.Data == "style" || t.Data == "noscript") && tt == html.StartTagToken {
				skip++
			}
			if tt == html.StartTagToken {
				switch {
				case strip > 0:
					strip++
				case hasClass(t, "mantra"):
					strip = 1
				}
			}
			for _, a := range t.Attr {
				switch {
				case a.Key == "href" && t.Data == "a":
					p.Hrefs[a.Val] = true
				case a.Key == "src" && t.Data == "img":
					p.Srcs[a.Val] = true
				}
			}
		case html.EndTagToken:
			t := z.Token()
			if (t.Data == "script" || t.Data == "style" || t.Data == "noscript") && skip > 0 {
				skip--
			}
			if strip > 0 {
				strip--
			}
		case html.TextToken:
			if skip > 0 {
				continue
			}
			s := string(z.Text())
			if strip > 0 {
				if n := norm(s); n != "" {
					p.Strip = append(p.Strip, n)
				}
				continue
			}
			all.WriteString(s)
			if n := norm(s); len(n) >= 3 && strings.IndexFunc(n, isLetter) >= 0 {
				p.Nodes = append(p.Nodes, n)
			}
		}
	}
}

// hasClass says whether a tag carries class c among its classes.
func hasClass(t html.Token, c string) bool {
	for _, a := range t.Attr {
		if a.Key == "class" {
			for _, f := range strings.Fields(a.Val) {
				if f == c {
					return true
				}
			}
		}
	}
	return false
}

func isLetter(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 0x7f }

func norm(s string) string { return strings.Join(strings.Fields(s), " ") }

// verdict of a migration: what the old page said and the new one does not.
type verdict struct {
	Lost      []string // text of the old page the new one (strip off) lacks
	LostLinks []string // links and pictures likewise
	Shown     []string // hidden repeats the page with the strip still shows
	Extra     []string // what the strip-on page lacks beyond the hidden repeats
	NoStrip   []string // strip values that are not TEXT on the strip-on page
}

func (v verdict) ok() bool {
	return len(v.Lost) == 0 && len(v.LostLinks) == 0 && len(v.Shown) == 0 && len(v.Extra) == 0 && len(v.NoStrip) == 0
}

// stripItems are the values the strip must say: the code's two, then the
// data chips the migration added.
func stripItems(data []string) []string {
	return append([]string{"LICENSE=BSD-3-Clause", "CGO_ENABLED=0"}, data...)
}

// licenceBadge is the one picture a migration may drop (it says the strip's
// licence): shields.io's licence badge.
func licenceBadge(src string) bool {
	s := strings.ToLower(src)
	return strings.Contains(s, "/badge/licen")
}

// judge compares the old page with the new one built twice: with the strip
// (what readers get) and without it (so the hidden repeats render).
//   - everything the old page said, the new page without the strip says;
//   - every link and picture of the old page is still there;
//   - with the strip on, the new page says exactly the same minus the
//     hidden repeats, and none of them.
func judge(old, on, off page, frags []fragment, want []string) verdict {
	var v verdict
	// the strip says its values as text: a chip that is only drawn (CSS
	// generated content, an image) is not said at all
	have := map[string]bool{}
	for _, s := range on.Strip {
		have[s] = true
	}
	for _, w := range want {
		if !have[w] {
			v.NoStrip = append(v.NoStrip, w)
		}
	}
	for _, n := range old.Nodes {
		if !strings.Contains(off.Text, n) {
			v.Lost = append(v.Lost, n)
		}
	}
	for h := range old.Hrefs {
		if !off.Hrefs[h] {
			v.LostLinks = append(v.LostLinks, "href "+h)
		}
	}
	for s := range old.Srcs {
		if !off.Srcs[s] && !licenceBadge(s) {
			v.LostLinks = append(v.LostLinks, "src "+s)
		}
	}
	// The strip-on page must be the strip-off page with hidden repeats cut
	// out, and nothing else. Compared as character streams without space (a
	// cut shifts spaces and glues neighbouring words), walked together: where
	// they part, a hidden repeat must sit in the strip-off stream at that
	// point (or just before it, when its first characters equal what follows
	// it) for the walk to resume after it. Text alone cannot say WHICH
	// "CGO_ENABLED=0" was hidden, a pill or the one in a kept sentence; the
	// position can.
	squash := func(s string) string { return strings.Join(strings.Fields(s), "") }
	var cuts []string
	for _, f := range frags {
		if t := squash(plain(f.HTML)); f.Hidden && t != "" {
			cuts = append(cuts, t)
			if !strings.Contains(squash(off.Text), t) {
				v.Lost = append(v.Lost, "hidden repeat not even shown without the strip: "+t)
			}
		}
	}
	sort.SliceStable(cuts, func(i, j int) bool { return len(cuts[i]) > len(cuts[j]) })
	a, b := squash(off.Text), squash(on.Text)
	used := map[string]int{}
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		if i < len(a) && j < len(b) && a[i] == b[j] {
			i, j = i+1, j+1
			continue
		}
		cut := false
		for _, c := range cuts {
			// the cut may begin fewer than len(c) characters before the
			// parting (it always moves the walk forward in the off stream)
			for back := 0; back < len(c) && back <= i && back <= j; back++ {
				p := i - back
				if strings.HasPrefix(a[p:], c) && a[p:i] == b[j-back:j] {
					i, j = p+len(c), j-back
					used[c]++
					cut = true
					break
				}
			}
			if cut {
				break
			}
		}
		if !cut {
			lo := max(0, i-40)
			v.Extra = append(v.Extra, fmt.Sprintf("the pages part at %q (strip off) / %q (strip on)",
				a[lo:min(len(a), i+40)], b[max(0, j-40):min(len(b), j+40)]))
			break
		}
	}
	needed := map[string]int{}
	for _, c := range cuts {
		needed[c]++
	}
	for c, n := range needed {
		if used[c] < n && len(v.Extra) == 0 {
			v.Shown = append(v.Shown, c)
		}
	}
	sort.Strings(v.Shown)
	sort.Strings(v.Lost)
	sort.Strings(v.LostLinks)
	return v
}
