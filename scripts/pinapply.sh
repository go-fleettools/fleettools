#!/usr/bin/env bash
# Applies what `gopin` reports, by CLONING and PUSHING rather than through the
# contents API.
#
# WHY NOT THE API. `gopin -apply` writes with `gh api -X PUT
# repos/.../contents/.github/workflows/...`, and that cannot work here. The
# token `gh` holds carries `admin:public_key, gist, read:org, repo` and NOT
# `workflow`, and GitHub answers a contents PUT under `.github/workflows/`
# with a bare
#
#     {"message":"Not Found", ... "status":"404"}
#
# which names neither the scope nor the path. Measured on go-avkit/bitstream:
# the GET of the same file succeeds, the POST that creates the branch succeeds
# — so the token does write to that repository — and only the PUT under
# `.github/workflows/` fails. The 404 is the missing scope wearing the wrong
# number.
#
# The wide token lives in a FILE, served to git over a pipe by
# git-credential-tannevaled, and `gitpush` names that helper instead of
# reading it. So a push carries the scope that an API call here does not, and
# nothing has to put a secret in an environment variable to get it. This is
# the same route scripts/tidyall.sh already takes.
#
# Usage:
#   gopin -version 1.27.1 -list repos.txt > report.txt
#   scripts/pinapply.sh 1.27.1 report.txt [max]
set -uo pipefail

want=${1:?usage: pinapply.sh <version> <gopin-report> [max]}
report=${2:?usage: pinapply.sh <version> <gopin-report> [max]}
max=${3:-0}

work=$(mktemp -d); trap 'rm -rf "$work"' EXIT
done_n=0 ok=0 failed=0 skipped=0

while IFS= read -r line; do
    case $line in "ALIAS   "*) ;; *) continue ;; esac

    repo=${line#ALIAS   }; repo=${repo%%:*}
    files=${line#*: * in }; files=${files%% ; *}
    # A line with no " ; " keeps its whole tail, which is the file list.
    raise=yes
    case $line in *"go.mod HELD"*) raise=no ;; *"go.mod "*" -> "*) raise=yes ;; *) raise=no ;; esac

    [ "$max" -gt 0 ] && [ "$done_n" -ge "$max" ] && break
    done_n=$((done_n + 1))

    if ! ~/.local/bin/agentsync claim --note "pin Go $want instead of the stable alias" "$repo" >/dev/null 2>&1; then
        echo "  held    $repo (another session has it)"; skipped=$((skipped + 1)); continue
    fi

    d=$work/$(echo "$repo" | tr / _)
    if ! git clone -q --depth 1 "https://github.com/$repo" "$d" 2>/dev/null; then
        echo "  FAIL    $repo clone"; failed=$((failed + 1)); continue
    fi

    changed=0
    for f in $files; do
        p=$d/.github/workflows/$f
        [ -f "$p" ] || continue
        # Both spellings the fleet uses: block mapping and inline flow mapping.
        perl -0pi -e "s/(go-version:\s*)stable/\${1}'$want'/g" "$p"
        changed=1
    done
    if [ "$raise" = yes ] && [ -f "$d/go.mod" ]; then
        perl -0pi -e "s/^go \d+\.\d+(\.\d+)?$/go $want/m" "$d/go.mod"
    fi
    if [ "$changed" = 0 ] || git -C "$d" diff --quiet; then
        echo "  nothing $repo (the alias was not where the report said)"
        skipped=$((skipped + 1)); continue
    fi

    br=go-$(echo "$want" | tr -d .)-pinned
    git -C "$d" switch -qc "$br"
    git -C "$d" add -A
    git -C "$d" -c commit.gpgsign=false commit -q -F - <<MSG
ci: pin Go $want instead of \`stable\`

\`stable\` is a channel, not a version, so the toolchain moved here with no
commit: CI has been building and running on go$want while go.mod said
otherwise, and a MINOR Go bump is the one that reindents under gofmt and
changes coverage block counting.

It also kept the shared Renovate rule from ever firing. That rule matches dep
name \`go\` under the \`gomod\` and \`github-actions\` managers with
\`automerge: false\`, citing golang/go#81000 — and an alias has no version to
compare or rewrite. Pinning is what makes the review rule apply at all.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
MSG
    if ! (cd "$d" && ~/.local/bin/gitpush -q origin "$br" >/dev/null 2>&1); then
        echo "  FAIL    $repo push"; failed=$((failed + 1)); continue
    fi
    if ! gh pr create --repo "$repo" --head "$br" \
        --title "ci: pin Go $want instead of \`stable\`" \
        --body "$(printf '`go-version: stable` is a channel alias, not a version, so this repository has been built and run on `go%s` with nothing in the tree saying so — and a *minor* Go bump is the one that reindents under `gofmt` and changes coverage block counting.\n\nIt also kept the shared Renovate rule from ever firing: that rule matches dep name `go` under the `gomod` and `github-actions` managers with `automerge: false`, citing `golang/go#81000`, and an alias has no version to compare or rewrite. **Pinning is the condition for the review rule to apply at all**, not rigidity.\n\n## What to check before merging\n\nCI is the control, and two lanes are worth a glance:\n\n- **`gofmt`**, if this repository gates it. A minor Go bump reindents; a patch does not.\n- **`loong64` under qemu**, if this repository runs it. `golang/go#81147`, the 1.27 backport of the loong64 miscompile, is still **open** (milestone Go1.27.2) — verified in the released toolchain, where `LOONG64.rules` still maps `(Cvt64Fto32 ...)` to `(TRUNCDW ...)` with all four float-to-integer ops declared `reg: fp11`. A repository that only *cross-compiles* for loong64 cannot tell the difference, because a code-generation defect cannot redden a build that never runs.\n\nPushed with `gitpush`, because a workflow file cannot be written through the contents API with the token `gh` holds: that one has no `workflow` scope, and GitHub answers the PUT with a bare 404 naming neither.\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)\n' "$want")" >/dev/null 2>&1; then
        echo "  pushed  $repo (branch $br; pull request FAILED)"; failed=$((failed + 1)); continue
    fi
    echo "  opened  $repo"
    ok=$((ok + 1))
    sleep 3
done < "$report"

echo
echo "$done_n considered · $ok opened · $failed failed · $skipped skipped"
