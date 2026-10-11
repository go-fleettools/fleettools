package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-fleettools/fleettools/internal/checkout"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

// ⛔ WHEN GORELEASE CANNOT LOAD THE MODULE GRAPH, THE API CAN STILL BE READ.
//
// gorelease loads the whole module graph before it compares anything. On
// 2026-10-10 five go-widgets modules (toolkit, tui, desktop, gallery,
// isoicons) were UNREAD on every platform with
//
//	go: github.com/ajroetker/go-highway/hwy/goat@v0.0.0: invalid version: unknown revision hwy/goat/v0.0.0
//
// go-highway v0.0.12 declares its code generator as a `tool` requirement at
// v0.0.0 and resolves it with a local `replace`, which Go ignores in a
// dependency -- so for every consumer that version exists nowhere
// (ajroetker/go-highway#73, #74). `go build` never needs the package and
// works; `go list -m all`, and therefore gorelease, does not. Nothing in OUR
// code is wrong, and requiring the published goat v0.0.1 does not hold:
// `go mod tidy` drops a requirement no package imports.
//
// apidiff (golang.org/x/exp/cmd/apidiff) compares the exported API package by
// package, through the same loader `go build` uses, so it reads what gorelease
// cannot. This file asks it ONLY for that one kind of refusal -- every other
// one (a dirty tree, a base gorelease will not accept) stays unread -- and
// applies gorelease's own rule to turn what it found into a number.
//
// A verdict reached this way is never "safe": gorelease does checks apidiff
// does not (retractions, a go.mod that is not tidy), so a person asks for it by
// category, and the report says how it was derived.

// graphUnreadable reports whether gorelease refused because it could not load
// the module graph, the one refusal apidiff can stand in for.
func graphUnreadable(err string) bool {
	return strings.HasPrefix(err, "go: ") && strings.Contains(err, "invalid version")
}

// askAPIDiff is the exec seam for the fallback; a test replaces it.
//
// gitDir is the CLONE; tree is the head materialised for reading. A materialised
// tree has no .git (checkout.MaterialiseAt says so), and the first version of
// this ran git there: "cannot read go.mod at v0.328.0" on every module.
var askAPIDiff = func(gitDir, tree, base, goos string) reading {
	r := reading{goos: goos, via: "apidiff"}
	dir := tree

	headMod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		r.err = "apidiff: " + err.Error()
		return r
	}
	path := modfile.ModulePath(headMod)
	if path == "" {
		r.err = "apidiff: go.mod names no module"
		return r
	}
	baseTree, cleanup, ok := checkout.MaterialiseAt(gitDir, base)
	if !ok {
		r.err = "apidiff: cannot materialise " + base
		return r
	}
	defer cleanup()
	baseMod, err := os.ReadFile(filepath.Join(baseTree, "go.mod"))
	if err != nil {
		r.err = "apidiff: no go.mod at " + base
		return r
	}

	tmp, err := os.MkdirTemp("", "vscan-apidiff-")
	if err != nil {
		r.err = "apidiff: " + err.Error()
		return r
	}
	defer os.RemoveAll(tmp)

	oldAPI, newAPI := filepath.Join(tmp, "old.api"), filepath.Join(tmp, "new.api")
	for _, w := range []struct{ tree, file string }{{baseTree, oldAPI}, {dir, newAPI}} {
		if out, err := runAPIDiff(w.tree, goos, "-m", "-w", w.file, path); err != nil {
			r.err = "apidiff: " + firstLine(out, err)
			return r
		}
	}
	out, err := runAPIDiff(dir, goos, "-m", oldAPI, newAPI)
	if err != nil {
		r.err = "apidiff: " + firstLine(out, err)
		return r
	}
	r.breaks, r.grew = parseAPIDiff(out)
	r.suggested, r.err = suggest(base, r.breaks > 0, r.grew > 0, requirementsChanged(baseMod, headMod))
	return r
}

func runAPIDiff(dir, goos string, args ...string) (string, error) {
	cmd := exec.Command("apidiff", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if _, lookErr := exec.LookPath("apidiff"); lookErr != nil {
			return "", fmt.Errorf("not installed: go install golang.org/x/exp/cmd/apidiff@latest")
		}
	}
	return string(out), err
}

func firstLine(out string, err error) string {
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "Ignoring internal package") {
			return l
		}
	}
	return err.Error()
}

// parseAPIDiff counts the changes listed under apidiff's two headings. It
// counts lines, not headings: a heading with nothing under it is no change.
func parseAPIDiff(out string) (breaks, grew int) {
	section := ""
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "Incompatible changes:"):
			section = "breaks"
		case strings.HasPrefix(line, "Compatible changes:"):
			section = "grew"
		case strings.HasPrefix(line, "- "):
			switch section {
			case "breaks":
				breaks++
			case "grew":
				grew++
			}
		case strings.TrimSpace(line) == "":
		default:
			section = ""
		}
	}
	return breaks, grew
}

// requirementsChanged is gorelease's own rule, restated: the go directive
// rose, a requirement was added, or one moved up a minor (or a major).
func requirementsChanged(baseMod, headMod []byte) bool {
	b, err1 := modfile.ParseLax("base/go.mod", baseMod, nil)
	h, err2 := modfile.ParseLax("head/go.mod", headMod, nil)
	if err1 != nil || err2 != nil {
		return true // cannot tell: say so with the larger number, never the smaller
	}
	if b.Go != nil && h.Go != nil && semver.Compare("v"+h.Go.Version, "v"+b.Go.Version) > 0 {
		return true
	}
	old := map[string]string{}
	for _, r := range b.Require {
		old[r.Mod.Path] = r.Mod.Version
	}
	for _, r := range h.Require {
		was, ok := old[r.Mod.Path]
		if !ok {
			return true
		}
		if semver.Compare(semver.MajorMinor(r.Mod.Version), semver.MajorMinor(was)) > 0 {
			return true
		}
	}
	return false
}

// suggest turns what changed into the next version, as gorelease does: below
// v1 anything but a pure patch is a minor; from v1 an incompatible change has
// no suggestion (it needs a new major path), and an addition or a requirement
// change is a minor.
func suggest(base string, breaks, grew, reqs bool) (string, string) {
	if !semver.IsValid(base) || semver.Prerelease(base) != "" || semver.Build(base) != "" {
		return "", "apidiff: base " + base + " is not a plain vX.Y.Z"
	}
	p := splitSemver(base)
	if p == nil {
		return "", "apidiff: base " + base + " is not a plain vX.Y.Z"
	}
	var major, minor, patch int
	fmt.Sscan(p[0], &major)
	fmt.Sscan(p[1], &minor)
	fmt.Sscan(p[2], &patch)
	switch {
	case breaks && major > 0:
		return "", "cannot suggest a version: Incompatible changes were detected."
	case breaks || grew || reqs:
		return fmt.Sprintf("v%d.%d.0", major, minor+1), ""
	default:
		return fmt.Sprintf("v%d.%d.%d", major, minor, patch+1), ""
	}
}
