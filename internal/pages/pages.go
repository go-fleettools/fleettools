// Copyright (c) 2026, the go-fleettools authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

// Package pages holds the rules a documentation branch obeys: which
// directories are versions, which version "latest" names, and the three files
// that follow from that.
//
// ⛔ IT IS SHARED BECAUSE TWO COMMANDS DECIDE THE SAME THING. pagespublish
// writes a version and then reconciles the branch; pagesretire removes
// superseded versions and then reconciles the branch. A second copy of "newest
// release, or main when none" is a second answer to the question `/latest/`
// asks, and the branch can only have one.
package pages

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// VersionRE matches a published documentation version: MAJOR.MINOR.
var VersionRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// Published is the versions in the branch, newest release first and main last.
//
// ⛔ 0.10 SORTS AFTER 0.9, which string order gets wrong — and a documentation
// site that offers "latest" has to mean it.
func Published(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var releases []string
	hasMain := false
	for _, e := range entries {
		// A symlink is an alias (latest), not a version.
		fi, err := os.Lstat(filepath.Join(dir, e.Name()))
		if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			continue
		}
		switch {
		case e.Name() == "main":
			hasMain = true
		case VersionRE.MatchString(e.Name()):
			releases = append(releases, e.Name())
		}
	}
	sort.Slice(releases, func(i, j int) bool {
		ai, bi := split(releases[i]), split(releases[j])
		if ai[0] != bi[0] {
			return ai[0] > bi[0]
		}
		return ai[1] > bi[1]
	})
	if hasMain {
		releases = append(releases, "main")
	}
	if len(releases) == 0 {
		return nil, fmt.Errorf("%s holds no version directory", dir)
	}
	return releases, nil
}

func split(v string) [2]int {
	parts := strings.SplitN(v, ".", 2)
	a, _ := strconv.Atoi(parts[0])
	b, _ := strconv.Atoi(parts[1])
	return [2]int{a, b}
}

// Reconcile rewrites versions.json, the latest link and the root redirect from
// the version directories present, and reports the versions and which one
// latest now names.
func Reconcile(dir string) (versions []string, latest string, err error) {
	versions, err = Published(dir)
	if err != nil {
		return nil, "", err
	}
	latest = versions[0]
	if err := WriteVersions(dir, versions, latest); err != nil {
		return nil, "", err
	}
	if err := WriteLatest(dir, latest); err != nil {
		return nil, "", err
	}
	if err := WriteRedirect(dir); err != nil {
		return nil, "", err
	}
	return versions, latest, nil
}

type entry struct {
	Version    string          `json:"version"`
	Title      string          `json:"title"`
	Aliases    []string        `json:"aliases"`
	Properties map[string]bool `json:"properties,omitempty"`
}

// WriteVersions writes mike's versions.json, which the Hextra fork's version
// selector reads.
func WriteVersions(dir string, versions []string, latest string) error {
	list := make([]entry, 0, len(versions))
	for _, v := range versions {
		e := entry{Version: v, Title: v, Aliases: []string{}}
		if v == latest {
			e.Aliases = []string{"latest"}
		}
		if v == "main" {
			e.Properties = map[string]bool{"development": true}
		}
		list = append(list, e)
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "versions.json"), append(b, '\n'), 0o644)
}

// WriteLatest points the latest alias at one version.
func WriteLatest(dir, latest string) error {
	link := filepath.Join(dir, "latest")
	if err := os.RemoveAll(link); err != nil {
		return err
	}
	return os.Symlink(latest, link)
}

const redirect = `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>Redirecting</title>
  <noscript>
    <meta http-equiv="refresh" content="1; url=latest/" />
  </noscript>
  <script>
    window.location.replace(
      "latest/" + window.location.search + window.location.hash
    );
  </script>
</head>
<body>
  Redirecting to <a href="latest/">latest/</a>...
</body>
</html>
`

// WriteRedirect writes the branch root, which sends every reader to latest/.
func WriteRedirect(dir string) error {
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(redirect), 0o644); err != nil {
		return err
	}
	// ⛔ .nojekyll, or GitHub Pages runs Jekyll over the build and drops every
	// directory whose name begins with an underscore — which is most of what
	// Hugo writes.
	return os.WriteFile(filepath.Join(dir, ".nojekyll"), nil, 0o644)
}
