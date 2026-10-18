// Copyright (c) 2026, the go-fleettools authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ⛔ EVERY FIXTURE HERE IS A SHAPE THAT OCCURS IN THE FLEET, not an invented
// one: the census over 85 sites and 575 pages counted 209 admonitions in nine
// kinds, 10 collapsibles, 24 content tabs, 84 attribute lists and 1 252 links
// to a .md file. A converter tested on shapes nobody writes converts nothing.
func TestTheConstructsAMkdocsPageHolds(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		want string
	}{
		{
			"a note with a title",
			"!!! note \"Built is not tested\"\n    It read \"built and tested\".\n",
			"{{< callout type=\"info\" >}}\n**Built is not tested**\n\nIt read \"built and tested\".\n{{< /callout >}}\n",
		},
		{
			"a note with no title",
			"!!! note\n    One line.\n",
			"{{< callout type=\"info\" >}}\nOne line.\n{{< /callout >}}\n",
		},
		{
			"a warning becomes a warning and danger becomes an error",
			"!!! warning\n    careful\n\n!!! danger\n    no\n",
			"{{< callout type=\"warning\" >}}\ncareful\n{{< /callout >}}\n\n{{< callout type=\"error\" >}}\nno\n{{< /callout >}}\n",
		},
		{
			// 41 of the fleet's 209 are "success", which Hextra has no colour
			// for: info rather than a shortcode nobody defines.
			"success has no colour of its own",
			"!!! success\n    done\n",
			"{{< callout type=\"info\" >}}\ndone\n{{< /callout >}}\n",
		},
		{
			"a kind nothing recognises is still a box",
			"!!! lorem\n    text\n",
			"{{< callout type=\"info\" >}}\ntext\n{{< /callout >}}\n",
		},
		{
			// ⛔ Several paragraphs: a reader that stopped at the first blank
			// line would leave the rest of the box outside it.
			"an admonition of two paragraphs",
			"!!! note\n    first\n\n    second\n\nafter\n",
			"{{< callout type=\"info\" >}}\nfirst\n\nsecond\n{{< /callout >}}\n\nafter\n",
		},
		{
			"a collapsible becomes details",
			"??? warning \"Why\"\n    because\n",
			"{{% details title=\"Why\" %}}\n\nbecause\n\n{{% /details %}}\n",
		},
		{
			"an open collapsible says so",
			"???+ note \"Open\"\n    text\n",
			"{{% details title=\"Open\" closed=\"false\" %}}\n\ntext\n\n{{% /details %}}\n",
		},
		{
			"a collapsible with no title is named for its kind",
			"??? question\n    text\n",
			"{{% details title=\"Question\" %}}\n\ntext\n\n{{% /details %}}\n",
		},
		{
			"two content tabs become one tab set",
			"=== \"Go\"\n    go build\n\n=== \"Shell\"\n    make\n",
			"{{< tabs >}}\n\n{{% tab name=\"Go\" %}}\n\ngo build\n\n{{% /tab %}}\n\n{{% tab name=\"Shell\" %}}\n\nmake\n\n{{% /tab %}}\n\n{{< /tabs >}}\n",
		},
		{
			"a link to another page resolves at build time",
			"see [the API](api.md) and [a section](guide.md#part-two)\n",
			"see [the API]({{< relref \"api.md\" >}}) and [a section]({{< relref \"guide.md#part-two\" >}})\n",
		},
		{
			"a link to the index becomes the section page",
			"back to [home](index.md)\n",
			"back to [home]({{< relref \"_index.md\" >}})\n",
		},
		{
			"an external link is left alone",
			"[upstream](https://example.com/a.md)\n",
			"[upstream](https://example.com/a.md)\n",
		},
		{
			"an attribute list is dropped",
			"A paragraph.\n{: .note }\n",
			"A paragraph.\n\n",
		},
		{
			// ⛔ The fence is what makes this safe: a shell example holding
			// MkDocs syntax is an example.
			"nothing inside a fence is converted",
			"```\n!!! note\n    not an admonition\n=== \"not a tab\"\n[not a link](x.md)\n```\n",
			"```\n!!! note\n    not an admonition\n=== \"not a tab\"\n[not a link](x.md)\n```\n",
		},
		{
			"a tilde fence is not closed by a backtick one",
			"~~~\n!!! note\n```\nstill inside\n~~~\n",
			"~~~\n!!! note\n```\nstill inside\n~~~\n",
		},
		{
			"ordinary prose is untouched, byte for byte",
			"# Heading\n\nA paragraph with **bold**, `code` and a | table | cell |.\n",
			"# Heading\n\nA paragraph with **bold**, `code` and a | table | cell |.\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, _ := convertBody(c.in)
			if got != c.want {
				t.Errorf("\n got %q\nwant %q", got, c.want)
			}
		})
	}
}

// ⛔ THE COUNTS ARE THE REPORT, so they are asserted: a migration of 85 sites
// is reviewed through "what did it carry over", and a counter that drifts makes
// that review worthless.
func TestWhatTheConversionReports(t *testing.T) {
	in := strings.Join([]string{
		`!!! note "A"`,
		"    text with [a link](api.md)",
		"",
		`??? tip "B"`,
		"    more",
		"",
		`=== "One"`,
		"    first",
		"",
		`=== "Two"`,
		"    second",
		"",
		"A paragraph.",
		"{: .cls }",
		"```mermaid",
		"graph TD;",
		"```",
		"",
	}, "\n")
	_, s := convertBody(in)
	if s.Callouts != 1 || s.Details != 1 || s.Tabs != 1 || s.Links != 1 || s.AttrLists != 1 || s.Mermaid != 1 {
		t.Errorf("%+v", s)
	}
	if got := s.String(); !strings.Contains(got, "1 callouts") || !strings.Contains(got, "1 mermaid") {
		t.Errorf("the report reads %q", got)
	}
}

func TestReadingAnMkdocsConfig(t *testing.T) {
	cfg := `site_name: go-asmgen docs
site_description: "Assembly generation, in Go."
site_url: https://go-asmgen.github.io/docs/
repo_url: https://github.com/go-asmgen
edit_uri: ""

theme:
  name: material
  palette:
    - media: "(prefers-color-scheme)"

extra:
  version:
    provider: mike

nav:
  - Overview: index.md
  - Getting started: getting-started.md
  - Reference:
    - Instructions: ref/instructions.md
    - Registers: ref/registers.md
  - API: api.md

markdown_extensions:
  - admonition
`
	s := readMkdocs(cfg)
	if s.Name != "go-asmgen docs" || s.Description != "Assembly generation, in Go." {
		t.Errorf("%+v", s)
	}
	if s.URL != "https://go-asmgen.github.io/docs/" || s.RepoURL != "https://github.com/go-asmgen" {
		t.Errorf("%+v", s)
	}
	if !s.Mike {
		t.Error("the site was versioned with mike and that was not read")
	}
	if len(s.Nav) != 4 {
		t.Fatalf("%d top-level nav entries: %+v", len(s.Nav), s.Nav)
	}
	// ⛔ The nesting has to survive, because it IS the sidebar: a flat nav
	// would put "Instructions" and "Registers" beside "API" instead of under
	// "Reference".
	ref := s.Nav[2]
	if ref.Title != "Reference" || ref.File != "" || len(ref.Children) != 2 {
		t.Errorf("the nested section came back as %+v", ref)
	}
	if ref.Children[1].Title != "Registers" || ref.Children[1].File != "ref/registers.md" {
		t.Errorf("%+v", ref.Children[1])
	}
}

// ⛔ A SITE WITH NO NAV CANNOT BE ORDERED, and MkDocs' own fallback is
// alphabetical — which puts chapter 10 before chapter 2. The converter refuses
// rather than inventing an order.
func TestANavThatIsNotThere(t *testing.T) {
	s := readMkdocs("site_name: x\ntheme:\n  name: material\n")
	if len(s.Nav) != 0 {
		t.Errorf("a nav came out of a config with none: %+v", s.Nav)
	}
}

func TestTheGeneratedConfigSaysWhatItMust(t *testing.T) {
	s := readMkdocs("site_name: x docs\nsite_url: https://x.github.io/docs/\nrepo_url: https://github.com/x\n")
	cfg := hugoConfig(s, "v0.3.1")
	for _, want := range []string{
		`baseURL: "https://x.github.io/docs/v0.3.1/"`,
		"theme: hextra",
		"contentDir: content/en",
		"enableGitInfo: true",
		`version: "v0.3.1"`,
		"type: docs",
		`base: "https://github.com/x/edit/main/content/en"`,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("the config does not say %q:\n%s", want, cfg)
		}
	}
	// A site with no repo_url gets no edit link rather than a broken one.
	if strings.Contains(hugoConfig(readMkdocs("site_name: y\n"), "main"), "editURL") {
		t.Error("an edit link was written for a site with no repository")
	}
}

func TestTheWorkflowPublishesOneVersionAndFailsOnAWarning(t *testing.T) {
	w := workflow(readMkdocs("site_name: x\nsite_url: https://x.github.io/docs/\n"), "main")
	for _, want := range []string{
		"--panicOnWarning",
		`DOCS_URL: "https://x.github.io/docs"`,
		"submodules: recursive",
		`rm -rf "docs/${version}"`,
		"group: gh-pages-deploy",
		"cancel-in-progress: false",
		"lycheeverse/lychee-action@v2",
	} {
		if !strings.Contains(w, want) {
			t.Errorf("the workflow does not say %q", want)
		}
	}
	// ⛔ A pull request must not publish. The deploy job's condition is the
	// only thing standing between a fork's PR and gh-pages.
	if !strings.Contains(w, `if: github.event_name != 'pull_request' && github.ref == 'refs/heads/main'`) {
		t.Error("the deploy job would run for a pull request")
	}
}

// ⛔ A TITLE MAY CONTAIN A COLON, and no fixture of mine had one: go-asmgen's
// nav holds `- "Second architecture: riscv64": asmgen/riscv64.md` five times.
// Cutting at the first colon gave the file `riscv64": asmgen/riscv64.md`, so
// the converter reported five pages missing that were sitting in docs/ — a
// tool reporting a defect in the thing it converts.
func TestANavTitleThatContainsAColon(t *testing.T) {
	for _, c := range []struct {
		in          string
		title, file string
		hasTarget   bool
	}{
		{`"Second architecture: riscv64": asmgen/riscv64.md`, "Second architecture: riscv64", "asmgen/riscv64.md", true},
		{`'Single quoted: too': a.md`, "Single quoted: too", "a.md", true},
		{`Plain title: a.md`, "Plain title", "a.md", true},
		{`Section:`, "Section", "", true},
		{`a-page.md`, "a-page.md", "", false},
		{`"Quoted with no file"`, "Quoted with no file", "", false},
	} {
		t.Run(c.in, func(t *testing.T) {
			title, file, hasTarget := splitEntry(c.in)
			if title != c.title || file != c.file || hasTarget != c.hasTarget {
				t.Errorf("got %q, %q, %v; want %q, %q, %v", title, file, hasTarget, c.title, c.file, c.hasTarget)
			}
		})
	}
}

// ⛔ A PAGE KEEPS ITS OWN PATH, and a section's directory is the one its pages
// are already in. Building a directory from the section's TITLE gave
// content/en/asmgen/asmgen/design.md on the first real site — and every
// converted link is relative, so an invented path breaks all of them.
func TestWhereTheConvertedPagesGo(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		t.Helper()
		path := filepath.Join(dir, "docs", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"index.md", "contributing.md", "asmgen/index.md", "asmgen/simd.md", "guide/one.md", "ref/two.md"} {
		write(name)
	}
	nav := readMkdocs(`nav:
  - Home: index.md
  - asmgen:
      - Overview: asmgen/index.md
      - SIMD: asmgen/simd.md
  - Mixed:
      - One: guide/one.md
      - Two: ref/two.md
  - Contributing: contributing.md
`).Nav
	pages, missing := collect(nav, dir)
	if len(missing) != 0 {
		t.Fatalf("reported missing: %v", missing)
	}
	got := map[string]int{}
	for _, p := range pages {
		got[p.dest] = p.weight
	}
	for _, want := range []string{
		filepath.Join("content", "en", "_index.md"),
		// The section's own page IS the child index.md, renamed — not a second
		// file under a directory named after the title.
		filepath.Join("content", "en", "asmgen", "_index.md"),
		filepath.Join("content", "en", "asmgen", "simd.md"),
		filepath.Join("content", "en", "guide", "one.md"),
		filepath.Join("content", "en", "ref", "two.md"),
		filepath.Join("content", "en", "contributing.md"),
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("%s was not written; got %v", want, got)
		}
	}
	if _, doubled := got[filepath.Join("content", "en", "asmgen", "asmgen", "simd.md")]; doubled {
		t.Error("the section's title was turned into a directory on top of the one the pages are in")
	}
	// ⛔ A section whose pages are in DIFFERENT directories cannot become one
	// without moving them, so no section page is invented for it.
	if _, invented := got[filepath.Join("content", "en", "mixed", "_index.md")]; invented {
		t.Error("a directory was invented for a section whose pages do not share one")
	}
	// The nav's order is the weight, ten apart and increasing.
	if got[filepath.Join("content", "en", "_index.md")] >= got[filepath.Join("content", "en", "contributing.md")] {
		t.Error("the nav order did not become the weight")
	}
}

// A section whose pages share a directory but have no index.md of their own
// gets one written beside them, so the heading survives.
func TestASectionWithNoIndexPageOfItsOwn(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"ref/one.md", "ref/two.md"} {
		path := filepath.Join(dir, "docs", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pages, _ := collect(readMkdocs("nav:\n  - Reference:\n      - One: ref/one.md\n      - Two: ref/two.md\n").Nav, dir)
	var section *page
	for i := range pages {
		if pages[i].dest == filepath.Join("content", "en", "ref", "_index.md") {
			section = &pages[i]
		}
	}
	if section == nil {
		t.Fatalf("no section page was written: %+v", pages)
	}
	if section.source != "" || section.nav != "Reference" {
		t.Errorf("the section page is %+v", *section)
	}
}

// ⛔ `cascade.target`, NOT `cascade._target`. The underscored spelling was
// deprecated in Hugo 0.156, and the fleet's older Hextra sites still carry it:
// copying one of them is how a freshly converted site arrives with a
// deprecation. The pilot build caught it because it runs with
// --panicOnWarning, and this is what stops it coming back.
func TestTheConfigDoesNotCarryADeprecatedSpelling(t *testing.T) {
	cfg := hugoConfig(readMkdocs("site_name: x\nsite_url: https://x.github.io/docs/\n"), "main")
	// ⛔ The SETTINGS, not the comments. The first version of this test read
	// the whole file and failed on the comment that explains the rule — a
	// control that cannot tell a claim from its explanation.
	for i, line := range strings.Split(cfg, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		// Each of these was refused by a pilot build, and each came from
		// copying an older site in this fleet.
		for key, since := range map[string]string{
			"_target":      "0.156, use cascade.target",
			"languageCode": "0.158, use locale or the languages block",
		} {
			if strings.Contains(line, key) {
				t.Errorf("line %d uses %s, deprecated in Hugo %s: %q", i+1, key, since, line)
			}
		}
	}
	if !strings.Contains(cfg, "- target:") {
		t.Errorf("the config has no cascade target:\n%s", cfg)
	}
}

// ⛔ 46 of the 84 sites have a README and 38 have NONE. Of the 46, 43 tell the
// reader to run `pip install -r requirements.txt`: a migration that left those
// standing would publish instructions for a site that no longer exists.
func TestTheReadmeOfASiteThatHasOne(t *testing.T) {
	current := `<p align="center"><img src="banner.png" alt="x"></p>

# go-asmgen/docs

Versioned documentation for [go-asmgen](https://github.com/go-asmgen), built
with [MkDocs Material](https://squidfunk.github.io/mkdocs-material/) and
versioned with [mike](https://github.com/jimporter/mike). Published to the
` + "`gh-pages`" + ` branch and served at <https://go-asmgen.github.io/docs/>.

The organization landing page links here.

## Local preview

` + "```bash" + `
python -m venv .venv && . .venv/bin/activate
pip install -r requirements.txt
mkdocs serve
` + "```" + `

## Releasing a new docs version

` + "```bash" + `
mike deploy --push --update-aliases <version> latest
` + "```" + `
`
	got, changed := updateREADME(current, "go-asmgen/docs", "https://go-asmgen.github.io/docs/")
	if !changed {
		t.Fatal("nothing was recognised in the fleet's own boilerplate")
	}
	for _, gone := range []string{"MkDocs Material", "pip install", "mkdocs serve", "mike deploy", "venv"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q survived:\n%s", gone, got)
		}
	}
	for _, want := range []string{"hugo server", "git submodule update", "params.docs.version", "panicOnWarning", "Hextra fork"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is missing:\n%s", want, got)
		}
	}
	// ⛔ And what was NOT the migration's business is kept: the brand banner,
	// the title, the served URL and the landing-page link.
	for _, kept := range []string{`<img src="banner.png"`, "# go-asmgen/docs", "https://go-asmgen.github.io/docs/", "The organization landing page"} {
		if !strings.Contains(got, kept) {
			t.Errorf("%q was lost:\n%s", kept, got)
		}
	}
}

// A README this cannot place is left alone and reported, because appending to
// it would leave two contradictory sets of instructions in the repository.
func TestAReadmeThisCannotPlace(t *testing.T) {
	current := "# a docs repo\n\nSomething nobody else wrote.\n"
	got, changed := updateREADME(current, "x/docs", "https://x.github.io/docs/")
	if changed || got != current {
		t.Errorf("it was rewritten anyway:\n%s", got)
	}
}

// The 38 with none get one, naming the right repository.
func TestTheReadmeOfASiteWithNone(t *testing.T) {
	got, changed := updateREADME("", "go-avkit/docs", "https://go-avkit.github.io/docs/")
	if !changed {
		t.Fatal("no README was written for a site with none")
	}
	for _, want := range []string{
		"# go-avkit/docs",
		"https://github.com/go-avkit",
		"<https://go-avkit.github.io/docs/>",
		"hugo server",
		"MkDocs Material plus mike",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is missing:\n%s", want, got)
		}
	}
}

// ⛔ repo_url names the ORGANISATION in 21 of these configs, not the
// repository, so the name has to be completed from the directory — a README
// naming the wrong repository is worse than one naming none.
func TestWhichRepositoryTheReadmeIsFor(t *testing.T) {
	for _, c := range []struct {
		repoURL, dir, want string
	}{
		{"https://github.com/go-asmgen", "/x/go-asmgen/docs", "go-asmgen/docs"},
		{"https://github.com/go-asmgen/docs", "/x/go-asmgen/docs", "go-asmgen/docs"},
		{"", "/x/go-tex/docs", filepath.Join("go-tex", "docs")},
	} {
		got := repoName(c.dir, site{RepoURL: c.repoURL})
		if got != c.want {
			t.Errorf("repo_url %q, dir %q: got %q, want %q", c.repoURL, c.dir, got, c.want)
		}
	}
}

// ⛔ A LINK WRITTEN AS RAW HTML is the same defect and a different regexp. 12
// of the 85 sites use md_in_html and write card grids as
// `<a href="page.md">`; the local Hugo build does not mind — an attribute is
// an attribute — so the first site with such a grid (go-aiquota/docs) shipped
// THREE DEAD LINKS on its front page and only the link checker in CI saw
// them.
func TestALinkWrittenAsHtml(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{
			`<a class="repo-card" href="repos/tray.md"><code>tray</code></a>`,
			`<a class="repo-card" href='{{< relref "repos/tray.md" >}}'><code>tray</code></a>`,
		},
		{
			`<a href='guide.md#part-two'>part two</a>`,
			`<a href='{{< relref "guide.md#part-two" >}}'>part two</a>`,
		},
		{
			`<a href="index.md">home</a>`,
			`<a href='{{< relref "_index.md" >}}'>home</a>`,
		},
		// Left alone: an absolute path, an external URL, and a link to
		// something that is not a page.
		{`<a href="/docs/x.md">x</a>`, `<a href="/docs/x.md">x</a>`},
		{`<a href="https://example.com/a.md">a</a>`, `<a href="https://example.com/a.md">a</a>`},
		{`<img src="diagram.png">`, `<img src="diagram.png">`},
	} {
		got, _ := convertBody(c.in + "\n")
		if got != c.want+"\n" {
			t.Errorf("\n got %q\nwant %q", got, c.want)
		}
	}
}

// ⛔ THE SENTENCE DOES NOT END AT THE FIRST FULL STOP, because the first full
// stop is inside the URL: `squidfunk.github.io`. The first version of this cut
// there and left `github.io/mkdocs-material/) and versioned with [mike](…)`
// standing in the README — visible in the very first diff, which is the
// argument for a tool that writes files a person then reads.
func TestTheBuiltWithSentenceIsReplacedWhole(t *testing.T) {
	for _, c := range []struct{ name, in string }{
		{
			"MkDocs and mike, over two lines",
			"built\nwith [MkDocs Material](https://squidfunk.github.io/mkdocs-material/) and\n" +
				"versioned with [mike](https://github.com/jimporter/mike). Published to the\n" +
				"`gh-pages` branch and served at <https://x.github.io/docs/>.\n",
		},
		{
			"MkDocs alone",
			"built with [MkDocs Material](https://squidfunk.github.io/mkdocs-material/). Published to the\n" +
				"`gh-pages` branch.\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, changed := updateREADME(c.in, "x/docs", "https://x.github.io/docs/")
			if !changed {
				t.Fatal("nothing was recognised")
			}
			for _, leftover := range []string{"squidfunk", "github.io/mkdocs-material", "jimporter", "MkDocs", "mike]"} {
				if strings.Contains(got, leftover) {
					t.Errorf("%q survived:\n%s", leftover, got)
				}
			}
			// The rest of the sentence is kept, and the full stop is not
			// doubled.
			if !strings.Contains(got, "Published to the") {
				t.Errorf("the rest of the sentence was eaten:\n%s", got)
			}
			if strings.Contains(got, "..") {
				t.Errorf("a doubled full stop:\n%s", got)
			}
		})
	}
}

// ⛔ MkDocs SERVES docs/ AS THE SITE ROOT, so docs/assets/x.png is at
// /assets/x.png and Hugo serves static/ that way instead. A converter that
// moved only the pages lost every picture on the site — go-filesystems/docs,
// 53 of them — and the local build did not care, because a missing image is
// not a build error. And a .md the nav never named is still built by MkDocs
// and still reachable, so dropping it loses a page.
func TestEverythingElseUnderDocs(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(dir, "docs", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.md", "# Home\n")
	write("listed.md", "# Listed\n")
	write("forgotten.md", "# Forgotten\n")
	write("notes/aside.md", "# An aside\n")
	write("assets/fs/apfs.png", "\x89PNG\r\n\x1a\nnot really")
	write("favicon.ico", "\x00\x00\x01\x00")
	write("css/extra.css", "body{}")

	nav := readMkdocs("nav:\n  - Home: index.md\n  - Listed: listed.md\n").Nav
	pages, missing := collect(nav, dir)
	if len(missing) != 0 {
		t.Fatalf("missing: %v", missing)
	}
	static, orphans, err := alsoUnderDocs(dir, pages)
	if err != nil {
		t.Fatal(err)
	}

	// The three non-pages move to static/, at the SAME relative path, because
	// that is the path the pages' own links use.
	for _, want := range []string{
		filepath.Join("static", "assets", "fs", "apfs.png"),
		filepath.Join("static", "favicon.ico"),
		filepath.Join("static", "css", "extra.css"),
	} {
		if _, ok := static[want]; !ok {
			t.Errorf("%s was not carried over; got %v", want, keysOf(static))
		}
	}
	if len(static) != 3 {
		t.Errorf("%d static files, want three: %v", len(static), keysOf(static))
	}
	// ⛔ And the bytes are the bytes: a picture read as text and written back
	// has to be identical, or every image on the site is subtly corrupt.
	if got := static[filepath.Join("static", "assets", "fs", "apfs.png")]; got != "\x89PNG\r\n\x1a\nnot really" {
		t.Errorf("the picture's bytes changed: %q", got)
	}

	// The two pages the nav never named are carried over, after the rest.
	var names []string
	for _, p := range orphans {
		names = append(names, filepath.Base(p.source))
		if p.weight < 1000 {
			t.Errorf("%s has weight %d, which puts it among the pages the nav ordered", p.source, p.weight)
		}
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "aside.md" || names[1] != "forgotten.md" {
		t.Errorf("the pages the nav never named came back as %v", names)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
