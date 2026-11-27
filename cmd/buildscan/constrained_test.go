// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const siteMod = "module example.com/site\n\ngo 1.21\n"
const toolsFile = "//go:build tools\n\npackage site\n"

// The landing-site case: the only file is `//go:build tools`, and vetting the
// module with that tag is a real pass, not "matched no packages".
func TestVetModuleVetsAConstrainedOnlyModuleWithItsTags(t *testing.T) {
	dir := writeTree(t, map[string]string{"go.mod": siteMod, "kit.go": toolsFile})
	if got := constrainedOnlyTags(dir); !reflect.DeepEqual(got, []string{"tools"}) {
		t.Fatalf("tags = %v, want [tools]", got)
	}
	if out, err := vetModule(config{goos: "linux", goarch: "amd64", tests: true}, dir, nil, nil); err != nil {
		t.Fatalf("a tools-tagged module did not pass: %v\n%s", err, out)
	}
}

// Not a pass when the emptiness has another cause: no Go file at all.
func TestVetModuleStillFailsWhenThereIsNoGoFile(t *testing.T) {
	dir := writeTree(t, map[string]string{"go.mod": siteMod})
	if got := constrainedOnlyTags(dir); got != nil {
		t.Fatalf("tags = %v for a module with no Go file", got)
	}
	if _, err := vetModule(config{goos: "linux", goarch: "amd64", tests: true}, dir, nil, nil); err == nil {
		t.Fatal("a module with no package was reported as a pass")
	}
}

// Not a pass when one file is unconstrained: that file is a package the plain
// vet would have found, so "no packages" cannot be from constraints alone.
func TestConstrainedOnlyTagsNilWithAnUnconstrainedFile(t *testing.T) {
	dir := writeTree(t, map[string]string{"go.mod": siteMod, "kit.go": toolsFile, "a.go": "package site\n"})
	if got := constrainedOnlyTags(dir); got != nil {
		t.Fatalf("tags = %v with an unconstrained file", got)
	}
}

// An unreadable file is not evidence that everything is constrained.
func TestConstrainedOnlyTagsNilWhenAFileCannotBeRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	dir := writeTree(t, map[string]string{"go.mod": siteMod, "kit.go": toolsFile, "b.go": toolsFile})
	if err := os.Chmod(filepath.Join(dir, "b.go"), 0); err != nil {
		t.Fatal(err)
	}
	if got := constrainedOnlyTags(dir); got != nil {
		t.Fatalf("tags = %v with an unreadable file", got)
	}
}

// `ignore` and negated tags are never set; a nested module is not this one's.
func TestConstrainedOnlyTagsSkipsIgnoreNegationAndNestedModules(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod":        siteMod,
		"a.go":          "//go:build tools && !windows\n\npackage site\n",
		"gen.go":        "//go:build ignore\n\npackage main\n",
		"nested/go.mod": "module example.com/n\n\ngo 1.21\n",
		"nested/n.go":   "package n\n",
	})
	if got := constrainedOnlyTags(dir); !reflect.DeepEqual(got, []string{"tools"}) {
		t.Fatalf("tags = %v, want [tools]", got)
	}
}

// Files under vendor, testdata, a dot or underscore directory are not the
// module's source: an unconstrained one there does not stop the module being
// constrained-only.
func TestConstrainedOnlyTagsIgnoresVendorTestdataAndHiddenDirs(t *testing.T) {
	for _, d := range []string{"vendor", "testdata", ".hidden", "_x"} {
		dir := writeTree(t, map[string]string{"go.mod": siteMod, "kit.go": toolsFile, d + "/u.go": "package u\n"})
		if got := constrainedOnlyTags(dir); !reflect.DeepEqual(got, []string{"tools"}) {
			t.Errorf("%s: tags = %v, want [tools]", d, got)
		}
	}
}

// A directory that cannot be listed is not evidence of anything.
func TestConstrainedOnlyTagsNilWhenADirectoryCannotBeRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	dir := writeTree(t, map[string]string{"go.mod": siteMod, "kit.go": toolsFile, "sub/b.go": toolsFile})
	if err := os.Chmod(filepath.Join(dir, "sub"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "sub"), 0o755) })
	if got := constrainedOnlyTags(dir); got != nil {
		t.Fatalf("tags = %v with an unreadable directory", got)
	}
}

// A file whose constraint does not parse is not constrained.
func TestConstrainedOnlyTagsNilWhenAConstraintDoesNotParse(t *testing.T) {
	dir := writeTree(t, map[string]string{"go.mod": siteMod, "kit.go": "//go:build (\n\npackage site\n"})
	if got := constrainedOnlyTags(dir); got != nil {
		t.Fatalf("tags = %v for a constraint that does not parse", got)
	}
}

func TestBuildExprBadConstraintAndMergeTags(t *testing.T) {
	if buildExpr("//go:build (\n\npackage x\n") != nil {
		t.Fatal("a constraint that does not parse gave an expression")
	}
	if got := mergeTags([]string{"b", "a"}, []string{"a", "c"}); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("merge = %v", got)
	}
}
