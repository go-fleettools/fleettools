// Command acdefault says what an autotools switch REALLY defaults to, by
// reading the generated `configure` rather than the help string beside it.
//
//	acdefault DIR [--flag with-python ...]
//
// It exists because a help string can describe a default the code no longer
// applies. On 2026-11-17 a recipe for gnome.org/libxml2 2.15.4 was written,
// reviewed and MERGED (go-zpkg/recipes#149) on the claim "the default
// configuration of this C library refuses to configure without doxygen", taken
// from
//
//	configure.ac:97-98   --with-python           Python bindings (on)
//
// That "(on)" is stale: the bindings became off-by-default in 2.15 and the help
// line kept its old wording. The generated script settles it — `with_python` is
// assigned only when the flag is PASSED, and its two consumers test it against
// "yes" — so a plain ./configure leaves it empty and the doxygen AC_MSG_ERROR is
// unreachable. go-zpkg/recipes#150 corrected it.
//
// ⛔ `./configure --help` IS NOT A CONTROL. It reprints the same stale string;
// printing it is all it does. The only oracle is what the script DOES with the
// variable.
//
// ⛔ AND "EMPTY" DOES NOT MEAN "OFF". It means whatever the consumers make it
// mean. In the very same file, `with_python` is compared against "yes" (so empty
// is off) while `with_output` is compared against "no" (so empty is ON). A tool
// that assumed either way would be right half the time and silent about it, so
// the verdict here is derived from the COMPARISONS and nothing else.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Reads an extracted autotools source and reports, per switch, the default the
generated ` + "`configure`" + ` actually applies — then compares it with the default the
help string claims.

DIR is an unpacked source tree (it must contain ` + "`configure`" + `; ` + "`configure.ac`" + ` is read
too when present, only for the help strings). With no --flag, every --with-* and
--enable-* the help text documents WITH a stated default is examined.

What is read, and why that and not the help:

  assignment   a line VAR=... at conditional depth 0, unguarded. If one exists,
               that value IS the default and is reported as such.
  comparison   every test "$VAR" = "LIT" / test "x$VAR" = "xLIT" / [ "$VAR" =
               "LIT" ]. With no unconditional assignment the variable is EMPTY
               after a plain ./configure, and what empty MEANS is decided here:

                 compared only against "yes"  → empty behaves as OFF
                 compared only against "no"   → empty behaves as ON
                 compared against both        → UNDETERMINED, and said so

  documented   the (on) / (off) / [default=X] token the help line carries.

⛔ UNDETERMINED is a finding, not a shrug: it is the case this tool must not
guess at, and the one where a person has to read the branches.

Exit status: 0 every switch examined agrees with its documented default,
1 at least one disagrees or is undetermined, 2 nothing could be read — a tree
without a generated configure, an unreadable file, or a --flag the help text
never documents (a census that read nothing must not look like a clean one).`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	var flags []string
	c := cli.New("acdefault DIR",
		"Say what an autotools switch really defaults to, from the generated configure", longHelp)
	c.Flags().StringArrayVar(&flags, "flag", nil,
		"a switch to examine, without the leading dashes (with-python, enable-foo); repeatable, default every documented one")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 {
			return cli.Unanswerable("usage: acdefault DIR [--flag with-python ...]")
		}
		return cli.FromStatus(run(args[0], flags, cli.Out(cmd), cli.Err(cmd)))
	}
	return c
}

// Verdict is the reading of one switch.
type Verdict struct {
	Flag       string   // "with-python"
	Var        string   // "with_python"
	Documented string   // "on", "off", "" when the help states none
	Assigned   string   // the value an unconditional assignment gives, "" when there is none
	AssignLine int      // its line in configure, 0 when there is none
	Compared   []string // the distinct literals the script compares the variable against
	Effective  string   // "on", "off", or "" when undetermined
	Why        string   // one sentence naming what decided it
}

// Agrees reports whether the documented default matches the effective one.
func (v Verdict) Agrees() bool {
	return v.Documented != "" && v.Effective != "" && v.Documented == v.Effective
}

func run(dir string, flags []string, stdout, stderr io.Writer) int {
	confPath := filepath.Join(dir, "configure")
	conf, err := os.ReadFile(confPath)
	if err != nil {
		fmt.Fprintf(stderr, "acdefault: %v\n", err)
		fmt.Fprintf(stderr, "acdefault: DIR must be an unpacked tree holding the GENERATED configure; "+
			"configure.ac alone cannot answer this, which is the whole point\n")
		return 2
	}
	help := Documented(string(conf))
	if ac, err := os.ReadFile(filepath.Join(dir, "configure.ac")); err == nil {
		// configure.ac's help lines are the ones a person reads and quotes, so
		// they win when the two differ — this tool exists to judge THEM.
		for k, v := range Documented(string(ac)) {
			help[k] = v
		}
	}

	wanted := flags
	undocumented := 0
	if len(wanted) == 0 {
		for k, v := range help {
			if v != "" {
				wanted = append(wanted, k)
			} else {
				undocumented++
			}
		}
		sort.Strings(wanted)
	}
	// ⛔ "N examined" ALONE IS THE SHAPE THAT HIDES A SCAN THAT READ NOTHING.
	// Measured on two real trees: pcre2 10.49 printed "3 switch(es) examined"
	// while walking past 40 more whose help lines state no default, and ncurses
	// 6.6 printed a bare refusal while documenting 33 switches, none with a
	// default token. Both numbers are true and both are useless without the
	// other, so the census says N OF M and names what it walked past.
	if len(wanted) == 0 {
		fmt.Fprintf(stderr, "acdefault: %s documents %d --with-*/--enable-* switch(es) and a stated default for NONE of them, so there is nothing to compare the code against\n", confPath, undocumented)
		fmt.Fprintf(stderr, "acdefault: that is a fact about this project, not a failure here — some autoconf macro sets (ncurses' among them) write help lines like `--with-shared  generate shared-libraries` with no (on)/(off)/[default=…] token at all. Name the switches with --flag to read their code anyway.\n")
		return 2
	}

	var out []Verdict
	for _, f := range wanted {
		f = strings.TrimLeft(f, "-")
		doc, known := help[f]
		if len(flags) > 0 && !known {
			fmt.Fprintf(stderr, "acdefault: %s documents no switch --%s; refusing to report a default for a flag that is not there\n", confPath, f)
			return 2
		}
		v := Examine(string(conf), f)
		v.Documented = doc
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Flag < out[j].Flag })

	bad := 0
	for _, v := range out {
		mark := "✓"
		switch {
		case v.Effective == "":
			mark, bad = "⛔", bad+1
		case v.Documented == "":
			mark = "·"
		case !v.Agrees():
			mark, bad = "✗", bad+1
		}
		doc := v.Documented
		if doc == "" {
			doc = "(none stated)"
		}
		eff := v.Effective
		if eff == "" {
			eff = "UNDETERMINED"
		}
		fmt.Fprintf(stdout, "%s --%-28s documented %-13s actual %-12s %s\n", mark, v.Flag, doc, eff, v.Why)
	}
	if len(flags) > 0 {
		fmt.Fprintf(stdout, "%d switch(es) examined, %d disagreeing or undetermined\n", len(out), bad)
	} else {
		fmt.Fprintf(stdout, "%d of %d documented switch(es) examined, %d disagreeing or undetermined\n",
			len(out), len(out)+undocumented, bad)
		if undocumented > 0 {
			fmt.Fprintf(stdout, "⚠ %d switch(es) NOT examined: their help line states no default, so this has nothing to compare the code against (name them with --flag to read the code anyway)\n", undocumented)
		}
	}
	if bad > 0 {
		return 1
	}
	return 0
}

var (
	// "  --with-python           Python bindings (on)" and the autoconf
	// "[default=yes]" / "(default: no)" spellings.
	reHelpLine = regexp.MustCompile(`^\s*--((?:with|enable)-[A-Za-z0-9_.-]+)\b(.*)$`)
	reParen    = regexp.MustCompile(`\((on|off|yes|no)\)\s*$`)
	reDefault  = regexp.MustCompile(`(?:\[default[=:]\s*|\(default[=:]\s*)(yes|no|on|off)`)
)

// Documented maps a switch name to the default its help line states, "" when
// the line states none. src may be a configure script or a configure.ac.
func Documented(src string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(src, "\n") {
		m := reHelpLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name, rest := m[1], m[2]
		// --disable-foo and --without-foo document the SAME variable as their
		// positive spelling; autoconf prints whichever the author wrote.
		if _, seen := out[name]; !seen {
			out[name] = ""
		}
		if d := reDefault.FindStringSubmatch(rest); d != nil {
			out[name] = normalise(d[1])
			continue
		}
		if d := reParen.FindStringSubmatch(rest); d != nil {
			out[name] = normalise(d[1])
		}
	}
	return out
}

func normalise(s string) string {
	switch s {
	case "yes", "on":
		return "on"
	case "no", "off":
		return "off"
	}
	return s
}

// Examine reads src (a generated configure) for one switch.
func Examine(src, flag string) Verdict {
	v := Verdict{Flag: flag, Var: VarName(flag)}

	reAssign := regexp.MustCompile(`^(\s*)` + regexp.QuoteMeta(v.Var) + `=(\S*)`)
	reCmp := regexp.MustCompile(`(?:test|\[)\s+"?(?:x)?\$\{?` + regexp.QuoteMeta(v.Var) +
		`\}?"?\s*=\s*"?x?([A-Za-z0-9_-]*)"?`)

	seen := map[string]bool{}
	depth := 0
	for i, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		for _, lit := range reCmp.FindAllStringSubmatch(line, -1) {
			if lit[1] != "" && !seen[lit[1]] {
				seen[lit[1]] = true
				v.Compared = append(v.Compared, lit[1])
			}
		}
		// An assignment only counts as the default when nothing guards it:
		// conditional depth 0, and not the right-hand side of a `test … &&`.
		if m := reAssign.FindStringSubmatch(line); m != nil && v.Assigned == "" {
			if depth == 0 && !strings.Contains(line, "&&") && !strings.HasPrefix(trimmed, "test ") {
				v.Assigned, v.AssignLine = strings.Trim(m[2], `"'`), i+1
			}
		}
		depth += depthDelta(trimmed)
		if depth < 0 {
			depth = 0
		}
	}
	sort.Strings(v.Compared)

	switch {
	case v.Assigned != "":
		v.Effective = normalise(v.Assigned)
		v.Why = fmt.Sprintf("configure:%d assigns %s=%s unconditionally", v.AssignLine, v.Var, v.Assigned)
	case len(v.Compared) == 0:
		v.Why = fmt.Sprintf("%s is never compared against a literal: nothing here decides what empty means", v.Var)
	case onlyAgainst(v.Compared, "yes"):
		v.Effective = "off"
		v.Why = fmt.Sprintf("no unconditional assignment, and %s is only ever compared against \"yes\": empty is off", v.Var)
	case onlyAgainst(v.Compared, "no"):
		v.Effective = "on"
		v.Why = fmt.Sprintf("no unconditional assignment, and %s is only ever compared against \"no\": empty is on", v.Var)
	default:
		v.Why = fmt.Sprintf("no unconditional assignment and %s is compared against %s: read the branches",
			v.Var, strings.Join(quoteAll(v.Compared), ", "))
	}
	return v
}

// VarName is the shell variable a switch sets: --with-python → with_python,
// and the negative spellings name the same variable as their positive one.
func VarName(flag string) string {
	flag = strings.TrimLeft(flag, "-")
	switch {
	case strings.HasPrefix(flag, "without-"):
		flag = "with-" + strings.TrimPrefix(flag, "without-")
	case strings.HasPrefix(flag, "disable-"):
		flag = "enable-" + strings.TrimPrefix(flag, "disable-")
	}
	return strings.ReplaceAll(flag, "-", "_")
}

func onlyAgainst(lits []string, want string) bool {
	for _, l := range lits {
		if l != want {
			return false
		}
	}
	return len(lits) > 0
}

func quoteAll(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = `"` + v + `"`
	}
	return out
}

// depthDelta is a line-oriented reading of shell nesting. It is deliberately
// crude and only ever decides whether an assignment is UNGUARDED; every verdict
// that matters is derived from the comparisons instead, which need no nesting.
func depthDelta(trimmed string) int {
	d := 0
	switch {
	case trimmed == "if" || strings.HasPrefix(trimmed, "if "):
		d++
	case trimmed == "case" || strings.HasPrefix(trimmed, "case "):
		d++
	case strings.HasPrefix(trimmed, "for ") || strings.HasPrefix(trimmed, "while "):
		d++
	}
	switch {
	case trimmed == "fi" || strings.HasPrefix(trimmed, "fi;"):
		d--
	case trimmed == "esac" || strings.HasPrefix(trimmed, "esac;"):
		d--
	case trimmed == "done" || strings.HasPrefix(trimmed, "done;"):
		d--
	}
	return d
}
