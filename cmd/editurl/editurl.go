// Package main implements editurl: whether a Hextra site's "Edit this page on
// GitHub" link points at the page's own source.
package main

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v4"
)

// Config is one site's edit-link configuration, as its configuration file
// writes it.
type Config struct {
	File    string // path relative to the checkout, so a finding names the line to change
	Base    string // params.editURL.base, verbatim
	Enabled bool   // params.editURL.enable
	Content string // contentDir, "content" when the config does not say
}

// configCandidates are the files this tool reads a site's configuration from.
//
// ⚠ IT IS NOT HUGO'S FULL SEARCH, and the difference is declared rather than
// hidden: Hugo merges every file under config/<environment>/ over these, and a
// theme's own config contributes too. What that costs here is a base defined
// in a place this list does not name, which is reported as a site with NO edit
// link rather than as a broken one -- an understatement, never a false finding.
var configCandidates = []string{
	"hugo.yaml", "hugo.yml",
	"config.yaml", "config.yml",
	"config/_default/hugo.yaml", "config/_default/hugo.yml",
	"config/_default/config.yaml", "config/_default/config.yml",
	"config/_default/params.yaml", "config/_default/params.yml",
}

// unreadableCandidates are configuration formats this tool does not parse.
//
// ⛔ THEY ARE COUNTED AND NAMED, NOT SKIPPED. A site whose configuration is
// TOML would otherwise read as a site with no edit link, which is the shape of
// defect this tool exists to catch: an absence that comes from not having
// looked. The count goes under the table, so "77 of 100" can never quietly
// mean "77 of the 100 I could open".
var unreadableCandidates = []string{
	"hugo.toml", "config.toml",
	"config/_default/hugo.toml", "config/_default/config.toml",
	"config/_default/params.toml",
	"hugo.json", "config.json",
}

// lookup walks a decoded YAML map by key, comparing keys WITHOUT case.
//
// ⛔ Hugo's configuration keys are case-insensitive: `editURL`, `editurl` and
// `EditURL` are one setting, and the fleet's own files use the first spelling
// while Hugo's documentation uses the second. A tool that matched the case
// would report a correctly configured site as unconfigured.
func lookup(m map[string]any, keys ...string) (any, bool) {
	cur := any(m)
	for _, want := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		found := false
		for k, v := range mm {
			if strings.EqualFold(k, want) {
				cur, found = v, true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	return cur, true
}

func lookupString(m map[string]any, keys ...string) string {
	v, ok := lookup(m, keys...)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// ReadConfig reads one checkout's edit-link configuration.
//
// found is false when no candidate file defines a base, and unreadable then
// names the configuration file that exists in a format this tool cannot parse,
// or "" when there is none. A base defined twice with two different values is
// reported as unreadable too: picking one would be a guess, and the site's
// rendered link follows Hugo's merge order rather than this tool's.
func ReadConfig(dir string) (cfg Config, found bool, unreadable string) {
	type def struct {
		file string
		cfg  Config
	}
	var defs []def
	for _, name := range configCandidates {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			continue
		}
		var m map[string]any
		if err := yaml.Unmarshal(b, &m); err != nil || m == nil {
			continue
		}
		// config/_default/params.yaml holds the params map itself, with no
		// `params:` key above it.
		var at []string
		base := filepath.Base(name)
		if base == "params.yaml" || base == "params.yml" {
			at = []string{"editURL"}
		} else {
			at = []string{"params", "editURL"}
		}
		raw, ok := lookup(m, at...)
		if !ok {
			continue
		}
		edit, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		c := Config{File: name, Content: "content"}
		c.Base = lookupString(edit, "base")
		if v, ok := lookup(edit, "enable"); ok {
			c.Enabled, _ = v.(bool)
		}
		if cd := lookupString(m, "contentDir"); cd != "" {
			c.Content = strings.Trim(cd, "/")
		}
		if c.Base == "" {
			continue
		}
		defs = append(defs, def{file: name, cfg: c})
	}
	switch len(defs) {
	case 0:
		for _, name := range unreadableCandidates {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name))); err == nil {
				return Config{}, false, name
			}
		}
		return Config{}, false, ""
	case 1:
		return defs[0].cfg, true, ""
	}
	for _, d := range defs[1:] {
		if d.cfg.Base != defs[0].cfg.Base {
			return Config{}, false, fmt.Sprintf("%s and %s define different bases (%s, %s)",
				defs[0].file, d.file, defs[0].cfg.Base, d.cfg.Base)
		}
	}
	return defs[0].cfg, true, ""
}

// Href is the link Hextra renders for the page at rel, a path relative to the
// site's content directory.
//
// ⛔ THE FORMULA IS READ OFF THE THEME, not guessed from one site's output.
// tannevaled/hextra@v0.24.1, layouts/_partials/toc.html:63-66:
//
//	$sourceDir := strings.TrimPrefix "/content" (… BaseDir … SourceRoot)
//	$path      := replace .File.Path "\\" "/"
//	$editURL    = urls.JoinPath $editURL $sourceDir $path
//
// SourceRoot is the language's content root (content/en for a site with
// languages in directories), BaseDir the project, so sourceDir is the part of
// the content path BELOW content -- "/en" -- and path is the file below that.
// Joined, they are exactly the page's path relative to content/, which is why
// base must end at content and not at content/en.
func Href(base, rel string) string {
	u, err := url.Parse(base)
	if err != nil {
		return strings.TrimRight(base, "/") + "/" + rel
	}
	u.Path = path.Join(u.Path, rel)
	return u.String()
}

// Reason is what is wrong with an href, as a value rather than as prose.
//
// ⛔ IT IS NOT DERIVED BACK OUT OF THE SENTENCE. A caller that classified
// findings by matching words in Why would be reading a label instead of a
// diagnosis, and the two drift the first time a sentence is reworded. The
// decision is made once, here, where the evidence is.
type Reason int

// The reasons, in the order the checks are made.
const (
	ReasonNone         Reason = iota // the href reaches the page's source
	ReasonNotAnEditURL               // the base is not github.com/<owner>/<repo>/edit/<ref>/...
	ReasonOtherRepo                  // it is an edit URL for another repository
	ReasonOtherBranch                // it is an edit URL for a branch this checkout's remote does not serve by default
	ReasonNoSuchPath                 // the shape is right and the path is not in the tree
	ReasonEscapesRepo                // the path it names lies outside the repository
)

// Link is one rendered href and the verdict on it.
type Link struct {
	Href   string
	InRepo string // the repository path the href names, when the shape allows one
	Why    string // the sentence a reader gets, "" when there is nothing wrong
	Reason Reason
}

// Resolve says which path inside the repository an href names.
//
// A non-zero Reason other than ReasonNoSuchPath is a defect of the BASE, and
// therefore true of every page the site renders. ReasonNoSuchPath cannot be
// decided here: only the tree knows, so Inspect decides it.
func Resolve(base, rel, owner, name, branch string) Link {
	l := Link{Href: Href(base, rel)}
	u, err := url.Parse(base)
	switch {
	case err != nil:
		l.Why, l.Reason = "the base is not a URL: "+err.Error(), ReasonNotAnEditURL
		return l
	case u.Scheme != "https" && u.Scheme != "http":
		l.Why = fmt.Sprintf("the base is not an http(s) URL (scheme %q)", u.Scheme)
		l.Reason = ReasonNotAnEditURL
		return l
	case u.Host != "github.com":
		l.Why = fmt.Sprintf("the base is not on github.com (host %q)", u.Host)
		l.Reason = ReasonNotAnEditURL
		return l
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segs) < 4 {
		l.Why = fmt.Sprintf("the base has %d path segments; /<owner>/<repo>/edit/<branch>/ needs four", len(segs))
		l.Reason = ReasonNotAnEditURL
		return l
	}
	// ⛔ THE /edit/ SEGMENT IS CHECKED BY POSITION, because the defect that
	// motivated this tool drops the REPOSITORY and leaves everything else in
	// place: github.com/go-desktop/edit/main/content names an owner, a
	// repository called "edit" -- which does not exist -- and a branch called
	// "main". Every segment is individually plausible; only their positions are
	// wrong, and GitHub answers 404 because the repository is not there.
	if segs[2] != "edit" {
		l.Why = fmt.Sprintf("the third segment is %q, not \"edit\": the href names the repository %s/%s",
			segs[2], segs[0], segs[1])
		l.Reason = ReasonOtherRepo
		return l
	}
	if segs[0] != owner || segs[1] != name {
		l.Why = fmt.Sprintf("the href names %s/%s; this checkout is %s/%s", segs[0], segs[1], owner, name)
		l.Reason = ReasonOtherRepo
		return l
	}
	if branch != "" && segs[3] != branch {
		l.Why = fmt.Sprintf("the href names branch %q; this checkout's default is %q", segs[3], branch)
		l.Reason = ReasonOtherBranch
		return l
	}
	l.InRepo = path.Join(path.Join(segs[4:]...), rel)
	// ⛔ THE BASE IS UNTRUSTED INPUT AND path.Join CLEANS RATHER THAN REFUSES.
	// It comes out of a repository's own hugo.yaml, and this command walks
	// ~1300 checkouts; any one of them can write what it likes there. A base
	// ending in `..` left InRepo as "../passwd", and Inspect then stat-ed a
	// path OUTSIDE the checkout -- existence only, never a read, but an
	// existence oracle over the file system is not something a documentation
	// checker gets to have. Witnessed by
	// TestABaseThatEscapesTheRepositoryIsRefused on the released v0.124.0 code
	// before this guard existed, and held by FuzzResolve as a property.
	if l.InRepo == ".." || strings.HasPrefix(l.InRepo, "../") || strings.HasPrefix(l.InRepo, "/") {
		l.Why = fmt.Sprintf("the path it names, %s, is outside the repository", l.InRepo)
		l.Reason = ReasonEscapesRepo
		l.InRepo = ""
	}
	return l
}

// Pages lists the content files that render an edit link, as paths relative to
// the content directory, and counts the ones a front-matter editURL overrides.
//
// ⚠ A PAGE MAY SET ITS OWN editURL, and the theme prefers it to the base
// (toc.html:57-59). Those pages are not governed by the base, so counting them
// as broken would charge a site for a link it does not render from the setting
// under test.
func Pages(contentRoot string) (rel []string, overridden int, err error) {
	err = filepath.WalkDir(contentRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		base := d.Name()
		if d.IsDir() {
			if base != "." && strings.HasPrefix(base, ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(base, ".md") {
			return nil
		}
		r, rerr := filepath.Rel(contentRoot, p)
		if rerr != nil {
			return rerr
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if frontMatterEditURL(b) {
			overridden++
			return nil
		}
		rel = append(rel, filepath.ToSlash(r))
		return nil
	})
	return rel, overridden, err
}

// frontMatterEditURL says whether a page sets editURL in its own YAML front
// matter.
func frontMatterEditURL(b []byte) bool {
	s := string(b)
	if !strings.HasPrefix(s, "---\n") {
		return false
	}
	end := strings.Index(s[4:], "\n---")
	if end < 0 {
		return false
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(s[4:4+end]), &m); err != nil || m == nil {
		return false
	}
	_, ok := lookup(m, "editURL")
	return ok
}

// Problem is one site whose edit links do not reach the pages' source.
type Problem struct {
	Repo    string // org/repo, as checkout.Repos names it
	Config  string // the configuration file that holds the base
	Base    string // the base as written
	Why     string
	Reason  Reason
	Href    string // the first href that does not resolve
	Page    string // the page it was rendered for
	Broken  int    // pages whose href does not resolve
	Pages   int    // pages that render an edit link at all
	Correct string // a base that would resolve
}

// Inspect judges one checkout's edit links. tree is the directory holding the
// site (a checkout, or a tree materialised at a ref); repo is its org/repo
// name, owner and name its GitHub coordinates and branch its default branch,
// all passed in because a materialised tree has no .git to ask.
func Inspect(tree, repo, owner, name, branch string, cfg Config) (*Problem, int, error) {
	contentRoot := filepath.Join(tree, filepath.FromSlash(cfg.Content))
	rel, _, err := Pages(contentRoot)
	if err != nil {
		return nil, 0, err
	}
	correct := "https://github.com/" + owner + "/" + name + "/edit/" + branch + "/" + cfg.Content
	p := &Problem{
		Repo: repo, Config: cfg.File, Base: cfg.Base,
		Pages: len(rel), Correct: correct,
	}
	for _, r := range rel {
		l := Resolve(cfg.Base, r, owner, name, branch)
		if l.Reason == ReasonNone {
			if _, err := os.Stat(filepath.Join(tree, filepath.FromSlash(l.InRepo))); err == nil {
				continue
			}
			l.Why = "the path it names, " + l.InRepo + ", is not in the repository"
			l.Reason = ReasonNoSuchPath
		}
		p.Broken++
		if p.Why == "" {
			p.Why, p.Reason, p.Href, p.Page = l.Why, l.Reason, l.Href, r
		}
	}
	if p.Broken == 0 {
		return nil, len(rel), nil
	}
	return p, len(rel), nil
}

// originRepo is the GitHub owner and repository a checkout's origin names.
//
// ⛔ IT IS NOT THE DIRECTORY NAME. The clone farm is laid out as
// <org>/<repo>, so the path looks like an answer -- but a repository that has
// been TRANSFERRED keeps the old directory name, and go-desktop's catalog read
// eight such repositories as gone when they had moved to another organisation.
// The remote is what GitHub will serve.
//
// It is a variable so a test can give a fixture tree an origin without
// creating a repository.
var originRepo = func(dir string) (owner, name string, ok bool) {
	b, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", "", false
	}
	return splitRemote(strings.TrimSpace(string(b)))
}

// splitRemote reads owner and repository out of a git remote URL.
//
// ⛔ BOTH SPELLINGS ARE ONE REPOSITORY. runson counted
// git@github.com:o/r and https://github.com/o/r separately and reported 620
// repositories where there were 504; the count looked entirely reasonable and
// the list had the same name twice.
func splitRemote(remote string) (owner, name string, ok bool) {
	s := remote
	switch {
	case strings.HasPrefix(s, "git@"):
		if i := strings.Index(s, ":"); i >= 0 {
			s = s[i+1:]
		}
	default:
		u, err := url.Parse(s)
		if err != nil {
			return "", "", false
		}
		s = strings.TrimPrefix(u.Path, "/")
	}
	s = strings.TrimSuffix(s, ".git")
	segs := strings.Split(strings.Trim(s, "/"), "/")
	if len(segs) != 2 || segs[0] == "" || segs[1] == "" {
		return "", "", false
	}
	return segs[0], segs[1], true
}
