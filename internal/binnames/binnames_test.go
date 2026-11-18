package binnames

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

// fixture writes files (path -> content) under a fresh directory.
func fixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, c := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func names(ms []Main) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Dir+"="+m.Name)
	}
	sort.Strings(out)
	return out
}

func scan(t *testing.T, files map[string]string, opt Options) ([]Main, []Unread) {
	t.Helper()
	ms, un, err := Scan(DirTree{Root: fixture(t, files)}, opt)
	if err != nil {
		t.Fatal(err)
	}
	return ms, un
}

const mainSrc = "package main\n\nfunc main() {}\n"

func TestInstallName(t *testing.T) {
	for in, want := range map[string]string{
		"github.com/go-authn/kinit":           "kinit",
		"github.com/go-authn/kinit/cmd/x":     "x",
		"example.com/mycmd/v2":                "mycmd",
		"example.com/mycmd/v10":               "mycmd",
		"example.com/mycmd/cmd/tool/v3":       "tool",
		"example.com/mycmd/v1":                "v1", // /v1 is not a major-version suffix
		"example.com/a/v2x":                   "v2x",
		"v2":                                  "v2",
		"gopkg.in/yaml.v3":                    "yaml.v3",
		"github.com/go-authn/kinit/cmd/x/v2/": "x",
	} {
		if got := InstallName(in); got != want {
			t.Errorf("InstallName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestACmdDirectory(t *testing.T) {
	ms, un := scan(t, map[string]string{
		"go.mod":          "module github.com/o/r\n",
		"cmd/x/main.go":   mainSrc,
		"cmd/y/main.go":   mainSrc,
		"lib/lib.go":      "package lib\n",
		"cmd/x/x_test.go": "package main\n",
	}, Options{RepoName: "r"})
	if got, want := names(ms), []string{"cmd/x=x", "cmd/y=y"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if len(un) != 0 {
		t.Fatalf("unread %v", un)
	}
	if ms[0].Import != "github.com/o/r/cmd/x" {
		t.Fatalf("import path %q", ms[0].Import)
	}
}

// The case that started this: a main at the module root is named for the
// module, not for anything the README says.
func TestARootMainIsNamedForTheModule(t *testing.T) {
	ms, _ := scan(t, map[string]string{
		"go.mod":  "module github.com/go-authn/kinit\n",
		"main.go": mainSrc,
	}, Options{RepoName: "whatever-the-directory-is-called"})
	if got, want := names(ms), []string{"=kinit"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAMajorVersionModuleIsNamedForItsPredecessor(t *testing.T) {
	ms, _ := scan(t, map[string]string{
		"go.mod":        "module github.com/o/mycmd/v2\n",
		"main.go":       mainSrc,
		"cmd/a/main.go": mainSrc,
	}, Options{})
	if got, want := names(ms), []string{"=mycmd", "cmd/a=a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestANestedModuleOwnsItsDirectory(t *testing.T) {
	ms, _ := scan(t, map[string]string{
		"go.mod":         "module github.com/o/outer\n",
		"tools/go.mod":   "module github.com/o/innertool\n",
		"tools/main.go":  mainSrc,
		"tools/sub/m.go": mainSrc,
	}, Options{})
	if got, want := names(ms), []string{"tools/sub=sub", "tools=innertool"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if ms[1].Import != "github.com/o/innertool/sub" {
		t.Fatalf("import %q", ms[1].Import)
	}
}

func TestWhatGoInstallDoesNotBuild(t *testing.T) {
	ms, _ := scan(t, map[string]string{
		"go.mod":             "module github.com/o/r\n",
		"gen.go":             "//go:build ignore\n\npackage main\n",
		"lib.go":             "package r\n",
		"testdata/x/main.go": mainSrc,
		"vendor/v/main.go":   mainSrc,
		"_old/main.go":       mainSrc,
		".hidden/main.go":    mainSrc,
		"tools/only_test.go": "package main\n",
	}, Options{})
	if len(ms) != 0 {
		t.Fatalf("expected no main, got %v", names(ms))
	}
}

func TestAnIgnoredFileDoesNotHideTheRealPackage(t *testing.T) {
	ms, _ := scan(t, map[string]string{
		"go.mod":   "module github.com/o/r\n",
		"a_gen.go": "//go:build ignore\n\npackage gen\n",
		"b.go":     mainSrc,
	}, Options{})
	if got, want := names(ms), []string{"=r"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestNoGoMod(t *testing.T) {
	ms, _ := scan(t, map[string]string{"main.go": mainSrc, "cmd/z/main.go": mainSrc}, Options{RepoName: "legacy"})
	if got, want := names(ms), []string{"=legacy", "cmd/z=z"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for _, m := range ms {
		if m.Note == "" {
			t.Fatalf("%v: a guessed name must say so", m)
		}
	}
}

type brokenTree struct{ Tree }

func (b brokenTree) Read(p string) ([]byte, error) {
	if strings.HasSuffix(p, ".go") {
		return nil, errors.New("boom")
	}
	return b.Tree.Read(p)
}

// A directory whose files cannot be read is UNREAD, never "not a main".
func TestUnreadableIsNotNoMain(t *testing.T) {
	root := fixture(t, map[string]string{"go.mod": "module m\n", "main.go": mainSrc})
	ms, un, err := Scan(brokenTree{DirTree{Root: root}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 0 || len(un) != 1 || !strings.Contains(un[0].Why, "boom") {
		t.Fatalf("mains %v unread %v", ms, un)
	}
}

func TestGitTreeReadsARefAndNotTheWorkingTree(t *testing.T) {
	root := fixture(t, map[string]string{"go.mod": "module github.com/o/fromgit\n", "main.go": mainSrc})
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("add", "-A")
	git("commit", "-q", "-m", "x")
	git("tag", "published")
	// The working tree moves on: the tag must not.
	if err := os.Remove(filepath.Join(root, "main.go")); err != nil {
		t.Fatal(err)
	}
	ms, _, err := Scan(GitTree{Dir: root, Ref: "published"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := names(ms), []string{"=fromgit"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if _, _, err := Scan(GitTree{Dir: root, Ref: "nope"}, Options{}); err == nil {
		t.Fatal("a ref that does not exist must be an error")
	}
}

func TestRemoteTree(t *testing.T) {
	var calls int
	api := func(args ...string) ([]byte, error) {
		calls++
		return []byte(`{"truncated":false,"tree":[
		 {"path":"go.mod","type":"blob"},{"path":"cmd/a/main.go","type":"blob"},
		 {"path":"docs/x.go","type":"blob"},{"path":"cmd","type":"tree"}]}`), nil
	}
	var fetched []string
	raw := func(u string) ([]byte, error) {
		fetched = append(fetched, u)
		if strings.HasSuffix(u, "go.mod") {
			return []byte("module github.com/o/r\n"), nil
		}
		return []byte(mainSrc), nil
	}
	b := &Budget{Cap: 600}
	ms, un, err := Scan(&Remote{Repo: "o/r", Branch: "main", Budget: b, API: api, Raw: raw},
		Options{RepoName: "r", Candidate: RemoteCandidate})
	if err != nil || len(un) != 0 {
		t.Fatalf("%v %v", err, un)
	}
	if got, want := names(ms), []string{"cmd/a=a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if calls != 1 || b.Used != 1 {
		t.Fatalf("one tree call expected, got %d calls, %d spent", calls, b.Used)
	}
	for _, u := range fetched {
		if strings.Contains(u, "docs/x.go") {
			t.Fatalf("docs/x.go is not a candidate directory but was fetched")
		}
	}
}

func TestRemoteTruncatedAndCapped(t *testing.T) {
	trunc := func(...string) ([]byte, error) { return []byte(`{"truncated":true,"tree":[]}`), nil }
	if _, err := (&Remote{Repo: "o/r", Branch: "m", Budget: &Budget{Cap: 5}, API: trunc}).Files(); err == nil ||
		!strings.Contains(err.Error(), "truncated") {
		t.Fatalf("a truncated tree is partial and must be refused, got %v", err)
	}
	b := &Budget{Cap: 1}
	ok := func(...string) ([]byte, error) { return []byte(`{"tree":[]}`), nil }
	r := &Remote{Repo: "o/r", Branch: "m", Budget: b, API: ok}
	if _, err := r.Files(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Files(); !errors.Is(err, ErrCap) {
		t.Fatalf("second call must hit the cap, got %v", err)
	}
	if b.Used != 1 {
		t.Fatalf("a refused call must not be charged: %d", b.Used)
	}
}

func TestNormalise(t *testing.T) {
	for in, want := range map[string]string{"Kinit.EXE": "kinit", "kinit": "kinit", "KINIT.exe": "kinit", "a.exe.exe": "a.exe"} {
		if got := Normalise(in); got != want {
			t.Errorf("Normalise(%q) = %q want %q", in, got, want)
		}
	}
}

func refs(t *testing.T) Index {
	t.Helper()
	idx, err := loadRefs(fstest.MapFS{
		"ref/a.txt": {Data: []byte("# list: A list\n# source: https://example.org/a\n# fetched: 2026-01-01\nkinit\tMIT krb5\nLS\tcoreutils\n")},
		"ref/b.txt": {Data: []byte("# source: https://example.org/b\nklist.exe\twindows\nkvno\tmit\thttps://example.org/own-page\n")},
	}, "ref")
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestCheckIsCaseAndExeInsensitiveAndCitesItsSource(t *testing.T) {
	idx := refs(t)
	for _, n := range []string{"kinit", "Kinit", "KINIT.exe", "ls", "klist"} {
		c, bad := idx.Check(n, nil)
		if !bad {
			t.Errorf("%q must collide", n)
			continue
		}
		if d := c.Describe(); !strings.Contains(d, "https://example.org/") {
			t.Errorf("%q: the description cites no source: %s", n, d)
		}
	}
	if c, _ := idx.Check("kvno", nil); !strings.Contains(c.Describe(), "own-page") || strings.Contains(c.Describe(), "example.org/b]") {
		t.Errorf("a line's own source must replace the file's: %s", c.Describe())
	}
	if _, bad := idx.Check("authn-kinit", nil); bad {
		t.Error("authn-kinit must not collide")
	}
	if _, bad := idx.Check("kinit", map[string]bool{"kinit": true}); bad {
		t.Error("an allowed name must not collide")
	}
}

func TestAListWithoutASourceOrWithoutNamesIsRefused(t *testing.T) {
	if _, err := loadRefs(fstest.MapFS{"ref/a.txt": {Data: []byte("kinit\n")}}, "ref"); err == nil {
		t.Error("a list that cites nothing must be refused")
	}
	if _, err := loadRefs(fstest.MapFS{"ref/a.txt": {Data: []byte("# source: x\n")}}, "ref"); err == nil {
		t.Error("an empty reference would call everything clean")
	}
}

// The committed lists must know the commands that motivated this tool.
func TestTheCommittedListsCoverTheCases(t *testing.T) {
	idx, err := LoadRefs()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"kinit", "klist", "ls", "ssh", "git", "docker", "kubectl", "java", "python3", "node", "systemctl", "ip", "ipconfig", "diskutil", "cat"} {
		if _, bad := idx.Check(n, nil); !bad {
			t.Errorf("%s is a well-known command and is missing from the reference", n)
		}
	}
	for _, n := range []string{"authn-kinit", "gotest", "prwait", "binnames"} {
		if _, bad := idx.Check(n, nil); bad {
			t.Errorf("%s collides with the reference", n)
		}
	}
	for _, rs := range idx {
		for _, r := range rs {
			if r.Source == "" || r.Fetched == "" {
				t.Fatalf("%s: a name without a dated source", r.Name)
			}
		}
	}
}

func TestPathSupplement(t *testing.T) {
	dir := fixture(t, map[string]string{"hosttool": "#!/bin/sh\n", "notexec": "x\n"})
	if err := os.Chmod(filepath.Join(dir, "hosttool"), 0o755); err != nil {
		t.Fatal(err)
	}
	own := fixture(t, map[string]string{"mine": "#!/bin/sh\n"})
	if err := os.Chmod(filepath.Join(own, "mine"), 0o755); err != nil {
		t.Fatal(err)
	}
	idx := refs(t)
	read, failed := idx.PathSupplement(dir+string(os.PathListSeparator)+own+string(os.PathListSeparator)+"/no/such/dir",
		map[string]bool{own: true})
	if len(failed) != 0 || len(read) != 1 || read[0] != dir {
		t.Fatalf("read %v failed %v", read, failed)
	}
	c, bad := idx.Check("HostTool", nil)
	if !bad || !c.Owners[0].Host || !strings.Contains(c.Describe(), "host's PATH") {
		t.Fatalf("a PATH command must collide and say it came from the host: %+v", c)
	}
	if _, bad := idx.Check("notexec", nil); bad {
		t.Error("a file without the execute bit is not a command")
	}
	if _, bad := idx.Check("mine", nil); bad {
		t.Error("a skipped directory must not contribute")
	}
}
