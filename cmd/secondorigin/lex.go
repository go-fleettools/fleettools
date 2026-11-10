package main

import (
	"bytes"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// stringLiterals returns the string literals of a JavaScript source: what is
// between quotes, never what is in a comment.
//
// ⛔ A SCAN FOR "https://…" OVER THE WHOLE SOURCE WOULD BE WRONG BOTH WAYS. A
// comment that links a specification names an origin the code does not accept,
// and a grep would call the shell drifted; a literal assembled across a line
// would be missed. So the source is walked once, with three states (code, a
// comment, a literal), and only literals come out. Regular-expression literals
// are not recognised: a `/` is division or a regexp and telling them apart needs
// the whole grammar. A quote inside one can desynchronise the walk, and the
// walk then errs towards finding MORE literals, which this command reports as
// drift -- the safe direction for a gate.
func stringLiterals(src string) []string {
	var out []string
	i, n := 0, len(src)
	for i < n {
		c := src[i]
		switch {
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			j := strings.Index(src[i+2:], "*/")
			if j < 0 {
				return out
			}
			i += j + 4
		case c == '"' || c == '\'' || c == '`':
			var sb strings.Builder
			i++
			for i < n && src[i] != c {
				if src[i] == '\\' && i+1 < n {
					i++
				} else if src[i] == '\n' && c != '`' {
					break // an unterminated literal ends at the line
				}
				sb.WriteByte(src[i])
				i++
			}
			out = append(out, sb.String())
			i++
		default:
			i++
		}
	}
	return out
}

// originOf says the origin a literal names, if it is an http(s) URL.
func originOf(lit string) (string, bool) {
	if !strings.HasPrefix(lit, "http://") && !strings.HasPrefix(lit, "https://") {
		return "", false
	}
	if strings.ContainsAny(lit, " \t\n") {
		return "", false
	}
	u, err := url.Parse(lit)
	if err != nil || u.Host == "" {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

// shellPage is what the served shell document says about itself.
type shellPage struct {
	csp     []string // content of every <meta http-equiv=Content-Security-Policy>
	scripts []string // src of every external <script>
	inline  []string // text of every inline <script>
}

func parseShell(page []byte) shellPage {
	var p shellPage
	z := html.NewTokenizer(bytes.NewReader(page))
	inScript := false
	for {
		switch z.Next() {
		case html.ErrorToken:
			return p
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			attrs := map[string]string{}
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				attrs[strings.ToLower(string(k))] = string(v)
			}
			switch string(name) {
			case "meta":
				if strings.EqualFold(attrs["http-equiv"], "content-security-policy") {
					p.csp = append(p.csp, attrs["content"])
				}
			case "script":
				if src, ok := attrs["src"]; ok {
					p.scripts = append(p.scripts, src)
				} else {
					inScript = true
				}
			}
		case html.TextToken:
			if inScript {
				p.inline = append(p.inline, string(z.Text()))
			}
		case html.EndTagToken:
			inScript = false
		}
	}
}

// normCSP puts a policy in a form two spellings of the same policy share:
// directives sorted by name, source lists sorted, whitespace collapsed. CSP
// does not care about the order of either (CSP Level 3, 2.2: a policy is a set
// of directives), and a repository that reorders its policy has not changed it.
func normCSP(p string) string {
	var ds []string
	for _, d := range strings.Split(p, ";") {
		f := strings.Fields(d)
		if len(f) == 0 {
			continue
		}
		f[0] = strings.ToLower(f[0])
		rest := f[1:]
		sort.Strings(rest)
		ds = append(ds, strings.Join(append([]string{f[0]}, rest...), " "))
	}
	sort.Strings(ds)
	return strings.Join(ds, "; ")
}

var wildcardPost = regexp.MustCompile(`postMessage\s*\([^;]*,\s*["'` + "`" + `]\*["'` + "`" + `]\s*[,)]`)
var readsOrigin = regexp.MustCompile(`\.origin\b|ancestorOrigins`)
