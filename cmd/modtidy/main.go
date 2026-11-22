// Command modtidy refuses a push from a clone where one of the go.mod files is
// not tidy, however deep it sits.
//
// ⛔ `go mod tidy` AT THE ROOT DOES NOT DESCEND INTO A DIRECTORY THAT HAS ITS OWN
// go.mod. Three recorded occurrences (memory a-repository-can-hold-two-modules):
// go-widgets/toolkit twice (the rougelex/ sub-module), and
// go-numerics/integrate#34 on 2026-11-14 (bench/ is its own module). Each time
// the root was tidied and tested, green, and the CI went red with
// "updates to go.mod needed". The note existed; re-reading it was not part of
// the gesture, so the check runs instead.
//
// Every go.mod under the directory is checked with
// `go mod tidy -diff`, GOTOOLCHAIN=local and GOWORK=off: the CI's constraint.
// With the default GOTOOLCHAIN=auto a local run fetches whatever toolchain the
// directive asks for and hides the problem; a go.work file would let a sibling
// module satisfy a requirement the CI does not see.
//
// ⛔ A MODULE THAT COULD NOT BE READ IS NEVER REPORTED CLEAN. The command
// failing for any reason other than "there is a diff" (a missing toolchain, a
// broken go.mod, no network) is UNREAD, and UNREAD outranks untidy.
//
// ⛔⛔ AND A NON-VERDICT IS NAMED, NEVER GUESSED. Two of them exist, both
// measured on 2026-11-15:
//
//  1. A module with NO GO SOURCE. `go mod tidy` can then only REMOVE
//     requirements, so its verdict carries no information -- and the remedy it
//     prescribes destroys the repository. go-fft/docs is a Hugo site whose only
//     requirement is a theme; tidying it deletes the theme from go.mod AND
//     go.sum, and the hook's own text told the next session to commit that
//     (#294). Such a module is `skipped`, with the reason named. The Hugo
//     config is reported, never what decides: that site has no [module] block,
//     so a rule keyed on one would miss the occurrence it was written for. And
//     the tools-file trick the landings use CANNOT be applied -- a Hugo theme
//     carries a go.mod but no Go package, so no import can hold it.
//  2. A DIRECTIVE FLOOR ABOVE THE LOCAL TOOLCHAIN. GOTOOLCHAIN=local on a host
//     whose installed Go is older than a module's `go` line cannot build it at
//     all, and in the hook UNREAD means allow -- so the gate went silently off
//     for every repository Renovate raised to go 1.27.4, this one included
//     (#300). The check retries once with the toolchain the directive NAMES.
//
// Exit status: 0 every module is tidy, 1 a module is untidy, 2 a module could
// not be read (or no module was found). A skipped module is none of those: it
// is printed, counted, and does not decide the status.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
	"golang.org/x/mod/modfile"
)

const longHelp = `Checks that every go.mod of a clone is tidy, nested modules included.

  modtidy [dir]       check every go.mod under dir (default: the current directory)
  modtidy --hook      answer a PreToolUse payload read on stdin

go mod tidy at the root never enters a directory with its own go.mod, so a
bench/, example/ or rougelex/ module goes stale unseen and the CI fails
with "updates to go.mod needed". Each module is judged with
GOTOOLCHAIN=local GOWORK=off go mod tidy -diff, which is what the CI sees.
vendor/, testdata/ and directories starting with "." or "_" are skipped.

Each module is reported tidy, untidy (with the diff), UNREAD (the command failed
for another reason: broken go.mod, no network) or skipped. UNREAD is never
clean.

A module with no .go file of its own is skipped, with the reason named: tidy can
only REMOVE requirements there, so it has nothing to say, and running it would
delete them -- a Hugo site whose go.mod pins only a theme loses its theme. A
nested module's sources never answer for its parent.

When a module's go directive asks for a newer Go than the installed one, the
check retries with the toolchain the directive NAMES and says which it used.
That is what the CI runner has; GOTOOLCHAIN=auto would leave the version
unstated.

--hook is a PreToolUse hook for the Bash tool. It acts only when a command
position of the line is gitpush or git push (words in prose, quotes and heredocs
do not count), takes the clone from the payload's cwd and any -C or cd, and
denies the push when a module is untidy, naming the module and the fix. It
fails open on anything it cannot read. A waiver states its reason:

  MODTIDY_WAIVE="<reason>" gitpush ...

allows the push and prints the reason. Command: ~/.local/bin/modtidy --hook

Exit status: 0 all tidy, 1 a module is untidy, 2 a module could not be read.
Under --hook the status is always 0; the verdict is the JSON on stdout.`

func main() {
	if hookMode(os.Args[1:]) {
		driveHook(os.Args[1:], os.Stdout, os.Stderr)
		return
	}
	// Not in hook mode: the staleness note is a network call, and a PreToolUse
	// hook runs before tool calls.
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// hookMode reads the raw argv, because the answer must be known BEFORE cobra
// parses anything: a malformed flag exits 2, which a PreToolUse hook reads as a
// block. An unparseable --hook=value still means "hook"; only an explicit false
// does not.
func hookMode(args []string) bool {
	for _, a := range args {
		name, value, _ := strings.Cut(a, "=")
		if name != "--hook" && name != "-hook" {
			continue
		}
		// A bare --hook has no value, and ParseBool("") fails: that is "hook" too.
		if on, err := strconv.ParseBool(value); err != nil || on {
			return true
		}
	}
	return false
}

// driveHook leaves the process with status 0 whatever happens: a failure of the
// command itself becomes an allow, never a status the protocol reads as a block.
func driveHook(args []string, stdout, stderr io.Writer) {
	if code := cli.Drive(RootCmd(), args, stdout, stderr); code != 0 {
		fmt.Fprintf(stderr, "modtidy: allowing: the command itself failed (status %d)\n", code)
		fmt.Fprintln(stdout, "{}")
	}
}

// stdin is the seam the tests feed the hook payload through.
var stdin io.Reader = os.Stdin

// tidyRun runs the CI's tidy check in one module directory, under the named
// GOTOOLCHAIN.
type tidyRun func(ctx context.Context, dir, toolchain string) (stdout, stderr string, code int, err error)

func goTidy(ctx context.Context, dir, toolchain string) (string, string, int, error) {
	c := exec.CommandContext(ctx, "go", "mod", "tidy", "-diff")
	c.Dir = dir
	c.Env = append(os.Environ(), "GOTOOLCHAIN="+toolchain, "GOWORK=off")
	var so, se bytes.Buffer
	c.Stdout, c.Stderr = &so, &se
	c.Stdin = nil
	err := c.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return so.String(), se.String(), ee.ExitCode(), nil
	}
	return so.String(), se.String(), 0, err
}

// runner is replaceable by tests; moduleTimeout bounds one module.
var (
	runner        tidyRun = goTidy
	moduleTimeout         = 90 * time.Second
)

type state int

const (
	tidy state = iota
	untidy
	unread
	// skipped is a module `go mod tidy` cannot say anything about: it holds no
	// Go source, so the check could only ever REMOVE requirements. Never
	// silently clean -- it is printed and counted like every other class.
	skipped
)

type result struct {
	dir       string // as shown: relative to the scanned root
	abs       string
	state     state
	text      string // the diff for untidy, the reason for unread or skipped
	toolchain string // set when the verdict needed one other than "local"
}

// localToolchain is the CI's constraint, and the first thing tried.
const localToolchain = "local"

// findModules lists the directories under root that hold a go.mod, nested ones
// included, skipping vendor/, testdata/ and any directory whose name starts with
// "." or "_". The root itself is never skipped.
func findModules(root string) ([]string, error) {
	var mods []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "go.mod" {
			mods = append(mods, filepath.Dir(p))
		}
		return nil
	})
	sort.Strings(mods)
	return mods, err
}

func skipDir(name string) bool {
	return name == "vendor" || name == "testdata" ||
		strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// hasGoSource says whether the module rooted at dir holds a .go file of its
// own. ⛔ A directory with its own go.mod is a DIFFERENT module, so its sources
// never answer for this one; vendor/, testdata/ and dot or underscore
// directories are skipped, exactly as findModules skips them, because the go
// command does not read them either.
//
// A file behind a build constraint still counts: `go mod tidy` considers every
// build configuration, which is why a `//go:build tools` file holding a blank
// import is the documented way to keep a requirement no package uses.
func hasGoSource(dir string) (bool, error) {
	found := false
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == dir {
				return nil
			}
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found, err
}

// sourceOf is replaceable by tests: findModules walks the same tree first, so a
// real filesystem cannot make hasGoSource fail after it succeeded.
var sourceOf = hasGoSource

// hugoConfigNames are the files that make a directory a Hugo site.
//
// ⛔ The presence of one is REPORTED, never what decides. Hugo mounts a theme
// module through `theme = [...]` as well as through a [module] block, and
// go-fft/docs -- the repository #294 was filed for -- has no [module] block at
// all, so a rule keyed on that block would miss its own occurrence.
var hugoConfigNames = []string{
	"hugo.toml", "hugo.yaml", "hugo.yml", "hugo.json",
	"config.toml", "config.yaml", "config.yml", "config.json",
}

func hugoConfig(dir string) string {
	for _, n := range hugoConfigNames {
		if st, err := os.Stat(filepath.Join(dir, n)); err == nil && !st.IsDir() {
			return n
		}
	}
	if st, err := os.Stat(filepath.Join(dir, "config", "_default")); err == nil && st.IsDir() {
		return "config/_default"
	}
	return ""
}

// noSourceReason says why the tidy check carries no information for this
// module, naming a Hugo configuration when one is there.
func noSourceReason(dir string) string {
	why := "no Go source, so go mod tidy can only REMOVE requirements: it has nothing to say here, and running it would delete them"
	if c := hugoConfig(dir); c != "" {
		why += " (Hugo module site: " + c + ")"
	}
	return why
}

// parsableGoMod says whether the module's go.mod reads as one.
//
// ⛔ A module whose go.mod is BROKEN must not be skipped on the strength of
// holding no .go file: skipping is a clean answer, and nothing in that
// reasoning has read the file that is wrong. It goes to the tidy check
// instead, which names the real error and reports UNREAD. The repository's own
// corpus caught this -- `broken/go.mod` holding "garbage" has no Go source
// either.
func parsableGoMod(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	_, err = modfile.Parse("go.mod", b, nil)
	return err == nil
}

// directive is the `go` line of the module's go.mod, or "" when it cannot be
// read. It is the authority for which toolchain the module asks for; the error
// text only says that the local one is too old.
func directive(dir string) string {
	// An unreadable file leaves b empty, which parses to a file with no go line.
	b, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	f, err := modfile.Parse("go.mod", b, nil)
	if err != nil || f.Go == nil {
		return ""
	}
	return f.Go.Version
}

// floorRe matches the one failure a newer toolchain fixes:
// "go.mod requires go >= 1.27.4 (running go 1.27.3; GOTOOLCHAIN=local)".
var floorRe = regexp.MustCompile(`requires go >= [0-9]`)

func check(ctx context.Context, root string) ([]result, error) {
	mods, err := findModules(root)
	if err != nil {
		return nil, err
	}
	var out []result
	for _, m := range mods {
		rel, _ := filepath.Rel(root, m)
		r := result{dir: rel, abs: m}
		src, serr := sourceOf(m)
		switch {
		case serr != nil:
			r.state, r.text = unread, serr.Error()
		case !src && parsableGoMod(m):
			// Decided WITHOUT running go: there is nothing to resolve, so this
			// needs no toolchain and no network.
			r.state, r.text = skipped, noSourceReason(m)
		default:
			judge(ctx, &r)
		}
		out = append(out, r)
	}
	return out, nil
}

// judge fills r by running the tidy check, retrying once with the toolchain the
// module's directive NAMES when the local one is too old to build it.
//
// ⛔ That retry reproduces the CI rather than loosening the check: the runner
// there HAS the version the directive requires, which is the only reason
// GOTOOLCHAIN=local succeeds for it. Deliberately not GOTOOLCHAIN=auto, which
// would leave the version used unstated -- an instrument whose version is
// implicit is not an instrument. GOWORK=off is untouched either way, so a
// sibling module still cannot satisfy a requirement the CI would not see.
func judge(ctx context.Context, r *result) {
	for tc := localToolchain; ; {
		mctx, cancel := context.WithTimeout(ctx, moduleTimeout)
		so, se, code, err := runner(mctx, r.abs, tc)
		cancel()
		switch {
		case err != nil:
			r.state, r.text = unread, err.Error()
		case code == 0:
			r.state = tidy
		case code == 1 && strings.TrimSpace(so) != "":
			r.state, r.text = untidy, so
		default:
			r.state, r.text = unread, strings.TrimSpace(se+" (exit "+strconv.Itoa(code)+")")
		}
		if tc != localToolchain {
			// The retry has had its turn: its answer stands, whatever it is.
			r.toolchain = tc
			return
		}
		if r.state != unread || !floorRe.MatchString(r.text) {
			return
		}
		d := directive(r.abs)
		if d == "" {
			return // the floor is unreadable, so UNREAD it stays
		}
		tc = "go" + d
	}
}

// RootCmd is the command, driven by tests through SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	var hook bool
	c := cli.New("modtidy [flags] [dir]", "Is every go.mod of this clone tidy?", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if hook {
			runHook(stdin, cli.Out(cmd), cli.Err(cmd))
			return nil
		}
		dir := "."
		if len(args) > 1 {
			return cli.Unanswerable("usage: modtidy [dir]")
		}
		if len(args) == 1 {
			dir = args[0]
		}
		return report(cmd.Context(), cli.Out(cmd), dir)
	}
	c.Flags().BoolVar(&hook, "hook", false, "answer a PreToolUse payload read on stdin")
	return c
}

func report(ctx context.Context, w io.Writer, dir string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	res, err := check(ctx, dir)
	if err != nil {
		return cli.Unanswerable("modtidy: cannot walk %s: %v", dir, err)
	}
	if len(res) == 0 {
		return cli.Unanswerable("modtidy: no go.mod under %s: nothing was checked", dir)
	}
	var nu, nr, ns int
	for _, r := range res {
		switch r.state {
		case tidy:
			fmt.Fprintf(w, "tidy    %s%s\n", r.dir, usedToolchain(r))
		case untidy:
			nu++
			fmt.Fprintf(w, "untidy  %s%s\n%s", r.dir, usedToolchain(r), indent(r.text))
		case unread:
			nr++
			fmt.Fprintf(w, "UNREAD  %s%s: %s\n", r.dir, usedToolchain(r), r.text)
		case skipped:
			ns++
			fmt.Fprintf(w, "skipped %s: %s\n", r.dir, r.text)
		}
	}
	fmt.Fprintf(w, "%d module(s): %d untidy, %d UNREAD, %d skipped\n", len(res), nu, nr, ns)
	switch {
	case nr > 0:
		return cli.Unanswerable("modtidy: %d module(s) could not be read", nr)
	case nu > 0:
		return cli.Found("modtidy: %d module(s) untidy", nu)
	}
	return nil
}

// usedToolchain names the toolchain when it was not the local one, so a reader
// can tell a verdict reached under the directive's version from one reached
// under whatever this host happens to have installed.
func usedToolchain(r result) string {
	if r.toolchain == "" {
		return ""
	}
	return " (GOTOOLCHAIN=" + r.toolchain + ")"
}

func indent(s string) string {
	s = strings.TrimRight(s, "\n")
	return "    " + strings.ReplaceAll(s, "\n", "\n    ") + "\n"
}

// ---- hook ----

type payload struct {
	ToolName  string `json:"tool_name"`
	Cwd       string `json:"cwd"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

type pushCall struct {
	dir    string
	waiver string
}

// runHook answers on stdout: `{}` to allow, a deny decision otherwise. It
// FAILS OPEN on anything it cannot read: a refusal is not a verdict.
func runHook(in io.Reader, stdout, stderr io.Writer) {
	allow := func(why string) {
		if why != "" {
			fmt.Fprintf(stderr, "modtidy: allowing: %s\n", why)
		}
		fmt.Fprintln(stdout, "{}")
	}
	raw, err := io.ReadAll(io.LimitReader(in, 1<<20))
	if err != nil {
		allow("unreadable payload")
		return
	}
	var p payload
	if json.Unmarshal(raw, &p) != nil || (p.ToolName != "" && p.ToolName != "Bash") || p.ToolInput.Command == "" {
		allow("")
		return
	}
	cwd := p.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	// No push on the line: the loop below has nothing to judge and allows.
	pushes := findPushes(p.ToolInput.Command, cwd)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	seen := map[string]bool{}
	var problems []string
	for _, pc := range pushes {
		waiver := pc.waiver
		if waiver == "" {
			waiver = strings.TrimSpace(os.Getenv("MODTIDY_WAIVE"))
		}
		root := toplevel(ctx, pc.dir)
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		res, err := check(ctx, root)
		if err != nil {
			allow("cannot walk " + root)
			return
		}
		for _, r := range res {
			switch r.state {
			case skipped:
				fmt.Fprintf(stderr, "modtidy: %s skipped: %s\n", r.dir, firstLine(r.text))
			case unread:
				fmt.Fprintf(stderr, "modtidy: %s UNREAD (%s); not blocking\n", r.dir, firstLine(r.text))
			case untidy:
				if waiver != "" {
					fmt.Fprintf(stderr, "modtidy: WAIVED (%s): %s is untidy\n", waiver, r.dir)
					writeDecision(stdout, "allow", "modtidy waived: "+waiver)
					return
				}
				problems = append(problems, fmt.Sprintf(
					"module %q in %s is untidy under GOTOOLCHAIN=local GOWORK=off.\nFix: (cd %s && GOTOOLCHAIN=local GOWORK=off go mod tidy), commit go.mod and go.sum, push again.\nDiff:\n%s",
					r.dir, root, r.abs, indent(r.text)))
			}
		}
	}
	if len(problems) == 0 {
		allow("")
		return
	}
	reason := "modtidy refuses this push (go mod tidy at the root does not enter a nested module; memory a-repository-can-hold-two-modules).\n" +
		strings.Join(problems, "\n") +
		"Waiver: MODTIDY_WAIVE=\"<reason>\" gitpush ..."
	writeDecision(stdout, "deny", reason)
}

func writeDecision(w io.Writer, decision, reason string) {
	out := map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName":            "PreToolUse",
		"permissionDecision":       decision,
		"permissionDecisionReason": reason,
	}}
	b, _ := json.Marshal(out)
	fmt.Fprintln(w, string(b))
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

// toplevel is the work tree root of dir, or "" when dir is not in one.
func toplevel(ctx context.Context, dir string) string {
	c := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel")
	c.Stdin = nil
	out, err := c.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

var assignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// findPushes returns one entry per command position whose command is gitpush or
// git push. Only positions count: words inside quotes, comments and heredoc
// bodies are never commands. `cd X` earlier on the line moves the directory,
// and -C X on the push itself moves it again.
func findPushes(command, cwd string) []pushCall {
	var out []pushCall
	dir := cwd
	join := func(base, p string) string {
		if p == "~" || strings.HasPrefix(p, "~/") {
			if h, err := os.UserHomeDir(); err == nil {
				p = filepath.Join(h, strings.TrimPrefix(p, "~"))
			}
		}
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	for _, seg := range segments(command) {
		var waiver string
		i := 0
		for i < len(seg) {
			w := seg[i]
			switch {
			case assignRe.MatchString(w):
				if k, v, _ := strings.Cut(w, "="); k == "MODTIDY_WAIVE" {
					waiver = strings.TrimSpace(v)
				}
			case w == "command" || w == "exec" || w == "time" || w == "env":
			default:
				goto found
			}
			i++
		}
		continue
	found:
		words := seg[i:]
		switch filepath.Base(words[0]) {
		case "cd":
			if len(words) > 1 {
				dir = join(dir, words[1])
			}
		case "gitpush":
			out = append(out, pushCall{dir: gitDir(dir, words[1:], join), waiver: waiver})
		case "git":
			rest := words[1:]
			j := 0
			for j < len(rest) && strings.HasPrefix(rest[j], "-") {
				if rest[j] == "-C" || rest[j] == "-c" || rest[j] == "--git-dir" || rest[j] == "--work-tree" || rest[j] == "--namespace" {
					j++
				}
				j++
			}
			if j < len(rest) && rest[j] == "push" {
				out = append(out, pushCall{dir: gitDir(dir, rest[:min(j, len(rest))], join), waiver: waiver})
			}
		}
	}
	return out
}

// gitDir applies every `-C path` among args to dir.
func gitDir(dir string, args []string, join func(base, p string) string) string {
	for k := 0; k < len(args); k++ {
		if args[k] == "-C" && k+1 < len(args) {
			dir = join(dir, args[k+1])
			k++
		}
	}
	return dir
}

// segments splits a shell line into simple commands, each a list of unquoted
// words. It understands quotes, backslashes, comments, the separators ; & | ( )
// and newline, and skips heredoc bodies. It is deliberately not a shell: an
// unclosed quote swallows the rest of the line, which can only hide a command,
// and a hook that cannot see fails open.
func segments(cmd string) [][]string {
	var segs [][]string
	var cur []string
	var word strings.Builder
	inWord := false
	flushWord := func() {
		if inWord {
			cur = append(cur, word.String())
			word.Reset()
			inWord = false
		}
	}
	flushSeg := func() {
		flushWord()
		if len(cur) > 0 {
			segs = append(segs, cur)
			cur = nil
		}
	}
	var heredocs []string
	r := []rune(cmd)
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == '\\' && i+1 < len(r):
			i++
			if r[i] != '\n' {
				word.WriteRune(r[i])
				inWord = true
			}
		case c == '\'':
			inWord = true
			for i++; i < len(r) && r[i] != '\''; i++ {
				word.WriteRune(r[i])
			}
		case c == '"':
			inWord = true
			for i++; i < len(r) && r[i] != '"'; i++ {
				if r[i] == '\\' && i+1 < len(r) {
					i++
				}
				word.WriteRune(r[i])
			}
		case c == '#' && !inWord:
			for i+1 < len(r) && r[i+1] != '\n' {
				i++
			}
		case c == '\n':
			flushSeg()
			for _, d := range heredocs {
				for i+1 < len(r) {
					j := i + 1
					for j < len(r) && r[j] != '\n' {
						j++
					}
					line := strings.TrimLeft(string(r[i+1:j]), "\t")
					i = j
					if line == d {
						break
					}
				}
			}
			heredocs = nil
		case c == ';' || c == '&' || c == '|' || c == '(' || c == ')':
			flushSeg()
		case c == ' ' || c == '\t' || c == '\r':
			flushWord()
		case c == '<' && i+2 < len(r) && r[i+1] == '<' && r[i+2] != '<':
			flushWord()
			i += 2
			if r[i] == '-' {
				i++
			}
			for i < len(r) && (r[i] == ' ' || r[i] == '\t') {
				i++
			}
			var d strings.Builder
			for ; i < len(r) && !strings.ContainsRune(" \t\n;&|()", r[i]); i++ {
				if r[i] != '\'' && r[i] != '"' && r[i] != '\\' {
					d.WriteRune(r[i])
				}
			}
			i--
			if d.Len() > 0 {
				heredocs = append(heredocs, d.String())
			}
		default:
			word.WriteRune(c)
			inWord = true
		}
	}
	flushSeg()
	return segs
}
