// Package paritytext reads prose and decides whether it defers or declines a
// performance change BECAUSE the change would break bit identity ("bit
// parity") with a reference implementation.
//
// The rule is the fleet decision of 2026-11-11: bit identity with a reference
// is a PORTING STEP, never the contract. The contract for numeric code is
// "correct and as fast as possible" — a stated error bound, asserted against an
// extended-precision oracle, with the bit-exact arithmetic kept only as an
// option. A performance gap is never parked because "it would break
// bit-parity". The model is go-fft/fft#121; go-fft/fft#116 is the parking
// pattern: a faster real-input path "rounds differently", "is a contract
// change", so "it needs a decision, not a drive-by".
//
// # What a finding is
//
// A sentence is a finding when it has the three parts of the pattern and the
// text cites no measured bound:
//
//   - a PARITY cue: bit-identical, bit-exact, bit-for-bit, bit parity,
//     bit identity, bitwise identical, "rounds differently", "differs in the
//     last bits"...;
//   - a BLOCKING relation tying it to a performance change: "because", "would
//     break", "contract change", "keep the bit-identity contract" beside an
//     option, a decline or a deferral;
//   - a PERFORMANCE cue in the sentence or its neighbours (faster, SIMD, FMA,
//     kernel, reorder, gap...).
//
// The three are separate regular expressions, listed below, so that a finding
// says which rule fired and the reader can disagree with a rule rather than
// with a score. A sentence that only MENTIONS parity ("output unchanged,
// bit-identical") has no blocking relation and passes: that is the commonest
// sentence in a performance pull request and a gate that fired on it would be
// worse than none.
//
// # What is not read
//
// Fenced code, inline code, block quotes and HTML comments are blanked: a test
// name (`TestFFTRealBitIdenticalToPromotedFFT`) or a quoted earlier comment is
// not the author's statement.
//
// # The exemption and the waiver
//
// A text that cites a MEASURED error bound passes: a sentence naming an error
// (error, ulp, RMS, tolerance, bound...) beside a number written as a number
// (2.2e-16, 3 ulp, 4ε). "Within tolerance" is not a measurement. Otherwise a
// waiver, a REASON that is STAMPED into the published text, lets it through.
package paritytext

import (
	"fmt"
	"regexp"
	"strings"
)

// Issue is the model: the decision, with its reasons, in the tracker.
const Issue = "go-fft/fft#121"

// Parked is the example of the pattern the gate catches.
const Parked = "go-fft/fft#116"

// Rule names the fleet rule, quoted in every refusal.
const Rule = "bit identity with a reference is a porting step, never the contract (decision 2026-11-11)"

// StampPrefix begins the line a waiver publishes; the scanner skips such a line
// so that a reason can say what it needs to.
const StampPrefix = "⚠ Published with a bit-parity waiver, by express reason: "

// MaxText bounds what Scan reads: a body is a page, not a corpus.
const MaxText = 1 << 20

// Unit is one sentence and where it starts.
type Unit struct {
	Text string
	Line int // 1-based
	para int
}

// Finding is a sentence that defers or declines a performance change because of
// bit identity.
type Finding struct {
	Rule     string // which pattern fired
	Sentence string
	Line     int
}

func (f Finding) String() string {
	s := strings.Join(strings.Fields(f.Sentence), " ")
	if r := []rune(s); len(r) > 240 {
		s = string(r[:240]) + "…"
	}
	return fmt.Sprintf("line %d [%s]: %s", f.Line, f.Rule, s)
}

// The cues. Each is a separate expression so a finding can name the rule.
var (
	// parity is bit identity with a reference, or its paraphrase in the
	// parking pattern ("rounds differently").
	bitIdentity = regexp.MustCompile(`(?i)bit[\s-]*(identical(ly)?|identity|exact(ness|ly)?|for[\s-]*bit|parity|equal|compatib\w+|reproducib\w+)` +
		`|bitwise[\s-]*(identical|equal|same|exact|reproducib\w+)`)
	parity = regexp.MustCompile(bitIdentity.String() +
		`|rounds?\s+differently` +
		`|differs?\s+in\s+the\s+last\s+(bits?|digits?|ulps?)` +
		`|changes?\s+the\s+last\s+(bits?|digits?|ulps?)`)

	// perf is a performance change or the gap it would close.
	perf = regexp.MustCompile(`(?i)\b(faster|fastest|speed\w*|slower|slow|perf|performance|throughput|ns/op|simd|avx\w*|neon|sve\w*|fma|vectori[sz]\w*|kernels?|optimi[sz]\w*|cheaper|gap|reorder\w*|reassociat\w*|fused|fusing|real-input|algorithm|radix|unroll\w*|asm|assembly|cache-blocking)\b`)

	// decline is a no, a deferral, a park or a "needs a decision".
	decline = regexp.MustCompile(`(?i)\b(defer\w*|park\w*|declin\w+|reject\w*|abandon\w*|postpon\w+|skip\w*|shelv\w+|drop(ped|ping)?|rul(e|ed|es|ing)\s+out|out\s+of\s+scope|wontfix|not\s+planned|blocked|held\s+back|needs?\s+a\s+decision|decision\s+needed|only\s+as\s+(a\s+|an\s+)?(separate|opt\w*)|` +
		`(won'?t|will\s+not|would\s+not|wouldn'?t|do(es)?\s+not|don'?t|doesn'?t|did\s+not|didn'?t|cannot|can'?t|can\s+not|could\s+not|couldn'?t|unable\s+to|not)\s+(\w+\s+){0,3}(use|enable|adopt|apply|ship|take|do|port|add|implement|turn|switch|vectori[sz]e|optimi[sz]e|reorder|fuse|merge|change|go|worth|allowed|possible)|` +
		`(keep|keeps|kept|keeping|stay|stays|remain|remains|leave|leaves|left)\s+(the\s+)?(scalar|generic|reference|existing|current|old|plain|portable|pure-go|as\s+is|unchanged|on\s+the))\b`)

	// because ties the decline to the parity cue.
	because = regexp.MustCompile(`(?i)\b(because|since|due\s+to|owing\s+to|on\s+account\s+of|as\s+(it|that|this|they|doing)|given|so\s+as\s+to|in\s+order\s+to|to\s+(keep|preserve|maintain|retain|stay|remain|honou?r|protect)|for\s+the\s+sake\s+of|which\s+(would\s+|will\s+|could\s+)?(break|violate|lose|change|alter|give\s+up|sacrific\w+|forfeit)|would\s+(break|violate|lose|change|alter|give\s+up|sacrific\w+|forfeit|void|invalidate)|breaks?|violat\w+|invalidat\w+|conflict\w*|incompatible|contract|guarantee|forbid\w*|prevent\w*|precludes?)\b`)

	// wouldBreak: "<perf change> would break bit parity".
	wouldBreak = regexp.MustCompile(`(?i)\b(would|will|could|might|may|can)\s+(also\s+)?(break|violate|lose|give\s+up|sacrifice|forfeit|void|invalidate|change|alter)\b[^.]{0,60}?(` + parity.String() + `)`)

	// contractChange: "that is a contract change (bit identity ...)".
	contractChange = regexp.MustCompile(`(?i)\b(contract\s+change|changes?\s+(to\s+)?the\s+(\w+\s+)?contract|breaks?\s+the\s+(\w+\s+)?contract)\b`)

	// keepContract: "keep the bit-identity contract" beside an option.
	keepContract = regexp.MustCompile(`(?i)\b(keep|keeping|preserve|preserving|retain|maintain|maintaining)\b[^.]{0,40}?(` + parity.String() + `)[^.]{0,30}?\b(contract|guarantee|requirement)\b`)

	// noBitIdentical: "no bit-identical reduction exists; no code change" — the
	// gap is left because no kernel keeps the bits.
	noBitIdentical = regexp.MustCompile(`(?i)\bno\s+(\w+\s+)?bit[\s-]*(identical|exact)\s+\w+` +
		`|bit[\s-]*(identical|exact)\s+(\w+\s+){1,3}?(is|are)\s+(not|never)\s+(expressible|possible|available|achievable)` +
		`|bit[\s-]*(identical|exact)\s+(\w+\s+){1,3}?(cannot|can'?t)\s+be\s+(built|expressed|made|done|written)`)

	// reductionIdentity: "no bit-identical reduction identity to feed combine" is
	// about an identity ELEMENT of a reduction, not a missing kernel.
	reductionIdentity = regexp.MustCompile(`(?i)\bidentity\s+(element|value|to\b)`)

	// noVectorISA: where the assembler or the instruction set has no vector
	// double arithmetic, the kernel is INEXPRESSIBLE and parity is not what blocks
	// it ("the Go ppc64 assembler exposes no vector double arithmetic"). Read in
	// the SAME sentence only: a neighbour's "no vector FMA" must not excuse a
	// different sentence's deferral.
	noVectorISA = regexp.MustCompile(`(?i)\b(assembler|isa|instruction\s+set|hardware|cpu|vector\s+facility)\b[^.;]{0,60}?\b(has|have|exposes?|provides?|offers?|lacks?|without)\s+(\*\*)?(no|a)?\s*(vector|simd)` +
		`|\bno\s+vector\s+(double|float|arithmetic|instruction|fma|multiply|add)` +
		`|\binexpressible\b|\bcannot\s+be\s+expressed\s+in\s+(the\s+)?(go\s+)?assembler`)

	// leftButClosed: "**left** (already fused in order, PR #7)" says the gap is
	// ALREADY closed.
	leftButClosed = regexp.MustCompile(`(?i)\*\*left\*\*\s*\(\s*(already|done|fixed|closed|shipped|merged|resolved)\b`)

	// leftOnParity: a table row validated "bit for bit" whose change is
	// "**left**" — the gap is left open, and the row says what it is held by.
	leftOnParity = regexp.MustCompile(`(?i)\*\*left\*\*|\bleft\s+(open|as\s+is|alone|for\s+now)\b`)

	// stayToKeep: "each sum must stay sequential to keep the results
	// bit-identical" — an order is frozen for the sake of the bits.
	stayToKeep = regexp.MustCompile(`(?i)\b(must|has\s+to|have\s+to|needs?\s+to|can\s+only)\b[^.;]{0,50}?\bto\s+(keep|preserve|maintain|retain)\b[^.;]{0,40}?(` + parity.String() + `)`)

	// noLonger: "the outputs are no longer bit-identical" ANNOUNCES the end of
	// bit identity (the decision's own direction); it is not "no bit-identical
	// way exists".
	noLonger = regexp.MustCompile(`(?i)\bno\s+longer\s+bit`)

	// quoted: text inside double quotes is quoted, not asserted — a body that
	// quotes the rule ("we skip FMA because it breaks bit-parity") is not
	// deferring anything.
	quoted = regexp.MustCompile("\"[^\"\n]*\"|“[^”\n]*”")

	// clauseSplit cuts a sentence where a new thought starts, so that "because"
	// is read together with the parity cue it governs and not with one three
	// clauses away.
	clauseSplit = regexp.MustCompile(`[;:,]|\s+(?:but|and|while|though|although|whereas)\s+`)
	leadingSo   = regexp.MustCompile(`(?i)^\s*(so|therefore|hence|thus|which\s+means)\b`)

	// blocksParity: "what blocks a bit-identical kernel is the missing FMA".
	blocksParity = regexp.MustCompile(`(?i)\b(blocks?|blocked|prevents?|forbids?|rules?\s+out|precludes?|stops?)\b[^.]{0,30}?bit[\s-]*(identical|exact)`)

	optionWords = regexp.MustCompile(`(?i)\b(option|separate|opt-in|opt\s+in|only\s+as|instead|flag)\b`)

	// errWord and number make a measured bound: an error word beside a number
	// written as a number.
	errWord = regexp.MustCompile(`(?i)\b(error|errors|ulps?|rms|tolerance|bound|bounds|epsilon|eps|accuracy)\b|ε`)
	number  = regexp.MustCompile(`(?i)(^|[^\w.])(\d+(\.\d+)?e[+-]?\d+|\d+(\.\d+)?\s*(ulps?|ε|eps\b|epsilon)|\d*\.\d+\s*(ulps?|ε)|10[-⁻]\d+|1/\d{4,})`)

	fence    = regexp.MustCompile("^\\s*(```|~~~)")
	inline   = regexp.MustCompile("`[^`\n]*`")
	htmlCmt  = regexp.MustCompile(`<!--.*?-->`)
	urlRe    = regexp.MustCompile(`https?://\S+`)
	listItem = regexp.MustCompile(`^\s*([-*+]|\d+[.)])\s`)
	heading  = regexp.MustCompile(`^\s{0,3}#{1,6}\s`)
)

// RuleSummary is the rule in one line; a text may quote it (and only it)
// without being read as deferring anything.
const RuleSummary = "a performance gap is never parked because it would break bit parity"

// unquote blanks a quote of the rule's own summary, and otherwise keeps the
// quoted words (without the quote marks): quoting a deferral is how an author
// would hide one — `we do not use the kernel "because it would break bit parity"`.
func unquote(q string) string {
	inner := strings.Trim(q, "\"“” ")
	if strings.EqualFold(strings.Trim(inner, ".,;: "), RuleSummary) {
		return " "
	}
	return " " + inner + " "
}

// citing says the words just before a quotation FRAME it as text being cited —
// what a repository's comment says, what a pull request removes or replaces —
// and not as the author's own claim. The frame must name a TEXT ("the comment
// says", "the old text said") or an act on text ("removed", "replaced", "was:"):
// "the maintainers said" is a person's claim, and a quoted deferral after it is
// still a deferral.
var citing = regexp.MustCompile(`(?i)\b(?:(?:comments?|text|docs?|docstring|godoc|readme|line|sentence|note|paragraph|section|code)\s+(?:says?|said|reads?|read|states?|stated|claims?|claimed)|(?:removes?|removed|removing|replaces?|replaced|replacing|deletes?|deleted|deleting|drops?|dropped)\s*:?|(?:was|were)\s*:)\s*$`)

// unquoteLine applies unquote to every quotation of a line, blanking the ones
// that cite text (their words are not the author's statement).
func unquoteLine(l string) string {
	idx := quoted.FindAllStringIndex(l, -1)
	if idx == nil {
		return l
	}
	var b strings.Builder
	last := 0
	for _, m := range idx {
		b.WriteString(l[last:m[0]])
		if citing.MatchString(l[:m[0]]) {
			b.WriteString(" ")
		} else {
			b.WriteString(unquote(l[m[0]:m[1]]))
		}
		last = m[1]
	}
	b.WriteString(l[last:])
	return b.String()
}

// Strip blanks what is not the author's statement, keeping every newline so
// that line numbers survive.
func Strip(text string) string {
	lines := strings.Split(text, "\n")
	// Only the LAST non-blank line can be a stamp: a stamp anywhere else would
	// be a way to write a deferral and have it skipped.
	last := len(lines) - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	if last >= 0 && strings.HasPrefix(lines[last], StampPrefix) {
		lines[last] = ""
	}
	inFence := false
	inCmt := false
	for i, l := range lines {
		switch {
		case fence.MatchString(l):
			inFence = !inFence
			lines[i] = ""
			continue
		case inFence:
			lines[i] = ""
			continue
		case strings.HasPrefix(strings.TrimSpace(l), ">"):
			lines[i] = ""
			continue
		}
		// An HTML comment may span lines.
		if inCmt {
			if j := strings.Index(l, "-->"); j >= 0 {
				l = l[j+3:]
				inCmt = false
			} else {
				lines[i] = ""
				continue
			}
		}
		l = htmlCmt.ReplaceAllString(l, " ")
		if j := strings.Index(l, "<!--"); j >= 0 {
			l = l[:j]
			inCmt = true
		}
		l = inline.ReplaceAllString(l, " ")
		l = unquoteLine(l)
		l = urlRe.ReplaceAllString(l, " ")
		lines[i] = l
	}
	return strings.Join(lines, "\n")
}

// Units splits stripped prose into sentences with their line numbers. A blank
// line, a list item, a heading or a table row begins a new paragraph.
func Units(text string) []Unit {
	var out []Unit
	var cur []string
	var nos []int // the line each element of cur came from
	para := 0
	flush := func() {
		if len(cur) > 0 {
			// Each sentence is placed on the line its first character came from.
			at, k := 0, 0
			ends := make([]int, len(cur)) // rune offset where each line's text ends in the joined paragraph
			for i, c := range cur {
				at += len([]rune(c)) + 1
				ends[i] = at
			}
			for _, s := range sentences(strings.Join(cur, " ")) {
				for k < len(cur)-1 && s.at >= ends[k] {
					k++
				}
				out = append(out, Unit{Text: s.text, Line: nos[k], para: para})
			}
			para++
		}
		cur, nos = nil, nil
	}
	for i, l := range strings.Split(Strip(text), "\n") {
		t := strings.TrimSpace(l)
		switch {
		case t == "":
			flush()
			continue
		case listItem.MatchString(l), heading.MatchString(l), strings.HasPrefix(t, "|"):
			flush()
		}
		cur = append(cur, t)
		nos = append(nos, i+1)
	}
	flush()
	return out
}

var abbrev = map[string]bool{"e.g": true, "i.e": true, "vs": true, "etc": true, "cf": true, "approx": true, "fig": true, "eq": true, "no": true}

// sentences splits at . ! ? followed by space and a capital, digit, quote or
// bracket, except after an abbreviation.
func sentences(s string) []span {
	var out []span
	r := []rune(s)
	from := 0
	for i := 0; i < len(r); i++ {
		if r[i] != '.' && r[i] != '!' && r[i] != '?' {
			continue
		}
		j := i + 1
		for j < len(r) && (r[j] == ')' || r[j] == '"' || r[j] == '\'' || r[j] == '*' || r[j] == '_') {
			j++
		}
		if j >= len(r) || r[j] != ' ' {
			continue
		}
		k := j
		for k < len(r) && r[k] == ' ' {
			k++
		}
		if k >= len(r) {
			continue
		}
		if c := r[k]; !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '"' || c == '(' || c == '[' || c == '*' || c == '`') {
			continue
		}
		w := i
		for w > from && r[w-1] != ' ' {
			w--
		}
		if abbrev[strings.ToLower(string(r[w:i]))] {
			continue
		}
		out = append(out, span{strings.TrimSpace(string(r[from:j])), from})
		from = k
		i = k - 1
	}
	if t := strings.TrimSpace(string(r[from:])); t != "" {
		out = append(out, span{t, from})
	}
	return out
}

// span is a sentence and the rune offset it starts at in its paragraph.
type span struct {
	text string
	at   int
}

// Window is a cue's reach: the sentence and its neighbours in the paragraph.
func Window(us []Unit, i int) string {
	var b strings.Builder
	for j := i - 1; j <= i+1; j++ {
		if j >= 0 && j < len(us) && us[j].para == us[i].para {
			b.WriteString(us[j].Text)
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// HasBitIdentity reports whether s uses a bit-identity word, without the
// paraphrases the parking pattern uses.
func HasBitIdentity(s string) bool { return bitIdentity.MatchString(s) }

// boundedParagraphs says which paragraphs (or list items) hold a measured bound:
// a number three sections away does not excuse a deferral. One pass over the
// sentences — computing it per finding made a 1 MiB paragraph take minutes.
func boundedParagraphs(us []Unit) map[int]bool {
	m := map[int]bool{}
	for _, u := range us {
		if !m[u.para] && errWord.MatchString(u.Text) && number.MatchString(u.Text) {
			m[u.para] = true
		}
	}
	return m
}

// Scan returns the sentences of text that defer or decline a performance change
// because of bit identity. It reads at most MaxText bytes; the rest is not
// looked at, which the caller must say (see Gate).
func Scan(text string) []Finding {
	if len(text) > MaxText {
		text = text[:MaxText]
	}
	us := Units(text)
	bounded := boundedParagraphs(us) // once per paragraph, not once per finding
	// The performance cue is read once per sentence; a sentence's window is it
	// and its paragraph neighbours.
	perfS := make([]bool, len(us))
	for i, u := range us {
		perfS[i] = perf.MatchString(u.Text)
	}
	var out []Finding
	for i, u := range us {
		inPerf := perfS[i]
		for _, j := range [2]int{i - 1, i + 1} {
			if j >= 0 && j < len(us) && us[j].para == u.para && perfS[j] {
				inPerf = true
			}
		}
		rule := classify(u.Text, perfS[i], inPerf, func() string { return Window(us, i) })
		if rule != "" && !bounded[u.para] {
			out = append(out, Finding{Rule: rule, Sentence: u.Text, Line: u.Line})
		}
	}
	return out
}

// classify names the rule a sentence fires, or "" — the sentence needs a parity
// cue to be a candidate at all. perfSent and inPerf say whether the sentence,
// and its window, carry a performance cue; win builds the window text only for
// the rule that reads it.
func classify(s string, perfSent, inPerf bool, win func() string) string {
	if !parity.MatchString(s) {
		return ""
	}
	switch {
	case perfSent && decline.MatchString(s) && governs(s):
		return "declined-because-parity"
	case inPerf && wouldBreak.MatchString(s):
		return "would-break-parity"
	case inPerf && contractChange.MatchString(s):
		return "contract-change"
	case inPerf && keepContract.MatchString(s) && optionWords.MatchString(win()):
		return "keep-parity-contract"
	case noBitIdentical.MatchString(s) && !noLonger.MatchString(s) && !reductionIdentity.MatchString(s) && !noVectorISA.MatchString(s):
		return "no-bit-identical-way"
	case leftOnParity.MatchString(s) && !leftButClosed.MatchString(s):
		return "left-on-parity"
	case inPerf && stayToKeep.MatchString(s):
		return "stay-to-keep-parity"
	case inPerf && blocksParity.MatchString(s):
		return "parity-blocks-kernel"
	}
	return ""
}

// governs says the "because" is about the parity cue: a clause holds both, or a
// clause with the cue is followed by one that opens with "so"/"therefore".
// "We kept the scalar path because the kernel was slower; output is bit-identical
// either way" has a because and a parity cue three words apart and no link
// between them.
func governs(s string) bool {
	clauses := clauseSplit.Split(s, -1)
	for i, c := range clauses {
		loc := parity.FindStringIndex(c)
		if loc == nil {
			continue
		}
		// The cause comes BEFORE the cue it makes a reason: "because it would
		// break bit parity". "…bit-identical because every product is rounded the
		// same" explains the identity; it does not defer anything for it.
		if because.MatchString(c[:loc[0]]) {
			return true
		}
		if i+1 < len(clauses) && leadingSo.MatchString(clauses[i+1]) {
			return true
		}
	}
	return false
}

// Stamp is the line a waiver publishes beside the text it excused.
func Stamp(reason string) string {
	return StampPrefix + strings.Join(strings.Fields(reason), " ")
}

// Refusal is what the author reads: it names the rule and the model, the
// sentences, and the two ways forward.
func Refusal(what string, found []Finding, flag string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "the %s defers or declines a performance change because of bit identity, in %d place(s).\n", what, len(found))
	fmt.Fprintf(&b, "  Fleet rule: %s.\n", Rule)
	fmt.Fprintf(&b, "  The contract is a STATED error bound asserted against an extended-precision oracle; bit-exact arithmetic is an option; a performance gap is never parked because it would break bit parity.\n")
	fmt.Fprintf(&b, "  Model: %s; the pattern it replaces: %s.\n", Issue, Parked)
	for i, f := range found {
		if i == 5 {
			fmt.Fprintf(&b, "  … and %d more\n", len(found)-i)
			break
		}
		fmt.Fprintf(&b, "  %s\n", f)
	}
	fmt.Fprintf(&b, "Say what was MEASURED (an error beside a number: \"relative RMS error 3.1e-16 against double-double, bound 4.4e-15\"), or, if parity really is the subject, pass %s \"<why>\" — the reason is appended to the published text.", flag)
	return fmt.Errorf("%s", b.String())
}

// Gate returns text, with a waiver's stamp appended when one was needed, or the
// refusal. A text over MaxText is refused rather than half read: an unread
// answer is not a pass.
func Gate(text, waiver, flag string) (string, error) {
	if len(text) > MaxText {
		return "", fmt.Errorf("the text is over %d bytes, which the bit-parity gate does not read; shorten it", MaxText)
	}
	found := Scan(text) // a finding with a measured bound in its paragraph is not one
	if len(found) == 0 {
		return text, nil
	}
	if strings.TrimSpace(waiver) == "" {
		return "", Refusal("text", found, flag)
	}
	return strings.TrimRight(text, "\n") + "\n\n" + Stamp(waiver) + "\n", nil
}

// GateTitle refuses a title that defers a performance change because of bit
// identity. No waiver covers a title: it cannot carry the reason.
func GateTitle(t, flag string) error {
	// Backticks are dropped, not blanked: a title cannot be a code span's
	// neighbour ("`FMA` skipped because bit-parity") and lose its subject.
	if found := Scan(strings.ReplaceAll(t, "`", " ")); len(found) > 0 {
		return Refusal("title (no waiver covers a title: it cannot carry the reason; rewrite it)", found, flag)
	}
	return nil
}
