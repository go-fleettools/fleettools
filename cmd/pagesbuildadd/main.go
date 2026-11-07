// Command pagesbuildadd inserts the pagesbuild step into one docs checkout's
// .github/workflows/docs.yml, exactly as go-redfish/docs#11 did, and refuses a
// workflow whose deploy job does not have the shape that change was proven on.
// It edits the file only: the branch, commit and pull request are the caller's
// (pagesbuildcampaign drives them).
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/go-fleettools/fleettools/internal/pagesroll"
	"github.com/spf13/cobra"
)

var osExit = os.Exit

// RootCmd builds the command; tests drive it with SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	var version, actionlint string
	cmd := &cobra.Command{
		Use:   "pagesbuildadd [--version vX.Y.Z] <docs checkout>",
		Short: "add the pagesbuild step to a docs repository's deploy workflow",
		Long: `pagesbuildadd makes go-redfish/docs#11's change to DIR/.github/workflows/docs.yml:
'pages: write' on the deploy job, 'id: push' and an output of the pushed SHA on
the "Push gh-pages" step, and a step after it that runs pagesbuild on that SHA.

A workflow that already runs pagesbuild is reported, not edited (exit 0); one
whose deploy job is not the known shape is refused (exit 1) and left untouched.
The result is checked with actionlint, and the file restored if the edit
introduces a finding.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			msg, err := pagesroll.Edit(args[0], version, actionlint)
			if errors.Is(err, pagesroll.ErrAlready) {
				fmt.Fprintln(cmd.OutOrStdout(), "already: "+pagesroll.Workflow+" has pagesbuild, not edited")
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), msg)
			return nil
		},
	}
	cmd.Flags().StringVar(&version, "version", pagesroll.DefaultVersion, "the fleettools release the step pins pagesbuild to")
	cmd.Flags().StringVar(&actionlint, "actionlint", "actionlint", "the actionlint binary to check the result with (empty: skip)")
	return cmd
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	if err := RootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		osExit(1)
	}
}
