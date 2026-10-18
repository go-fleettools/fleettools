package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Compat rewrites the single-dash long flags the standard `flag` package
// accepted into the double-dash spelling pflag requires, and says it did.
//
// WHY THIS EXISTS. The migration from `flag` to cobra changes one spelling, and
// it is the spelling this fleet's call sites use: `flag` accepted -max-db-age,
// --max-db-age and -j alike, while pflag reads one dash as ONE letter. Measured
// on 2026-10-18 with the tool that counts them, over every workflow in the
// local fleet: 16 call sites across 13 repositories would break, 13 of them
// calling `vulngate -go`, and that is only the lanes — scripts, READMEs and Go
// source hold 71 more inside this repository alone.
//
// Without this shim the migration needs a flag day across thirteen
// repositories, which is the kind of change that lands half-done. With it, an
// old call site keeps working and is TOLD, once, on stderr, where a person
// running a lane will read it.
//
// WHAT IT REFUSES TO DO. `-ab` is genuinely ambiguous when `ab` is a long flag
// and `a` and `b` are both registered shorthands: pflag reads a cluster, `flag`
// read the long name, and guessing would silently change what a lane means.
// Those are refused, naming both readings. Nothing else is touched: an unknown
// single-dash token is left exactly as it came so that pflag still produces its
// own error, and everything after a bare `--` is an argument, not a flag.
func Compat(c *cobra.Command, args []string, warn io.Writer) ([]string, error) {
	flags := c.Flags()
	out := make([]string, 0, len(args))
	var rewritten []string

	for i, arg := range args {
		name, ok := singleDashLong(arg)
		if !ok {
			out = append(out, arg)
			if arg == "--" {
				// Everything after it is an argument. Copy the rest verbatim.
				out = append(out, args[i+1:]...)
				break
			}
			continue
		}
		if flags.Lookup(name) == nil {
			// Not one of ours. Leave it for pflag to reject in its own words,
			// rather than inventing a second error message for the same fault.
			out = append(out, arg)
			continue
		}
		if cluster(flags, name) {
			return nil, Unanswerable("%s is ambiguous: --%s is a flag of its own, and "+
				"every letter of %q is also a registered shorthand, so pflag would read "+
				"it as %s. The standard flag package read it as the long flag. Say which "+
				"you mean: --%s for the flag, or %s for the shorthands",
				arg, name, name, shorthandList(name), name, shorthandList(name))
		}
		out = append(out, "-"+arg) // -name becomes --name
		// The NAME, not the token: the token already carries its single dash,
		// and prepending another produced "---max-busy" in the first version of
		// this message. A test that only looked for the phrase "two dashes"
		// passed over it.
		rewritten = append(rewritten, name)
	}

	if len(rewritten) > 0 && warn != nil {
		fmt.Fprintf(warn, "⚠ %s: %s now takes two dashes; read as %s. "+
			"The standard flag package accepted one dash and pflag does not. "+
			"Update the call site: this shim will not outlive the next major.\n",
			c.Name(), singleDashed(rewritten), doubleDashed(rewritten))
	}
	return out, nil
}

// singleDashLong reports the flag name in a token like "-max-db-age" or
// "-go=go1.27.2": one dash, a name of two letters or more.
func singleDashLong(arg string) (string, bool) {
	if !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") || arg == "-" {
		return "", false
	}
	name, _, _ := strings.Cut(strings.TrimPrefix(arg, "-"), "=")
	if len(name) < 2 {
		return "", false
	}
	// A negative number is an argument.
	if name[0] >= '0' && name[0] <= '9' {
		return "", false
	}
	return name, true
}

// cluster reports whether every letter of name is a registered one-letter
// shorthand, which is the one case where pflag would also accept the token and
// mean something else entirely.
func cluster(flags *pflag.FlagSet, name string) bool {
	for _, r := range name {
		if r > 127 || flags.ShorthandLookup(string(rune(r))) == nil {
			return false
		}
	}
	return true
}

func shorthandList(name string) string {
	parts := make([]string, 0, len(name))
	for _, r := range name {
		parts = append(parts, "-"+string(r))
	}
	return strings.Join(parts, " ")
}

func singleDashed(names []string) string { return dashed(names, "-") }
func doubleDashed(names []string) string { return dashed(names, "--") }

func dashed(names []string, prefix string) string {
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, prefix+n)
	}
	return strings.Join(parts, ", ")
}
