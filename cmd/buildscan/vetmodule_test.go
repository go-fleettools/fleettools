// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeGo puts a `go` on PATH that fails `go list` and passes everything else.
func fakeGo(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell shim")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = list ]; then echo boom; exit 1; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// With nothing exempt the vet is one `go vet ./...`: no package list is asked for.
func TestVetModuleWithoutExemptionsDoesNotListPackages(t *testing.T) {
	fakeGo(t)
	if _, err := vetModule(config{goos: "linux", goarch: "amd64"}, t.TempDir(), nil); err != nil {
		t.Fatalf("a plain vet asked for a package list: %v", err)
	}
}

// A package list that fails must fail the vet, never leave only the exempt
// packages vetted and call it a pass.
func TestVetModuleFailsWhenThePackageListFails(t *testing.T) {
	fakeGo(t)
	_, err := vetModule(config{goos: "linux", goarch: "amd64"}, t.TempDir(), map[string][]string{"seeker": {"-stdmethods=false"}})
	if err == nil {
		t.Fatal("go list failed and the vet passed")
	}
}
