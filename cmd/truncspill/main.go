package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Reads ` + "`go build -gcflags=-S`" + ` output for GOARCH=loong64 and reports every
float-to-integer conversion whose result is spilled as eight bytes in a way that
can reach the stack slots beside it — golang.org/issue/81000.

    CGO_ENABLED=0 GOOS=linux GOARCH=loong64 GOTOOLCHAIN=go1.27.1 \
      go build -gcflags=-S ./... 2>asm.s
    truncspill asm.s

Add test files to the picture with:

    go test -run '^$' -gcflags=-S ./... 2>asm.s

A clean run means "no exposure found in THIS build", not "cannot happen":
inlining and register allocation are per-build, and a frame reported hazardous
may still pass, because a clobbered byte can be rewritten before anyone reads it.

Exit status: 0 no frame is hazardous, 1 one is, 2 nothing was read — an empty
dump reads as "0 conversions, 0 hazardous", which is indistinguishable from a
clean sweep, so it is refused instead.`

// RootCmd builds the command. Tests drive it through run, below.
func RootCmd() *cobra.Command {
	var (
		quiet  bool
		prefix string
	)
	c := cli.New("truncspill [flags] [file...]",
		"Float-to-integer spills that can reach the stack slots beside them", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return scanDumps(cmd, quiet, prefix, args)
	}
	f := c.Flags()
	f.BoolVarP(&quiet, "quiet", "q", false, "print only the hazardous frames")
	f.StringVar(&prefix, "prefix", "",
		"consider only functions whose symbol starts with this (e.g. a module path)")
	return c
}

// run drives the command and returns its status, for a test.
func run(args []string, out, errOut io.Writer) int {
	return cli.Drive(RootCmd(), args, out, errOut)
}

func scanDumps(cmd *cobra.Command, quiet bool, prefix string, names []string) error {
	out, errOut := cli.Out(cmd), cli.Err(cmd)
	if len(names) == 0 {
		names = []string{"-"}
	}
	hazard := false
	for _, name := range names {
		in := cmd.InOrStdin()
		if name != "-" {
			f, err := os.Open(name)
			if err != nil {
				return cli.Wrap(err)
			}
			defer f.Close()
			in = f
		}
		rep, err := Scan(in, prefix)
		if err != nil {
			return cli.Wrap(err)
		}
		if len(names) > 1 {
			fmt.Fprintf(out, "== %s\n", name)
		}
		haz, err := report(out, errOut, rep, quiet)
		if err != nil {
			return err
		}
		if haz {
			hazard = true
		}
	}
	if hazard {
		return cli.FromStatus(1)
	}
	return nil
}

func main() {
	// This binary may be older than the repository it came from, and a wrong
	// verdict from a stale build looks exactly like a right one.
	fleet.WarnIfStale(os.Stderr)

	cli.Run(RootCmd())
}

// report prints one dump's verdict and says whether anything was hazardous.
//
// It takes its writers rather than reaching for os.Stdout, and returns the
// "nothing was read" refusal as an error rather than calling os.Exit from
// inside a function a test wants to drive.
func report(out, errOut io.Writer, rep Report, quiet bool) (bool, error) {
	// `go build -gcflags=-S` prints nothing for a package it does not have to
	// compile, so a warm build cache yields an EMPTY dump -- and an empty dump
	// read as "0 conversions, 0 hazardous", which is indistinguishable from a
	// clean sweep. Say it instead, and exit 2 so a script cannot mistake it
	// for a pass. Build with `-a` to be sure the assembly is emitted.
	if rep.Seen == 0 {
		return false, cli.Unanswerable("no STEXT symbol in the input -- nothing was read,\n" +
			"  which is NOT the same as nothing found. `go build -gcflags=-S` prints\n" +
			"  nothing for a package it does not recompile: add -a.")
	}
	if rep.Funcs == 0 {
		return false, cli.Unanswerable("%d functions in the dump, none matching the prefix --\n"+
			"  this measures nothing. Check the prefix against a symbol in the dump.", rep.Seen)
	}
	fmt.Fprintf(out, "functions read           : %d of %d in the dump\n", rep.Funcs, rep.Seen)
	fmt.Fprintf(out, "TRUNCD* conversions      : %d\n", rep.Truncs)
	fmt.Fprintf(out, "  straight to a GPR      : %d\n", rep.Drained)
	fmt.Fprintf(out, "  spilled as 8-byte MOVD : %d  (in %d frame(s))\n", rep.Spilled(), len(rep.Frames))
	fmt.Fprintf(out, "  of those, HAZARDOUS    : %d  (stride under 8, or a misaligned offset)\n", rep.Hazards())
	// Say which toolchain this is, rather than making the reader infer it.
	fix := "absent -- this toolchain does NOT carry CL 820040"
	if rep.Fpgp > 0 {
		fix = fmt.Sprintf("%d occurrence(s) -- CL 820040 is present", rep.Fpgp)
	}
	fmt.Fprintf(out, "MOV[WV]fpgp              : %s\n", fix)

	for _, f := range rep.Frames {
		if quiet && !f.Hazard {
			continue
		}
		tag := "ok "
		if f.Hazard {
			tag = "HAZ"
		}
		offs := make([]string, len(f.Spills))
		for i, s := range f.Spills {
			offs[i] = fmt.Sprint(s.Off)
		}
		stride := "-"
		if f.Stride > 0 {
			stride = fmt.Sprint(f.Stride)
		}
		fmt.Fprintf(out, "  %s %s\n      %d spill(s) at %s(SP) offsets [%s], min stride %s, conversion at %s\n",
			tag, f.Func, len(f.Spills), f.Spills[0].Slot, strings.Join(offs, " "), stride, f.Spills[0].TruncAt)
	}
	return rep.Hazards() > 0, nil
}
