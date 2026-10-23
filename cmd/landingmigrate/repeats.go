package main

import (
	"html"
	"regexp"
	"strings"
)

// The mantra strip (landingkit v0.5.0+) says LICENSE=BSD-3-Clause and
// CGO_ENABLED=0 under the top bar of every page. A landing that says them
// again in a pill, a badge, a sentence or its footer repeats the strip. A
// fragment that says NOTHING ELSE is a repeat: it is wrapped in
//
//	{{ if not (partial "mantra-on.html" $) }}…{{ end }}
//
// so it disappears while the strip says it and comes back if the strip is
// ever turned off. A fragment that mentions them and says more ("the only
// package that needs cgo is X", "BSD-3 code · MIT artwork") is kept, and
// listed for a person to review.

// mention: the licence or cgo, the two things the strip says. "pure Go"
// alone is not a mention (it is the fleet's subject, not its licence), but
// it counts as part of the mantra when a fragment is judged a repeat.
var mention = regexp.MustCompile(`(?i)\bBSD\b|\bBSD[- ]?3|\bcgo\b|CGO_ENABLED|\bCGO\s*=`)

// mantraWords are removed before asking whether anything is left.
var mantraWords = regexp.MustCompile(`(?i)BSD[- ]?3(?:[- ]clause)?|\bBSD\b|CGO_ENABLED\s*=\s*0|\bCGO\s*=\s*0|\bcgo[- ]free\b|\bno[- ]cgo\b|\bzero[- ]cgo\b|\bwithout[- ]cgo\b|\bcgo[- ]disabled\b|\bcgo\b|\bpure[- ]go\b|\blicen[cs]ed?\b|\blicen[cs]es\b`)

// filler is what a sentence made only of the mantra is built with.
var filler = regexp.MustCompile(`(?i)\b(is|are|it|its|it's|everything|all|and|the|a|an|under|with|in|of|written|code|whole|stack|entirely|fully|everywhere|disabled|enabled|zero|no|only|by|default|100|%|itself)\b`)

// repeat says whether text (HTML allowed) says nothing but the mantra.
func repeat(text string) bool {
	// a fragment with a Hugo action renders content this cannot read (a
	// card's blurb): it is never hidden, only listed
	if strings.Contains(text, "{{") {
		return false
	}
	t := plain(text)
	if !mention.MatchString(t) {
		return false
	}
	t = mantraWords.ReplaceAllString(t, " ")
	t = filler.ReplaceAllString(t, " ")
	for _, r := range t {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return false
		}
		if r > 0x7f && r != '·' && r != '—' && r != '–' && r != ' ' && r != '’' {
			return false
		}
	}
	return true
}

var tagRe = regexp.MustCompile(`<[^>]*>`)
var actionRe = regexp.MustCompile(`\{\{.*?\}\}`)

// plain is the text of an HTML fragment: tags and Hugo actions dropped,
// entities decoded, space collapsed.
func plain(s string) string {
	s = actionRe.ReplaceAllString(s, " ")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

// fragment is one place the strip's values were said.
type fragment struct {
	Where  string `json:"where"`  // pill, badge, sentence, footer, trust
	HTML   string `json:"html"`   // as written
	Text   string `json:"text"`   // its visible text
	Hidden bool   `json:"hidden"` // true: wrapped (a repeat); false: kept for review
}

// $ and not .: a fragment inside {{ range }} has a repository for its dot,
// and the partial needs the page, which $ is at a layout's top level.
const (
	ifOff = `{{ if not (partial "mantra-on.html" $) }}`
	end   = `{{ end }}`
)

// hide wraps s so that it shows only without the strip.
func hide(s string) string { return ifOff + s + end }

// repeats finds the strip's values said again in body (a layout's HTML) and
// returns the body with every pure repeat hidden, and every fragment found.
func repeats(body string) (string, []fragment) {
	var frags []fragment
	note := func(where, h string, hidden bool) {
		frags = append(frags, fragment{Where: where, HTML: h, Text: plain(h), Hidden: hidden})
	}
	// pills and the hero's trust line: one element is one fragment
	body = eachElement(body, `<span class="pill"`, "span", func(whole string) string {
		if !mention.MatchString(plain(whole)) {
			return whole
		}
		if repeat(whole) {
			note("pill", whole, true)
			return hide(whole)
		}
		note("pill", whole, false)
		return whole
	})
	// badges: a licence badge says the strip's licence
	body = eachElement(body, `<div class="badges"`, "div", func(whole string) string {
		return licenceBadges.ReplaceAllStringFunc(whole, func(b string) string {
			note("badge", b, true)
			return hide(b)
		})
	})
	// footers: segments between middle dots
	body = eachElement(body, `<footer`, "footer", func(whole string) string {
		return footerSegments(whole, note)
	})
	// sentences, in paragraphs and list items
	for _, tag := range []string{"p", "li"} {
		body = eachElement(body, "<"+tag, tag, func(whole string) string {
			return sentences(whole, tag, note)
		})
	}
	return body, frags
}

var licenceBadges = regexp.MustCompile(`(?is)(<a\b[^>]*>\s*)?<img\b[^>]*(?:alt="licen[cs]e"|/badge/licen[cs]e)[^>]*>(\s*</a>)?`)

// eachElement calls f on every element whose opening tag starts with open,
// with nested elements of the same tag counted, and replaces it with what f
// returns. An element inside a Hugo comment is left alone.
func eachElement(src, open, tag string, f func(string) string) string {
	var out strings.Builder
	i := 0
	for {
		j := indexOpen(src, open, i)
		if j < 0 {
			out.WriteString(src[i:])
			return out.String()
		}
		k := closeOf(src, j, tag)
		if k < 0 {
			out.WriteString(src[i:])
			return out.String()
		}
		out.WriteString(src[i:j])
		out.WriteString(f(src[j:k]))
		i = k
	}
}

// indexOpen finds open at or after i, as a whole tag name ("<p" is not "<pre").
func indexOpen(src, open string, i int) int {
	for {
		j := strings.Index(src[i:], open)
		if j < 0 {
			return -1
		}
		j += i
		next := j + len(open)
		if next >= len(src) {
			return -1
		}
		if strings.HasSuffix(open, `"`) || src[next] == ' ' || src[next] == '>' || src[next] == '\n' || src[next] == '\t' {
			return j
		}
		i = next
	}
}

// closeOf is the offset just past the element opened at j, nested
// same-name elements counted; -1 when it never closes.
func closeOf(src string, j int, tag string) int {
	depth := 0
	i := j
	for i < len(src) {
		o := indexOpen(src, "<"+tag, i)
		c := strings.Index(src[i:], "</"+tag+">")
		if c < 0 {
			return -1
		}
		c += i
		if o >= 0 && o < c {
			depth++
			i = o + len(tag) + 1
			continue
		}
		depth--
		i = c + len(tag) + 3
		if depth == 0 {
			return i
		}
	}
	return -1
}

var middot = regexp.MustCompile(`\s*(?:·|&middot;|&#183;)\s*`)

// footerSegments hides the segments of a footer's text (between middle
// dots, inside each <span> or the footer itself) that are pure repeats.
func footerSegments(whole string, note func(string, string, bool)) string {
	doSegs := func(s string) string {
		// split on middle dots that are outside tags and actions
		idx := middot.FindAllStringIndex(s, -1)
		if len(idx) == 0 {
			if mention.MatchString(plain(s)) && strings.TrimSpace(plain(s)) != "" && !strings.Contains(s, "<span") && !strings.Contains(s, "<div") {
				if repeat(s) {
					note("footer", s, true)
					return hide(s)
				}
				note("footer", s, false)
			}
			return s
		}
		var segs, seps []string
		last := 0
		for _, m := range idx {
			segs = append(segs, s[last:m[0]])
			seps = append(seps, s[m[0]:m[1]])
			last = m[1]
		}
		segs = append(segs, s[last:])
		var b strings.Builder
		for i, seg := range segs {
			r := mention.MatchString(plain(seg)) && repeat(seg)
			// what is hidden is noted as hidden, separator included, so
			// verify takes out of the page exactly what the page lost
			switch {
			case !r:
				if mention.MatchString(plain(seg)) {
					note("footer", seg, false)
				}
				b.WriteString(seg)
				if i < len(seps) {
					b.WriteString(seps[i])
				}
			case i < len(seps):
				// a repeat and the dot after it go together
				lead := seg[:len(seg)-len(strings.TrimLeft(seg, " \t\n"))]
				h := strings.TrimLeft(seg, " \t\n") + seps[i]
				note("footer", h, true)
				b.WriteString(lead + hide(h))
			default:
				// the last segment takes the dot before it, already written
				prev := b.String()
				if n := len(seps); n > 0 && strings.HasSuffix(prev, seps[n-1]) {
					b.Reset()
					b.WriteString(prev[:len(prev)-len(seps[n-1])])
					note("footer", seps[n-1]+seg, true)
					b.WriteString(hide(seps[n-1] + seg))
				} else {
					note("footer", seg, true)
					b.WriteString(hide(seg))
				}
			}
		}
		return b.String()
	}
	if strings.Contains(whole, "<span") {
		return eachElement(whole, "<span", "span", func(sp string) string {
			gt := strings.Index(sp, ">")
			inner := sp[gt+1 : len(sp)-len("</span>")]
			if strings.TrimSpace(plain(inner)) != "" && repeat(inner) {
				note("footer", inner, true)
				return hide(sp)
			}
			return sp[:gt+1] + doSegs(inner) + "</span>"
		})
	}
	gt := strings.Index(whole, ">")
	closeAt := strings.LastIndex(whole, "</")
	inner := whole[gt+1 : closeAt]
	// the footer's own text may sit in a .wrap div
	if strings.HasPrefix(strings.TrimSpace(inner), "<div") {
		dgt := strings.Index(inner, ">")
		dclose := strings.LastIndex(inner, "</div>")
		if dgt >= 0 && dclose > dgt {
			return whole[:gt+1] + inner[:dgt+1] + doSegs(inner[dgt+1:dclose]) + inner[dclose:] + whole[closeAt:]
		}
	}
	return whole[:gt+1] + doSegs(inner) + whole[closeAt:]
}

// sentences hides the sentences of one paragraph that are pure repeats (or
// the whole paragraph when all of it is), and notes the ones that mention
// the mantra and say more.
func sentences(whole, tag string, note func(string, string, bool)) string {
	if !mention.MatchString(plain(whole)) {
		return whole
	}
	gt := strings.Index(whole, ">")
	inner := whole[gt+1 : len(whole)-len("</"+tag+">")]
	if strings.Contains(inner, "<"+tag+" ") || strings.Contains(inner, "<"+tag+">") || strings.Contains(inner, "<ul") || strings.Contains(inner, "<ol") {
		return whole // nested blocks: their own elements are visited separately
	}
	parts := splitSentences(inner)
	var b strings.Builder
	any, all := false, true
	for _, s := range parts {
		if strings.TrimSpace(plain(s)) == "" {
			b.WriteString(s)
			continue
		}
		if mention.MatchString(plain(s)) && repeat(s) {
			any = true
			lead := s[:len(s)-len(strings.TrimLeft(s, " \t\n"))]
			b.WriteString(lead + hide(strings.TrimLeft(s, " \t\n")))
			continue
		}
		all = false
		if mention.MatchString(plain(s)) {
			note("sentence", strings.TrimSpace(s), false)
		}
		b.WriteString(s)
	}
	if !any {
		return whole
	}
	if all {
		note("sentence", strings.TrimSpace(inner), true)
		return hide(whole)
	}
	for _, s := range parts {
		if strings.TrimSpace(plain(s)) != "" && mention.MatchString(plain(s)) && repeat(s) {
			note("sentence", strings.TrimSpace(s), true)
		}
	}
	return whole[:gt+1] + b.String() + "</" + tag + ">"
}

// splitSentences cuts HTML text after each . ! or ? that ends a sentence:
// outside a tag or a Hugo action, followed by space or the end (closing
// tags after the mark stay with the sentence).
func splitSentences(s string) []string {
	var out []string
	start, inTag, inAction := 0, false, false
	for i := 0; i < len(s); i++ {
		switch {
		case inAction:
			if strings.HasPrefix(s[i:], "}}") {
				inAction = false
				i++
			}
			continue
		case strings.HasPrefix(s[i:], "{{"):
			inAction = true
			i++
			continue
		case s[i] == '<':
			inTag = true
			continue
		case s[i] == '>':
			inTag = false
			continue
		case inTag:
			continue
		}
		if s[i] != '.' && s[i] != '!' && s[i] != '?' {
			continue
		}
		j := i + 1
		// closing inline tags right after the mark belong to this sentence
		for strings.HasPrefix(s[j:], "</") {
			k := strings.Index(s[j:], ">")
			if k < 0 {
				break
			}
			j += k + 1
		}
		if j == len(s) || s[j] == ' ' || s[j] == '\n' || s[j] == '\t' {
			out = append(out, s[start:j])
			start = j
			i = j - 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
