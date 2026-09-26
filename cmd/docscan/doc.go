// docscan reports every repository an organisation HAS but does not ADVERTISE
// — and every one it advertises and no longer has.
//
//	docscan            # the whole fleet
//	docscan -orgs a,b  # narrowed, and it says so
//	docscan -orgs o -tree o/o.github.io=. -fail-on-drift   # judge a branch
//
// It exists because a written rule was not enough, and because the drift is
// invisible from inside a repository. On 2026-09-25 go-fsctl had seven modules
// and told the world about five: blk and outdir were created after the landing
// was last touched, so they were absent from data/repos.yaml, from the docs
// nav, from the home-page table and from the organisation profile — four
// surfaces, none of which anybody opens while editing code. outdir's
// documentation URL returned 404 for three weeks.
//
// Worse, the landing's own comment said "one card per real, non-fork repo in
// the org" while listing five of seven. A comment stating a rule does not keep
// the data to it.
//
// The next day the same check over seven more organisations found
// go-compressions naming seven of thirteen — including bzip2, the encoder Go's
// standard library does not have, which is the reason somebody visits that
// organisation at all.
//
// # It matches ENTRIES, not words
//
// A first pass at this compared repository names as words against the whole
// landing file. It reported go-compressions/compress and matchlen as present:
// both appear in PROSE, matchlen inside another module's description
// ("delegates to matchlen's SIMD common-prefix kernel"). A word that appears is
// not an entry that exists, and a sweep that cannot tell them apart
// under-reports in the direction that looks reassuring.
//
// # Read the branch, not the default one
//
// Every surface is read over the API, which serves the DEFAULT BRANCH. Run as a
// pull-request check inside the landing repository, that judges main — so the
// pull request that FIXES the drift is told the drift is still there, and the
// one that DELETES every card is told nothing is wrong. The second half is why
// -tree exists rather than a re-run after merging: it names a checkout to read
// one repository's surfaces from, and refuses a path it cannot read instead of
// falling back to the API, which would pass while reading nothing.
//
// # Being unlisted is not always wrong
//
// A repository may be deliberately quiet: an experiment, an internal helper, a
// module that moved. So this prints WHICH surfaces name it and which do not,
// rather than a verdict.
//
// Under -fail-on-drift a verdict is what it gives, and for a while the failure
// said "say so by listing it and marking it" while offering no marking at all.
// The only way to pass was to put a card on the page — including for
// go-composites/is, whose whole content is a README with a banner and a
// heading. A row pointing at nothing is worse than no row, so the instruction
// as given made the page worse.
//
// A .docs-unlisted file at the root of the tree given with -tree names one
// repository per line and, after a space, WHY. The reason is required: an
// allowance that can be written silently is the check switched off one line at
// a time. The line keeps being printed, as "unlisted on purpose: <reason>", so
// the repository moves from invisible to accounted for rather than out of the
// report. The file lives in the tree under review, so it turns up in the pull
// request's own diff. The reverse direction is reported too — a surface
// naming a repository that is gone or archived sends a reader to a tombstone,
// which is how go-compressions kept pointing at matchlen after it moved to
// go-simd.
package main
