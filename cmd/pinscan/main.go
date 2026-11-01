// pinscan answers "which of our modules are pinned to an UNTAGGED commit of
// another of our modules that has since been released?"
//
//	pinscan <checkout-dir>...        # the roots to walk
//	pinscan -v ~/src                 # and name every pair
//
// It exits 1 when it finds something, 2 on a usage error, 0 otherwise, and
// always says what it read — a scan that walked nothing passes everything.
//
// # Why it exists
//
// go-downloader pinned github.com/go-macos/appbundle at
// v0.0.0-20260829193321, an untagged commit from 29 August. appbundle's Sign
// landed in v0.5.0 and the proxy serves v0.6.0, so the bundle it produced went
// on signing itself `Identifier=a.out` — the name the Go linker gives every
// binary — for seven weeks. The code was written, documented, tested and
// published, and the consumer could not reach it.
//
// The compiler is what found it, on an attempt to call Sign. Nothing in the
// fleet asked the question, and gorequires asks the other one: who requires
// module X, named in advance.
//
// # The distinction that makes it a measurement rather than a count
//
// ⛔ A PSEUDO-VERSION IS NOT A DEFECT. A module that was never tagged can only
// be referenced that way, and most of this fleet is in that state: measured
// 2026-10-18 over 669 local go.mod files, 262 fleet modules were pinned by
// pseudo-version and 234 of them had never been released. The defect is the
// intersection — pinned to a commit AND the module has a tag — which is 27
// modules, and 120 consumer-to-module pairs.
//
// ⛔ AND "BEHIND" IS NOT "DIFFERENT". A sibling tool in this repository once
// reported 52 stale pins by testing inequality, and 35 of them were NEWER than
// the tag: raising those would have been a downgrade. So the pin's own
// timestamp is compared against the tag's publication time, and a pin that is
// ahead is reported separately rather than counted as a problem.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Reports every require of a fleet module pinned at an untagged commit that is
OLDER than a release the consumer therefore cannot reach.

⛔ "BEHIND" IS NOT "DIFFERENT". A sibling tool here once reported 52 stale pins
by testing inequality, and 35 of them were NEWER than the tag: raising those
would have been a downgrade. So the pin's own timestamp is compared against the
tag's publication time, and a pin that is ahead is reported separately rather
than counted as a problem.

Exit status: 0 nothing is behind, 1 something is, 2 nothing was read — no go.mod
under the roots given means the roots are wrong, not the fleet.`

// ours decides whether a module path belongs to this fleet.
//
// It is a prefix test, and a prefix is not an ownership test — but here the
// question is only "is this one of ours to have released", and a false positive
// costs one proxy query that answers "never tagged". The orgs are the ones this
// fleet actually publishes under.
func ours(path string) bool {
	const host = "github.com/"
	if !strings.HasPrefix(path, host) {
		return false
	}
	org, _, ok := strings.Cut(strings.TrimPrefix(path, host), "/")
	if !ok {
		return false
	}
	switch org {
	// ⛔ THIS LIST IS THE TAXONOMY'S BLIND SPOT, and `wasmdesk` was missing
	// from it until 2026-11-01, when pinscan's own --repos output called
	// wasmdesk/wasmaqua and wasmdesk/wasmbox "not ours". They are ours. A
	// name list cannot tell "not in the list" from "not ours", so the report
	// no longer says "not ours" about an org it merely does not recognise.
	case "openweft", "nano-container-linux", "deskplan", "cloud-boot", "wasmdesk":
		return true
	}
	return strings.HasPrefix(org, "go-")
}

// noCommit is the pseudo-version Go writes when there is no commit at all:
// a module required but absent, or replaced by a local path.
//
// ⛔ IT IS NOT A PIN, and reading it as one is a measurement error this tool was
// written with. A shell pass over the same data reported
// openweft/weft-microvm-agent as "pinned since 0001-01-01", which is the zero
// time wearing a version number.
const noCommit = "00010101000000"

// Pin is one require of a fleet module at an untagged commit.
type Pin struct {
	Repo   string // the consumer, as owner/name
	Module string
	Pinned string // the pseudo-version in its go.mod
	Latest string // the newest tag the proxy serves, "" if never released
	TagAt  time.Time
	PinAt  time.Time

	// GitRepo is the repository that would receive the pull request, which is
	// NOT [Pin.Repo]: a worktree and a nested module each have their own
	// go.mod and consumer name, and share one repository. See [gitRepoOf].
	GitRepo string

	// Indirect is the `// indirect` marker: this consumer does not require the
	// module, something it requires does.
	//
	// ⛔ IT DECIDES WHERE THE REMEDY GOES, and most of them are inherited.
	// Measured 2026-11-01: go-freedesktop/notifications pins
	// go-images/images at a 2026-08-31 pseudo-version, marked indirect, and
	// `go mod why` gives notifications/cmd/notifyd -> go-widgets/toolkit ->
	// go-images/images/ops. toolkit requires it DIRECTLY. So raising it in
	// notifications treats a symptom; raising it in toolkit and releasing
	// toolkit reaches every consumer, as an ordinary TAGGED bump that
	// Renovate performs by itself.
	//
	// That is also why Renovate had not fixed any of this, which I had
	// wrongly blamed on dead runners: go-images's runner has succeeded daily
	// since 2026-10-28. Renovate raises tagged versions, and the
	// pseudo-version sits in the intermediate library, not in the consumer.
	Indirect bool
}

// Behind reports whether the pinned commit predates the release.
func (p Pin) Behind() bool {
	return p.Latest != "" && !p.PinAt.IsZero() && !p.TagAt.IsZero() && p.PinAt.Before(p.TagAt)
}

func main() { cli.Run(RootCmd()) }

// RootCmd builds the command. Tests drive it through run, below.
func RootCmd() *cobra.Command {
	var verbose, named bool
	c := cli.New("pinscan [flags] DIR...",
		"Pins of a fleet module older than a release the consumer cannot reach", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return cli.FromStatus(scan(cli.Out(cmd), cli.Err(cmd), verbose, named, args))
	}
	c.Flags().BoolVarP(&verbose, "verbose", "v", false,
		"name every pair rather than only counting")
	c.Flags().BoolVar(&named, "repos", false,
		"name the repositories that DECLARE a stale pin, which is the campaign's set")
	return c
}

// run keeps the signature this command's tests were written against.
func run(args []string, stdout, stderr io.Writer) int {
	return cli.Drive(RootCmd(), args, stdout, stderr)
}

func scan(stdout, stderr io.Writer, verbose, named bool, roots []string) int {
	// On stderr and inside the work, as it was: the staleness note is not part
	// of the answer, and a reader of the table must not have to filter it out.
	fleet.WarnIfStale(stderr)

	if len(roots) == 0 {
		fmt.Fprintln(stderr, "pinscan: name the directories to walk, e.g. ~/src")
		return 2
	}

	mods, files, err := walk(roots)
	if err != nil {
		fmt.Fprintln(stderr, "pinscan:", err)
		return 2
	}
	// ⛔ Say what was read, always: a walk that found no go.mod would otherwise
	// report "nothing pinned" in the same words as a clean fleet.
	fmt.Fprintf(stdout, "read %d go.mod file(s), %d require(s) of a fleet module at an untagged commit\n",
		files, len(mods))
	if files == 0 {
		fmt.Fprintln(stderr, "pinscan: no go.mod under those roots: the roots are wrong, not the fleet")
		return 2
	}

	// One proxy query per distinct module, not per pair.
	latest := map[string]string{}
	when := map[string]time.Time{}
	for _, p := range distinct(mods) {
		v, err := latestVersion(p)
		if err != nil {
			fmt.Fprintf(stderr, "  ? %s: %v\n", p, err)
			continue
		}
		if v == "" {
			continue // never released, so a pseudo-version is correct
		}
		latest[p] = v
		if t, err := releasedAt(p, v); err == nil {
			when[p] = t
		}
	}

	var behind, ahead, untagged []Pin
	for _, p := range mods {
		p.Latest, p.TagAt = latest[p.Module], when[p.Module]
		switch {
		case p.Latest == "":
			untagged = append(untagged, p)
		case p.Behind():
			behind = append(behind, p)
		default:
			ahead = append(ahead, p)
		}
	}

	fmt.Fprintf(stdout, "  %d pin(s) of a module that was NEVER released — a pseudo-version is the only way to name it\n", len(untagged))
	fmt.Fprintf(stdout, "  %d pin(s) NEWER than the newest tag — raising those would be a downgrade\n", len(ahead))
	fmt.Fprintf(stdout, "  %d pin(s) older than a release the consumer therefore cannot reach\n", len(behind))
	if len(behind) == 0 {
		return 0
	}

	// ⛔ A CAMPAIGN IS COSTED IN PULL REQUESTS, NOT IN go.mod FILES. A worktree
	// and a module nested inside a repository each have their own go.mod, and
	// share one pull request: measured in go-pdfkit, five consumers of
	// go-images/images are three repositories. Printing only the pin count
	// invites a plan about 40% too large.
	repos := map[string]bool{}
	for _, p := range behind {
		repos[p.GitRepo] = true
	}
	fmt.Fprintf(stdout, "  %d repositor%s hold them — "+
		"worktrees and nested modules share one\n",
		len(repos), plural(len(repos), "y", "ies"))

	// ⛔ DIRECT AND INHERITED ARE DIFFERENT WORK, and conflating them sizes a
	// campaign against the wrong set. An inherited pin is raised by releasing
	// the library that DECLARES it, after which every consumer gets an
	// ordinary tagged bump — so the campaign is the DIRECT set, and the
	// inherited count is what that set buys.
	//
	// ⛔ AND OUR TREES HOLD REPOSITORIES THAT ARE NOT OURS. A checkout under
	// these roots is not ours because it is on this disk: a sibling tool
	// published "904 of 939" over three pages before anybody asked whose the
	// modules were, and the true total was 262. Origin's owner decides, so a
	// repository we cannot open a pull request against is counted apart
	// rather than silently included.
	// ⛔⛔ AND "UNKNOWN" IS NOT "NOT OURS". [gitRepoOf] falls back to the
	// directory when a checkout has no readable origin, so those come back as
	// a PATH rather than owner/name. The first version of this counted all of
	// them as foreign: of 7 so called, 5 were simply checkouts whose remote
	// could not be read. An unread answer must not read as a negative.
	direct, inherited := map[string]bool{}, map[string]bool{}
	foreign, unknown := map[string]bool{}, map[string]bool{}
	nDirect := 0
	for _, p := range behind {
		if p.Indirect {
			inherited[p.GitRepo] = true
			continue
		}
		nDirect++
		direct[p.GitRepo] = true
		switch {
		case !strings.Contains(p.GitRepo, "/") || strings.HasPrefix(p.GitRepo, "/"):
			unknown[p.GitRepo] = true // a path: no origin to attribute it to
		case !ours("github.com/" + p.GitRepo):
			foreign[p.GitRepo] = true
		}
	}
	fmt.Fprintf(stdout, "  %d declared by the consumer, %d inherited through a dependency\n",
		nDirect, len(behind)-nDirect)
	fmt.Fprintf(stdout, "  → the campaign is %d repositor%s that DECLARE one: "+
		"%d in a fleet org, %d in another org, %d with no origin this could read\n",
		len(direct), plural(len(direct), "y", "ies"),
		len(direct)-len(foreign)-len(unknown), len(foreign), len(unknown))

	if named {
		fmt.Fprintln(stdout, "\nrepositories that declare a stale pin:")
		for _, r := range sortedStrings(direct) {
			switch {
			case unknown[r]:
				fmt.Fprintf(stdout, "    %s   (no origin read)\n", r)
			case foreign[r]:
				fmt.Fprintf(stdout, "    %s   (another org)\n", r)
			default:
				fmt.Fprintf(stdout, "    %s\n", r)
			}
		}
	}

	byModule(stdout, behind)
	if verbose {
		sort.Slice(behind, func(i, j int) bool { return behind[i].Repo < behind[j].Repo })
		for _, p := range behind {
			fmt.Fprintf(stdout, "    %-34s %-44s %s -> %s\n", p.Repo, p.Module,
				p.PinAt.Format("2006-01-02"), p.Latest)
		}
	}
	return 1
}

// byModule groups the findings, because the remedy is per module: one release
// reaches every consumer of it.
func byModule(w io.Writer, pins []Pin) {
	n := map[string]int{}
	oldest := map[string]time.Time{}
	tag := map[string]string{}
	// The repositories per module, which is what that module's remedy costs in
	// pull requests: two go.mod files in one checkout are one of them.
	repos := map[string]map[string]bool{}
	for _, p := range pins {
		n[p.Module]++
		tag[p.Module] = p.Latest
		if repos[p.Module] == nil {
			repos[p.Module] = map[string]bool{}
		}
		repos[p.Module][p.GitRepo] = true
		if t, ok := oldest[p.Module]; !ok || p.PinAt.Before(t) {
			oldest[p.Module] = p.PinAt
		}
	}
	keys := make([]string, 0, len(n))
	for k := range n {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if n[keys[i]] != n[keys[j]] {
			return n[keys[i]] > n[keys[j]]
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		fmt.Fprintf(w, "  %3d consumer(s) in %3d repo(s)  %-44s oldest pin %s, released %s\n",
			n[k], len(repos[k]), k, oldest[k].Format("2006-01-02"), tag[k])
	}
}

func distinct(pins []Pin) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range pins {
		if !seen[p.Module] {
			seen[p.Module] = true
			out = append(out, p.Module)
		}
	}
	sort.Strings(out)
	return out
}

// walk reads every root go.mod under the roots with modfile, not with a grep.
//
// ⛔ A PARSER, BECAUSE THE GREP VERSION GOT IT WRONG TWICE. A shell pass over
// this data mis-read a column-aligned report and reported zero findings, and
// separately took a proxy error message for a version string. modfile answers
// the question the file actually asks.
func walk(roots []string) ([]Pin, int, error) {
	var out []Pin
	files := 0
	repoCache := map[string]string{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // an unreadable directory is not an opinion
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", "vendor", "node_modules", "testdata":
					return filepath.SkipDir
				}
				if strings.HasSuffix(d.Name(), "-worktrees") {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Name() != "go.mod" {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			f, err := modfile.Parse(p, b, nil)
			if err != nil {
				return nil
			}
			files++
			repo := consumer(root, p)
			for _, r := range f.Require {
				if !ours(r.Mod.Path) || !module.IsPseudoVersion(r.Mod.Version) {
					continue
				}
				// ⛔ The zero-time pseudo-version is not a pin.
				if strings.Contains(r.Mod.Version, noCommit) {
					continue
				}
				pin := Pin{
					Repo:     repo,
					GitRepo:  gitRepoOf(filepath.Dir(p), repoCache),
					Module:   r.Mod.Path,
					Pinned:   r.Mod.Version,
					Indirect: r.Indirect,
				}
				if t, err := module.PseudoVersionTime(r.Mod.Version); err == nil {
					pin.PinAt = t
				}
				out = append(out, pin)
			}
			return nil
		})
		if err != nil {
			return nil, files, err
		}
	}
	return out, files, nil
}

// sortedStrings is the set in a stable order, so two runs of the same corpus
// print the same list.
func sortedStrings(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// plural picks a suffix, so the count and the noun agree.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// gitRepoOf names the REPOSITORY a go.mod belongs to, which is the unit of a
// pull request — and is not the same as the unit [consumer] reports.
//
// ⛔ MEASURED 2026-11-01, go-pdfkit: five "consumers" of go-images/images are
// THREE pull requests. `app` and `app.wt-verbs2` are one repository and one of
// its worktrees; `html2pdf` and `html2pdf/corpus` are one repository and a
// module nested inside it. A per-file count therefore overstates a campaign by
// about 40% in that sample, and a campaign is costed in pull requests.
//
// It walks up to the nearest `.git`. A linked worktree has a `.git` FILE whose
// `gitdir:` line points into the main checkout's `.git/worktrees/`, so a
// worktree resolves to the repository it belongs to rather than to itself. The
// identity is origin's owner/name when there is one, because that is what a
// pull request is opened against; otherwise the resolved directory, so a
// checkout with no remote still counts once rather than disappearing.
func gitRepoOf(dir string, cache map[string]string) string {
	if id, ok := cache[dir]; ok {
		return id
	}
	id := dir
	for d := dir; ; {
		if fi, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			root := d
			if !fi.IsDir() {
				if main := mainWorktreeOf(filepath.Join(d, ".git")); main != "" {
					root = main
				}
			}
			id = root
			if owner := originOf(root); owner != "" {
				id = owner
			}
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			break // no repository above it: the directory is its own identity
		}
		d = parent
	}
	cache[dir] = id
	return id
}

// mainWorktreeOf reads the `gitdir:` line of a linked worktree's .git file and
// returns the main checkout's root, or "" when the file is not one.
func mainWorktreeOf(gitFile string) string {
	b, err := os.ReadFile(gitFile)
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	gitDir := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if gitDir == line {
		return "" // no gitdir: prefix
	}
	sep := string(filepath.Separator)
	if i := strings.Index(gitDir, sep+".git"+sep+"worktrees"+sep); i >= 0 {
		return gitDir[:i]
	}
	return ""
}

// originOf reads origin's URL from a repository's config and renders it as
// owner/name. It parses the file rather than running git: this walks thousands
// of checkouts, and a process each would dominate the scan.
func originOf(root string) string {
	b, err := os.ReadFile(filepath.Join(root, ".git", "config"))
	if err != nil {
		return ""
	}
	inOrigin := false
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inOrigin = strings.HasPrefix(line, `[remote "origin"]`)
			continue
		}
		if !inOrigin {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) != "url" {
			continue
		}
		return ownerName(strings.TrimSpace(value))
	}
	return ""
}

// ownerName takes the last two segments of a remote URL, without `.git`, so
// that https://, ssh:// and scp-style remotes of one repository agree.
func ownerName(url string) string {
	url = strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git")
	url = strings.ReplaceAll(url, ":", "/")
	parts := strings.Split(url, "/")
	if len(parts) < 2 {
		return ""
	}
	owner, name := parts[len(parts)-2], parts[len(parts)-1]
	if owner == "" || name == "" {
		return ""
	}
	return owner + "/" + name
}

// consumer names the checkout a go.mod belongs to, as owner/name when the
// layout says so and as a path otherwise.
func consumer(root, goMod string) string {
	rel, err := filepath.Rel(root, filepath.Dir(goMod))
	if err != nil || rel == "." {
		return filepath.Base(filepath.Dir(goMod))
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	return rel
}

// proxy is the module proxy, a variable so a test can answer without a network.
var proxy = "https://proxy.golang.org"

// latestVersion is the newest release the proxy serves, or "" for a module that
// has never been released.
//
// ⛔ IT REFUSES ANYTHING THAT IS NOT A VERSION. The shell pass this replaces
// sorted the proxy's reply and took its last line, and for one module that
// reply was
//
//	fatal: could not read Username for 'https://github.com': terminal prompts disabled
//
// which sorted to the end and was reported as the module's latest release. A
// line that semver does not accept is an error, not a version.
func latestVersion(path string) (string, error) {
	esc, err := module.EscapePath(path)
	if err != nil {
		return "", err
	}
	resp, err := http.Get(proxy + "/" + esc + "/@v/list")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return "", nil // never released
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the proxy answered %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	best := ""
	for _, line := range strings.Fields(string(b)) {
		if !semver.IsValid(line) {
			// Not a version: a proxy error, an HTML page, anything.
			continue
		}
		if best == "" || semver.Compare(line, best) > 0 {
			best = line
		}
	}
	return best, nil
}

// releasedAt is when that version was published.
func releasedAt(path, version string) (time.Time, error) {
	esc, err := module.EscapePath(path)
	if err != nil {
		return time.Time{}, err
	}
	resp, err := http.Get(proxy + "/" + esc + "/@v/" + version + ".info")
	if err != nil {
		return time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return time.Time{}, fmt.Errorf("the proxy answered %s", resp.Status)
	}
	var info struct{ Time time.Time }
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info); err != nil {
		return time.Time{}, err
	}
	return info.Time, nil
}
