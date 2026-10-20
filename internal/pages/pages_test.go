// Copyright (c) 2026, the go-fleettools authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package pages

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func branch(t *testing.T, versions ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, v := range versions {
		if err := os.MkdirAll(filepath.Join(dir, v), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// ⛔ 0.10 SORTS AFTER 0.9 as a number and BEFORE it as a string, and a site
// that offers "latest" has to mean it. Both commands depend on this one rule.
func TestTheNewestReleaseIsFoundByNumberNotByString(t *testing.T) {
	dir := branch(t, "0.1", "0.9", "0.10", "0.2", "main")

	versions, err := Published(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := "0.10,0.9,0.2,0.1,main"
	if got := strings.Join(versions, ","); got != want {
		t.Errorf("Published = %s, want %s", got, want)
	}
}

func TestMainIsTheLatestWhenNoReleaseIsPublished(t *testing.T) {
	dir := branch(t, "main")

	versions, latest, err := Reconcile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if latest != "main" || len(versions) != 1 {
		t.Fatalf("latest = %q, versions = %v", latest, versions)
	}
	target, err := os.Readlink(filepath.Join(dir, "latest"))
	if err != nil || target != "main" {
		t.Fatalf("latest -> %q, %v", target, err)
	}
}

// An alias is a symlink, and counting it as a version would let `latest` come
// to name itself.
func TestAnAliasIsNotAVersion(t *testing.T) {
	dir := branch(t, "0.1", "main")
	if err := os.Symlink("0.1", filepath.Join(dir, "latest")); err != nil {
		t.Fatal(err)
	}
	versions, err := Published(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range versions {
		if v == "latest" {
			t.Fatalf("the alias was counted as a version: %v", versions)
		}
	}
}

func TestReconcileWritesWhatTheVersionSelectorReads(t *testing.T) {
	dir := branch(t, "0.2", "0.1", "main")

	if _, _, err := Reconcile(dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Declared here rather than reusing the writer's struct: decoding with the
	// producer's own type agrees with it by construction.
	var list []struct {
		Version    string          `json:"version"`
		Title      string          `json:"title"`
		Aliases    []string        `json:"aliases"`
		Properties map[string]bool `json:"properties,omitempty"`
	}
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].Version != "0.2" || list[2].Version != "main" {
		t.Fatalf("versions.json = %s", b)
	}
	if len(list[0].Aliases) != 1 || list[0].Aliases[0] != "latest" {
		t.Errorf("the newest release does not carry the latest alias: %v", list[0].Aliases)
	}
	if !list[2].Properties["development"] {
		t.Errorf("main is not marked development: %v", list[2].Properties)
	}
	// ⛔ .nojekyll, or GitHub Pages runs Jekyll over the build and drops every
	// directory whose name begins with an underscore.
	if _, err := os.Stat(filepath.Join(dir, ".nojekyll")); err != nil {
		t.Errorf(".nojekyll is missing: %v", err)
	}
}

func TestABranchWithNoVersionIsAnError(t *testing.T) {
	if _, err := Published(t.TempDir()); err == nil {
		t.Fatal("an empty branch was accepted")
	}
}

// Reconcile replaces an alias that names a version being removed, rather than
// following it and deleting what it points at.
func TestReconcileReplacesAStaleAlias(t *testing.T) {
	dir := branch(t, "main")
	if err := os.Symlink("0.1", filepath.Join(dir, "latest")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Reconcile(dir); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(dir, "latest"))
	if err != nil || target != "main" {
		t.Fatalf("latest -> %q, %v", target, err)
	}
}
