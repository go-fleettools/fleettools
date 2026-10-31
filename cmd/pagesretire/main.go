// Copyright (c) 2026, the go-fleettools authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

// Command pagesretire removes the superseded MkDocs version directories from a
// documentation branch, so that `latest` names the Hugo build.
//
//	pagesretire [-apply] <gh-pages checkout>
//
// ⛔ IT EXISTS BECAUSE THE MIGRATION SUCCEEDED AND NOBODY COULD SEE IT. Every
// one of the 77 converted sites publishes a Hugo build, under the version in
// params.docs.version — "main", for a repository with no release tagged. But
// mike had left a release directory (0.1, sometimes 0.2) holding the MkDocs
// build, and `latest` is the newest RELEASE, so the alias went on naming the
// old build. The branch root redirects every reader to latest/. Measured on
// 2026-10-20: 75 of 77 sites served MkDocs at the only URL anything links to,
// and the two exceptions are the two repositories that never published a
// release, where the alias fell back to main.
//
// ⛔ AND IT REFUSES RATHER THAN GUESS. Deleting a published version directory
// cannot be undone from the branch, so every page under it must be shown to
// exist in the Hugo build first. Across the fleet that check read 514 pages
// against 673 and found none missing — but it is run per repository, because
// "it was true for the others" is not a measurement of this one.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	pagesrules "github.com/go-fleettools/fleettools/internal/pages"
	"github.com/spf13/cobra"
)

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

const usage = `usage: pagesretire [--apply] <gh-pages checkout>

Removes the version directories a documentation branch kept from MkDocs, so
that the "latest" alias names the Hugo build in main/ instead of a release
directory nothing can rebuild. Without --apply it only reports.

It refuses unless main/ holds the Hugo build and every page under the
directories it would remove also exists there.`

// RootCmd builds the command. Tests drive it through run, below.
//
// --apply was hand-parsed before, accepting both -apply and --apply; pflag
// takes the second, and internal/cli's shim rewrites the first with a warning,
// so every existing call site still reaches it.
func RootCmd() *cobra.Command {
	var apply bool
	c := cli.New("pagesretire [--apply] <gh-pages checkout>",
		"Remove the version directories a docs branch kept from MkDocs", usage)
	// The refusal IS the usage, as it was: an arity message tells the reader
	// how many arguments, not what to type.
	c.Args = func(_ *cobra.Command, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("%s", usage)
		}
		return nil
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return retire(cli.Out(cmd), args[0], apply)
	}
	c.Flags().BoolVar(&apply, "apply", false,
		"remove the directories, instead of only reporting what would go")
	return c
}

// run keeps the signature this command's tests were written against -- an
// error rather than a status, because its failures are sentences.
func run(args []string, out io.Writer) error {
	return cli.Exec(RootCmd(), args, out, out)
}

func retire(out io.Writer, dir string, apply bool) error {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}

	// ⛔ The Hugo build has to be there before anything is removed. On a branch
	// whose main/ is missing or is itself MkDocs, retiring the releases would
	// leave the site with no current documentation at all.
	hugo := filepath.Join(dir, "main", "index.html")
	b, err := os.ReadFile(hugo)
	if err != nil {
		return fmt.Errorf("%s is missing: this branch has no Hugo build to point latest at", hugo)
	}
	// The theme's name appears in the markup's class names and asset paths, and
	// nowhere in a MkDocs Material page.
	if !strings.Contains(string(b), "hextra") {
		return fmt.Errorf("%s is not the Hugo build (no sign of the theme in it)", hugo)
	}

	superseded, err := supersededDirs(dir)
	if err != nil {
		return err
	}
	if len(superseded) == 0 {
		fmt.Fprintf(out, "nothing to retire: main/ is the only version directory\n")
		return nil
	}

	kept := pageSet(filepath.Join(dir, "main"))
	if len(kept) == 0 {
		return fmt.Errorf("read no pages under main/, so nothing could be compared against it")
	}
	var lost []string
	read := 0
	for _, v := range superseded {
		got := pageSet(filepath.Join(dir, v))
		// ⛔ An unreadable directory and a duplicated one both yield "no page
		// missing". Counting what was read is what tells them apart.
		if len(got) == 0 {
			return fmt.Errorf("read no pages under %s/, so removing it cannot be justified", v)
		}
		read += len(got)
		for p := range got {
			if !kept[p] {
				lost = append(lost, v+"/"+p)
			}
		}
	}
	if len(lost) > 0 {
		sort.Strings(lost)
		return fmt.Errorf("%d page(s) exist only in the directories to remove, starting with %s",
			len(lost), strings.Join(first(lost, 5), ", "))
	}

	fmt.Fprintf(out, "%d page(s) under %s are all present in main/ (%d pages)\n",
		read, strings.Join(superseded, ", "), len(kept))
	if !apply {
		fmt.Fprintf(out, "would remove %s and point latest at main (use -apply)\n",
			strings.Join(superseded, ", "))
		return nil
	}
	for _, v := range superseded {
		if err := os.RemoveAll(filepath.Join(dir, v)); err != nil {
			return err
		}
	}
	versions, latest, err := pagesrules.Reconcile(dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "removed %s; latest is %s; versions: %s\n",
		strings.Join(superseded, ", "), latest, strings.Join(versions, ", "))
	return nil
}

// supersededDirs is every published version directory other than main.
func supersededDirs(dir string) ([]string, error) {
	versions, err := pagesrules.Published(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, v := range versions {
		if v != "main" {
			out = append(out, v)
		}
	}
	return out, nil
}

// pageSet is the set of page paths under one version directory, as a reader
// addresses them.
//
// ⛔ Assets are excluded deliberately. MkDocs Material and Hextra ship entirely
// different stylesheets, fonts and search indexes, so comparing every file
// would report hundreds of "losses" that are the old theme's own machinery.
// The question is about documentation, and a page is an index.html.
func pageSet(dir string) map[string]bool {
	out := map[string]bool{}
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "index.html" {
			return nil
		}
		rel, err := filepath.Rel(dir, filepath.Dir(path))
		if err != nil {
			return nil
		}
		if rel == "." {
			rel = ""
		}
		out[filepath.ToSlash(rel)] = true
		return nil
	})
	return out
}

func first(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(append([]string{}, s[:n]...), fmt.Sprintf("… and %d more", len(s)-n))
}
