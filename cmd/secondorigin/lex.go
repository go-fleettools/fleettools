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

// matchClose returns the index just after the bracket that closes the one at
// src[i] (open), walking strings and comments as stringLiterals does, or -1.
func matchClose(src string, i int, open, close byte) int {
	depth, n := 0, len(src)
	for i < n {
		c := src[i]
		switch {
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				i++
			}
			continue
		case c == '/' && i+1 < n && src[i+1] == '*':
			j := strings.Index(src[i+2:], "*/")
			if j < 0 {
				return -1
			}
			i += j + 4
			continue
		case c == '"' || c == '\'' || c == '`':
			i++
			for i < n && src[i] != c {
				if src[i] == '\\' {
					i++
				}
				i++
			}
		case c == open:
			depth++
		case c == close:
			depth--
			if depth == 0 {
				return i + 1
			}
		}
		i++
	}
	return -1
}

var messageListener = regexp.MustCompile(`addEventListener\(\s*["']message["']\s*,`)
var firstIf = regexp.MustCompile(`^\s*(?:(?://[^\n]*\n|/\*.*?\*/)\s*)*if\s*\(`)

// messageGuards checks the shape of every window "message" listener: ITS FIRST
// STATEMENT must be an `if` whose condition compares both the message's origin
// and its source and whose consequence returns.
//
// ⛔ A WORD IS NOT A CHECK (fleettools#243). The first version asked only that
// the script mention `.origin`, and a shell whose listener had no origin test
// at all, or no listener guard, still verified: the real shell names `.origin`
// in four other places. The browser run (behaviour.go) is the proof; this is the
// cheap first pass, and it fails on the shapes a mutation produces: the origin
// test removed, the source test removed, the whole guard removed, or the guard
// no longer first.
func messageGuards(src string) (n int, problems []string) {
	for _, loc := range messageListener.FindAllStringIndex(src, -1) {
		n++
		rest := src[loc[1]:]
		bi := strings.IndexByte(rest, '{')
		if bi < 0 {
			problems = append(problems, "a message listener with no body")
			continue
		}
		end := matchClose(rest, bi, '{', '}')
		if end < 0 {
			problems = append(problems, "a message listener whose body does not end")
			continue
		}
		body := rest[bi+1 : end-1]
		m := firstIf.FindStringIndex(body)
		if m == nil {
			problems = append(problems, "a message listener whose first statement is not an origin guard")
			continue
		}
		po := m[1] - 1
		pe := matchClose(body, po, '(', ')')
		if pe < 0 {
			problems = append(problems, "a message listener with an unreadable guard")
			continue
		}
		cond, then := body[po+1:pe-1], strings.TrimSpace(body[pe:])
		then = strings.TrimSpace(strings.TrimPrefix(then, "{"))
		switch {
		case !strings.Contains(cond, ".origin"):
			problems = append(problems, "a message listener whose guard does not test the message's origin: "+oneLine(cond))
		case !strings.Contains(cond, ".source"):
			problems = append(problems, "a message listener whose guard does not test the message's source: "+oneLine(cond))
		case !strings.HasPrefix(then, "return"):
			problems = append(problems, "a message listener whose guard does not return: "+oneLine(cond))
		}
	}
	return n, problems
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 100 {
		s = s[:100] + "…"
	}
	return s
}
