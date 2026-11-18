package binnames

import (
	"bufio"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The reference lists are committed under ref/ and embedded. Each file opens
// with `# list:`, `# source:` and `# fetched:` header lines naming what was
// opened to make it, then one `name<TAB>detail` per line. They are produced by
// internal/binnames/refgen, never typed: a list written from memory is a list
// of the commands one remembers.
//
//go:embed ref/*.txt
var refFS embed.FS

// A Ref is one well-known command.
type Ref struct {
	Name    string // as the source spells it
	List    string
	Source  string
	Detail  string
	Host    bool // from the host's PATH, not from a committed list
	Fetched string
}

// Index maps a normalised command name to everything that owns it.
type Index map[string][]Ref

// Normalise folds a command name for comparison: lower case, no `.exe`.
// Windows resolves `Kinit.EXE` and `kinit` to the same command.
func Normalise(name string) string {
	n := strings.ToLower(name)
	return strings.TrimSuffix(n, ".exe")
}

// LoadRefs reads the committed lists.
func LoadRefs() (Index, error) { return loadRefs(refFS, "ref") }

func loadRefs(fsys fs.FS, dir string) (Index, error) {
	ents, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	idx := Index{}
	n := 0
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		f, err := fsys.Open(dir + "/" + e.Name())
		if err != nil {
			return nil, err
		}
		cur := Ref{List: strings.TrimSuffix(e.Name(), ".txt")}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := strings.TrimRight(sc.Text(), "\r")
			switch {
			case line == "":
			case strings.HasPrefix(line, "# list:"):
				cur.List = strings.TrimSpace(strings.TrimPrefix(line, "# list:"))
			case strings.HasPrefix(line, "# source:"):
				if cur.Source != "" {
					cur.Source += "; "
				}
				cur.Source += strings.TrimSpace(strings.TrimPrefix(line, "# source:"))
			case strings.HasPrefix(line, "# fetched:"):
				cur.Fetched = strings.TrimSpace(strings.TrimPrefix(line, "# fetched:"))
			case strings.HasPrefix(line, "#"):
			default:
				// name TAB detail [TAB source]: a line may cite its own page.
				name, rest, _ := strings.Cut(line, "\t")
				detail, src, hasSrc := strings.Cut(rest, "\t")
				r := cur
				r.Name, r.Detail = name, detail
				if hasSrc && src != "" {
					r.Source = src
				}
				k := Normalise(name)
				idx[k] = append(idx[k], r)
				n++
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if cur.Source == "" {
			return nil, fmt.Errorf("%s names no source: a list nobody can check is not a reference", e.Name())
		}
	}
	if n == 0 {
		return nil, fmt.Errorf("no reference names under %s: a comparison with nothing would call everything clean", dir)
	}
	return idx, nil
}

// PathSupplement adds the executables found on a PATH to the index, as a
// supplement to the committed lists. Directories in skip are not read: they
// hold the fleet's own installed tools, which would otherwise collide with
// themselves. It returns the directories it read and the ones it could not.
func (idx Index) PathSupplement(pathEnv string, skip map[string]bool) (read []string, failed []Unread) {
	seen := map[string]bool{}
	for _, d := range filepath.SplitList(pathEnv) {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		if skip[filepath.Clean(d)] {
			continue
		}
		ents, err := os.ReadDir(d)
		if err != nil {
			if !os.IsNotExist(err) {
				failed = append(failed, Unread{d, err.Error()})
			}
			continue
		}
		read = append(read, d)
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			fi, err := os.Stat(filepath.Join(d, e.Name())) // follows symlinks
			if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
				continue
			}
			k := Normalise(e.Name())
			idx[k] = append(idx[k], Ref{
				Name: e.Name(), List: "host PATH", Source: "the host's PATH (" + d + ")", Host: true,
			})
		}
	}
	sort.Strings(read)
	return read, failed
}

// A Collision is an installed name that something else already owns.
type Collision struct {
	Binary string
	Owners []Ref
}

// Check compares an install name with the index.
func (idx Index) Check(name string, allow map[string]bool) (Collision, bool) {
	k := Normalise(name)
	if allow[k] {
		return Collision{}, false
	}
	owners := idx[k]
	if len(owners) == 0 {
		return Collision{}, false
	}
	return Collision{Binary: name, Owners: owners}, true
}

// Describe renders what owns a name, preferring the committed lists, which
// cite a source, and saying when only the host's PATH knows it.
func (c Collision) Describe() string {
	var parts []string
	for _, o := range c.Owners {
		s := o.Name + " [" + o.List
		if o.Detail != "" {
			s += ": " + o.Detail
		}
		s += "; source: " + o.Source + "]"
		parts = append(parts, s)
	}
	return strings.Join(parts, " | ")
}
