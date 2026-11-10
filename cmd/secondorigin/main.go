// Command secondorigin keeps a "second origin" honest.
//
// A second origin is an `<org>-usercontent` GitHub organisation whose Pages site
// (`<org>-usercontent.github.io`) gives a playground a SEPARATE ORIGIN:
// github.io is on the Public Suffix List, so the two are different sites with
// isolated storage, and what runs on the second one cannot read the first's
// IndexedDB, cookies or tokens. The first page embeds the second one's shell
// and talks to it by postMessage, each side checking the other's origin.
//
// The parent repository DECLARES its second origin in `.second-origin.json`:
//
//	{
//	  "org":     "go-wasmcontainers-usercontent",
//	  "repo":    "go-wasmcontainers-usercontent.github.io",
//	  "parents": ["https://go-wasmcontainers.github.io"],
//	  "shells":  ["/shell.html"],
//	  "csp":     "default-src 'none'; script-src 'self'; ..."
//	}
//
// `secondorigin verify` reads the PUBLISHED thing and compares it with the
// declaration; `secondorigin init` prepares the Pages repository of an
// organisation that already exists. It never creates an organisation: GitHub
// asks a human for that.
//
// ⛔ THE VERDICT HAS THREE VALUES, because a gate that cannot tell "I could not
// read it" from "it is fine" is the one that reports a disappeared origin as a
// pass: exit 0 verified, 1 the origin DRIFTED (or is gone), 2 NOT VERIFIED (an
// unreadable answer, a rate limit, a 5xx, a declaration that is not one).
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Keeps a "second origin" -- an <org>-usercontent GitHub organisation whose Pages
site gives a playground a separate origin -- verified against what its parent
repository declares in .second-origin.json.

    secondorigin verify [--file .second-origin.json] [--retry N --wait 20s]
    secondorigin init --org O-usercontent --parent https://O.github.io --dir DIR [--create]

verify checks: the organisation exists; the Pages repository exists, is public,
is not archived and has Pages enabled; each shell URL answers 200 on the
declared origin without leaving it; the shell's CSP equals the declared one
(directive and source order are not significant); every origin the shell's
scripts name is a declared parent -- exactly those, none other, none missing;
no postMessage is sent to "*"; and the scripts read event.origin.

Exit status: 0 verified, 1 drifted or gone, 2 not verified (unreadable answer,
rate limit, bad declaration). Not verified is never a pass.

init never creates an organisation. When it is missing, init prints what a
human must create and stops.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it through run, below.
func RootCmd() *cobra.Command {
	c := cli.New("secondorigin verify|init [flags]",
		"Verify a second origin against its declaration, or prepare its Pages repository", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cli.Err(cmd), longHelp)
		return cli.Unanswerable("secondorigin: say verify or init")
	}
	c.AddCommand(verifyCmd(), initCmd())
	return c
}

// run keeps the signature the tests drive.
func run(args []string, out, errOut io.Writer) int {
	return cli.Drive(RootCmd(), args, out, errOut)
}
