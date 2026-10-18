// Command mkdocs2hextra converts an MkDocs Material site to Hugo with this
// fleet's Hextra fork.
//
// It exists because the fleet has EIGHTY-FIVE of them — 575 pages, measured —
// and the conversion is the same five mechanical steps every time, four of
// which are invisible when done wrong:
//
//	1 252 links to a .md file   Hugo serves /page/, not /page.md. Rewritten to
//	                            {{< relref >}}, which FAILS THE BUILD when the
//	                            target is missing instead of 404ing later.
//	  209 admonitions           !!! note "…" → Hextra's callout shortcode,
//	                            across nine kinds; the collapsible ??? form
//	                            becomes details.
//	   24 content tabs          === "label" → one tabs shortcode per run.
//	   84 attribute lists       {: .class } carries a stylesheet that does not
//	                            come with the pages; dropped and counted.
//	   82 sites on mike         each documented version had its own path, so
//	                            the Hugo build keeps one: params.docs.version
//	                            reaches --baseURL in CI.
//
// ⛔ IT PREVIEWS BY DEFAULT AND WRITES ONLY WITH --apply. A converter that
// writes on sight is one that cannot be read before it runs, and the diff of a
// migration is the only review it will ever get.
//
// ⛔ AND IT NEVER DELETES. mkdocs.yml, requirements.txt and the old workflow
// are LISTED for the person to remove in the same commit, because a tool that
// removes the old site and writes a broken new one leaves nothing to go back
// to. git is the undo, and a tool should not be the only thing standing
// between a repository and its history.
//
//	mkdocs2hextra ~/src/go-asmgen/docs              # say what it would do
//	mkdocs2hextra --apply ~/src/go-asmgen/docs      # write it
//	mkdocs2hextra --apply --version v0.3.1 ~/src/x  # a release, not main
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

// hextraPin is the fork's tag this converter wires in. It is a constant and not
// a flag: eighty-five sites pinned to whatever was newest on the day each one
// was converted is eighty-five different sites.
const hextraPin = "v0.16.0"

const longHelp = `Converts an MkDocs Material site (mkdocs.yml plus docs/*.md) to Hugo with the
tannevaled/hextra fork, in place:

    content/en/…               the pages, with front matter and nav weights
    hugo.yaml                  the site, with a version in its baseURL
    .github/workflows/docs.yml build, link-check, and publish per version
    .gitmodules                themes/hextra pinned at ` + hextraPin + `

Without --apply it writes nothing and prints what it would do. Exits 1 when
something was found that it cannot convert, 2 when it cannot read the site.`

func RootCmd() *cobra.Command {
	var (
		apply   bool
		version string
	)
	c := &cobra.Command{
		Use:   "mkdocs2hextra <site directory>",
		Short: "Convert an MkDocs Material site to Hugo + the Hextra fork",
		Long:  longHelp,
		// A failure is a failure, not an invitation to read the usage text
		// again: the usage is printed for a usage error only.
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	c.Args = cobra.ExactArgs(1)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return run(cmd, args[0], apply, version)
	}
	f := c.Flags()
	f.BoolVar(&apply, "apply", false, "write the converted site instead of describing it")
	f.StringVar(&version, "version", "main", `the version the pages describe, as vMAJOR.MINOR.PATCH, or "main"`)
	return c
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	runMain(RootCmd())
}

func run(cmd *cobra.Command, dir string, apply bool, version string) error {
	out := cmd.OutOrStdout()

	cfgPath := filepath.Join(dir, "mkdocs.yml")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		return unanswerable("%s: %v", cfgPath, err)
	}
	s := readMkdocs(string(cfg))
	if len(s.Nav) == 0 {
		// ⛔ A site with no nav is not a site this can order. MkDocs falls
		// back to alphabetical, and alphabetical puts chapter 10 before
		// chapter 2 — the same defect as reading an EPUB's ZIP in name order.
		return unanswerable("%s has no nav: the page order cannot be recovered, and alphabetical is not it", cfgPath)
	}

	pages, missing := collect(s.Nav, dir)
	fmt.Fprintf(out, "%s: %d pages in the nav\n", s.Name, len(pages))

	var total conversion
	writes := map[string]string{}
	for _, p := range pages {
		// ⛔ A SECTION HAS NO FILE OF ITS OWN in MkDocs — `- Reference:` is a
		// heading in the nav and nothing else — so Hugo needs an _index.md
		// that does not exist in the source. It gets front matter and no body,
		// which is what makes the section appear in Hextra's sidebar with its
		// pages under it. A real site found this; no fixture had a section.
		if p.source == "" {
			writes[p.dest] = frontMatter(p.nav, p.weight)
			continue
		}
		src, err := os.ReadFile(p.source)
		if err != nil {
			return unanswerable("%s: %v", p.source, err)
		}
		body, stats := convertBody(string(src))
		total.add(stats)
		writes[p.dest] = frontMatter(p.title(string(src)), p.weight) + strings.TrimLeft(body, "\n")
	}
	// ⛔ EVERY OTHER FILE UNDER docs/ IS CONTENT TOO. MkDocs serves docs/ as
	// the site root, so docs/assets/fs/x.png is served at /assets/fs/x.png;
	// Hugo serves static/ that way instead. A converter that moved only the
	// pages lost every picture: go-filesystems/docs lost 53 of them, and the
	// local build did not care — a missing image is not a build error — so
	// the link checker in CI is what said so.
	//
	// ⛔ AND A PAGE THE NAV DOES NOT NAME IS STILL A PAGE. MkDocs builds every
	// .md under docs/ whether the nav lists it or not, so an unlisted page is
	// reachable by its URL and may be linked from one that IS listed. They are
	// carried over, after the listed ones, and counted out loud.
	extra, orphans, err := alsoUnderDocs(dir, pages)
	if err != nil {
		return unanswerable("reading docs/: %v", err)
	}
	for name, body := range extra {
		writes[name] = body
	}
	for _, p := range orphans {
		src, err := os.ReadFile(p.source)
		if err != nil {
			return unanswerable("%s: %v", p.source, err)
		}
		body, stats := convertBody(string(src))
		total.add(stats)
		writes[p.dest] = frontMatter(p.title(string(src)), p.weight) + strings.TrimLeft(body, "\n")
	}

	writes["hugo.yaml"] = hugoConfig(s, version)
	writes[filepath.Join(".github", "workflows", "docs.yml")] = workflow(s, version)

	// ⛔ THE README IS PART OF THE CHANGE. In this fleet a change a reader can
	// see is not finished until the README says it, and 43 of the 46 sites
	// that have one tell the reader to run `pip install -r requirements.txt`.
	// A migration that left those standing would publish instructions for a
	// site that no longer exists.
	readmeNote := ""
	switch current, err := os.ReadFile(filepath.Join(dir, "README.md")); {
	case err != nil:
		// One of the 38 with none: it gets one.
		writes["README.md"] = newREADME(repoName(dir, s), strings.TrimSuffix(s.URL, "/")+"/")
	default:
		updated, changed := updateREADME(string(current), repoName(dir, s), strings.TrimSuffix(s.URL, "/")+"/")
		if changed {
			writes["README.md"] = updated
		} else {
			readmeNote = "README.md says nothing this recognises: read it yourself"
		}
	}

	names := make([]string, 0, len(writes))
	for name := range writes {
		names = append(names, name)
	}
	sort.Strings(names)
	// ⛔ The static files are COUNTED rather than listed: one site has 53
	// pictures, and a report nobody reads to the end is a report.
	staticFiles, staticBytes := 0, 0
	for _, name := range names {
		if strings.HasPrefix(name, "static"+string(filepath.Separator)) {
			staticFiles++
			staticBytes += len(writes[name])
			continue
		}
		fmt.Fprintf(out, "  %s %-46s %d bytes\n", verb(apply), name, len(writes[name]))
	}
	if staticFiles > 0 {
		fmt.Fprintf(out, "  %s %-46s %d bytes (pictures and the like, from docs/)\n",
			verb(apply), fmt.Sprintf("static/ — %d files", staticFiles), staticBytes)
	}
	fmt.Fprintf(out, "carried over: %v\n", total)
	if len(orphans) > 0 {
		fmt.Fprintf(out, "⚠ %d page(s) the nav never named, carried over after the rest: %s\n",
			len(orphans), strings.Join(orphanNames(orphans), ", "))
	}
	if readmeNote != "" {
		fmt.Fprintf(out, "⚠ %s\n", readmeNote)
	}

	if apply {
		for _, name := range names {
			path := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return unanswerable("%s: %v", path, err)
			}
			if err := os.WriteFile(path, []byte(writes[name]), 0o644); err != nil {
				return unanswerable("%s: %v", path, err)
			}
		}
		if err := pinTheme(dir); err != nil {
			return unanswerable("themes/hextra: %v", err)
		}
	}

	// ⛔ What is left behind is NAMED, every time, whether or not --apply was
	// given. The old site is still the one the CI builds until somebody
	// removes these, and a converter that said nothing about them would
	// produce a repository with two documentation systems and one green lane.
	leftovers := leftBehind(dir)
	if len(leftovers) > 0 {
		fmt.Fprintf(out, "\nstill there, and the old site builds from them — remove in the same commit:\n")
		for _, l := range leftovers {
			fmt.Fprintf(out, "  %s\n", l)
		}
	}
	if s.Mike {
		fmt.Fprintf(out, "\nthis site was versioned with mike: the Hugo build keeps a path per version (params.docs.version = %q)\n", version)
	}
	if len(missing) > 0 {
		for _, m := range missing {
			fmt.Fprintf(out, "\nthe nav names %s, which is not in docs/\n", m)
		}
		return found("%d page(s) the nav names are not there", len(missing))
	}
	return nil
}

func verb(apply bool) string {
	if apply {
		return "wrote"
	}
	return "would write"
}

// a page is one nav entry resolved onto a file.
type page struct {
	source string
	dest   string
	weight int
	nav    string
}

// title is the nav's title when it has one, and otherwise the page's own first
// heading — never the file name, which is how a page ends up called
// "getting-started".
func (p page) title(src string) string {
	if p.nav != "" {
		return p.nav
	}
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return strings.TrimSuffix(filepath.Base(p.source), ".md")
}

// collect walks the nav in order and gives each page its weight.
//
// ⛔ THE WEIGHT IS THE NAV'S ORDER, which is the only thing that carries it
// over: MkDocs orders pages by the nav and Hugo orders them by weight, and
// nothing else in either system knows that "Getting started" comes before
// "API". Ten apart, so a page can be inserted later without renumbering.
// ⛔ A PAGE KEEPS ITS OWN PATH. The first version of this built a directory
// from each section's TITLE and put the children inside it, which on a real
// site gave content/en/asmgen/asmgen/design.md: the section was called
// "asmgen" and its pages were already under docs/asmgen/. Every one of the
// 1 252 converted links is relative, so a path invented here breaks them all.
//
// So the section's directory is the one its pages are ALREADY in, and the
// section's own page is either a child index.md — renamed _index.md, which is
// what Hugo calls a section page — or a bare one written beside them.
func collect(nav []navEntry, dir string) ([]page, []string) {
	var (
		pages   []page
		missing []string
		weight  = 10
	)
	var walk func(entries []navEntry, section string)
	walk = func(entries []navEntry, section string) {
		for _, e := range entries {
			if len(e.Children) > 0 {
				common := commonDir(e.Children)
				if common != "" && !hasIndex(e.Children) {
					// The pages share a directory and none of them is its
					// index: the section needs a page of its own there.
					pages = append(pages, page{
						dest:   filepath.Join("content", "en", common, "_index.md"),
						weight: weight,
						nav:    e.Title,
					})
					weight += 10
				}
				// ⛔ When the children DO NOT share a directory, the section
				// cannot become one without moving files, and moving them
				// would break their links. The pages keep their paths, which
				// flattens that heading in the sidebar — said out loud in the
				// report rather than silently.
				walk(e.Children, e.Title)
				continue
			}
			if e.File == "" {
				continue
			}
			src := filepath.Join(dir, "docs", e.File)
			if _, err := os.Stat(src); err != nil {
				missing = append(missing, e.File)
				continue
			}
			dest := e.File
			title := e.Title
			if filepath.Base(dest) == "index.md" {
				dest = filepath.Join(filepath.Dir(dest), "_index.md")
				// A section's index page is the section in Hugo's sidebar, so
				// it takes the SECTION's title rather than its own nav label:
				// "asmgen" is what the nav showed as the group, and
				// "Overview" was the label of the page inside it.
				if section != "" {
					title = section
				}
			}
			pages = append(pages, page{
				source: src,
				dest:   filepath.Join("content", "en", dest),
				weight: weight,
				nav:    title,
			})
			weight += 10
		}
	}
	walk(nav, "")
	return pages, missing
}

// commonDir is the directory every one of these entries' files is in, or "".
func commonDir(entries []navEntry) string {
	dir := ""
	for _, e := range entries {
		if e.File == "" {
			return "" // a nested section: not one directory
		}
		d := filepath.Dir(e.File)
		if d == "." {
			return ""
		}
		if dir == "" {
			dir = d
			continue
		}
		if d != dir {
			return ""
		}
	}
	return dir
}

func hasIndex(entries []navEntry) bool {
	for _, e := range entries {
		if filepath.Base(e.File) == "index.md" {
			return true
		}
	}
	return false
}

// leftBehind is the MkDocs machinery the conversion does not touch.
func leftBehind(dir string) []string {
	var out []string
	for _, name := range []string{
		"mkdocs.yml",
		"requirements.txt",
		filepath.Join(".github", "workflows", "docs-mkdocs.yml"),
		filepath.Join(".github", "workflows", "mkdocs.yml"),
		filepath.Join(".github", "workflows", "pages.yml"),
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			out = append(out, name)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "docs")); err == nil && len(entries) > 0 {
		out = append(out, fmt.Sprintf("docs/ (%d entries — the pages are now under content/en/)", len(entries)))
	}
	return out
}
