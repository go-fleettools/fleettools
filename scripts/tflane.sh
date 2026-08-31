#!/usr/bin/env bash
# Adds a validate lane to a Terraform repository that has no CI, but only where
# the repository already passes it.
#
# These repositories hold .tf files and nothing else, so nobody gave them CI,
# and their provider-version pull requests arrive with no check of any kind --
# which means they cannot be merged on evidence, only on hope.
#
# `terraform init -backend=false` resolves the provider constraints without
# reaching for state or credentials, and `terraform validate` checks the
# configuration against them. That is exactly what a provider bump changes.
#
# fmt is reported, never gated: several of these trees are not `terraform fmt`
# clean already, and gating would make the lane red on arrival for a reason
# unrelated to the change under review.
#
# Usage: tflane.sh <owner>/<repo> ...
set -uo pipefail
W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT

lane() {
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

      - uses: hashicorp/setup-terraform@v3
        with:
          terraform_wrapper: false

      # -backend=false because nothing here should reach for state or
      # credentials to be checked. It still resolves the provider
      # constraints, which is what a provider version bump changes.
      - name: Init (providers only)
        run: terraform init -backend=false -input=false

      - name: Validate
        run: terraform validate

      # Reported, not gated: these trees are not all `terraform fmt` clean,
      # and gating would make the lane red on arrival for a reason that has
      # nothing to do with the change under review.
      - name: fmt (advisory)
        continue-on-error: true
        run: terraform fmt -check -recursive -diff
EOF
}

for repo in "$@"; do
    d="$W/$(echo "$repo" | tr /. _)"
    if ! gh repo clone "$repo" "$d" -- -q --depth 1 >/dev/null 2>&1; then
        echo "  SKIP    $repo: clone failed"; continue
    fi
    (
        cd "$d" || exit 1
        ls ./*.tf >/dev/null 2>&1 || exit 3

        # Prove it passes BEFORE giving it a lane.
        if ! timeout 400 pkgx terraform init -backend=false -input=false >/tmp/tfl_i.log 2>&1; then
            echo "  FINDING $repo: terraform init fails"
            sed 's/\x1b\[[0-9;]*m//g' /tmp/tfl_i.log | grep -iE 'error' | head -2 | sed 's/^/            /'
            exit 4
        fi
        if ! timeout 300 pkgx terraform validate >/tmp/tfl_v.log 2>&1; then
            echo "  FINDING $repo: terraform validate fails"
            sed 's/\x1b\[[0-9;]*m//g' /tmp/tfl_v.log | grep -iE 'error' | head -2 | sed 's/^/            /'
            exit 4
        fi

        git checkout -q -B ci-validate
        mkdir -p .github/workflows
        lane > .github/workflows/ci.yml
        # Lint the lane AFTER writing it: checking the repository before the
        # lane exists cannot check the lane, which is how a workflow that
        # failed its own actionlint step got proposed once already.
        if ! pkgx actionlint .github/workflows/ci.yml >/tmp/tfl_a.log 2>&1; then
            echo "  FINDING $repo: the generated lane does not pass actionlint"
            sed 's/^/            /' /tmp/tfl_a.log | head -3
            exit 4
        fi
        git add .github/workflows/ci.yml
        git commit -q -m "ci: validate the configuration, so a provider bump can be judged

Nothing checks a pull request here, so the provider-version bumps that
arrive carry no check of any kind and cannot be merged on evidence.

terraform init -backend=false resolves the provider constraints without
reaching for state or credentials; terraform validate then checks the
configuration against them, which is what such a bump changes.

fmt is reported, not gated: gating would make the lane red on arrival for
a reason unrelated to the change under review.

Verified against this repository before the lane was written: init and
validate both succeed, and the lane passes actionlint.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>" || exit 1
        gitpush -u origin ci-validate >/dev/null 2>&1 || exit 5
        gh pr create --head ci-validate --base main \
            --title 'ci: validate the configuration, so a provider bump can be judged' \
            --body 'Nothing checks a pull request here, so the provider-version bumps that arrive carry **no check of any kind** and cannot be merged on evidence.

`terraform init -backend=false` resolves the provider constraints without reaching for state or credentials; `terraform validate` then checks the configuration against them — which is exactly what such a bump changes.

`fmt` is reported rather than gated: gating would make the lane red the moment it lands, for a reason that has nothing to do with the change under review.

**Verified against this repository before the lane was written**, so it is not red on arrival: `terraform init -backend=false` and `terraform validate` both succeed, and `actionlint` passes on the lane itself.

Same lane as `openstack-terraform-modules/computes`, where the two provider PRs went from no checks at all to `validate:success`.' >/dev/null 2>&1 || exit 6
    )
    case $? in
        0) echo "  proposed $repo" ;;
        3) echo "  SKIP    $repo: no .tf at the root" ;;
        4) : ;;
        5) echo "  FAIL    $repo: push" ;;
        6) echo "  FAIL    $repo: PR" ;;
        *) echo "  FAIL    $repo" ;;
    esac
done
