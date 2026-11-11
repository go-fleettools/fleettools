// Command paritycheck is the gate behind a decision of 2026-11-11 that used to
// be a sentence: bit identity with a reference implementation is a PORTING STEP,
// never the contract. The contract for numeric code is "correct and as fast as
// possible" — a stated error bound, asserted by tests against an
// extended-precision oracle, with bit-exact arithmetic kept only as an option —
// and a performance gap is never parked because "it would break bit-parity".
// The model is go-fft/fft#121; go-fft/fft#116 is the parking pattern.
//
// Exit status: 0 PASS, 1 FAIL or refused, 2 the question could not be answered
// (UNREAD, an unreadable file, a bad invocation). UNREAD is never PASS.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/go-fleettools/fleettools/internal/parityrepo"
	"github.com/go-fleettools/fleettools/internal/paritytext"
	"github.com/spf13/cobra"
)

const longHelp = `Two modes.

  paritycheck repo <clone>    the CI / sweep mode: does the repository make bit
                              identity with a reference its contract?
  paritycheck text <file>     the gate mode: does this issue, comment or pull
                              request text defer a performance change BECAUSE it
                              would break bit identity?

THE MARKER (repo mode). Nothing is inferred. A repository that states the
contract has, in its README, docs/ or package godoc, a section headed
"Accuracy contract" (any heading level; in godoc "# Accuracy contract") with
three keyed lines:

    ## Accuracy contract

    Bound: relative RMS error <= c*eps*log2(n), c measured per function
    Oracle: double-double (math/big for small n)
    Exact: Options.Exact keeps the bit-exact arithmetic

and a test named TestAccuracyBound... (Python: test_accuracy_bound...) in a
test file, so the bound is asserted and not only written.

A repository whose results are integers or bytes has no rounding and no bound:
bit identity is the only correct answer there. It says so with one line in the
same section, "Domain: exact - <why>", and passes. Statements are counted only
in a repository whose prose and godoc use floating-point words (float32,
float64, floating-point, ULP, FMA, NumPy, SciPy, FFTW, BLAS, IEEE-754) at least
5 times, so most such repositories never need the line.

  PASS    no bit-identity contract is stated, or the marker is complete;
  FAIL    bit identity is stated (a sentence of README*, docs/ or a non-test Go
          comment saying bit-identical, bit-exact, bit-for-bit, bit parity...,
          not negated, not an option) and the marker is not complete;
  UNREAD  a surface could not be read. UNREAD is not PASS.

THE GATE (text mode). A text is REFUSED when a sentence defers or declines a
performance change because it breaks bit identity ("we skip FMA because it
would break bit-parity", "that is a contract change, so it needs a decision"),
unless it cites a MEASURED error bound (an error beside a number: 2.2e-16, 3 ulp)
or carries --parity-waiver "<why>", which is APPENDED to the file (the published
text) as a stamped line. "Output unchanged, bit-identical" passes: a mention is
not a deferral. Code, quotes and HTML comments are not read.

The rule: bit identity with a reference is a porting step, never the contract
(decision 2026-11-11). Model: go-fft/fft#121. Pattern it replaces: go-fft/fft#116.

Exit status: 0 PASS, 1 FAIL or refused, 2 could not be answered.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command; tests drive it with SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	c := cli.New("paritycheck repo <clone> | text <file>", "Fail when bit identity is the contract, or is the reason a speedup was parked", longHelp)
	c.AddCommand(repoCmd(), textCmd())
	return c
}

func repoCmd() *cobra.Command {
	var asJSON bool
	c := cli.New("repo <clone>...", "Does the repository state bit identity as its contract, with no error bound?",
		"Reads README*, docs/ and the Go comments of each <clone>; verdict PASS, FAIL or UNREAD (see paritycheck --help for the marker).\n"+
			"Several clones make a sweep: one verdict each and a count; the status is the worst (UNREAD 2, FAIL 1, PASS 0).\n"+
			"A clone that does not exist is UNREAD, never skipped.")
	c.Args = cobra.MinimumNArgs(1)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		out := cli.Out(cmd)
		reps := make([]*parityrepo.Report, len(args))
		code := 0
		counts := map[parityrepo.Verdict]int{}
		for i, root := range args {
			rep, err := parityrepo.Check(root)
			if err != nil {
				rep = &parityrepo.Report{Verdict: parityrepo.Unread, Reason: err.Error()}
			}
			reps[i] = rep
			counts[rep.Verdict]++
			code = max(code, map[parityrepo.Verdict]int{parityrepo.Pass: 0, parityrepo.Fail: 1, parityrepo.Unread: 2}[rep.Verdict])
			if !asJSON {
				printRepo(out, root, rep)
			}
		}
		if asJSON {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			// A write error to the command's own stdout has nowhere better to go
			// than the status below, which does not depend on it.
			_ = enc.Encode(reps)
		} else if len(args) > 1 {
			fmt.Fprintf(out, "paritycheck: %d PASS, %d FAIL, %d UNREAD of %d\n", counts[parityrepo.Pass], counts[parityrepo.Fail], counts[parityrepo.Unread], len(args))
		}
		if code == 0 {
			return nil
		}
		return &cli.Exit{Code: code}
	}
	c.Flags().BoolVar(&asJSON, "json", false, "print the report as JSON")
	return c
}

func printRepo(w io.Writer, root string, r *parityrepo.Report) {
	fmt.Fprintf(w, "%s %s: %s\n", r.Verdict, root, r.Reason)
	for _, s := range r.Statements {
		fmt.Fprintf(w, "  statement %s:%d: %s\n", s.File, s.Line, s.Sentence)
	}
	for _, u := range r.Unread {
		fmt.Fprintf(w, "  UNREAD %s\n", u)
	}
	m := r.Marker
	fmt.Fprintf(w, "  marker: bound=%t oracle=%t exact=%t test=%q (%s); %d file(s) read, %d floating-point word(s)\n", m.Bound, m.Oracle, m.Exact, m.Test, m.File, r.Files, r.FloatCues)
}

func textCmd() *cobra.Command {
	var waiver string
	c := cli.New("text <file>", "Refuse a text that defers a performance change because it breaks bit identity",
		"Reads <file>; exit 0 PASS, 1 refused (the refusal names the rule and "+paritytext.Issue+"), 2 unreadable.\n"+
			"--parity-waiver \"<why>\" appends a stamped line to the file and exits 0.")
	c.Args = cobra.ExactArgs(1)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		b, err := readText(args[0])
		if err != nil {
			return cli.Unanswerable("%v", err)
		}
		text := string(b)
		got, err := paritytext.Gate(text, waiver, "--parity-waiver")
		if err != nil {
			// Status 1 prints nothing of its own (the command's output is the
			// finding), so the refusal is written here, to the author.
			fmt.Fprintln(cli.Err(cmd), err)
			return &cli.Exit{Code: 1}
		}
		if got != text {
			if err := os.WriteFile(args[0], []byte(got), 0o644); err != nil {
				return cli.Unanswerable("stamping %s: %v", args[0], err)
			}
			fmt.Fprintf(cli.Out(cmd), "WAIVED %s: the reason is stamped into the file\n", args[0])
			return nil
		}
		fmt.Fprintf(cli.Out(cmd), "PASS %s\n", args[0])
		return nil
	}
	c.Flags().StringVar(&waiver, "parity-waiver", "", "why this text may defer a performance change because of bit identity; STAMPED into the file")
	return c
}

func readText(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, paritytext.MaxText+1))
	if err != nil {
		return nil, err
	}
	if len(b) > paritytext.MaxText {
		return nil, fmt.Errorf("%s is over %d bytes: an unread text is not a pass", path, paritytext.MaxText)
	}
	if strings.TrimSpace(string(b)) == "" {
		return nil, fmt.Errorf("%s is empty", path)
	}
	return b, nil
}
