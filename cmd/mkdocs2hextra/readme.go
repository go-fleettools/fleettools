// Copyright (c) 2026, the go-fleettools authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// README handling, because in this fleet a change a reader can see is not
// finished until the README says it.
//
// ⛔ MEASURED FIRST: of the 84 sites, 46 have a README and 38 have NONE. Of
// those 46, 43 say "mkdocs serve" and "pip install -r requirements.txt", 38 say
// "MkDocs Material", 31 say "versioned with mike" and 27 carry the same "mike
// deploy --push --update-aliases" release recipe. So the common case is one
// boilerplate with three recognisable parts, and the right tool rewrites those
// three parts rather than the whole file: a README may also carry a brand
// banner, a description nobody else has, and links worth keeping.
//
// ⛔ AND WHEN IT RECOGNISES NOTHING IT SAYS SO rather than appending. A README
// this cannot place is a README a person has to read, and a converter that
// silently added a section would leave two contradictory sets of instructions
// in 46 repositories.

const (
	mkdocsBuiltWith = "[MkDocs Material](https://squidfunk.github.io/mkdocs-material/)"
	mikeLink        = "[mike](https://github.com/jimporter/mike)"
)

// updateREADME returns the README a converted site should have, and whether
// anything was recognised. An empty `current` means the repository has none.
func updateREADME(current, repo, siteURL string) (out string, changed bool) {
	if strings.TrimSpace(current) == "" {
		return newREADME(repo, siteURL), true
	}
	out = current

	// 1. What it is built with.
	if i := strings.Index(out, mkdocsBuiltWith); i >= 0 {
		// The sentence runs to the next full stop, and both forms occur:
		// "built with MkDocs Material and versioned with mike." and
		// "built with MkDocs Material." alone.
		end := strings.Index(out[i:], ".")
		if end < 0 {
			end = len(out) - i
		}
		out = out[:i] + hugoBuiltWith + out[i+end+1:]
		changed = true
	}

	// 2. The local preview: a Python virtual environment becomes a submodule
	// and a server.
	if a, b, ok := fenceAfter(out, "## Local preview"); ok {
		out = out[:a] + localPreview + out[b:]
		changed = true
	}

	// 3. The release recipe. mike's two commands become the sentence that says
	// where the version comes from, because with Hugo it is a field in a file
	// rather than a command somebody has to remember to run.
	for _, heading := range []string{"## Releasing a new docs version", "## Releasing a new version", "## Releasing"} {
		if a, b, ok := fenceAfter(out, heading); ok {
			out = out[:a] + versionsText(repo) + out[b:]
			changed = true
			break
		}
	}
	return out, changed
}

const hugoBuiltWith = "[Hugo](https://gohugo.io/) and this fleet's " +
	"[Hextra fork](https://github.com/tannevaled/hextra) — upstream Hextra plus " +
	"the opt-in features the fleet's sites use."

const localPreview = "```bash\n" +
	"git submodule update --init --recursive   # themes/hextra, pinned at a release tag\n" +
	"hugo server                               # http://localhost:1313\n" +
	"```"

func versionsText(repo string) string {
	return "Each documented version is its own build under `/docs/<version>/`, and\n" +
		"`/docs/latest/` follows the newest — which is what `mike` did before. The version\n" +
		"comes from `params.docs.version` in `hugo.yaml`: set it to the release the pages\n" +
		"describe (`v0.16.0` → `/docs/0.16/`), or leave it at `main` while no release is\n" +
		"tagged. The `docs` workflow reads it, builds with that `--baseURL`, and replaces\n" +
		"**only** that version's path.\n\n" +
		"⛔ The build runs with `--panicOnWarning`, so a link to a page that is not there\n" +
		"fails the lane instead of 404ing for a reader."
}

// fenceAfter finds the fenced code block that follows a heading, and returns
// the span to replace.
//
// ⛔ THE FIRST FENCE AFTER THE HEADING, and nothing if another heading comes
// first: a section whose commands were already removed must not have the
// NEXT section's block rewritten into it.
func fenceAfter(s, heading string) (start, end int, ok bool) {
	h := strings.Index(s, heading)
	if h < 0 {
		return 0, 0, false
	}
	rest := s[h+len(heading):]
	open := strings.Index(rest, "```")
	next := strings.Index(rest, "\n## ")
	if open < 0 || (next >= 0 && next < open) {
		return 0, 0, false
	}
	after := rest[open+3:]
	close := strings.Index(after, "```")
	if close < 0 {
		return 0, 0, false
	}
	return h + len(heading) + open, h + len(heading) + open + 3 + close + 3, true
}

// repoName is "org/repo", from the site's own URL when it has one and from the
// directory otherwise.
//
// ⛔ Not from the directory alone: these checkouts are laid out as
// <root>/<org>/<repo>, so the directory gives the right answer — but a site
// cloned anywhere else would be named after its parent folder, and a README
// naming the wrong repository is worse than one naming none.
func repoName(dir string, s site) string {
	if s.RepoURL != "" {
		if _, path, ok := strings.Cut(strings.TrimSuffix(s.RepoURL, "/"), "github.com/"); ok {
			name := path
			if strings.Count(name, "/") == 0 {
				// repo_url often names the ORGANISATION rather than the
				// repository, which is how 21 of these READMEs are written.
				return name + "/" + filepath.Base(dir)
			}
			return name
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return filepath.Base(dir)
	}
	return filepath.Join(filepath.Base(filepath.Dir(abs)), filepath.Base(abs))
}

// newREADME is written for the 38 sites that have none at all.
func newREADME(repo, siteURL string) string {
	org := repo
	if i := strings.Index(repo, "/"); i > 0 {
		org = repo[:i]
	}
	url := siteURL
	if url == "" {
		url = "https://" + org + ".github.io/docs/"
	}
	return fmt.Sprintf(`# %s

Versioned documentation for [%s](https://github.com/%s), built with
[Hugo](https://gohugo.io/) and this fleet's
[Hextra fork](https://github.com/tannevaled/hextra). Published to the
`+"`gh-pages`"+` branch and served at <%s>.

⛔ What this replaced was MkDocs Material plus mike, which needed a Python
toolchain, a `+"`requirements.txt`"+` and a `+"`pip install`"+` in CI to render a static
site for a pure-Go organisation.

## Local preview

%s

## Versions

%s
`, repo, org, org, url, localPreview, versionsText(repo))
}
