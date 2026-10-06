package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

func main() {
	// This binary may be older than the repository it came from, and a wrong
	// verdict from a stale build looks exactly like a right one.
	fleet.WarnIfStale(os.Stderr)

	quiet := flag.Bool("q", false, "print only the hazardous frames")
	prefix := flag.String("prefix", "", "consider only functions whose symbol starts with this (e.g. a module path)")
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, `usage: truncspill [-q] [file...]

Reads `+"`go build -gcflags=-S`"+` output for GOARCH=loong64 and reports every
float-to-integer conversion whose result is spilled as eight bytes in a way
that can reach the stack slots beside it -- golang.org/issue/81000.

    CGO_ENABLED=0 GOOS=linux GOARCH=loong64 GOTOOLCHAIN=go1.27.1 \
      go build -gcflags=-S ./... 2>asm.s
    truncspill asm.s

Add test files to the picture with:

    go test -run '^$' -gcflags=-S ./... 2>asm.s

Exits 1 when any frame is hazardous, 0 when none is, 2 on an error. A clean
run means "no exposure found in THIS build", not "cannot happen": inlining and
register allocation are per-build, and a frame reported hazardous may still
pass, because a clobbered byte can be rewritten before anyone reads it.
`)
	}
	flag.Parse()

	names := flag.Args()
	if len(names) == 0 {
		names = []string{"-"}
	}

	exit := 0
	for _, name := range names {
		in := os.Stdin
		if name != "-" {
			f, err := os.Open(name)
			if err != nil {
				fmt.Fprintln(os.Stderr, "truncspill:", err)
				os.Exit(2)
			}
			defer f.Close()
			in = f
		}
		rep, err := Scan(in, *prefix)
		if err != nil {
			fmt.Fprintln(os.Stderr, "truncspill:", err)
			os.Exit(2)
		}
		if len(names) > 1 {
			fmt.Printf("== %s\n", name)
		}
		if report(rep, *quiet) {
			exit = 1
		}
	}
	os.Exit(exit)
}

// report prints one dump's verdict and says whether anything was hazardous.
func report(rep Report, quiet bool) bool {
	// `go build -gcflags=-S` prints nothing for a package it does not have to
	// compile, so a warm build cache yields an EMPTY dump -- and an empty dump
	// read as "0 conversions, 0 hazardous", which is indistinguishable from a
	// clean sweep. Say it instead, and exit 2 so a script cannot mistake it
	// for a pass. Build with `-a` to be sure the assembly is emitted.
	if rep.Seen == 0 {
		fmt.Fprintln(os.Stderr, "truncspill: no STEXT symbol in the input -- nothing was read,\n"+
			"  which is NOT the same as nothing found. `go build -gcflags=-S` prints\n"+
			"  nothing for a package it does not recompile: add -a.")
		os.Exit(2)
	}
	if rep.Funcs == 0 {
		fmt.Fprintf(os.Stderr, "truncspill: %d functions in the dump, none matching the prefix --\n"+
			"  this measures nothing. Check the prefix against a symbol in the dump.\n", rep.Seen)
		os.Exit(2)
	}
	fmt.Printf("functions read           : %d of %d in the dump\n", rep.Funcs, rep.Seen)
	fmt.Printf("TRUNCD* conversions      : %d\n", rep.Truncs)
	fmt.Printf("  straight to a GPR      : %d\n", rep.Drained)
	fmt.Printf("  spilled as 8-byte MOVD : %d  (in %d frame(s))\n", rep.Spilled(), len(rep.Frames))
	fmt.Printf("  of those, HAZARDOUS    : %d  (stride under 8, or a misaligned offset)\n", rep.Hazards())
	// Say which toolchain this is, rather than making the reader infer it.
	fix := "absent -- this toolchain does NOT carry CL 820040"
	if rep.Fpgp > 0 {
		fix = fmt.Sprintf("%d occurrence(s) -- CL 820040 is present", rep.Fpgp)
	}
	fmt.Printf("MOV[WV]fpgp              : %s\n", fix)

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
		fmt.Printf("  %s %s\n      %d spill(s) at %s(SP) offsets [%s], min stride %s, conversion at %s\n",
			tag, f.Func, len(f.Spills), f.Spills[0].Slot, strings.Join(offs, " "), stride, f.Spills[0].TruncAt)
	}
	return rep.Hazards() > 0
}
