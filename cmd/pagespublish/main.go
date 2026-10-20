// Command pagespublish puts one documentation build into a checkout of a
// gh-pages branch, the way mike did it, so that every published version keeps
// its own path.
//
//	pagespublish <version> <built site> <gh-pages checkout>
//
// <version> is MAJOR.MINOR (0.16) or "main" while no release is tagged. The
// build replaces <pages>/<version>/ and nothing else; then, from the
// directories present:
//
//	versions.json   mike's format ({version, title, aliases}), newest release
//	                first and "main" last, which is what the Hextra fork's
//	                version selector reads
//	latest          a link to the newest release, or to main when none
//	index.html      the root, redirecting to latest/, as mike wrote it
//
// ⛔ IT EXISTS BECAUSE A HAND-WRITTEN PUBLISH STEP GOT THE PATH WRONG, in 76
// repositories at once. The workflow mkdocs2hextra generated copied the built
// site to `docs/<version>/` inside gh-pages — but a project repository called
// `docs` is already served at `/docs/`, so the site landed at
// `/docs/docs/main/` while `/docs/latest/` went on serving the MkDocs build
// mike had left there. Both URLs answered 200, which is why no check caught
// it: the site was published, just not where anything links.
//
// ⛔ AND IT IS GO RATHER THAN THE SHELL SCRIPT IT REPLACES. The fleet's
// reference site carries a 60-line scripts/publish-version.sh; copying that
// into 76 repositories would be 76 copies of untested shell, and the house
// rule is Go. One command, one test suite, called in one line from every
// workflow.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-fleettools/fleettools/internal/fleet"
	// Aliased: `pages` is this command's name for the gh-pages checkout.
	pagesrules "github.com/go-fleettools/fleettools/internal/pages"
)

var versionRE = pagesrules.VersionRE

func main() {
	// Every command here says whether the binary is older than the module it
	// was built from; a publish step run from CI through `go run …@tag` is
	// exactly where that matters.
	fleet.WarnIfStale(os.Stderr)
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "⛔ %v\n", err)
		os.Exit(2)
	}
}

const usage = `usage: pagespublish <version> <built site> <gh-pages checkout>

Puts one documentation build into a checkout of a gh-pages branch, the way mike
did it: <version> — MAJOR.MINOR, or "main" while no release is tagged —
replaces <pages>/<version>/ AT THE BRANCH ROOT and nothing else, and then
versions.json, the latest link and the root redirect are rewritten from the
versions present.`

func run(args []string, out io.Writer) error {
	// ⛔ -h ANSWERS. A command that reads -h as its first argument is one
	// somebody runs to see its flags and gets a publish instead; this
	// repository has a control for exactly that, and the control was right
	// about this command before this loop existed.
	for _, a := range args {
		if fleet.IsHelpRequest(a) {
			fmt.Fprintln(out, usage)
			return nil
		}
	}
	if len(args) != 3 {
		return fmt.Errorf("%s", usage)
	}
	version, site, pages := args[0], args[1], args[2]
	if version != "main" && !versionRE.MatchString(version) {
		return fmt.Errorf("version %q is neither MAJOR.MINOR nor main", version)
	}
	// ⛔ The build has to exist before anything is replaced: publishing an
	// empty directory over a live version is the one mistake here that cannot
	// be undone from the branch itself.
	if _, err := os.Stat(filepath.Join(site, "index.html")); err != nil {
		return fmt.Errorf("%s/index.html is missing: build the site first", site)
	}
	if fi, err := os.Stat(pages); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", pages)
	}

	// The version's own directory, replaced whole. If it was a link (an
	// alias), the link goes rather than what it pointed at.
	dest := filepath.Join(pages, version)
	if fi, err := os.Lstat(dest); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(dest); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	if err := copyTree(site, dest); err != nil {
		return fmt.Errorf("copying the built site: %w", err)
	}

	if pruned, err := pruneMisplaced(pages); err != nil {
		return err
	} else if pruned != "" {
		fmt.Fprintf(out, "removed %s, which an earlier workflow published one level too deep\n", pruned)
	}

	versions, latest, err := pagesrules.Reconcile(pages)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "published %s; latest is %s; versions: %s\n",
		version, latest, strings.Join(versions, ", "))
	return nil
}

// pruneMisplaced removes a `docs` directory at the branch root that holds
// nothing but version directories — the shape the mistaken workflow left
// behind, and nothing a real site has.
//
// ⛔ It refuses to touch a `docs` directory that has an index.html of its own:
// that would be somebody's page, not a misplacement.
func pruneMisplaced(pages string) (string, error) {
	stray := filepath.Join(pages, "docs")
	entries, err := os.ReadDir(stray)
	if err != nil {
		return "", nil // nothing there, which is the normal case
	}
	if _, err := os.Stat(filepath.Join(stray, "index.html")); err == nil {
		return "", nil
	}
	if len(entries) == 0 {
		return "", nil
	}
	for _, e := range entries {
		if !e.IsDir() || (e.Name() != "main" && !versionRE.MatchString(e.Name())) {
			return "", nil
		}
	}
	if err := os.RemoveAll(stray); err != nil {
		return "", err
	}
	return "docs/", nil
}

func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}
