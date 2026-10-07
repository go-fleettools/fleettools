#!/bin/bash
# Move every Renovate runner off the user's PAT and onto a GitHub App.
#
# WHY, measured three times: the REST budget of 5000 requests an hour is counted
# per USER, not per token. ~/.renovate-token and ~/.github-token both belong to
# `tannevaled`, so a fleet sweep and 130 scheduled Renovate runs draw on one
# budget. When a sweep spends it, Renovate dies at initialisation with
#
#     WARN : Rate limit exceeded for api.github.com
#     FATAL: Initialization error   "errorMessage": "Authentication failure"
#
# and the repository looks broken while nothing is wrong. It happened on
# 2026-09-08 (5 runners), 2026-09-26 (2) and 2026-10-04 (4). Each time every
# runner passed on a plain re-run, same commit, same workflow -- the matched
# witness that the cause is the budget and nothing else.
#
# A GitHub App has its OWN 5000/hour, scaled by installation count. That removes
# the shared budget rather than rationing it, which is why this exists instead of
# a reserve inside internal/fleet.
#
# ⛔ WHAT THIS SCRIPT CANNOT DO, AND WHY IT SAYS SO RATHER THAN PRETENDING
#
# Creating a GitHub App needs a browser: the REST API has no create endpoint,
# only `POST /app-manifests/{code}/conversions`, and that `code` arrives through
# a redirect a person has to follow. Installing it on an organisation is the
# same. So the gesture is the user's, and `-apply` REFUSES until the App exists
# and its two values are on disk.
#
# The dry path is the part that can be tested without any of that, and it is
# tested: it reads every runner, produces the workflow it would write, and
# validates it. A write path that has never run is not shipped as if it had.
set -uo pipefail
export PATH=$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH

APP_ID_FILE=${RENOVATE_APP_ID_FILE:-$HOME/.renovate-app-id}
APP_KEY_FILE=${RENOVATE_APP_KEY_FILE:-$HOME/.renovate-app-key.pem}
TOKEN_ACTION=actions/create-github-app-token@v3.2.0
LIST=${2:-/tmp/runners.txt}
PHASE=${1:-dry}           # dry | apply
OUT=${OUT:-/tmp/renovate-app}

usage() {
  cat <<'EOF'
usage: renovate-app.sh [dry|apply] [runners-file]

  dry     read every runner, produce the workflow it WOULD write, validate it,
          and write the result under $OUT. Touches nothing. The default.
  apply   do it for real. Refuses unless the App id and private key are both
          on disk; see the header for why that gesture is not scriptable.

  runners-file   one `<org>/<repo>` per line. Produce it with:
                 quietscan -all | awk '/^    [a-z0-9-]+\/(\.github|renovate-runner)/{print $1}'
EOF
}

# transform prints the workflow that replaces the PAT with an App token.
#
# It is a FUNCTION so a test can drive it with a fixture: the alternative is a
# transformation that only ever runs against the live fleet, which cannot be
# proved wrong before it has already been wrong 133 times.
transform() {
  python3 - "$1" <<'PY'
import re, sys, pathlib
y = pathlib.Path(sys.argv[1]).read_text()

# The step that mints the token has to come BEFORE the one that uses it, and
# `steps.app-token.outputs.token` is only in scope inside the same job.
step = """      - name: Mint a token for this organisation
        id: app-token
        uses: ACTION
        with:
          app-id: ${{ vars.RENOVATE_APP_ID }}
          private-key: ${{ secrets.RENOVATE_APP_PRIVATE_KEY }}
          owner: ${{ github.repository_owner }}
"""

anchor = re.search(r'^(\s*)- name: Run Renovate\n', y, re.M)
if not anchor:
    print("NO-ANCHOR", file=sys.stderr); sys.exit(2)

y2 = y[:anchor.start()] + step + y[anchor.start():]
n = len(re.findall(r'\$\{\{ *secrets\.RENOVATE_TOKEN *\}\}', y2))
if n != 1:
    print(f"TOKEN-REFS-{n}", file=sys.stderr); sys.exit(3)
y2 = re.sub(r'\$\{\{ *secrets\.RENOVATE_TOKEN *\}\}',
            '${{ steps.app-token.outputs.token }}', y2)
sys.stdout.write(y2)
PY
}

dry_one() {
  local O=$1 dir="$OUT/$O"
  mkdir -p "$dir"
  local repo path
  for path in .github/workflows/renovate.yml; do
    if gh api "repos/$O/contents/$path" -H 'Accept: application/vnd.github.raw' > "$dir/before.yml" 2>/dev/null \
       && [ -s "$dir/before.yml" ]; then
      break
    fi
    : > "$dir/before.yml"
  done
  if [ ! -s "$dir/before.yml" ]; then
    printf '  %-34s no renovate.yml\n' "$O"; return 1
  fi
  if ! ACTION_SUB=$TOKEN_ACTION transform "$dir/before.yml" \
        | sed "s|uses: ACTION|uses: $TOKEN_ACTION|" > "$dir/after.yml" 2>"$dir/err"; then
    printf '  %-34s REFUSED: %s\n' "$O" "$(tr -d '\n' < "$dir/err")"; return 1
  fi
  # It must parse, and it must no longer name the PAT.
  if ! python3 -c "import yaml,sys; yaml.safe_load(open('$dir/after.yml'))" 2>/dev/null; then
    printf '  %-34s REFUSED: the result is not valid YAML\n' "$O"; return 1
  fi
  if grep -q 'RENOVATE_TOKEN' "$dir/after.yml"; then
    printf '  %-34s REFUSED: the PAT is still named\n' "$O"; return 1
  fi
  if ! grep -q 'steps.app-token.outputs.token' "$dir/after.yml"; then
    printf '  %-34s REFUSED: the App token is not used\n' "$O"; return 1
  fi
  printf '  %-34s ok\n' "$O"
}

case "$PHASE" in
  -h|--help|help) usage; exit 0 ;;
  dry)
    [ -s "$LIST" ] || { echo "no runner list at $LIST"; usage; exit 2; }
    rm -rf "$OUT"; mkdir -p "$OUT"
    ok=0; bad=0
    while read -r O; do
      [ -n "$O" ] || continue
      if dry_one "$O"; then ok=$((ok+1)); else bad=$((bad+1)); fi
    done < "$LIST"
    echo
    echo "$ok runner(s) would be rewritten, $bad refused. Nothing was written to GitHub."
    echo "The diffs are under $OUT/<org>/{before,after}.yml"
    [ "$bad" -eq 0 ] || exit 1
    ;;
  apply)
    # ⛔ The gesture this cannot make. Named precisely rather than attempted.
    missing=0
    [ -s "$APP_ID_FILE" ]  || { echo "missing: $APP_ID_FILE (the App's numeric id)"; missing=1; }
    [ -s "$APP_KEY_FILE" ] || { echo "missing: $APP_KEY_FILE (the App's private key, .pem)"; missing=1; }
    if [ "$missing" -ne 0 ]; then
      cat <<'EOF'

Creating a GitHub App is not scriptable: the REST API has no create endpoint,
and the manifest flow needs a browser redirect. Installing it on an
organisation is the same. So this refuses rather than half-doing it.

What the App needs, derived from what the current token actually carries
(`repo, workflow`) and not from a guess:

    contents: write        Renovate pushes branches
    pull requests: write   it opens and updates them
    issues: write          the dependency dashboard is an issue
    workflows: write       it bumps go-version inside .github/workflows/*
    checks: read           automerge waits on them
    statuses: read         same
    metadata: read         mandatory

Then put the id in ~/.renovate-app-id and the .pem in ~/.renovate-app-key.pem,
and run `renovate-app.sh apply`.
EOF
      exit 2
    fi
    echo "apply is not implemented: it has never been run, and a write path"
    echo "that has never run must not ship looking ready. Write it against a"
    echo "real App, one organisation first."
    exit 2
    ;;
  *) usage; exit 2 ;;
esac
