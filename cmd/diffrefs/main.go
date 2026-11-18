// Command diffrefs reads the issue and pull-request references a DIFF ADDS,
// and refuses one that does not exist yet.
//
//	git diff --cached | diffrefs --repo owner/name -
//	diffrefs --repo owner/name <patch>
//
// ⛔ IT EXISTS BECAUSE EVERY OTHER CHECK HERE READS A PUBLISHED BODY. `prbody`
// reads a pull request's body, and the controls written into the lesson
// never-guess-a-pr-number read a body and its comments. None of them reads
// COMMITTED PROSE — a README, a doc comment, a test comment, a commit message —
// and that is where the mistake recurred on 2026-11-17 (go-pdfkit/reader):
// "#68" was written into about fifteen places, all deduced from "the last one
// was #67", before the pull request existed. `gh pr create` then returned #68
// and the reference was RIGHT.
//
// ⛔⛔ THAT IS THE DANGEROUS FORM, NOT THE BENIGN ONE. Nothing could catch it:
// the number was correct, so a human reading the diff saw nothing, and the
// error is visible only in the ORDER of operations — prose committed before the
// artifact it cites exists. The earlier advice, "capture the number `gh pr
// create` returned", cannot apply here by construction: the prose is committed
// BEFORE the push, so before the pull request exists at all.
//
// So the question this asks is not "is this number plausible" but "does it
// exist RIGHT NOW, before the commit lands". A reference to something that does
// not exist yet is a bet, whether or not it comes in.
//
// ⚠ AND IT IS HONEST ABOUT WHAT IT CANNOT DO. A guessed number almost always
// lands on something that DOES exist — the recorded cases hit a Renovate pull
// request, a peer's pull request, an issue long closed — so existence alone
// never proves a reference right. Every resolved reference is printed WITH ITS
// TITLE, for a person to compare against what the sentence claims to cite.
// That half is a report. The refusal is the gate.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/go-fleettools/fleettools/internal/ghtoken"
	"github.com/spf13/cobra"
)

const longHelp = `Reads the issue/pull-request references a diff ADDS and refuses one that does
not exist yet.

  git diff --cached | diffrefs --repo owner/name -
  diffrefs --repo owner/name changes.patch

Only ADDED lines are read (` + "`+`" + ` in a unified diff, never ` + "`+++`" + `), because a
reference already in the file is somebody else's problem and was checked when it
was written. Both spellings are found: ` + "`#123`" + ` means --repo, and
` + "`owner/name#123`" + ` means that repository.

⛔ WHAT IT REFUSES: a reference that does not resolve. That is the shape of the
mistake it is for — prose committed before the artifact it cites exists, which
no check that reads a published body can see.

⚠ WHAT IT CANNOT DO: tell a right number from a wrong one that exists. A guessed
number almost always lands on something real. Every resolved reference is
printed with its TITLE so a person can compare it against what the sentence
claims; that half is a report, not a verdict.

A number below --min-number (default 4) is ignored: ` + "`#1`" + `, ` + "`#2`" + ` and ` + "`#3`" + ` are
TeX macro parameters, C preprocessor stringification and shell positional
parameters far more often than they are references. ⚠ That is a THRESHOLD, not
a proof: a real ` + "`#3`" + ` exists in young repositories and this will not see it.

The token is read only from a file (default ~/.github-token) and sent only in
the Authorization header.

Exit status: 0 every reference resolved (or there were none to read), 1 at least
one did not, 2 nothing could be read — an unreadable diff or token, or an API
that would not answer, because a check that read nothing must not look like a
clean one.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	var repo, tokFile, base string
	var minNumber int
	c := cli.New("diffrefs <patch>|-",
		"Refuse an issue/PR reference a diff adds that does not exist yet", longHelp)
	c.Flags().StringVar(&repo, "repo", "", "owner/name a bare #N refers to (required)")
	c.Flags().StringVar(&tokFile, "token-file", "", "file holding the token (default ~/.github-token)")
	c.Flags().StringVar(&base, "base", "https://api.github.com", "API base URL (https)")
	c.Flags().IntVar(&minNumber, "min-number", 4,
		"ignore #N below this: #1, #2 and #3 are macro and shell parameters far more often than references")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 {
			return cli.Unanswerable("usage: diffrefs --repo owner/name <patch>|-")
		}
		if repo == "" {
			return cli.Unanswerable("--repo owner/name is required: a bare #N has no meaning without it")
		}
		return cli.FromStatus(run(args[0], repo, tokFile, base, minNumber, cli.Out(cmd), cli.Err(cmd)))
	}
	return c
}

// Ref is one reference found in added lines.
type Ref struct {
	Repo   string // owner/name, the --repo default filled in
	Number int
	Line   int // the line of the diff it was found on
}

func (r Ref) String() string { return fmt.Sprintf("%s#%d", r.Repo, r.Number) }

func run(path, repo, tokFile, base string, minNumber int, stdout, stderr io.Writer) int {
	src, err := readInput(path)
	if err != nil {
		fmt.Fprintf(stderr, "diffrefs: %v\n", err)
		return 2
	}
	added, refs := Added(string(src), repo, minNumber)
	if added == 0 {
		fmt.Fprintln(stderr, "diffrefs: the diff adds no lines; nothing was read")
		return 2
	}
	fmt.Fprintf(stdout, "%d added line(s) read, %d reference(s)\n", added, len(refs))
	if len(refs) == 0 {
		return 0
	}
	if !strings.HasPrefix(base, "https://") && !isTestBase(base) {
		fmt.Fprintf(stderr, "diffrefs: base %q: must be https\n", base)
		return 2
	}
	tok, err := ghtoken.Read(tokFile)
	if err != nil {
		fmt.Fprintf(stderr, "diffrefs: %v\n", err)
		return 2
	}
	c := &http.Client{Timeout: 30 * time.Second}

	missing := 0
	for _, r := range refs {
		title, err := lookup(c, base, tok, r)
		switch {
		case errors.Is(err, errNotFound):
			missing++
			fmt.Fprintf(stdout, "⛔ %s does not exist (diff line %d): a reference committed before the thing it cites is a bet, whether or not it comes in\n", r, r.Line)
		case err != nil:
			fmt.Fprintf(stderr, "diffrefs: %s: %v\n", r, err)
			return 2
		default:
			fmt.Fprintf(stdout, "·  %s %s\n", r, title)
		}
	}
	if missing > 0 {
		fmt.Fprintf(stdout, "%d reference(s) do not exist yet\n", missing)
		return 1
	}
	fmt.Fprintln(stdout, "every reference resolves — read the titles above against what the prose claims to cite")
	return 0
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(io.LimitReader(os.Stdin, 32<<20))
	}
	return os.ReadFile(path)
}

// reRef matches "#123" and "owner/name#123". The repository part is optional
// and deliberately NOT guarded by a left word boundary on the "#": the form
// go-tex/math#27 was invisible to an earlier control whose lookbehind was
// blocked by the "h" before the "#".
var reRef = regexp.MustCompile(`(?:([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+))?#(\d+)`)

// Added returns how many added lines were read and the distinct references in
// them, in the order they first appear.
func Added(diff, repo string, minNumber int) (int, []Ref) {
	var refs []Ref
	seen := map[string]bool{}
	added := 0
	for i, line := range strings.Split(diff, "\n") {
		// "+++ b/file" is a header, not an added line. "+" alone is an added
		// blank line and is counted.
		if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") {
			continue
		}
		added++
		for _, m := range reRef.FindAllStringSubmatch(line[1:], -1) {
			n := 0
			fmt.Sscanf(m[2], "%d", &n)
			if n < minNumber {
				continue
			}
			r := Ref{Repo: m[1], Number: n, Line: i + 1}
			if r.Repo == "" {
				r.Repo = repo
			}
			if key := r.String(); !seen[key] {
				seen[key] = true
				refs = append(refs, r)
			}
		}
	}
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].Repo != refs[j].Repo {
			return refs[i].Repo < refs[j].Repo
		}
		return refs[i].Number < refs[j].Number
	})
	return added, refs
}

var errNotFound = errors.New("not found")

// lookup asks for the issue, which GitHub answers for a pull request too.
func lookup(c *http.Client, base, tok string, r Ref) (string, error) {
	parts := strings.SplitN(r.Repo, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("%q is not owner/name", r.Repo)
	}
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d", strings.TrimRight(base, "/"), parts[0], parts[1], r.Number)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusNotFound {
		return "", errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", resp.Status, firstLineOf(string(b)))
	}
	var got struct {
		Title string `json:"title"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		return "", err
	}
	// ⛔ An empty title is not an answer. A reply read as a success with
	// nothing in it is how a check comes to report a reference it never saw.
	if got.Title == "" {
		return "", errors.New("the reply carried no title, so nothing says what this reference is")
	}
	return fmt.Sprintf("[%s] %s", got.State, got.Title), nil
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return strings.TrimSpace(s)
}

func isTestBase(base string) bool {
	return strings.HasPrefix(base, "http://127.0.0.1:") || strings.HasPrefix(base, "http://[::1]:")
}
