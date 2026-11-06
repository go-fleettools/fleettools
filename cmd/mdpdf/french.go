package main

// French typography, applied with --lang fr to the text of the HTML body
// before layout. French authors type ordinary spaces; the layout engine then
// breaks "70 000 $" as "70" / "000 $" and puts a ":" at the start of a line.
// html2pdf already refuses to break at U+00A0 and U+202F (TestNoBreakSpaces
// NeverBreakALine in go-pdfkit/html2pdf), so the fix is to write those
// characters where French typography wants them (Imprimerie nationale,
// Lexique des règles typographiques, "espaces insécables"):
//
//   - digit groups: "70 000" -> U+202F between the groups;
//   - number and unit, currency or percent: "70 000 $", "20 %" -> U+00A0;
//   - before ";", "!", "?", "»" -> U+202F; before ":" -> U+00A0; after "«" ->
//     U+202F.
//
// Text inside code, pre, kbd, samp, tt, script, style and textarea is never
// touched, nor is any attribute.

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	nnbsp = " " // narrow no-break space
	nbsp  = " " // no-break space
)

var (
	// A digit group needs 1-3 leading digits, then groups of exactly 3.
	groupRE = regexp.MustCompile(`[0-9]+(?: [0-9]{3})+`)
	// A digit, a space, then a unit that ends the word.
	unitRE = regexp.MustCompile(`([0-9]) ((?:%|‰|\$|€|£|¥|USD|EUR|CAD|CHF|GBP|[kKMGT]?(?:o|B|Hz|W|V|A|g|m|L)|mm|cm|min|ms|h|s)(?:\z|[^\p{L}\p{N}'’]))`)
	// A space before ; ! ? » (narrow) or : (plain), ending the word.
	highRE  = regexp.MustCompile(` ([;!?»])(\z|[^\p{L}\p{N}])`)
	colonRE = regexp.MustCompile(` (:)(\z|[^\p{L}\p{N}])`)
	// A space after «.
	openRE = regexp.MustCompile(`« `)
)

// isFrench reports whether lang's primary subtag is fr.
func isFrench(lang string) bool {
	primary, _, _ := strings.Cut(strings.ToLower(lang), "-")
	return primary == "fr"
}

// frenchText applies the rules to one run of text.
func frenchText(s string) string {
	if !strings.ContainsAny(s, " ") {
		return s
	}
	s = groupRE.ReplaceAllStringFunc(s, func(m string) string {
		first, _, _ := strings.Cut(m, " ")
		if len(first) > 3 {
			return m // "2025 100": not a grouped number
		}
		return strings.ReplaceAll(m, " ", nnbsp)
	})
	// A group followed by more digits ("70 0001") is not a group either.
	s = fixTrailingDigit(s)
	s = unitRE.ReplaceAllString(s, "$1"+nbsp+"$2")
	s = highRE.ReplaceAllString(s, nnbsp+"$1$2")
	s = colonRE.ReplaceAllString(s, nbsp+"$1$2")
	s = openRE.ReplaceAllString(s, "«"+nnbsp)
	return s
}

// fixTrailingDigit undoes a join whose last group is followed by a digit:
// "70 0001" was not a number with a thousands group.
func fixTrailingDigit(s string) string {
	r := []rune(s)
	for i := 0; i+4 < len(r); i++ {
		if r[i] == ' ' && isDigit(r[i+1]) && isDigit(r[i+2]) && isDigit(r[i+3]) && isDigit(r[i+4]) {
			r[i] = ' '
		}
	}
	return string(r)
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// frenchTypography rewrites the text nodes of an HTML body fragment.
func frenchTypography(fragment string) (string, error) {
	nodes, err := parseFragment(fragment)
	if err != nil {
		return "", err
	}
	for _, n := range nodes {
		frenchWalk(n, 0)
	}
	return renderNodes(nodes)
}

func frenchWalk(n *html.Node, depth int) {
	if depth > maxDepth {
		return
	}
	switch n.Type {
	case html.TextNode:
		n.Data = frenchText(n.Data)
		return
	case html.ElementNode:
		switch n.DataAtom {
		case atom.Code, atom.Pre, atom.Kbd, atom.Samp, atom.Tt, atom.Script, atom.Style, atom.Textarea:
			return
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		frenchWalk(c, depth+1)
	}
}
