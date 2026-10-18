// modrename gives a forked Go module its new module path, everywhere the path
// is a module path and nowhere it is not.
//
//	modrename -from github.com/ajroetker/go-highway -to github.com/go-simd/highway DIR          # report
//	modrename -from github.com/ajroetker/go-highway -to github.com/go-simd/highway -write DIR   # rewrite
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
// files -- the places a module path means something. Without -write it
// reports what it would change and exits 1 if anything would.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

func main() {
	o, code, proceed := parseArgs(os.Args[1:], os.Stderr)
	if !proceed {
		os.Exit(code)
	}
	fleet.WarnIfStale(os.Stderr)
	os.Exit(run(o, os.Stdout, os.Stderr))
}

type options struct {
	from, to, dir string
	write         bool
}

func parseArgs(args []string, stderr io.Writer) (options, int, bool) {
	fs := flag.NewFlagSet("modrename", flag.ContinueOnError)
	fs.SetOutput(stderr)
	o := options{}
	fs.StringVar(&o.from, "from", "", "the module path to replace (required)")
	fs.StringVar(&o.to, "to", "", "the new module path (required)")
	fs.BoolVar(&o.write, "write", false, "rewrite the files (otherwise report only)")
	switch err := fs.Parse(args); {
	case errors.Is(err, flag.ErrHelp):
		return o, 0, false
	case err != nil:
		return o, 2, false
	}
	if o.from == "" || o.to == "" || o.from == o.to || fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: modrename -from OLD -to NEW [-write] DIR")
		return o, 2, false
	}
	o.dir = fs.Arg(0)
	return o, 0, true
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
