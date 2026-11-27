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
	"flag": 3, "url-userinfo": 3,
	"env": 2, "config-field": 2, "env-const": 2, "env-implicit": 2,
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

// awsExplicit: a file that names its credentials provider does not fall back
// on the SDK's default chain (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY,
// AWS_SESSION_TOKEN read from the environment).
var awsExplicit = []string{"WithCredentialsProvider", "credentials.New", "WithCredentials("}

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
	// the env name of a secret held in a variable: a const, or the default of
	// an indirection (`name := os.Getenv("X_ENV"); if name == "" { name =
	// "X" }`), is where the name is spelled, whatever os.Getenv later gets.
	envConst := func(e ast.Expr) {
		if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, _ := strconv.Unquote(lit.Value); envLike(s) {
				add("env-const", s, lit.Pos())
			}
		}
	}
	awsSDK := false
	for _, p := range gs.Imports {
		awsSDK = awsSDK || strings.HasPrefix(p, "github.com/aws/aws-sdk-go")
	}
	explicitAWS := false
	for _, m := range awsExplicit {
		explicitAWS = explicitAWS || bytes.Contains(src, []byte(m))
	}
	// companions holds every spelling of `<NAME>_FROM` the file contains: a
	// string literal, or a const's name joined to the literal "_FROM".
	companions := map[string]bool{}
	constVals := map[string]string{}
	var joined []string // consts joined to "_FROM", resolved once all consts are known
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ValueSpec:
			for i, v := range x.Values {
				envConst(v)
				if lit, ok := v.(*ast.BasicLit); ok && lit.Kind == token.STRING && i < len(x.Names) {
					constVals[x.Names[i].Name], _ = strconv.Unquote(lit.Value)
				}
			}
		case *ast.AssignStmt:
			for _, v := range x.Rhs {
				envConst(v)
			}
		case *ast.BinaryExpr:
			// `envPassword + "_FROM"`
			if id, ok := x.X.(*ast.Ident); ok && x.Op == token.ADD {
				if lit, ok := x.Y.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if suffix, _ := strconv.Unquote(lit.Value); suffix == "_FROM" {
						joined = append(joined, id.Name)
					}
				}
			}
		case *ast.BasicLit:
			// a number or a rune literal does not unquote to a URL.
			s, _ := strconv.Unquote(x.Value)
			if strings.HasSuffix(s, "_FROM") {
				companions[s] = true
			}
			if scheme, pw, ok := URLSecret(s); ok {
				line := fset.Position(x.Pos()).Line
				if placeholder(pw) {
					fd := newFinding("sample", "sample", Plaintext, URLName(scheme), rel, line)
					fd.Placeholder = true
					gs.Findings = append(gs.Findings, fd)
				} else {
					gs.Findings = append(gs.Findings,
						newFinding("input", "url-userinfo", Plaintext, URLName(scheme), rel, line))
				}
			}
		case *ast.Field:
			if name := fieldName(x); name != "" {
				add("config-field", name, x.Pos())
			}
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && awsSDK && !explicitAWS &&
				(sel.Sel.Name == "LoadDefaultConfig" || sel.Sel.Name == "NewSession") {
				gs.Findings = append(gs.Findings, newFinding("input", "env-implicit", Plaintext,
					"AWS_SECRET_ACCESS_KEY", rel, fset.Position(x.Pos()).Line))
			}
			if source, name, pathy := callName(x); name != "" {
				if pathy && Classify(name) == Plaintext {
					// the flag takes a path (or a prompt), not the value.
					gs.Findings = append(gs.Findings,
						newFinding("input", source, Path, name, rel, fset.Position(x.Pos()).Line))
				} else {
					add(source, name, x.Pos())
				}
			}
		}
		return true
	})
	// a name the file also reads by reference (`<NAME>_FROM`, through a
	// secretsource resolver) is received by reference: the plain name is the
	// fallback, not a plaintext input the gate should refuse.
	for _, id := range joined {
		if v, ok := constVals[id]; ok {
			companions[v+"_FROM"] = true
		}
	}
	kept := gs.Findings[:0]
	for _, fd := range gs.Findings {
		if (fd.Source == "env-const" || fd.Source == "env") && companions[fd.Name+"_FROM"] {
			continue
		}
		kept = append(kept, fd)
	}
	gs.Findings = kept
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

// pathLike: a flag default that is a path (`~/.github-token`, `/etc/x`,
// `./tok`) says the flag takes a path.
func pathLike(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") || strings.HasPrefix(s, "./") ||
		strings.HasPrefix(s, "../")
}

// pathUsage: a usage text that says "file" or "path" says the same.
func pathUsage(s string) bool {
	// "the credential to use (default ~/.github-token)": a path named in the
	// usage is the default, whatever the declared default says.
	for _, f := range strings.Fields(s) {
		if pathLike(strings.Trim(f, "()[],;:'\"`")) {
			return true
		}
	}
	for _, w := range Tokens(s) {
		if w == "file" || w == "path" || w == "filename" {
			return true
		}
	}
	return false
}

// strArg returns the value of the string literal argument i (the caller has
// checked the argument count), or "".
func strArg(c *ast.CallExpr, i int) string {
	if lit, ok := c.Args[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
		s, _ := strconv.Unquote(lit.Value)
		return s
	}
	return ""
}

// callName returns ("flag"|"env", name, pathy) for a pflag/flag string flag
// definition or an os.Getenv/os.LookupEnv of a literal name, else "". pathy
// is true for a flag whose default or usage says it takes a path: its value
// is read from that file, so it is not the secret. Where the value merely
// flows into os.Open or a *From reader, that flow is NOT followed: a stated
// limit.
func callName(c *ast.CallExpr) (string, string, bool) {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", "", false
	}
	source, idx, pathy := "", 0, false
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
			return "", "", false
		}
		// the last two arguments are always the default and the usage.
		pathy = pathLike(strArg(c, want-2)) || pathUsage(strArg(c, want-1))
	case fn == "Getenv" || fn == "LookupEnv":
		if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "os" {
			return "", "", false
		}
		source = "env"
	default:
		return "", "", false
	}
	if idx >= len(c.Args) {
		return "", "", false
	}
	lit, ok := c.Args[idx].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", "", false
	}
	name, _ := strconv.Unquote(lit.Value)
	return source, name, pathy
}
