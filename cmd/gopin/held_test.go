package main

import (
	"strings"
	"testing"
)

func TestHeldBackByNamesTheOldestPinThatIsBehindTheTarget(t *testing.T) {
	target := mustParse("1.27.1")
	for _, c := range []struct {
		name     string
		literals []string
		want     string
		held     bool
	}{
		{"no literals at all", nil, "", false},
		{"one older pin holds it", []string{"1.26.4"}, "1.26.4", true},
		{"the oldest of several wins", []string{"1.26.4", "1.25.0", "1.26.8"}, "1.25.0", true},
		{"a newer pin holds nothing", []string{"1.28.0"}, "", false},
		{"the target itself holds nothing", []string{"1.27.1"}, "", false},
		// A spec with no patch resolves to the newest patch in its minor:
		// measured on cloud-boot/tamago-uefi, `1.27.x` installed go1.27.1. So
		// it is NOT behind 1.27.1, and withholding go.mod for it would be a
		// raise refused for nothing. reLiteral captures `1.27` from `1.27.x`.
		{"a patchless pin in the target's own minor holds nothing", []string{"1.27"}, "", false},
		{"a patchless pin in an older minor does hold", []string{"1.26"}, "1.26", true},
		{"an unparsable pin is not reasoned about", []string{"stable"}, "", false},
		{"an older pin wins over an unparsable one", []string{"stable", "1.26.4"}, "1.26.4", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, held := heldBackBy(c.literals, target)
			if got != c.want || held != c.held {
				t.Errorf("heldBackBy(%v) = (%q, %v); want (%q, %v)", c.literals, got, held, c.want, c.held)
			}
		})
	}
}

// The defect this guards against is silent. Raising the directive past a
// deliberate older pin does not fail: with GOTOOLCHAIN unset, go DOWNLOADS the
// newer toolchain, so the lane goes on printing the pinned version in its setup
// step while running the one it was pinned away from. Measured on go1.26.4
// against a module asking for 1.27.1:
//
//	GOTOOLCHAIN=go1.26.4+auto  ->  runs go1.27.1
//	GOTOOLCHAIN=go1.26.4       ->  go.mod requires go >= 1.27.1, refused
//
// go-gfx/gfx is the live case: its loong64 lane names go1.26.4 because
// golang/go#81000 miscompiles that package there, and golang/go#81147 — the
// 1.27 backport — is still open.
func TestOpenHoldsTheGoDirectiveWhenAJobPinsAnOlderVersion(t *testing.T) {
	r := &fakeRepo{files: map[string]string{
		".github/workflows/ci.yml": "          go-version: stable\n          go-version: '1.26.4'\n",
		"go.mod":                   "module x\n\ngo 1.26.4\n",
	}}
	defer r.install(t)()

	f := finding{Repo: "o/r", Files: []string{"ci.yml"}, Aliases: 1, GoMod: "1.26.4", Literals: []string{"1.26.4"}}
	if err := open("o/r", f, "1.27.1"); err != nil {
		t.Fatalf("open: %v", err)
	}

	if got := r.read(t, "go.mod"); !strings.Contains(got, "go 1.26.4") {
		t.Errorf("go.mod was raised past the 1.26.4 pin, which defeats it: %q", got)
	}

	// The alias is still pinned: withholding the directive must not turn into
	// withholding the whole change, or the repository keeps its moving channel.
	wf := r.read(t, ".github/workflows/ci.yml")
	if strings.Contains(wf, "stable") {
		t.Errorf("an alias survived:\n%s", wf)
	}
	if !strings.Contains(wf, "'1.26.4'") {
		t.Errorf("the deliberate pin was not left alone:\n%s", wf)
	}
}

// A pull request that silently omitted the directive would be worse than one
// that raised it: the next reader cannot see a decision nobody wrote down.
func TestPRBodySaysWhyTheDirectiveStayed(t *testing.T) {
	f := finding{Repo: "o/r", Files: []string{"ci.yml"}, Aliases: 1, GoMod: "1.26.4", Literals: []string{"1.26.4"}}
	body := prBody(f, "1.27.1")
	for _, want := range []string{
		"deliberately NOT raised",
		"GOTOOLCHAIN=go1.26.4+auto",
		"downloading",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the body never says %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Raised from `go 1.26.4`") {
		t.Error("the body still promises a raise that will not happen")
	}
}

// Without a pin behind the target, nothing changes: the raise is the normal case.
func TestPRBodyStillPromisesTheRaiseWithoutAnOlderPin(t *testing.T) {
	f := finding{Repo: "o/r", Files: []string{"ci.yml"}, Aliases: 1, GoMod: "1.26.4"}
	body := prBody(f, "1.27.1")
	if !strings.Contains(body, "Raised from `go 1.26.4` to `go 1.27.1`") {
		t.Errorf("the ordinary raise is no longer announced:\n%s", body)
	}
	if strings.Contains(body, "deliberately NOT raised") {
		t.Error("a repository with no older pin was reported as held back")
	}
}
