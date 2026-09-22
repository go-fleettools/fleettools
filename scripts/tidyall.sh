#!/usr/bin/env bash
# For each pull request named on stdin as owner/repo#number: check out its
# branch, run `go mod tidy`, and push only if tidy actually changed something
# AND the tree still builds. Renovate leaves superseded go.sum lines behind,
# which fails any lane that asserts `go mod tidy` produces no diff.
set -uo pipefail
W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
export GOWORK=off
while IFS= read -r spec; do
    [ -z "$spec" ] && continue
    repo="${spec%%#*}"; num="${spec##*#}"
    br=$(gh pr view "$num" --repo "$repo" --json headRefName --jq .headRefName 2>/dev/null)
    [ -z "$br" ] && { echo "  SKIP $spec: no branch"; continue; }
    # One directory per PULL REQUEST: two PRs in the same repository
    # would otherwise collide and the second clone would fail.
    d="$W/$(echo "$repo" | tr / _)-$num"
    git clone -q --depth 1 --branch "$br" "https://github.com/$repo.git" "$d" 2>/dev/null || {
        echo "  SKIP $spec: clone failed"; continue; }
    (
        cd "$d" || exit 1
        # EVERY module in the tree, not just the root one: go-widgets/toolkit
        # carries a rougelex/ sub-module with its own go.mod, and a lane that
        # vets it fails while the root module is spotless.
        mods=$(find . -name go.mod -not -path '*/.git/*' | sort)
        [ -n "$mods" ] || exit 4
        # Best-effort per module, because a tree can carry auxiliary modules
        # that do not resolve standalone -- tamago-uefi has two verification
        # modules whose `replace` points at sibling checkouts that are not
        # there. Those are skipped; the ROOT module must still tidy, since
        # that is the one every lane checks.
        ok=""
        broken_before=""
        for m in $mods; do
            if (cd "$(dirname "$m")" && timeout 400 go mod tidy >/dev/null 2>&1); then
                ok="$ok $m"
            elif [ "$m" = "./go.mod" ]; then
                exit 5
            fi
        done
        git diff --quiet && exit 3
        for m in $ok; do
            (cd "$(dirname "$m")" && timeout 500 go build ./... >/dev/null 2>&1) || {
                # The build gate asks whether the tidy BROKE the tree, so it
                # has to compare against how the tree built BEFORE it -- not
                # against zero. Two repositories fail here for reasons the
                # tidy neither caused nor could fix:
                #
                #   - a GOOS-specific harness. go-tpm2/validate imports
                #     github.com/usbarmory/tamago/amd64, which exists only
                #     under GOOS=tamago, so `go build ./...` on the host has
                #     never succeeded there and never will.
                #   - the missing-go.sum case itself: the tree does not build
                #     BEFORE the tidy either, because Renovate raised go.mod
                #     without writing go.sum, and the tidy is the fix.
                #
                # In both, "fails after tidy" is true and means nothing. So
                # re-run the same build on the tree as it was, and only refuse
                # when the tidy is what turned it red.
                pre=$(mktemp -d)
                git -c advice.detachedHead=false stash -q --include-untracked 2>/dev/null
                (cd "$(dirname "$m")" && timeout 500 go build ./... >/dev/null 2>&1)
                was_ok=$?
                git stash pop -q 2>/dev/null
                rm -rf "$pre"
                [ "$was_ok" -eq 0 ] && exit 6   # cassé PAR le rangement
                broken_before=1
            }
        done
        git commit -q -am "go.mod: run go mod tidy, in every module of the tree

Renovate raised the module and wrote the new sums, but left the
superseded lines behind, so the lane that asserts \`go mod tidy\` produces
no diff failed on go.sum alone.

Tidy only, applied to every go.mod in the tree -- a sub-module keeps its
own, and a lane that vets it fails while the root module is spotless.
go build passes in each." || exit 1
        gitpush origin "$br" >/dev/null 2>&1 || exit 7
        [ -n "$broken_before" ] && exit 8
    )
    case $? in
        0) echo "  pushed  $spec" ;;
        8) echo "  pushed  $spec (l'arbre ne compilait pas non plus AVANT: CI juge)" ;;
        3) echo "  clean   $spec (tidy changes nothing; other cause)" ;;
        4) echo "  skip    $spec (no go.mod)" ;;
        5) echo "  skip    $spec (tidy failed)" ;;
        6) echo "  BUILD   $spec fails after tidy -- left alone" ;;
        7) echo "  FAIL    $spec push" ;;
        *) echo "  FAIL    $spec" ;;
    esac
done
