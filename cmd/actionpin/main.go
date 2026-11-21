// Command actionpin refuses a GitHub Actions `uses:` that is not pinned by full
// commit SHA.
//
// # WHY A TAG IS NOT A PIN
//
// `uses: actions/checkout@v7` names a movable reference. Whoever can move that
// tag, or whoever compromises the account that can, runs new code in the
// workflow with its token: read access to the repository, and write scope in a
// release or Pages job. A 40-hex commit SHA cannot be moved. The tag stays in a
// trailing comment (`uses: actions/checkout@<sha> # v7`) so that a reader, and
// Renovate's helpers:pinGitHubActionDigests, can tell which release it is.
//
// # WHAT IT CHECKS
//
// Every `uses:` in .github/workflows/*.yml|yaml and in .github/actions/*/action.yml|yaml:
//
//   - a local reference (`./path`) is fine: it is this repository's own code;
//   - `docker://image@sha256:<64 hex>` is fine, `docker://image:tag` is not;
//   - anything else must be `owner/repo[/path]@<40 hex>` followed by a `# comment`
//     naming the tag. A SHA with no comment is refused too, because the comment is
//     what a bump rewrites; without it the pin is unreadable and unmaintainable.
//
// # WHAT IT REFUSES TO REPORT AS PASSING
//
// A directory with no workflow file is an error, not a pass: a scan that read
// nothing looks exactly like a clean repository. It reads files, never the
// network, so it needs no token and cannot tell whether a SHA exists.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var osExit = os.Exit

// reUses matches a `uses:` key at the start of a line, with an optional list
// dash. Anchoring at the start is what keeps a comment that merely quotes
// `uses:` from being read as a step.
var reUses = regexp.MustCompile(`^\s*(?:-\s*)?uses:\s*(.*)$`)

var (
	reSHA    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	reDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Finding is one refused `uses:`.
type Finding struct {
	File   string
	Line   int
	Uses   string
	Reason string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", f.File, f.Line, f.Uses, f.Reason)
}

// judge classifies one `uses:` value (the text after the key) and returns a
// reason, or "" if it is acceptable.
func judge(rest string) (uses, reason string) {
	value, comment, _ := strings.Cut(rest, " #")
	hasComment := strings.Contains(rest, " #") && strings.TrimSpace(comment) != ""
	uses = strings.Trim(strings.TrimSpace(value), `"'`)
	switch {
	case uses == "":
		return uses, "empty uses"
	case strings.HasPrefix(uses, "./") || uses == ".":
		return uses, ""
	case strings.HasPrefix(uses, "docker://"):
		_, digest, ok := strings.Cut(uses, "@")
		if !ok || !reDigest.MatchString(digest) {
			return uses, "docker image is not pinned by @sha256:<64 hex> digest"
		}
		return uses, ""
	}
	_, ref, ok := strings.Cut(uses, "@")
	if !ok {
		return uses, "no @ref: pin it by full commit SHA"
	}
	if !reSHA.MatchString(ref) {
		return uses, fmt.Sprintf("ref %q is not a full 40-hex commit SHA", ref)
	}
	if !hasComment {
		return uses, "SHA has no trailing `# tag` comment"
	}
	return uses, ""
}

// scan reads one file's text.
func scan(name, text string) []Finding {
	var out []Finding
	for i, line := range strings.Split(text, "\n") {
		m := reUses.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		uses, reason := judge(strings.TrimRight(m[1], " \t\r"))
		if reason != "" {
			out = append(out, Finding{File: name, Line: i + 1, Uses: uses, Reason: reason})
		}
	}
	return out
}

// workflowFiles lists the YAML files under dir/.github that hold `uses:`.
func workflowFiles(dir string) ([]string, error) {
	var files []string
	for _, pat := range []string{
		".github/workflows/*.yml", ".github/workflows/*.yaml",
		".github/actions/*/action.yml", ".github/actions/*/action.yaml",
	} {
		m, err := filepath.Glob(filepath.Join(dir, filepath.FromSlash(pat)))
		if err != nil {
			return nil, err
		}
		files = append(files, m...)
	}
	sort.Strings(files)
	return files, nil
}

// Run scans every directory and returns the findings. It is an error if a
// directory holds no workflow file at all.
func Run(dirs []string) ([]Finding, error) {
	var all []Finding
	for _, dir := range dirs {
		files, err := workflowFiles(dir)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("%s: no workflow file under .github/workflows: a scan that read nothing is not a pass", dir)
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			all = append(all, scan(f, string(b))...)
		}
	}
	return all, nil
}

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "actionpin [DIR...]",
		Short:         "Refuse a GitHub Actions `uses:` that is not pinned by full commit SHA",
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				args = []string{"."}
			}
			found, err := Run(args)
			if err != nil {
				return err
			}
			return report(cmd.OutOrStdout(), found)
		},
	}
	return cmd
}

type refusal struct{ n int }

func (r refusal) Error() string {
	return fmt.Sprintf("%d `uses:` not pinned by full commit SHA", r.n)
}

func report(w io.Writer, found []Finding) error {
	for _, f := range found {
		fmt.Fprintln(w, f)
	}
	if len(found) > 0 {
		return refusal{len(found)}
	}
	fmt.Fprintln(w, "ok: every uses: is pinned by full commit SHA")
	return nil
}

func main() {
	cmd := RootCmd()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "actionpin:", err)
		osExit(1)
	}
}
