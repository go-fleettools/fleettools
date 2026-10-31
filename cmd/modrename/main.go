// modrename gives a forked Go module its new module path, everywhere the path
// is a module path and nowhere it is not.
//
//	modrename --from github.com/ajroetker/go-highway --to github.com/go-simd/highway DIR          # report
//	modrename --from github.com/ajroetker/go-highway --to github.com/go-simd/highway --write DIR   # rewrite
//
// A fork that its consumers can actually use needs a new module path: a
// `replace` in OUR go.mod never reaches the modules that depend on ours, which
// is precisely the defect that made go-highway worth forking (its replace of
// hwy/goat reached nobody). So every import, every go.mod, every documented
// `go get` changes -- 2032 files in go-highway.
//
// What is NOT a module path is left alone, deliberately: an occurrence inside a
// file-system path. go-highway's 605 generated assembly files record where they
// were generated from,
//
//	// /Users/ajroetker/go/src/github.com/ajroetker/go-highway/hwy/contrib/...
//
// and rewriting that would invent a path that never existed on anybody's
// machine. The rule is mechanical: an occurrence directly preceded by '/' is a
// file path, not a module path.
//
// It works on the files git tracks, refuses a dirty tree (so its diff is
// exactly its own), never follows the path into a longer name
// (go-highway2 is not go-highway), and only rewrites go.mod, .go and .md
// files -- the places a module path means something. Without --write it
// reports what it would change and exits 1 if anything would.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Rewrites a forked Go module's path everywhere the path is a module path, and
nowhere it is not.

An occurrence directly preceded by '/' is a FILE path and is left alone: the
605 generated assembly files in go-highway record the directory they were
generated from, and rewriting that would invent a path that never existed on
anybody's machine.

It works on the files git tracks, refuses a dirty tree so its diff is exactly
its own, never follows the path into a longer name (go-highway2 is not
go-highway), and only touches go.mod, .go and .md files.

Exit status: 0 the files were rewritten (--write), 1 something WOULD change
(without --write), 2 the rewrite could not be made or nothing names --from.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

type options struct {
	from, to, dir string
	write         bool
}

// RootCmd builds the command. Tests drive it through parseArgs, below.
func RootCmd() *cobra.Command {
	var o options
	c := command(&o)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if err := validate(&o, args); err != nil {
			return err
		}
		// run keeps its body and its integer statuses exactly as they were:
		// each one is a deliberate choice documented against a defect, and
		// re-deciding thirty-two of those by hand is how a 1 becomes a 2.
		return cli.FromStatus(run(o, cli.Out(cmd), cli.Err(cmd)))
	}
	return c
}

func command(o *options) *cobra.Command {
	c := cli.New("modrename --from OLD --to NEW [--write] DIR",
		"Give a forked Go module its new module path", longHelp)
	f := c.Flags()
	f.StringVar(&o.from, "from", "", "the module path to replace (required)")
	f.StringVar(&o.to, "to", "", "the new module path (required)")
	f.BoolVar(&o.write, "write", false, "rewrite the files (otherwise report only)")
	return c
}

func validate(o *options, args []string) error {
	if o.from == "" || o.to == "" || o.from == o.to || len(args) != 1 {
		return cli.Unanswerable("usage: modrename --from OLD --to NEW [--write] DIR")
	}
	o.dir = args[0]
	return nil
}

// parseArgs keeps the signature this command's tests were written against, and
// now parses with pflag through the real command.
func parseArgs(args []string, stderr io.Writer) (options, int, bool) {
	var o options
	c := command(&o)
	c.RunE = func(_ *cobra.Command, rest []string) error { return validate(&o, rest) }
	code := cli.Drive(c, args, stderr, stderr)
	// --help is a status of 0 and NOT a reason to proceed: cobra prints the
	// help and returns nil, so a caller reading the status alone would print
	// the help and then do the work.
	return o, code, code == 0 && !cli.HelpRequested(c)
}

// Rename returns src with every module-path occurrence of from replaced by to,
// and how many it replaced. An occurrence counts when it is not preceded by
// '/' (a file path) and not followed by a character that continues a name.
func Rename(src []byte, from, to string) ([]byte, int) {
	var out bytes.Buffer
	n, i := 0, 0
	f := []byte(from)
	for {
		j := bytes.Index(src[i:], f)
		if j < 0 {
			out.Write(src[i:])
			return out.Bytes(), n
		}
		at := i + j
		end := at + len(f)
		prev := byte(0)
		if at > 0 {
			prev = src[at-1]
		}
		next := byte(0)
		if end < len(src) {
			next = src[end]
		}
		out.Write(src[i:at])
		if prev != '/' && !continuesName(next) && !continuesName(prev) {
			out.WriteString(to)
			n++
		} else {
			out.Write(f)
		}
		i = end
	}
}

// continuesName reports whether c would make the match part of a longer
// module or host name: go-highway2, xgithub.com, go-highway.v2.
func continuesName(c byte) bool {
	return c == '-' || c == '_' || c == '.' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func eligible(path string) bool {
	return filepath.Base(path) == "go.mod" || strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".md")
}

func run(o options, stdout, stderr io.Writer) int {
	if out, err := exec.Command("git", "-C", o.dir, "status", "--porcelain").Output(); err != nil {
		fmt.Fprintln(stderr, "modrename: not a git checkout:", err)
		return 2
	} else if len(bytes.TrimSpace(out)) > 0 {
		fmt.Fprintln(stderr, "modrename: the tree has uncommitted changes; commit or stash them first, so the diff is exactly this tool's")
		return 2
	}
	ls, err := exec.Command("git", "-C", o.dir, "ls-files", "-z").Output()
	if err != nil {
		fmt.Fprintln(stderr, "modrename:", err)
		return 2
	}
	files, replaced := 0, 0
	skipped := map[string]int{}
	var changed []string
	for _, rel := range strings.Split(strings.TrimRight(string(ls), "\x00"), "\x00") {
		if rel == "" {
			continue
		}
		p := filepath.Join(o.dir, rel)
		b, err := os.ReadFile(p)
		if err != nil {
			continue // a symlink to nowhere, a submodule: not ours to rewrite
		}
		if !bytes.Contains(b, []byte(o.from)) {
			continue
		}
		if !eligible(rel) {
			skipped[filepath.Ext(rel)]++
			continue
		}
		nb, n := Rename(b, o.from, o.to)
		if n == 0 {
			skipped["(file paths only)"]++
			continue
		}
		files++
		replaced += n
		changed = append(changed, rel)
		if o.write {
			fi, err := os.Stat(p)
			if err != nil {
				fmt.Fprintln(stderr, "modrename:", err)
				return 2
			}
			if err := os.WriteFile(p, nb, fi.Mode().Perm()); err != nil {
				fmt.Fprintln(stderr, "modrename:", err)
				return 2
			}
		}
	}
	verb := "would rewrite"
	if o.write {
		verb = "rewrote"
	}
	var sk []string
	for ext, c := range skipped {
		sk = append(sk, fmt.Sprintf("%s %d", ext, c))
	}
	sort.Strings(sk)
	fmt.Fprintf(stdout, "%s %d occurrence(s) in %d file(s); left alone (not go.mod/.go/.md, or file paths only): %s\n",
		verb, replaced, files, strings.Join(sk, ", "))
	if files == 0 {
		fmt.Fprintln(stderr, "modrename: nothing names", o.from, "-- wrong -from, or already renamed")
		return 2
	}
	if !o.write {
		return 1
	}
	return 0
}
