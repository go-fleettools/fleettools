package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree writes a content directory: lang -> relative path -> page.
func tree(t *testing.T, pages map[string]map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for lang, files := range pages {
		for rel, body := range files {
			p := filepath.Join(root, lang, rel)
			os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

const en = "---\ntitle: \"A Kerberos realm\"\nweight: 50\n---\n\n## A realm for the same people\n\nSee {{< relref \"/authnd/check.md#reading-it\" >}}.\n\n```\n$ authnd check\nrealm EXAMPLE.ORG\n```\n\n## Who can get a ticket, and why\n\n```sh\nkinit alice\n```\n"

const fr = "---\ntitle: \"Un royaume Kerberos\"\nweight: 50\n---\n\n## Un royaume pour les mêmes personnes {#a-realm-for-the-same-people}\n\nVoir {{< relref \"/authnd/check.md#reading-it\" >}}.\n\n```\n$ authnd check\nrealm EXAMPLE.ORG\n```\n\n## Qui peut obtenir un ticket, et pourquoi {#who-can-get-a-ticket-and-why}\n\n```sh\nkinit alice\n```\n"

func runOn(t *testing.T, root string) (int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run([]string{root}, &out, &errb)
	return code, out.String() + errb.String()
}

func TestAFaithfulTranslationPasses(t *testing.T) {
	code, out := runOn(t, tree(t, map[string]map[string]string{"en": {"k.md": en}, "fr": {"k.md": fr}}))
	if code != 0 || !strings.Contains(out, "1 pages in en, compared with fr: 2 code blocks, 2 headings; 0 problems") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

// Each of the four things a translation must keep, broken once: each must
// be reported, and the exit status must say so.
func TestEveryDriftIsReported(t *testing.T) {
	for name, c := range map[string]struct{ fr, want string }{
		"a transcript retyped":   {strings.Replace(fr, "realm EXAMPLE.ORG", "royaume EXAMPLE.ORG", 1), `code block 1 differs from the source's: line 3, "royaume EXAMPLE.ORG" instead of "realm EXAMPLE.ORG"`},
		"a block dropped":        {strings.Replace(fr, "```sh\nkinit alice\n```\n", "", 1), "1 code blocks, the source has 2"},
		"an anchor not kept":     {strings.Replace(fr, " {#who-can-get-a-ticket-and-why}", "", 1), "anchor #qui-peut-obtenir-un-ticket-et-pourquoi, the source's is #who-can-get-a-ticket-and-why"},
		"a heading level":        {strings.Replace(fr, "## Qui", "### Qui", 1), "heading level 3, the source's is 2"},
		"a link elsewhere":       {strings.Replace(fr, "check.md#reading-it", "check.md#lire", 1), "relref targets"},
		"another place in menus": {strings.Replace(fr, "weight: 50", "weight: 51", 1), `weight "51", the source says "50"`},
	} {
		t.Run(name, func(t *testing.T) {
			code, out := runOn(t, tree(t, map[string]map[string]string{"en": {"k.md": en}, "fr": {"k.md": c.fr}}))
			if code != 1 || !strings.Contains(out, c.want) {
				t.Errorf("exit %d, want 1 and %q:\n%s", code, c.want, out)
			}
		})
	}
}

func TestMissingAndExtraPages(t *testing.T) {
	code, out := runOn(t, tree(t, map[string]map[string]string{
		"en": {"k.md": en, "only-en.md": en},
		"fr": {"k.md": fr, "only-fr.md": fr},
	}))
	for _, want := range []string{"fr/only-en.md: missing (it is in en)", "fr/only-fr.md: not in en"} {
		if code != 1 || !strings.Contains(out, want) {
			t.Errorf("exit %d, want %q:\n%s", code, want, out)
		}
	}
}

// A check that compared nothing has not passed.
func TestNothingToCompareIsNotAPass(t *testing.T) {
	for name, pages := range map[string]map[string]map[string]string{
		"no base language":  {"fr": {"k.md": fr}},
		"no other language": {"en": {"k.md": en}},
		"no page":           {"en": {"x.txt": "x"}, "fr": {"x.txt": "x"}},
	} {
		if code, out := runOn(t, tree(t, pages)); code != 2 {
			t.Errorf("%s: exit %d, want 2:\n%s", name, code, out)
		}
	}
}

// Anchors as Hugo's "github" autoHeadingIDType makes them. Every case below,
// the repeats included, is what Hugo v0.167.0 printed for a page holding
// exactly these headings; and the tool reads go-authn's documentation (651
// headings, four languages, every anchor copied from Hugo's HTML) with 0
// mismatches.
func TestAnchor(t *testing.T) {
	for in, want := range map[string]string{
		"A realm for the same people":                                            "a-realm-for-the-same-people",
		"`check`, before you restart":                                            "check-before-you-restart",
		"What `-once` reports":                                                   "what--once-reports",
		"The certificate, the keytab, and the one thing":                         "the-certificate-the-keytab-and-the-one-thing",
		"[RFC 8707](https://www.rfc-editor.org/rfc/rfc8707) resource indicators": "rfc-8707-resource-indicators",
		"access_token_groups = false":                                            "access_token_groups--false",
		"Quand le certificat est demandé":                                        "quand-le-certificat-est-demandé",
	} {
		if got := anchor(in); got != want {
			t.Errorf("anchor(%q) = %q, want %q", in, got, want)
		}
	}
	// A repeated heading on one page gets -1, -2.
	p := parse([]byte("## Example\n\n## Example\n\n## Example\n"))
	if ids := []string{p.headings[0].id, p.headings[1].id, p.headings[2].id}; strings.Join(ids, " ") != "example example-1 example-2" {
		t.Errorf("repeated headings: %v", ids)
	}
	// A heading inside a code block is not a heading.
	if p := parse([]byte("```\n## not a heading\n```\n")); len(p.headings) != 0 {
		t.Errorf("a # line in a block was read as a heading")
	}
}

// -comments lets a configuration example's comments be translated, and
// nothing else: a changed value, a changed quoted string that holds a #, or
// any change in a block with no language (a transcript) is still reported.
func TestCommentsMayBeTranslatedWhenAsked(t *testing.T) {
	src := "---\nweight: 1\n---\n\n```hcl\n# the web listener\nurl = \"https://fs.example.org:8443\"   # this web listener\ngroup = \"urn:x#login.example.org\"\n```\n\n```\n$ fileshare check\nthis configuration can be served\n```\n"
	tr := func(old, new string) string { return strings.Replace(src, old, new, 1) }
	for name, c := range map[string]struct {
		fr   string
		want int // exit status with -comments
	}{
		"comments translated":          {tr("# this web listener", "# ce port d'écoute web"), 0},
		"a whole-line comment":         {tr("# the web listener", "# le port web"), 0},
		"a value changed":              {tr(":8443", ":9443"), 1},
		"a # inside quotes changed":    {tr("#login.example.org", "#autre.example.org"), 1},
		"a transcript line translated": {tr("this configuration can be served", "cette configuration peut être servie"), 1},
	} {
		t.Run(name, func(t *testing.T) {
			root := tree(t, map[string]map[string]string{"en": {"a.md": src}, "fr": {"a.md": c.fr}})
			var out, errb bytes.Buffer
			if code := run([]string{"-comments", root}, &out, &errb); code != c.want {
				t.Errorf("-comments: exit %d, want %d:\n%s%s", code, c.want, out.String(), errb.String())
			}
			// Without the flag every difference counts.
			if c.fr != src {
				if code := run([]string{root}, &out, &errb); code != 1 {
					t.Errorf("without -comments: exit %d, want 1", code)
				}
			}
		})
	}
}
