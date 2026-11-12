package paritytext

import (
	"strings"
	"testing"
	"time"
)

// refusing are texts that defer or decline a performance change because of bit
// identity. The first two are go-fft/fft#116's own sentences.
var refusing = []struct{ name, text, rule string }{
	{"116 contract change",
		"A real-input path is cheaper but rounds differently. That is a contract change (bit identity with the documented definition), so it needs a decision, not a drive-by.",
		"contract-change"},
	{"116 keep the contract",
		"Keep the bit-identity contract for FFTReal (and add the real path only as a separate function or option), and port rfftp for odd n.\nA faster kernel is the reason.",
		"keep-parity-contract"},
	{"because parity",
		"We do not use FMA in the default kernel because it would break bit-parity.",
		"declined-because-parity"},
	{"would break",
		"Reordering the reduction would break bit-identical output, and the SIMD kernel needs it.",
		"would-break-parity"},
	{"no way",
		"- No bit-identical reduction exists; no code change, no release.",
		"no-bit-identical-way"},
	{"blocks",
		"What blocks a bit-identical kernel on loong64 is the missing vector FMA.",
		"parity-blocks-kernel"},
	{"so we skip",
		"Output must stay bit-identical, so we skip the SIMD kernel.",
		"declined-because-parity"},
	{"skip simd",
		"We skip the AVX2 kernel since it is not bit-exact with the scalar path.",
		"declined-because-parity"},
}

// passing are texts that mention bit identity without deferring anything for
// it: the commonest sentences in a performance pull request.
var passing = []string{
	"Output unchanged, bit-identical.",
	"The new SIMD kernel is bit-identical to the scalar path, so we keep it enabled.",
	"Bit-identical to the Go loops; about 1.2x faster on Zen 3.",
	"Tests: per-kernel bit identity in all four layouts, 19 whole factorizations.",
	"No arithmetic changes. TestFoo passes bit for bit on Haswell.",
	"Results are byte-identical to the previous release.",
	"Regeneration reproduces the checked-in assembly byte-for-byte.",
	"We do not need a fallback; the kernel is bit-identical.",
	"A plain sentence with no cue at all.",
	"The radix-32 pass was built bit-identical and lost on every length, so no length is routed to it.",
	// go-avkit/decode#37, which follows go-fft/fft#121: the end of bit identity,
	// announced, with the accuracy gate named.
	"The outputs are no longer bit-identical to main (a different FFT); the gate is accuracy: TestAACAccuracy, TestAACMDCTMatchesDefinition, conformance, copycheck 0, covergate, race, vet (386) green.",
	"The outputs are no longer bit-identical to main, which is fine: the AVX2 kernel is 2x faster.",
	// A quote of the rule is not a deferral.
	`The gate enforces "a performance gap is never parked because it would break bit parity" unless the text cites a bound; we skip the SIMD kernel.`,
	"The rule reads “A performance gap is never parked because it would break bit parity.”",
	// The "because" is about something else; parity is only mentioned.
	"We kept the scalar path because the SIMD kernel was slower on M4; output is bit-identical either way.",
	"We do not use FMA here; output is bit-identical.",
	// No performance cue anywhere: these are about something else.
	"This would break bit-identical output.",
	"That is a contract change (bit identity with the documented definition).",
	"Keep the bit-identity contract, as an option.",
	"What blocks a bit-identical match is the host's endianness.",
}

func TestRefusing(t *testing.T) {
	for _, c := range refusing {
		t.Run(c.name, func(t *testing.T) {
			got := Scan(c.text)
			if len(got) == 0 {
				t.Fatalf("not refused: %q", c.text)
			}
			found := false
			for _, f := range got {
				if f.Rule == c.rule {
					found = true
				}
			}
			if !found {
				t.Errorf("rules %v, want %s", got, c.rule)
			}
			if _, err := Gate(c.text, "", "--parity-waiver"); err == nil {
				t.Error("Gate let it through")
			}
		})
	}
}

func TestPassing(t *testing.T) {
	for _, s := range passing {
		if got := Scan(s); len(got) != 0 {
			t.Errorf("%q refused: %v", s, got)
		}
		if out, err := Gate(s, "", "--parity-waiver"); err != nil || out != s {
			t.Errorf("%q: Gate = %q, %v", s, out, err)
		}
	}
}

// TestMutationRemovingTheClausePasses is the mutation: the refusing sentence
// without its "because parity" clause passes.
func TestMutationRemovingTheClausePasses(t *testing.T) {
	refused := "We do not use FMA in the default kernel because it would break bit-parity."
	if len(Scan(refused)) == 0 {
		t.Fatal("the fixture must refuse")
	}
	mutated := strings.Replace(refused, " because it would break bit-parity", "", 1)
	if got := Scan(mutated); len(got) != 0 {
		t.Errorf("without the clause it still refuses: %v", got)
	}
}

func TestMeasuredBoundExempts(t *testing.T) {
	base := "We skip the AVX2 kernel because it would break bit-parity."
	if len(Scan(base)) == 0 {
		t.Fatal("fixture")
	}
	for _, bound := range []string{
		"Measured relative RMS error 3.1e-16 against double-double, bound 4.4e-15.",
		"The error stays under 2 ulp on every length.",
		"Max error 4ε at n = 1024.",
	} {
		if out, err := Gate(base+" "+bound, "", "--parity-waiver"); err != nil || !strings.Contains(out, bound) {
			t.Errorf("%q: %v", bound, err)
		}
	}
	// Words without a number are not a measurement.
	if _, err := Gate(base+" The error is within tolerance.", "", "--parity-waiver"); err == nil {
		t.Error("\"within tolerance\" passed as a measurement")
	}
	// A number with no error word is not one either.
	if _, err := Gate(base+" It ran 3.5e-3 seconds.", "", "--parity-waiver"); err == nil {
		t.Error("a bare number passed as a measurement")
	}
}

// A bound elsewhere in the text does not excuse a deferral: it must be in the
// finding's own paragraph.
func TestBoundMustBeInTheFindingsParagraph(t *testing.T) {
	deferral := "We skip the AVX2 kernel because it would break bit-parity."
	bound := "Measured relative RMS error 3.1e-16 against double-double."
	if got := Scan(deferral + "\n\n" + bound); len(got) != 1 {
		t.Errorf("a bound in another paragraph excused the deferral: %v", got)
	}
	if got := Scan(deferral + " " + bound); len(got) != 0 {
		t.Errorf("a bound in the same paragraph did not: %v", got)
	}
	if got := Scan("- " + deferral + "\n- " + bound); len(got) != 1 {
		t.Errorf("a bound in the next list item excused it: %v", got)
	}
}

// Quoting is not a bypass: only the rule's own summary is exempt.
func TestQuotingADeferralIsStillADeferral(t *testing.T) {
	for _, s := range []string{
		`We do not use the SIMD kernel "because it would break bit parity", per the maintainers.`,
		"We do not use the SIMD kernel “because it would break bit parity”, per the maintainers.",
		`The maintainers said "we skip the SIMD kernel because it would break bit parity".`,
	} {
		if len(Scan(s)) == 0 {
			t.Errorf("quoting hid a deferral: %q", s)
		}
	}
	quote := `The rule is "` + RuleSummary + `".`
	if got := Scan(quote + " We skip the SIMD kernel."); len(got) != 0 {
		t.Errorf("an exact quote of the rule was refused: %v", got)
	}
	if len(Scan(`We skip the SIMD kernel: "`+strings.ToUpper(RuleSummary)+`" because it would break bit parity.`)) == 0 {
		t.Error("a quote followed by a deferral passed")
	}
}

// A worst case the size MaxText allows must scan in well under a second: the
// bound was once recomputed per finding, which made one 1 MiB paragraph take
// minutes.
func TestScanIsNotQuadratic(t *testing.T) {
	unit := "We skip the SIMD kernel because it would break bit parity and the radix gap. "
	text := strings.Repeat(unit, MaxText/len(unit))
	start := time.Now()
	got := Scan(text)
	d := time.Since(start)
	t.Logf("Scan of %d bytes: %v, %d findings", len(text), d, len(got))
	budget := time.Second
	if raceOn {
		budget = 10 * time.Second
	}
	if d > budget {
		t.Fatalf("1 MiB of findings took %v (budget %v)", d, budget)
	}
	if len(got) < 10000 {
		t.Errorf("only %d findings: the worst case was not exercised", len(got))
	}
	start = time.Now()
	if _, err := Gate(text, "because", "--parity-waiver"); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > budget {
		t.Errorf("Gate took %v", d)
	}
	// Four times the text must cost well under sixteen times the time: a
	// quadratic scan fails this on any machine, race detector or not.
	unitText := func(n int) time.Duration {
		s := strings.Repeat(unit, n)
		t0 := time.Now()
		Scan(s)
		return time.Since(t0)
	}
	small, big := unitText(1500), unitText(6000)
	if big > 10*small+50*time.Millisecond {
		t.Errorf("4x the text took %v vs %v: not linear", big, small)
	}
}

// Each cue is paired: the sentence fires, and the same sentence minus that cue
// passes, so removing the cue from the code breaks the passing half.
func TestPairedCues(t *testing.T) {
	for _, c := range []struct{ cue, fires, passes string }{
		{"because governs the parity clause",
			"We skip the SIMD kernel because it breaks bit parity.",
			"We skip the SIMD kernel because it is slower; it keeps bit parity."},
		{"a performance cue",
			"The SIMD kernel would break bit-identical output.",
			"This would break bit-identical output."},
		{"a decline",
			"We skip the SIMD kernel because it breaks bit parity.",
			"We use the SIMD kernel because it breaks bit parity records."},
		{"a measured bound in the paragraph",
			"We skip the SIMD kernel because it breaks bit parity.",
			"We skip the SIMD kernel because it breaks bit parity. Measured error 3e-16."},
		{"no longer",
			"No bit-identical way exists.",
			"No longer bit-identical to main."},
	} {
		t.Run(c.cue, func(t *testing.T) {
			if len(Scan(c.fires)) == 0 {
				t.Errorf("the firing sentence passed: %q", c.fires)
			}
			if got := Scan(c.passes); len(got) != 0 {
				t.Errorf("the passing sentence fired: %q %v", c.passes, got)
			}
		})
	}
}

func TestTitleInBackticks(t *testing.T) {
	if err := GateTitle("perf: `FMA` skipped because it would break bit-parity", "--parity-waiver"); err == nil {
		t.Error("a title whose subject is in backticks passed")
	}
}

func TestNoLongerIsNotNoWay(t *testing.T) {
	if got := Scan("No bit-identical way exists."); len(got) != 1 || got[0].Rule != "no-bit-identical-way" {
		t.Errorf("%v", got)
	}
	if got := Scan("The outputs are no longer bit-identical to main."); len(got) != 0 {
		t.Errorf("%v", got)
	}
}

func TestWaiverIsStamped(t *testing.T) {
	text := "We skip the AVX2 kernel because it would break bit-parity.\n"
	out, err := Gate(text, "  the\nreason  ", "--parity-waiver")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, strings.TrimRight(text, "\n")) || !strings.Contains(out, StampPrefix+"the reason\n") {
		t.Errorf("stamp missing or not one line: %q", out)
	}
	// The stamp is not read back: a reason may name what it excuses.
	again, err := Gate(out, "", "--parity-waiver")
	if err == nil {
		t.Errorf("the stamped text must still be refused without a waiver (the sentence is still there): %q", again)
	}
	if got, want := len(Scan(out)), len(Scan(text)); got != want {
		t.Errorf("stamp changed the findings: %d vs %d", got, want)
	}
	stamped := "Fine.\n\n" + Stamp("we skip the kernel because it would break bit-parity") + "\n\n"
	if got := Scan(stamped); len(got) != 0 {
		t.Errorf("the stamp line was read: %v", got)
	}
	// A stamp-looking line that is NOT last is the author's text, read like any.
	forged := Stamp("we skip the SIMD kernel because it would break bit-parity") + "\n\nAnd more.\n"
	if got := Scan(forged); len(got) == 0 {
		t.Error("a stamp in the middle of a text skipped the gate")
	}
}

func TestRefusalNamesTheRuleAndTheModel(t *testing.T) {
	_, err := Gate(refusing[2].text, "", "--parity-waiver")
	if err == nil {
		t.Fatal("not refused")
	}
	for _, want := range []string{Issue, Parked, "porting step", "--parity-waiver", "line 1", "declined-because-parity"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q:\n%v", want, err)
		}
	}
}

func TestRefusalTruncates(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 7; i++ {
		b.WriteString("- We skip the SIMD kernel because it would break bit-parity.\n")
	}
	_, err := Gate(b.String(), "", "--parity-waiver")
	if err == nil || !strings.Contains(err.Error(), "and 2 more") {
		t.Errorf("want a truncated list, got %v", err)
	}
	long := "We skip the SIMD kernel because it would break bit-parity " + strings.Repeat("and more ", 60) + "."
	f := Scan(long)
	if len(f) != 1 || !strings.HasSuffix(f[0].String(), "…") {
		t.Errorf("a long sentence is shortened: %v", f)
	}
}

func TestTitleNeverWaived(t *testing.T) {
	if err := GateTitle("perf: SIMD kernel skipped because it breaks bit-parity", "--parity-waiver"); err == nil || !strings.Contains(err.Error(), "no waiver covers a title") {
		t.Errorf("got %v", err)
	}
	if err := GateTitle("perf: SIMD kernel, bit-identical", "--parity-waiver"); err != nil {
		t.Errorf("a mention in a title must pass: %v", err)
	}
}

func TestOverSizeIsRefusedNotHalfRead(t *testing.T) {
	big := strings.Repeat("x", MaxText+1)
	if _, err := Gate(big, "", "--parity-waiver"); err == nil || !strings.Contains(err.Error(), "does not read") {
		t.Errorf("got %v", err)
	}
	// Scan alone reads the first MaxText bytes only.
	if got := Scan(strings.Repeat("a ", MaxText/2) + " We skip the SIMD kernel because it would break bit-parity."); len(got) != 0 {
		t.Errorf("read past the bound: %v", got)
	}
}

func TestStripBlanksWhatIsNotTheAuthors(t *testing.T) {
	text := "Ok.\n```go\nwe skip SIMD because it would break bit-parity\n```\n" +
		"> quoted: we skip SIMD because it would break bit-parity\n" +
		"inline `we skip SIMD because it would break bit-parity` code, <!-- we skip SIMD because it would break bit-parity --> and\n" +
		"<!-- multi\nwe skip SIMD because it would break bit-parity\n--> after.\n" +
		"~~~\nwe skip SIMD because it would break bit-parity\n~~~\n" +
		"see https://example.com/we-skip-simd-because-bit-parity for more\n" +
		"<!-- open and never closed we skip SIMD because it would break bit-parity\n"
	if got := Scan(text); len(got) != 0 {
		t.Errorf("read what is not the author's: %v", got)
	}
	if n := strings.Count(Strip(text), "\n"); n != strings.Count(text, "\n") {
		t.Errorf("Strip changed the line count: %d vs %d", n, strings.Count(text, "\n"))
	}
}

func TestUnitsAndLines(t *testing.T) {
	text := "Intro sentence. Second one, e.g. an abbreviation, stays whole. Third (it ends here.) Fourth!\n\n" +
		"# Heading\n- item one\n  continues here\n- item two\n| a | b |\n\nLast paragraph, v1.2 stays whole"
	us := Units(text)
	var got []string
	for _, u := range us {
		got = append(got, u.Text)
	}
	want := []string{
		"Intro sentence.", "Second one, e.g. an abbreviation, stays whole.", "Third (it ends here.)", "Fourth!",
		"# Heading", "- item one continues here", "- item two", "| a | b |", "Last paragraph, v1.2 stays whole",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("units\n got %q\nwant %q", got, want)
	}
	if us[0].Line != 1 || us[4].Line != 3 || us[5].Line != 4 || us[6].Line != 6 || us[8].Line != 9 {
		t.Errorf("lines: %+v", us)
	}
	// A sentence is placed on the line it starts on, not its paragraph's first.
	wrapped := Units("Alpha one.\nBeta two\nstill beta. Gamma three.\nDelta.")
	var lines []int
	for _, u := range wrapped {
		lines = append(lines, u.Line)
	}
	if len(lines) != 4 || lines[0] != 1 || lines[1] != 2 || lines[2] != 3 || lines[3] != 4 {
		t.Errorf("wrapped lines %v (%v)", lines, wrapped)
	}
	if got := Units("   \n\n"); len(got) != 0 {
		t.Errorf("blank text has units: %v", got)
	}
}

func TestSentenceEdges(t *testing.T) {
	for in, n := range map[string]int{
		"One. Two.":          2,
		"One.Two":            1, // no space: not a boundary
		"One. two":           1, // lower-case continuation
		"One.  ":             1,
		"One. ":              1,
		"Is it? Yes! 3 more": 3,
		"Vs. Not a boundary": 1, // abbreviation
		"A \"quoted.\" Next": 2,
		"No. 5 is here":      1,
	} {
		if got := sentences(in); len(got) != n {
			t.Errorf("%q: %d sentences %v, want %d", in, len(got), got, n)
		}
	}
}

func TestWindowStaysInTheParagraph(t *testing.T) {
	us := Units("First. Second. Third.\n\nOther.")
	if w := Window(us, 1); !strings.Contains(w, "First") || !strings.Contains(w, "Third") {
		t.Errorf("window %q", w)
	}
	if w := Window(us, 2); strings.Contains(w, "Other") {
		t.Errorf("window crossed a paragraph: %q", w)
	}
	if w := Window(us, 0); strings.Contains(w, "Other") || !strings.HasPrefix(w, "First") {
		t.Errorf("window %q", w)
	}
}

func TestHasBitIdentityExcludesParaphrase(t *testing.T) {
	if !HasBitIdentity("it is Bit-Identical") || HasBitIdentity("it rounds differently") {
		t.Error("HasBitIdentity")
	}
	if !parity.MatchString("it rounds differently") {
		t.Error("the paraphrase is a parity cue")
	}
}

func FuzzScan(f *testing.F) {
	for _, c := range refusing {
		f.Add(c.text)
	}
	for _, s := range passing {
		f.Add(s)
	}
	f.Add("```\nunclosed fence we skip SIMD because bit-parity")
	f.Add("<!-- unclosed")
	f.Add("> > nested\n\n\n- \n|\n#\n")
	f.Add("1.\n2. Three.\n. . . ? ! e.g. i.e.")
	f.Fuzz(func(t *testing.T, s string) {
		found := Scan(s)
		lines := strings.Count(s, "\n") + 1
		for _, fd := range found {
			if fd.Line < 1 || fd.Line > lines {
				t.Fatalf("line %d outside 1..%d", fd.Line, lines)
			}
			if fd.Rule == "" || fd.Sentence == "" {
				t.Fatalf("empty finding %+v", fd)
			}
			_ = fd.String()
		}
		if n := strings.Count(Strip(s), "\n"); n != strings.Count(s, "\n") {
			t.Fatalf("Strip changed the line count")
		}
		out, err := Gate(s, "because", "--parity-waiver")
		if len(s) > MaxText {
			return
		}
		if err != nil {
			t.Fatalf("a waiver must pass any text: %v", err)
		}
		if strings.TrimSpace(s) != "" && len(found) > 0 && !strings.Contains(out, StampPrefix) {
			t.Fatalf("a waived finding must be stamped")
		}
		// The stamp is never read back: stamping does not change the findings.
		if got := len(Scan(out)); got != len(found) {
			t.Fatalf("stamping changed the findings: %d vs %d", got, len(found))
		}
	})
}
