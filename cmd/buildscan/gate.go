// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

// # THE PUSH GATE  (#394)
//
//	buildscan --hook      (a Claude Code PreToolUse hook on Bash)
//
// refuses a `gitpush` whose commits ADD OR CHANGE a Go file that is
// platform-specific, unless `buildscan --dir . --vet` has passed for exactly the
// tree being pushed. Everything else is allowed without a word.
//
// ⛔ WHY. go-fleettools/modfloor#24: packages_test.go called syscall.Mkfifo, which
// does not exist on Windows, and `GOOS=windows go vet ./...` is a lane of that
// repository's CI. Locally go test was green, gofmt printed nothing, mutsweep was
// satisfied: the local GOOS is exactly the configuration that needs no telling.
// The tool that would have said so existed, and its lesson had been written down
// ninety minutes earlier (crossbuild-is-buildscan-vet). A check whose whole value
// is BEFORE the push is only reached by remembering it at the one moment nobody
// thinks about other platforms -- which is a gate's job.
//
// # WHAT TRIGGERS IT
//
// A .go file (test files included: the modfloor failure was one) that the pushed
// range adds, copies, renames or modifies, and that
//
//   - carries a `//go:build` or `// +build` line, or
//   - is named `_<goos>.go`, `_<goarch>.go` or `_<goos>_<goarch>.go` (before an
//     optional `_test`), or
//   - imports syscall, unsafe or golang.org/x/sys/...
//
// A push touching only platform-neutral files is not refused. testdata/ and
// vendor/ are not judged (the go tool does not build them as part of a package).
// A file that does not parse is treated as platform-specific: it cannot be shown
// to be neutral, and the vet it asks for fails loudly on it.
//
// # THE RECORD
//
// `buildscan --vet` that passes under every configuration the CI declares writes
// a record keyed by the HEAD TREE hash, under the user config directory (not a
// cache: evidence that vanishes on its own is not evidence). The gate asks for
// the record of the tree it is about to push, so a pass for an older tree does
// not count -- editing anything changes the tree, which is the whole property.
// No record is written when the working tree has uncommitted changes: the vet
// would have compiled bytes that HEAD does not hold.
//
// # WHEN IT CANNOT READ
//
// It ALLOWS and says so (stderr and the hook's reason): a refusal is not a
// verdict. Not a git repository, no origin/HEAD|main|master to measure the range
// from, a range that cannot be listed, or a repository whose CI declares no
// configuration to vet -- in each, "not judged" is printed, never silence.
//
// # THE WAIVER
//
//	BUILDSCAN_SKIP=1 gitpush ...
//
// allows the push and prints which files went out without a vet pass.
//
// ⚠ It judges what HEAD is: `gitpush origin other` from a different checkout is
// judged by the wrong commit; and it does not see `ghmerge`, which has no tree.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	skipEnv    = "BUILDSCAN_SKIP"
	recordsEnv = "BUILDSCAN_RECORDS"
)

// ---- the record ------------------------------------------------------------

// passRecord says that vet passed for the tree.
type passRecord struct {
	Tree    string    `json:"tree"`
	Dir     string    `json:"dir"` // where it was vetted; informational only
	Configs []string  `json:"configs"`
	At      time.Time `json:"at"`
}

func recordDir() (string, error) {
	if d := os.Getenv(recordsEnv); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "buildscan", "records"), nil
}

// validTree reports a 40- or 64-digit lowercase hex name: a tree is a file name
// here, so nothing else may become one.
var validTree = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

func storeRecord(r passRecord) error {
	if !validTree.MatchString(r.Tree) {
		return fmt.Errorf("not a tree hash: %q", r.Tree)
	}
	dir, err := recordDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	// Atomic: about twenty-five sessions share this machine, and a half-written
	// record read by the gate would be read as corrupt.
	tmp, err := os.CreateTemp(dir, ".record-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, r.Tree+".json"))
}

// lookupRecord returns the record for a tree. No store, or no file: "no record".
func lookupRecord(tree string) (passRecord, bool, error) {
	if !validTree.MatchString(tree) {
		return passRecord{}, false, fmt.Errorf("not a tree hash: %q", tree)
	}
	dir, err := recordDir()
	if err != nil {
		return passRecord{}, false, err
	}
	b, err := os.ReadFile(filepath.Join(dir, tree+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return passRecord{}, false, nil
	}
	if err != nil {
		return passRecord{}, false, err
	}
	var r passRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return passRecord{}, false, fmt.Errorf("record %s: %w", tree, err)
	}
	// A record names the tree it was written for; one filed under another name
	// was moved or edited by hand and must not vouch.
	if r.Tree != tree {
		return passRecord{}, false, fmt.Errorf("record %s holds a vet of %s", tree, r.Tree)
	}
	return r, true, nil
}

// recordVet files a passing vet of the clone at dir. It says, on stderr, why it
// did not, so a missing record is never a silence.
func recordVet(stderr io.Writer, dir string, f finding) {
	if len(f.vetFail) > 0 {
		return
	}
	if len(f.configs) == 0 {
		fmt.Fprintf(stderr, "buildscan: no record: the CI declares no configuration, so the vet compiled nothing\n")
		return
	}
	// The root is absolute with its symlinks resolved, so dir must be too: "." from
	// the root, or a path through a symlink, is the root all the same. Abs fails only
	// without a working directory and then returns "", which is not the root: the
	// comparison below refuses it, so its error needs no branch of its own.
	abs, _ := filepath.Abs(dir)
	if a, err := filepath.EvalSymlinks(abs); err == nil {
		abs = a
	}
	if mods, err := modules(dir); err != nil || len(mods) == 0 {
		fmt.Fprintf(stderr, "buildscan: no record: no module was found to vet\n")
		return
	}
	top, err := gitIn(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return // not a repository: there is no HEAD to key a pass by
	}
	top = strings.TrimSpace(top)
	if a, err := filepath.EvalSymlinks(top); err == nil {
		top = a
	}
	if top != abs {
		fmt.Fprintf(stderr, "buildscan: no record: %s is not the repository root (%s), the vet covered part of it\n", dir, top)
		return
	}
	st, err := gitIn(dir, "status", "--porcelain")
	if err != nil {
		fmt.Fprintf(stderr, "buildscan: no record: %v\n", oneLine(err))
		return
	}
	if strings.TrimSpace(st) != "" {
		fmt.Fprintf(stderr, "buildscan: no record: the working tree has uncommitted changes, so this pass describes bytes HEAD does not hold; commit and vet again\n")
		return
	}
	tree, err := gitIn(dir, "rev-parse", "HEAD^{tree}")
	if err != nil {
		fmt.Fprintf(stderr, "buildscan: no record: %v\n", oneLine(err))
		return
	}
	tree = strings.TrimSpace(tree)
	var cfgs []string
	for _, c := range f.configs {
		cfgs = append(cfgs, c.key())
	}
	sort.Strings(cfgs)
	if err := storeRecord(passRecord{Tree: tree, Dir: abs, Configs: cfgs, At: time.Now().UTC()}); err != nil {
		fmt.Fprintf(stderr, "buildscan: no record: %v\n", oneLine(err))
		return
	}
	fmt.Fprintf(stderr, "buildscan: recorded a vet pass for tree %s\n", tree[:12])
}

// ---- what is platform-specific ---------------------------------------------

var knownOS = setOf("aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios", "js",
	"linux", "nacl", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows", "zos")

var knownArch = setOf("386", "amd64", "amd64p32", "arm", "armbe", "arm64", "arm64be", "loong64", "mips",
	"mipsle", "mips64", "mips64le", "mips64p32", "mips64p32le", "ppc", "ppc64", "ppc64le", "riscv",
	"riscv64", "s390", "s390x", "sparc", "sparc64", "wasm")

func setOf(s ...string) map[string]bool {
	m := make(map[string]bool, len(s))
	for _, v := range s {
		m[v] = true
	}
	return m
}

// nameReason: the file name alone makes the go tool constrain the file.
func nameReason(path string) string {
	n := strings.TrimSuffix(filepath.Base(path), ".go")
	n = strings.TrimSuffix(n, "_test")
	parts := strings.Split(n, "_")
	if len(parts) < 2 { // `linux.go` is not constrained: the suffix needs a stem
		return ""
	}
	last := parts[len(parts)-1]
	if knownArch[last] {
		return "named for the architecture " + last
	}
	if knownOS[last] {
		return "named for the operating system " + last
	}
	return ""
}

// platformReason says why a Go file is platform-specific, or "" when nothing
// shows it is.
func platformReason(path string, src []byte) string {
	if r := nameReason(path); r != "" {
		return r
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ImportsOnly|parser.ParseComments)
	if err != nil {
		return "does not parse, so it cannot be shown to be neutral"
	}
	for _, cg := range f.Comments {
		if cg.Pos() >= f.Package {
			break
		}
		for _, c := range cg.List {
			if constraint.IsGoBuild(c.Text) || constraint.IsPlusBuild(c.Text) {
				return "carries a build constraint (" + strings.TrimSpace(c.Text) + ")"
			}
		}
	}
	for _, im := range f.Imports {
		p := strings.Trim(im.Path.Value, "\"`")
		if p == "syscall" || p == "unsafe" || p == "golang.org/x/sys" || strings.HasPrefix(p, "golang.org/x/sys/") {
			return "imports " + p
		}
	}
	return ""
}

// skippedDir: the go tool does not build testdata/ or vendor/ as a package.
func skippedDir(f string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(f), "/") {
		if seg == "testdata" || seg == "vendor" {
			return true
		}
	}
	return false
}

// ---- reading the push ------------------------------------------------------

func gitIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var e bytes.Buffer
	cmd.Stderr = &e
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(e.String()))
	}
	return string(out), nil
}

func oneLine(err error) string { return strings.Join(strings.Fields(err.Error()), " ") }

// pushBase is the commit the pushed range starts from: where HEAD leaves the
// remote's default branch.
func pushBase(dir string) (string, error) {
	for _, ref := range []string{"origin/HEAD", "origin/main", "origin/master"} {
		if _, err := gitIn(dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
			continue
		}
		mb, err := gitIn(dir, "merge-base", "HEAD", ref)
		if err != nil {
			continue
		}
		return strings.TrimSpace(mb), nil
	}
	return "", errors.New("no origin/HEAD, origin/main or origin/master to measure the pushed range from")
}

// triggers lists "file: why" for each platform-specific Go file the range
// base..HEAD adds or changes, read at the bytes HEAD holds.
func triggers(top, base string) (hits, unread []string, err error) {
	list, err := gitIn(top, "diff", "--name-only", "--diff-filter=AMCR", base, "HEAD", "--", "*.go")
	if err != nil {
		return nil, nil, err
	}
	for _, f := range strings.Split(strings.TrimSpace(list), "\n") {
		if f == "" || skippedDir(f) {
			continue
		}
		src, err := gitIn(top, "show", "HEAD:"+f)
		if err != nil {
			unread = append(unread, f+": "+oneLine(err))
			continue
		}
		if why := platformReason(f, []byte(src)); why != "" {
			hits = append(hits, f+" ("+why+")")
		}
	}
	return hits, unread, nil
}

// ---- reading the command ---------------------------------------------------

type payload struct {
	ToolName  string `json:"tool_name"`
	Cwd       string `json:"cwd"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

var (
	segmentSplit = regexp.MustCompile(`&&|\|\||[;&|\n]`)
	envAssign    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	redirectWord = regexp.MustCompile(`^\d*[<>]`)
)

// headMovers are the git subcommands after which HEAD is not what the hook read.
var headMovers = setOf("commit", "merge", "cherry-pick", "rebase", "am", "revert", "reset",
	"checkout", "switch", "pull")

type push struct {
	dir   string
	skip  bool
	moved string // a git command earlier in the line that moves HEAD
}

func cdTo(dir, arg string) string {
	arg = strings.Trim(arg, `"'`)
	if arg == "~" || strings.HasPrefix(arg, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			arg = filepath.Join(home, strings.TrimPrefix(arg, "~"))
		}
	}
	if filepath.IsAbs(arg) || strings.HasPrefix(arg, "/") {
		return filepath.Clean(arg)
	}
	return filepath.Join(dir, arg)
}

// pushesIn finds the pushes in a Bash command line. It reads text, not a shell:
// a push hidden in a quoted string or an eval is not seen, and so is ALLOWED --
// the direction this hook fails in whenever it cannot read.
func pushesIn(command, cwd string) []push {
	dir, moved := cwd, ""
	var out []push
	for _, seg := range segmentSplit.Split(command, -1) {
		f := strings.Fields(seg)
		skip := false
		for len(f) > 0 && (envAssign.MatchString(f[0]) || redirectWord.MatchString(f[0])) {
			if f[0] == skipEnv+"=1" {
				skip = true
			}
			f = f[1:]
		}
		if len(f) == 0 {
			continue
		}
		switch prog := filepath.Base(f[0]); {
		case prog == "cd" && len(f) > 1:
			dir = cdTo(dir, f[1])
		case prog == "gitpush":
			out = append(out, push{dir: dir, skip: skip, moved: moved})
		case prog == "git":
			d := dir
			for i := 1; i < len(f); i++ {
				w := f[i]
				if (w == "-C" || w == "-c") && i+1 < len(f) {
					i++
					if w == "-C" {
						d = cdTo(d, f[i])
					}
					continue
				}
				if strings.HasPrefix(w, "-") {
					continue
				}
				switch {
				case w == "push":
					out = append(out, push{dir: d, skip: skip, moved: moved})
				case headMovers[w] && moved == "":
					moved = "git " + w
				}
				break
			}
		}
	}
	return out
}

// ---- the decision ----------------------------------------------------------

type decision struct {
	deny   bool
	reason string // the refusal, or the notice of an allowance; "" = silent allow
}

// judgePush returns (refusal, notice) for one push.
func judgePush(ps push) (refusal, notice string) {
	top, err := gitIn(ps.dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "buildscan gate: NOT JUDGED in " + ps.dir + " (" + oneLine(err) + "); allowed, not counted as vetted"
	}
	top = strings.TrimSpace(top)
	base, err := pushBase(top)
	if err != nil {
		return "", "buildscan gate: NOT JUDGED in " + top + " (" + oneLine(err) + "); allowed, not counted as vetted"
	}
	if ps.moved != "" {
		// The hook runs BEFORE the line: it would judge the HEAD from before this
		// command's own commit. Refused only where it matters, a repository that
		// holds Go.
		if gos, err := gitIn(top, "ls-files", "--", "*.go"); err != nil || strings.TrimSpace(gos) == "" {
			return "", ""
		}
		if ps.skip {
			return "", "buildscan gate WAIVED by " + skipEnv + "=1 — a push after `" + ps.moved + "` in the same command, not judged"
		}
		return "in " + top + ": this command runs `" + ps.moved + "` before it pushes, and the gate runs BEFORE the command, " +
			"so it would judge HEAD as it is now, not the commit being pushed. Run the push as a command of its own, after the commit.", ""
	}
	hits, unread, err := triggers(top, base)
	if err != nil {
		return "", "buildscan gate: NOT JUDGED in " + top + " (could not list the pushed range: " + oneLine(err) + "); allowed, not counted as vetted"
	}
	var said []string
	if len(unread) > 0 {
		said = append(said, "buildscan gate: NOT JUDGED "+strings.Join(unread, "; "))
	}
	if len(hits) == 0 {
		return "", strings.Join(said, "\n")
	}
	tree, err := gitIn(top, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return "", "buildscan gate: NOT JUDGED in " + top + " (" + oneLine(err) + "); allowed, not counted as vetted"
	}
	tree = strings.TrimSpace(tree)
	_, ok, err := lookupRecord(tree)
	if err != nil {
		return "in " + top + ": the record for tree " + tree[:12] + " cannot be read (" + oneLine(err) +
			"); a record that cannot be read is one a new vet replaces: run `buildscan --dir " + top + " --vet`", ""
	}
	if ok {
		return "", strings.Join(said, "\n")
	}
	list := "  " + strings.Join(hits, "\n  ")
	if ps.skip {
		said = append(said, "buildscan gate WAIVED by "+skipEnv+"=1 — pushed without a vet pass for tree "+tree[:12]+":\n"+list)
		return "", strings.Join(said, "\n")
	}
	f, serr := scan(top, filepath.Base(top))
	switch {
	case serr != nil:
		said = append(said, "buildscan gate: NOT JUDGED in "+top+" (could not read the CI's configurations: "+oneLine(serr)+"); allowed, not counted as vetted")
		return "", strings.Join(said, "\n")
	case len(f.configs) == 0:
		said = append(said, "buildscan gate: NOT JUDGED in "+top+" (the CI declares no configuration to vet); allowed, not counted as vetted")
		return "", strings.Join(said, "\n")
	}
	return "in " + top + ": this push adds or changes platform-specific Go code, and no `buildscan --vet` pass is recorded for tree " +
		tree[:12] + ":\n" + list + "\n\nVet every configuration the CI compiles (from a clean, committed tree):\n  buildscan --dir " + top + " --vet\n\n" +
		"A deliberate exception is `" + skipEnv + "=1 gitpush ...`, and it is shown to the user.", ""
}

// decide is the whole policy: only a definite, unwaived finding denies.
func decide(p payload) decision {
	if p.ToolName != "" && p.ToolName != "Bash" {
		return decision{}
	}
	var deny, notes []string
	for _, ps := range pushesIn(p.ToolInput.Command, p.Cwd) {
		r, n := judgePush(ps)
		if r != "" {
			deny = append(deny, r)
		}
		if n != "" {
			notes = append(notes, n)
		}
	}
	if len(deny) > 0 {
		return decision{deny: true, reason: "⛔ buildscan gate: this push is refused.\n\n" + strings.Join(deny, "\n\n")}
	}
	return decision{reason: strings.Join(notes, "\n")}
}

// hookMode answers a PreToolUse payload; whatever it cannot read allows and says so.
func hookMode(out, errOut io.Writer, stdin io.Reader) int {
	b, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintln(errOut, "buildscan gate: could not read the hook payload; allowed (a refusal is not a verdict)")
		return 0
	}
	var p payload
	if err := json.Unmarshal(b, &p); err != nil {
		fmt.Fprintf(errOut, "buildscan gate: could not decode the hook payload (%v); allowed (a refusal is not a verdict)\n", err)
		return 0
	}
	d := decide(p)
	switch {
	case d.deny:
		writeDecision(out, "deny", d.reason)
	case d.reason != "":
		fmt.Fprintln(errOut, d.reason)
		writeDecision(out, "allow", d.reason)
	}
	return 0
}

func writeDecision(w io.Writer, decision, reason string) {
	b, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName":            "PreToolUse",
		"permissionDecision":       decision,
		"permissionDecisionReason": reason,
	}})
	fmt.Fprintln(w, string(b))
}
