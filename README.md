# fleet — sweeping ~1800 repositories across ~320 organisations

Written 2026-08-30 while getting every default branch green. They lived in a
scratchpad, which is purged; they are here because each one encodes something
that cost a wrong answer to learn.

| | |
|---|---|
| `cmd/redscan` | Every default branch whose last run failed. One pass: 1819 repositories, 44 red — **all of them stale Renovate lanes** whose last run predated a token rotation by three hours. A red branch is not a broken one; check WHEN it last ran. |
| `cmd/prscan` | Open pull requests across every org. Batches ~15 `org:` qualifiers per search query, so ~20 calls instead of 320 — the search API is rate-limited far more tightly than REST. |
| `cmd/prmerge` | Merges dependency PRs that are mergeable **and** fully green. |
| `scripts/tidyall.sh` | Runs `go mod tidy` on a PR branch and pushes only if it changed something AND the tree still builds. |
| `scripts/wfmerge.sh` | Lands PRs that edit a workflow file, which `gh pr merge` cannot: the merge API refuses an OAuth app without the `workflow` scope. |

## What is built into them, and why

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

## The failure these were built for

Renovate writes the new `go.sum` lines and **leaves the superseded ones**, so any
lane asserting `go mod tidy` produces no diff fails on `go.sum` alone — with the
module itself perfectly fine. The message names no file. It hit 11 PRs at once
across four organisations. `tidyall.sh` is the pass that fixes it.

    go build ./... && go vet ./...
