package secretscan

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
)

// Finding is one secret input, or one sample that shows a secret value.
type Finding struct {
	Kind        string `json:"kind"`   // "input" (what the daemon takes) or "sample" (what a doc shows)
	Source      string `json:"source"` // config-field, flag, env, unit-env, unit-flag, sample, doc
	Class       Class  `json:"class"`
	Name        string `json:"name"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	PermChecked *bool  `json:"perm_checked,omitempty"` // path class only; a heuristic, see ScanRepo
	Placeholder bool   `json:"placeholder,omitempty"`  // sample class only: the value looks like a stand-in
	Exposure    int    `json:"exposure"`               // 1 doc … 4 value stored in a shipped file
}

var exposure = map[string]int{
	"unit-env": 4, "unit-flag": 4,
	"flag": 3,
	"env":  2, "config-field": 2,
	"sample": 1, "doc": 1,
}

func newFinding(kind, source string, class Class, name, file string, line int) Finding {
	return Finding{Kind: kind, Source: source, Class: class, Name: name, File: file, Line: line, Exposure: exposure[source]}
}

// GoScan is what one Go source file says.
type GoScan struct {
	Findings     []Finding
	Imports      []string
	HasMain      bool // declares func main
	Serves       bool // listens, serves, or waits for a signal: a daemon marker
	PermEvidence bool // checks a file's permissions, or goes through secretsource
}

var tagKeys = []string{"toml", "yaml", "json", "mapstructure", "env", "flag", "koanf", "hcl"}

var flagFuncs = map[string]bool{
	"String": true, "StringP": true, "StringVar": true, "StringVarP": true,
	"StringSlice": true, "StringSliceP": true, "StringSliceVar": true, "StringSliceVarP": true,
	"StringArray": true, "StringArrayP": true, "StringArrayVar": true, "StringArrayVarP": true,
}

var serveMarkers = []string{
	"ListenAndServe", "net.Listen(", ".Serve(", "http.Server{",
	"signal.Notify", "NewTicker", "ListenConfig",
}

var permMarkers = []string{".Perm()", "Mode()&", "0o077", "0077", "0o177", "0177"}

// SecretsourcePath is the import path of the fleet's reference resolver.
const SecretsourcePath = "github.com/go-authn/secretsource"

// ScanGo reads one Go file. rel is the name findings carry.
func ScanGo(rel string, src []byte) (GoScan, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution|parser.ParseComments)
	if err != nil {
		return GoScan{}, err
	}
	var gs GoScan
	for _, m := range serveMarkers {
		gs.Serves = gs.Serves || bytes.Contains(src, []byte(m))
	}
	for _, m := range permMarkers {
		gs.PermEvidence = gs.PermEvidence || bytes.Contains(src, []byte(m))
	}
	for _, im := range f.Imports {
		p, _ := strconv.Unquote(im.Path.Value)
		gs.Imports = append(gs.Imports, p)
		if strings.HasPrefix(p, SecretsourcePath) {
			gs.PermEvidence = true
		}
	}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == "main" {
			gs.HasMain = true
		}
	}
	add := func(source, name string, pos token.Pos) {
		if c := Classify(name); c != None {
			gs.Findings = append(gs.Findings,
				newFinding("input", source, c, name, rel, fset.Position(pos).Line))
		}
	}
	if ast.IsGenerated(f) {
		// protobuf and codegen output describe a wire schema, not what the
		// daemon is configured with.
		return gs, nil
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Field:
			if name := fieldName(x); name != "" {
				add("config-field", name, x.Pos())
			}
		case *ast.CallExpr:
			if source, name := callName(x); name != "" {
				add(source, name, x.Pos())
			}
		}
		return true
	})
	return gs, nil
}

// fieldName returns the configuration name of a struct field that carries a
// string-like value, the first tag name that denotes a secret, or "".
func fieldName(f *ast.Field) string {
	if f.Tag == nil || len(f.Names) == 0 || !stringLike(f.Type) {
		return ""
	}
	raw, _ := strconv.Unquote(f.Tag.Value)
	tag := reflect.StructTag(raw)
	for _, k := range tagKeys {
		v, ok := tag.Lookup(k)
		if !ok {
			continue
		}
		name, _, _ := strings.Cut(v, ",")
		if Classify(name) != None {
			return name
		}
	}
	return ""
}

// stringLike: a bool, a duration or a count named `token_required` is not a
// secret whatever its name says, so only a string, bytes, or a list or map of
// strings can hold one.
func stringLike(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name == "string"
	case *ast.StarExpr:
		return stringLike(t.X)
	case *ast.ArrayType:
		id, ok := t.Elt.(*ast.Ident)
		return ok && (id.Name == "string" || id.Name == "byte")
	case *ast.MapType:
		id, ok := t.Value.(*ast.Ident)
		return ok && id.Name == "string"
	}
	return false
}

// callName returns ("flag"|"env", name) for a pflag/flag string flag
// definition or an os.Getenv/os.LookupEnv of a literal name, else "".
func callName(c *ast.CallExpr) (string, string) {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", ""
	}
	source, idx := "", 0
	switch fn := sel.Sel.Name; {
	case flagFuncs[fn]:
		source = "flag"
		// flag.String(name, value, usage) takes three arguments, a P
		// variant a shorthand, a Var variant a pointer: any other count
		// is a serializer's `enc.String("Token")`, not a flag.
		want := 3
		if strings.Contains(fn, "Var") {
			idx = 1
			want++
		}
		if strings.HasSuffix(fn, "P") {
			want++
		}
		if len(c.Args) != want {
			return "", ""
		}
	case fn == "Getenv" || fn == "LookupEnv":
		if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "os" {
			return "", ""
		}
		source = "env"
	default:
		return "", ""
	}
	if idx >= len(c.Args) {
		return "", ""
	}
	lit, ok := c.Args[idx].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", ""
	}
	name, _ := strconv.Unquote(lit.Value)
	return source, name
}
