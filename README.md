# fleet — sweeping ~1800 repositories across ~320 organisations

Written 2026-08-30 while getting every default branch green. They lived in a
scratchpad, which is purged; they are here because each one encodes something
that cost a wrong answer to learn.

| | |
|---|---|
| `cmd/redscan` | Every default branch whose last run failed. One pass: 1819 repositories, 44 red — **all of them stale Renovate lanes** whose last run predated a token rotation by three hours. A red branch is not a broken one; check WHEN it last ran. |
| `cmd/prscan` | Open pull requests across every org. Batches ~15 `org:` qualifiers per search query, so ~20 calls instead of 320 — the search API is rate-limited far more tightly than REST. |
| `cmd/prmerge` | Merges dependency PRs that are mergeable **and** fully green. |
| `cmd/tagscan` | Every tagged Go module whose default branch is **ahead of its latest tag** — work that is merged and that `go get` cannot reach. Built after the same mistake twice: `ghmerge` alone on `go-widgets/toolkit` (2026-09-05) and on `go-pdfkit/ops` (2026-09-07), the second time with the rule already written down. It asks about **tags, not releases**: `go get` resolves a tag, and a first draft asking `/releases/latest` reported `ops` as behind fifteen minutes after `v0.12.0` had been pushed and proved to resolve. `git ls-remote` costs no API budget, and a tag that IS the head settles without a call. |
| `cmd/quietscan` | Every organisation whose Renovate runner has **stopped**. `redscan` asks whether the last run FAILED; a runner that stopped does not fail, it says nothing. This is the other half of that lesson: check WHEN it last ran. |
| `cmd/judgescan` | Every repository whose tests look for an external tool its CI **never installs** — a foreign judge that exists in the source and has never once run. Written after the same defect surfaced twice in one afternoon: `go-fde/luks` had a cryptsetup interop test gated on root (formatting a file needs none; only attaching a device does), and `go-filesystems/btrfs` had seven oracles whose `btrfs check` half — upstream judging OUR WRITES — was locked behind the same borrowed root gate, in a CI that installed no btrfs-progs. Both looked like a green lane over a file full of assertions, judging nothing. Reads the working tree only, so it cannot starve Renovate: its ONE call is the staleness check below. Knows that a step says `poppler-utils` and a test says `pdftotext`, and that a checkout `path: btrfs` is not an installation of btrfs-progs — the false negative that would have erased its own founding case. |
| `.github/workflows/docs-current.yml` | **Reusable.** An organisation's landing repository calls it and the check runs THERE, with no secret — listing a public organisation is a public read. Nine lines in the caller — ⛔ not `on: [push, pull_request]`, which both fire for a branch with a PR open and ran the check twice on the first three organisations that copied the snippet. |
| `cmd/docscan` | Every repository an organisation **has** but does not **advertise** — and every one it advertises and no longer has. First pass (2026-09-25): 334 organisations, **87 modules named on no surface**. **2026-09-26: `0 organisations with drift`**, with the check adopted in eighteen organisations so it stays there — re-measure with `go run ./cmd/docscan` rather than trusting this line. Five of its six hardenings came from FALSE POSITIVES, which cost more than a miss: an entry carrying `org = "<other-org>"` is not a claim about this one, a nav line may name its module in its title (`- The client (reddit): client.md`), an **archived** repository is not a gone one, a YAML flow mapping on one line is a list the reader could not parse, and a PR check reading the published page judges `main` rather than the branch. Two definitions of drift — the printed count and the exit status — disagreed in both directions until one method served both. |
| `cmd/testscan` | Every repository whose CI runs `go test` on only PART of what has tests — or on nothing at all. Written after `openweft/weft`: five workflows, `go test` in one of them naming four packages, and 83 packages with tests. Every lane green, about four of them. Switching on a plain `go test ./...` there found two tests that could not pass on Linux at all. One pass: 884 repositories, 519 have tests, 482 run them all. Reads the working tree only, so it cannot starve Renovate: its ONE call is the staleness check below. It follows a `task ci` into the Taskfile rather than calling it absent, and reports a computed package set (`go test $PKGS`, `go list | xargs`) as unresolved rather than as zero — the false positive that named three dozen healthy repositories on its first run, including one fixed an hour earlier. |
| `cmd/vscan` | For every tagged module whose branch has moved past its tag, what the next version should be — **derived** from the public API with `gorelease`, not guessed. `tagscan` found 280 of 333 tagged modules carrying merged work `go get` cannot reach; choosing 280 bumps by hand is 280 chances to publish a number that lies about compatibility. It asks **once per GOOS**, which is not a refinement: removing `New` from `go-xrkit/depth3d`'s `!darwin` file — a break for every Linux consumer — reads as `v0.1.1`, a patch, on a darwin host, and as `v0.2.0` with an incompatible change on linux and windows. It reads the **default branch**, never the checkout: a first version ran where the clone happened to be and called `go-simd/floats` a patch when its own branch head was a minor. And it records the incompatible-changes section **separately from the number**, because in `v0.x` an addition and a removal both suggest `v0.2.0`. A **frozen** base — a tag `git tag --merged` cannot see, left on an orphaned line by a history rewrite — gets its own section rather than reading as *never tagged*: `go-filesystems/xfs` `v0.1.0` is published and required by six repositories, and "never tagged" is an invitation to cut it again. `gorelease` still answers for those, because it fetches the base from the module proxy and never consults git ancestry. |
| `cmd/gopin` | Replaces `go-version: stable` in a repository's workflows with an explicit version, and raises `go.mod` to match. **`stable` is a channel, not a version, and that is the defect**: measured on go-pkgx on 2026-10-04, CI had been building and running on `go1.27.1` — `Setup go version spec stable` → `go version go1.27.1` in the job log — while all seven `go.mod` still said `go 1.26.4`. A *minor* Go bump, the one that reindents under `gofmt` and changes coverage block counting, arrived with no commit and nothing to review. Worse, the organisation's shared Renovate preset carries a rule written for exactly this — dep name `go` under `gomod` and `github-actions`, `automerge: false`, `groupName: null`, citing `golang/go#81000` — and it had never produced a pull request, because **an alias has no version to compare or rewrite**. The contrast is demonstrable rather than argued: in one `ci.yml` (`go-pkgx/registry-viewer`) the `build` job said `stable` and Renovate did nothing, while the `bricolint` job said `"1.26.4"` and Renovate opened a pull request moving it to `1.27.1` — same file, same run, one variable. So pinning is not rigidity: it is the condition for the review rule to apply at all. It reads the **default branch through the API, never a checkout** — a local sweep still reported three go-pkgx repositories as holding the alias hours after all eight were pinned and merged. It **refuses to touch a job that already names a version**, even an old one: somebody chose that, possibly to hold a repository back. And when that version is OLDER than the target it also **holds `go.mod` where it is**, because raising the directive past the pin does not merely look inconsistent with it, it breaks it: with `GOTOOLCHAIN` unset — the default `setup-go` leaves in place — `go` satisfies a directive it is too old for by DOWNLOADING the newer toolchain, so the lane keeps printing the pinned version in its setup step while running the one it was pinned away from. Measured on go1.26.4 against a module asking for 1.27.1: `GOTOOLCHAIN=go1.26.4+auto` runs go1.27.1, while `GOTOOLCHAIN=go1.26.4` refuses with `go.mod requires go >= 1.27.1`. `go-gfx/gfx` is the live case — its loong64 lane names go1.26.4 because `golang/go#81000` miscompiles that package there and the 1.27 backport is still open — and it is reported as held rather than changed. A pin naming no patch (`1.27`, `1.27.x`) is NOT a hold inside its own minor, because setup-go resolves it to the newest patch there — measured on `cloud-boot/tamago-uefi`, `Setup go version spec 1.27.x` → `go version go1.27.1` — so only `1.26` and below hold. Reports by default; `-apply` opens the pull requests, paced because GitHub's SECONDARY limit brakes a burst. Local census 2026-10-04: **376 of 620** repositories with a `go-version` line asked for `stable`, of which **258** either execute `loong64` under qemu (180) or gate coverage at 100% (239) — the two places where an unannounced toolchain change actually bites. |
| `scripts/tidyall.sh` | Runs `go mod tidy` on a PR branch and pushes only if it changed something AND the tree still builds. |
| `scripts/pinapply.sh` | Applies what `gopin` reports, by CLONING and pushing with `gitpush` rather than through the contents API — because `gopin -apply` cannot write a workflow file with the token `gh` holds. Measured on `go-avkit/bitstream`: the `GET` succeeds, the `POST` that creates the branch succeeds (so the token does write to that repository), and only the `PUT` under `.github/workflows/` fails — as a bare `{"message":"Not Found","status":"404"}` naming neither the scope nor the path. `gh api -i user` gives `admin:public_key, gist, read:org, repo` and no `workflow`. ⚠ `ghscopes` does NOT show this: with no `-f` it reads `~/.github-token`, the WIDE token, so running it with and without `-f` returns two identical answers about one file and looks like confirmation. The alternative fix is the one `wfmerge.sh` names — `gh auth refresh -s workflow`, once — which makes `gopin -apply` work directly; this script needs no scope change because `git-credential-tannevaled` serves the wide token to git over a pipe. It reads `gopin`'s own report, so a line saying `go.mod HELD` leaves `go.mod` alone, and it claims each repository through `agentsync` and skips what another session holds. |
| `scripts/wfmerge.sh` | **Refuses by default.** It lands workflow-touching PRs by pushing to the default branch, which `git-pre-push-guard` exists to stop. The fix is `gh auth refresh -s workflow`, once. |

## What is built into them, and why

### Every one of them says when it is not the code in this repository

`fleet.WarnIfStale` runs first in every `main`, costs one `compare` call, and
writes at most one line to stderr.

It is here because of a measured failure. A guard landed on 2026-09-27 that
sets aside a red belonging to a workflow every push skips. Nothing rebuilt the
installed binaries, so for four days `redscan` reported the same three
repositories as RED default branches — `go-fsctl/go-fsctl.github.io`,
`go-ruby-hanami/go-ruby-hanami.github.io`, `nano-container-linux/dnsctl` — and
the fix for exactly that was sitting on `main`, 32 commits ahead. Rebuilt:
**1998 of 1998, 0 red.** Nothing was broken, every number was wrong, and no
output said so.

It reads the **VCS stamps**, not the module pseudo-version, because only the
stamps carry whether the tree was clean, and a reading from a modified tree is
not one anybody can reproduce.

⛔ It is silent in exactly one case: built from the head of the default branch,
from a clean tree. It says something in every other case **including "I could
not tell"** — a check that falls silent when it cannot answer teaches you to
trust a silence it never earned.

The test that keeps it honest enumerates `cmd/` **on disk** rather than naming
the commands: a list written in a test has to be remembered, a directory
listing cannot be forgotten, so a ninth command fails the suite until it is
wired up. Both sabotage directions are checked.

To rebuild them all:

    GOWORK=off GOBIN=~/.local/bin go install ./cmd/...


**`prmerge` never merges a Go toolchain bump.** The org presets set
`automerge: false` on it, and a sweep that merges whatever is green does not
know that. Mine took two before the guard existed. It now skips them with the
reason `go-toolchain-left-to-a-person`; one pass skipped 18.

**`prmerge` reads `agentsync`'s leases and stays out of claimed repositories.** A
fleet sweep cannot claim a hundred repos without blocking every other session,
so it does the opposite: it looks at what someone else claimed and leaves it
alone. See `../agentsync`.

**`prmerge` treats "no checks at all" as not-green.** A PR with nothing run
against it cannot be merged on evidence. This is also how repositories with no
CI get found.

**`tidyall.sh` names its work directory per PULL REQUEST**, because two PRs in
one repository otherwise collide and the second clone fails — which reads as an
auth error and is not one. It tidies **every `go.mod` in the tree**: a
sub-module keeps its own, and a lane that vets it fails while the root module is
spotless (`go-widgets/toolkit` has `rougelex/`). It tolerates modules that
cannot resolve standalone, but requires the ROOT one to tidy.

## What `quietscan` is, and the five rules it will not soften

Nothing else in the fleet queries run recency. `redscan` fetches `created_at`
and never compares it to anything: it branches on `conclusion` alone, and
`status=completed&per_page=1` on a repository with zero runs returns `{}` -- so
a runner that has NEVER run is invisible to it.

**Twelve hours of slack, and the observed lag printed every pass.** GitHub's
scheduler was measured on this fleet running 1h59m to 7h56m behind its crons. A
one-hour tolerance would have paged on 113 runners that were working perfectly,
and the eight hours this shipped with left **four minutes** of headroom against
the worst lag observed on the second day. A pager that goes off on a fleet with
nothing wrong is the same failure as an informational section nobody reads, and
it costs more. Because the number drifts, `quietscan` prints the distribution on
every pass and says so out loud when the worst lag passes three quarters of the
slack -- the threshold is a measurement to re-take, not a constant to trust.

**Age against each runner's OWN cron, never a constant.** The fleet's runners
are staggered across 24 hours -- `0 4`, `44 16`, `11 15`, `33 12` -- so the
period is read from each workflow's own `cron:` line.

**"Not yet due" is a different verdict from "never fired."** Conflating them
produced a false alarm about 26 dead runners that were merely queued behind a
schedule they had not reached. The birth date is keyed on the workflow's
`created_at`, and the classifier starts its clock at the last run, falling back
to the birth date ONLY when there has never been one. Never on the last commit
to the workflow file: a healthy Renovate rewrites that file every time it bumps
its own action pin, so a monitor keyed on the file's mtime resets its own clock
whenever Renovate works, and goes blind precisely when things are fine. Taking
the LATER of birth and last run reintroduces the same blindness by another door,
and did, until a test caught it.

**Four verdicts, four different fixes, never one total.** `archived` (no pull
request can fix it), `disabled_inactivity` (re-enable -- and it expires again in
60 days), `disabled_manually` (may be entirely legitimate), and the runner that
simply stopped. Each section names its own fix.

**Driven from the ORGANISATION list, not the runner list.** A recency check over
runners structurally cannot see an organisation that has no runner to be quiet.

## Attribution: the property belongs to whatever COVERS the repository

The first version got its headline right and its informational output wrong, in
two ways that were the same mistake twice.

It reported `no_runner` for 199 organisations. They were not uncovered: a
Renovate runner does not watch the repository it lives in, it watches whatever
its `autodiscoverFilter` names, and `go-ruby-stdlib/renovate-runner` names
`go-ruby-*/**` -- 204 organisations from one repository. And it warned that some
twenty `go-ruby-*/.github` repositories were 53 days from a 60-day latch, when
those repositories hold **no workflow at all**: there is no schedule there for
GitHub to disable. The repository that actually carries that risk for all of
them is the shared runner, and that is not where the warning pointed.

So `quietscan` reads every runner's `autodiscoverFilter` and resolves coverage
fleet-wide:

- An organisation reached by a live runner's filter reads `covered`, **naming
  the runner** -- a count by default, a list under `-all`. What is left in
  `no_runner` is what the verdict was built for, and it is printed even at zero,
  because "no organisation in this fleet is unwatched" is a strong statement and
  a section that vanishes when empty cannot make it.
- The idle/latch warning applies **only to a repository that holds a scheduled
  workflow**, names the runner, and says how many organisations its silence
  would take with it.
- A **retired** runner covers nothing. `go-attest/renovate-runner` is
  `disabled_manually`, so the organisations it used to watch are not covered by
  it any more; the line says what it used to watch.
- A filter that could not be read is reported as **unread, never as zero**.
  `go-attest/renovate-runner` builds its filter with `mine.map((o) => ...)` over
  a list sliced by an env var, and no string scan can know what that resolves
  to. Treating it as no coverage would be the same mistake in a smaller coat.

**The Renovate App is the third form of coverage**, asked about only where it
can change an answer -- an organisation with no runner and no filter reaching
it. `openweft` has the App installed and 16 open pull requests from it, and no
census of workflows can see that: there is no workflow, no cron and no
repository to be quiet. A refused installations read is `coverage unknown (App
visibility)` on a line of its own. **An unread installation is unread, not
absent**, and that rule now runs the other way too: if a LIVE runner's filter
could not be read, an organisation "no filter reaches" might in fact be reached
by it, so it reads `coverage_unknown` rather than becoming a gap.

Both candidate repositories -- `.github` and `renovate-runner` -- are probed in
every organisation, not just until one answers: `go-attest` holds an active own
runner beside the retired shared one, and stopping at the first would have
hidden the retirement.

**Coverage is a snapshot with a time on it.** `go-pdfkit/renovate-runner` was
retired between two passes an evening apart, and nothing says it could not be
retired between two calls of one pass. Every runner that others are counted as
covered by is re-read at the end of the pass; if one moved, coverage is
re-resolved from scratch and the move is printed. The header carries the window
the runners were read in, not a single instant.

A watcher that prints 199 non-problems and a latch date that cannot arrive gets
scrolled past, and then the one real page is scrolled past with it. That is the
failure mode for an alerting tool -- not being wrong, but being ignored.

## The trap `quietscan` exists to catch before it latches

All runner repositories here are public, so GitHub's 60-day inactivity rule
applies, and a `.github` repository whose only content is the runner receives no
push except Renovate's own. **If Renovate stops, the repository goes quiet; at
day 60 GitHub disables the schedule; and the disablement makes the silence
permanent.** Re-enabling expires again. The durable answer is a working
Renovate, which is why `quietscan` warns at 45 days -- fifteen days of margin --
and prints the date each repository latches.

## A failed call must never become a verdict

The core budget was exhausted mid-evening with three sessions on the same fleet,
and **`/rate_limit` reported `core 5000/5000` while a real request returned 403
with `X-RateLimit-Remaining: 0`**. So the budget is judged by what the API did,
never by what it says about itself.

The worst possible outcome for a watcher is a verdict derived from a call that
did not answer -- an outage wearing the mask of a stopped runner. Every refused
read becomes `unreadable`, which pages, and never `never_fired`, `overdue` or
`no_runner`. Three specific holes were closed:

- `isNotFound` requires the **404 itself**, not the words around it. A 403 that
  happens to say "Not Found" would otherwise have read as "this organisation has
  no runner".
- A failed `config.js` read used to leave only a note, silently removing a
  runner's coverage and turning its 204 organisations into gaps. It now makes
  them `coverage_unknown`, naming the runner that could not be read.
- A **core** rate-limit refusal fails fast instead of backing off. It refills on
  the hour, so it will not clear inside a pass, and 1300 calls x 5 sleeps is not
  a pass but a hang. A **secondary** limit still backs off, because that one is
  genuinely transient. A rate-limited pass prints a banner saying its verdicts
  are incomplete.

## What one pass costs, and when not to run it

About **1300 REST calls, roughly 26% of an hour's core budget**: two repository
probes per organisation, then the workflow, its cron, its `config.js` and its
last scheduled run per runner, plus one installations read per otherwise-uncovered
organisation. An archived runner costs one call and a disabled one three -- their
cron and run history decide nothing, though their filter still does.

**Twice a day is comfortable; a tighter loop is not.** Do not run it beside
another fleet sweep -- `redscan` and `prmerge` walk 1800 repositories, and two of
those in one hour will starve one or the other. Three sessions working this fleet
concurrently exhausted the budget once already, and eight were holding leases on
it the same evening.

`-orgs a,b,c` checks a handful for about four calls each, which is how to
re-check one organisation without spending a quarter of the hour. A narrowed
pass **cannot establish a gap**, because coverage is decided by other
organisations' runners: `-orgs go-ruby-aasm` alone cannot see the filter that
covers it. So `no_runner` is unreachable under `-orgs` -- those organisations
read `coverage_unknown` naming the narrowing. Safe by construction, not safe if
you read the banner. What a narrowed pass CAN establish -- a runner's own
health, an App installed on that organisation -- it still reports.

## Two things `quietscan` is not

**It is not a scheduled GitHub Actions workflow.** A watcher that shares the
failure mode it watches for is not a watcher. Run it from this machine like its
three siblings, or from `launchd` if it is to be unattended. It exits 1 when
anything pages.

**It never dispatches a run.** A dispatch opens real pull requests across an
organisation, and `actions/workflows/{id}/dispatches` is one path segment from
the runs listing it reads all day. So every call passes through `readOnly`,
which allows `gh api` and nothing else -- an allowlist of one, because the first
version of that gate was a denylist of shapes I had thought of, and its own test
caught `gh workflow run` walking straight past it.

**The runs listing is filtered to `event=schedule`**, and that filter is the
whole tool. On the pass that found them, twelve runners that had never once
started on their own carried *successful* `workflow_dispatch` runs from rollout
day. Any check that counts "a run" rather than "a scheduled run" calls all
twelve healthy.

    quietscan                                    # the live fleet
    quietscan -all                               # every runner, and every covered org
    quietscan -fixture cmd/quietscan/testdata/broken.json -all
    quietscan -fixture cmd/quietscan/testdata/unread-filter.json -all

The fixtures are how the watcher is proved: doctored copies of one real runner,
each changed in exactly one way, so the verdict names what was changed. A
watcher that has never fired in anger is not known to work, and this one's whole
purpose is to speak when everything looks quiet.

`broken.json` walks the verdict set. `unread-filter.json` is separate because it
would swallow the first: a LIVE runner whose filter could not be read casts
doubt on every organisation no readable filter reaches, so `no_runner` correctly
drops to zero there and `coverage_unknown` takes its place. That is the rule
working, and it is easier to see on its own.

## The failure these were built for

Renovate writes the new `go.sum` lines and **leaves the superseded ones**, so any
lane asserting `go mod tidy` produces no diff fails on `go.sum` alone — with the
module itself perfectly fine. The message names no file. It hit 11 PRs at once
across four organisations. `tidyall.sh` is the pass that fixes it.

## The workflow-scope blocker

`gh pr merge` cannot merge a pull request that edits `.github/workflows/*`: the
merge API refuses an OAuth app without the `workflow` scope, and gh's keyring
token carries `repo` but not `workflow`. On 2026-08-31 this was the single
largest blocker in the fleet -- 64 green pull requests waiting on it in one
pass, after 69 had already been landed by hand.

`wfmerge.sh` was the workaround: squash locally, push to the default branch.
That is exactly what `git-pre-push-guard` was installed to stop -- "a fix went
straight onto main by habit, green, tested and reviewed by nobody" -- so the
script now refuses unless someone says `WFMERGE_I_MEAN_IT=1` for a specific
case.

**The fix is one command, and it is not automatable**: `gh auth refresh -s
workflow` opens a browser. Until it is run, these pull requests wait.

## Layout and gates

    quiet/           the cron reader and the verdict, with its tests
    cmd/<name>/      one sweep each

    go build ./... && go vet ./...
    go test ./...

`quiet` is held at 100% of statements, the standard `gitsafe/protect` and
`gitsafe/redact` hold: it is the part that can be wrong in a way nobody
notices. The command around it is at 62%, with `read()` exercised against a
stub `gh` on PATH -- a wrong field name there would make every runner in the
fleet look healthy, silently.

See "What one pass costs" above before scheduling it beside another sweep.
