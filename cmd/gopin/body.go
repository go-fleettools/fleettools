package main

import (
	"fmt"
	"os"
	"strings"
)

var osWriteFile = os.WriteFile

// prBody explains the change in the pull request, with this repository's own
// numbers rather than a generic paragraph.
func prBody(f finding, want string) string {
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
