package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The "baseof" template is a third pre-kit landing shape (go-gfx, go-typeset,
// go-thumbnail, go-freedesktop, go-graphdrawing, cloud-boot): a
// layouts/_default/baseof.html (head, an inline theme script, nav and footer
// partials, an external static/css/main.css) and a layouts/index.html that is
// only `{{ define "main" }}`: a <section class="hero"> and sections of cards.
// The hero's title, tagline, intro and pills are landing data (hugo.toml
// params); only the parts every landing shares move onto the kit: the top bar,
// the Digital Commons band, the hero's button row, the strip, the footer.
var (
	baseofHeroLogoRe = regexp.MustCompile(`(?s)<img\s[^>]*class="hero-logo"[^>]*>`)
	footerInnerRe    = regexp.MustCompile(`(?s)<footer[^>]*>(.*?)</footer>`)
	mainLinkRe       = regexp.MustCompile(`(?s)<link\s+rel="stylesheet"\s+href=[^>]*css/main\.css[^>]*>`)
	defineMainRe     = regexp.MustCompile(`(?s)^\s*\{\{\s*define "main"\s*\}\}(.*)\{\{\s*end\s*\}\}\s*$`)
	baseofHeader     = regexp.MustCompile(`^(\.hero.*|nav.*|footer.*|\.container|\.nav-links.*|\.brand.*|\.theme-toggle.*|\.btn.*|\.cta|\.pills?.*|\.pill|body|html.*|\*|main.*)$`)
)

// baseofFiles are what a migration removes: the kit ships all of them.
var baseofFiles = []string{"layouts/_default/baseof.html", "layouts/partials/nav.html", "layouts/partials/footer.html", "static/css/main.css"}

// isBaseof says whether dir holds the shape.
func isBaseof(dir, index string) bool {
	if _, err := os.Stat(filepath.Join(dir, "layouts/_default/baseof.html")); err != nil {
		return false
	}
	return defineMainRe.MatchString(index)
}

// parseBaseof reads the shape from dir.
func parseBaseof(dir, index string) (landing, error) {
	var l landing
	read := func(p string) string {
		b, _ := os.ReadFile(filepath.Join(dir, p))
		return string(b)
	}
	baseof := read("layouts/_default/baseof.html")
	h := baseof
	if m := headRe.FindStringSubmatch(baseof); m != nil {
		h = m[1]
	}
	if m := titleRe.FindStringSubmatch(h); m != nil {
		l.Title = strings.TrimSpace(m[1])
	}
	l.Description = descRe.FindString(h)
	l.Favicon = extFaviconRe.FindString(h)
	m := defineMainRe.FindStringSubmatch(index)
	if m == nil {
		return l, fmt.Errorf("layouts/index.html is not a single define \"main\"")
	}
	body := m[1]
	hs, he, ok := span(body, `<section class="hero"`, "section")
	if !ok {
		return l, fmt.Errorf("no <section class=\"hero\"> in the main block")
	}
	hero := body[hs:he]
	l.Main = strings.TrimSpace(body[he:])
	rest := baseofHeroLogoRe.ReplaceAllString(hero, "")
	if hm := h1Re.FindStringSubmatch(rest); hm != nil {
		l.H1 = strings.TrimSpace(hm[1])
		rest = strings.Replace(rest, hm[0], "", 1)
	} else {
		return l, fmt.Errorf("no <h1> in the hero")
	}
	if cs, ce, ok := span(rest, `<div class="cta"`, "div"); ok {
		c := rest[cs:ce]
		for _, re := range []*regexp.Regexp{btnRe, btnRe2} {
			for _, b := range re.FindAllStringSubmatch(c, -1) {
				l.CTAs = append(l.CTAs, cta{Href: b[1], Inner: strings.TrimSpace(b[2])})
			}
		}
		rest = rest[:cs] + rest[ce:]
	}
	for _, open := range []struct{ o, tag string }{{`<div class="pills"`, "div"}, {`<p class="pills"`, "p"}} {
		if ps, pe, ok := span(rest, open.o, open.tag); ok {
			d := rest[ps:pe]
			l.PillsRaw = strings.TrimSpace(d[strings.Index(d, ">")+1 : len(d)-len("</"+open.tag+">")])
			rest = rest[:ps] + rest[pe:]
			break
		}
	}
	rest = regexp.MustCompile(`(?s)^\s*<section[^>]*>|</section>\s*$`).ReplaceAllString(strings.TrimSpace(rest), "")
	l.HeroExtra = strings.TrimSpace(hugoCmtRe.ReplaceAllString(rest, ""))
	if f := footerInnerRe.FindStringSubmatch(read("layouts/partials/footer.html")); f != nil {
		in := strings.TrimSpace(f[1])
		if strings.HasPrefix(in, "<div") {
			if g := strings.Index(in, ">"); g >= 0 {
				in = strings.TrimSpace(strings.TrimSuffix(in[g+1:], "</div>"))
			}
		}
		l.Footer = in
	}
	l.Style = baseofStyle(read("static/css/main.css"))
	return l, nil
}

// baseofStyle is main.css without the rules of what the kit draws (the top
// bar, the footer, the hero, the buttons); legacyCSS scopes what is left.
func baseofStyle(css string) string {
	var b strings.Builder
	for _, it := range cssItems(css) {
		sel := it.sel
		if !strings.HasPrefix(sel, "@") {
			var keep []string
			for _, s := range strings.Split(sel, ",") {
				if s = strings.TrimSpace(s); !baseofHeader.MatchString(s) {
					keep = append(keep, s)
				}
			}
			if len(keep) == 0 {
				continue
			}
			sel = strings.Join(keep, ",")
		}
		b.WriteString(sel + "{" + it.body + "}\n")
	}
	return b.String()
}

var brandStringRe = regexp.MustCompile(`(?m)^(\s*)brand(\s*=\s*")`)

// renameBrandParam renames a string param `brand` to `brandName`.
func renameBrandParam(conf string) (string, bool) {
	if !brandStringRe.MatchString(conf) {
		return conf, false
	}
	return brandStringRe.ReplaceAllString(conf, "${1}brandName${2}"), true
}
