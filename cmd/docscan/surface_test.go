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
	for _, r := range []string{".github", "brand", "docs", "go-authn.github.io", "renovate-runner"} {
		if !infra("go-authn", r) {
			t.Errorf("%q should not count as a module", r)
		}
	}
	if infra("go-authn", "ldap") {
		t.Error("a real module was excluded")
	}
}

// TestAnEntryThatNamesAnotherOrgIsNotAClaimAboutThisOne. go-composites' landing
// carries nonnil and respondto with org = "go-vet-analyzers" and the words "Now
// maintained in the go-vet-analyzers org" — which is what a moved module should
// look like — and both were reported as names this organisation had lost.
func TestAnEntryThatNamesAnotherOrgIsNotAClaimAboutThisOne(t *testing.T) {
	body := `[[params.repos]]
  name = "composites"
  result = "the library"
[[params.repos]]
  name = "nonnil"
  org = "go-vet-analyzers"
  result = "Now maintained in the go-vet-analyzers org."
[[params.repos]]
  name = "gone"
  result = "this one really is missing"
`
	have := map[string]bool{"composites": true}
	got := listedRepos(body, "go-composites", have)
	if len(got) != 1 || got[0] != "gone" {
		t.Errorf("listedRepos = %v, want [gone] — nonnil belongs to another organisation", got)
	}
}

// TestTheOrgKeyBelongsToItsOwnEntry — the other direction, which a flat scan
// gets wrong: an org key must not carry over to the entry after it.
func TestTheOrgKeyBelongsToItsOwnEntry(t *testing.T) {
	body := `[[params.repos]]
  name = "moved"
  org = "elsewhere"
[[params.repos]]
  name = "gone"
`
	got := listedRepos(body, "mine", map[string]bool{})
	if len(got) != 1 || got[0] != "gone" {
		t.Errorf("listedRepos = %v, want [gone] — the org key leaked to the next entry", got)
	}
}

// TestAnEntryNamingThisOrgExplicitlyStillCounts. Some pages set org on every
// entry, including their own.
func TestAnEntryNamingThisOrgExplicitlyStillCounts(t *testing.T) {
	body := "[[params.repos]]\n  name = \"gone\"\n  org = \"mine\"\n"
	got := listedRepos(body, "mine", map[string]bool{})
	if len(got) != 1 || got[0] != "gone" {
		t.Errorf("listedRepos = %v, want [gone]", got)
	}
}

// TestTwoEntriesInOneYamlList. The YAML shape has no per-entry header, so a
// second `- name:` is what ends the first entry.
func TestTwoEntriesInOneYamlList(t *testing.T) {
	body := "modules:\n  - name: kept\n    kind: driver\n  - name: moved\n    org: somewhere-else\n"
	got := listedRepos(body, "mine", map[string]bool{})
	if len(got) != 1 || got[0] != "kept" {
		t.Errorf("listedRepos = %v, want [kept]", got)
	}
}

// TestANavLineNamesItsModuleInTheTitle. go-reddit's documentation reads
// `- The client (reddit): client.md`: the page is client.md, so matching only
// the filename called the organisation's one documented module undocumented.
func TestANavLineNamesItsModuleInTheTitle(t *testing.T) {
	nav := `nav:
  - Home: index.md
  - The client (reddit): client.md
  - Authentication (OAuth): oauth.md
  - The reader app: reader.md
  - Contributing: contributing.md
`
	if !docsEntry(nav, "reddit") {
		t.Error("the nav names reddit in a title and was not read")
	}
	if !docsEntry(nav, "reader") {
		t.Error("the filename spelling stopped working")
	}
	// The other direction: a module this organisation does not document must
	// not be conjured out of a neighbouring line.
	// "client" is NOT in this list: the nav points at client.md, so a module
	// of that name really would be documented by that line.
	for _, absent := range []string{"read", "oauth2", "home", "The client"} {
		if docsEntry(nav, absent) {
			t.Errorf("the nav was read as naming %q", absent)
		}
	}
}

// TestProseInAPageIsStillNotAnEntry — the rule the whole matcher exists for.
func TestProseInAPageIsStillNotAnEntry(t *testing.T) {
	if docsEntry("A paragraph mentioning reddit, and reddit again.\n", "reddit") {
		t.Error("prose was read as a nav entry")
	}
	if docsEntry("  - see also: the reddit client\n", "reddit") {
		t.Error("a nav line pointing at no page was read as an entry")
	}
	// The name has to be parenthesised, not merely present.
	if docsEntry("  - The reddit client: client.md\n", "reddit") {
		t.Error("an unparenthesised name in a title was read as an entry")
	}
}
