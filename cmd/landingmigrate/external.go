package main

import (
	"fmt"
	"regexp"
	"strings"
)

// The "external" template is the fleet's second pre-kit landing shape (go-commonmark,
// go-odf, go-mastodon, ...): the page's rules live in static/css/brand.css and
// its theme script in static/js/theme.js, the hero is a <section class="hero">
// with the logo, h1, a paragraph and a div.btns, and the body is one
// <div class="wrap"> then a <footer>. It has no <style> in its <head>, which
// is why parseTemplate refuses it.
var (
	// the hrefs are Hugo expressions, `{{ "css/brand.css" | relURL }}`, whose
	// quotes a `[^"]*` value does not survive (found on the real go-commonmark)
	extLinkRe    = regexp.MustCompile(`(?s)<link\s+rel="stylesheet"\s+href=[^>]*css/brand\.css[^>]*>`)
	extScriptRe  = regexp.MustCompile(`(?s)<script\s+src=[^>]*js/theme\.js[^>]*>\s*</script>`)
	extFaviconRe = regexp.MustCompile(`(?s)<link\s+rel="icon"[^>]*>`)
	extBtnRe     = regexp.MustCompile(`(?s)<button[^>]*id="themebtn"[^>]*>.*?</button>`)
	extImgRe     = regexp.MustCompile(`(?s)<img\s[^>]*>`)
	extPRe       = regexp.MustCompile(`(?s)<p>(.*?)</p>`)
	cssVarRe     = regexp.MustCompile(`(?i)--(from|to)\s*:\s*(#[0-9a-f]{6})`)
)

// isExternal says whether a layout links its rules from css/brand.css.
func isExternal(src string) bool { return extLinkRe.MatchString(src) }

// parseExternal reads layouts/index.html and static/css/brand.css of an
// external-stylesheet landing. Anything in the page it does not know is a
// refusal, as for parseTemplate.
func parseExternal(src, css string) (landing, error) {
	var l landing
	head := headRe.FindStringSubmatch(src)
	body := bodyRe.FindStringSubmatch(src)
	if head == nil || body == nil {
		return l, fmt.Errorf("no <head> or no <body>")
	}
	h := head[1]
	if m := titleRe.FindStringSubmatch(h); m != nil {
		l.Title = strings.TrimSpace(m[1])
	}
	l.Description = descRe.FindString(h)
	l.Favicon = extFaviconRe.FindString(h)
	rest := h
	for _, re := range []*regexp.Regexp{titleRe, descRe, extFaviconRe, extLinkRe, extScriptRe, hugoCmtRe,
		regexp.MustCompile(`<meta\s+charset[^>]*>`), regexp.MustCompile(`<meta\s+name="viewport"[^>]*>`),
		regexp.MustCompile(`<meta\s+name="darkreader-lock"[^>]*>`)} {
		rest = re.ReplaceAllString(rest, "")
	}
	l.HeadExtra = strings.TrimSpace(rest)
	l.Style = externalStyle(css)

	b := body[1]
	hs, he, ok := span(b, `<section class="hero"`, "section")
	if !ok {
		return l, fmt.Errorf("no <section class=\"hero\">")
	}
	hero := b[hs:he]
	left := hero
	left = extBtnRe.ReplaceAllString(left, "")
	if m := extImgRe.FindString(left); m != "" {
		left = strings.Replace(left, m, "", 1) // the kit draws the logo from the favicon
	}
	if m := h1Re.FindStringSubmatch(left); m != nil {
		l.H1 = strings.TrimSpace(m[1])
		left = strings.Replace(left, m[0], "", 1)
	} else {
		return l, fmt.Errorf("no <h1> in the hero")
	}
	if cs, ce, ok := span(left, `<div class="btns"`, "div"); ok {
		c := left[cs:ce]
		for _, m := range btnRe.FindAllStringSubmatch(c, -1) {
			l.CTAs = append(l.CTAs, cta{Href: m[1], Inner: strings.TrimSpace(m[2])})
		}
		left = left[:cs] + left[ce:]
	}
	if m := extPRe.FindStringSubmatch(left); m != nil {
		l.Tagline = strings.TrimSpace(m[1])
		left = strings.Replace(left, m[0], "", 1)
	}
	left = regexp.MustCompile(`(?s)^<section[^>]*>|</section>$`).ReplaceAllString(strings.TrimSpace(left), "")
	if strings.TrimSpace(hugoCmtRe.ReplaceAllString(left, "")) != "" {
		return l, fmt.Errorf("the hero holds something this tool does not know: %q", clip(strings.TrimSpace(left)))
	}

	ws, we, ok := span(b, `<div class="wrap"`, "div")
	if !ok {
		return l, fmt.Errorf("no <div class=\"wrap\">")
	}
	w := b[ws:we]
	l.Main = w[strings.Index(w, ">")+1 : len(w)-len("</div>")]
	fs, fe, fok := span(b, `<footer`, "footer")
	if fok {
		f := b[fs:fe]
		l.Footer = strings.TrimSpace(f[strings.Index(f, ">")+1 : len(f)-len("</footer>")])
	}
	r := b[:hs] + b[he:ws] + b[we:]
	if fok {
		r = strings.Replace(r, b[fs:fe], "", 1)
	}
	r = hugoCmtRe.ReplaceAllString(r, "")
	r = scriptRe.ReplaceAllString(r, "")
	if strings.TrimSpace(r) != "" {
		return l, fmt.Errorf("the body holds something this tool does not know: %q", clip(strings.TrimSpace(r)))
	}
	return l, nil
}

// externalHeader are the rules of what the kit now draws.
var externalHeader = regexp.MustCompile(`^(\.hero\b.*|\.btns|\.btn-solid|\.theme|body|\*|\.wrap)$`)

// externalStyle is the stylesheet without the rules of the hero, the theme
// button and the page frame; what is left styles <main> (legacyCSS scopes it).
func externalStyle(css string) string {
	var b strings.Builder
	for _, it := range cssItems(css) {
		sel := it.sel
		if !strings.HasPrefix(sel, "@") {
			var keep []string
			for _, s := range strings.Split(sel, ",") {
				if s = strings.TrimSpace(s); !externalHeader.MatchString(s) {
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

// stylesheetBrand are the --from (the bright stop) and --to (the base) of
// brand.css, lower-case.
func stylesheetBrand(css string) (from, to string) {
	for _, m := range cssVarRe.FindAllStringSubmatch(css, -1) {
		switch strings.ToLower(m[1]) {
		case "from":
			if from == "" {
				from = strings.ToLower(m[2])
			}
		case "to":
			if to == "" {
				to = strings.ToLower(m[2])
			}
		}
	}
	return
}

var brandKeyRe = regexp.MustCompile(`(?m)^\s*(base|bright)\s*=\s*"(#[0-9a-fA-F]{6})"`)

// checkBrand refuses a landing whose logo and stylesheet disagree: the kit
// derives the whole palette from the logo, and a landing that paints another
// colour would change colour on migrating (go-commonmark: slate logo, cyan page).
func checkBrand(brandToml, css string) error {
	from, to := stylesheetBrand(css)
	if from == "" || to == "" {
		return fmt.Errorf("brand.css has no --from/--to to compare the logo's brand with")
	}
	var base, bright string
	for _, m := range brandKeyRe.FindAllStringSubmatch(brandToml, -1) {
		if m[1] == "base" {
			base = strings.ToLower(m[2])
		} else {
			bright = strings.ToLower(m[2])
		}
	}
	if base != to || bright != from {
		return fmt.Errorf("the brand disagrees: the logo gives base %s / bright %s, brand.css has --to %s / --from %s; left out of the batch", base, bright, to, from)
	}
	return nil
}
