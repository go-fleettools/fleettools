// Command binnames says what `go install` will call each command of a Go
// repository, and whether a well-known command already owns that name.
//
//	binnames ~/src/go-authn-kinit        # a local clone
//	binnames go-authn/kinit              # a repository, read from GitHub
//	binnames fleet                       # every repository of the fleet
//
// Exit status: 0 no collision and nothing unread; 1 a collision; 2 something
// could not be read, so the answer is unknown.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/binnames"
	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `binnames lists every package main of a repository and the name 'go install'
gives it (the last element of the import path, or of the module path for a
main at the root, with a /vN suffix skipped), then compares each name,
ignoring case and .exe, with the reference lists committed in
internal/binnames/ref: POSIX utilities, the commands of Debian's most
installed packages, macOS /usr/bin and /usr/sbin, Windows commands, and the
tool suites MIT krb5, Heimdal, OpenSSH, coreutils, util-linux, iproute2, git,
docker, kubectl, systemd, Java, Python and Node. Every list names the source it
was read from.

The host's PATH is a SUPPLEMENT: it knows what this machine has installed and
nothing else, and the output says which directories it read. The fleet's own
install directories (GOBIN, GOPATH/bin, ~/.local/bin) are left out, or every
tool would collide with itself.

An operand that is a directory is read as it is on disk. An operand of the form
owner/name is read from GitHub: one git-trees API call, then raw file contents.
A main package there is looked for in the root, in directories with a main.go
and under cmd/; a main package elsewhere under another file name is not seen.

Exit status: 0 clean; 1 at least one collision; 2 something could not be read.`

// opts are shared by the repository command and the fleet sweep.
type opts struct {
	allow    []string
	noPath   bool
	pathSkip []string
	list     bool
}

func rootCmd() *cobra.Command {
	var o opts
	c := cli.New("binnames [flags] DIR|owner/name...", "Does a command we install share its name with a well-known one?", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cli.Unanswerable("name a directory or owner/name, or use `binnames fleet`")
		}
		o.list = true
		ck, err := newChecker(cmd.ErrOrStderr(), o)
		if err != nil {
			return cli.Wrap(err)
		}
		budget := &binnames.Budget{Cap: 600}
		var res []result
		for _, a := range args {
			res = append(res, ck.one(a, budget))
		}
		return ck.finish(cmd.OutOrStdout(), res, o.list)
	}
	c.PersistentFlags().StringSliceVar(&o.allow, "allow", nil, "a binary name that is allowed to collide (repeatable)")
	c.PersistentFlags().BoolVar(&o.noPath, "no-path", false, "do not read the host's PATH")
	c.PersistentFlags().StringSliceVar(&o.pathSkip, "path-skip", nil, "a PATH directory to leave out (repeatable), besides GOBIN, GOPATH/bin and ~/.local/bin")
	c.AddCommand(fleetCmd(&o))
	return c
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(rootCmd())
}

// A checker holds the reference index for one run.
type checker struct {
	idx       binnames.Index
	allow     map[string]bool
	pathLine  string
	hostFails []binnames.Unread
	raw       func(string) ([]byte, error)
}

func newChecker(errOut io.Writer, o opts) (*checker, error) {
	idx, err := binnames.LoadRefs()
	if err != nil {
		return nil, err
	}
	ck := &checker{idx: idx, allow: map[string]bool{}}
	for _, a := range o.allow {
		ck.allow[binnames.Normalise(a)] = true
	}
	if o.noPath {
		ck.pathLine = "PATH supplement: off (--no-path)"
	} else {
		skip := ownBinDirs()
		for _, s := range o.pathSkip {
			skip[filepath.Clean(s)] = true
		}
		read, failed := idx.PathSupplement(os.Getenv("PATH"), skip)
		ck.hostFails = failed
		var sk []string
		for d := range skip {
			sk = append(sk, d)
		}
		sort.Strings(sk)
		ck.pathLine = fmt.Sprintf("PATH supplement: read %d host PATH directories (this machine only; left out: %s)",
			len(read), strings.Join(sk, ", "))
	}
	return ck, nil
}

// ownBinDirs are where this fleet installs its tools. They are skipped in the
// PATH supplement: a tool's own binary is not a collision with itself.
func ownBinDirs() map[string]bool {
	out := map[string]bool{}
	if v := os.Getenv("GOBIN"); v != "" {
		out[filepath.Clean(v)] = true
	}
	if v := os.Getenv("GOPATH"); v != "" {
		for _, p := range filepath.SplitList(v) {
			out[filepath.Join(p, "bin")] = true
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		out[filepath.Join(home, "go", "bin")] = true
		out[filepath.Join(home, ".local", "bin")] = true
	}
	return out
}

var ownerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$`)

// result is what one repository gave: what it holds and what could not be read.
type result struct {
	repo       string
	mains      []binnames.Main
	collisions []hit
	unread     []binnames.Unread
	note       string
}

type hit struct {
	main binnames.Main
	c    binnames.Collision
}

// one scans a directory or an owner/name.
func (ck *checker) one(arg string, b *binnames.Budget) result {
	if fi, err := os.Stat(arg); err == nil && fi.IsDir() {
		abs, _ := filepath.Abs(arg)
		return ck.scan(arg, binnames.DirTree{Root: abs},
			binnames.Options{RepoName: filepath.Base(abs)})
	}
	if !ownerName.MatchString(arg) {
		return result{repo: arg, unread: []binnames.Unread{{What: arg, Why: "neither a directory nor owner/name"}}}
	}
	return ck.remote(arg, "", b)
}

func (ck *checker) scan(name string, t binnames.Tree, opt binnames.Options) result {
	r := result{repo: name}
	mains, unread, err := binnames.Scan(t, opt)
	if err != nil {
		r.unread = append(r.unread, binnames.Unread{What: name, Why: err.Error()})
		return r
	}
	r.mains, r.unread = mains, unread
	for _, m := range mains {
		if c, bad := ck.idx.Check(m.Name, ck.allow); bad {
			r.collisions = append(r.collisions, hit{m, c})
		}
	}
	return r
}

// finish prints the results and returns the status.
func (ck *checker) finish(out io.Writer, res []result, list bool) error {
	fmt.Fprintf(out, "reference: %d names; %s\n", len(ck.idx), ck.pathLine)
	var mains, cols, unread int
	for _, r := range res {
		mains += len(r.mains)
		if list {
			for _, m := range r.mains {
				note := ""
				if m.Note != "" {
					note = " (" + m.Note + ")"
				}
				fmt.Fprintf(out, "main %s %s -> %s%s\n", r.repo, dirLabel(m.Dir), m.Name, note)
			}
		}
		for _, h := range r.collisions {
			cols++
			fmt.Fprintf(out, "COLLISION %s %s: binary %q collides with %s\n",
				r.repo, dirLabel(h.main.Dir), h.main.Name, h.c.Describe())
		}
		for _, u := range r.unread {
			unread++
			fmt.Fprintf(out, "UNREAD %s: %s\n", r.repo, u)
		}
	}
	for _, u := range ck.hostFails {
		fmt.Fprintf(out, "UNREAD PATH %s\n", u)
	}
	fmt.Fprintf(out, "%d repositories, %d main packages, %d collisions, %d unread (unread is not clean)\n",
		len(res), mains, cols, unread)
	switch {
	case cols > 0:
		return cli.FromStatus(1)
	case unread > 0 || len(ck.hostFails) > 0:
		return cli.Unanswerable("%d things could not be read; the repositories with an UNREAD line were not judged", unread+len(ck.hostFails))
	}
	return nil
}

func dirLabel(d string) string {
	if d == "" {
		return "."
	}
	return d
}
