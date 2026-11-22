// Command listcheck asks whether a list of names written in a document still
// matches the tree it describes.
//
//	listcheck --tree cmd --marker abbench ../docs/content/en/repos/fleettools.md
//
// # WHY IT EXISTS
//
// ⛔ BECAUSE A COUNTED, NAMED INVENTORY WENT STALE BY ELEVEN AND NOTHING SAID
// SO. go-fleettools/docs said "102 directories under `cmd/`" and printed all
// 102 names in a fenced block, with the provenance of the count written beside
// it — `git ls-tree -d cmd/` on a named commit, and the arithmetic of how it
// grew from 64. Every part of that was true when it was written. The real count
// at `origin/main` on 2026-11-22 was 113: actionpin, archrun, ctxgate, dashfix,
// envcheck, ghadvisory, handlayout, orgfile, osvm, renovatedup and secretscan
// had landed, from other sessions, and no gate compares a page with a tree.
//
// ⛔ AND THE FIRST RECOUNT WAS WRONG IN THE OTHER DIRECTION. `binnames
// --no-path .` in the clone I happened to be standing in answered 112, because
// that BRANCH had forked before two of the eleven landed. The tree a count is
// taken from is part of the count, so this reads a directory you name and says
// which one it read — and `--at origin/main` REFUSES unless that directory's
// checkout is at the ref the count is about to be published as.
//
// The alternative was to transcribe 113 names by eye into a document, which is
// the operation this fleet has written down as a defect in four other shapes:
// a number read off one list and checked against another is not a measurement.
// So the list is compared by a program, and on a difference the program prints
// the tree's own names, ready to paste.
//
// # WHAT IT READS
//
// A fenced code block of a Markdown file, found by a name it CONTAINS rather
// than by its position or its info string: `--marker abbench` means "the block
// that holds the token abbench". A document has many fenced blocks and they get
// reordered; the first one, or the third ```text one, is a position that moves.
// It refuses when no block holds the marker, and when more than one does.
//
// The names in the block are its whitespace-separated tokens. The truth is the
// immediate SUBDIRECTORIES of --tree, which is what `cmd/` is; a file in there
// is not a command.
//
// Exit status: 0 the two agree; 1 they differ, or --count disagrees with the
// tree; 2 something could not be read, so the answer is unknown.
package main

import (
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

const longHelp = `listcheck compares the names written in a document with the directories that
exist, and is meant for the inventory a page keeps of a repository: "113
directories under cmd/", followed by all 113 names.

  listcheck --tree cmd --marker abbench docs/repos/fleettools.md

--marker names a token the block CONTAINS. That is deliberate: a document has
many fenced blocks, and "the first block" or "the second text block" is a
position that moves when someone adds an example above it. If no block holds
the marker, or more than one does, nothing is compared and the exit status is
2 — an unread list must never read as an agreeing one.

The names are the whitespace-separated tokens of the block. The truth is the
immediate subdirectories of --tree.

--block LANG narrows the search to the fenced blocks whose info string is LANG,
and the case that needs it is the honest one: a page that DOCUMENTS this command
holds the marker twice, once in the list and once in the example, so the two are
told apart by kind ("text" against "bash") and not by position. The refusal names
the kinds it found, so the flag to add is in the message.

--at REF refuses unless the tree's checkout is at REF, and that refusal is the
one earned the hard way: the first recount of this very inventory was taken in
whatever clone happened to be at hand, whose branch had forked before two
commands landed, and answered 112 where origin/main held 113. Nothing in that
run was wrong except the tree, and nothing said which tree it was. A count is
published as a fact about a ref, so name the ref: --at origin/main. A tree
outside a repository, or a ref that does not resolve, is exit 2.

--count N asserts that the tree holds exactly N entries, so a number written
in the prose beside the block is checked too, and not only the block itself.
A count that is right while the list is wrong, or the other way round, is the
ordinary way one of these pages rots.

--skip NAME leaves a directory out of the comparison, for a tree that holds
something the page is right not to list.

⛔ On a difference it prints the TREE's names, wrapped, so the fix is a paste
and not a transcription. Transcribing a hundred names by eye is how the
numbers in these pages went wrong in the first place.

Exit status: 0 the two agree; 1 they differ; 2 something could not be read.`

type opts struct {
	tree   string
	marker string
	block  string
	at     string
	count  int
	skip   []string
	width  int
}

// gitHEAD is replaceable in tests.
var gitHEAD = func(dir, rev string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", rev).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// atRef refuses when the tree is not checked out at the ref the caller means.
//
// ⛔ This is the gate for the defect that produced the wrong number first: the
// count was taken in the clone I happened to be standing in, whose BRANCH had
// forked before two of the commands landed, and answered 112 where origin/main
// held 113. Nothing in that run was wrong except the tree, and nothing said
// which tree it was. A directory cannot say what it is not asked, so --at makes
// the caller name the ref and compares it with HEAD.
func atRef(dir, ref string) error {
	head, err := gitHEAD(dir, "HEAD")
	if err != nil {
		return cli.Unanswerable("--at %s: %s is not in a git repository, so which tree this is cannot be read: %v", ref, dir, err)
	}
	want, err := gitHEAD(dir, ref)
	if err != nil {
		return cli.Unanswerable("--at %s: that ref cannot be resolved in %s (fetch it first): %v", ref, dir, err)
	}
	if head != want {
		return cli.Unanswerable("--at %s resolves to %s but the tree is at %s: a count taken from the wrong tree is a wrong count, and this one would have been read as a fact about %s", ref, short(want), short(head), ref)
	}
	return nil
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func rootCmd() *cobra.Command {
	var o opts
	c := cli.New("listcheck [flags] FILE...", "Does a list of names in a document still match the tree?", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cli.Unanswerable("name at least one Markdown file to read")
		}
		if o.tree == "" {
			return cli.Unanswerable("--tree is required: name the directory whose subdirectories the list describes")
		}
		if o.marker == "" {
			return cli.Unanswerable("--marker is required: name one token the list contains, so the block is found by what it holds and not by where it sits")
		}
		return run(cmd.OutOrStdout(), o, args)
	}
	c.Flags().StringVar(&o.tree, "tree", "", "the directory whose immediate subdirectories the list describes")
	c.Flags().StringVar(&o.marker, "marker", "", "a token the fenced block contains, which is how the block is found")
	c.Flags().StringVar(&o.block, "block", "", "only consider fenced blocks whose info string is this (default: any)")
	c.Flags().StringVar(&o.at, "at", "", "refuse unless the tree's checkout is at this ref (e.g. origin/main)")
	c.Flags().IntVar(&o.count, "count", -1, "assert the tree holds exactly this many entries (-1: do not check)")
	c.Flags().StringSliceVar(&o.skip, "skip", nil, "a directory to leave out of the comparison (repeatable)")
	c.Flags().IntVar(&o.width, "width", 88, "wrap the printed list at this many columns")
	return c
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(rootCmd())
}

func run(out io.Writer, o opts, files []string) error {
	if o.at != "" {
		if err := atRef(o.tree, o.at); err != nil {
			return err
		}
		head, _ := gitHEAD(o.tree, "HEAD")
		fmt.Fprintf(out, "%s: the checkout is at %s == %s\n", o.tree, o.at, short(head))
	}
	have, err := subdirs(o.tree)
	if err != nil {
		return cli.Unanswerable("--tree %s: %v", o.tree, err)
	}
	skip := map[string]bool{}
	for _, s := range o.skip {
		skip[s] = true
	}
	var tree []string
	for _, n := range have {
		if !skip[n] {
			tree = append(tree, n)
		}
	}
	sort.Strings(tree)

	fmt.Fprintf(out, "%s: %d subdirector%s", o.tree, len(tree), plural(len(tree)))
	if len(o.skip) > 0 {
		fmt.Fprintf(out, " (%d skipped)", len(have)-len(tree))
	}
	fmt.Fprintln(out)

	findings := 0
	if o.count >= 0 && o.count != len(tree) {
		findings++
		fmt.Fprintf(out, "⛔ --count %d, but the tree holds %d\n", o.count, len(tree))
	}

	for _, f := range files {
		n, err := compare(out, f, o, tree)
		if err != nil {
			return err
		}
		findings += n
	}
	if findings > 0 {
		fmt.Fprintf(out, "\nthe tree's own names, to paste:\n\n%s\n", wrap(tree, o.width))
		return cli.Found("%d difference(s) between what is written and what exists", findings)
	}
	fmt.Fprintf(out, "every name agrees, in %d file(s)\n", len(files))
	return nil
}

// compare reads one document and reports what it says that the tree does not,
// and the other way round.
func compare(out io.Writer, file string, o opts, tree []string) (int, error) {
	src, err := os.ReadFile(file)
	if err != nil {
		return 0, cli.Unanswerable("%s: %v", file, err)
	}
	all := fenced(string(src))
	var blocks, held []block
	for _, b := range all {
		if o.block != "" && b.lang != o.block {
			continue
		}
		blocks = append(blocks, b)
		for _, tok := range b.tokens {
			if tok == o.marker {
				held = append(held, b)
				break
			}
		}
	}
	of := ""
	if o.block != "" {
		of = fmt.Sprintf(" of %d `%s` block(s), out of %d in all", len(blocks), o.block, len(all))
	}
	switch len(held) {
	case 1:
	case 0:
		return 0, cli.Unanswerable("%s: no fenced block holds the token %q, so nothing was compared (%d block(s) read%s)", file, o.marker, len(blocks), of)
	default:
		langs := make([]string, 0, len(held))
		for _, b := range held {
			langs = append(langs, "`"+b.lang+"`")
		}
		return 0, cli.Unanswerable("%s: %d fenced blocks hold the token %q (%s), so which one is the list is unknown — name the kind with --block", file, len(held), o.marker, strings.Join(langs, ", "))
	}

	inDoc := map[string]bool{}
	for _, tok := range held[0].tokens {
		inDoc[tok] = true
	}
	inTree := map[string]bool{}
	for _, n := range tree {
		inTree[n] = true
	}

	var missing, extra []string
	for _, n := range tree {
		if !inDoc[n] {
			missing = append(missing, n)
		}
	}
	for n := range inDoc {
		if !inTree[n] {
			extra = append(extra, n)
		}
	}
	sort.Strings(extra)

	fmt.Fprintf(out, "%s: %d name(s) listed\n", file, len(inDoc))
	if len(missing) > 0 {
		fmt.Fprintf(out, "⛔ in the tree, NOT written down (%d): %s\n", len(missing), strings.Join(missing, " "))
	}
	if len(extra) > 0 {
		fmt.Fprintf(out, "⛔ written down, NOT in the tree (%d): %s\n", len(extra), strings.Join(extra, " "))
	}
	return len(missing) + len(extra), nil
}

// subdirs is the immediate subdirectories of dir, by name.
func subdirs(dir string) ([]string, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, de := range des {
		if de.IsDir() {
			out = append(out, filepath.Base(de.Name()))
		}
	}
	return out, nil
}

// A block is one fenced code block: the info string of its opening fence, and
// the whitespace-separated tokens of its content.
type block struct {
	lang   string
	tokens []string
}

// fenced is every ``` block of a Markdown document. The fence's own line is not
// a token of it, and an unterminated block at the end of the file is still a
// block: its content was written to be read.
func fenced(src string) []block {
	var blocks []block
	var cur strings.Builder
	lang := ""
	in := false
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "```") {
			if in {
				blocks = append(blocks, block{lang, strings.Fields(cur.String())})
				cur.Reset()
				in = false
				continue
			}
			// The info string is the first word after the fence: ```text, and
			// ```go {linenos=true} is a `go` block.
			lang = firstWord(strings.TrimLeft(trimmed, "`"))
			in = true
			continue
		}
		if in {
			cur.WriteString(line)
			cur.WriteByte('\n')
		}
	}
	if in && strings.TrimSpace(cur.String()) != "" {
		blocks = append(blocks, block{lang, strings.Fields(cur.String())})
	}
	return blocks
}

func firstWord(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// wrap joins names into lines no longer than width, so the output can be pasted
// into a document as it stands.
func wrap(names []string, width int) string {
	if width < 1 {
		width = 1
	}
	var b strings.Builder
	col := 0
	for i, n := range names {
		if col > 0 && col+1+len(n) > width {
			b.WriteByte('\n')
			col = 0
		} else if i > 0 {
			b.WriteByte(' ')
			col++
		}
		b.WriteString(n)
		col += len(n)
	}
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
