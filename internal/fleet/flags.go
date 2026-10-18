package fleet

import (
	"errors"
	"flag"
)

// ParseFlags parses args and says what the command should do about the result:
// the status to exit with, and whether there is any work left to do.
//
//	if code, ok := fleet.ParseFlags(fs, args); !ok {
//		return code
//	}
//
// ⛔ IT EXISTS FOR ONE DISTINCTION THE OBVIOUS SPELLING DROPS. The shape every
// command here was written with --
//
//	if err := fs.Parse(args); err != nil {
//		return 2
//	}
//
// -- answers `-h` with 2, because the flag package reports a help request as
// an error ([flag.ErrHelp]) like any other. Asking a tool what its flags are is
// not a usage error, and the flag package's own default says so: with
// flag.ExitOnError it calls os.Exit(0) for ErrHelp and os.Exit(2) for the rest.
// Every command here uses flag.ContinueOnError instead -- so that run() can be
// tested rather than exit the process -- and that choice silently gave up the
// distinction. Measured 2026-10-18: of 36 commands, 13 answered `-h` with 2.
//
// ⛔ AND IT IS ONE HELPER RATHER THAN FOURTEEN COPIES because the help branch
// is the one nobody exercises: thirteen hand-written copies are thirteen
// branches to cover, and the twelve commands that did get it right prove the
// shape is easy to type and easy to forget. Here it is covered once, by
// TestParseFlagsSeparatesAQuestionFromAMistake, and TestEveryCommandAnswersHelpWithZero
// fails for a command that goes back to the two-line form.
func ParseFlags(fs *flag.FlagSet, args []string) (code int, ok bool) {
	switch err := fs.Parse(args); {
	case errors.Is(err, flag.ErrHelp):
		return 0, false
	case err != nil:
		return 2, false
	}
	return 0, true
}

// IsHelpRequest reports whether this argument is somebody asking what the
// command does, in any of the four spellings people actually type.
//
// ⛔ IT IS FOR THE COMMANDS [ParseFlags] CANNOT HELP: the ones that dispatch on
// a subcommand before any flag set exists. blobpurge answered `blobpurge -h`
// with 2 for exactly that reason -- `-h` is not "scan" and not "strip", so it
// fell through to "I do not know that subcommand", and the behavioural measure
// found it while the text gate was satisfied. The three commands that ask this
// question by hand each knew a DIFFERENT three of the four spellings: prmerge
// missed `-help`, pagespublish missed `help`, blobpurge asked nothing at all.
func IsHelpRequest(arg string) bool {
	switch arg {
	case "-h", "-help", "--help", "help":
		return true
	}
	return false
}
