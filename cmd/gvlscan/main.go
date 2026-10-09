// gvlscan reports calls that BLOCK the operating-system thread but are not
// wrapped in a call that releases the interpreter lock first.
//
// It exists because grep cannot answer the question. In a pure-Go Ruby VM the
// dangerous call and the harmless one are spelled identically:
//
//	conn.Read(buf)   // net.Conn   -- blocks until a peer sends, maybe forever
//	buf.Read(p)      // *bytes.Reader -- returns immediately, always
//
// Both are `.Read(`. Telling them apart needs the type of the receiver, so this
// loads real type information rather than matching text. That difference is not
// academic: go-embedded-ruby#771 was an unbounded hang -- a Ruby program that
// talked to a server in one of its own threads never returned, and the embedder
// calling ruby.Run never got control back -- caused by exactly one unwrapped
// net.Conn read in a file that otherwise wrapped all of them.
//
// WHAT IT CAN AND CANNOT SEE. Enclosure is LEXICAL: a blocking call counts as
// wrapped when a wrapper call encloses it in the same function body, which is
// the shape these wrappers are used in:
//
//	ioBlock(vm, func() { n, err = conn.Read(buf) })
//
// A blocking call sitting in a helper that is only ever reached from inside a
// wrapper is reported anyway. That is a false positive, and the tool says so by
// printing the enclosing function, so the reader can follow the callers. It is
// the safe direction to be wrong in: the other one would be silence about a
// real hang. -callers prints, for each function holding an unwrapped call, the
// places that call it, which is usually enough to settle it without reading the
// file.
//
// Exit status: 0 when nothing is unwrapped, 1 when something is, 2 when the
// packages could not be loaded -- which is a refusal to answer, not an answer.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

// blocker names one call shape that can park the thread.
//
// A method is named by its receiver type and method name, written as it appears
// in Go source: "net.Conn.Read". Interface and pointer forms are both matched
// against the receiver's static type, because that is what a caller actually
// has in hand.
//
// A package-level function is named by its import path and name: "time.Sleep".
type blocker struct {
	recv string // qualified receiver type, empty for a plain function
	fn   string // method or function name
	why  string // what it waits for, printed with the finding
	kind string // net, file, proc, sync, time -- see -kinds
}

// defaultBlockers is the set a Ruby VM can reach from ordinary Ruby code. Each
// entry waits on something the program does not control -- a peer, a disk, a
// clock, a child process -- which is what makes it a thread-parking call rather
// than a slow one.
// The kind is what makes the output usable. A first run against a real VM
// returned 29 unwrapped calls, twelve of them short internal sync.Mutex locks
// that have nothing to do with the question -- and a list where the four
// unbounded network waits sit among a dozen mutexes is a list nobody acts on.
// -kinds names which ones are being asked about, so the count in the census
// line is a count of the thing under discussion.
var defaultBlockers = []blocker{
	{"net.Conn", "Read", "a peer that may never send", "net"},
	{"net.Conn", "Write", "a peer that may never read", "net"},
	{"net.Listener", "Accept", "a client that may never connect", "net"},
	{"net.PacketConn", "ReadFrom", "a datagram that may never arrive", "net"},
	{"net.PacketConn", "WriteTo", "a socket buffer that may be full", "net"},
	{"net.TCPConn", "Read", "a peer that may never send", "net"},
	{"net.TCPConn", "Write", "a peer that may never read", "net"},
	{"net.TCPListener", "Accept", "a client that may never connect", "net"},
	{"net.UDPConn", "ReadFrom", "a datagram that may never arrive", "net"},
	{"net.UDPConn", "ReadFromUDP", "a datagram that may never arrive", "net"},
	{"net.UnixConn", "Read", "a peer that may never send", "net"},
	{"", "net.Dial", "a DNS server and a peer", "net"},
	{"", "net.DialTimeout", "a DNS server and a peer", "net"},
	{"", "net.Listen", "the kernel", "net"},
	{"", "net.ResolveTCPAddr", "a DNS server", "net"},
	{"", "net.LookupHost", "a DNS server", "net"},
	{"", "net.LookupIP", "a DNS server", "net"},
	{"os.File", "Read", "a device, pipe or slow filesystem", "file"},
	{"os.File", "Write", "a device, pipe or full filesystem", "file"},
	{"exec.Cmd", "Run", "a child process", "proc"},
	{"exec.Cmd", "Wait", "a child process", "proc"},
	{"exec.Cmd", "Output", "a child process", "proc"},
	{"exec.Cmd", "CombinedOutput", "a child process", "proc"},
	{"sync.WaitGroup", "Wait", "another goroutine", "sync"},
	{"sync.Mutex", "Lock", "another goroutine", "sync"},
	{"sync.RWMutex", "Lock", "another goroutine", "sync"},
	{"", "time.Sleep", "the clock", "time"},
}

type finding struct {
	pos      token.Position
	call     string
	why      string
	enclFunc string
	pkg      string
}

func main() {
	var (
		wrappers = flag.String("wrappers", "ioBlock,threadBlock,threadPass",
			"comma-separated function names that release the lock; a blocking call lexically inside one of these is not reported")
		extra = flag.String("also", "",
			"extra blocking calls, comma-separated, as Type.Method or pkg.Func (e.g. \"sql.DB.Query,os.Hostname\")")
		kinds = flag.String("kinds", "net,file,proc,time",
			"which kinds of waiting to report: net, file, proc, sync, time. sync is off by default -- a short internal mutex is not what a thread-parking audit is about, and a dozen of them bury the findings that are")
		showCallers = flag.Bool("callers", false,
			"for each function holding an unwrapped call, also print who calls it -- enclosure is lexical, so this is how a false positive is settled")
		quiet = flag.Bool("q", false, "print only the counts")
	)
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, `gvlscan -- find blocking calls that do not release the interpreter lock.

usage: gvlscan [flags] <package pattern>...

	gvlscan ./internal/vm
	gvlscan -callers ./internal/...

Exit status: 0 nothing unwrapped, 1 something unwrapped, 2 could not load.
A load failure is a REFUSAL TO ANSWER, not a clean result.

flags:
`)
		flag.PrintDefaults()
	}
	flag.Parse()
	// A stale scanner is worse here than elsewhere: its answer is a list of
	// places nobody has to look at, and an old binary's list is shorter.
	fleet.WarnIfStale(os.Stderr)
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}

	want := map[string]bool{}
	for _, k := range splitList(*kinds) {
		want[k] = true
	}
	var blockers []blocker
	for _, b := range defaultBlockers {
		if want[b.kind] {
			blockers = append(blockers, b)
		}
	}
	// A -kinds that selects nothing would scan a whole tree and print a
	// confident zero. Refuse: an empty question is not a clean answer.
	if len(blockers) == 0 {
		fmt.Fprintf(os.Stderr, "gvlscan: -kinds %q selects no blocking call at all; nothing would be reported\n", *kinds)
		os.Exit(2)
	}
	for _, s := range splitList(*extra) {
		b, err := parseBlocker(s)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gvlscan: -also %q: %v\n", s, err)
			os.Exit(2)
		}
		blockers = append(blockers, b)
	}
	wrapperSet := map[string]bool{}
	for _, w := range splitList(*wrappers) {
		wrapperSet[w] = true
	}

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps |
			packages.NeedImports,
		Tests: false,
	}
	pkgs, err := packages.Load(cfg, flag.Args()...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gvlscan: load: %v\n", err)
		os.Exit(2)
	}
	// A package that did not type-check gives wrong answers quietly: its
	// selections resolve to nothing, so every blocking call in it simply
	// disappears and the tool reports a reassuring zero. Refuse instead.
	nerr := 0
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			if nerr < 10 {
				fmt.Fprintf(os.Stderr, "gvlscan: %v\n", e)
			}
			nerr++
		}
	})
	if nerr > 0 {
		fmt.Fprintf(os.Stderr, "gvlscan: %d type error(s); refusing to report, because an unparsed file yields no findings and looks like a clean one\n", nerr)
		os.Exit(2)
	}
	if len(pkgs) == 0 {
		fmt.Fprintln(os.Stderr, "gvlscan: no packages matched")
		os.Exit(2)
	}

	var found, wrapped []finding
	scanned := 0
	for _, p := range pkgs {
		if len(p.Syntax) == 0 {
			continue
		}
		scanned++
		f, w := scanPackage(p, blockers, wrapperSet)
		found = append(found, f...)
		wrapped = append(wrapped, w...)
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].pos.Filename != found[j].pos.Filename {
			return found[i].pos.Filename < found[j].pos.Filename
		}
		return found[i].pos.Line < found[j].pos.Line
	})

	// "N of M", never N alone: a count of findings with no population behind it
	// cannot be told apart from a scan that read nothing.
	fmt.Printf("%d package(s) scanned, %d blocking call(s) found, %d wrapped, %d NOT wrapped\n",
		scanned, len(found)+len(wrapped), len(wrapped), len(found))
	if *quiet {
		if len(found) > 0 {
			os.Exit(1)
		}
		return
	}
	if len(found) == 0 {
		return
	}

	fmt.Println()
	byFunc := map[string][]finding{}
	for _, f := range found {
		fmt.Printf("%s:%d: %s waits on %s\n", f.pos.Filename, f.pos.Line, f.call, f.why)
		fmt.Printf("    in %s\n", f.enclFunc)
		byFunc[f.pkg+"."+f.enclFunc] = append(byFunc[f.pkg+"."+f.enclFunc], f)
	}

	if *showCallers {
		fmt.Println("\n-- who calls the functions holding an unwrapped call --")
		fmt.Println("   (enclosure is lexical: a call reached only from inside a wrapper is")
		fmt.Println("    reported above but is not a defect. These callers settle which.)")
		names := map[string]bool{}
		for _, f := range found {
			names[f.enclFunc] = true
		}
		printCallers(pkgs, names)
	}
	os.Exit(1)
}

// scanPackage walks every function body and classifies each blocking call by
// whether a wrapper call encloses it.
func scanPackage(p *packages.Package, blockers []blocker, wrappers map[string]bool) (unwrapped, wrapped []finding) {
	for _, file := range p.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			decl, ok := n.(*ast.FuncDecl)
			if !ok || decl.Body == nil {
				return true
			}
			// stack of wrapper calls currently open around the cursor
			depth := 0
			var walk func(ast.Node) bool
			walk = func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if isWrapperCall(call, wrappers) {
					depth++
					for _, a := range call.Args {
						ast.Inspect(a, walk)
					}
					depth--
					return false
				}
				if b, name := matchBlocker(p.TypesInfo, call, blockers); b != nil {
					f := finding{
						pos:      p.Fset.Position(call.Pos()),
						call:     name,
						why:      b.why,
						enclFunc: funcName(decl),
						pkg:      p.PkgPath,
					}
					if depth > 0 {
						wrapped = append(wrapped, f)
					} else {
						unwrapped = append(unwrapped, f)
					}
				}
				return true
			}
			ast.Inspect(decl.Body, walk)
			return false // the decl's body is fully walked above
		})
	}
	return unwrapped, wrapped
}

// isWrapperCall reports whether call is a call to one of the lock-releasing
// functions, by the name written at the call site: both `ioBlock(...)` and
// `vm.threadBlock(...)` are matched on the final identifier, since that is how
// they are spelled and a method of the same name on an unrelated type would be
// a genuine ambiguity worth reading rather than silently splitting.
func isWrapperCall(call *ast.CallExpr, wrappers map[string]bool) bool {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return wrappers[f.Name]
	case *ast.SelectorExpr:
		return wrappers[f.Sel.Name]
	}
	return false
}

// matchBlocker resolves the callee through type information and reports which
// blocker it is, if any. Resolution is what separates this from grep: a method
// is matched on the RECEIVER'S TYPE, so conn.Read and bytes.Buffer.Read do not
// collide.
func matchBlocker(info *types.Info, call *ast.CallExpr, blockers []blocker) (*blocker, string) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		// A plain identifier call cannot be one of the qualified functions.
		return nil, ""
	}
	name := sel.Sel.Name

	// pkg.Func(...) -- the selector's X is a package name.
	if id, ok := sel.X.(*ast.Ident); ok {
		if pkgName, ok := info.Uses[id].(*types.PkgName); ok {
			qual := pkgName.Imported().Name() + "." + name
			for i := range blockers {
				if blockers[i].recv == "" && blockers[i].fn == qual {
					return &blockers[i], qual
				}
			}
			return nil, ""
		}
	}

	// value.Method(...) -- match on the receiver's static type.
	tv, ok := info.Types[sel.X]
	if !ok {
		return nil, ""
	}
	recv := typeName(tv.Type)
	if recv == "" {
		return nil, ""
	}
	for i := range blockers {
		if blockers[i].recv == recv && blockers[i].fn == name {
			return &blockers[i], recv + "." + name
		}
	}
	return nil, ""
}

// typeName renders a type as pkg.Name, stripping pointers and aliases, so that
// *net.TCPConn and net.TCPConn are the same entry.
func typeName(t types.Type) string {
	for {
		p, ok := t.Underlying().(*types.Pointer)
		if !ok {
			break
		}
		t = p.Elem()
	}
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return ""
	}
	obj := named.Obj()
	if obj.Pkg() == nil {
		return obj.Name()
	}
	return obj.Pkg().Name() + "." + obj.Name()
}

func funcName(d *ast.FuncDecl) string {
	if d.Recv != nil && len(d.Recv.List) > 0 {
		return "(" + exprString(d.Recv.List[0].Type) + ")." + d.Name.Name
	}
	return d.Name.Name
}

func exprString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return "*" + exprString(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return exprString(t.X) + "." + t.Sel.Name
	case *ast.IndexExpr:
		return exprString(t.X)
	}
	return "?"
}

// printCallers lists, for each named function, the call sites that reach it by
// name. It is deliberately a NAME match and not a call graph: a call graph
// needs SSA and whole-program analysis, and the question here -- "is this
// helper only ever used from inside a wrapper?" -- is answered by reading the
// handful of sites it prints.
func printCallers(pkgs []*packages.Package, names map[string]bool) {
	bare := map[string]bool{}
	for n := range names {
		if i := strings.LastIndex(n, "."); i >= 0 {
			bare[n[i+1:]] = true
		} else {
			bare[n] = true
		}
	}
	type site struct {
		callee string
		pos    token.Position
		in     string
	}
	var sites []site
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, file := range p.Syntax {
			var cur string
			ast.Inspect(file, func(n ast.Node) bool {
				if d, ok := n.(*ast.FuncDecl); ok {
					cur = funcName(d)
				}
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				var nm string
				switch f := call.Fun.(type) {
				case *ast.Ident:
					nm = f.Name
				case *ast.SelectorExpr:
					nm = f.Sel.Name
				}
				if nm != "" && bare[nm] {
					sites = append(sites, site{nm, p.Fset.Position(call.Pos()), cur})
				}
				return true
			})
		}
	})
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].callee != sites[j].callee {
			return sites[i].callee < sites[j].callee
		}
		return sites[i].pos.String() < sites[j].pos.String()
	})
	if len(sites) == 0 {
		fmt.Println("   (no call sites found by name)")
		return
	}
	last := ""
	for _, s := range sites {
		if s.callee != last {
			fmt.Printf("  %s:\n", s.callee)
			last = s.callee
		}
		fmt.Printf("    %s:%d  in %s\n", s.pos.Filename, s.pos.Line, s.in)
	}
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseBlocker reads "net.Conn.Read" as a method and "time.Sleep" as a
// function. The distinction is the dot count, which is unambiguous because a
// receiver is always written qualified.
func parseBlocker(s string) (blocker, error) {
	parts := strings.Split(s, ".")
	switch len(parts) {
	case 2:
		return blocker{recv: "", fn: s, why: "(named with -also)", kind: "also"}, nil
	case 3:
		return blocker{recv: parts[0] + "." + parts[1], fn: parts[2], why: "(named with -also)", kind: "also"}, nil
	}
	return blocker{}, fmt.Errorf("want pkg.Func or pkg.Type.Method")
}
