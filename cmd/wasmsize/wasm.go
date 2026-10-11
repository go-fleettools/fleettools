package main

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The parts of the WebAssembly binary format (core specification, chapter 5,
// "Binary Format") this tool reads: the module header, the section headers,
// the import section (imported functions come first in the function index
// space), the code section (one body per defined function) and the "name"
// custom section's function-name subsection.
const (
	secCustom = 0
	secImport = 2
	secCode   = 10
	secData   = 11
)

var sectionNames = map[byte]string{
	0: "custom", 1: "type", 2: "import", 3: "function", 4: "table", 5: "memory",
	6: "global", 7: "export", 8: "start", 9: "element", 10: "code", 11: "data",
	12: "data count",
}

// module is what wasmsize needs from a .wasm file.
type module struct {
	sections map[string]int // section name (custom ones as "custom:<name>") -> bytes, header included
	imported int            // imported functions: the first indices of the function space
	bodies   []int          // size in bytes of each defined function's body, in order
	names    map[int]string // function index -> name, from the "name" section
}

type reader struct {
	b   []byte
	off int
}

var errShort = errors.New("truncated")

func (r *reader) byte() (byte, error) {
	if r.off >= len(r.b) {
		return 0, errShort
	}
	c := r.b[r.off]
	r.off++
	return c, nil
}

// uleb reads an unsigned LEB128 number of at most 32 bits, as every count and
// size in the format is (core spec 5.2.2).
func (r *reader) uleb() (int, error) {
	var v uint64
	for shift := 0; shift < 35; shift += 7 {
		c, err := r.byte()
		if err != nil {
			return 0, err
		}
		v |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			if v > 1<<32-1 {
				return 0, fmt.Errorf("LEB128 value %d does not fit in 32 bits", v)
			}
			return int(v), nil
		}
	}
	return 0, errors.New("LEB128 number longer than 5 bytes")
}

func (r *reader) bytes(n int) ([]byte, error) {
	if n < 0 || n > len(r.b)-r.off {
		return nil, errShort
	}
	s := r.b[r.off : r.off+n]
	r.off += n
	return s, nil
}

func (r *reader) name() (string, error) {
	n, err := r.uleb()
	if err != nil {
		return "", err
	}
	s, err := r.bytes(n)
	return string(s), err
}

// parse reads a WebAssembly module. It refuses anything that is not one, and
// any length that runs past the end, rather than reporting partial sizes.
func parse(b []byte) (*module, error) {
	if len(b) < 8 || !bytes.Equal(b[:4], []byte("\x00asm")) {
		return nil, errors.New("not a WebAssembly module (no \\0asm magic)")
	}
	if !bytes.Equal(b[4:8], []byte{1, 0, 0, 0}) {
		return nil, fmt.Errorf("WebAssembly binary version %v, want 1", b[4:8])
	}
	m := &module{sections: map[string]int{}, names: map[int]string{}}
	r := &reader{b: b, off: 8}
	for r.off < len(b) {
		start := r.off
		id, _ := r.byte()
		size, err := r.uleb()
		if err != nil {
			return nil, fmt.Errorf("section at offset %d: %w", start, err)
		}
		body, err := r.bytes(size)
		if err != nil {
			return nil, fmt.Errorf("section %d at offset %d: %d bytes declared, %d left", id, start, size, len(b)-r.off)
		}
		name, ok := sectionNames[id]
		if !ok {
			return nil, fmt.Errorf("unknown section id %d at offset %d", id, start)
		}
		sr := &reader{b: body}
		switch id {
		case secCustom:
			cname, err := sr.name()
			if err != nil {
				return nil, fmt.Errorf("custom section at offset %d: %w", start, err)
			}
			name = "custom:" + cname
			if cname == "name" {
				if err := m.readNames(sr); err != nil {
					return nil, fmt.Errorf("name section: %w", err)
				}
			}
		case secImport:
			if err := m.readImports(sr); err != nil {
				return nil, fmt.Errorf("import section: %w", err)
			}
		case secCode:
			if err := m.readCode(sr); err != nil {
				return nil, fmt.Errorf("code section: %w", err)
			}
		}
		m.sections[name] += r.off - start
	}
	return m, nil
}

func (m *module) readImports(r *reader) error {
	n, err := r.uleb()
	if err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		if _, err := r.name(); err != nil { // module
			return err
		}
		if _, err := r.name(); err != nil { // field
			return err
		}
		kind, err := r.byte()
		if err != nil {
			return err
		}
		switch kind {
		case 0: // function: a type index
			if _, err := r.uleb(); err != nil {
				return err
			}
			m.imported++
		case 1: // table: reftype, limits
			if _, err := r.byte(); err != nil {
				return err
			}
			if err := r.limits(); err != nil {
				return err
			}
		case 2: // memory: limits
			if err := r.limits(); err != nil {
				return err
			}
		case 3: // global: valtype, mutability
			if _, err := r.bytes(2); err != nil {
				return err
			}
		default:
			return fmt.Errorf("import %d: unknown kind %d", i, kind)
		}
	}
	return nil
}

func (r *reader) limits() error {
	flag, err := r.byte()
	if err != nil {
		return err
	}
	if _, err := r.uleb(); err != nil {
		return err
	}
	if flag&1 != 0 {
		_, err = r.uleb()
	}
	return err
}

func (m *module) readCode(r *reader) error {
	n, err := r.uleb()
	if err != nil {
		return err
	}
	m.bodies = make([]int, 0, n)
	for i := 0; i < n; i++ {
		start := r.off
		size, err := r.uleb()
		if err != nil {
			return err
		}
		if _, err := r.bytes(size); err != nil {
			return fmt.Errorf("body %d: %w", i, err)
		}
		m.bodies = append(m.bodies, r.off-start)
	}
	return nil
}

func (m *module) readNames(r *reader) error {
	for r.off < len(r.b) {
		id, _ := r.byte()
		size, err := r.uleb()
		if err != nil {
			return err
		}
		sub, err := r.bytes(size)
		if err != nil {
			return err
		}
		if id != 1 { // only function names
			continue
		}
		sr := &reader{b: sub}
		n, err := sr.uleb()
		if err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			idx, err := sr.uleb()
			if err != nil {
				return err
			}
			name, err := sr.name()
			if err != nil {
				return err
			}
			m.names[idx] = name
		}
	}
	return nil
}

// sanitize spells a Go symbol the way cmd/link writes it into the name
// section: every byte outside [0-9A-Za-z_.] becomes "_" (nameRegexp in
// cmd/link/internal/wasm/asm.go). Package paths lose their "/" and "-" there,
// so a name cannot be split back into a path; it can only be matched against
// the sanitized form of the paths a build is known to contain.
var unsafeName = regexp.MustCompile(`[^\w.]`)

func sanitize(s string) string { return unsafeName.ReplaceAllString(s, "_") }

// attributor maps sanitized function names back to package paths.
type attributor struct {
	byPrefix   map[string]string   // sanitized path -> path
	collisions map[string][]string // sanitized path -> every path that spells it
	modules    map[string]string   // path -> module path, when the list gave one
	maxLen     int
}

// newAttributor reads the build's package list: one package per line, as
// `go list -deps` prints it, optionally followed by its module path, as
// `go list -deps -f '{{.ImportPath}} {{with .Module}}{{.Path}}{{end}}'`
// prints it. With modules given, -by module needs no guessing.
func newAttributor(lines []string) *attributor {
	// The linker names a program's own package "main", whatever its import
	// path; `go list -deps` lists it by path, so "main" is always known.
	a := &attributor{byPrefix: map[string]string{"main": "main"}, collisions: map[string][]string{}, modules: map[string]string{}, maxLen: len("main")}
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		p := f[0]
		if len(f) > 1 {
			a.modules[p] = f[1]
		}
		s := sanitize(p)
		if q, ok := a.byPrefix[s]; ok && q != p {
			if len(a.collisions[s]) == 0 {
				a.collisions[s] = []string{q}
			}
			a.collisions[s] = append(a.collisions[s], p)
			continue
		}
		a.byPrefix[s] = p
		a.maxLen = max(a.maxLen, len(s))
	}
	return a
}

// pkg returns the package path a function name belongs to: the longest known
// sanitized path that the name starts with, followed by a ".". Without a match
// it returns the bucket the name falls in, in angle brackets.
func (a *attributor) pkg(name string) string {
	for i := min(len(name)-1, a.maxLen); i > 0; i-- {
		if name[i] != '.' {
			continue
		}
		if p, ok := a.byPrefix[name[:i]]; ok {
			if _, ambiguous := a.collisions[name[:i]]; ambiguous {
				return "<ambiguous " + name[:i] + ">"
			}
			return p
		}
	}
	switch {
	case name == "":
		return "<unnamed>"
	case strings.HasPrefix(name, "type_."), strings.HasPrefix(name, "go_."):
		return "<compiler-generated>"
	}
	return "<unattributed>"
}

// modulePath guesses the module of a package the list gave no module for: the
// standard library for a path whose first element has no dot, otherwise the
// first three elements (host/owner/repo), plus a /vN major-version suffix. It
// is a heuristic, and it is wrong for google.golang.org/protobuf (two
// elements), which it splits into protobuf/internal, protobuf/types, ...:
// give the module column to -deps to avoid it.
func modulePath(pkg string) string {
	if strings.HasPrefix(pkg, "<") {
		return pkg
	}
	parts := strings.Split(pkg, "/")
	if !strings.Contains(parts[0], ".") {
		return "std"
	}
	n := min(3, len(parts))
	if len(parts) > 3 && len(parts[3]) > 1 && parts[3][0] == 'v' && strings.Trim(parts[3][1:], "0123456789") == "" {
		n = 4
	}
	return strings.Join(parts[:n], "/")
}

type row struct {
	key   string
	bytes int
	funcs int
}

// tally sums function body sizes by package (or module, when byModule).
func tally(m *module, a *attributor, byModule bool) []row {
	acc := map[string]*row{}
	for i, size := range m.bodies {
		key := a.pkg(m.names[m.imported+i])
		if byModule {
			if mod, ok := a.modules[key]; ok {
				key = mod
			} else {
				key = modulePath(key)
			}
		}
		r := acc[key]
		if r == nil {
			r = &row{key: key}
			acc[key] = r
		}
		r.bytes += size
		r.funcs++
	}
	rows := make([]row, 0, len(acc))
	for _, r := range acc {
		rows = append(rows, *r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].bytes != rows[j].bytes {
			return rows[i].bytes > rows[j].bytes
		}
		return rows[i].key < rows[j].key
	})
	return rows
}
