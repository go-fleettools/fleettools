#!/bin/bash
# Audit the Renovate policy across organisations, one per line on stdin.
#
# This exists because every failure in this area was SILENT. A preset that
# reaches no repository, a runner that stopped, a rule that was never in force
# -- none of them produce a red mark anywhere. The only way to know is to go and
# look, so this goes and looks, and exits non-zero when an invariant is broken.
#
# Each check states what it proves. A check that cannot fail is not a check.
#
#   PRESET      <org>/.github/default.json exists and parses.
#               PROVES the file inheritConfigStrict demands is there. Without it
#               every run in the organisation now ABORTS -- strict turns a silent
#               omission into a loud one, which is the point, but it means this
#               check is load-bearing.
#
#   INHERIT     config.js sets inheritConfig AND inheritConfigStrict.
#               PROVES the preset is applied to every repository rather than to
#               the ones carrying a renovate.json that extends it. Measured
#               2026-09-03, before this was set: 77 of 835 repositories were
#               running on Renovate's factory defaults.
#               DOES NOT PROVE that Renovate read it -- only a debug run shows
#               "Inherited config", and at info level its absence means nothing.
#
#   TIDY        default.json sets postUpdateOptions with gomodTidy AND
#               gomodUpdateImportPaths.
#               PROVES go mod tidy will run after an update. Both are needed:
#               artifacts.ts skips tidy when updateType is major, and Renovate
#               calls a 0.x minor bump a major.
#
#   DESCRIPTION packageRules[].description is an ARRAY, not a string.
#               PROVES the preset survives validateConfig('inherit', ...).
#               As a preset a plain string is fine -- Renovate migrates it --
#               but the inherited path runs WITHOUT that migration and
#               rejects the whole config:
#                 Configuration option `packageRules[0].description`
#                 should be a list (Array)
#               Renovate then opens "Action Required: Fix Renovate
#               Configuration" on EVERY repository in the organisation and
#               stops producing updates there. On 2026-09-03 that hit all
#               117 at once and the workflow run still reported SUCCESS,
#               because a per-repo config error is an issue, not a failed
#               job -- which is exactly why it is checked here.
#
#   GOGUARD     default.json carries a rule pinning matchDepNames ["go"] to
#               automerge:false with groupName null.
#               PROVES the Go toolchain cannot ride into an auto-merged group.
#               It is checked separately from PRESET because 194 organisations
#               carry an OLDER preset that has no such rule, and they look
#               entirely healthy until a runner is switched on.
#
#   RUNNER      a renovate workflow exists, is active, and its last run is
#               recent and green.
#               PROVES updates are actually being produced. A stopped runner and
#               a fleet with nothing to update look identical from outside.
set -uo pipefail
export PATH=$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH
STALE_DAYS=${POLICY_STALE_DAYS:-3}
RC=0; OK=0; BAD=0
NOW=$(date -u +%s)

while read -r O; do
  [ -z "$O" ] && continue
  # Une organisation RETIRÉE n'est pas un défaut. go-iconoir a ses quatre
  # dépôts archivés depuis le 2026-08-30: GitHub n'exécute aucune tâche
  # programmée dans un dépôt archivé, et rien n'y est poussable. Sans ce
  # cas l'audit sort non nul à chaque passage pour une raison connue, et
  # un contrôle qui échoue toujours cesse d'être lu.
  if [ "$(gh api "repos/$O/.github" --jq .archived 2>/dev/null)" = "true" ]; then
    echo "retiré  $O: dépôt .github archivé, ignoré"
    continue
  fi
  FAULTS=""

  P=$(gh api "repos/$O/.github/contents/default.json" --jq '.content' 2>/dev/null | base64 -d 2>/dev/null)
  if [ -z "$P" ]; then
    FAULTS="$FAULTS PRESET-ABSENT"
  else
    printf '%s' "$P" > /tmp/.pa.json
    if ! python3 -c "import json,sys;json.load(open('/tmp/.pa.json'))" 2>/dev/null; then
      FAULTS="$FAULTS PRESET-INVALIDE"
    else
      python3 - <<'PY' || FAULTS="$FAULTS TIDY-MANQUANT"
import json, sys
d = json.load(open('/tmp/.pa.json'))
o = d.get('postUpdateOptions') or []
sys.exit(0 if ('gomodTidy' in o and 'gomodUpdateImportPaths' in o) else 1)
PY
      python3 - <<'PY' || FAULTS="$FAULTS DESCRIPTION-EN-CHAINE"
import json, sys
d = json.load(open('/tmp/.pa.json'))
if isinstance(d.get('description'), str):
    sys.exit(1)
for r in d.get('packageRules') or []:
    if isinstance(r.get('description'), str):
        sys.exit(1)
sys.exit(0)
PY
      python3 - <<'PY' || FAULTS="$FAULTS GOGUARD-MANQUANT"
import json, sys
d = json.load(open('/tmp/.pa.json'))
for r in d.get('packageRules') or []:
    if r.get('matchDepNames') == ['go'] and r.get('automerge') is False and 'groupName' in r:
        sys.exit(0)
sys.exit(1)
PY
    fi
  fi

  C=$(gh api "repos/$O/.github/contents/config.js" --jq '.content' 2>/dev/null | base64 -d 2>/dev/null)
  if [ -z "$C" ]; then
    FAULTS="$FAULTS CONFIGJS-ABSENT"
  else
    printf '%s' "$C" | grep -q 'inheritConfig: *true'       || FAULTS="$FAULTS INHERIT-ABSENT"
    printf '%s' "$C" | grep -q 'inheritConfigStrict: *true' || FAULTS="$FAULTS STRICT-ABSENT"
  fi

  W=$(gh api "repos/$O/.github/actions/workflows" 2>/dev/null)
  ID=$(printf '%s' "$W" | jq -r '[.workflows[]? | select(.path|test("renovate"))][0].id // empty' 2>/dev/null)
  if [ -z "$ID" ]; then
    FAULTS="$FAULTS RUNNER-ABSENT"
  else
    R=$(gh api "repos/$O/.github/actions/workflows/$ID/runs?per_page=1" --jq '.workflow_runs[0] | "\(.conclusion // .status) \(.created_at)"' 2>/dev/null)
    case "$R" in
      success\ *)
        # Un job vert ne dit PAS que Renovate a travaille. L'epuisement du
        # budget est un resultat PAR DEPOT -- "result": "rate-limit-exceeded"
        # -- et le conteneur sort quand meme a 0. Le 2026-09-03, 175 executions
        # ont ainsi rendu "success" sans traiter un seul depot.
        D=$(printf '%s' "$R" | awk '{print $2}')
        T=$(python3 -c "import datetime,sys;print(int(datetime.datetime.fromisoformat(sys.argv[1].replace('Z','+00:00')).timestamp()))" "$D" 2>/dev/null || echo 0)
        AGE=$(( (NOW - T) / 86400 ))
        [ "$AGE" -gt "$STALE_DAYS" ] && FAULTS="$FAULTS RUNNER-DORMANT(${AGE}j)"
        ;;
      "") FAULTS="$FAULTS RUNNER-JAMAIS-EXECUTE" ;;
      # Une exécution EN COURS n'est pas un défaut. Sans ce cas, lancer
      # l'audit pendant une vérification fait échouer l'organisation qu'on
      # est précisément en train de vérifier.
      in_progress\ *|queued\ *|requested\ *|waiting\ *) ;;
      *)  FAULTS="$FAULTS RUNNER-ROUGE($(printf '%s' "$R" | awk '{print $1}'))" ;;
    esac
  fi

  if [ -z "$FAULTS" ]; then
    OK=$((OK+1))
  else
    echo "MANQUE $O:$FAULTS"
    BAD=$((BAD+1)); RC=1
  fi
done

echo "=== conformes $OK, en défaut $BAD ==="
exit $RC
