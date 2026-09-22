#!/bin/bash
# Add to each <org>/.github CI lane the check that the preset survives the
# INHERITED path, plus that check's own self-test.
#
# WHY. config.js sets inheritConfig, so Renovate reads default.json through
# validateConfig('inherit', ...) -- stricter than the repo-config validator the
# lane already runs, because it does NOT apply the migration a preset gets. A
# plain string `description` is legal in a preset and rejected when inherited,
# and Renovate then opens "Action Required: Fix Renovate Configuration" on
# EVERY repository in the organisation and stops producing updates. That landed
# on all 117 organisations on 2026-09-03 while every lane stayed green.
#
# Push with gitpush, never git push. Merge with ghmerge, which refuses a pull
# request whose checks did not actually run. Both are needed here: the change
# touches .github/workflows, which requires the `workflow` scope.
set -uo pipefail
export PATH=$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH
ROOT=${LANE_ROOT:-$HOME/Documents/VCS/GIT/github.com}
BR=lane-inherited-path-check
PHASE=${1:-both}
LIST=${2:-/tmp/runners.txt}
STATE=/tmp/lane-prs.txt
STEPS=${LANE_STEPS:?LANE_STEPS doit nommer le fichier contenant les etapes a inserer}

create_one() {
  local ORG R D
  ORG="$1"; R="$ORG/.github"; D="$ROOT/$ORG/.github"

  agentsync claim --ttl 30m --note "voie: controle du chemin herite" "$R" >/dev/null 2>&1 \
    || { echo "skip   $R: réservé ailleurs"; return; }

  rm -rf "$D"; mkdir -p "$(dirname "$D")"
  git clone -q --depth 1 -b main "https://github.com/$R" "$D" 2>/dev/null \
    || { echo "FAIL   $R: clone"; agentsync release "$R" >/dev/null 2>&1; return; }

  local F="$D/.github/workflows/ci.yml"
  [ -f "$F" ] || { echo "skip   $R: pas de ci.yml"; agentsync release "$R" >/dev/null 2>&1; return; }

  python3 - "$F" "$STEPS" <<'PY'
import sys, io
p, stepsfile = sys.argv[1], sys.argv[2]
s = io.open(p, encoding='utf-8').read()
if 'The preset survives the inherited path' in s:
    sys.exit(3)
anchor = "      # 5. PROVES: every preset named in a TOP-LEVEL `extends` exists and is\n"
if s.count(anchor) != 1:
    sys.exit(4)
steps = io.open(stepsfile, encoding='utf-8').read()
io.open(p, 'w', encoding='utf-8').write(s.replace(anchor, steps + anchor))
PY
  local rc=$?
  if [ $rc -eq 3 ]; then echo "ok     $R: déjà présent"; agentsync release "$R" >/dev/null 2>&1; return; fi
  if [ $rc -ne 0 ]; then echo "FAIL   $R: édition (rc=$rc)"; agentsync release "$R" >/dev/null 2>&1; return; fi

  # Linter la voie APRES l'avoir ecrite. L'erreur deja commise ici: verifier le
  # depot avant d'ajouter la voie, donc ne jamais verifier la voie.
  if command -v actionlint >/dev/null 2>&1; then
    actionlint "$F" >/dev/null 2>&1 || { echo "FAIL   $R: actionlint"; agentsync release "$R" >/dev/null 2>&1; return; }
  fi

  ( cd "$D" && git checkout -q -b "$BR" && git add .github/workflows/ci.yml && git commit -q -F - <<'MSG'
ci: check the preset the way the INHERITED path reads it, not only as repo config

On 2026-09-03 a preset change stopped Renovate on all 117 organisations, and
this lane was green throughout.

config.js sets inheritConfig, so Renovate reads default.json through
validateConfig('inherit', ...) -- a different validator from the one step 3
runs, and stricter, because it does NOT apply the migration a preset gets. A
plain string `description` is legal in a preset and rejected when inherited:

    Configuration option `description` should be a list (Array)
    Configuration option `packageRules[0].description` should be a list (Array)

Renovate then opens "Action Required: Fix Renovate Configuration" on every
repository in the organisation and stops producing updates there.

Step 3 passed the whole time because it validates default.json as REPO config
-- the shape Renovate no longer reads it in. Nothing else noticed either: a
per-repository config error is an ISSUE, not a failed job, so the Renovate
workflow run reported success while every repository it touched was refusing
its own configuration.

Step 4b checks the one rule that has actually bitten. Step 4c is its self-test,
built on a config shaped exactly like the one that caused the outage -- the
same reason step 4 exists for step 3: a check whose subject quietly stops being
checked is worse than no check, because it turns an absence into assurance.

Verified both directions before rollout by extracting the two `run:` bodies
exactly as YAML de-indents them and executing them: green on a real
default.json, and red on that file with its descriptions turned back into
strings, naming every offending field. actionlint clean.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
MSG
  ) >/dev/null 2>&1 || { echo "FAIL   $R: commit"; agentsync release "$R" >/dev/null 2>&1; return; }

  ( cd "$D" && gitpush origin "$BR" ) >/dev/null 2>&1 \
    || { echo "FAIL   $R: push"; agentsync release "$R" >/dev/null 2>&1; return; }

  local N
  N=$(gh pr create --repo "$R" --base main --head "$BR" \
        --title "ci: check the preset the way the INHERITED path reads it, not only as repo config" \
        --body "On 2026-09-03 a preset change stopped Renovate on all 117 organisations and **this lane was green throughout**.

\`config.js\` sets \`inheritConfig\`, so Renovate reads \`default.json\` through \`validateConfig('inherit', …)\` — stricter than step 3's validator, because it does not apply the migration a preset gets. A plain string \`description\` is legal in a preset and rejected when inherited, and Renovate then opens \"Action Required: Fix Renovate Configuration\" on every repository here and stops producing updates.

Step 3 passed throughout because it validates \`default.json\` as **repo** config — the shape Renovate no longer reads it in. Nothing else noticed either: a per-repository config error is an *issue*, not a failed job.

**Step 4b** checks the rule that actually bit. **Step 4c** is its self-test, on a config shaped exactly like the one that caused the outage.

🤖 Generated with [Claude Code](https://claude.com/claude-code)" \
        2>/dev/null | grep -oE '[0-9]+$')
  if [ -z "$N" ]; then echo "FAIL   $R: pr create"; agentsync release "$R" >/dev/null 2>&1; return; fi
  echo "$R $N" >> "$STATE"
  echo "pr     $R#$N"
  sleep 2
}

merge_all() {
  local M=0 K=0
  while read -r R N; do
    [ -z "${R:-}" ] && continue
    if gh pr checks "$N" --repo "$R" --watch --fail-fast >/dev/null 2>&1; then
      if ghmerge "$R" "$N" >/dev/null 2>&1; then echo "merged $R#$N"; M=$((M+1))
      else echo "held   $R#$N: ghmerge a refusé"; K=$((K+1)); fi
    else
      echo "held   $R#$N: contrôles rouges"; K=$((K+1))
    fi
    agentsync release "$R" >/dev/null 2>&1
  done < "$STATE"
  echo "=== fusionnées $M, retenues $K ==="
}

case "$PHASE" in
  create) : > "$STATE"; while read -r O; do [ -n "$O" ] && create_one "$O"; done < "$LIST" ;;
  merge)  merge_all ;;
  both)   : > "$STATE"; while read -r O; do [ -n "$O" ] && create_one "$O"; done < "$LIST"; merge_all ;;
esac
