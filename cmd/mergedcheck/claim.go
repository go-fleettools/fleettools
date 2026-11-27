package main

import (
	"regexp"
	"strconv"
	"strings"
)

// ref names a pull request: repo is "owner/name" or "" when the reply (or the
// read) did not say, number is 0 when a read could not say which pull request
// it was about.
type ref struct {
	repo   string
	number int
}

// A claim is a sentence of the reply that says a pull request is merged.
type claim struct {
	sentence string
	refs     []ref
}

// merged is the past participle, in English and French, with a boundary that
// understands accents (RE2's \b is ASCII-only, and « fusionné » ends on a
// letter it does not know).
var mergedRe = regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}_-])(?:[\p{L}]+-)?(merged|mergée?s?|fusionnée?s?)(?:[^\p{L}\p{N}_]|$)`)

// notAClaimRe is a sentence that mentions "merged" without saying that it
// happened: a negation, a future or a modal, a condition, a wait. ⛔ It is a
// heuristic over prose and errs toward allowing: a refusal costs a turn, a
// miss is one more unread claim, which is what the gate was before it existed.
var notAClaimRe = regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}_])(?:` +
	// English: negation, future and modal, condition, wait.
	`not|never|unmerged|yet to be|to be merged|will be|would be|should be|can be|could be|must be|` +
	`ready to be|needs? to be|about to|cannot|can't|won't|if|once|when|whenever|unless|until|` +
	`waiting|awaiting|` +
	// French.
	`pas|jamais|non|aucune?|sera|serait|seront|devra|devrait|doit|peut|pourra|si|dès|lorsque|quand|` +
	`une fois|attend|attente|à fusionner|prête? à` +
	`)(?:[^\p{L}\p{N}_]|$)|n['’]t(?:[^\p{L}\p{N}_]|$)`)

// attributiveRe: "the merged result", "a merged PR list" — the participle is an
// adjective, not a report. A determiner before it, or a noun of a merged
// artefact after it.
var attributiveRe = regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}_])(?:the|an|this|that|its|their|each|every|any|le|la|les|un|une|ce|cette|des|ces|chaque)\s+(?:[\p{L}-]+\s+)?(?:merged|mergée?s?|fusionnée?s?)(?:[^\p{L}\p{N}_]|$)`)

var (
	// "PR #12", "pull request 12", ".../pull/12", "PR 12".
	cuedRefRe = regexp.MustCompile(`(?i)(?:\bPRs?\s*#?|pull requests?\s*#?|\bMR\s*#?|/pull/)(\d{1,7})\b`)
	// "owner/repo#12".
	repoRefRe = regexp.MustCompile(`([A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9._-]+)#(\d{1,7})\b`)
	// a bare "#12".
	bareRefRe = regexp.MustCompile(`(?:^|[^\p{L}\p{N}_/])#(\d{1,7})\b`)
	// What precedes a bare "#N" that is an issue, not the pull request.
	issueCueRe = regexp.MustCompile(`(?i)(?:close[sd]?|fix(?:es|ed)?|resolve[sd]?|ferme[sr]?|issues?|refs?|see|voir)\s*$`)
)

// prose removes what the author of the reply did not assert: fenced and inline
// code, block quotes, and text between quotation marks.
func prose(text string) string {
	var out []string
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || strings.HasPrefix(t, ">") {
			continue
		}
		out = append(out, line)
	}
	s := strings.Join(out, "\n")
	s = stripSpans(s, '`', '`')
	s = stripSpans(s, '"', '"')
	s = stripSpans(s, '“', '”')
	s = stripSpans(s, '«', '»')
	return s
}

// stripSpans drops each open..close span; an unclosed opener drops nothing, so
// a stray quotation mark cannot hide the rest of a reply.
func stripSpans(s string, open, closer rune) string {
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); i++ {
		if rs[i] != open {
			b.WriteRune(rs[i])
			continue
		}
		end := -1
		for j := i + 1; j < len(rs); j++ {
			if rs[j] == closer {
				end = j
				break
			}
		}
		if end < 0 {
			b.WriteRune(rs[i])
			continue
		}
		b.WriteByte(' ')
		i = end
	}
	return b.String()
}

// sentences splits on newlines, semicolons and a . ! ? that ends a word, so
// "v0.1.2" and "owner/repo#12." stay whole enough.
func sentences(text string) []string {
	var out []string
	rs := []rune(text)
	start := 0
	flush := func(end int) {
		if s := strings.TrimSpace(string(rs[start:end])); s != "" {
			out = append(out, s)
		}
	}
	for i, r := range rs {
		switch {
		case r == '\n' || r == ';':
			flush(i)
			start = i + 1
		case r == '.' || r == '!' || r == '?':
			if i+1 == len(rs) || rs[i+1] == ' ' || rs[i+1] == '\n' {
				// Keep a question mark: a question is not a claim.
				end := i
				if r == '?' {
					end = i + 1
				}
				flush(end)
				start = i + 1
			}
		}
	}
	flush(len(rs))
	return out
}

// refsOf extracts the pull requests a piece of text names.
func refsOf(s string) []ref {
	var refs []ref
	seen := map[ref]bool{}
	add := func(r ref) {
		if !seen[r] {
			seen[r] = true
			refs = append(refs, r)
		}
	}
	for _, m := range repoRefRe.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.Atoi(m[2])
		add(ref{strings.ToLower(m[1]), n})
	}
	for _, m := range cuedRefRe.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.Atoi(m[1])
		add(ref{"", n})
	}
	// A bare #N counts only when it is not an issue the pull request closes,
	// and only when the same text does not already name a pull request with a
	// cue ("PR #12 merged, closes #5" claims #12 alone).
	if len(refs) == 0 {
		for _, loc := range bareRefRe.FindAllStringSubmatchIndex(s, -1) {
			before := s[:loc[2]-1] // up to the '#'
			if issueCueRe.MatchString(before) {
				continue
			}
			n, _ := strconv.Atoi(s[loc[2]:loc[3]])
			add(ref{"", n})
		}
	}
	return refs
}

// claimsIn returns the sentences of text that say a pull request is merged.
func claimsIn(text string) []claim {
	var out []claim
	for _, s := range sentences(prose(text)) {
		if strings.HasSuffix(s, "?") {
			continue
		}
		if !mergedRe.MatchString(s) || notAClaimRe.MatchString(s) {
			continue
		}
		// Every participle in the sentence is attributive: no claim.
		if attributiveRe.MatchString(s) && len(mergedRe.FindAllString(s, -1)) == len(attributiveRe.FindAllString(s, -1)) {
			continue
		}
		out = append(out, claim{sentence: s, refs: refsOf(s)})
	}
	return out
}
