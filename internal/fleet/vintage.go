package fleet

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	"golang.org/x/mod/module"
)

// Repository is where these tools live. The staleness check below asks GitHub
// about this repository, so a fork that renames it must change this line too.
const Repository = "go-fleettools/fleettools"

// Vintage is what a running binary can say about its own provenance.
//
// It comes from the VCS stamps the Go toolchain embeds when they are there,
// NOT from the module version: a checkout build may carry both, and only the
// stamps say whether that tree was clean -- a reading taken from a modified
// tree is not one anybody can reproduce. The module version is used only when
// there are no stamps at all, which is a module-mode build (see Module), whose
// tree is the published zip and cannot have been modified.
type Vintage struct {
	Revision string // full commit sha, empty when the binary does not say
	Modified bool   // the working tree had uncommitted changes

	// Module is the main module's version when the binary was built in
	// module mode -- `go install github.com/go-fleettools/fleettools/cmd/x@v0.4.0`
	// -- and so carries no VCS stamp at all. Such a build is made from the
	// module zip the proxy serves and sum.golang.org vouches for, so it is
	// clean by construction, and its version (a tag, or a pseudo-version
	// ending in a commit) is enough to compare with the default branch.
	// Empty for a checkout build, which says "(devel)" or carries stamps.
	Module string
}

// BuildVintage reads the stamps out of the running binary.
func BuildVintage() Vintage {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Vintage{}
	}
	v := vintageFrom(info.Settings)
	v.Module = moduleVersion(info.Main.Version)
	return v
}

// moduleVersion keeps a version a module-mode build records, and drops what a
// checkout build says instead: "(devel)", or nothing. Split out for the same
// reason as vintageFrom: a `go test` binary always says "(devel)".
func moduleVersion(m string) string {
	if m == "" || m == "(devel)" {
		return ""
	}
	return m
}

// moduleRef is what GitHub's compare endpoint accepts for a module version:
// the commit of a pseudo-version, the tag itself otherwise.
func moduleRef(version string) string {
	if module.IsPseudoVersion(version) {
		if rev, err := module.PseudoVersionRev(version); err == nil {
			return rev
		}
	}
	return strings.TrimSuffix(version, "+incompatible")
}

// vintageFrom is split out because a `go test` binary carries NO vcs stamps at
// all: every test driving BuildVintage directly can only skip, so a typo in
// either key name would have no witness. This takes the settings as data.
func vintageFrom(settings []debug.BuildSetting) Vintage {
	var v Vintage
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			v.Revision = s.Value
		case "vcs.modified":
			v.Modified = s.Value == "true"
		}
	}
	return v
}

// comparison is the part of GitHub's compare response this needs.
type comparison struct {
	Status   string `json:"status"` // identical, ahead, behind, diverged
	AheadBy  int    `json:"ahead_by"`
	BehindBy int    `json:"behind_by"`
}

// compareWithHead asks how far the default branch has moved past rev.
//
// It deliberately does NOT go through GH: that applies the shared retry policy,
// and five attempts with the shared backoff is five minutes spent on a line of
// advice. A sweep that cannot reach GitHub has worse problems than a stale
// binary, and will say so on its own.
var compareWithHead = func(rev string) (comparison, error) {
	out, msg, err := run([]string{"api", "repos/" + Repository + "/compare/" + rev + "...HEAD"})
	if err != nil {
		return comparison{}, fmt.Errorf("%s", firstLine(msg))
	}
	var c comparison
	if err := json.Unmarshal(out, &c); err != nil {
		return comparison{}, err
	}
	return c, nil
}

// WarnIfStale writes at most one line to w when the binary that is running is
// not the code this repository currently holds.
//
// ⛔ This exists because of a measured failure, not a hunch. On 2026-09-27 a
// guard landed that sets aside a red belonging to a workflow every push skips.
// The installed binaries were never rebuilt, so four days of sweeps ran without
// it and reported the same three repositories as RED default branches each
// time. Nothing was broken, every number was wrong, and no output said so --
// which is the shape of defect this whole repository exists to refuse.
//
// It is silent in the only case that needs no attention: built from the head of
// the default branch, from a clean tree. Every other case says something,
// including "I could not tell", because a check that fails open teaches you to
// trust a silence it did not earn.
func WarnIfStale(w io.Writer) { warnStale(w, BuildVintage(), compareWithHead) }

// warnStale is the whole decision, with both facts passed in.
//
// It is separate from WarnIfStale so a test can drive every branch. Reading the
// real stamps would make the test depend on whether the checkout it runs in
// happens to be clean -- and it is a test for the modified-tree branch that
// would have been the first casualty.
func warnStale(w io.Writer, v Vintage, compare func(string) (comparison, error)) {
	// What the binary was built from, and how to rebuild it from the head.
	ref, name := v.Revision, short(v.Revision)
	rebuild := "GOWORK=off GOBIN=~/.local/bin go install ./cmd/..."
	switch {
	case v.Revision == "" && v.Module != "":
		// ⛔ This branch said "carries no VCS stamp, so whether it is current
		// cannot be told" for every binary installed the way Go installs
		// tools -- `go install …@v0.4.0` -- although its own build info names
		// the version. A note that fires on the canonical install teaches
		// people to read past it, which is the failure this function exists
		// to refuse.
		ref, name = moduleRef(v.Module), v.Module
		rebuild = "GOBIN=~/.local/bin go install github.com/" + Repository + "/cmd/...@latest"
	case v.Revision == "":
		fmt.Fprintln(w, "note: this binary carries no VCS stamp and no module version, so whether it is current cannot be told (built with `go run`, or from outside a checkout).")
		return
	case v.Modified:
		fmt.Fprintf(w, "note: built from a MODIFIED tree at %s -- this reading is not reproducible from any commit.\n", short(v.Revision))
		return
	}
	c, err := compare(ref)
	if err != nil {
		fmt.Fprintf(w, "note: could not tell whether this binary is current (%s).\n", err)
		return
	}
	// ⛔ Switch on the STATUS, not on AheadBy. A diverged revision has a
	// non-zero AheadBy too, and an earlier draft of this reported it as plainly
	// "N commits behind" -- true, and misleading, because it was also two
	// commits nobody had merged. Its test is the one that caught it.
	switch c.Status {
	case "identical":
		return
	case "ahead":
		fmt.Fprintf(w, "note: STALE -- %s is %d commit(s) behind %s. Those commits may change what this pass reports. Rebuild: %s\n",
			name, c.AheadBy, Repository, rebuild)
	case "behind":
		fmt.Fprintf(w, "note: built from %s, which is %d commit(s) AHEAD of %s -- unmerged work, not a stale binary.\n",
			name, c.BehindBy, Repository)
	default:
		fmt.Fprintf(w, "note: built from %s, which has DIVERGED from %s: %d commit(s) it lacks, %d it carries alone (not on the default branch).\n",
			name, Repository, c.AheadBy, c.BehindBy)
	}
}

func short(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	if s == "" {
		return "no error message"
	}
	return s
}
