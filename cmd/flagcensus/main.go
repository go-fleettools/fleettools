// Command flagcensus says, for every command in a repository, HOW it reads its
// arguments and what a migration from the standard `flag` package to cobra
// would have to carry across.
//
// It exists because the migration is a campaign across fifty-three commands,
// and the two things it needs answering are both counts somebody would
// otherwise guess:
//
//   - which idiom each command uses, so that the ones already finished are not
//     opened again and the ones that answer -h with a sweep are found;
//   - which flags need two dashes after the move, because `flag` accepts
//     -releases, --releases and -j alike while pflag reads one dash as ONE
//     letter.
//
// ⛔ IT GENERATES NOTHING. A blanket rewrite of fifty-three files is how a
// previous campaign here replaced a helper with a call to itself -- infinite
// recursion that compiled. This prints an inventory for a person to work from,
// and the rewiring of each main() stays a deliberate edit.
//
// ⛔ AND IT LIVES HERE RATHER THAN IN A SCRATCHPAD. Its two ancestors were
// written in /private/tmp on 2026-10-18, measured the fleet, and were gone with
// the next reboot -- so the same counts had to be derived again from nothing.
// A measurement worth quoting is worth a repository.
package main

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"os"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Reports, per command, which argument-parsing idiom it uses and which of its
flags would need two dashes after a move to cobra.

  cobra        built with cobra: parses and answers -h by construction
  flag+help    a FlagSet through fleet.ParseFlags or an ErrHelp branch
  flag-global  the package-level flag.Parse(): ExitOnError answers -h with 0
               itself, so not broken — but os.Args is parsed in place, so the
               work is not drivable from a test
  flag-raw     a FlagSet with ContinueOnError and neither: -h really does
               arrive as an error
  hand -h      compares os.Args against "-h" itself
  UNREAD       no sign the arguments are read at all -- on 2026-10-11 a command
               in this state started a sweep of every organisation on -h

The subcommand ` + "`mains`" + ` prints the entry-point functions of the
commands named, which is what a migration rewrites.

Exit status: 0 the census was taken, 1 some command is UNREAD, 2 it could not
be taken.`

func main() {
	// ⛔ FIRST, as every command here does: a census taken by a binary older
	// than the tree it is reading answers for an older tree, and nothing in the
	// table would say so. A repository-wide test enumerates cmd/ to make sure
	// none forgets, and it caught this one.
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it with SetArgs, SetOut and SetErr.
func RootCmd() *cobra.Command {
	var (
		cmdDir  string
		only    string
		verbose bool
	)
	c := cli.New("flagcensus [flags]",
		"How each command reads its arguments, and what a cobra move would carry", longHelp)
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		return report(cli.Out(cmd), cmdDir, only, verbose)
	}
	f := c.Flags()
	f.StringVarP(&cmdDir, "cmd-dir", "d", "cmd", "the repository's cmd/ directory")
	f.StringVar(&only, "only", "", "a single command name, for working through them one at a time")
	f.BoolVarP(&verbose, "verbose", "v", false, "list every flag, not only the counts")
	c.AddCommand(mainsCmd())
	return c
}

func mainsCmd() *cobra.Command {
	var (
		cmdDir string
		funcs  string
	)
	c := cli.New("mains <command>...", "Print the entry-point functions a migration rewrites", "")
	c.Args = cobra.MinimumNArgs(1)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		want := map[string]bool{}
		for _, n := range strings.Split(funcs, ",") {
			if n = strings.TrimSpace(n); n != "" {
				want[n] = true
			}
		}
		if len(want) == 0 {
			return cli.Unanswerable("--funcs names no function")
		}
		for _, name := range args {
			if err := Funcs(cli.Out(cmd), filepath.Join(cmdDir, name), name, want); err != nil {
				return cli.Wrap(err)
			}
		}
		return nil
	}
	f := c.Flags()
	f.StringVarP(&cmdDir, "cmd-dir", "d", "cmd", "the repository's cmd/ directory")
	f.StringVar(&funcs, "funcs", "main,run,parseArgs,usage", "comma-separated function names to print")
	return c
}

func report(out io.Writer, cmdDir, only string, verbose bool) error {
	cmds, census, err := Read(cmdDir, only)
	if err != nil {
		return cli.Wrap(err)
	}
	// A census that read nothing must not be reported as a repository with no
	// commands: zero UNREAD out of zero is the best value in its column.
	if census.Commands == 0 {
		return cli.Unanswerable("no command with a main() under %s (%d directories read) "+
			"— nothing was measured, which is not the same as nothing being there", cmdDir, census.Dirs)
	}

	w := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "COMMAND\tIDIOM\tFLAGS\tTWO DASHES\tos.Exit\tOWN USAGE")
	byIdiom := map[Idiom]int{}
	flags, breaking := 0, 0
	for _, c := range cmds {
		byIdiom[c.Idiom]++
		flags += len(c.Flags)
		breaking += c.Breaking()
		usage := ""
		if c.HasUsage {
			usage = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t%s\n",
			c.Name, c.Idiom, len(c.Flags), c.Breaking(), c.Exits, usage)
	}
	w.Flush()

	if verbose {
		fmt.Fprintln(out)
		w2 := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
		fmt.Fprintln(w2, "COMMAND\tFLAG\tKIND\tDEFAULT\tWHERE")
		for _, c := range cmds {
			for _, f := range c.Flags {
				mark := f.Name
				if f.NeedsTwoDashes() {
					mark = "⚠ " + f.Name
				}
				fmt.Fprintf(w2, "%s\t%s\t%s\t%s\t%s\n", c.Name, mark, f.Kind, f.Default, f.Pos)
			}
		}
		w2.Flush()
	}

	fmt.Fprintf(out, "\n%d commands in %s (%d directories read)", census.Commands, cmdDir, census.Dirs)
	for _, i := range []Idiom{Cobra, FlagHelped, FlagGlobal, FlagRaw, HandRolled, Unread} {
		if byIdiom[i] > 0 {
			fmt.Fprintf(out, "  %s=%d", i, byIdiom[i])
		}
	}
	fmt.Fprintf(out, "\n%d flags, %d of them needing two dashes after the move\n", flags, breaking)
	for _, u := range census.Unparsable {
		fmt.Fprintln(out, "⚠ unparsable:", u)
	}

	if n := byIdiom[Unread]; n > 0 {
		fmt.Fprintf(out, "⛔ %d command(s) show no sign of reading their arguments, "+
			"so -h runs them.\n", n)
		return cli.FromStatus(1)
	}
	return nil
}

// sortedNames is used by the tests to assert the census covers a whole tree.
func sortedNames(cmds []Command) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, c.Name)
	}
	sort.Strings(out)
	return out
}
