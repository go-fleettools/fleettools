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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

var versionRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

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
		if a == "-h" || a == "--help" || a == "-help" {
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

	versions, err := published(pages)
	if err != nil {
		return err
	}
	latest := versions[0]
	if err := writeVersions(pages, versions, latest); err != nil {
		return err
	}
	if err := writeLatest(pages, latest); err != nil {
		return err
	}
	if err := writeRedirect(pages); err != nil {
		return err
	}
	fmt.Fprintf(out, "published %s; latest is %s; versions: %s\n",
		version, latest, strings.Join(versions, ", "))
	return nil
}

// published is the versions in the branch, newest release first and main last.
//
// ⛔ 0.10 SORTS AFTER 0.9, which string order gets wrong — and a documentation
// site that offers "latest" has to mean it.
func published(pages string) ([]string, error) {
	entries, err := os.ReadDir(pages)
	if err != nil {
		return nil, err
	}
	var releases []string
	hasMain := false
	for _, e := range entries {
		// A symlink is an alias (latest), not a version.
		if fi, err := os.Lstat(filepath.Join(pages, e.Name())); err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			continue
		}
		switch {
		case e.Name() == "main":
			hasMain = true
		case versionRE.MatchString(e.Name()):
			releases = append(releases, e.Name())
		}
	}
	sort.Slice(releases, func(i, j int) bool {
		ai, bi := split(releases[i]), split(releases[j])
		if ai[0] != bi[0] {
			return ai[0] > bi[0]
		}
		return ai[1] > bi[1]
	})
	if hasMain {
		releases = append(releases, "main")
	}
	if len(releases) == 0 {
		return nil, fmt.Errorf("%s holds no version directory, not even the one just written", pages)
	}
	return releases, nil
}

func split(v string) [2]int {
	parts := strings.SplitN(v, ".", 2)
	a, _ := strconv.Atoi(parts[0])
	b, _ := strconv.Atoi(parts[1])
	return [2]int{a, b}
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

type entry struct {
	Version    string            `json:"version"`
	Title      string            `json:"title"`
	Aliases    []string          `json:"aliases"`
	Properties map[string]bool   `json:"properties,omitempty"`
	extra      map[string]string `json:"-"`
}

func writeVersions(pages string, versions []string, latest string) error {
	list := make([]entry, 0, len(versions))
	for _, v := range versions {
		e := entry{Version: v, Title: v, Aliases: []string{}}
		if v == latest {
			e.Aliases = []string{"latest"}
		}
		if v == "main" {
			e.Properties = map[string]bool{"development": true}
		}
		list = append(list, e)
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(pages, "versions.json"), append(b, '\n'), 0o644)
}

func writeLatest(pages, latest string) error {
	link := filepath.Join(pages, "latest")
	if err := os.RemoveAll(link); err != nil {
		return err
	}
	return os.Symlink(latest, link)
}

const redirect = `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>Redirecting</title>
  <noscript>
    <meta http-equiv="refresh" content="1; url=latest/" />
  </noscript>
  <script>
    window.location.replace(
      "latest/" + window.location.search + window.location.hash
    );
  </script>
</head>
<body>
  Redirecting to <a href="latest/">latest/</a>...
</body>
</html>
`

func writeRedirect(pages string) error {
	if err := os.WriteFile(filepath.Join(pages, "index.html"), []byte(redirect), 0o644); err != nil {
		return err
	}
	// ⛔ .nojekyll, or GitHub Pages runs Jekyll over the build and drops every
	// directory whose name begins with an underscore — which is most of what
	// Hugo writes.
	return os.WriteFile(filepath.Join(pages, ".nojekyll"), nil, 0o644)
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
