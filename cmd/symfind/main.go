// Command symfind says whether an ELF file EXPORTS a dynamic symbol.
//
// It answers the question a dependency argument turns on: does this library
// PROVIDE the function, or merely consume it? Those have opposite
// consequences, and a reader that does not separate them answers the wrong one
// confidently.
//
// ⛔ THE CASE IT WAS WRITTEN FOR. Ten projects in the go-pkgx pantry declare
// `gnu.org/gettext` on every platform, and on linux none of them links
// `libintl`. The explanation — "glibc provides gettext itself" — is the kind
// of thing everybody repeats and nobody checks. Measured 2026-10-28 against
// the registry's own glibc 2.44, with `printf` as the control:
//
//	libc.so.6: 2921 exported dynamic symbol(s)
//	positive control: it does export printf, so an absence below is an absence
//	  gettext true · dcgettext true · dgettext true
//	  bindtextdomain true · textdomain true · ngettext true
//
// So on linux/glibc nothing links -lintl, and GNU libintl is a darwin
// requirement. A declaration that does not say `darwin:` is dead weight for
// every linux consumer.
//
//	symfind -must printf libc.so.6 gettext dcgettext ngettext
//
// ⛔ UNDEFINED SYMBOLS ARE EXCLUDED, and that is the whole correctness of it.
// `libfoo.so` listing `gettext` as UNDEF means it CALLS gettext — the exact
// opposite of providing it. Counting those would turn "glibc provides this"
// into "something here mentions this", which is true of almost any library and
// answers nothing.
package main

import (
	"debug/elf"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

var osExit = os.Exit

func main() {
	fleet.WarnIfStale(os.Stderr)
	osExit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("symfind", flag.ContinueOnError)
	fs.SetOutput(stderr)
	must := fs.String("must", "", "a symbol that must be exported, as the positive control")
	if code, ok := fleet.ParseFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() < 2 {
		fmt.Fprintln(stderr, "usage: symfind [-must sym] <file.so> <symbol>...")
		return 2
	}
	path, wanted := fs.Arg(0), fs.Args()[1:]

	exported, err := exportsOf(path)
	if err != nil {
		fmt.Fprintln(stderr, "symfind:", err)
		return 1
	}
	// ⛔ A FILE THAT EXPORTS NOTHING IS A READ THAT FAILED, not a library that
	// provides nothing: a stripped object, the wrong file, a path that
	// resolved somewhere unexpected. Reporting "false" for every symbol would
	// be an answer, and the wrong one.
	if len(exported) == 0 {
		fmt.Fprintf(stderr, "symfind: %s exports no dynamic symbol at all — a broken read, not an answer\n", path)
		return 1
	}
	if *must != "" && !exported[*must] {
		fmt.Fprintf(stderr, "symfind: positive control FAILED: %s does not export %q, so an absence below means nothing\n", path, *must)
		return 1
	}

	fmt.Fprintf(stdout, "%s: %d exported dynamic symbol(s)\n", path, len(exported))
	if *must != "" {
		fmt.Fprintf(stdout, "positive control: it does export %s, so an absence below is an absence\n", *must)
	}
	for _, w := range wanted {
		fmt.Fprintf(stdout, "  %-24s %v\n", w, exported[w])
	}
	return 0
}

// exportsOf is the set of dynamic symbols a file DEFINES.
func exportsOf(path string) (map[string]bool, error) {
	f, err := elf.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	syms, err := f.DynamicSymbols()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, s := range syms {
		if s.Section == elf.SHN_UNDEF {
			continue
		}
		// glibc spells a versioned symbol `gettext@@GLIBC_2.2.5`; the caller
		// asks for `gettext`, and the version is not part of the question.
		name := s.Name
		if i := strings.Index(name, "@"); i >= 0 {
			name = name[:i]
		}
		if name != "" {
			out[name] = true
		}
	}
	return out, nil
}
