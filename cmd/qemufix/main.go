// Command qemufix repairs the GitHub Actions lanes that run a Go test binary
// under qemu user-mode emulation, broken fleet-wide by Ubuntu 26.04.
//
//	qemufix .github/workflows/ci.yml        # rewrite in place
//	qemufix --check .github/workflows/*.yml # report, change nothing (exit 1 if work remains)
//
// # THE DEFECT
//
// `ubuntu-latest` became Ubuntu 26.04 from 2026-10-19. There `qemu-user-static`
// is a VIRTUAL package and apt refuses to choose between its two providers:
//
//	E: Package 'qemu-user-static' has no installation candidate
//	Package qemu-user-static is a virtual package provided by:
//	  qemu-user-binfmt-hwe 1:10.2.1+ds-1ubuntu4.4
//	  qemu-user-binfmt     1:10.2.1+ds-1ubuntu3.3
//
// The step exits 100 in about twenty seconds. 333 repositories in 258
// organisations of this account carried the old line on 2026-10-25, having gone
// red between a green main and the next push without anything being committed.
//
// # THE TWO THINGS THAT ARE NOT THE FIX
//
// ⚠ Installing a PROVIDER is not the fix. `qemu-user-binfmt` registers binfmt
// handlers; it does not ship the `qemu-<arch>-static` binaries these lanes
// invoke. A lane given only a provider takes its `command -v` branch and goes
// GREEN while testing nothing -- worse than red. `qemu-user` ships the dynamic
// `qemu-<arch>`, which is all `go test -exec` needs, so this installs
// `qemu-user qemu-user-binfmt` and accepts EITHER binary name.
//
// ⛔ Skipping on a missing emulator is not the fix either. Every rewrite here
// FAILS the lane when no emulator is found, and PRINTS which one it ran
// (`running ppc64le under qemu-ppc64le`). Without that line, "ran under
// emulation" and "skipped" are indistinguishable from a green tick -- which is
// how a fix for this looks like it worked while testing nothing.
//
// # WHY IT REFUSES RATHER THAN GUESSES
//
// A sweep that edits 333 files cannot be reviewed file by file, so the only
// safe failure is a loud one. qemufix recognises four shapes (see Shape in
// patch.go) and exits non-zero NAMING THE FILE on anything else: a half-patched
// file, two install steps, a `run:` that is not the step's last key, two
// emulated lanes of different shapes. The brief for this sweep said there were
// three shapes; reading the files found a fourth (`retry+case`, a third of the
// first batch). A tool that guessed would have silently mangled those.
//
// It is idempotent: a file already carrying the fix comes back byte-identical
// and is reported `already-fixed`.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

var osExit = os.Exit

// failure is a file this tool refused or could not write (exit 1). Anything
// else out of the command is a usage error (exit 2).
type failure struct{ err error }

func (f *failure) Error() string { return f.err.Error() }
func (f *failure) Unwrap() error { return f.err }

const longHelp = `Repair the GitHub Actions lanes that run Go tests under qemu user-mode
emulation, broken by ubuntu-latest becoming Ubuntu 26.04 on 2026-10-19, where
qemu-user-static is a virtual package with two providers and no installation
candidate.

Each file is rewritten to install "qemu-user qemu-user-binfmt", to accept
EITHER qemu-<arch>-static or qemu-<arch> at run time, to FAIL the lane when
neither is installed, and to print which emulator it ran.

Four shapes are recognised. Anything else is refused by name rather than
guessed at: with 333 repositories to sweep, the only safe failure is a loud
one.

Running it twice changes nothing: a file already carrying the fix is reported
already-fixed and left byte-identical.

Exits 1 when a file is refused, or when --check finds work remaining; 2 on a
usage error.`

func RootCmd() *cobra.Command {
	var check bool
	c := &cobra.Command{
		Use:           "qemufix FILE... [flags]",
		Short:         "Repair qemu-user CI lanes broken by Ubuntu 26.04",
		Long:          longHelp,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MinimumNArgs(1),
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return run(cmd, args, check)
	}
	c.Flags().BoolVarP(&check, "check", "n", false,
		"report what would change and write nothing; exit 1 if any file still needs the fix")
	return c
}

func run(cmd *cobra.Command, files []string, check bool) error {
	out := cmd.OutOrStdout()
	patched, already, refused := 0, 0, 0
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "qemufix: %v\n", err)
			refused++
			continue
		}
		got, shape, err := Patch(f, src)
		if err != nil {
			// The error already names the file: that is the whole contract of
			// a refusal in a 333-repository sweep.
			fmt.Fprintf(cmd.ErrOrStderr(), "qemufix: REFUSED %v\n", err)
			refused++
			continue
		}
		if shape == ShapeFixed {
			fmt.Fprintf(out, "%s: already-fixed\n", f)
			already++
			continue
		}
		if check {
			fmt.Fprintf(out, "%s: %s NEEDS FIX\n", f, shape)
			patched++
			continue
		}
		if err := os.WriteFile(f, got, 0o644); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "qemufix: %v\n", err)
			refused++
			continue
		}
		fmt.Fprintf(out, "%s: %s patched\n", f, shape)
		patched++
	}
	// A sweep that read nothing must not look like a clean bill.
	fmt.Fprintf(out, "\n%d file(s): %d patched, %d already fixed, %d refused\n",
		len(files), patched, already, refused)
	if refused > 0 {
		return &failure{fmt.Errorf("%d file(s) refused; nothing was guessed at", refused)}
	}
	if check && patched > 0 {
		return &failure{fmt.Errorf("%d file(s) still need the fix", patched)}
	}
	return nil
}

func execute(c *cobra.Command) int {
	err := c.Execute()
	if err == nil {
		return 0
	}
	fmt.Fprintf(c.ErrOrStderr(), "qemufix: %v\n", err)
	var f *failure
	if errors.As(err, &f) {
		return 1
	}
	return 2
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	osExit(execute(RootCmd()))
}
