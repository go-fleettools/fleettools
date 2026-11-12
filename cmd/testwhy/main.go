// Command testwhy reads a `go test` log and refuses to let a COUNT of failures
// stand in for knowing WHICH failures they are.
//
// ⛔⛔ "SAME NUMBER OF FAILURES AS BEFORE" IS NOT A MEASUREMENT. Measured on
// 2026-11-12, deciding whether a dependency bump was safe to merge: the suite
// had a known local breakage (a broken node binary killing every browser-driven
// test), I read the log by eye, and reported "18 failures, 0 non-browser, same
// as before". The file held 21 `--- FAIL` lines. The count was wrong, and worse,
// it was the only criterion — so "unchanged" would have covered a NEW failure
// arriving under the old total. What matters is the NAMES and their CAUSE.
//
//	testwhy -log out.txt -cause node=libsimdjson
//	go test ./... 2>&1 | tee out.txt ; testwhy -log out.txt -cause node=libsimdjson
//
// Every failure whose output contains a named cause's substring is attributed
// to it. Every failure left over is printed and the exit status is 1: an
// unexplained failure is the finding.
//
// # Three things it gets right that reading by eye does not
//
// ⛔ A PARENT CARRIES NO MESSAGE OF ITS OWN. `--- FAIL: T` printed above
// `--- FAIL: T/case-1` has an empty body; the cause belongs to the subtest. The
// first version of this program reported such a parent as unexplained, which is
// not merely noisy — it sends you hunting a regression that does not exist. A
// parent is attributed when it HAS subtests and EVERY one of them is
// attributed; a parent with one unattributed subtest stays unattributed,
// because that subtest is the real finding.
//
// ⛔ A PACKAGE THAT DOES NOT BUILD PRINTS NO `--- FAIL` AT ALL. `FAIL pkg
// [build failed]` has no test names under it, so a program that counts FAIL
// lines reports zero failures and exits 0 on a package that compiled nothing.
// That is the same shape as a scan that could not read reporting a clean sweep.
// A build failure is always unexplained, whatever causes are named.
//
// ⛔ AND THE TIME IS A WITNESS. A package that should take seconds and returns
// in 0.0s ran nothing. The elapsed time of every package is printed, and
// -min-seconds refuses a package that finished faster than it plausibly could.
//
// Exit status: 0 every failure is attributed (or there are none), 1 something
// is unexplained, 2 the arguments or the log could not be read.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Reads a ` + "`go test`" + ` log and refuses to let a COUNT of failures stand in for
knowing WHICH failures they are.

A known breakage is named with -cause NAME=SUBSTRING, repeatable. Every failing
test whose own output contains that substring is attributed to NAME. Every
failure left over is printed, and the exit status is 1.

It attributes a PARENT test only when it has subtests and every one of them is
attributed, because a parent's --- FAIL line carries no message of its own.

A package that failed to BUILD prints no --- FAIL at all, so counting them
reports zero; a build failure is always unexplained here.

-min-seconds refuses a package that finished faster than it plausibly could: a
suite that should take seconds and returns in 0.0s ran nothing.

Exit status: 0 every failure is attributed (or there are none), 1 something is
unexplained, 2 the arguments or the log could not be read.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	var (
		logPath string
		causes  []string
		minSecs float64
		all     bool
	)
	c := cli.New("testwhy -log FILE [-cause NAME=SUBSTRING ...]",
		"classify a go test log's failures by cause; an unexplained one is the finding", longHelp)
	c.Flags().StringVar(&logPath, "log", "", "the `go test` output to read; - for standard input")
	c.Flags().StringArrayVar(&causes, "cause", nil, "a known breakage, NAME=SUBSTRING (repeatable)")
	c.Flags().Float64Var(&minSecs, "min-seconds", 0, "refuse a package that finished faster than this")
	c.Flags().BoolVar(&all, "all", false, "print every failure and its cause, not only the unexplained")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return testwhy(cmd, logPath, causes, minSecs, all)
	}
	return c
}

// run keeps the signature this command's tests drive it through.
func run(argv []string, stdout, stderr io.Writer) int {
	return cli.Drive(RootCmd(), argv, stdout, stderr)
}

// cause is one named known breakage.
type cause struct {
	name string
	sub  string
}

// parseCauses reads the NAME=SUBSTRING forms. An empty name or an empty
// substring is refused rather than accepted: a cause matching the empty string
// attributes EVERY failure, which turns this program into one that always
// exits 0 — the exact failure mode it exists to prevent.
func parseCauses(args []string) ([]cause, error) {
	var out []cause
	for _, a := range args {
		i := strings.Index(a, "=")
		if i <= 0 || i == len(a)-1 {
			return nil, fmt.Errorf("-cause %q: want NAME=SUBSTRING, both non-empty", a)
		}
		out = append(out, cause{name: a[:i], sub: a[i+1:]})
	}
	return out, nil
}

// pkgResult is one package line of the log.
type pkgResult struct {
	name    string
	status  string // "ok", "FAIL", "?"
	seconds float64
	timed   bool // an elapsed time was printed (not "[build failed]" / "[no test files]")
	note    string
}

// failure is one `--- FAIL:` line.
type failure struct {
	name  string
	cause string // "" while unattributed
}

// report is what the log says.
type report struct {
	failures []failure
	pkgs     []pkgResult
	builds   []string // packages that failed to build
}

// scan reads a `go test` log.
//
// The cause of a failure is any line between its own `--- FAIL:` and the next
// test boundary. Boundaries are `=== RUN`, `--- PASS:`, `--- SKIP:` and the next
// `--- FAIL:`, which is why a parent's body comes out empty: its subtests'
// `--- FAIL:` lines follow immediately.
func scan(r io.Reader, causes []cause) (*report, error) {
	rep := &report{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)

	idx := map[string]int{} // failure name -> position in rep.failures
	cur := -1               // the failure whose body we are inside, or -1
	for sc.Scan() {
		line := sc.Text()
		t := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(t, "--- FAIL: "):
			n := firstField(strings.TrimPrefix(t, "--- FAIL: "))
			if _, seen := idx[n]; !seen {
				idx[n] = len(rep.failures)
				rep.failures = append(rep.failures, failure{name: n})
			}
			cur = idx[n]
			continue
		case strings.HasPrefix(t, "=== RUN"), strings.HasPrefix(t, "--- PASS: "),
			strings.HasPrefix(t, "--- SKIP: "), strings.HasPrefix(t, "=== CONT"):
			cur = -1
			continue
		}

		if p, ok := parsePkgLine(t); ok {
			rep.pkgs = append(rep.pkgs, p)
			if strings.Contains(p.note, "build failed") {
				rep.builds = append(rep.builds, p.name)
			}
			cur = -1
			continue
		}

		if cur >= 0 && rep.failures[cur].cause == "" {
			for _, c := range causes {
				if strings.Contains(line, c.sub) {
					rep.failures[cur].cause = c.name
					break
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	attributeParents(rep.failures)
	return rep, nil
}

// attributeParents gives a parent its subtests' cause when they ALL have the
// same one. See the ⛔ in the package comment: a parent's --- FAIL line has no
// body, so nothing can ever match against it directly.
func attributeParents(fs []failure) {
	kids := map[string][]int{}
	for i, f := range fs {
		if j := strings.Index(f.name, "/"); j > 0 {
			p := f.name[:j]
			kids[p] = append(kids[p], i)
		}
	}
	at := map[string]int{}
	for i, f := range fs {
		at[f.name] = i
	}
	for p, ks := range kids {
		i, ok := at[p]
		if !ok || fs[i].cause != "" {
			continue
		}
		want := fs[ks[0]].cause
		if want == "" {
			continue
		}
		same := true
		for _, k := range ks {
			if fs[k].cause != want {
				same = false
				break
			}
		}
		if same {
			fs[i].cause = want
		}
	}
}

// parsePkgLine reads "ok \tpkg\t1.23s", "FAIL\tpkg\t1.23s",
// "FAIL\tpkg [build failed]" and "ok \tpkg\t0.01s [no test files]".
func parsePkgLine(t string) (pkgResult, bool) {
	var p pkgResult
	switch {
	case strings.HasPrefix(t, "ok  \t"), strings.HasPrefix(t, "ok\t"):
		p.status = "ok"
	case strings.HasPrefix(t, "FAIL\t"):
		p.status = "FAIL"
	case strings.HasPrefix(t, "?   \t"), strings.HasPrefix(t, "?\t"):
		p.status = "?"
	default:
		return p, false
	}
	f := strings.Split(t, "\t")
	if len(f) < 2 {
		return p, false
	}
	// ⛔ THE NOTE IS NOT ALWAYS ITS OWN FIELD, and assuming it was is how the
	// first version of this let a build failure through. `go test` prints
	//
	//	FAIL	example/broken [build failed]
	//
	// with the note inside the SAME tab-separated field as the package name,
	// while
	//
	//	ok  	example/pkg	0.012s [no test files]
	//
	// puts it after a tab. Reading only the fields past the name therefore found
	// no note, left builds empty, and reported "failures=0 … exit 0" for a
	// package that compiled nothing — the precise reading this command exists to
	// refuse. So the bracket is looked for across the whole remainder.
	rest := strings.Join(f[1:], " ")
	if i := strings.Index(rest, "["); i >= 0 {
		if j := strings.Index(rest[i:], "]"); j >= 0 {
			p.note = rest[i+1 : i+j]
			rest = strings.TrimSpace(rest[:i] + rest[i+j+1:])
		}
	}
	for _, w := range strings.Fields(rest) {
		if strings.HasSuffix(w, "s") {
			if v, err := strconv.ParseFloat(strings.TrimSuffix(w, "s"), 64); err == nil {
				p.seconds, p.timed = v, true
				continue
			}
		}
		if p.name == "" {
			p.name = w
		}
	}
	return p, true
}

func firstField(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return s
}

// testwhy is the verdict.
func testwhy(cmd *cobra.Command, logPath string, causeArgs []string, minSecs float64, all bool) error {
	if logPath == "" {
		return cli.Unanswerable("-log is required: the `go test` output to read, or - for standard input")
	}
	causes, err := parseCauses(causeArgs)
	if err != nil {
		return cli.Wrap(err)
	}

	var r io.Reader
	if logPath == "-" {
		r = cmd.InOrStdin()
	} else {
		f, err := os.Open(logPath)
		if err != nil {
			return cli.Wrap(err)
		}
		defer f.Close()
		r = f
	}

	rep, err := scan(r, causes)
	if err != nil {
		return cli.Wrap(err)
	}

	out := cmd.OutOrStdout()
	byCause := map[string]int{}
	var unexplained []string
	for _, f := range rep.failures {
		if f.cause == "" {
			unexplained = append(unexplained, f.name)
			continue
		}
		byCause[f.cause]++
	}
	sort.Strings(unexplained)

	fmt.Fprintf(out, "failures=%d attributed=%d unexplained=%d\n",
		len(rep.failures), len(rep.failures)-len(unexplained), len(unexplained))
	names := make([]string, 0, len(byCause))
	for n := range byCause {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(out, "  %s: %d\n", n, byCause[n])
	}

	// A package's own line, always: a reader deciding "same as before" needs the
	// package status and its TIME beside the failure count, not instead of it.
	for _, p := range rep.pkgs {
		switch {
		case p.note != "":
			fmt.Fprintf(out, "pkg %s %s [%s]\n", p.status, p.name, p.note)
		case p.timed:
			fmt.Fprintf(out, "pkg %s %s %.3fs\n", p.status, p.name, p.seconds)
		default:
			fmt.Fprintf(out, "pkg %s %s\n", p.status, p.name)
		}
	}

	bad := false
	for _, b := range rep.builds {
		fmt.Fprintf(out, "⛔ %s did not BUILD: it printed no test at all, so no count can see it\n", b)
		bad = true
	}
	if minSecs > 0 {
		for _, p := range rep.pkgs {
			if p.status == "?" || p.note != "" {
				continue
			}
			if p.timed && p.seconds < minSecs {
				fmt.Fprintf(out, "⛔ %s finished in %.3fs, under -min-seconds %.3f: a suite that returns "+
					"that fast ran nothing\n", p.name, p.seconds, minSecs)
				bad = true
			}
		}
	}
	for _, n := range unexplained {
		fmt.Fprintf(out, "⛔ unexplained: %s\n", n)
		bad = true
	}
	if all {
		for _, f := range rep.failures {
			if f.cause != "" {
				fmt.Fprintf(out, "   %s: %s\n", f.cause, f.name)
			}
		}
	}
	if bad {
		// A finding, not a failure to look: the output above names each one.
		return cli.FromStatus(1)
	}
	return nil
}
