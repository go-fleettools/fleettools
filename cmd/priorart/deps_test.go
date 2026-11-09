package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// stubModules replaces the build-list reader for one test.
func stubModules(t *testing.T, f func(dir string) ([]Module, error)) {
	t.Helper()
	saved := modules
	modules = f
	t.Cleanup(func() { modules = saved })
}

// stubOrgs replaces the organisation list for one test.
func stubOrgs(t *testing.T, names []string, err error) {
	t.Helper()
	saved := ourOrgs
	ourOrgs = func() ([]string, error) { return names, err }
	t.Cleanup(func() { ourOrgs = saved })
}

// writeModule lays out a tiny module on disk and returns it.
func writeModule(t *testing.T, path, version string, files map[string]string) Module {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Module{Path: path, Version: version, Dir: dir}
}

// ⛔⛔ THE REGRESSION THAT MADE THIS GATE USABLE. A substring match answered
// "file" from ICCLutProfile — "Profile" ends in those four letters — and from
// File_collab_proto. Measured on go-tex's playground that was 322 hits in 38
// modules, which is a gate nobody reads. Components matched by equality give 47.
func TestCarriesTermMatchesComponentsNotSubstrings(t *testing.T) {
	terms := []string{"file", "manager"}
	for _, name := range []string{"FileTree", "FileChooser", "NewFileTree", "File", "FileTree.BeginRename"} {
		if _, ok := carriesTerm(name, terms); !ok {
			t.Errorf("%q does not answer %v, and it is the thing being looked for", name, terms)
		}
	}
	for _, name := range []string{"ICCLutProfile", "Profile", "Profiler", "Filename"} {
		if term, ok := carriesTerm(name, terms); ok {
			t.Errorf("%q answered %q; a component match must not fire on a substring", name, term)
		}
	}
	// A multi-word phrase can never be one component, and its own words are in
	// the term list separately — so it must not be tried.
	if _, ok := carriesTerm("FileManager", []string{"file manager"}); ok {
		t.Error("a phrase matched a single identifier component")
	}
	// A method answers for its receiver as well as its own name.
	if term, ok := carriesTerm("FileTree.Reveal", []string{"reveal"}); !ok || term != "reveal" {
		t.Errorf("FileTree.Reveal did not answer its own name: %q %v", term, ok)
	}
}

func TestCamelComponents(t *testing.T) {
	for name, want := range map[string][]string{
		"FileTree":          {"file", "tree"},
		"ICCLutProfile":     {"icc", "lut", "profile"},
		"File_collab_proto": {"file", "collab", "proto"},
		"A":                 {"a"},
		"":                  nil,
		"lowercase":         {"lowercase"},
		"HTTPServer":        {"http", "server"},
	} {
		if got := camelComponents(name); !slices.Equal(got, want) {
			t.Errorf("camelComponents(%q) = %v, want %v", name, got, want)
		}
	}
}

// ⛔ A prefix heuristic was wrong in BOTH directions: github.com/go-* matches
// go-git, go-logr, go-jose and go-openapi, none of them ours. The authoritative
// list is the organisations this account belongs to.
func TestOwnedUsesTheOrganisationList(t *testing.T) {
	orgs := map[string]bool{"go-tex": true, "go-widgets": true}
	for path, want := range map[string]bool{
		"github.com/go-widgets/toolkit":    true,
		"github.com/go-tex/engine":         true,
		"github.com/go-git/gcfg":           false, // starts with go-, not ours
		"github.com/go-logr/logr":          false,
		"golang.org/x/net":                 false,
		"github.com/go-widgets":            false, // no repository part
		"gitlab.com/go-widgets/toolkit":    false, // another host
		"github.com/go-widgets/toolkit/v2": true,
	} {
		if got := Owned(path, orgs); got != want {
			t.Errorf("Owned(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestDecodeModulesReadsConcatenatedObjects(t *testing.T) {
	ms, err := decodeModules([]byte(`{"Path":"m","Main":true,"Dir":"/m"}
{"Path":"github.com/go-tex/engine","Version":"v1.2.3","Dir":"/cache/engine"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || !ms[0].Main || ms[1].Version != "v1.2.3" {
		t.Fatalf("decodeModules = %+v", ms)
	}
	if _, err := decodeModules([]byte(`{"Path":`)); err == nil {
		t.Error("a truncated build list was accepted")
	}
}

func TestSurfaceReadsTypesFuncsAndMethods(t *testing.T) {
	m := writeModule(t, "github.com/go-widgets/toolkit", "v0.340.0", map[string]string{
		"filetree.go": `package toolkit
type FileTree struct{}
func (f *FileTree) BeginRename(p string) {}
func (f FileTree) AskDelete(p string) {}
func NewFileTree() *FileTree { return nil }
const FileCreate = 1
var FileDefault = 2
type fileNode struct{}
func (f *fileNode) hidden() {}
`,
		"other_test.go":    `package toolkit` + "\n" + `type FileFromATest struct{}`,
		"testdata/skip.go": `package x` + "\n" + `type FileFixture struct{}`,
		"vendor/v/vend.go": `package v` + "\n" + `type FileVendored struct{}`,
		"_ignored/ign.go":  `package i` + "\n" + `type FileIgnored struct{}`,
		"broken.go":        `package toolkit` + "\n" + `type FileBroken struct{`,
		"imports.go":       `package toolkit` + "\n" + `import "fmt"` + "\n" + `var _ = fmt.Sprint`,
	})

	got, err := Surface(m, []string{"file"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range got {
		names = append(names, d.Kind+" "+d.Name)
	}
	want := []string{
		"const FileCreate",
		"var FileDefault",
		"type FileTree",
		"method FileTree.AskDelete",
		"method FileTree.BeginRename",
		"func NewFileTree",
	}
	slices.Sort(want)
	slices.Sort(names)
	if !slices.Equal(names, want) {
		t.Errorf("Surface =\n  %v\nwant\n  %v", names, want)
	}
	// ⛔ Each exclusion above is deliberate, so each is asserted: an unexported
	// declaration, a _test.go file, testdata, vendor and a _-prefixed directory
	// answer a different question, and a file that does not parse must not
	// abandon the module — `broken.go` is unparseable and FileBroken is absent
	// while the rest of the module still answered.
	for _, absent := range []string{"fileNode", "FileFromATest", "FileFixture", "FileVendored", "FileIgnored", "FileBroken"} {
		if slices.ContainsFunc(got, func(d Decl) bool { return strings.Contains(d.Name, absent) }) {
			t.Errorf("Surface included %q", absent)
		}
	}
	// The module and version travel with every hit, because "it exists
	// upstream" and "it exists in the version you depend on" differ.
	for _, d := range got {
		if d.Module != m.Path || d.Version != "v0.340.0" || d.Pkg != "toolkit" {
			t.Errorf("hit %+v lost its provenance", d)
		}
	}
}

// ⛔ A GENERIC RECEIVER IS STILL A RECEIVER. `func (s *Store[T]) FileAt()` is
// the shape a fleet module written with type parameters has, and a receiver
// this could not name would drop the method silently — the gate failing open,
// which is the only direction that matters.
func TestSurfaceNamesGenericReceivers(t *testing.T) {
	m := writeModule(t, "github.com/go-tex/x", "v1", map[string]string{
		"g.go": `package x
type FileStore[T any] struct{}
func (s *FileStore[T]) Reveal() {}
type Pair[A any, B any] struct{}
func (p Pair[A, B]) FileAt() {}
func Bare() {}
`,
	})
	got, err := Surface(m, []string{"file", "reveal"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range got {
		names = append(names, d.Name)
	}
	slices.Sort(names)
	want := []string{"FileStore", "FileStore.Reveal", "Pair.FileAt"}
	if !slices.Equal(names, want) {
		t.Errorf("Surface = %v, want %v", names, want)
	}
}

// ⛔ goListModules is the one function that runs the go command, so it is
// exercised against a REAL module rather than only through its seam: a stub
// proves the callers, never the reader. Both outcomes are asserted, because the
// failure is the one that must stay a refusal — see the doc comment for the
// dependency that makes `go list -m all` refuse outright for every consumer.
func TestGoListModulesReadsARealModuleAndReportsARefusal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/only\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ms, err := goListModules(dir)
	if err != nil {
		t.Fatalf("reading a module with no requirements: %v", err)
	}
	if len(ms) != 1 || ms[0].Path != "example.com/only" || !ms[0].Main {
		t.Fatalf("build list = %+v, want the main module alone", ms)
	}

	// A directory that is not a module: the go command refuses, and so must we.
	_, err = goListModules(t.TempDir())
	if err == nil {
		t.Fatal("a directory with no go.mod was read as an empty build list")
	}
	// ⛔ The go command's own sentence is carried through. Without it the reader
	// gets "exit status 1" and has to reproduce the call to learn anything.
	if !strings.Contains(err.Error(), "go.mod") {
		t.Errorf("error = %v, want the go command's own explanation", err)
	}
}

// A module the cache does not hold is reported, not treated as empty.
func TestSurfaceRefusesAModuleWithNoDirectory(t *testing.T) {
	_, err := Surface(Module{Path: "github.com/go-tex/x", Version: "v1"}, []string{"file"})
	if err == nil {
		t.Fatal("a module with no local directory was read as clean")
	}
	if !strings.Contains(err.Error(), "module cache") {
		t.Errorf("error = %v, want it to say where it looked", err)
	}
}

// ⛔ One unreadable module must NOT hide the hits from the readable ones. The
// first draft returned on the first failure, and on a real repository that meant
// one uncached module hid every hit from ten that were readable.
func TestDepSurfaceKeepsHitsWhenAModuleIsUnreadable(t *testing.T) {
	good := writeModule(t, "github.com/go-widgets/toolkit", "v0.340.0", map[string]string{
		"a.go": "package toolkit\ntype FileTree struct{}\n",
	})
	stubOrgs(t, []string{"go-widgets", "go-tex"}, nil)
	stubModules(t, func(string) ([]Module, error) {
		return []Module{
			{Path: "example.com/main", Main: true, Dir: "/main"},
			good,
			{Path: "github.com/go-tex/texmf", Version: "v0.7.0"}, // no Dir: uncached
			{Path: "github.com/go-logr/logr", Version: "v1.4.3"}, // not ours
		}, nil
	})

	r, err := depSurface(".", []string{"file"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Hits) != 1 || r.Hits[0].Name != "FileTree" {
		t.Errorf("hits = %+v, want the one from the readable module", r.Hits)
	}
	if r.Read != 1 {
		t.Errorf("Read = %d, want 1", r.Read)
	}
	if len(r.Unread) != 1 || !strings.Contains(r.Unread[0], "texmf") {
		t.Errorf("Unread = %v, want the uncached module named", r.Unread)
	}
}

// Without the organisation list we cannot tell our modules from a stranger's,
// so the surface is unread rather than read with a guess.
func TestDepSurfaceIsUnreadWithoutTheOrganisationList(t *testing.T) {
	stubOrgs(t, nil, errors.New("no network"))
	stubModules(t, func(string) ([]Module, error) {
		return []Module{{Path: "github.com/go-widgets/toolkit", Version: "v1", Dir: t.TempDir()}}, nil
	})
	if _, err := depSurface(".", []string{"file"}, false); err == nil {
		t.Fatal("a failed organisation listing was treated as an answer")
	}
}

// A build list with no module of ours answered nothing at all, and says so
// rather than reporting clean.
func TestDepSurfaceSaysWhenNothingWasSelected(t *testing.T) {
	stubOrgs(t, []string{"go-tex"}, nil)
	stubModules(t, func(string) ([]Module, error) {
		return []Module{
			{Path: "example.com/main", Main: true},
			{Path: "golang.org/x/net", Version: "v0.1.0", Dir: "/x/net"},
		}, nil
	})
	_, err := depSurface(".", []string{"file"}, false)
	if err == nil {
		t.Fatal("a build list with no fleet module reported clean")
	}
	if !strings.Contains(err.Error(), "deps-all") {
		t.Errorf("error = %v, want it to name the way round it", err)
	}
}

// -deps-all reads every module and needs no organisation list at all.
func TestDepSurfaceAllSkipsTheOrganisationList(t *testing.T) {
	stubOrgs(t, nil, errors.New("must not be called"))
	m := writeModule(t, "golang.org/x/tools", "v0.1.0", map[string]string{
		"a.go": "package tools\ntype FileSet struct{}\n",
	})
	stubModules(t, func(string) ([]Module, error) {
		return []Module{{Path: "example.com/main", Main: true}, m}, nil
	})
	r, err := depSurface(".", []string{"file"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Hits) != 1 || r.Hits[0].Name != "FileSet" {
		t.Errorf("-deps-all hits = %+v", r.Hits)
	}
}

// A build list that cannot be read at all holds the gate shut with nothing
// behind it.
func TestDepSurfaceReportsAnUnreadableBuildList(t *testing.T) {
	stubModules(t, func(string) ([]Module, error) { return nil, errors.New("go list refused") })
	if _, err := depSurface(".", []string{"file"}, false); err == nil {
		t.Fatal("an unreadable build list was treated as an answer")
	}
}

// And the whole command: the dependency surface alone, with no -repo, finds
// prior art and exits 1.
func TestRunDepsSurfaceAloneFiresTheGate(t *testing.T) {
	m := writeModule(t, "github.com/go-widgets/toolkit", "v0.340.0", map[string]string{
		"a.go": "package toolkit\ntype FileTree struct{}\nfunc (f *FileTree) BeginRename(p string) {}\n",
	})
	stubOrgs(t, []string{"go-widgets"}, nil)
	stubModules(t, func(string) ([]Module, error) {
		return []Module{{Path: "example.com/main", Main: true}, m}, nil
	})

	code, out, _ := drive(t, "--deps=.", "file manager")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (prior art found); output:\n%s", code, out)
	}
	for _, want := range []string{"ALREADY REQUIRE", "FileTree", "BeginRename", "v0.340.0", "you can call them now"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
}

// Nothing in the dependencies, nothing asked elsewhere: a clean exit that says
// how many modules it covered.
func TestRunDepsSurfaceCleanNamesItsPopulation(t *testing.T) {
	m := writeModule(t, "github.com/go-widgets/toolkit", "v0.340.0", map[string]string{
		"a.go": "package toolkit\ntype Button struct{}\n",
	})
	stubOrgs(t, []string{"go-widgets"}, nil)
	stubModules(t, func(string) ([]Module, error) {
		return []Module{{Path: "example.com/main", Main: true}, m}, nil
	})

	code, out, _ := drive(t, "--deps=.", "file manager")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, out)
	}
	// ⛔ "no exported declaration carries this subject" over ZERO modules read
	// is not the same answer as over one, so the count is in the line.
	if !strings.Contains(out, "1 module(s) you require") {
		t.Errorf("a clean dependency surface did not name its population:\n%s", out)
	}
}

// With no surface at all the command refuses rather than exiting 0.
func TestRunRefusesWithNoSurface(t *testing.T) {
	code, _, errOut := drive(t, "file manager")
	if code == 0 {
		t.Fatal("exit 0 with no surface read")
	}
	if !strings.Contains(errOut, "-deps") {
		t.Errorf("the refusal does not mention the new surface: %q", errOut)
	}
}
