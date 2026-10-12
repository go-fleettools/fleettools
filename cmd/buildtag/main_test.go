package main

import (
	"bytes"
	"go/build/constraint"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustExpr(t *testing.T, s string) constraint.Expr {
	t.Helper()
	x, err := constraint.Parse("//go:build " + s)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func TestAddConstraint(t *testing.T) {
	const header = "// Copyright (c) someone\n//\n// SPDX-License-Identifier: BSD-3-Clause\n\n"
	cases := []struct {
		name, in, want string
		changed        bool
	}{
		{
			name:    "after a licence header",
			in:      header + "package vm\n",
			want:    header + "//go:build !core\n\npackage vm\n",
			changed: true,
		},
		{
			name:    "a licence header, then a package doc comment",
			in:      header + "// Package vm runs code.\npackage vm\n",
			want:    header + "//go:build !core\n\n// Package vm runs code.\npackage vm\n",
			changed: true,
		},
		{
			// No blank line closes the comment: it is the package's doc comment,
			// so the constraint goes above it, never between it and the clause.
			name:    "a doc comment attached to the package clause",
			in:      "// Package vm runs code.\npackage vm\n",
			want:    "//go:build !core\n\n// Package vm runs code.\npackage vm\n",
			changed: true,
		},
		{
			name:    "no comment at all",
			in:      "package vm\n\nfunc f() {}\n",
			want:    "//go:build !core\n\npackage vm\n\nfunc f() {}\n",
			changed: true,
		},
		{
			name:    "ANDed into an existing negated group",
			in:      header + "//go:build !(js && wasm)\n\npackage vm\n",
			want:    header + "//go:build !(js && wasm) && !core\n\npackage vm\n",
			changed: true,
		},
		{
			// An OR must keep its parentheses once it is an operand of &&.
			name:    "ANDed into an OR",
			in:      "//go:build linux || darwin\n\npackage vm\n",
			want:    "//go:build (linux || darwin) && !core\n\npackage vm\n",
			changed: true,
		},
		{
			name: "already there alone",
			in:   "//go:build !core\n\npackage vm\n",
			want: "//go:build !core\n\npackage vm\n",
		},
		{
			name: "already there as a conjunct",
			in:   "//go:build wasm && !core && js\n\npackage vm\n",
			want: "//go:build wasm && !core && js\n\npackage vm\n",
		},
	}
	add := mustExpr(t, "!core")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, changed, err := addConstraint([]byte(c.in), add)
			if err != nil {
				t.Fatal(err)
			}
			if changed != c.changed {
				t.Errorf("changed = %v, want %v", changed, c.changed)
			}
			if string(got) != c.want {
				t.Errorf("got\n%s\nwant\n%s", got, c.want)
			}
			// A second pass must be a no-op.
			again, changed2, err := addConstraint(got, add)
			if err != nil || changed2 || !bytes.Equal(again, got) {
				t.Errorf("second pass changed the file (changed=%v, err=%v)", changed2, err)
			}
		})
	}
}

func TestAddConstraintRefuses(t *testing.T) {
	cases := map[string]string{
		"legacy +build":       "// +build linux\n\npackage vm\n",
		"two go:build lines":  "//go:build linux\n//go:build darwin\n\npackage vm\n",
		"block comment":       "/* licence */\n\npackage vm\n",
		"no package clause":   "// just a comment\n",
		"unparsable existing": "//go:build linux &&\n\npackage vm\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := addConstraint([]byte(in), mustExpr(t, "!core")); err == nil {
				t.Fatal("accepted it")
			}
		})
	}
}

func TestRunWritesNothingWhenOneFileFails(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.go")
	bad := filepath.Join(dir, "bad.go")
	if err := os.WriteFile(good, []byte("package vm\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("// +build linux\n\npackage vm\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"-and", "!core", good, bad}, &out, &errb); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if b, _ := os.ReadFile(good); string(b) != "package vm\n" {
		t.Errorf("good.go was written although bad.go failed:\n%s", b)
	}
	if !strings.Contains(errb.String(), "nothing written") {
		t.Errorf("stderr does not say nothing was written: %q", errb.String())
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	b := filepath.Join(dir, "b.go")
	if err := os.WriteFile(a, []byte("package vm\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("//go:build !core\n\npackage vm\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := run([]string{"-n", "-and", "!core", a, b}, &out, &errb); code != 0 {
		t.Fatalf("dry run: exit %d: %s", code, errb.String())
	}
	if got, _ := os.ReadFile(a); string(got) != "package vm\n" {
		t.Error("-n wrote the file")
	}
	if !strings.Contains(out.String(), a) || !strings.Contains(out.String(), "1 would change, 1 already") {
		t.Errorf("dry-run report: %q", out.String())
	}

	out.Reset()
	if code := run([]string{"-and", "!core", a, b}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if got, _ := os.ReadFile(a); string(got) != "//go:build !core\n\npackage vm\n" {
		t.Errorf("a.go = %q", got)
	}
	if !strings.Contains(out.String(), "1 changed, 1 already") {
		t.Errorf("report: %q", out.String())
	}
}

func TestRunArguments(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-h"}, &out, &errb); code != 0 {
		t.Errorf("-h: exit %d", code)
	}
	for _, args := range [][]string{
		{},
		{"-and", "!core"},
		{"a.go"},
		{"-and", "linux &&", "a.go"},
		{"-and", "!core", filepath.Join(t.TempDir(), "missing.go")},
		{"-bogus"},
	} {
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
	}
}
