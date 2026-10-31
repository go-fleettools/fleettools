package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// An Idiom is how one command reads its arguments.
//
// The four are kept apart because they call for different work: a cobra command
// is finished, a `flag` command that routes through fleet.ParseFlags answers -h
// correctly and still needs converting, a raw `flag` command needs both, and a
// command with no sign of either is the one that would run a fleet sweep on -h.
type Idiom string

const (
	// Cobra: built with cobra. Parses and answers -h by construction.
	Cobra Idiom = "cobra"

	// FlagHelped: a flag.FlagSet routed through fleet.ParseFlags or an
	// errors.Is(err, flag.ErrHelp) branch, so -h answers 0.
	FlagHelped Idiom = "flag+help"

	// FlagGlobal: the package-level flag.Parse(). The global set is
	// ExitOnError, which answers -h with 0 ITSELF, so these are not broken --
	// but they parse os.Args directly, so their work is not drivable from a
	// test, and the conversion has nowhere to hook a run() onto.
	FlagGlobal Idiom = "flag-global"

	// FlagRaw: a FlagSet with ContinueOnError and neither helper nor ErrHelp
	// branch. Here -h really does arrive as an error, which is the defect
	// fleet.ParseFlags exists for.
	FlagRaw Idiom = "flag-raw"

	// HandRolled: compares os.Args against "-h" itself.
	HandRolled Idiom = "hand -h"

	// Unread: no sign that the arguments are read at all. On 2026-10-11 a
	// command in this state started a sweep of every organisation on -h.
	Unread Idiom = "UNREAD"
)

// A Flag is one registration found in one command.
type Flag struct {
	Kind    string // String, Int, BoolVar, Var, ...
	Name    string // as the source spells it; "" when it is not a literal
	Default string // source text of the default
	Usage   string // source text of the usage argument
	Pos     string // file:line
}

// NeedsTwoDashes reports whether pflag will refuse this flag's name with a
// single dash: the measurement that decides how many call sites a migration
// moves. One letter is a shorthand and keeps working; two or more do not.
func (f Flag) NeedsTwoDashes() bool { return len(f.Name) > 1 }

// A Command is one cmd/<name> directory.
type Command struct {
	Name     string
	Idiom    Idiom
	Flags    []Flag
	Exits    int // os.Exit calls: each one is a status a migration must preserve
	HasUsage bool
	Funcs    []string // the entry points found: main, run, parseArgs, usage
}

// Breaking counts this command's flags that need two dashes.
func (c Command) Breaking() int {
	n := 0
	for _, f := range c.Flags {
		if f.NeedsTwoDashes() {
			n++
		}
	}
	return n
}

// A Census is what was read and what was set aside, reported next to the answer:
// a directory this could not parse must not be counted as a command with no
// flags.
type Census struct {
	Dirs       int
	Commands   int
	Unparsable []string
}

// flagMethods are the registration calls. The *Var forms take the destination
// first, which shifts every other argument by one -- getting that wrong is how a
// usage string is reported as a default.
var flagMethods = map[string]bool{
	"Bool": true, "Duration": true, "Float64": true, "Int": true, "Int64": true,
	"String": true, "Uint": true, "Uint64": true,
	"BoolVar": true, "DurationVar": true, "Float64Var": true, "IntVar": true,
	"Int64Var": true, "StringVar": true, "UintVar": true, "Uint64Var": true,
	"Var": true, "Func": true, "TextVar": true,
}

// Read walks a cmd/ directory and reports one Command per subdirectory that
// holds a main().
func Read(cmdDir string, only string) ([]Command, Census, error) {
	var c Census
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		return nil, c, err
	}
	var out []Command
	for _, e := range entries {
		if !e.IsDir() || (only != "" && e.Name() != only) {
			continue
		}
		c.Dirs++
		cmd, ok, err := readCommand(filepath.Join(cmdDir, e.Name()), e.Name())
		if err != nil {
			c.Unparsable = append(c.Unparsable, e.Name()+": "+err.Error())
			continue
		}
		if !ok {
			continue // a directory under cmd/ with no main() is not a command
		}
		c.Commands++
		out = append(out, cmd)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, c, nil
}

func readCommand(dir, name string) (Command, bool, error) {
	cmd := Command{Name: name}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, notATest, 0)
	if err != nil {
		return cmd, false, err
	}
	var all strings.Builder
	hasMain := false

	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			b, err := os.ReadFile(path)
			if err != nil {
				return cmd, false, err
			}
			all.Write(b)
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil {
					continue
				}
				if fn.Name.Name == "main" {
					hasMain = true
				}
				switch fn.Name.Name {
				case "main", "run", "parseArgs", "usage":
					cmd.Funcs = append(cmd.Funcs, fn.Name.Name)
				}
			}
			collect(&cmd, file, fset)
		}
	}
	if !hasMain {
		return cmd, false, nil
	}
	cmd.Idiom = classify(all.String())
	sort.Strings(cmd.Funcs)
	sort.Slice(cmd.Flags, func(i, j int) bool { return cmd.Flags[i].Name < cmd.Flags[j].Name })
	return cmd, true, nil
}

func notATest(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }

// classify names the idiom from the source text.
//
// ⛔ The order matters and cobra comes FIRST: a command mid-migration can hold
// both a cobra command and a leftover `flag` import, and what decides how it
// behaves is the one main() hands argv to.
// ⛔ And FlagGlobal is separated from FlagRaw because the first version of this
// said "-h arrives as an error" about both, which is FALSE for the global set:
// flag.Parse() uses flag.ExitOnError, which answers -h with 0 itself. Six
// commands were labelled with a defect they do not have.
func classify(src string) Idiom {
	switch {
	case strings.Contains(src, "cobra.Command"):
		return Cobra
	case strings.Contains(src, "ParseFlags") || strings.Contains(src, "flag.ErrHelp"):
		return FlagHelped
	case strings.Contains(src, "flag.Parse()"):
		return FlagGlobal
	case strings.Contains(src, ".Parse("):
		return FlagRaw
	case strings.Contains(src, `"-h"`) || strings.Contains(src, "IsHelpRequest"):
		return HandRolled
	default:
		return Unread
	}
}

func collect(cmd *Command, file *ast.File, fset *token.FileSet) {
	src := func(n ast.Node) string {
		var b strings.Builder
		_ = printer.Fprint(&b, fset, n)
		s := strings.ReplaceAll(b.String(), "\n", " ")
		if len(s) > 70 {
			s = s[:67] + "..."
		}
		return s
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if as, ok := n.(*ast.AssignStmt); ok {
			for _, lhs := range as.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "Usage" {
					cmd.HasUsage = true
				}
			}
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == "os" && sel.Sel.Name == "Exit" {
			cmd.Exits++
			return true
		}
		method := sel.Sel.Name
		if !flagMethods[method] {
			return true
		}
		// The receiver is not type-checked: `flag.String(...)` and
		// `fs.String(...)` are both registrations, and type information would
		// cost a full load for no gain. The cost is false positives, which the
		// filter below removes.
		if _, ok := sel.X.(*ast.Ident); !ok {
			return true
		}
		f := Flag{Kind: method}
		pos := fset.Position(call.Pos())
		f.Pos = fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line)
		args := call.Args
		if strings.HasSuffix(method, "Var") || method == "Func" {
			if len(args) > 0 {
				args = args[1:]
			}
		}
		if len(args) > 0 {
			f.Name = literal(args[0])
		}
		switch method {
		case "Var", "Func", "TextVar":
			if len(args) > 1 {
				f.Usage = src(args[len(args)-1])
			}
		default:
			if len(args) > 1 {
				f.Default = src(args[1])
			}
			if len(args) > 2 {
				f.Usage = src(args[2])
			}
		}
		// ⛔ A REGISTRATION HAS A NAME OR A USAGE STRING. Without this, every
		// `x.String()` in the command counted as a flag, because the receiver
		// is not type-checked: a first version reported 149 flags of which 36
		// were method calls on unrelated values, and a count that includes
		// those is a count about the wrong thing.
		if f.Name == "" && f.Usage == "" {
			return true
		}
		cmd.Flags = append(cmd.Flags, f)
		return true
	})
}

func literal(e ast.Expr) string {
	if b, ok := e.(*ast.BasicLit); ok && b.Kind == token.STRING {
		return strings.Trim(b.Value, "`\"")
	}
	return ""
}

// Funcs prints the entry-point functions of one command, for a person about to
// rewrite them.
//
// Reading thirty whole files to change thirty entry points is how the middle of
// a file gets edited by accident.
func Funcs(out io.Writer, dir, name string, want map[string]bool) error {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, notATest, parser.ParseComments)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\n═══════════════════ %s\n", name)
	found := 0
	for _, pkg := range pkgs {
		paths := make([]string, 0, len(pkg.Files))
		for p := range pkg.Files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			for _, decl := range pkg.Files[p].Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || !want[fn.Name.Name] {
					continue
				}
				found++
				fmt.Fprintf(out, "--- %s:%d\n", filepath.Base(p), fset.Position(fn.Pos()).Line)
				if err := printer.Fprint(out, fset, fn); err != nil {
					return err
				}
				fmt.Fprintln(out)
			}
		}
	}
	// A command whose entry points are named something else must SAY so rather
	// than come back empty, which reads as "nothing to migrate".
	if found == 0 {
		names := make([]string, 0, len(want))
		for k := range want {
			names = append(names, k)
		}
		sort.Strings(names)
		fmt.Fprintf(out, "(no function named %v -- read the file)\n", names)
	}
	return nil
}
