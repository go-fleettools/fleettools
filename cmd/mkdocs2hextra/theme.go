// Copyright (c) 2026, the go-fleettools authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// pinTheme adds themes/hextra as a submodule at the pinned tag, or moves an
// existing one to it.
//
// ⛔ A SUBMODULE AND NOT A HUGO MODULE, which is the fleet's existing choice
// and worth keeping for one measured reason: go-pdfkit/docs imports
// `github.com/imfing/hextra` as a Hugo module and therefore builds UPSTREAM
// Hextra, without the fork's opt-in features. The seven sites already on the
// fork use a submodule. Two mechanisms for one theme is how half a fleet ends
// up on a different theme from the other half without anybody choosing it.
//
// ⛔ And it is pinned to a TAG, read back afterwards. `git submodule add`
// leaves the default branch checked out, so a repository that never says
// otherwise tracks whatever the fork's main does next.
func pinTheme(dir string) error {
	path := filepath.Join(dir, "themes", "hextra")
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		if out, err := git(dir, "submodule", "add", "-f",
			"https://github.com/tannevaled/hextra.git", "themes/hextra"); err != nil {
			return fmt.Errorf("adding the submodule: %v: %s", err, out)
		}
	}
	if out, err := git(path, "fetch", "--tags", "--quiet", "origin"); err != nil {
		return fmt.Errorf("fetching the fork's tags: %v: %s", err, out)
	}
	if out, err := git(path, "checkout", "--quiet", hextraPin); err != nil {
		return fmt.Errorf("checking out %s: %v: %s", hextraPin, err, out)
	}
	// Read it back: a checkout that silently stayed where it was is the whole
	// hazard this function exists for.
	at, err := git(path, "describe", "--tags", "--exact-match", "HEAD")
	if err != nil || strings.TrimSpace(at) != hextraPin {
		return fmt.Errorf("themes/hextra is at %q, not %s", strings.TrimSpace(at), hextraPin)
	}
	return nil
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	b, err := cmd.CombinedOutput()
	return string(b), err
}

// workflow is the CI for a converted site: build, check the links offline, and
// publish one version's path.
//
// ⛔ IT PUBLISHES PER VERSION, replacing only the version being built. 82 of
// the 85 MkDocs sites used mike, which does exactly that, and a workflow that
// pushed the whole of gh-pages would delete every other version on the first
// run.
//
// ⛔ --panicOnWarning IS THE POINT OF THE REWRITE. A relref to a page that is
// not there is a warning in Hugo and an error here, which is what makes the
// 1 252 converted links checkable rather than hopeful.
func workflow(s site, version string) string {
	base := strings.TrimSuffix(s.URL, "/")
	if base == "" {
		base = "https://example.github.io/docs"
	}
	return fmt.Sprintf(`name: docs

# Every documentation version is a separate Hugo build, published under
# %s/<version>/ on the gh-pages branch, where <version> is
# the minor version of the code the pages describe (params.docs.version in
# hugo.yaml: v0.3.1 -> 0.3), or "main" while no release is tagged. Each push to
# main replaces that version's path and leaves the others alone. Pull requests
# build and check, and publish nothing.
#
# ⛔ This replaced MkDocs Material plus mike: a Python toolchain, a
# requirements.txt and a pip install to render a static site for a pure-Go
# organisation.

on:
  push:
    branches: [main]
  pull_request:
  workflow_dispatch:

permissions:
  contents: read

env:
  HUGO_VERSION: "0.167.0"
  # sha256 of hugo_${HUGO_VERSION}_linux-amd64.tar.gz, from the release's checksums.txt.
  HUGO_SHA256: "4d84519b9f619e6d4c3fb45a50157abeabeb724f859c60605f44c23def6e1169"
  DOCS_URL: "%s"

jobs:
  build:
    runs-on: ubuntu-latest
    outputs:
      version: ${{ steps.version.outputs.version }}
    steps:
      - name: Checkout
        uses: actions/checkout@v7
        with:
          # themes/hextra is the tannevaled/hextra fork, pinned to a release tag.
          submodules: recursive
          # The whole history: each page's date comes from git.
          fetch-depth: 0

      - name: Install Hugo ${{ env.HUGO_VERSION }}
        run: |
          curl -fsSL -o /tmp/hugo.tar.gz \
            "https://github.com/gohugoio/hugo/releases/download/v${HUGO_VERSION}/hugo_${HUGO_VERSION}_linux-amd64.tar.gz"
          echo "${HUGO_SHA256}  /tmp/hugo.tar.gz" | sha256sum -c -
          sudo tar -xzf /tmp/hugo.tar.gz -C /usr/local/bin hugo
          hugo version

      - name: Version
        id: version
        run: |
          documented=$(hugo config --format json | jq -r '.params.docs.version')
          if [ "${documented}" = main ]; then
            version=main
          else
            version=$(printf '%%s\n' "${documented}" | sed -nE 's/^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/\1.\2/p')
          fi
          if [ -z "${version}" ]; then
            echo "::error::params.docs.version in hugo.yaml is \"${documented}\", not vMAJOR.MINOR.PATCH or main"
            exit 1
          fi
          echo "version=${version}" >> "$GITHUB_OUTPUT"
          echo "documented ${documented}: documentation version ${version}"

      # --panicOnWarning: a relref to a page that is not there, a deprecated
      # setting or a template warning fails the build rather than scrolling by.
      - name: Build
        env:
          HUGO_ENVIRONMENT: production
        run: >-
          hugo --gc --minify --panicOnWarning --printPathWarnings
          --baseURL "${DOCS_URL}/${{ steps.version.outputs.version }}/"

      - name: Lay out the site for the link check
        run: |
          mkdir -p _check/docs
          cp -R public "_check/docs/${{ steps.version.outputs.version }}"

      - name: Check links
        uses: lycheeverse/lychee-action@v2
        with:
          args: >-
            --offline --no-progress --include-fragments
            --index-files index.html
            --root-dir ${{ github.workspace }}/_check
            '_check/docs/**/*.html'
          fail: true

      - uses: actions/upload-artifact@v7
        with:
          name: site
          path: public
          retention-days: 7

  deploy:
    if: github.event_name != 'pull_request' && github.ref == 'refs/heads/main'
    needs: build
    # Never two deploys at once: both would push gh-pages and the second would
    # be rejected. cancel-in-progress stays false — killing a publish half way
    # through is worse than making it wait.
    concurrency:
      group: gh-pages-deploy
      cancel-in-progress: false
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@v7
        with:
          path: gh-pages
          fetch-depth: 0

      - name: Switch to gh-pages
        working-directory: gh-pages
        run: |
          if git ls-remote --exit-code --heads origin gh-pages >/dev/null; then
            git fetch -q origin gh-pages
            git checkout -q -B gh-pages FETCH_HEAD
          else
            echo "no gh-pages branch yet: starting one"
            git checkout -q --orphan gh-pages
            git rm -rqf .
            git clean -fdxq
          fi

      - uses: actions/download-artifact@v8
        with:
          name: site
          path: site

      # Only the version being built is replaced; every other version stays as
      # it is, and /docs/latest/ follows the newest.
      - name: Publish ${{ needs.build.outputs.version }}
        working-directory: gh-pages
        run: |
          version="${{ needs.build.outputs.version }}"
          rm -rf "docs/${version}"
          mkdir -p "docs/${version}"
          cp -R ../site/. "docs/${version}/"
          newest=$(ls docs | grep -E '^[0-9]+\.[0-9]+$' | sort -t. -k1,1n -k2,2n | tail -1 || true)
          if [ -n "${newest}" ]; then
            rm -rf docs/latest
            cp -R "docs/${newest}" docs/latest
          fi
          touch .nojekyll

      - name: Push gh-pages
        working-directory: gh-pages
        run: |
          git config user.name  "github-actions[bot]"
          git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
          git add -A
          if git diff --cached --quiet; then
            echo "nothing changed"
            exit 0
          fi
          git commit -q -m "Deployed ${GITHUB_SHA::7} to ${{ needs.build.outputs.version }} with Hugo ${HUGO_VERSION}"
          git push origin HEAD:gh-pages
`, base, base)
}
