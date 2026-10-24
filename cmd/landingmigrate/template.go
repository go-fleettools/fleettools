package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// A "template" landing is the fleet's first landing shape, copied into most
// organisations before the kit existed: one self-contained
// layouts/index.html with its own <style>, a centred header (logo, h1,
// tagline, pills, buttons, badges), a <main> and a <footer>. It is parsed
// by its markers; anything in its body the parser does not recognise is a
// refusal, never dropped.
type landing struct {
	Title, Description, Favicon string
	HeadExtra                   string
	Style                       string
	Logo                        string
	H1, Tagline                 string
	Pills                       []string
	PillsRaw                    string // the pills div's inner HTML, as written
	CTAs                        []cta
	Badges                      string
	Main                        string
	Footer                      string
	Scripts                     []string
	Repos                       []string // the organisation's repositories the page links to
}

var ghRepoLink = regexp.MustCompile(`href="https://github\.com/([A-Za-z0-9-]+)/([A-Za-z0-9._-]+)/?"`)

// linkedRepos are the organisation's code repositories a page links to, in
// order, without the organisation's own (.github, brand, docs, the landing).
func linkedRepos(src, org string) []string {
	seen := map[string]bool{".github": true, "brand": true, "docs": true, org + ".github.io": true}
	var out []string
	for _, m := range ghRepoLink.FindAllStringSubmatch(src, -1) {
		if m[1] != org || seen[m[2]] {
			continue
		}
		seen[m[2]] = true
		out = append(out, m[2])
	}
	return out
}

type cta struct {
	Href, Inner string
}

var (
	titleRe   = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	descRe    = regexp.MustCompile(`(?s)<meta\s+name="description"[^>]*>`)
	faviconRe = regexp.MustCompile(`(?s)<link\s+rel="icon"[^>]*>`)
	styleRe   = regexp.MustCompile(`(?s)<style>(.*?)</style>`)
	logoRe    = regexp.MustCompile(`(?s)<img\s[^>]*class="logo"[^>]*>`)
	srcRe     = regexp.MustCompile(`\bsrc="([^"]+)"`)
	hrefRe    = regexp.MustCompile(`\bhref="([^"]+)"`)
	h1Re      = regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`)
	taglineRe = regexp.MustCompile(`(?s)<p class="tagline">(.*?)</p>`)
	pillRe    = regexp.MustCompile(`(?s)<span class="pill">(.*?)</span>`)
	btnRe     = regexp.MustCompile(`(?s)<a\s+class="btn[^"]*"\s*href="([^"]+)"[^>]*>(.*?)</a>`)
	btnRe2    = regexp.MustCompile(`(?s)<a\s+href="([^"]+)"\s*class="btn[^"]*"[^>]*>(.*?)</a>`)
	scriptRe  = regexp.MustCompile(`(?s)<script\b[^>]*>.*?</script>`)
	toggleRe  = regexp.MustCompile(`(?s)<button[^>]*id="theme-toggle"[^>]*>.*?</button>`)
	hugoCmtRe = regexp.MustCompile(`(?s)\{\{-?\s*/\*.*?\*/\s*-?\}\}`)
	headRe    = regexp.MustCompile(`(?s)<head>(.*?)</head>`)
	bodyRe    = regexp.MustCompile(`(?s)<body[^>]*>(.*?)</body>`)
)

// parseTemplate reads a template landing's layouts/index.html.
func parseTemplate(src string) (landing, error) {
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
	l.Favicon = faviconRe.FindString(h)
	if m := styleRe.FindStringSubmatch(h); m != nil {
		l.Style = m[1]
	} else {
		return l, fmt.Errorf("no <style> in <head>: not the template shape")
	}
	// what the head holds besides the known pieces is kept, verbatim
	rest := h
	for _, re := range []*regexp.Regexp{titleRe, descRe, faviconRe, styleRe, hugoCmtRe, scriptRe,
		regexp.MustCompile(`<meta\s+charset[^>]*>`), regexp.MustCompile(`<meta\s+name="viewport"[^>]*>`),
		regexp.MustCompile(`<meta\s+name="darkreader-lock"[^>]*>`)} {
		rest = re.ReplaceAllString(rest, "")
	}
	for _, s := range scriptRe.FindAllString(h, -1) {
		if !strings.Contains(s, "localStorage.getItem('theme')") {
			rest += "\n" + s // a script that is not the theme bootstrap is kept
		}
	}
	l.HeadExtra = strings.TrimSpace(rest)

	b := body[1]
	hs, he, ok := span(b, `<header`, "header")
	if !ok {
		return l, fmt.Errorf("no <header>")
	}
	header := b[hs:he]
	if m := logoRe.FindString(header); m != "" {
		if s := srcRe.FindStringSubmatch(m); s != nil {
			l.Logo = s[1]
		}
	}
	if m := h1Re.FindStringSubmatch(header); m != nil {
		l.H1 = strings.TrimSpace(m[1])
	} else {
		return l, fmt.Errorf("no <h1> in the header")
	}
	if m := taglineRe.FindStringSubmatch(header); m != nil {
		l.Tagline = strings.TrimSpace(m[1])
	}
	for _, m := range pillRe.FindAllStringSubmatch(header, -1) {
		l.Pills = append(l.Pills, strings.TrimSpace(m[1]))
	}
	// the pills are kept as written, not rebuilt from what pillRe saw:
	// four landings range over [params] pills ({{ range .Site.Params.pills }}
	// <span class="pill">{{ . }}</span>{{ end }}), and a rebuild printed one
	// pill reading "{{ . }}" outside any range -- verify caught it as lost text
	if ps, pe, ok := span(header, `<div class="pills"`, "div"); ok {
		d := header[ps:pe]
		l.PillsRaw = strings.TrimSpace(d[strings.Index(d, ">")+1 : len(d)-len("</div>")])
	}
	if cs, ce, ok := span(header, `<div class="cta"`, "div"); ok {
		c := header[cs:ce]
		for _, re := range []*regexp.Regexp{btnRe, btnRe2} {
			for _, m := range re.FindAllStringSubmatch(c, -1) {
				l.CTAs = append(l.CTAs, cta{Href: m[1], Inner: strings.TrimSpace(m[2])})
			}
		}
		// order as written
		sort.SliceStable(l.CTAs, func(i, j int) bool {
			return strings.Index(c, `"`+l.CTAs[i].Href+`"`) < strings.Index(c, `"`+l.CTAs[j].Href+`"`)
		})
	}
	if bs, be, ok := span(header, `<div class="badges"`, "div"); ok {
		l.Badges = header[bs:be]
	}
	// the header must hold nothing else
	left := header
	for _, cut := range []string{l.Badges} {
		if cut != "" {
			left = strings.Replace(left, cut, "", 1)
		}
	}
	if cs, ce, ok := span(left, `<div class="cta"`, "div"); ok {
		left = left[:cs] + left[ce:]
	}
	if ps, pe, ok := span(left, `<div class="pills"`, "div"); ok {
		left = left[:ps] + left[pe:]
	}
	for _, re := range []*regexp.Regexp{logoRe, h1Re, taglineRe, hugoCmtRe} {
		left = re.ReplaceAllString(left, "")
	}
	left = regexp.MustCompile(`(?s)^<header[^>]*>|</header>$`).ReplaceAllString(strings.TrimSpace(left), "")
	if strings.TrimSpace(left) != "" {
		return l, fmt.Errorf("the header holds something this tool does not know: %q", clip(strings.TrimSpace(left)))
	}

	ms, me, ok := span(b, `<main`, "main")
	if !ok {
		return l, fmt.Errorf("no <main>")
	}
	main := b[ms:me]
	l.Main = main[strings.Index(main, ">")+1 : len(main)-len("</main>")]
	fs, fe, ok := span(b, `<footer`, "footer")
	if ok {
		f := b[fs:fe]
		l.Footer = strings.TrimSpace(f[strings.Index(f, ">")+1 : len(f)-len("</footer>")])
	}
	// the body must hold nothing else but the theme toggle and scripts
	rest = b[:hs] + b[he:ms] + b[me:]
	if ok {
		rest = strings.Replace(rest, b[fs:fe], "", 1)
	}
	rest = toggleRe.ReplaceAllString(rest, "")
	rest = hugoCmtRe.ReplaceAllString(rest, "")
	for _, s := range scriptRe.FindAllString(rest, -1) {
		if !strings.Contains(s, "theme-toggle") {
			l.Scripts = append(l.Scripts, s)
		}
	}
	rest = scriptRe.ReplaceAllString(rest, "")
	if strings.TrimSpace(rest) != "" {
		return l, fmt.Errorf("the body holds something this tool does not know: %q", clip(strings.TrimSpace(rest)))
	}
	return l, nil
}

func clip(s string) string {
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

// span finds an element by the start of its opening tag.
func span(src, open, tag string) (int, int, bool) {
	j := indexOpen(src, open, 0)
	if j < 0 {
		return 0, 0, false
	}
	k := closeOf(src, j, tag)
	if k < 0 {
		return 0, 0, false
	}
	return j, k, true
}

// icons the kit ships under other names; a local copy of a kit icon would
// silently override the kit's.
var iconRename = map[string]string{
	`partial "icons/mark-github.html"`: `partial "icons/github.html"`,
}

func renameIcons(s string) string {
	for from, to := range iconRename {
		s = strings.ReplaceAll(s, from, to)
	}
	return s
}

var snippetRe = regexp.MustCompile(`(?s)\s*<pre class="snippet"><code>(.*?)</code></pre>`)

// render writes the kit's layout around what the template said, and the
// hero terminal the kit's hero-carousel.html shows with no cards.
func render(l landing, org string) (index, terminal string) {
	main := l.Main
	// the template's first code snippet becomes the hero's terminal
	code := ""
	if m := snippetRe.FindStringSubmatchIndex(main); m != nil {
		code = main[m[2]:m[3]]
		main = main[:m[0]] + main[m[1]:]
	}
	if code != "" {
		terminal = `<div class="term">
      <div class="bar"><i style="background:#ff5f57"></i><i style="background:#febc2e"></i><i style="background:#28c840"></i><span class="t">main.go</span></div>
<pre>` + code + `</pre>
    </div>`
	} else {
		// no snippet: what a reader types first, from [[params.repos]] when
		// the site has it, else from the repositories the page links to,
		// else the organisation itself
		var static strings.Builder
		for i, r := range l.Repos {
			if i == 6 {
				break
			}
			fmt.Fprintf(&static, "<span class=\"c-cmd\">$ go get</span> github.com/%s/%s@latest\n", org, r)
		}
		if static.Len() == 0 {
			fmt.Fprintf(&static, "<span class=\"c-cmd\">$ gh repo list</span> %s\n", org)
		}
		terminal = `<div class="term">
      <div class="bar"><i style="background:#ff5f57"></i><i style="background:#febc2e"></i><i style="background:#28c840"></i><span class="t">zsh</span></div>
<pre>{{ with .Site.Params.repos }}{{ range first 6 . }}{{ with or .pkg (strings.TrimPrefix "https://" (.url | default "")) (printf "github.com/` + org + `/%s" .name) }}<span class="c-cmd">$ go get</span> {{ . }}@latest
{{ end }}{{ end }}{{ else }}` + static.String() + `{{ end }}</pre>
    </div>`
	}
	var b strings.Builder
	w := func(s ...string) {
		for _, x := range s {
			b.WriteString(x)
		}
	}
	w("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	w("<title>", l.Title, "</title>\n")
	if l.Description != "" {
		w(l.Description, "\n")
	}
	if l.Favicon != "" {
		w(l.Favicon, "\n")
	} else {
		w(`<link rel="icon" type="image/svg+xml" href="/favicon.svg">`, "\n")
	}
	w(`{{ partial "styles.html" . }}`, "\n")
	if css := legacyCSS(l.Style); css != "" {
		w("{{/* The template's own rules for what <main> holds, kept for it alone (class legacy),\n     its colours read from the kit's tokens. Written by landingmigrate. */}}\n<style>\n", css, "</style>\n")
	}
	if l.HeadExtra != "" {
		w(l.HeadExtra, "\n")
	}
	w("</head>\n<body class=\"lm\">\n")
	logo := l.Logo
	if logo == "" {
		logo = "/img/logo.svg"
	}
	w(`  <header class="nav"><div class="wrap">
    <a class="brand" href="/"><img class="glyph" src="`, logo, `" width="32" height="32" alt="`, org, ` logo"> {{ .Site.Title }}</a>
    {{ partial "theme-toggle.html" . }}
  </div></header>
  {{ partial "fork-ribbon.html" "https://github.com/`, org, `" }}

  <div class="hero"><div class="wrap">
    <div>
      <h1 class="head">`, renameIcons(l.H1), "</h1>\n")
	if l.Tagline != "" {
		w(`      <p class="sub">`, renameIcons(l.Tagline), "</p>\n")
	}
	if len(l.CTAs) > 0 {
		w(`      <div class="cta">`, "\n")
		for i, c := range l.CTAs {
			cls := "btn btn-ghost"
			if i == 0 {
				cls = "btn btn-primary"
			}
			w(`        <a class="`, cls, `" href="`, c.Href, `">`, renameIcons(c.Inner), "</a>\n")
		}
		w("      </div>\n")
	}
	switch {
	case l.PillsRaw != "":
		w(`      <div class="trust">`, renameIcons(l.PillsRaw), "</div>\n")
	case len(l.Pills) > 0:
		w(`      <div class="trust">`)
		for _, p := range l.Pills {
			w(`<span class="pill">`, p, `</span>`)
		}
		w("</div>\n")
	}
	if l.Badges != "" {
		w(`      <div class="legacy">`, l.Badges, "</div>\n")
	}
	w(`    </div>
    {{ partial "hero-carousel.html" . }}
  </div></div>

  {{ with .Site.Params.repos }}<div class="strip"><div class="wrap">
    <span class="lead-line">One organisation, <b>{{ len . }}</b> repositories</span>
    <div class="mods">{{ range . }}<a href="#repos">{{ .name }}</a>{{ end }}</div>
  </div></div>{{ end }}

  <main id="repos" class="legacy"><div class="wrap">`, renameIcons(main), "  </div></main>\n")
	if l.Footer != "" {
		w("\n  <footer><div class=\"wrap\"><span>", renameIcons(l.Footer), "</span></div></footer>\n")
	}
	for _, s := range l.Scripts {
		w(s, "\n")
	}
	w("</body>\n</html>\n")
	return b.String(), terminal
}

// legacyCSS keeps the template's rules for what its <main> holds, scoped to
// .legacy so they style nothing of the kit's, with its colour variables
// read from the kit's tokens where the kit has the same role, and its own
// values (per theme) where it does not.
func legacyCSS(style string) string {
	items := cssItems(style)
	kitRole := map[string]string{"--fg": "var(--ink)", "--muted": "var(--slate)", "--card": "var(--bg-soft)", "--border": "var(--line)"}
	skipVar := map[string]bool{"--bg": true, "--accent": true, "--accent-fg": true}
	var light, dark []string
	var rules []string
	for _, it := range items {
		switch {
		case it.sel == ":root":
			for _, d := range decls(it.body) {
				switch {
				case kitRole[d[0]] != "":
					light = append(light, d[0]+":"+kitRole[d[0]])
				case !skipVar[d[0]] && strings.HasPrefix(d[0], "--"):
					light = append(light, d[0]+":"+d[1])
				}
			}
		case it.sel == `:root[data-theme="dark"]`:
			for _, d := range decls(it.body) {
				if kitRole[d[0]] == "" && !skipVar[d[0]] && strings.HasPrefix(d[0], "--") {
					dark = append(dark, d[0]+":"+d[1])
				}
			}
		case strings.HasPrefix(it.sel, "@media") && strings.Contains(it.sel, "prefers-color-scheme"):
			// the dark theme's variables, said again by the :root[data-theme] rule
		case strings.HasPrefix(it.sel, "@"):
			var inner []string
			for _, r := range cssItems(it.body) {
				if s := scope(r.sel); s != "" {
					inner = append(inner, "  "+s+"{"+strings.TrimSpace(r.body)+"}")
				}
			}
			if len(inner) > 0 {
				rules = append(rules, it.sel+"{\n"+strings.Join(inner, "\n")+"\n}")
			}
		default:
			if s := scope(it.sel); s != "" {
				rules = append(rules, s+"{"+strings.TrimSpace(it.body)+"}")
			}
		}
	}
	var b strings.Builder
	if len(light) > 0 {
		b.WriteString(".legacy{" + strings.Join(light, ";") + "}\n")
	}
	if len(dark) > 0 {
		d := strings.Join(dark, ";")
		b.WriteString("@media(prefers-color-scheme:dark){:root:not([data-theme]) .legacy{" + d + "}}\n")
		b.WriteString(`:root[data-theme="dark"] .legacy{` + d + "}\n")
	}
	for _, r := range rules {
		b.WriteString(r + "\n")
	}
	return b.String()
}

// header rules belong to what the kit now draws.
var notMain = regexp.MustCompile(`^(\*|html|body|header|footer|a|h1|\.wrap|\.theme-toggle|\.logo|\.tagline|\.pills?|\.cta|\.btn|\.lead code|\.btn\b.*|header\b.*|footer\b.*|a:\w+)$`)

// scope prefixes each selector of a rule with .legacy, dropping those that
// style what the kit now draws.
func scope(sel string) string {
	var out []string
	for _, s := range strings.Split(sel, ",") {
		s = strings.TrimSpace(s)
		if s == "" || notMain.MatchString(s) || strings.HasPrefix(s, ":root") {
			continue
		}
		out = append(out, ".legacy "+s)
	}
	return strings.Join(out, ",")
}

type cssItem struct{ sel, body string }

// cssItems splits a stylesheet into its top-level rules and at-rules.
func cssItems(css string) []cssItem {
	css = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
	var out []cssItem
	depth, start, open := 0, 0, 0
	for i := 0; i < len(css); i++ {
		switch css[i] {
		case '{':
			if depth == 0 {
				open = i
			}
			depth++
		case '}':
			depth--
			if depth == 0 {
				out = append(out, cssItem{sel: strings.Join(strings.Fields(css[start:open]), " "), body: css[open+1 : i]})
				start = i + 1
			}
		}
	}
	return out
}

// decls are a rule's declarations as name, value.
func decls(body string) [][2]string {
	var out [][2]string
	for _, d := range strings.Split(body, ";") {
		k, v, ok := strings.Cut(d, ":")
		if !ok {
			continue
		}
		out = append(out, [2]string{strings.TrimSpace(k), strings.TrimSpace(v)})
	}
	return out
}
