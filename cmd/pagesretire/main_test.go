// Copyright (c) 2026, the go-fleettools authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hugoPage is a page from the Hugo build: the theme's name is in it, which is
// what tells the two builds apart.
const hugoPage = `<!doctype html><html class="hextra-theme"><body>x</body></html>`

const mkdocsPage = `<!doctype html><html><body data-md-color-scheme="default">x</body></html>`

// branch builds a gh-pages checkout: a Hugo build under main/ holding pages,
// and one MkDocs release directory holding the pages named.
func branch(t *testing.T, mainPages, oldPages []string) string {
	t.Helper()
	dir := t.TempDir()
	for _, p := range mainPages {
		write(t, filepath.Join(dir, "main", p, "index.html"), hugoPage)
	}
	for _, p := range oldPages {
		write(t, filepath.Join(dir, "0.1", p, "index.html"), mkdocsPage)
	}
	return dir
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestItRetiresASupersededReleaseAndPointsLatestAtMain(t *testing.T) {
	dir := branch(t, []string{"", "guide", "api"}, []string{"", "guide", "api"})

	var out bytes.Buffer
	if err := run([]string{"-apply", dir}, &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "0.1")); !os.IsNotExist(err) {
		t.Errorf("0.1/ is still there")
	}
	target, err := os.Readlink(filepath.Join(dir, "latest"))
	if err != nil || target != "main" {
		t.Fatalf("latest -> %q, %v", target, err)
	}
	// The reader's path, end to end: the root redirect names latest/, latest
	// resolves to main/, and main/ is the Hugo build. That chain is the whole
	// defect this command exists for.
	root, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil || !strings.Contains(string(root), `"latest/"`) {
		t.Fatalf("the root redirect does not name latest/: %v", err)
	}
	served, err := os.ReadFile(filepath.Join(dir, "latest", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(served), "hextra") {
		t.Errorf("latest/ still serves a page that is not the Hugo build")
	}

	var list []struct {
		Version string   `json:"version"`
		Aliases []string `json:"aliases"`
	}
	b, err := os.ReadFile(filepath.Join(dir, "versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Version != "main" {
		t.Fatalf("versions.json lists %v, want main alone", list)
	}
	if len(list[0].Aliases) != 1 || list[0].Aliases[0] != "latest" {
		t.Errorf("main does not carry the latest alias: %v", list[0].Aliases)
	}
}

// ⛔ THE CONTROL. Every other test here asserts that the command removes
// something; this one asserts it can SEE a page that would be lost. Without it
// a comparison that silently read nothing would pass the whole suite.
func TestItRefusesWhenAPageExistsOnlyInTheDirectoryItWouldRemove(t *testing.T) {
	dir := branch(t, []string{"", "guide"}, []string{"", "guide", "tutorial"})

	err := run([]string{"-apply", dir}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("it removed a directory holding a page that exists nowhere else")
	}
	if !strings.Contains(err.Error(), "tutorial") {
		t.Errorf("the refusal does not name the page at risk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "0.1", "tutorial")); err != nil {
		t.Errorf("it refused and removed the page anyway: %v", err)
	}
}

func TestItRefusesWhenThereIsNoHugoBuildToPointAt(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "0.1", "index.html"), mkdocsPage)

	err := run([]string{"-apply", dir}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("it retired the only documentation on the branch")
	}
	if !strings.Contains(err.Error(), "no Hugo build") {
		t.Errorf("unhelpful refusal: %v", err)
	}
}

// A main/ that is itself the old build is the shape a half-run migration
// leaves, and retiring the releases under it would publish MkDocs as "latest"
// with nothing left to recover.
func TestItRefusesWhenMainIsNotTheHugoBuild(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main", "index.html"), mkdocsPage)
	write(t, filepath.Join(dir, "0.1", "index.html"), mkdocsPage)

	err := run([]string{"-apply", dir}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("it accepted a branch whose main/ is still MkDocs")
	}
	if !strings.Contains(err.Error(), "not the Hugo build") {
		t.Errorf("unhelpful refusal: %v", err)
	}
}

func TestItChangesNothingWithoutApply(t *testing.T) {
	dir := branch(t, []string{"", "guide"}, []string{"", "guide"})

	var out bytes.Buffer
	if err := run([]string{dir}, &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "0.1")); err != nil {
		t.Errorf("a report removed the directory: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "latest")); err == nil {
		t.Errorf("a report wrote the latest alias")
	}
	if !strings.Contains(out.String(), "would remove") {
		t.Errorf("the report does not say what it would do: %q", out.String())
	}
}

func TestItIsAnswerableTwice(t *testing.T) {
	dir := branch(t, []string{"", "guide"}, []string{"", "guide"})

	if err := run([]string{"-apply", dir}, &bytes.Buffer{}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	var out bytes.Buffer
	if err := run([]string{"-apply", dir}, &out); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to retire") {
		t.Errorf("a second run does not say it has nothing to do: %q", out.String())
	}
}

// ⛔ -h ANSWERS. A command that reads -h as a path is one somebody runs to see
// its flags and gets a deletion instead.
func TestHelpIsAnsweredAndRemovesNothing(t *testing.T) {
	dir := branch(t, []string{""}, []string{""})
	var out bytes.Buffer
	if err := run([]string{"-h", dir}, &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "usage: pagesretire") {
		t.Errorf("-h did not print the usage: %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "0.1")); err != nil {
		t.Errorf("-h removed a directory: %v", err)
	}
}

func TestItRefusesSomethingThatIsNotADirectory(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a-file")
	write(t, f, "x")
	if err := run([]string{"-apply", f}, &bytes.Buffer{}); err == nil {
		t.Fatal("it accepted a file as a gh-pages checkout")
	}
}

// 0.10 sorts after 0.9, and a branch with several releases must retire them
// all rather than the ones that happen to sort first.
func TestItRetiresEveryRelease(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main", "index.html"), hugoPage)
	for _, v := range []string{"0.1", "0.9", "0.10"} {
		write(t, filepath.Join(dir, v, "index.html"), mkdocsPage)
	}
	var out bytes.Buffer
	if err := run([]string{"-apply", dir}, &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, v := range []string{"0.1", "0.9", "0.10"} {
		if _, err := os.Stat(filepath.Join(dir, v)); !os.IsNotExist(err) {
			t.Errorf("%s/ survived", v)
		}
	}
	target, _ := os.Readlink(filepath.Join(dir, "latest"))
	if target != "main" {
		t.Errorf("latest -> %q, want main", target)
	}
}
