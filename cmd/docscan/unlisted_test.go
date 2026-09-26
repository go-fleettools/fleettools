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
	if have["nonnil"] || have["a-fork"] {
		t.Errorf("an archived repository or a fork counts as owned: %v", have)
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
