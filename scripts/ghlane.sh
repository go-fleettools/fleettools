#!/usr/bin/env bash
# Adds a validation lane to an <org>/.github repository, but only where the
# repository already passes it.
#
# These repositories hold a Renovate config and a workflow and no code, so
# nothing checks them -- 37 of them had open dependency pull requests with no
# check of any kind, which means none could be merged on evidence.
#
# The lane is NOT pushed blind. Each repository is checked locally first, and
# one that fails is REPORTED rather than given a lane that is red on arrival:
# a Renovate config that does not validate is the defect that killed both runs
# go-attest/renovate-runner ever had, and it deserves a person, not a red tick.
#
# Usage: ghlane.sh <owner>/.github ...
set -uo pipefail
W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
ACTIONLINT_URL=https://github.com/rhysd/actionlint/releases/download/v1.7.7/actionlint_1.7.7_linux_amd64.tar.gz

lane_for() {
    cat <<'EOF'
name: ci

on:
  push:
    branches: [main]
  pull_request:
  workflow_dispatch:

concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: false

permissions:
  contents: read

jobs:
  validate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5

      - uses: actions/setup-node@v6
        with:
          node-version: '22'

      - name: Workflows parse
        run: |
          curl -sSL -o actionlint.tar.gz \
            https://github.com/rhysd/actionlint/releases/download/v1.7.7/actionlint_1.7.7_linux_amd64.tar.gz
          tar xzf actionlint.tar.gz actionlint
          ./actionlint .github/workflows/*.yml

      - name: config.js parses
        if: hashFiles('config.js') != ''
        run: node --check config.js

      # The Renovate config is validated ALONE, in an empty directory, because
      # that is how the container sees it: renovatebot/github-action mounts the
      # configuration file BY ITSELF --
      #
      #     --volume <configurationFile>:/github-action/config.js
      #
      # so a `require('./sibling')` works in the checkout, passes a validator
      # run there, and still kills every scheduled run with
      # "FATAL: Error parsing config file" -- a message that names no path and
      # reads like a syntax error in a file that has none.
      # go-attest/renovate-runner lost both of the runs it ever had to that.
      - name: Renovate config validates, as the container sees it
        run: |
          iso="$(mktemp -d)"
          files=()
          for f in config.js default.json renovate.json; do
            if [ -f "$f" ]; then
              cp "$f" "$iso/"
              files+=("$f")
            fi
          done
          if [ ${#files[@]} -eq 0 ]; then
            echo "no Renovate config to validate"
            exit 0
          fi
          cd "$iso"
          npx --yes --package renovate@latest renovate-config-validator "${files[@]}"
EOF
}

for repo in "$@"; do
    d="$W/$(echo "$repo" | tr /. _)"
    if ! gh repo clone "$repo" "$d" -- -q --depth 1 >/dev/null 2>&1; then
        echo "  SKIP    $repo: clone failed"; continue
    fi
    (
        cd "$d" || exit 1
        [ -d .github/workflows ] || exit 3

        # Prove the repository passes BEFORE giving it a lane.
        if ! pkgx actionlint .github/workflows/*.yml >/tmp/al.out 2>&1; then
            echo "  FINDING $repo: workflows do not parse"
            sed 's/^/            /' /tmp/al.out | head -3
            exit 4
        fi
        if [ -f config.js ] && ! node --check config.js >/dev/null 2>&1; then
            echo "  FINDING $repo: config.js does not parse"; exit 4
        fi
        iso="$(mktemp -d)"
        for f in config.js default.json renovate.json; do [ -f "$f" ] && cp "$f" "$iso/"; done
        files=$(cd "$iso" && ls *.js *.json 2>/dev/null)
        if [ -n "$files" ]; then
            if ! (cd "$iso" && npx --yes --package renovate@latest renovate-config-validator $files >/tmp/rv.out 2>&1); then
                echo "  FINDING $repo: Renovate config does not validate in isolation"
                grep -iE 'error|cannot find' /tmp/rv.out | head -2 | sed 's/^/            /'
                exit 4
            fi
        fi

        git checkout -q -B validate-config
        mkdir -p .github/workflows
        lane_for > .github/workflows/ci.yml
        # The lane must pass its OWN actionlint step. Checking the repository
        # before adding the lane never checks the lane: the first version of
        # this script proposed a workflow whose shell tripped SC2035 and
        # SC2086 in the very step it had just added.
        if ! pkgx actionlint .github/workflows/ci.yml >/tmp/al2.out 2>&1; then
            echo "  FINDING $repo: the generated lane does not pass actionlint"
            sed 's/^/            /' /tmp/al2.out | head -4
            exit 4
        fi
        git add .github/workflows/ci.yml
        git commit -q -m "ci: check what this repository actually is

Nothing checks a pull request here, so the dependency bumps that arrive
against the Renovate workflow cannot be judged on evidence.

Three checks, matched to what this repository holds: the workflows parse
(actionlint), config.js parses, and the Renovate config validates.

The last runs in an EMPTY DIRECTORY on purpose. renovatebot/github-action
mounts the configuration file BY ITSELF, so a require('./sibling') works
in the checkout, passes a validator run there, and still kills every
scheduled run with \"FATAL: Error parsing config file\" -- a message that
names no path. go-attest/renovate-runner lost both of its runs to that.

Verified against this repository before the lane was written, so it is
not red on arrival.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>" || exit 1
        gitpush -u origin validate-config >/dev/null 2>&1 || exit 5
        gh pr create --head validate-config --base main \
            --title 'ci: check what this repository actually is' \
            --body 'Nothing checks a pull request here, so the dependency bumps against the Renovate workflow cannot be judged on evidence.

Three checks, matched to what this repository holds: the workflows parse (`actionlint`), `config.js` parses, and the Renovate config validates.

**The last one runs in an empty directory, on purpose.** `renovatebot/github-action` mounts the configuration file by itself, so a `require(./sibling)` works in the checkout, passes a validator run there, and still kills every scheduled run with `FATAL: Error parsing config file` — a message that names no path and reads like a syntax error in a file that has none. `go-attest/renovate-runner` lost both of the runs it ever had to exactly that.

All three were run against this repository **before** the lane was written, so it is not red on arrival. Proven first on `go-simd/.github`, where the open dependency PR went from no checks at all to `validate:success` and merged.' >/dev/null 2>&1 || exit 6
    )
    case $? in
        0) echo "  proposed $repo" ;;
        3) echo "  SKIP    $repo: no workflows" ;;
        4) : ;;  # already reported
        5) echo "  FAIL    $repo: push" ;;
        6) echo "  FAIL    $repo: PR" ;;
        *) echo "  FAIL    $repo" ;;
    esac
done
