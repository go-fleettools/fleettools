package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeUnlisted(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, UnlistedFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { delete(unlisted, "o") })
	return root
}

func TestAReasonIsRead(t *testing.T) {
	root := writeUnlisted(t, "# a comment\n\nis  a placeholder: a README with a banner and a heading, no code\n")
	if err := readUnlisted("o", root); err != nil {
		t.Fatal(err)
	}
	why, ok := unlisted["o"]["is"]
	if !ok {
		t.Fatal("the entry was not read")
	}
	if want := "a placeholder: a README with a banner and a heading, no code"; why != want {
		t.Errorf("reason = %q, want %q", why, want)
	}
}

// TestANameWithoutAReasonIsRefused. An allowance that can be written silently
// is the check switched off one line at a time.
func TestANameWithoutAReasonIsRefused(t *testing.T) {
	for _, body := range []string{"is\n", "is   \n", "ok fine\nis\n"} {
		root := writeUnlisted(t, body)
		err := readUnlisted("o", root)
		if err == nil {
			t.Errorf("%q was accepted", body)
			continue
		}
		if _, silent := unlisted["o"]["is"]; silent {
			t.Errorf("%q registered the name anyway", body)
		}
	}
}

// TestNoFileAllowsNothing — absence is not permission.
func TestNoFileAllowsNothing(t *testing.T) {
	if err := readUnlisted("o", t.TempDir()); err != nil {
		t.Fatalf("a missing file is not an error: %v", err)
	}
	if len(unlisted["o"]) != 0 {
		t.Errorf("a missing file allowed %v", unlisted["o"])
	}
}

// TestAnUnreadableFileIsAnError, because a permission that cannot be read must
// not read as no permissions asked for — that is the quiet direction again.
func TestAnUnreadableFileIsAnError(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, UnlistedFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := readUnlisted("o", root); err == nil {
		t.Error("a directory in place of the file was accepted")
	}
}

func TestOrgOf(t *testing.T) {
	for in, want := range map[string]string{
		"go-composites/go-composites.github.io": "go-composites",
		"nolash":                                "nolash",
		"":                                      "",
	} {
		if got := orgOf(in); got != want {
			t.Errorf("orgOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestArchivedAndGoneAreDifferentLines. Told a surface names something the
// organisation "no longer has", a reader deletes the row. Told it is archived,
// they mark it — and that is the honest answer, because the repository is still
// there and the link still resolves.
func TestArchivedAndGoneAreDifferentLines(t *testing.T) {
	var b strings.Builder
	report([]finding{{
		org:      "o",
		modules:  3,
		surfaces: []string{"landing"},
		unlisted: map[string][]string{},
		quiet:    map[string]string{},
		stale:    map[string][]string{"landing": {"frozen", "deleted"}},
		archived: map[string]bool{"frozen": true},
	}}, &b)
	out := b.String()
	if !strings.Contains(out, "no longer has: deleted") {
		t.Errorf("a deleted repository was not reported as gone:\n%s", out)
	}
	if !strings.Contains(out, "ARCHIVED: frozen") {
		t.Errorf("an archived repository was not reported as archived:\n%s", out)
	}
	if strings.Contains(out, "no longer has: frozen") || strings.Contains(out, "frozen deleted") {
		t.Errorf("archived and gone were put on one line:\n%s", out)
	}
}

// TestOnlyGoneKeepsOneLine — the other direction: with nothing archived, the
// archived line must not appear at all.
func TestOnlyGoneKeepsOneLine(t *testing.T) {
	var b strings.Builder
	report([]finding{{
		org: "o", modules: 2, surfaces: []string{"landing"},
		unlisted: map[string][]string{}, quiet: map[string]string{},
		stale:    map[string][]string{"landing": {"deleted"}},
		archived: map[string]bool{},
	}}, &b)
	if out := b.String(); strings.Contains(out, "ARCHIVED") {
		t.Errorf("an archived line with nothing archived:\n%s", out)
	}
}

// TestAQuietOrganisationIsNotDrift. A deliberate omission is printed and does
// not count, or the file would be a way to turn the check red for using it.
func TestAQuietOrganisationIsNotDrift(t *testing.T) {
	var b strings.Builder
	report([]finding{{
		org: "o", modules: 2, surfaces: []string{"landing"},
		unlisted: map[string][]string{}, stale: map[string][]string{},
		archived: map[string]bool{},
		quiet:    map[string]string{"is": "a placeholder"},
	}}, &b)
	out := b.String()
	if !strings.Contains(out, "unlisted on purpose: a placeholder") {
		t.Errorf("the deliberate omission was not printed:\n%s", out)
	}
	if !strings.Contains(out, "0 organisations with drift") {
		t.Errorf("a deliberate omission counted as drift:\n%s", out)
	}
}

// TestClassifySortsTheOrganisation covers the half that a report test cannot
// reach. Removing the two lines that fill f.archived used to leave the whole
// suite green.
func TestClassifySortsTheOrganisation(t *testing.T) {
	var f finding
	f.archived = map[string]bool{}
	have, modules := classify("go-composites", []ghRepo{
		{Name: "composites", Disk: 900},
		{Name: "nonnil", Disk: 40, Archived: true},
		{Name: "a-fork", Disk: 40, Fork: true},
		{Name: "brand", Disk: 10},                   // infrastructure
		{Name: "go-composites.github.io", Disk: 80}, // infrastructure
		{Name: "shell", Disk: 0},                    // empty
	}, &f)

	if !f.archived["nonnil"] {
		t.Error("an archived repository was not recorded as archived")
	}
	if f.archived["a-fork"] {
		t.Error("a fork was recorded as archived")
	}
	// ⛔ ARCHIVED stays out of `have`, and that is load-bearing: it is what
	// routes the name to the archived line instead of "the org no longer has".
	if have["nonnil"] {
		t.Errorf("an archived repository counts as owned, so it will be reported as GONE: %v", have)
	}
	// ⛔ A FORK, on the other hand, IS owned — and this assertion used to say the
	// opposite, bundled with the archived one. Excluding it routed a surface that
	// lists an owned fork to "the org no longer has", which is false: go-images
	// got that sentence about jpeg2000, a fork pushed that same morning. There is
	// no fork line to route to, so the only honest answer is that the name is
	// owned and nothing is wrong.
	if !have["a-fork"] {
		t.Errorf("a fork is owned by the organisation and must count as owned: %v", have)
	}
	if f.archived["a-fork"] {
		t.Error("a fork was recorded as archived")
	}
	if !have["brand"] || !have["composites"] {
		t.Errorf("an owned name is missing: %v", have)
	}
	if got := strings.Join(modules, " "); got != "composites" {
		t.Errorf("modules = %q, want %q — infrastructure and empty repositories are not modules", got, "composites")
	}
	if got := strings.Join(f.empty, " "); got != "shell" {
		t.Errorf("empty = %q, want %q", got, "shell")
	}
}

// TestParseUnlistedIsWhatTheApiPathUses. The fleet-wide report has no checkout,
// so it parses the file's bytes rather than a path — and go-composites/is came
// back NOWHERE minutes after the allowance was merged, because only the tree
// path could see it.
func TestParseUnlistedIsWhatTheApiPathUses(t *testing.T) {
	got, err := parseUnlisted("# why\n\nis  a placeholder, no code\nother  moved to another org\n")
	if err != nil {
		t.Fatal(err)
	}
	if got["is"] != "a placeholder, no code" || got["other"] != "moved to another org" {
		t.Errorf("parsed %v", got)
	}
	// The empty body is what file() returns for a repository with no such
	// file, and it must mean "nothing allowed", not an error.
	empty, err := parseUnlisted("")
	if err != nil || len(empty) != 0 {
		t.Errorf("empty body: %v %v", empty, err)
	}
}

// TestAMalformedAllowanceIsDriftNotSilence.
func TestAMalformedAllowanceIsDriftNotSilence(t *testing.T) {
	var b strings.Builder
	code := report([]finding{{
		org: "o", modules: 1, surfaces: []string{"landing"},
		unlisted: map[string][]string{}, stale: map[string][]string{},
		archived: map[string]bool{}, quiet: map[string]string{},
		badAllow: ".docs-unlisted:3: \"is\" says which repository but not why",
	}}, &b)
	out := b.String()
	if !strings.Contains(out, "says which repository but not why") {
		t.Errorf("the malformed file was not reported:\n%s", out)
	}
	if !strings.Contains(out, "1 organisations with drift") {
		t.Errorf("a malformed allowance did not count as drift:\n%s", out)
	}
	_ = code
}

// TestTheCountAndTheGateCountAlike. They were two expressions and disagreed
// twice: an empty repository counted in the headline and not in the gate, and a
// malformed allowance counted in the headline and not in the gate — the second
// being the dangerous way round, since the file that grants exemptions could be
// unparseable and the check still pass.
func TestTheCountAndTheGateCountAlike(t *testing.T) {
	base := func() finding {
		return finding{
			org: "o", modules: 2, surfaces: []string{"landing"},
			unlisted: map[string][]string{}, stale: map[string][]string{},
			archived: map[string]bool{}, quiet: map[string]string{},
		}
	}
	empty := base()
	empty.empty = []string{"shell"}
	if empty.drifted() {
		t.Error("an empty repository counts as drift, though the report calls it not a module to advertise")
	}
	quiet := base()
	quiet.quiet = map[string]string{"is": "a placeholder"}
	if quiet.drifted() {
		t.Error("a deliberate omission counts as drift")
	}
	bad := base()
	bad.badAllow = ".docs-unlisted:1: no reason"
	if !bad.drifted() {
		t.Error("a malformed allowance does not fail the gate — the file granting exemptions could be unparseable and the check pass")
	}
	for name, f := range map[string]finding{
		"unlisted": func() finding { g := base(); g.unlisted = map[string][]string{"x": nil}; return g }(),
		"stale":    func() finding { g := base(); g.stale = map[string][]string{"landing": {"gone"}}; return g }(),
	} {
		if !f.drifted() {
			t.Errorf("%s is not drift", name)
		}
	}
	// And the headline must agree with drifted() on the same finding.
	var b strings.Builder
	report([]finding{empty}, &b)
	if !strings.Contains(b.String(), "0 organisations with drift") {
		t.Errorf("the headline disagrees with the gate:\n%s", b.String())
	}
}

// TestAnOrganisationThatAdvertisesNothingIsPrinted.
//
// ⛔ 24 organisations had no profile README and no landing page, and every one
// of them sat under a headline reading "0 organisations with drift" —
// openstack-terraform-modules with eight repositories, go-sicp with seven.
// With no surface there is nothing to compare, so it is not drift; saying
// nothing at all made an organisation that advertises nothing look exactly
// like one that advertises everything correctly.
func TestAnOrganisationThatAdvertisesNothingIsPrinted(t *testing.T) {
	var b strings.Builder
	code := report([]finding{{
		org: "go-sicp", modules: 7, silent: true,
		unlisted: map[string][]string{}, stale: map[string][]string{},
		archived: map[string]bool{}, quiet: map[string]string{},
	}}, &b)
	out := b.String()
	if !strings.Contains(out, "advertise NOTHING") {
		t.Errorf("a silent organisation was not reported:\n%s", out)
	}
	if !strings.Contains(out, "go-sicp (7)") {
		t.Errorf("the count of invisible repositories is the point:\n%s", out)
	}
	// It is NOT drift, and it must not become an exit status: there is no
	// defect to fix in a pull request, only a page nobody has written.
	if !strings.Contains(out, "0 organisations with drift") {
		t.Errorf("absence counted as drift:\n%s", out)
	}
	if code != 0 {
		t.Errorf("exit status = %d, want 0", code)
	}
}

// TestAnEmptyOrganisationIsNotWorthALine is the control: silent is only set
// when there is something to be invisible. Eight of the 24 held no
// repositories at all, and a line about them is noise.
func TestAnEmptyOrganisationIsNotWorthALine(t *testing.T) {
	var b strings.Builder
	report([]finding{{
		org: "go-ruby-tiktok", modules: 0, silent: false,
		unlisted: map[string][]string{}, stale: map[string][]string{},
		archived: map[string]bool{}, quiet: map[string]string{},
	}}, &b)
	if out := b.String(); strings.Contains(out, "advertise NOTHING") {
		t.Errorf("an organisation with no repositories needs no line:\n%s", out)
	}
}

// TestSilentOrgNeedsBothHalves covers the decision scan makes and a report
// test cannot reach.
func TestSilentOrgNeedsBothHalves(t *testing.T) {
	if !silentOrg(nil, 7) {
		t.Error("no surface and seven repositories is the case this exists for")
	}
	if silentOrg(nil, 0) {
		t.Error("an empty organisation advertises nothing and loses nothing")
	}
	if silentOrg([]string{"profile"}, 7) {
		t.Error("an organisation with a surface is judged by comparison, not by this")
	}
}

// ⛔ Le contrôle ATTEIGNABLE que ce fichier exige de lui-même: supprimer les deux
// lignes qui font entrer un fork dans `have` doit faire échouer quelque chose.
// C'est ce test. Sans lui, la correction se ferait supprimer par accident — ce qui
// est exactement ce qui est arrivé une fois aux deux lignes remplissant f.archived.
func TestASurfaceListingAnOwnedForkIsNotStale(t *testing.T) {
	var f finding
	f.archived = map[string]bool{}
	have, modules := classify("go-images", []ghRepo{
		{Name: "images", Disk: 900},
		{Name: "jpeg2000", Disk: 300, Fork: true},
	}, &f)

	// Le fork n'est pas un module: une surface ne lui doit pas de ligne.
	if got := strings.Join(modules, " "); got != "images" {
		t.Errorf("modules = %q, want %q — a fork is owned but not owed a row", got, "images")
	}
	// Mais il est possédé, donc une surface qui le nomme n'est pas périmée.
	// La surface est un vrai profil, pas une liste de noms: c'est Entry qui
	// décide ce que « nommer » veut dire, et on ne la contourne pas.
	profile := Surface{Name: "profile", Org: "go-images", Body: `
| [` + "`images`" + `](https://github.com/go-images/images) | the library |
| [` + "`jpeg2000`" + `](https://github.com/go-images/jpeg2000) | a fork, pushed this morning |
`}
	if gone := Stale(profile, have); len(gone) > 0 {
		t.Errorf("a surface listing an owned fork was called stale: %v\n"+
			"That is the sentence go-images got about jpeg2000, a fork pushed that morning.", gone)
	}
	// Et le contrôle dans l'autre sens: un nom que l'organisation n'a vraiment
	// plus doit toujours être rapporté, sinon ce test ne prouve rien.
	away := Surface{Name: "profile", Org: "go-images", Body: `
| [` + "`images`" + `](https://github.com/go-images/images) | the library |
| [` + "`renamed-away`" + `](https://github.com/go-images/renamed-away) | gone |
`}
	if gone := Stale(away, have); len(gone) != 1 || gone[0] != "renamed-away" {
		t.Errorf("a name the organisation really does not have was not reported: %v", gone)
	}
}
