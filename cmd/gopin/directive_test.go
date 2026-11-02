package main

import (
	"strings"
	"testing"
)

// ⛔⛔ THE EXPOSURE gopin COULD NOT SEE. Measured 2026-11-01 on the Go 1.27.3
// campaign: of the first 80 repositories whose PUBLISHED go.mod declared
// 1.27.1, modfloor said all 80 were below the floor and gopin reported 79 as
// "already explicit" — their CI names a version rather than reading go.mod,
// so --gomod never looked at the directive. One in eighty was actionable, and
// the exposure is in the directive, because that is the floor a CONSUMER
// honours when it runs `go install` with GOTOOLCHAIN=local.
func TestDirectiveRaisesAGoModNoWorkflowReads(t *testing.T) {
	w := newWorld(t)
	// CI names 1.27.3 explicitly — newer than the directive, and nothing reads
	// go.mod. This is the shape of 79 of those 80 repositories.
	w.repo("o/explicit", "          go-version: '1.27.3'\n", "module x\n\ngo 1.27.1\n")

	// Without --directive it is "pinned" and nothing happens, which is the
	// behaviour this flag exists to change — asserted so the control is in the
	// test and not only in the prose.
	code, out, _ := w.run("-from", "1.27.1", "-version", "1.27.3", "-list", w.list("o/explicit"))
	if code != 0 || strings.Contains(out, "REPIN") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	// ...but it must SAY the directive is below, because silence there is how
	// a repository below the floor reads as clean.
	if !strings.Contains(out, "go.mod 1.27.1 is below 1.27.3") {
		t.Errorf("a pinned repository below the floor must say so:\n%s", out)
	}

	code, out, _ = w.run("-from", "1.27.1", "-version", "1.27.3", "--directive",
		"-list", w.list("o/explicit"), "--apply", "-pause", "0s")
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if !strings.Contains(out, "REPIN   o/explicit: directive go.mod (go 1.27.1 -> 1.27.3)") {
		t.Fatalf("the act must be named for what it is:\n%s", out)
	}
	// A pull request was actually opened for it, which is the whole point.
	if len(w.pushed) != 1 {
		t.Fatalf("pushed = %v", w.pushed)
	}
}

// ⛔ THE HOLD RULE STILL DECIDES. A workflow naming a version OLDER than the
// target breaks the moment the directive goes past it: setup-go sets
// GOTOOLCHAIN=local, so the pin is enforced rather than re-downloaded. Such a
// repository is left exactly as it is.
func TestDirectiveIsHeldByAnOlderPin(t *testing.T) {
	w := newWorld(t)
	w.repo("o/held", "          go-version: '1.26.4'\n", "module x\n\ngo 1.27.1\n")
	code, out, _ := w.run("-from", "1.27.1", "-version", "1.27.3", "--directive",
		"-list", w.list("o/held"), "--apply", "-pause", "0s")
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if strings.Contains(out, "REPIN") {
		t.Fatalf("a directive held back by an older pin was raised:\n%s", out)
	}
	if !strings.Contains(out, "HELD by the 1.26.4 pin") {
		t.Errorf("the hold must be reported, not silent:\n%s", out)
	}
	if len(w.pushed) != 0 {
		t.Errorf("a held repository was pushed: %v", w.pushed)
	}
}

// A directive that is not EXACTLY --from is not a candidate, the same rule the
// workflow pins follow: "anything older" cannot tell a stale floor from a
// deliberate one.
func TestDirectiveMovesAnExactVersionOnly(t *testing.T) {
	w := newWorld(t)
	w.repo("o/other", "          go-version: '1.27.3'\n", "module x\n\ngo 1.26.0\n")
	code, out, _ := w.run("-from", "1.27.1", "-version", "1.27.3", "--directive",
		"-list", w.list("o/other"), "--apply", "-pause", "0s")
	if code != 0 || strings.Contains(out, "REPIN") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if len(w.pushed) != 0 {
		t.Errorf("a directive that is not exactly --from was moved: %v", w.pushed)
	}
}

// ⛔ The pull request must say which act it is performing. A body claiming the
// directive is NOT raised, on a pull request that raises it, is the one thing a
// campaign must never do — and that denial is exactly what a -from body says by
// default.
func TestTheBodySaysItRaisesTheConsumersFloorAndNeedsARelease(t *testing.T) {
	f := finding{Repo: "o/x", GoMod: "1.27.1", GoModFiles: []string{"go.mod"}, Directive: true}
	body := moveBody(f, "1.27.3", "1.27.1")
	for _, want := range []string{
		"different change from moving the CI pin",
		"floor a CONSUMER honours",
		"GOTOOLCHAIN=local",
		"not finished until this is RELEASED",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the body must say %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "`go.mod` is **not** raised") {
		t.Error("the body denies the change it makes:\n" + body)
	}
	if strings.Contains(body, "go-version-file") {
		t.Error("nothing here reads go-version-file:\n" + body)
	}

	msg := moveCommitMessage(f, "1.27.3", "1.27.1")
	if !strings.Contains(msg, "DIFFERENT change from moving the CI pin") ||
		!strings.Contains(msg, "RELEASED") {
		t.Error("the commit message outlives the body and must say it too:\n" + msg)
	}

	// And the go-version-file case keeps its own wording, unchanged.
	vf := moveBody(finding{Repo: "o/y", GoModFiles: []string{"go.mod"}}, "1.27.3", "1.27.2")
	if !strings.Contains(vf, "go-version-file") || strings.Contains(vf, "floor a CONSUMER honours") {
		t.Error("the go-version-file body changed:\n" + vf)
	}
}

// The prefilter decides from the go.mod, not from any workflow: a repository
// whose CI names an explicit version has nothing in its workflows to match, and
// dropping it there is how the 79 were filtered out before the REST read ever
// saw them.
func TestPrefilterKeepsADirectiveCandidateWithNothingInItsWorkflows(t *testing.T) {
	cfg := config{from: "1.27.1", version: "1.27.3", directive: true}
	wf := []string{"          go-version: '1.27.3'\n"}
	if !wantsMove(wf, "module x\n\ngo 1.27.1\n", cfg) {
		t.Error("a directive candidate was filtered out")
	}
	if wantsMove(wf, "module x\n\ngo 1.27.3\n", cfg) {
		t.Error("a repository already at the target was kept")
	}
	// A root go.mod GraphQL could not return is passed on, never dropped.
	if !wantsMove(wf, "", cfg) {
		t.Error("an unread go.mod must be passed on to the REST read")
	}
	// And without the flag the old answer stands.
	if wantsMove(wf, "module x\n\ngo 1.27.1\n", config{from: "1.27.1", version: "1.27.3"}) {
		t.Error("--directive changed the behaviour of a run that did not ask for it")
	}
}
