// Command gopin replaces `go-version: stable` in a repository's workflows with
// an explicit version, and raises go.mod to match.
//
// # WHY AN ALIAS IS THE DEFECT
//
// `stable` is a channel, not a version. Three things follow, all measured on
// go-pkgx on 2026-10-04 before this was written:
//
//   - The toolchain moves with no commit. CI was building and running on
//     go1.27.1 — `Setup go version spec stable` / `go version go1.27.1` in the
//     job log — while every go.mod in the organisation still said `go 1.26.4`.
//     A MINOR Go bump reindents under gofmt and changes coverage block
//     counting, and that one arrived with nothing to review.
//
//   - A Renovate rule written for exactly this cannot act. The go-pkgx preset
//     matches dep name `go` under the `gomod` and `github-actions` managers
//     with `automerge: false` and `groupName: null`, citing golang/go#81000.
//     It had never produced a pull request, because there was no version to
//     compare or rewrite.
//
//   - The contrast is demonstrable, not argued. In ONE ci.yml,
//     go-pkgx/registry-viewer, the `build` job said `stable` and Renovate did
//     nothing; the `bricolint` job said "1.26.4" and Renovate opened a pull
//     request to move it to 1.27.1. Same file, same run, one variable.
//
// So pinning is not rigidity: it is the condition for the review rule to apply
// at all.
//
// # WHY IT READS THE API AND NEVER A LOCAL CHECKOUT
//
// A local clone is a claim about whenever it was last fetched. On the day this
// was written a local sweep still reported three go-pkgx repositories as
// asking for `stable` hours after all eight had been pinned and merged. Every
// read here is of the default branch as GitHub holds it.
//
// # WHAT IT REFUSES TO DO
//
// It will not touch a job that already names a concrete version, even an old
// one. Somebody chose that, possibly to hold a repository back, and silently
// moving it is the opposite of what this tool is for. It reports those instead.
package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

// reStable matches the alias in both spellings the fleet uses: a block mapping
// (`go-version: stable`) and the inline flow mapping
// (`with: { go-version: stable }`).
//
// The trailing class is not `\b`, and a test says why: `\b` sits between `e`
// and `-`, so `go-version: stable-1` came out as `go-version: '1.27.1'-1`.
// RE2 has no lookahead, so the following character is captured and put back.
// `oldstable` is safe without any of this -- `\s*` cannot consume `old` --
// but it has a test too, because "safe by construction" is exactly how the
// first one got through.
var reStable = regexp.MustCompile(`(?m)(go-version:\s*)stable([^\w.-]|$)`)

// reLiteral finds a job that already names a version, which this tool leaves
// alone and reports.
var reLiteral = regexp.MustCompile(`go-version:\s*['"]?(\d+\.\d+(?:\.\d+)?)['"]?`)

// reGoDirective is go.mod's own version line.
var reGoDirective = regexp.MustCompile(`(?m)^go\s+(\d+\.\d+(?:\.\d+)?)\s*$`)

// finding is what one repository needs, or why it needs nothing.
type finding struct {
	Repo     string   `json:"repo"`
	Files    []string `json:"files,omitempty"`    // workflow paths holding the alias
	Aliases  int      `json:"aliases,omitempty"`  // how many occurrences
	GoMod    string   `json:"gomod,omitempty"`    // the current go directive, "" if none
	Literals []string `json:"literals,omitempty"` // versions already pinned, left alone
	Err      string   `json:"error,omitempty"`
}

func main() {
	// Every command here does this first: a binary built from an older
	// checkout than HEAD says so rather than quietly doing an older thing.
	// It matters more for this one than most, since it writes to other
	// people's repositories.
	fleet.WarnIfStale(os.Stderr)
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gopin", flag.ContinueOnError)
	fs.SetOutput(stderr)
	version := fs.String("version", "", "the Go version to pin, e.g. 1.27.1 (required)")
	list := fs.String("list", "", "file of org/repo lines, # comments; default: read stdin")
	apply := fs.Bool("apply", false, "open pull requests; without it this only reports")
	pause := fs.Duration("pause", 3*time.Second, "wait this long between repositories; GitHub's SECONDARY limit brakes a burst and waiting is the whole remedy")
	limit := fs.Int("limit", 0, "stop after this many repositories that needed a change (0: no limit)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *version == "" {
		fmt.Fprintln(stderr, "gopin: -version is required: there is no safe default, and a default would be the same unreviewable choice as `stable`")
		return 2
	}
	target, err := parseGoVersion(*version)
	if err != nil {
		fmt.Fprintf(stderr, "gopin: %v\n", err)
		return 2
	}

	repos, err := readList(*list)
	if err != nil {
		fmt.Fprintf(stderr, "gopin: %v\n", err)
		return 2
	}
	// A sweep with an empty corpus reports "nothing to do" exactly like one
	// that read everything.
	if len(repos) == 0 {
		fmt.Fprintln(stderr, "gopin: the list is empty — that is a failure, not a clean fleet")
		return 1
	}

	var changed, skipped, failed, opened int
	for _, repo := range repos {
		f := inspect(repo, *version)
		switch {
		case f.Err != "":
			failed++
			fmt.Fprintf(stdout, "ERROR   %s: %s\n", repo, f.Err)
			continue
		case f.Aliases == 0:
			skipped++
			if len(f.Literals) > 0 {
				fmt.Fprintf(stdout, "pinned  %s (%s)\n", repo, strings.Join(f.Literals, " "))
			}
			continue
		}
		changed++
		fmt.Fprintf(stdout, "ALIAS   %s: %d in %s", repo, f.Aliases, strings.Join(f.Files, " "))
		if olderGoMod(f.GoMod, target) {
			fmt.Fprintf(stdout, " ; go.mod %s -> %s", f.GoMod, *version)
		}
		if len(f.Literals) > 0 {
			fmt.Fprintf(stdout, " ; leaving %s alone", strings.Join(f.Literals, " "))
		}
		fmt.Fprintln(stdout)
		if *apply {
			if err := open(repo, f, *version); err != nil {
				failed++
				fmt.Fprintf(stdout, "        pull request FAILED: %v\n", err)
			} else {
				opened++
			}
			time.Sleep(*pause)
		}
		if *limit > 0 && changed >= *limit {
			fmt.Fprintf(stdout, "stopping at -limit %d\n", *limit)
			break
		}
	}
	fmt.Fprintf(stdout, "\n%d read · %d hold the alias · %d already explicit · %d unreadable", len(repos), changed, skipped, failed)
	if *apply {
		fmt.Fprintf(stdout, " · %d pull request(s) opened", opened)
	}
	fmt.Fprintln(stdout)
	if failed > 0 {
		return 1
	}
	return 0
}

// readList reads org/repo lines from a file or stdin.
func readList(path string) ([]string, error) {
	var b []byte
	var err error
	if path == "" {
		b, err = io.ReadAll(os.Stdin)
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	return readListFrom(string(b))
}

// ghJSON is a seam over fleet.GH so the inspection is testable.
//
// It names the call in the error. "gh: Not Found (HTTP 404)" from a sweep
// over 258 repositories says nothing about WHICH of eight calls failed, and
// the first pilot run cost a round of manual bisection to find out.
var ghJSON = func(args ...string) ([]byte, error) {
	out, err := fleet.GH(args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// inspect reads a repository's default branch and says what it needs.
func inspect(repo, want string) finding {
	f := finding{Repo: repo}
	names, err := workflowNames(repo)
	if err != nil {
		f.Err = err.Error()
		return f
	}
	lits := map[string]bool{}
	for _, n := range names {
		body, err := getFile(repo, ".github/workflows/"+n)
		if err != nil {
			f.Err = err.Error()
			return f
		}
		if m := reStable.FindAllString(body, -1); len(m) > 0 {
			f.Files = append(f.Files, n)
			f.Aliases += len(m)
		}
		for _, m := range reLiteral.FindAllStringSubmatch(body, -1) {
			lits[m[1]] = true
		}
	}
	for v := range lits {
		f.Literals = append(f.Literals, v)
	}
	sort.Strings(f.Literals)
	if body, err := getFile(repo, "go.mod"); err == nil {
		if m := reGoDirective.FindStringSubmatch(body); m != nil {
			f.GoMod = m[1]
		}
	}
	return f
}

// workflowNames lists the repository's workflow files. A repository with no
// workflows directory is not an error: it simply has nothing to pin.
func workflowNames(repo string) ([]string, error) {
	b, err := ghJSON("api", "repos/"+repo+"/contents/.github/workflows", "--jq", ".[].name")
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "Not Found") {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l = strings.TrimSpace(l); strings.HasSuffix(l, ".yml") || strings.HasSuffix(l, ".yaml") {
			out = append(out, l)
		}
	}
	return out, nil
}

// getFile returns a file's contents from the default branch.
func getFile(repo, path string) (string, error) {
	b, err := ghJSON("api", "repos/"+repo+"/contents/"+path, "--jq", ".content")
	if err != nil {
		return "", err
	}
	dec, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.TrimSpace(string(b)), "\n", ""))
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return string(dec), nil
}

// open clones the repository, edits it, pushes with gitpush and opens the
// pull request.
//
// NOT the contents API, and the reason is worth writing down because the
// first version of this tool used it and failed on the first real repository.
// GitHub's create-or-update-file endpoint REFUSES a path under
// .github/workflows/ with a flat 404 — measured directly, on the same branch,
// with the same token, in the same repository where a PUT to go.mod
// succeeded seconds earlier:
//
//	PUT repos/O/R/contents/go.mod                      -> 201, commit created
//	PUT repos/O/R/contents/.github/workflows/ci.yml    -> 404 Not Found
//
// It is not a missing scope: `ghscopes workflow` says both tokens on this
// machine carry it. It is the OAuth-app path that gh authenticates through,
// and no scope lifts it. Workflow files go in over git or they do not go in.
//
// The push is gitpush, never `git push`: it names a credential helper instead
// of reading a token, refuses a remote URL that carries a credential, and
// redacts what it prints. A token must never reach a URL, a command line or
// an environment variable.
func open(repo string, f finding, want string) error {
	base, err := defaultBranch(repo)
	if err != nil {
		return err
	}
	dir, err := osMkdirTemp("", "gopin-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	// Shallow, single-branch: this needs one commit's worth of tree and
	// nothing else, and 258 full clones would be minutes of nothing.
	if out, err := git("", "clone", "--depth", "1", "--single-branch",
		"--branch", base, "https://github.com/"+repo+".git", dir); err != nil {
		return fmt.Errorf("clone: %v: %s", err, out)
	}
	branch := "go-" + strings.ReplaceAll(want, ".", "") + "-pinned-because-an-alias-cannot-be-reviewed"
	if out, err := git(dir, "checkout", "-b", branch); err != nil {
		return fmt.Errorf("branch: %v: %s", err, out)
	}

	changed := 0
	for _, n := range f.Files {
		path := filepath.Join(dir, ".github", "workflows", n)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		next := reStable.ReplaceAllString(string(b), "${1}'"+want+"'${2}")
		if next == string(b) {
			continue
		}
		if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
			return err
		}
		changed++
	}
	if olderGoMod(f.GoMod, mustParse(want)) {
		path := filepath.Join(dir, "go.mod")
		if b, err := os.ReadFile(path); err == nil {
			next := reGoDirective.ReplaceAllString(string(b), "go "+want)
			if next != string(b) {
				if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
					return err
				}
				changed++
			}
		}
	}
	// Nothing to say is not a failure, but it must not become an empty pull
	// request either.
	if changed == 0 {
		return nil
	}

	if out, err := git(dir, "commit", "-aqm", commitMessage(f, want)); err != nil {
		return fmt.Errorf("commit: %v: %s", err, out)
	}
	if out, err := push(dir, branch); err != nil {
		return fmt.Errorf("gitpush: %v: %s", err, out)
	}
	bodyFile, err := osCreateTempFile(prBody(f, want))
	if err != nil {
		return err
	}
	defer os.Remove(bodyFile)
	_, err = ghJSON("pr", "create", "--repo", repo, "--base", base, "--head", branch,
		"--title", "ci: pin Go "+want+" instead of `stable`", "--body-file", bodyFile)
	return err
}

// git runs git in dir (or anywhere, for a clone) and returns its combined
// output for the error message.
var git = func(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// push is gitpush, deliberately not git. See open's comment.
var push = func(dir, branch string) (string, error) {
	cmd := exec.Command("gitpush", "-u", "origin", branch)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

var osMkdirTemp = os.MkdirTemp

// osCreateTempFile writes body to a temp file and returns its path, because
// `gh pr create --body` on a command line mangles a long markdown body and
// --body-file does not.
var osCreateTempFile = func(body string) (string, error) {
	f, err := os.CreateTemp("", "gopin-body-*.md")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		return "", err
	}
	return f.Name(), f.Close()
}

// defaultBranch is the branch a pull request targets and the one to clone.
//
// It asks for the NAME only. An earlier version also fetched the ref's sha,
// which the contents-API design needed to create a branch server-side; the
// git design does not, and that was 258 calls against a REST budget shared
// with Renovate's scheduled walk.
func defaultBranch(repo string) (string, error) {
	b, err := ghJSON("api", "repos/"+repo, "--jq", ".default_branch")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// readListFrom is readList's parser, split out so the refusals are testable
// without a file.
func readListFrom(s string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if i := strings.IndexByte(l, '#'); i >= 0 {
			l = l[:i]
		}
		l = strings.TrimSpace(l)
		if l == "" || seen[l] {
			continue
		}
		if strings.Count(l, "/") != 1 {
			return nil, fmt.Errorf("%q is not org/repo", l)
		}
		seen[l] = true
		out = append(out, l)
	}
	return out, nil
}
