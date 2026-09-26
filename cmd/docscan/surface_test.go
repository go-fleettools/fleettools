package main

import "testing"

// ⛔ Every case here is a false answer this tool actually gave before the line
// under test existed. They are kept as cases because each one looked right.
func TestAWordInProseIsNotAnEntry(t *testing.T) {
	// go-compressions' landing, near enough: matchlen is named only inside
	// another module's description, and compress only as an English word.
	body := `[[params.repos]]
  name = "lz4"
  result = "delegates to matchlen's SIMD common-prefix kernel; it can compress well"
`
	if landingEntry(body, "lz4") != true {
		t.Error("the entry that IS there was not found")
	}
	for _, mentioned := range []string{"matchlen", "compress"} {
		if landingEntry(body, mentioned) {
			t.Errorf("%q is mentioned in prose and was counted as a listing", mentioned)
		}
	}
}

// ⛔ mkdocs.yml carries `theme:\n  name: material`. A matcher looking for
// `name:` anywhere reported the THEME as a repository the organisation had
// deleted, in two organisations at once.
func TestTheDocsThemeIsNotADeletedRepository(t *testing.T) {
	s := Surface{Name: "docs", Org: "go-fsctl", Body: `theme:
  name: material
nav:
  - Home: index.md
  - loop/: loop.md
`}
	if got := Stale(s, map[string]bool{"loop": true}); len(got) != 0 {
		t.Errorf("a documentation nav produced %v; a page is not a repository", got)
	}
	if !docsEntry(s.Body, "loop") {
		t.Error("a real nav entry was not recognised")
	}
}

// ⛔ These profiles link to their neighbours on purpose -- "pairs with
// go-filesystems". Accepting any owner turned every neighbour into a module
// the organisation had lost.
func TestACrossOrganisationLinkIsNotOurModule(t *testing.T) {
	body := "| [`ldap`](https://github.com/go-authn/ldap) | the protocol half |\n" +
		"<a href=\"https://github.com/go-fileshare\"><img src=\"pairs-with\"></a>\n" +
		"| see also [`fileshare`](https://github.com/go-fileshare/fileshare) |\n"
	entry := profileEntryFor("go-authn")
	if !entry(body, "ldap") {
		t.Error("our own module was not recognised on the profile")
	}
	if entry(body, "fileshare") {
		t.Error("a link to another organisation was counted as ours")
	}
	s := Surface{Name: "profile", Org: "go-authn", Body: body}
	if got := Stale(s, map[string]bool{"ldap": true}); len(got) != 0 {
		t.Errorf("a neighbour's repository was reported as our tombstone: %v", got)
	}
}

// ⛔ go-fileshare declares its five protocols as [[params.protocols]], with the
// same `name` key the repository list uses. Taking every name reported smb,
// nfs, sftp, s3 and webdav as repositories that had vanished.
func TestOnlyTheRepositoryListCounts(t *testing.T) {
	body := `[[params.repos]]
  name = "fileshare"
[[params.protocols]]
  name = "smb"
[[params.protocols]]
  name = "nfs"
`
	s := Surface{Name: "landing", Org: "go-fileshare", Body: body}
	got := Stale(s, map[string]bool{"fileshare": true})
	if len(got) != 0 {
		t.Errorf("entries outside the repository list were read as repositories: %v", got)
	}
}

// And the direction that matters: a module that really is gone is still found.
func TestAModuleThatMovedIsReported(t *testing.T) {
	s := Surface{Name: "landing", Org: "go-compressions", Body: `[[params.repos]]
  name = "lz4"
[[params.repos]]
  name = "matchlen"
`}
	got := Stale(s, map[string]bool{"lz4": true})
	if len(got) != 1 || got[0] != "matchlen" {
		t.Errorf("Stale = %v, want [matchlen]", got)
	}
}

func TestYAMLAndTOMLAreBothAccepted(t *testing.T) {
	// go-filesystems keeps the list in data/fs.yaml, not data/repos.yaml -- a
	// sweep that guessed the filename read nothing and called all thirty
	// modules missing.
	yaml := "modules:\n  - name: ext4\n    kind: fs\n"
	if !landingEntry(yaml, "ext4") {
		t.Error("a YAML entry was not recognised")
	}
	if got := Stale(Surface{Name: "landing", Org: "x", Body: yaml},
		map[string]bool{}); len(got) != 1 || got[0] != "ext4" {
		t.Errorf("Stale on YAML = %v", got)
	}
}

func TestInfraIsNotAModule(t *testing.T) {
	for _, r := range []string{".github", "brand", "docs", "go-authn.github.io"} {
		if !infra("go-authn", r) {
			t.Errorf("%q should not count as a module", r)
		}
	}
	if infra("go-authn", "ldap") {
		t.Error("a real module was excluded")
	}
}
