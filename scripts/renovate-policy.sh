#!/bin/bash
# Make each organisation's Renovate policy UNAVOIDABLE, and make its absence loud.
#
# Two changes, one pull request per organisation, both in <org>/.github:
#
#   config.js     inheritConfig + inheritConfigStrict. default.json is a PRESET:
#                 it reaches a repository only through an `extends` naming it.
#                 Measured 2026-09-03: 77 of 835 repositories across the 117
#                 organisations that run Renovate carried NO config, so no org
#                 policy applied to them at all -- including the Go toolchain
#                 guard. inheritConfigStrict matters as much as inheritConfig:
#                 left false, a missing file is silent, which is the same
#                 failure one level up.
#
#   default.json  postUpdateOptions: gomodTidy AND gomodUpdateImportPaths.
#                 gomodTidy alone is inert: artifacts.ts skips tidy when
#                 updateType is major, and Renovate calls a 0.x minor bump a
#                 major. gomodUpdateImportPaths lifts that gate and rewrites
#                 nothing for 0.x (its commands are filtered to newMajor > 1).
#
# Both proven on go-macos before this script existed: 33 repositories read the
# inherited config, the three that had none now receive it, and `go mod tidy`
# on a branch Renovate produced afterwards changes nothing.
#
# Push with gitpush, never git push: a token must never reach a URL, a command
# line or an environment variable. Merge with ghmerge, which refuses a pull
# request whose checks did not actually run.
set -uo pipefail
# Chemin explicite: lancé depuis un shell dépouillé, `node` et `gh` de
# Homebrew manquent et chaque organisation échoue en disant « ne compile pas ».
export PATH=$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH
ROOT=${POLICY_ROOT:-$HOME/Documents/VCS/GIT/github.com}
BR=renovate-policy-inherit
PHASE=${1:-both}          # create | merge | both
LIST=${2:-/tmp/runners.txt}
STATE=/tmp/renovate-policy-prs.txt

create_one() {
  # Séparé: bash développe TOUS les mots avant d'affecter, donc $ORG dans le
  # même `local` est encore vide et `set -u` avorte.
  local ORG R D
  ORG="$1"; R="$ORG/.github"; D="$ROOT/$ORG/.github"

  agentsync claim --ttl 30m --note "politique Renovate: inheritConfig + postUpdateOptions" "$R" >/dev/null 2>&1 \
    || { echo "skip   $R: réservé ailleurs"; return; }

  rm -rf "$D"; mkdir -p "$(dirname "$D")"
  git clone -q --depth 1 -b main "https://github.com/$R" "$D" 2>/dev/null \
    || { echo "FAIL   $R: clone"; agentsync release "$R" >/dev/null 2>&1; return; }

  [ -f "$D/config.js" ] && [ -f "$D/default.json" ] \
    || { echo "skip   $R: config.js ou default.json manquant"; agentsync release "$R" >/dev/null 2>&1; return; }

  python3 - "$D/config.js" "$D/default.json" <<'PY'
import sys, io, json, collections
cj, dj = sys.argv[1], sys.argv[2]

s = io.open(cj, encoding='utf-8').read()
changed = False
if 'inheritConfig' not in s:
    if s.count('\n};') != 1:
        sys.exit(4)                      # forme inattendue, ne pas deviner
    block = """
  // default.json beside this file is a PRESET, and a preset reaches a
  // repository only through an `extends` that names it. Sitting in the
  // organisation applies it to nothing: with onboarding:false and
  // requireConfig:'optional', a repository carrying no config file runs on
  // Renovate's factory defaults and says so only at debug level --
  // "No renovate config file found". Measured 2026-09-03: 77 of the 835
  // repositories across the 117 organisations that run Renovate were in that
  // state, so the Go toolchain guard in default.json had never been in force
  // on any of them.
  inheritConfig: true,
  inheritConfigRepoName: '{{parentOrg}}/.github',
  inheritConfigFileName: 'default.json',
  // Loud when absent. Left at its default of false, Renovate proceeds silently
  // when the file is missing -- the same failure, one level up.
  inheritConfigStrict: true,
};"""
    s = s[:s.rindex('\n};')] + block + '\n'
    io.open(cj, 'w', encoding='utf-8').write(s)
    changed = True

d = json.load(io.open(dj, encoding='utf-8'), object_pairs_hook=collections.OrderedDict)

# packageRules[].description DOIT etre un tableau dans la configuration
# HERITEE. En tant que prereglage une chaine passe -- Renovate la migre --
# mais validateConfig('inherit', ...) tourne sans cette migration et rejette
# la configuration entiere:
#     "Configuration option `packageRules[0].description` should be a list (Array)"
# Renovate ouvre alors "Action Required: Fix Renovate Configuration" sur
# CHAQUE depot et arrete d'y produire des mises a jour. Un tableau est
# accepte par les deux chemins, donc c'est la forme a ecrire.
if isinstance(d.get("description"), str):
    d["description"] = [d["description"]]
    changed = True
for r in d.get("packageRules") or []:
    if isinstance(r.get("description"), str):
        r["description"] = [r["description"]]
        changed = True

want = ["gomodTidy", "gomodUpdateImportPaths"]
cur = list(d.get("postUpdateOptions") or [])
if not all(w in cur for w in want):
    for w in want:
        if w not in cur:
            cur.append(w)
    out = collections.OrderedDict()
    for k, v in d.items():
        if k == "packageRules" and "postUpdateOptions" not in out:
            out["postUpdateOptions"] = cur
        out[k] = v
    if "postUpdateOptions" not in out:
        out["postUpdateOptions"] = cur
    d = out
    changed = True

# Ecrire des que QUOI QUE CE SOIT a change dans le document. La version
# precedente n'ecrivait que depuis la branche postUpdateOptions ci-dessus,
# donc sur une organisation deja pourvue de ces options la normalisation des
# descriptions etait calculee puis jetee, et le commit echouait faute de
# difference -- en annoncant "FAIL commit" plutot que la verite.
if changed:
    io.open(dj, "w", encoding="utf-8").write(json.dumps(d, indent=2, ensure_ascii=False) + "\n")

sys.exit(0 if changed else 3)
PY
  local rc=$?
  if [ $rc -eq 3 ]; then echo "ok     $R: déjà conforme"; agentsync release "$R" >/dev/null 2>&1; return; fi
  if [ $rc -ne 0 ]; then echo "FAIL   $R: édition (rc=$rc)"; agentsync release "$R" >/dev/null 2>&1; return; fi

  node --check "$D/config.js" >/dev/null 2>&1 \
    || { echo "FAIL   $R: config.js ne compile pas"; agentsync release "$R" >/dev/null 2>&1; return; }
  python3 -c "import json,sys;json.load(open(sys.argv[1]))" "$D/default.json" >/dev/null 2>&1 \
    || { echo "FAIL   $R: default.json invalide"; agentsync release "$R" >/dev/null 2>&1; return; }

  ( cd "$D" && git checkout -q -b "$BR" && git add config.js default.json && git commit -q -F - <<'MSG'
fix(renovate): apply this organisation's policy to every repository, and tidy after an update

default.json is a PRESET. It reaches a repository only through an `extends`
naming it; sitting in the organisation applies it to nothing. With
onboarding:false and requireConfig:'optional', an uncovered repository runs on
Renovate's factory defaults and says so only at debug level:

    DEBUG: No renovate config file found (repository=...)

Measured 2026-09-03: 77 of the 835 repositories across the 117 organisations
that actually run Renovate carry no config at all. On every one of them the Go
toolchain guard -- written after go 1.27.0 auto-merged into seven repositories,
and because it miscompiles on loong64 (golang/go#81000) -- has never been in
force, and nothing reported it. The tell was in the branch names all along: an
uncovered repository gets renovate/<dep>-0.x, a covered one renovate/deps.

inheritConfig reads default.json before every repository regardless of what that
repository carries. inheritConfigStrict is not decoration: left at its default
of false, a missing file is silent, which is this same failure one level up.

postUpdateOptions carries BOTH gomodTidy and gomodUpdateImportPaths. gomodTidy
alone is inert here -- artifacts.ts skips tidy outright when updateType is
major, and Renovate calls a 0.x minor bump a major, which is nearly every
dependency in this fleet. gomodUpdateImportPaths lifts that gate and rewrites
nothing for a 0.x dependency: its commands are filtered to newMajor > 1.

Proven on go-macos before this landed anywhere else: 33 repositories read the
inherited config with none missing, the three that carried no config now receive
it, the container logged `go mod tidy command included` and ran it, and
`go mod tidy` on the branch Renovate then produced changes nothing.
MSG
  ) >/dev/null 2>&1 || { echo "FAIL   $R: commit"; agentsync release "$R" >/dev/null 2>&1; return; }

  ( cd "$D" && gitpush origin "$BR" ) >/dev/null 2>&1 \
    || { echo "FAIL   $R: push"; agentsync release "$R" >/dev/null 2>&1; return; }

  local N
  N=$(gh pr create --repo "$R" --base main --head "$BR" \
        --title "fix(renovate): apply this organisation's policy to every repository, and tidy after an update" \
        --body "\`default.json\` is a **preset** — it reaches a repository only through an \`extends\` naming it. Measured 2026-09-03: **77 of 835** repositories across the 117 organisations that run Renovate carry no config at all, so no organisation policy applied to them, including the Go toolchain guard.

\`inheritConfig\` reads it before every repository regardless. \`inheritConfigStrict: true\` because the default, \`false\`, is silent when the file is missing — the same failure one level up.

\`postUpdateOptions\` carries **both** \`gomodTidy\` and \`gomodUpdateImportPaths\`: the first alone is inert, since \`artifacts.ts\` skips tidy when \`updateType\` is major and Renovate calls a \`0.x\` minor bump a major. The second lifts that gate and rewrites nothing for \`0.x\`.

Proven on \`go-macos\` first: 33 repositories read the inherited config, none missing; the container ran \`go mod tidy\`; \`go mod tidy\` on the branch it produced changes nothing." \
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
