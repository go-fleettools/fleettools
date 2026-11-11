// Command aptneed installs the apt packages a CI job needs and refuses to
// call a mirror outage a missing package — or a successful install a present
// binary.
//
// ⛔⛔ A THIRD PARTY'S MIRROR MUST NOT BLOCK A RELEASE. Measured in
// go-pdfkit/ops on 2026-11-11 (issue #90): the step
//
//	sudo apt-get update && sudo apt-get install -y --no-install-recommends poppler-utils
//
// failed FOUR times in half an hour on one merge commit, every time with
//
//	E: Failed to fetch mirror+file:/etc/apt/apt-mirrors.txt/pool/main/p/poppler/…
//	   Temporary failure resolving 'azure.archive.ubuntu.com'
//
// The pull request had been green on the same workflow minutes earlier. Nothing
// about the repository had changed; the runner's MIRRORLIST had. `ghrelease`
// will not tag while main is red — correctly — so an unreachable mirror held a
// tagged release hostage, and the job had no way to say which of the two it was.
//
// ⛔ EVERY `apt-get` ON A GITHUB RUNNER GOES THROUGH THAT MIRRORLIST.
// /etc/apt/apt-mirrors.txt names a cloud-local mirror, and every source in
// /etc/apt/sources.list.d resolves through it with `mirror+file:`. When the
// name does not resolve, pointing at archive.ubuntu.com "instead" changes
// nothing: the sources still read the mirrorlist. So the fallback here
// rewrites THE MIRRORLIST, which is the one file every source agrees to obey.
//
// ⛔ A PACKAGE THAT DOES NOT EXIST IS NOT RETRIED. "Unable to locate package"
// means the name is wrong, and waiting twenty seconds to ask again dresses a
// typo up as an outage. The two failures get different exit messages because
// they want different people.
//
// ⛔ AND APT SAYING YES IS NOT THE BINARY BEING THERE. --need names what must
// end up on PATH; a package renamed upstream installs cleanly and leaves the
// job without its judge, which is how a test suite comes to skip the thing it
// was measuring. The check is the point, so --need is required.
//
//	aptneed --need pdftoppm poppler-utils
//	aptneed --need convert --need identify imagemagick
//
// Exit status: 0 the binaries are there, 1 they are not and why, 2 the
// arguments could not be read.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Installs the apt packages a CI job needs, and refuses to call a mirror
outage a missing package.

Every apt-get on a GitHub runner resolves through /etc/apt/apt-mirrors.txt. When
that mirror's name does not resolve, the install fails with "Temporary failure
resolving" and a job that treats it as a build failure hands a third party a
veto over every release. After the first such failure this rewrites the
MIRRORLIST to the canonical archive — the one file every source obeys — and
tries again.

A package apt cannot locate is NOT retried: that is a wrong name, and asking
again later dresses a typo up as an outage.

--need names a binary that must be on PATH when this exits, and is required:
apt reporting success is not the binary being there, and a package renamed
upstream installs cleanly while leaving the job without its judge.

Exit status: 0 the binaries are there, 1 they are not and why, 2 the arguments
could not be read.`

// mirrorList is the file every source on a GitHub runner resolves through.
const mirrorList = "/etc/apt/apt-mirrors.txt"

// canonicalMirror is what the mirrorlist is rewritten to when the runner's own
// mirror cannot be resolved. It is the archive every Ubuntu machine can reach
// and the one apt falls back to by hand everywhere else.
const canonicalMirror = "http://archive.ubuntu.com/ubuntu/\n"

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	var (
		need     []string
		retries  int
		delay    time.Duration
		fallback bool
	)
	c := cli.New("aptneed [flags] PACKAGE...",
		"install apt packages for CI, telling a mirror outage from a missing package", longHelp)
	c.Flags().StringArrayVar(&need, "need", nil,
		"a binary that must be on PATH when this exits (repeatable, required)")
	c.Flags().IntVar(&retries, "retries", 2, "attempts after the first")
	c.Flags().DurationVar(&delay, "delay", 20*time.Second, "wait between attempts")
	c.Flags().BoolVar(&fallback, "fallback-mirror", true,
		"after a mirror failure, rewrite "+mirrorList+" to the canonical archive")
	c.Args = cobra.ArbitraryArgs
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return aptneed(cmd, args, need, retries, delay, fallback)
	}
	return c
}

// run keeps the signature this command's tests drive it through.
func run(argv []string, stdout, stderr io.Writer) int {
	return cli.Drive(RootCmd(), argv, stdout, stderr)
}

// runCommand is the seam: it runs a program and returns everything it printed
// with its exit status. Tests replace it, because what this command decides is
// a function of apt's words.
var runCommand = func(name string, args ...string) (string, int) {
	c := exec.Command(name, args...)
	var out strings.Builder
	c.Stdout = &out
	c.Stderr = &out
	c.Stdin = nil
	err := c.Run()
	if err == nil {
		return out.String(), 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return out.String(), ee.ExitCode()
	}
	return out.String() + "\n" + err.Error(), -1
}

// lookPath and writeFile are the other two seams, for the same reason.
var (
	lookPath  = exec.LookPath
	writeFile = func(path string, data []byte) error { return os.WriteFile(path, data, 0o644) }
	sleep     = time.Sleep
)

// missing is the --need binaries that are not on PATH.
func missing(need []string) []string {
	var out []string
	for _, n := range need {
		if _, err := lookPath(n); err != nil {
			out = append(out, n)
		}
	}
	return out
}

// why says what kind of failure apt's output describes.
//
// ⛔ THE ORDER MATTERS. A run that could not reach the mirror ALSO prints
// "Unable to locate package" for everything it was asked about, because its
// package lists are empty — so reading the names first would call every
// outage a typo. The unreachable mirror is the earlier and truer fact.
type kind int

const (
	otherFailure kind = iota
	mirrorFailure
	noSuchPackage
)

func why(output string) kind {
	for _, s := range []string{
		"Temporary failure resolving",
		"Could not resolve",
		"Failed to fetch",
		"Connection failed",
		"Unable to connect",
	} {
		if strings.Contains(output, s) {
			return mirrorFailure
		}
	}
	for _, s := range []string{
		"Unable to locate package",
		"has no installation candidate",
	} {
		if strings.Contains(output, s) {
			return noSuchPackage
		}
	}
	return otherFailure
}

// install is one attempt: refresh the lists, then install.
//
// ⛔ Acquire::Retries is apt's OWN retry, which costs nothing and catches the
// single dropped connection this would otherwise sleep twenty seconds over.
func install(pkgs []string) (string, kind, bool) {
	var all strings.Builder
	out, status := runCommand("apt-get", "-o", "Acquire::Retries=3", "update")
	all.WriteString(out)
	// ⛔ A FAILED `update` IS NOT FATAL ON ITS OWN. apt exits non-zero when any
	// index is unreachable while the cached lists are still usable, and the
	// install below may well succeed from them. What decides is the install.
	args := append([]string{"-o", "Acquire::Retries=3", "install", "-y",
		"--no-install-recommends"}, pkgs...)
	out, status = runCommand("apt-get", args...)
	all.WriteString(out)
	if status == 0 {
		return all.String(), otherFailure, true
	}
	return all.String(), why(all.String()), false
}

func aptneed(cmd *cobra.Command, pkgs, need []string, retries int, delay time.Duration, fallback bool) error {
	stdout, stderr := cli.Out(cmd), cli.Err(cmd)
	if len(pkgs) == 0 {
		return cli.Unanswerable("name at least one package to install")
	}
	if len(need) == 0 {
		return cli.Unanswerable("--need names the binary the package must provide, and is " +
			"required: apt reporting success is not the binary being there")
	}

	// ⛔ ALREADY THERE IS A PASS, AND A SILENT ONE. A warm runner, a container
	// built with the judge in it, or a second job in the same shell must not
	// pay for a network round trip to be told what PATH already says.
	if len(missing(need)) == 0 {
		fmt.Fprintf(stdout, "aptneed: %s already on PATH, nothing to install\n",
			strings.Join(need, ", "))
		return nil
	}

	swapped := false
	last := otherFailure
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			fmt.Fprintf(stderr, "aptneed: attempt %d of %d after %s\n",
				attempt+1, retries+1, delay)
			sleep(delay)
		}
		output, k, ok := install(pkgs)
		if ok {
			if gone := missing(need); len(gone) > 0 {
				// ⛔ apt said yes and the binary is not there: the package
				// does not provide it (renamed upstream, or the wrong name
				// with a real package behind it). Retrying cannot help.
				//
				// ⛔ AND IT IS PRINTED HERE. cli.Status does not print a
				// finding's message — the command is expected to have said it
				// — so returning the sentence alone sends it nowhere.
				fmt.Fprintf(stderr, "aptneed: apt installed %s and %s is still not on PATH: "+
					"that package does not provide it\n",
					strings.Join(pkgs, ", "), strings.Join(gone, ", "))
				return cli.Found("the package does not provide %s", strings.Join(gone, ", "))
			}
			fmt.Fprintf(stdout, "aptneed: installed %s, %s on PATH\n",
				strings.Join(pkgs, ", "), strings.Join(need, ", "))
			return nil
		}
		switch k {
		case noSuchPackage:
			fmt.Fprintf(stderr, "aptneed: apt cannot locate %s — that is a name, not an "+
				"outage, so this is not retried\n", strings.Join(pkgs, ", "))
			return cli.Found("no such package: %s", strings.Join(pkgs, ", "))
		case mirrorFailure:
			fmt.Fprintf(stderr, "aptneed: the runner's mirror could not be reached\n")
			if fallback && !swapped {
				if err := writeFile(mirrorList, []byte(canonicalMirror)); err != nil {
					fmt.Fprintf(stderr, "aptneed: could not rewrite %s: %v\n", mirrorList, err)
				} else {
					swapped = true
					fmt.Fprintf(stderr, "aptneed: %s now names %s; every source resolves "+
						"through it\n", mirrorList, strings.TrimSpace(canonicalMirror))
				}
			}
		default:
			fmt.Fprint(stderr, output)
		}
		last = k
	}
	// ⛔ THE VERDICT IS AFTER THE LOOP, not inside it on the last turn. Written
	// the other way it needed a `return nil` underneath that nothing could
	// reach — and an unreachable line is a line no test can defend.
	fmt.Fprintf(stderr, "aptneed: could not install %s in %d attempt(s): %s\n",
		strings.Join(pkgs, ", "), retries+1, reason(last))
	return cli.Found("could not install %s", strings.Join(pkgs, ", "))
}

// reason is the sentence for a failure this did not return early on.
//
// ⛔ THERE IS NO noSuchPackage CASE. That one returns the moment it is seen,
// because retrying a name cannot change it — so a branch for it here would be
// dead code dressed as thoroughness. The coverage gate is what said so.
func reason(k kind) string {
	if k == mirrorFailure {
		return "the apt mirror could not be reached, including the canonical archive — " +
			"this is the network, not the package"
	}
	return "apt failed and said nothing this recognises; its output is above"
}
