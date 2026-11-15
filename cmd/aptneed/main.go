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
// ⛔ A PACKAGE THAT DOES NOT EXIST IS NOT RETRIED — WHEN APT COULD CHECK.
// "Unable to locate package" means the name is wrong, and waiting twenty
// seconds to ask again dresses a typo up as an outage. The two failures get
// different exit messages because they want different people.
//
// ⛔ BUT THOSE WORDS ARE ALSO WHAT A RUN WITH NO PACKAGE LISTS SAYS, about a
// name it never checked against anything. So when `apt-get update` FAILED, the
// same sentence is evidence of nothing, and this retries and says so rather
// than call a name wrong on the strength of a lookup that did not happen. It
// does not rewrite the runner's mirrorlist on that evidence until it has seen
// it twice: see install() and the switch in aptneed().
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
again later dresses a typo up as an outage. The exception is a run whose
package lists could not be refreshed at all — there "cannot locate" is about a
name apt never checked, so it is retried, the verdict names both possibilities,
and the mirrorlist is left alone until that same unprovable answer comes back
a second time.

⛔ THE LIMITS OF THAT, said plainly because they are trade-offs and not bugs:

  - apt exits non-zero when ANY index is unreachable, even one belonging to a
    broken optional PPA while the main archive refreshed perfectly. A genuine
    typo in that job is therefore retried, and on a second unprovable answer
    it does rewrite the mirrorlist. apt offers no status that separates that
    from a cold cache, so the choice is which way to be wrong; this errs
    towards retrying, because calling a name wrong on a lookup that did not
    happen ends the job on evidence that does not exist.
  - with --retries 1 there is no attempt left to USE a rewrite after the
    second sighting, so a cold-cache outage is reported rather than cured.
    The default of 2 is what makes the fallback reachable.

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
		"on a mirror failure, and while an attempt remains to use it, rewrite "+
			mirrorList+" to the canonical archive")
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
	// unprovenName is "apt cannot locate it" said by a run whose package
	// lists could not be refreshed. It is its own answer because it is
	// genuinely two situations wearing the same words, and neither reading
	// is safe on its own — see install().
	unprovenName
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
	updateOut, updateStatus := runCommand("apt-get", "-o", "Acquire::Retries=3", "update")
	all.WriteString(updateOut)
	// ⛔ A FAILED `update` IS NOT FATAL ON ITS OWN. apt exits non-zero when any
	// index is unreachable while the cached lists are still usable, and the
	// install below may well succeed from them. What decides is the install.
	args := append([]string{"-o", "Acquire::Retries=3", "install", "-y",
		"--no-install-recommends"}, pkgs...)
	out, status := runCommand("apt-get", args...)
	all.WriteString(out)
	if status == 0 {
		return all.String(), otherFailure, true
	}
	// ⛔⛔ THE INSTALL'S OWN WORDS DECIDE — AND WHEN THE LISTS COULD NOT BE
	// REFRESHED, THEY DECIDE NOTHING. Two reviews of this function returned
	// OPPOSITE verdicts on the same two lines of apt output:
	//
	//	update:  Temporary failure resolving '…'
	//	install: E: Unable to locate package poppler-utils
	//
	// One called it a typo wrongly retried as weather (a broken optional PPA
	// beside a real misspelling); the other called it an outage wrongly
	// dismissed as a typo (a cold cache, the name never checked against
	// anything). Both are right about their own case, and the two cases are
	// INDISTINGUISHABLE from the text — which is why neither reading may be
	// hard-coded. A tool that picks one is wrong half the time and confident
	// every time.
	//
	// So it is its own answer: "cannot locate" is UNPROVEN when the lists are
	// stale. It is retried like a mirror failure, because retrying is cheap
	// and the fallback may settle it, and the closing verdict names BOTH
	// facts rather than choosing between them.
	//
	// ⛔⛔ STALENESS IS `update`'s EXIT STATUS, NOT ITS PHRASES. An earlier
	// version asked why(updateOut) for a mirror phrase, which answers the
	// wrong question twice over. It said YES to a `Failed to fetch … 404 Not
	// Found` — a stale index, which is not an outage — and so dressed a plain
	// misspelling up as unprovable. And it said NO to every refresh that
	// failed without naming the network: an unsigned repository (NO_PUBKEY
	// disables the source), a held /var/lib/apt/lists/lock, a full disk. Those
	// leave the lists exactly as stale as a dead mirror does, and the name
	// exactly as unchecked. What matters is not why the refresh failed but
	// THAT it failed, which is the one thing the status says and no phrase
	// does.
	k := why(out)
	if k == noSuchPackage && updateStatus != 0 {
		k = unprovenName
	}
	// The pair is still RETURNED, because apt's whole account of the attempt
	// belongs in the log; it is only the VERDICT that narrows.
	return all.String(), k, false
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
	// ⛔ A NEGATIVE --retries RAN ZERO ATTEMPTS and then reported that apt had
	// failed with its output above, there being no output: a verdict about a
	// command never run.
	if retries < 0 {
		return cli.Unanswerable("--retries is the number of attempts AFTER the first, so it "+
			"cannot be %d: 0 means try once", retries)
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
		// ⛔ APT'S OWN WORDS, ON EVERY FAILED ATTEMPT. They used to be printed
		// only for a failure this did not recognise, so a `Failed to fetch …
		// 404 Not Found` — a stale index, not an outage — was classified as a
		// mirror failure, retried, and shown to nobody. A reader cannot
		// disagree with a classification whose evidence was swallowed.
		fmt.Fprint(stderr, output)
		switch k {
		case noSuchPackage:
			fmt.Fprintf(stderr, "aptneed: apt cannot locate %s — that is a name, not an "+
				"outage, so this is not retried\n", strings.Join(pkgs, ", "))
			return cli.Found("no such package: %s", strings.Join(pkgs, ", "))
		case mirrorFailure, unprovenName:
			if k == unprovenName {
				fmt.Fprintf(stderr, "aptneed: apt cannot locate %s, and it could not refresh "+
					"its package lists either — so that may be the name or may be the "+
					"mirror, and this retries rather than guess\n", strings.Join(pkgs, ", "))
			} else {
				fmt.Fprintf(stderr, "aptneed: the runner's mirror could not be reached\n")
			}
			// ⛔⛔ AN IRREVERSIBLE ACT NEEDS MORE THAN AMBIGUOUS EVIDENCE.
			// Rewriting the mirrorlist changes the runner for every later step
			// of the job, and unprovenName is by construction a case this
			// cannot read: it may be a misspelling. So a mirror failure — the
			// install's own words, the unambiguous one — buys the rewrite at
			// once, while an unproven name must REPEAT first. One cheap retry
			// costs a typo the delay and nothing else; a cold cache that is
			// still cold on the second look has earned the stronger remedy.
			// ⛔⛔ "THE SAME ANSWER TWICE", AND IT USED TO MEAN "ANY EARLIER
			// FAILURE". Written `attempt > 0`, a first attempt that died of
			// something else entirely — `dpkg was interrupted` — made the
			// NEXT unproven name rewrite the mirrorlist on its first
			// sighting, which is the single cheap retry this is supposed to
			// cost a typo. Review measured it, and --help said otherwise.
			//
			// `last` is the previous attempt's kind here, because it is
			// assigned at the END of the loop body.
			strong := k == mirrorFailure || (k == unprovenName && last == unprovenName)
			// ⛔ AND NOT WHEN THERE IS NO ATTEMPT LEFT TO USE IT. Rewriting the
			// mirrorlist on the last turn changes the runner permanently for
			// every later step of the job and tries nothing with it.
			if fallback && !swapped && strong && attempt < retries {
				if err := writeFile(mirrorList, []byte(canonicalMirror)); err != nil {
					fmt.Fprintf(stderr, "aptneed: could not rewrite %s: %v\n", mirrorList, err)
				} else {
					swapped = true
					fmt.Fprintf(stderr, "aptneed: %s now names %s; every source resolves "+
						"through it\n", mirrorList, strings.TrimSpace(canonicalMirror))
				}
			}
		}
		last = k
	}
	// ⛔ THE VERDICT IS AFTER THE LOOP, not inside it on the last turn. Written
	// the other way it needed a `return nil` underneath that nothing could
	// reach — and an unreachable line is a line no test can defend.
	fmt.Fprintf(stderr, "aptneed: could not install %s in %d attempt(s): %s\n",
		strings.Join(pkgs, ", "), retries+1, reason(last, swapped))
	return cli.Found("could not install %s", strings.Join(pkgs, ", "))
}

// reason is the sentence for a failure this did not return early on.
//
// ⛔ THERE IS NO noSuchPackage CASE. That one returns the moment it is seen,
// because retrying a name cannot change it — so a branch for it here would be
// dead code dressed as thoroughness. The coverage gate is what said so.
// ⛔ AND IT IS TOLD WHETHER THE CANONICAL ARCHIVE WAS ACTUALLY TRIED. The
// first version said "including the canonical archive" unconditionally — so
// with --fallback-mirror=false, which rewrites nothing, the verdict claimed a
// fallback that had never been attempted. A sentence about what was tried has
// to be a function of what was tried.
func reason(k kind, triedCanonical bool) string {
	if k == unprovenName {
		// ⛔ BOTH FACTS, AND NO VERDICT BETWEEN THEM. Naming only one sends
		// the reader to the wrong place half the time, with full confidence.
		return "apt could not locate it AND could not refresh its package lists, so this is " +
			"either a wrong name or a mirror that never came back — check the spelling first, " +
			"it is the cheaper of the two"
	}
	if k != mirrorFailure {
		return "apt failed and said nothing this recognises; its output is above"
	}
	if triedCanonical {
		return "the apt mirror could not be reached, including the canonical archive — " +
			"this is the network, not the package"
	}
	return "the apt mirror could not be reached, and the canonical archive was not tried — " +
		"this is the network, not the package"
}
