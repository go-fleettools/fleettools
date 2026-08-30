#!/usr/bin/env bash
# Lands dependency PRs that edit a workflow file.
#
# `gh pr merge` cannot: the merge API refuses an OAuth app without the
# `workflow` scope, and gh's keyring token does not carry it. The token that
# does is ~/.github-token, which gitpush serves through a credential helper --
# so it never reaches an argument list, an environment variable, or a URL.
#
# Usage: wfmerge.sh <owner/repo> <pr-number> [...]
set -uo pipefail
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

land() {
    local repo="$1" num="$2"
    local meta head title
    meta=$(gh pr view "$num" --repo "$repo" --json headRefName,title,mergeable,statusCheckRollup 2>/dev/null) || {
        echo "  SKIP $repo#$num: cannot read PR"; return 1; }
    head=$(printf '%s' "$meta" | jq -r .headRefName)
    title=$(printf '%s' "$meta" | jq -r .title)

    # Green means checks ran and passed. No checks at all is not green.
    local bad
    bad=$(printf '%s' "$meta" | jq -r '[.statusCheckRollup[]?|(.conclusion // .state // .status)]
                                       | if length == 0 then ["NONE"] else . end
                                       | map(select(. != "SUCCESS" and . != "NEUTRAL" and . != "SKIPPED"))
                                       | join(",")')
    if [ -n "$bad" ]; then echo "  SKIP $repo#$num: not green ($bad)"; return 1; fi

    local dir="$WORK/$(basename "$repo")"
    gh repo clone "$repo" "$dir" -- -q >/dev/null 2>&1 || { echo "  SKIP $repo#$num: clone failed"; return 1; }
    (
        cd "$dir" || exit 1
        git fetch -q origin "$head" || exit 1
        git checkout -q "$(git symbolic-ref --short HEAD)" || exit 1
        git merge --squash -q FETCH_HEAD >/dev/null 2>&1 || exit 1
        git diff --cached --quiet && exit 3   # nothing to land
        git commit -q -m "$title

Merged locally: the GitHub merge API refuses an OAuth app without the
\`workflow\` scope, and this PR edits a workflow file. The change itself
is unmodified.

Renovate PR #$num." || exit 1
        gitpush origin HEAD >/dev/null 2>&1 || exit 1
    )
    local rc=$?
    case $rc in
        0) gh pr close "$num" --repo "$repo" --delete-branch \
             --comment "Landed on the default branch as a squash commit, merged locally: the GitHub merge API refuses an OAuth app without the \`workflow\` scope, and this PR edits a workflow file. The change itself is unmodified." >/dev/null 2>&1
           echo "  landed $repo#$num  $title" ;;
        3) echo "  SKIP $repo#$num: already landed" ;;
        *) echo "  FAIL  $repo#$num (rc=$rc)" ;;
    esac
}

while [ $# -gt 0 ]; do
    land "$1" "$2"
    shift 2
done
