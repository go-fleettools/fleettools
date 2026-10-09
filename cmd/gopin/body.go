package main

import (
	"fmt"
	"os"
	"strings"
)

var osWriteFile = os.WriteFile

// ⛔ A MESSAGE THAT APPEARS IN THREE PLACES MUST BE CORRECTED IN THREE.
//
// The -from case shipped with a corrected pull-request TITLE and these two
// untouched. ghmerge squash-merges the COMMIT, so five go-pkgx repositories
// now carry "ci: pin Go 1.27.2 instead of `stable`" in their permanent
// history, about workflows that never said `stable` — while the pull request
// above them said the right thing.
//
// Fixing one surface of three is worse than fixing none: the inconsistency
// looks deliberate, and a reader who checks the commit against the title
// cannot tell which to believe. The test now reads all three.
func moveBody(f finding, want, from string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "This repository pins Go **%s** in `%s` — **%d** occurrence(s). "+
		"This moves them to **%s**.\n\n", from, strings.Join(f.MoveFiles, "`, `"), f.Moves, want)
	b.WriteString("## Why move a pin that is already explicit\n\n")
	b.WriteString("Pinning was the right thing: an alias has no version to review. " +
		"But a pinned fleet cannot move, and a Go patch release is usually a " +
		"**security** release — the standard library is compiled into every binary, " +
		"so the toolchain that built it is the toolchain it carries.\n\n")
	b.WriteString("Check what this one fixes with `govulncheck ./...` against both versions, " +
		"rather than taking the release notes' word for whether it reaches this code.\n\n")
	b.WriteString("## It moves an exact version, and only that one\n\n")
	fmt.Fprintf(&b, "`-from %s` names the version to move. \"Anything older\" cannot tell a stale "+
		"pin from a deliberate one, so a job held back on purpose at some other version is "+
		"not a candidate here and is left exactly as it is.\n\n", from)
	b.WriteString("`go.mod` is **not** raised. The directive is a minimum for consumers; " +
		"the workflow pin is what builds the artefacts.\n\n")
	b.WriteString("🤖 Generated with [Claude Code](https://claude.com/claude-code)\n")
	return b.String()
}

// moveCommitMessage is moveBody's counterpart, and the one that outlives it.
func moveCommitMessage(f finding, want, from string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ci: move the Go pin from %s to %s\n\n", from, want)
	fmt.Fprintf(&b, "%d occurrence(s) in %s.\n\n", f.Moves, strings.Join(f.MoveFiles, ", "))
	b.WriteString("Pinning was right: an alias has no version to review. But a pinned\n" +
		"fleet cannot move, and a Go patch release is usually a SECURITY\n" +
		"release -- the standard library is compiled into every binary, so the\n" +
		"toolchain that built it is the toolchain it carries.\n\n")
	fmt.Fprintf(&b, "An EXACT move: -from %s names the version to change, so a job held\n"+
		"back on purpose at some other version is not a candidate and is left\n"+
		"as it is.\n\n", from)
	b.WriteString("go.mod is not raised: the directive is a minimum for consumers, and\n" +
		"the workflow pin is what builds the artefacts.\n\n")
	b.WriteString("Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>\n")
	return b.String()
}

// prBody explains the change in the pull request, with this repository's own
// numbers rather than a generic paragraph.
func prBody(f finding, want, from string) string {
	if f.Aliases == 0 && f.Moves > 0 {
		return moveBody(f, want, from)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "`go-version: stable` is a channel alias, not a version. "+
		"This repository has **%d** of them, in `%s`.\n\n", f.Aliases, strings.Join(f.Files, "`, `"))

	b.WriteString("## Why an alias is the defect\n\n")
	b.WriteString("**The toolchain moves with no commit.** Measured on go-pkgx on 2026-10-04: CI was " +
		"building and running on `go1.27.1` — `Setup go version spec stable` → `go version go1.27.1` " +
		"in the job log — while every `go.mod` there still said `go 1.26.4`. That is a *minor* Go " +
		"bump, the one that reindents under `gofmt` and changes coverage block counting, and it " +
		"arrived with nothing to review.\n\n")
	b.WriteString("**A Renovate rule written for exactly this cannot act on it.** The shared preset " +
		"matches dep name `go` under the `gomod` and `github-actions` managers with " +
		"`automerge: false` and `groupName: null`, citing `golang/go#81000`. It had never produced a " +
		"pull request, because there was no version to compare or rewrite.\n\n")
	b.WriteString("**The contrast is demonstrable, not argued.** In one `ci.yml` " +
		"(`go-pkgx/registry-viewer`) the `build` job said `stable` and Renovate did nothing; the " +
		"`bricolint` job said `\"1.26.4\"` and Renovate opened a pull request moving it to `1.27.1`. " +
		"Same file, same run, one variable.\n\n")
	b.WriteString("So pinning is not rigidity — it is the condition for the review rule to apply at all.\n\n")

	held, isHeld := heldBackBy(f.Literals, mustParse(want))
	switch {
	case f.GoMod != "" && isHeld:
		fmt.Fprintf(&b, "## `go.mod` is deliberately NOT raised\n\nIt stays at `go %s`, because a job "+
			"here names `%s` on purpose and raising the directive past that would break the pin rather "+
			"than merely look inconsistent with it. With `GOTOOLCHAIN` unset — the default, and what "+
			"`setup-go` leaves in place — `go` satisfies a directive it is too old for by "+
			"**downloading** a newer toolchain. Measured on go1.26.4 against a module asking for "+
			"1.27.1:\n\n"+
			"```\nGOTOOLCHAIN=go1.26.4+auto  ->  runs go1.27.1\n"+
			"GOTOOLCHAIN=go1.26.4       ->  go.mod requires go >= 1.27.1, refused\n```\n\n"+
			"So the lane would keep printing `%s` in its setup step while running the version it was "+
			"pinned away from, and whatever that pin was protecting would come back with nothing to "+
			"show it.\n\nThe aliases in this repository still become `go %s`; it is only the "+
			"directive that stays where it is, and a later commit can raise it once the pin goes.\n\n",
			f.GoMod, held, held, want)
	case f.GoMod != "":
		fmt.Fprintf(&b, "## `go.mod`\n\nRaised from `go %s` to `go %s`, so the module says the version "+
			"CI has in fact been using. Note that an importer on an older Go with `GOTOOLCHAIN=local` "+
			"can no longer build it — that is a compatibility change, and worth a minor version if this "+
			"module is tagged.\n\n", f.GoMod, want)
	}
	if len(f.Literals) > 0 {
		fmt.Fprintf(&b, "## Left alone\n\nThis repository also names %s explicitly somewhere. Those are "+
			"untouched: somebody chose them, possibly to hold something back, and moving them silently "+
			"is the opposite of the point.\n\n", "`"+strings.Join(f.Literals, "`, `")+"`")
	}
	b.WriteString("## What to check before merging\n\n")
	b.WriteString("CI is the control, and two lanes are worth a glance:\n\n")
	b.WriteString("- **`gofmt`**, if this repository gates it. A minor Go bump reindents; a patch does not.\n")
	b.WriteString("- **`loong64` under qemu**, if this repository runs it. `golang/go#81147`, the 1.27 " +
		"backport of the loong64 miscompile, is still open — so the defect is in the toolchain being " +
		"pinned. It does not reach most code (go-pkgx's five qemu lanes are green on 1.27.1) but it " +
		"does reach some (`go-gfx/gfx` fails exactly there). A repository that only *cross-compiles* " +
		"for loong64 cannot tell the difference, because a code-generation defect cannot redden a " +
		"build that never runs.\n\n")
	b.WriteString("🤖 Generated with [Claude Code](https://claude.com/claude-code)\n")
	return b.String()
}

// writeFile is os.WriteFile, named here so the test helper does not pull os
// into a file that otherwise has no business with it.
func writeFile(path, body string) error { return osWriteFile(path, []byte(body), 0o600) }

// commitMessage is what lands in the repository's own history, which outlives
// the pull request description.
func commitMessage(f finding, want, from string) string {
	if f.Aliases == 0 && f.Moves > 0 {
		return moveCommitMessage(f, want, from)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "ci: pin Go %s instead of `stable`\n\n", want)
	b.WriteString("`stable` is a channel alias, not a version, and that is the\n" +
		"defect: the toolchain moves with no commit and nothing to review.\n" +
		"Measured on go-pkgx on 2026-10-04, CI had been building and running\n" +
		"on go1.27.1 -- `Setup go version spec stable` / `go version go1.27.1`\n" +
		"in the job log -- while every go.mod there still said `go 1.26.4`.\n\n")
	b.WriteString("Worse, the shared Renovate preset carries a rule written for\n" +
		"exactly this (dep name `go`, automerge false, ungrouped, citing\n" +
		"golang/go#81000) and it had never produced a pull request: an alias\n" +
		"has no version to compare or rewrite. Demonstrated rather than\n" +
		"argued -- in ONE ci.yml, the job saying `stable` got nothing and the\n" +
		"job saying \"1.26.4\" got a Renovate PR, same file, same run.\n\n")
	if held, isHeld := heldBackBy(f.Literals, mustParse(want)); isHeld {
		fmt.Fprintf(&b, "go.mod is left alone: a workflow here names %s on purpose, and\nraising the directive past it would break that job.\n\n", held)
	} else if f.GoMod != "" {
		fmt.Fprintf(&b, "go.mod moves from %s to %s, so the module says the version CI\nhas in fact been using.\n\n", f.GoMod, want)
	}
	b.WriteString("Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>\n")
	return b.String()
}
