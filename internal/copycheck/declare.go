package copycheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// span is where a declared symbol lives in our file, in lines.
type span struct {
	val        *Value
	start, end int
}

// symbolSpan finds the declaration of name in a Go file: a var or const spec
// (the whole spec, so a table in braces is inside it) or a function.
func symbolSpan(src []byte, file, name string) (start, end int, err error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, parser.SkipObjectResolution)
	if err != nil {
		return 0, 0, fmt.Errorf("%s does not parse as Go: %w", file, err)
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.GenDecl:
			for _, s := range d.Specs {
				vs, ok := s.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, id := range vs.Names {
					if id.Name == name {
						return fset.Position(vs.Pos()).Line, fset.Position(vs.End()).Line, nil
					}
				}
			}
		case *ast.FuncDecl:
			if d.Name.Name == name && d.Recv == nil {
				return fset.Position(d.Pos()).Line, fset.Position(d.End()).Line, nil
			}
		}
	}
	return 0, 0, fmt.Errorf("%s declares no var, const or func %q", file, name)
}

// declared resolves every Value against our files. A declaration that points at
// nothing (a renamed table, a typo, a deleted file) is itself a finding: a
// provenance record that has drifted from the code is worse than none, because
// it reads as one.
func declared(m *Manifest, ours map[string][]byte) (map[string][]span, []Finding) {
	by := map[string][]span{}
	var fs []Finding
	for i := range m.Values {
		v := &m.Values[i]
		src, ok := ours[v.File]
		if !ok {
			fs = append(fs, Finding{Kind: KindDeclaration, Reference: "-", Message: fmt.Sprintf("values entry %s %s names a file that is not among our sources", v.File, v.Symbol)})
			continue
		}
		s, e, err := symbolSpan(src, v.File, v.Symbol)
		if err != nil {
			fs = append(fs, Finding{Kind: KindDeclaration, Reference: "-", Message: fmt.Sprintf("values entry %s %s is stale: %v", v.File, v.Symbol, err)})
			continue
		}
		by[v.File] = append(by[v.File], span{v, s, e})
	}
	return by, fs
}

// coveredBy returns the declaration that contains lines [a,b] of file.
func coveredBy(by map[string][]span, file string, a, b int) *span {
	for i, s := range by[file] {
		if a >= s.start && b <= s.end {
			return &by[file][i]
		}
	}
	return nil
}

func underPrefix(rel string, prefixes []string) bool {
	for _, p := range prefixes {
		p = strings.TrimSuffix(p, "/")
		if strings.HasPrefix(rel, p) {
			return true
		}
	}
	return false
}
