package main

import (
	"regexp"
	"sort"
	"strings"
)

// Class is how a repository's CI checks links.
type Class string

const (
	// Shell is the hand-written loop: URLs pulled out with grep, then a curl or
	// wget per URL whose HTTP status is tested. This is what linkcheck replaced.
	Shell Class = "SHELL"
	// Linkcheck runs fleettools' cmd/linkcheck.
	Linkcheck Class = "LINKCHECK"
	// Lychee runs lychee (the offline mode is the fine one; the detail says
	// which this is).
	Lychee Class = "LYCHEE"
	// Other is another real link checker (landingkit links, markdown-link-check,
	// muffet, htmltest, linkinator): not the shell loop, so not NONE either --
	// counting it as NONE would hide that the repository does check links.
	Other Class = "OTHER"
	// None has no link-checking job this tool can recognise.
	None Class = "NONE"
)

// Verdict is the classification of one repository.
type Verdict struct {
	Class   Class
	Version string   // LINKCHECK: the pinned ref ("unpinned" when there is none)
	Offline bool     // LYCHEE: --offline was passed
	Tool    string   // OTHER: which checker
	Files   []string // the workflow files that matched the class
	Also    []Class  // other classes present in the same repository
}

var (
	fetcher = regexp.MustCompile(`\b(curl|wget)\b`)
	// A status test: curl's %{http_code}, wget's --spider, or a HEAD request.
	// A bare `curl -fsSL url | sh` (an install) has none of these.
	statusTest = regexp.MustCompile(`%\{http_code\}|--spider|--head\b|\bcurl\b[^\n|;]*\s-[A-Za-z]*I[A-Za-z]*\b`)
	loop       = regexp.MustCompile(`\bwhile\b|\bfor\b[^\n]*\bin\b|\bxargs\b`)
	// URLs are extracted from the tree, not listed by hand.
	extract = regexp.MustCompile(`grep[^\n]*https?`)

	linkcheckRef = regexp.MustCompile(`fleettools/cmd/linkcheck(?:@([^\s"'\\]+))?`)
	otherRef     = regexp.MustCompile(`landingkit/cmd/landingkit\s+links|markdown-link-check|\bmuffet\b|\bhtmltest\b|\blinkinator\b`)
	lycheeWord   = regexp.MustCompile(`\blychee\b`)
	offlineFlag  = regexp.MustCompile(`--offline\b`)
	trailingNote = regexp.MustCompile(`\s#\s.*$`)
)

// stripComments drops full-line comments and ` # ...` tails, so a comment that
// quotes the old loop (as go-wasmcontainers/docs's does not, but a migrated repo
// might) is not read as the loop.
func stripComments(s string) string {
	var b strings.Builder
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		b.WriteString(trailingNote.ReplaceAllString(l, ""))
		b.WriteByte('\n')
	}
	return b.String()
}

// isShell reports whether one workflow file holds the hand-written loop. All
// four signals are required: a fetcher, a status test, a loop and a grep that
// extracts URLs. Fewer would flag a deploy smoke test (`curl -w %{http_code}`
// of one known URL) as a link checker.
func isShell(text string) bool {
	return fetcher.MatchString(text) && statusTest.MatchString(text) &&
		loop.MatchString(text) && extract.MatchString(text)
}

// Classify reads a repository's workflow files (name -> text). Precedence is
// SHELL > LINKCHECK > LYCHEE > NONE: a repository still carrying the loop is
// the one to migrate, even if another job already uses linkcheck.
func Classify(files map[string]string) Verdict {
	var shell, lc, ly, ot []string
	version, tool := "", ""
	offline := false
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		t := stripComments(files[n])
		if isShell(t) {
			shell = append(shell, n)
		}
		if m := linkcheckRef.FindStringSubmatch(t); m != nil {
			lc = append(lc, n)
			if version == "" {
				version = m[1]
				if version == "" {
					version = "unpinned"
				}
			}
		}
		if m := otherRef.FindString(t); m != "" {
			ot = append(ot, n)
			if tool == "" {
				tool = strings.Fields(m)[0]
				if i := strings.LastIndex(tool, "/"); i >= 0 {
					tool = tool[i+1:]
				}
				if strings.HasPrefix(m, "landingkit") {
					tool = "landingkit-links"
				}
			}
		}
		if lycheeWord.MatchString(t) {
			ly = append(ly, n)
			if offlineFlag.MatchString(t) {
				offline = true
			}
		}
	}
	v := Verdict{Class: None}
	present := map[Class][]string{Shell: shell, Linkcheck: lc, Other: ot, Lychee: ly}
	for _, c := range []Class{Shell, Linkcheck, Other, Lychee} {
		if len(present[c]) == 0 {
			continue
		}
		if v.Class == None {
			v.Class, v.Files = c, present[c]
		} else {
			v.Also = append(v.Also, c)
		}
	}
	if v.Class == Linkcheck || containsClass(v.Also, Linkcheck) {
		v.Version = version
	}
	if v.Class == Other || containsClass(v.Also, Other) {
		v.Tool = tool
	}
	v.Offline = offline && (v.Class == Lychee || containsClass(v.Also, Lychee))
	return v
}

func containsClass(cs []Class, c Class) bool {
	for _, x := range cs {
		if x == c {
			return true
		}
	}
	return false
}
