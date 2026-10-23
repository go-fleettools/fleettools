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
	Hrefs map[string]bool
	Srcs  map[string]bool
}

// readPage tokenises a built page. Scripts, styles and the template's own
// comments are not text; the mantra strip is CSS generated content and is
// not text either, which is why "said once" is measured on what is left.
func readPage(r io.Reader) (page, error) {
	p := page{Hrefs: map[string]bool{}, Srcs: map[string]bool{}}
	z := html.NewTokenizer(r)
	skip := 0
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
		case html.TextToken:
			if skip > 0 {
				continue
			}
			s := string(z.Text())
			all.WriteString(s)
			if n := norm(s); len(n) >= 3 && strings.IndexFunc(n, isLetter) >= 0 {
				p.Nodes = append(p.Nodes, n)
			}
		}
	}
}

func isLetter(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 0x7f }

func norm(s string) string { return strings.Join(strings.Fields(s), " ") }

// verdict of a migration: what the old page said and the new one does not.
type verdict struct {
	Lost      []string // text of the old page the new one (strip off) lacks
	LostLinks []string // links and pictures likewise
	Shown     []string // hidden repeats the page with the strip still shows
	Extra     []string // what the strip-on page lacks beyond the hidden repeats
}

func (v verdict) ok() bool {
	return len(v.Lost) == 0 && len(v.LostLinks) == 0 && len(v.Shown) == 0 && len(v.Extra) == 0
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
func judge(old, on, off page, frags []fragment) verdict {
	var v verdict
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
	want := map[string]int{}
	for _, c := range cuts {
		want[c]++
	}
	for c, n := range want {
		if used[c] < n && len(v.Extra) == 0 {
			v.Shown = append(v.Shown, c)
		}
	}
	sort.Strings(v.Shown)
	sort.Strings(v.Lost)
	sort.Strings(v.LostLinks)
	return v
}
