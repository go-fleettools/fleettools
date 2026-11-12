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
	because = regexp.MustCompile(`(?i)\b(because|since|due\s+to|owing\s+to|on\s+account\s+of|as\s+(it|that|this|they|doing)|given|so\s+as\s+to|in\s+order\s+to|to\s+(keep|preserve|maintain|retain|stay|remain|honou?r|protect)|for\s+the\s+sake\s+of|which\s+(would\s+|will\s+|could\s+)?(break|violate|lose|change|alter|give\s+up|sacrific\w+|forfeit)|would\s+(break|violate|lose|change|alter|give\s+up|sacrific\w+|forfeit|void|invalidate)|breaks?|violat\w+|invalidat\w+|conflict\w*|incompatible|contract|guarantee|forbid\w*|prevent\w*|precludes?|so|therefore|hence|thus|means)\b`)

	// wouldBreak: "<perf change> would break bit parity".
	wouldBreak = regexp.MustCompile(`(?i)\b(would|will|could|might|may|can)\s+(also\s+)?(break|violate|lose|give\s+up|sacrifice|forfeit|void|invalidate|change|alter)\b[^.]{0,60}?(` + parity.String() + `)`)

	// contractChange: "that is a contract change (bit identity ...)".
	contractChange = regexp.MustCompile(`(?i)\b(contract\s+change|changes?\s+(to\s+)?the\s+(\w+\s+)?contract|breaks?\s+the\s+(\w+\s+)?contract)\b`)

	// keepContract: "keep the bit-identity contract" beside an option.
	keepContract = regexp.MustCompile(`(?i)\b(keep|keeping|preserve|preserving|retain|maintain|maintaining)\b[^.]{0,40}?(` + parity.String() + `)[^.]{0,30}?\b(contract|guarantee|requirement)\b`)

	// noBitIdentical: "no bit-identical reduction exists; no code change" — the
	// gap is left because no kernel keeps the bits.
	noBitIdentical = regexp.MustCompile(`(?i)\bno\s+(\w+\s+)?bit[\s-]*(identical|exact)\s+\w+`)

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
		l = quoted.ReplaceAllString(l, " ")
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

// HasMeasuredBound reports whether some sentence cites an error beside a
// number written as a number.
func HasMeasuredBound(us []Unit) bool {
	for _, u := range us {
		if errWord.MatchString(u.Text) && number.MatchString(u.Text) {
			return true
		}
	}
	return false
}

// boundInParagraph says the measured bound sits in the SAME paragraph (or list
// item) as sentence i: a number three sections away does not excuse a deferral.
func boundInParagraph(us []Unit, i int) bool {
	var para []Unit
	for _, u := range us {
		if u.para == us[i].para {
			para = append(para, u)
		}
	}
	return HasMeasuredBound(para)
}

// Scan returns the sentences of text that defer or decline a performance change
// because of bit identity. It reads at most MaxText bytes; the rest is not
// looked at, which the caller must say (see Gate).
func Scan(text string) []Finding {
	if len(text) > MaxText {
		text = text[:MaxText]
	}
	us := Units(text)
	var out []Finding
	for i, u := range us {
		if rule := classify(u.Text, Window(us, i)); rule != "" && !boundInParagraph(us, i) {
			out = append(out, Finding{Rule: rule, Sentence: u.Text, Line: u.Line})
		}
	}
	return out
}

// classify names the rule a sentence fires, or "" — the sentence needs a parity
// cue to be a candidate at all.
func classify(s, win string) string {
	if !parity.MatchString(s) {
		return ""
	}
	inPerf := perf.MatchString(win)
	switch {
	case perf.MatchString(s) && decline.MatchString(s) && governs(s):
		return "declined-because-parity"
	case inPerf && wouldBreak.MatchString(s):
		return "would-break-parity"
	case inPerf && contractChange.MatchString(s):
		return "contract-change"
	case inPerf && keepContract.MatchString(s) && optionWords.MatchString(win):
		return "keep-parity-contract"
	case noBitIdentical.MatchString(s) && !noLonger.MatchString(s):
		return "no-bit-identical-way"
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
		if !parity.MatchString(c) {
			continue
		}
		if because.MatchString(c) {
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
