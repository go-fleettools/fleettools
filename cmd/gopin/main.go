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
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
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
var ghJSON = func(args ...string) ([]byte, error) { return fleet.GH(args...) }

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

// open creates the branch, writes each file and opens the pull request.
//
// The contents API is used rather than the git data API: it costs one call per
// file instead of five or six per repository, and two commits in a pull
// request read better here than one — each says what it changed and why.
func open(repo string, f finding, want string) error {
	base, sha, err := defaultBranch(repo)
	if err != nil {
		return err
	}
	branch := "go-" + strings.ReplaceAll(want, ".", "") + "-pinned-because-an-alias-cannot-be-reviewed"
	if _, err := ghJSON("api", "-X", "POST", "repos/"+repo+"/git/refs",
		"-f", "ref=refs/heads/"+branch, "-f", "sha="+sha); err != nil &&
		!strings.Contains(err.Error(), "Reference already exists") {
		return err
	}
	for _, n := range f.Files {
		path := ".github/workflows/" + n
		body, fsha, err := getFileOn(repo, path, branch)
		if err != nil {
			return err
		}
		next := reStable.ReplaceAllString(body, "${1}'"+want+"'${2}")
		if next == body {
			continue
		}
		if err := putFile(repo, path, branch, fsha, next,
			"ci: pin Go "+want+" instead of `stable`"); err != nil {
			return err
		}
	}
	if cur, err := parseGoVersion(f.GoMod); f.GoMod != "" && err == nil && cur.olderThan(mustParse(want)) {
		body, fsha, err := getFileOn(repo, "go.mod", branch)
		if err != nil {
			return err
		}
		next := reGoDirective.ReplaceAllString(body, "go "+want)
		if next != body {
			if err := putFile(repo, "go.mod", branch, fsha, next,
				"go.mod: say the version CI has been using"); err != nil {
				return err
			}
		}
	}
	_, err = ghJSON("api", "-X", "POST", "repos/"+repo+"/pulls",
		"-f", "title=ci: pin Go "+want+" instead of `stable`",
		"-f", "head="+branch, "-f", "base="+base,
		"-f", "body="+prBody(f, want))
	return err
}

func defaultBranch(repo string) (string, string, error) {
	b, err := ghJSON("api", "repos/"+repo, "--jq", ".default_branch")
	if err != nil {
		return "", "", err
	}
	base := strings.TrimSpace(string(b))
	s, err := ghJSON("api", "repos/"+repo+"/git/ref/heads/"+base, "--jq", ".object.sha")
	if err != nil {
		return "", "", err
	}
	return base, strings.TrimSpace(string(s)), nil
}

func getFileOn(repo, path, ref string) (string, string, error) {
	b, err := ghJSON("api", "repos/"+repo+"/contents/"+path+"?ref="+ref, "--jq", ".content + \"\\n\" + .sha")
	if err != nil {
		return "", "", err
	}
	parts := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("%s: unexpected contents response", path)
	}
	sha := parts[len(parts)-1]
	dec, err := base64.StdEncoding.DecodeString(strings.Join(parts[:len(parts)-1], ""))
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", path, err)
	}
	return string(dec), sha, nil
}

func putFile(repo, path, branch, sha, body, msg string) error {
	payload := map[string]string{
		"message": msg + "\n\nCo-Authored-By: Claude Opus 5 <noreply@anthropic.com>",
		"content": base64.StdEncoding.EncodeToString([]byte(body)),
		"branch":  branch,
		"sha":     sha,
	}
	j, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "gopin-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(j); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	_, err = ghJSON("api", "-X", "PUT", "repos/"+repo+"/contents/"+path, "--input", tmp.Name())
	return err
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
