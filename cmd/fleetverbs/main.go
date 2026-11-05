// Command fleetverbs asks a Go repository what it DOES:
//
//	fleetverbs [flags] <directory>...
//
// ⛔ IT EXISTS BECAUSE FOUR INVENTORIES IN ONE MORNING WERE WRONG, each the
// same way. An inventory of what this fleet can do was revised four times on
// 2026-11-05, and every revision had the same cause: something was consulted
// that is ABOUT the code — a repository's one-line description, an
// application's ribbon of commands, a dependency one layer down, a set of
// notes — instead of the code that performs the capability. The count of
// capabilities believed missing fell from 58 to 37 without a line being
// written. go-pdfkit/convert, whose description says "pictures, SVG and comic
// archives", also turns XPS into PDF and redraws a page inverted, greyscale,
// re-backgrounded or scanner-damaged.
//
// A one-line description is a summary written once, usually at the first
// commit, and it ages with every feature added. What a package does is in its
// exported API and its command table, and this reads those.
//
// ⛔ AND IT REPORTS WHAT IT COULD NOT READ. A sweep that cannot parse half its
// input and prints the half it could is indistinguishable from a sweep that
// found nothing there, which is how an inventory comes to be confidently
// short. Every directory with no Go files, every file that would not parse and
// every package skipped is counted and named.
package main

import (
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Reads the Go source under each directory and prints what each package
exports, with the first line of each symbol's own documentation, plus the
verbs of any cobra command it defines.

    fleetverbs ~/src/go-pdfkit-convert
    fleetverbs --match xps ~/src/go-pdfkit-*
    fleetverbs --match psd --text ~/src/go-pdfkit-convert
    fleetverbs --cli ~/src/go-fleettools-fleettools

--match keeps only the symbols whose NAME or whose documentation mentions the
word, which is the question this answers: does anything here already do X?

--text asks a WEAKER question as well: does the word appear in the source at
all? A format handled through a lookup table has no exported symbol named
after it — go-pdfkit/convert reads PSD and says so nowhere in its API — so the
answer to the first question is honestly "no" and the answer to the second is
"codec.go, go and look". The two are printed apart and labelled, because a
word in the source is somewhere to look and not a capability.

Nothing is fetched and nothing is built: it parses the files on disk, so it
works on a clone with no dependencies downloaded.

Exit status: 0 something was read, 1 nothing was, 2 a usage error.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	var o options
	c := cli.New("fleetverbs [flags] <directory>...",
		"What a Go repository exports and what its commands are", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return cli.FromStatus(report(cli.Out(cmd), cli.Err(cmd), o, args))
	}
	f := c.Flags()
	f.StringVar(&o.match, "match", "", "keep only symbols whose name or documentation mentions this")
	f.BoolVar(&o.text, "text", false, "also say where the word appears in the source, which is weaker")
	f.BoolVar(&o.cliOnly, "cli", false, "print only the cobra verbs, not the exported API")
	f.BoolVar(&o.quiet, "quiet", false, "do not print the tally of what was read and skipped")
	return c
}

type options struct {
	match   string
	text    bool
	cliOnly bool
	quiet   bool
}

// textHits is how many source lines are named per package before the rest are
// only counted. A word that appears forty times is a word to go and look at,
// not a list to read here.
const textHits = 5

// run keeps the signature this command's tests are written against.
func run(args []string, out, errOut io.Writer) int {
	return cli.Drive(RootCmd(), args, out, errOut)
}

// A tally is what was read and what was not, which is printed whether or not
// anything was found.
type tally struct {
	dirs     int
	packages int
	files    int
	symbols  int
	verbs    int
	mentions int
	// skipped names what could not be read, with the reason.
	skipped []string
}

func report(out, errOut io.Writer, o options, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, longHelp)
		return 2
	}
	var t tally
	for _, root := range args {
		readTree(out, root, o, &t)
	}
	if !o.quiet {
		fmt.Fprintf(out, "\n%d director(ies), %d package(s), %d file(s): %d exported symbol(s), %d verb(s)",
			t.dirs, t.packages, t.files, t.symbols, t.verbs)
		if o.text {
			fmt.Fprintf(out, ", %d source mention(s)", t.mentions)
		}
		fmt.Fprintln(out)
		for _, s := range t.skipped {
			fmt.Fprintf(out, "  not read: %s\n", s)
		}
	}
	if t.packages == 0 {
		fmt.Fprintln(errOut, "fleetverbs: nothing was read")
		return 1
	}
	return 0
}

// readTree walks one directory and prints each package under it.
func readTree(out io.Writer, root string, o options, t *tally) {
	t.dirs++
	byDir := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			t.skipped = append(t.skipped, fmt.Sprintf("%s: %v", path, err))
			return nil
		}
		if d.IsDir() {
			// ⛔ testdata is skipped BY NAME and counted, because it holds Go
			// files that are fixtures rather than code, and a package built
			// out of broken fixtures would be reported as a package.
			if name := d.Name(); name == "vendor" || name == "testdata" ||
				(strings.HasPrefix(name, ".") && name != "." && len(name) > 1) {
				t.skipped = append(t.skipped, path+": not source")
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		dir := filepath.Dir(path)
		byDir[dir] = append(byDir[dir], path)
		return nil
	})
	if err != nil {
		t.skipped = append(t.skipped, fmt.Sprintf("%s: %v", root, err))
		return
	}

	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		readPackage(out, dir, byDir[dir], o, t)
	}
}

// readPackage parses one directory's files and prints what it exports.
func readPackage(out io.Writer, dir string, paths []string, o options, t *tally) {
	fset := token.NewFileSet()
	var files []*ast.File
	sort.Strings(paths)
	for _, p := range paths {
		f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if err != nil {
			// ⛔ Named, not swallowed. A file that will not parse is the one
			// most likely to hold the thing being looked for.
			t.skipped = append(t.skipped, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		files = append(files, f)
		t.files++
	}
	if len(files) == 0 {
		return
	}
	// ⛔⛔ THE VERBS ARE READ FIRST, AND THE ORDER IS THE WHOLE FIX. go/doc's
	// own documentation says NewFromFiles "takes ownership of the AST files and
	// may edit them", and what it edits away includes every UNEXPORTED
	// declaration — which is where a command's cobra literal usually sits,
	// since the function that builds a command is rarely exported. Read after
	// it, the verbs come back empty for a package that plainly has them, and
	// nothing says why: the files parse, the packages are counted, and the
	// answer is a confident zero.
	//
	// It was missed because the first command tested happened to build its
	// command in an EXPORTED function, so the one case that worked was the one
	// case go/doc kept. PreserveAST stays as well, so that nothing later in
	// this function is reading a tree somebody else has edited.
	// ⛔⛔ THE VERBS ARE READ FIRST, AND THE ORDER IS THE WHOLE FIX. go/doc's
	// own documentation says NewFromFiles "takes ownership of the AST files and
	// may edit them", and what it edits away includes every UNEXPORTED
	// declaration — which is where a command's cobra literal usually sits,
	// since the function that builds a command is rarely exported. Read after
	// it, the verbs come back empty for a package that plainly has them, and
	// nothing says why: the files parse, the packages are counted, and the
	// answer is a confident zero.
	//
	// ⛔ It survived the first round of testing because the one command tried
	// by hand, pagetext, builds its command in an EXPORTED function — so the
	// single case that worked was the single case go/doc keeps. PreserveAST
	// stays as well, so nothing below reads a tree somebody else has edited.
	verbs := verbsOf(files, o)

	d, err := doc.NewFromFiles(fset, files, dir, doc.PreserveAST)
	if err != nil {
		t.skipped = append(t.skipped, fmt.Sprintf("%s: %v", dir, err))
		return
	}
	t.packages++

	lines := symbolsOf(fset, d, o)
	mentions, total := mentionsOf(paths, o)
	t.symbols += countKind(lines)
	t.verbs += len(verbs)
	t.mentions += total
	if len(lines) == 0 && len(verbs) == 0 && total == 0 {
		return
	}

	fmt.Fprintf(out, "\n== %s (package %s) ==\n", dir, d.Name)
	for _, v := range verbs {
		fmt.Fprintf(out, "  verb %s\n", v)
	}
	if !o.cliOnly {
		for _, l := range lines {
			fmt.Fprintln(out, l)
		}
	}
	if total == 0 {
		return
	}
	// ⛔ Printed APART from the symbols and labelled, because the two answer
	// different questions. A word in the source says where to look; it does
	// not say the package does the thing.
	if len(lines) == 0 {
		fmt.Fprintf(out, "  (nothing in the API is named for it; the source mentions it %d time(s))\n", total)
	} else {
		fmt.Fprintf(out, "  (the source also mentions it %d time(s))\n", total)
	}
	for _, m := range mentions {
		fmt.Fprintf(out, "    text %s\n", m)
	}
	if total > len(mentions) {
		fmt.Fprintf(out, "    text … and %d more\n", total-len(mentions))
	}
}

// mentionsOf is where the word appears in a package's source, and how often.
func mentionsOf(paths []string, o options) (named []string, total int) {
	if !o.text || o.match == "" {
		return nil, 0
	}
	want := strings.ToLower(o.match)
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(b), "\n") {
			if !strings.Contains(strings.ToLower(line), want) {
				continue
			}
			total++
			if len(named) < textHits {
				named = append(named, fmt.Sprintf("%s:%d %s",
					filepath.Base(p), i+1, strings.TrimSpace(line)))
			}
		}
	}
	return named, total
}

// countKind counts the symbol lines, which are the ones not indented as
// documentation.
func countKind(lines []string) int {
	n := 0
	for _, l := range lines {
		if !strings.HasPrefix(l, "      ") {
			n++
		}
	}
	return n
}

// symbolsOf is every exported func and type of a package, each followed by the
// first line of its own documentation.
func symbolsOf(fset *token.FileSet, d *doc.Package, o options) []string {
	if o.cliOnly {
		return nil
	}
	var out []string
	add := func(sig, docText string) {
		first := d.Synopsis(docText)
		if !keeps(o.match, sig, first) {
			return
		}
		out = append(out, "  "+sig)
		if first != "" {
			out = append(out, "      "+first)
		}
	}
	for _, f := range d.Funcs {
		add(signature(fset, f.Decl), f.Doc)
	}
	for _, ty := range d.Types {
		add("type "+ty.Name, ty.Doc)
		for _, f := range ty.Funcs {
			add(signature(fset, f.Decl), f.Doc)
		}
		for _, m := range ty.Methods {
			add(signature(fset, m.Decl), m.Doc)
		}
	}
	return out
}

// keeps says whether a symbol survives -match.
func keeps(match, sig, synopsis string) bool {
	if match == "" {
		return true
	}
	m := strings.ToLower(match)
	return strings.Contains(strings.ToLower(sig), m) ||
		strings.Contains(strings.ToLower(synopsis), m)
}

// signature prints a declaration without its body, the way go doc does.
func signature(fset *token.FileSet, decl *ast.FuncDecl) string {
	if decl == nil {
		return "func ?"
	}
	shown := *decl
	shown.Body = nil
	shown.Doc = nil
	var b strings.Builder
	if err := (&printer.Config{Mode: printer.UseSpaces, Tabwidth: 4}).Fprint(&b, fset, &shown); err != nil {
		// Printing a declaration that already parsed cannot fail, and saying
		// so beats printing nothing where a signature should be.
		return "func " + decl.Name.Name + " (cannot be printed: " + err.Error() + ")"
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// verbsOf is every cobra command the package defines, by the words a person
// types and what the command says it does.
//
// ⛔ COBRA AND NOTHING ELSE, deliberately. This fleet's rule is that a CLI is
// built from cobra, so a command table in any other shape is a repository that
// has drifted — and a tool that quietly understood five homegrown table shapes
// would hide that drift instead of showing it. What it does not recognise, it
// does not count, and the tally says how much was read.
func verbsOf(files []*ast.File, o options) []string {
	var out []string
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			use, short, ok := commandOf(n)
			if !ok {
				return true
			}
			line := use
			if short != "" {
				line += " — " + short
			}
			if keeps(o.match, use, short) {
				out = append(out, line)
			}
			return true
		})
	}
	sort.Strings(out)
	return out
}

// commandOf reads a command out of one node, in either of the two shapes this
// fleet writes them.
//
// ⛔ THE SECOND SHAPE IS WHY pagetext ANSWERED NOTHING. Every command in
// go-fleettools is built by cli.New(use, short, long), whose own cobra literal
// sets `Use: use` — an IDENTIFIER, not a string — so a reader that only
// understands literals finds the fleet's own commands invisible and says so
// with a confident zero. The words a person types are at the CALL SITE.
func commandOf(n ast.Node) (use, short string, ok bool) {
	switch v := n.(type) {
	case *ast.CompositeLit:
		if !isCobraCommand(v.Type) {
			return "", "", false
		}
		use, short = fieldString(v, "Use"), fieldString(v, "Short")
	case *ast.CallExpr:
		if !isCommandConstructor(v.Fun) || len(v.Args) < 2 {
			return "", "", false
		}
		use, short = stringOf(v.Args[0]), stringOf(v.Args[1])
	default:
		return "", "", false
	}
	return use, short, use != ""
}

// isCommandConstructor matches cli.New, the constructor this fleet's commands
// are built with. It is matched by name rather than by resolved type, because
// nothing here type-checks: a package is parsed, not built, so that a clone
// with no dependencies downloaded still answers.
func isCommandConstructor(fun ast.Expr) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "New" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "cli"
}

// stringOf reads a string literal, or "" for anything else.
func stringOf(e ast.Expr) string {
	s, ok := e.(*ast.BasicLit)
	if !ok || s.Kind != token.STRING {
		return ""
	}
	return strings.Trim(s.Value, "`\"")
}

// isCobraCommand says whether a composite literal is a cobra.Command.
func isCobraCommand(t ast.Expr) bool {
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	sel, ok := t.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Command" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "cobra"
}

// fieldString reads one string-literal field out of a composite literal.
func fieldString(lit *ast.CompositeLit, name string) string {
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != name {
			continue
		}
		return stringOf(kv.Value)
	}
	return ""
}
