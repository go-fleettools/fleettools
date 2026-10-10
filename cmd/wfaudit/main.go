// wfaudit reports GitHub Actions workflows that let somebody else's pull
// request reach this repository's secrets or write token.
//
//	wfaudit DIR...        # each DIR a checkout, or a directory of checkouts
//	wfaudit -api DIR...   # also ask GitHub what a job with no permissions gets
//	wfaudit -fail DIR...  # exit 1 if anything at MEDIUM or above is found
//
// Four rules, after GitHub Security Lab's "Keeping your GitHub Actions and
// workflows secure" series and the audits of the same names in zizmor:
//
//   - dangerous-trigger: pull_request_target or workflow_run, which run with
//     the base repository's secrets and a write token for pull requests from
//     forks. HIGH when the workflow also checks out the pull request's code
//     (the "pwn request"), MEDIUM otherwise.
//   - excessive-permissions: write-all anywhere (HIGH), or write granted at
//     the workflow level, where every job inherits it (MEDIUM).
//   - artipacked: actions/checkout keeps its token in .git/config unless
//     persist-credentials is false, and a job that then uploads the whole
//     workspace as an artifact publishes that token (HIGH).
//   - secrets-under-privileged-trigger: a secret read in a workflow with a
//     privileged trigger (MEDIUM).
//
// Template injection -- an untrusted ${{ github.event... }} expanded inside
// run: -- is NOT checked here: actionlint already reports it, and two tools
// checking one thing disagree the day one of them is fixed.
//
// Written 2026-10-10, after an audit of go-widgets' 33 workflows had been
// attempted with grep in zsh: an unquoted $W was not word-split, grep read all
// 33 paths as one file name, and every section of the report listed every
// workflow. A tool that parses YAML cannot fail that way, and its tests say
// what each rule must and must not match.
//
// It reads working trees; it makes no API call unless -api is given, and then
// one per repository.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

func main() {
	fleet.WarnIfStale(os.Stderr)
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, defaultTokenPermission))
}

func run(args []string, stdout, stderr io.Writer, tokenPerm func(dir string) (string, error)) int {
	fs := flag.NewFlagSet("wfaudit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	api := fs.Bool("api", false, "ask GitHub each repository's default token permission (one call per repository)")
	failOn := fs.Bool("fail", false, "exit 1 when a MEDIUM or HIGH finding is reported")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "usage: wfaudit [-api] [-fail] DIR...")
		return 2
	}

	repos, err := findRepos(fs.Args())
	if err != nil {
		fmt.Fprintln(stderr, "wfaudit:", err)
		return 2
	}
	if len(repos) == 0 {
		// A scan that read nothing must not look like a clean bill.
		fmt.Fprintln(stderr, "wfaudit: no .github/workflows found under", strings.Join(fs.Args(), " "))
		return 2
	}

	var all []Finding
	files, unreadable := 0, 0
	for _, repo := range repos {
		wfs, _ := filepath.Glob(filepath.Join(repo, ".github", "workflows", "*.y*ml"))
		sort.Strings(wfs)
		var repoFindings []Finding
		for _, wf := range wfs {
			src, err := os.ReadFile(wf)
			if err != nil {
				fmt.Fprintln(stderr, "wfaudit:", err)
				unreadable++
				continue
			}
			rel, _ := filepath.Rel(filepath.Dir(repo), wf)
			found, err := Audit(rel, src)
			if err != nil {
				fmt.Fprintln(stderr, "wfaudit:", err)
				unreadable++
				continue
			}
			files++
			repoFindings = append(repoFindings, found...)
		}
		if *api {
			repoFindings = resolveDefaults(repo, repoFindings, tokenPerm, stderr)
		}
		all = append(all, repoFindings...)
	}

	counts := map[Severity]int{}
	for _, f := range all {
		fmt.Fprintln(stdout, f)
		counts[f.Severity]++
	}
	fmt.Fprintf(stdout, "\n%d repositories, %d workflows read, %d unreadable: %d HIGH, %d MEDIUM, %d INFO\n",
		len(repos), files, unreadable, counts[High], counts[Medium], counts[Info])
	if unreadable > 0 {
		return 2
	}
	if *failOn && counts[High]+counts[Medium] > 0 {
		return 1
	}
	return 0
}

// resolveDefaults replaces the no-permissions INFO, which can only say "it
// depends", with what the repository's setting makes of it.
func resolveDefaults(repo string, fs []Finding, tokenPerm func(string) (string, error), stderr io.Writer) []Finding {
	has := false
	for _, f := range fs {
		if f.Rule == "no-permissions" {
			has = true
			break
		}
	}
	if !has {
		return fs
	}
	perm, err := tokenPerm(repo)
	if err != nil {
		fmt.Fprintf(stderr, "wfaudit: %s: default token permission unknown: %v\n", repo, err)
		return fs
	}
	out := fs[:0]
	for _, f := range fs {
		if f.Rule != "no-permissions" {
			out = append(out, f)
			continue
		}
		if perm == "write" {
			f.Severity = Medium
			f.Message = "jobs declare no permissions and the repository's default token is READ-WRITE: declare permissions: contents: read at the top, and grant more on the job that needs it"
			out = append(out, f)
		}
		// "read": the default is the narrow one, and the finding has nothing
		// left to say.
	}
	return out
}

// findRepos returns every directory, among args and their children, that has
// a .github/workflows directory.
func findRepos(args []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	addIf := func(d string) bool {
		fi, err := os.Stat(filepath.Join(d, ".github", "workflows"))
		if err == nil && fi.IsDir() && !seen[d] {
			seen[d] = true
			out = append(out, d)
			return true
		}
		return false
	}
	for _, a := range args {
		fi, err := os.Stat(a)
		if err != nil {
			return nil, err
		}
		if !fi.IsDir() {
			return nil, fmt.Errorf("%s is not a directory", a)
		}
		if addIf(a) {
			continue
		}
		kids, err := os.ReadDir(a)
		if err != nil {
			return nil, err
		}
		for _, k := range kids {
			if k.IsDir() {
				addIf(filepath.Join(a, k.Name()))
			}
		}
	}
	return out, nil
}

// defaultTokenPermission asks GitHub what GITHUB_TOKEN gets in a job that
// declares nothing: "read" or "write".
func defaultTokenPermission(dir string) (string, error) {
	slug, err := originSlug(dir)
	if err != nil {
		return "", err
	}
	b, err := fleet.GH("api", "repos/"+slug+"/actions/permissions/workflow")
	if err != nil {
		return "", err
	}
	var v struct {
		Default string `json:"default_workflow_permissions"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return "", err
	}
	if v.Default != "read" && v.Default != "write" {
		return "", fmt.Errorf("unexpected answer %q", v.Default)
	}
	return v.Default, nil
}

// originSlug reads owner/repo from the checkout's origin remote.
func originSlug(dir string) (string, error) {
	b, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", fmt.Errorf("no origin remote: %w", err)
	}
	return slugFromURL(strings.TrimSpace(string(b)))
}

func slugFromURL(u string) (string, error) {
	u = strings.TrimSuffix(u, ".git")
	for _, p := range []string{"https://github.com/", "git@github.com:", "ssh://git@github.com/"} {
		if strings.HasPrefix(u, p) {
			s := strings.TrimPrefix(u, p)
			if strings.Count(s, "/") == 1 {
				return s, nil
			}
		}
	}
	// The URL itself is never printed: a remote can carry a credential
	// (https://x:TOKEN@github.com/...), and an error message is an output
	// stream like any other.
	return "", fmt.Errorf("origin is not a plain github.com URL")
}
