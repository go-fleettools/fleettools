// Copyright (c) 2026, the go-fleettools authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"os"
	"path/filepath"
	"strings"
)

// alsoUnderDocs is everything in docs/ that the nav did not account for: the
// files that are not pages, which move to static/, and the pages the nav never
// listed, which are still pages.
//
// ⛔ MkDocs SERVES docs/ AS THE SITE ROOT. So docs/assets/fs/x.png is at
// /assets/fs/x.png, docs/favicon.ico is at /favicon.ico, and a stylesheet at
// docs/css/extra.css is at /css/extra.css. Hugo serves static/ that way
// instead, which makes this a move rather than a decision — and a converter
// that skipped it lost every picture on the site. Measured where it was
// found: go-filesystems/docs, 53 images, and the LOCAL BUILD DID NOT CARE,
// because a missing image is not a build error in any static site generator.
//
// ⛔ And a .md file the nav does not name is still built by MkDocs and still
// reachable by its URL, so dropping it would lose a page that another page may
// link to. They come after the listed ones, by weight, and the caller says how
// many there were.
// orphanNames is what to call the pages the nav never named, for the report.
func orphanNames(orphans []page) []string {
	out := make([]string, 0, len(orphans))
	for _, p := range orphans {
		out = append(out, filepath.Base(p.source))
	}
	return out
}

func alsoUnderDocs(dir string, listed []page) (static map[string]string, orphans []page, err error) {
	root := filepath.Join(dir, "docs")
	known := map[string]bool{}
	for _, p := range listed {
		if p.source != "" {
			known[p.source] = true
		}
	}
	static = map[string]string{}
	weight := 1000 // after everything the nav ordered
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(path), ".md") {
			b, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			static[filepath.Join("static", rel)] = string(b)
			return nil
		}
		if known[path] {
			return nil
		}
		dest := rel
		if filepath.Base(dest) == "index.md" {
			dest = filepath.Join(filepath.Dir(dest), "_index.md")
		}
		orphans = append(orphans, page{
			source: path,
			dest:   filepath.Join("content", "en", dest),
			weight: weight,
		})
		weight += 10
		return nil
	})
	return static, orphans, err
}
