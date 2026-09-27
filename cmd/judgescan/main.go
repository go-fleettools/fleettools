// judgescan reports every repository whose tests look for an external tool
// that its CI never installs — a foreign judge that exists in the source and
// has never once run.
//
//	judgescan                  # the whole fleet, under the GitHub root
//	judgescan -root DIR        # somewhere else
//	judgescan -repo o/r        # one repository, verbosely
//
// It exists because the same defect turned up twice in one afternoon, in two
// unrelated repositories, and neither showed any symptom.
//
// go-fde/luks had TestInterop_LiveCryptsetup, which skipped unless it was root
// AND cryptsetup was present. The CI is unprivileged and installed neither, so
// it had never run. The committed fixtures were all LUKS2, so LUKS1 was read
// back only by the code that writes it — and a misreading of the LUKS
// anti-forensic split lived on both sides of that fence for months, agreed
// with itself, and passed.
//
// go-filesystems/btrfs had seven kernel oracles, each gated on
// `os.Geteuid() != 0` as a single block. But `btrfs check` — upstream's own
// reader, judging OUR WRITES, the most valuable direction there is — reads an
// image FILE and needs no privilege at all. Only the loop-mount half did. The
// check half was locked behind a requirement it never had, in a CI that also
// did not install btrfs-progs.
//
// Both look identical from outside: a green lane, a test file full of
// assertions about a third-party tool, and nothing whatsoever being judged.
//
// # What it cannot tell you
//
// This reports a MISSING TOOL, not a useless test. A macOS-only tool on a
// Linux lane is correct. A tool the repository deliberately treats as optional
// is a decision, not a defect. The output is a list of questions to ask, and
// the interesting column is the last one: what a test is prepared to check and
// no machine has ever checked.
//
// It reads the working tree only. No API calls, no network — which also means
// it cannot starve Renovate's hourly budget, and can be run as often as you
// like.
package main

import (
	"flag"
	"fmt"
	"github.com/go-fleettools/fleettools/internal/checkout"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// lookPathRE finds the tool name in `exec.LookPath("name")`.
//
// Only the literal form is matched. A LookPath over a variable or a slice
// element names its tool somewhere else in the file, and guessing which string
// that is produces confident nonsense; those are reported as unresolved rather
// than invented.
var lookPathRE = regexp.MustCompile(`LookPath\("([a-zA-Z0-9._+-]+)"\)`)

// discardedErrRE matches a LookPath whose ERROR is thrown away:
//
//	mockPath, _ := exec.LookPath("mock")
//
// That is not a gate. go-filesystems/ext4 and /xfs prefer a system `mock` and
// fall back to ./bin/mock in the repository, so the absence of the system one
// changes nothing and judges nothing. A tool looked up this way must not be
// reported as a missing judge: it was never acting as one.
var discardedErrRE = regexp.MustCompile(`,\s*_\s*:?=\s*exec\.LookPath\("([a-zA-Z0-9._+-]+)"\)`)

// fallbackRE matches a LookPath whose failure leads to a FALLBACK rather than
// a gate:
//
//	if p, err := exec.LookPath("git-http-backend"); err == nil {
//		return p
//	}
//	// ... otherwise ask `git --exec-path`
//
// go-tex/go-tex.github.io does exactly this. The tool being absent changes
// which path is taken, not whether anything is judged, so reporting it as a
// missing judge is noise. Same family as discardedErrRE: what matters is not
// that LookPath was called, but what happens when it fails.
var fallbackRE = regexp.MustCompile(`LookPath\("([a-zA-Z0-9._+-]+)"\);\s*err\s*==\s*nil`)

// lookPathDynamicRE finds a LookPath whose argument is not a literal.
var lookPathDynamicRE = regexp.MustCompile(`LookPath\([^")]`)

// ubiquitous names a tool every runner already has, or that is not a judge at
// all. Reporting these is noise: `sh` is not a witness to anything, and `go`
// is the thing under test.
var ubiquitous = map[string]bool{
	"sh": true, "bash": true, "ls": true, "cp": true, "mv": true, "rm": true,
	"true": true, "false": true, "echo": true, "sleep": true, "pwd": true,
	"env": true, "cat": true, "go": true, "git": true, "hostname": true,
	"uname": true, "which": true, "mount": true, "umount": true, "sudo": true,
	// Present on every runner image, GitHub's included, and never installed
	// by a workflow. Reporting them buries the real findings.
	"openssl": true, "tar": true, "gzip": true, "gunzip": true, "unzip": true,
	"curl": true, "wget": true, "make": true, "diff": true, "sed": true,
}

// packageOf maps a binary to the other spellings a workflow may install it
// under. A CI step says `poppler-utils`, never `pdftotext`, so a scan that
// looks only for the binary name reports every poppler judge as missing --
// which is exactly the false positive this table exists to prevent.
//
// The macOS entry is different in kind: hdiutil, diskutil and codesign are not
// installed by anyone, they come with the runner. Naming a macOS runner IS
// installing them.
var packageOf = map[string][]string{
	"pdftotext": {"poppler"}, "pdftoppm": {"poppler"}, "pdfinfo": {"poppler"},
	"debugfs": {"e2fsprogs"}, "e2fsck": {"e2fsprogs"}, "mke2fs": {"e2fsprogs"},
	"dumpe2fs": {"e2fsprogs"}, "resize2fs": {"e2fsprogs"},
	"xfs_db": {"xfsprogs"}, "xfs_repair": {"xfsprogs"}, "mkfs.xfs": {"xfsprogs"},
	"mkntfs": {"ntfs-3g", "ntfsprogs"}, "ntfs-3g": {"ntfs-3g"},
	"ntfsfix": {"ntfs-3g", "ntfsprogs"}, "ntfsls": {"ntfs-3g", "ntfsprogs"},
	"mdir": {"mtools"}, "mcopy": {"mtools"}, "mmd": {"mtools"},
	"btrfs": {"btrfs-progs"}, "mkfs.btrfs": {"btrfs-progs"},
	"zdb": {"zfsutils", "zfs"}, "zfs": {"zfsutils", "zfs"}, "zpool": {"zfsutils", "zfs"},
	"cryptsetup": {"cryptsetup-bin"},
	"ldapsearch": {"ldap-utils", "openldap"},
	"smbclient":  {"samba", "smbclient"},
	"sftp":       {"openssh"}, "ssh-keygen": {"openssh"},
	"dbus-daemon": {"dbus"},
	"qemu-img":    {"qemu"},
	"bundle":      {"ruby"}, "gem": {"ruby"}, "rubocop": {"ruby"},
	"ansible": {"ansible"}, "ansible-playbook": {"ansible"},
	"ansible-inventory": {"ansible"}, "ansible-vault": {"ansible"},
	"chktex":  {"texlive", "tex"},
	"node":    {"setup-node", "nodejs"},
	"hdiutil": {"macos-", "macOS", "darwin"}, "diskutil": {"macos-", "macOS", "darwin"},
	"codesign": {"macos-", "macOS", "darwin"},
}

// finding is one repository's verdict.
type finding struct {
	repo       string   // org/name, relative to root
	noCI       string   // non-empty when the repository has no workflow at all
	missing    []string // looked for by a test, named nowhere in any workflow
	dynamic    bool     // at least one LookPath whose argument is not a literal
	workflows  int      // how many workflow files were actually read
	bytesRead  int      // how much YAML was actually read -- the positive control
	fetchAge   time.Duration
	fetchKnown bool
	behind     int // commits this checkout is behind its remote default
}

func main() {
	root := flag.String("root", defaultRoot(), "directory holding org/repo checkouts")
	only := flag.String("repo", "", "scan a single org/repo, verbosely")
	flag.Parse()

	repos, err := findRepos(*root, *only)
	if err != nil {
		fmt.Fprintln(os.Stderr, "judgescan:", err)
		os.Exit(1)
	}
	if len(repos) == 0 {
		fmt.Fprintf(os.Stderr, "judgescan: no repositories under %s\n", *root)
		os.Exit(1)
	}

	var found []finding
	scanned, withTools := 0, 0
	for _, r := range repos {
		f, ok := scan(*root, r)
		scanned++
		if !ok {
			continue
		}
		withTools++
		if f.noCI != "" || len(f.missing) > 0 {
			found = append(found, f)
		}
	}
	// ⛔ Confirm each finding belongs to us before printing it. judgescan had
	// no such question and was still naming forks: tannevaled/tamago-go (a
	// fork of usbarmory's) and tannevaled/x-sys (not on GitHub at all) were
	// two of fourteen. See internal/checkout — testscan asks the same thing,
	// and it is one function so the two cannot drift apart again.
	var dropped []string
	if *only == "" {
		repos := make([]string, 0, len(found))
		for _, f := range found {
			repos = append(repos, f.repo)
		}
		owners, have := checkout.Owners()
		keep, out := checkout.Ours(repos, owners, have, checkout.Ask)
		dropped = out
		var kept []finding
		for _, f := range found {
			if keep[f.repo] {
				kept = append(kept, f)
			}
		}
		found = kept
	}

	sort.Slice(found, func(i, j int) bool { return found[i].repo < found[j].repo })

	for _, f := range found {
		switch {
		case f.noCI != "":
			fmt.Printf("%-40s  no CI at all       %s\n", f.repo, strings.Join(f.missing, " "))
		default:
			fmt.Printf("%-40s  never installed    %s\n", f.repo, strings.Join(f.missing, " "))
		}
		if *only != "" {
			fmt.Printf("    read %d workflow file(s), %d bytes of YAML\n", f.workflows, f.bytesRead)
			if f.dynamic {
				fmt.Println("    NOTE: a LookPath here takes a non-literal argument; its tool is not in this list")
			}
		}
	}

	// How stale is the evidence? This reads the working tree, so a checkout
	// that has not fetched in weeks gives a weeks-old answer that looks exactly
	// like a current one. Say so before anyone acts on the list.
	if len(dropped) > 0 {
		sort.Strings(dropped)
		fmt.Fprintf(os.Stderr, "\n%d checkout(s) left out — not ours to fix:\n", len(dropped))
		for _, d := range dropped {
			fmt.Fprintf(os.Stderr, "  %s\n", d)
		}
	}

	// ⛔ The distance, not the fetch age. This counted clones that had not
	// FETCHED in a week, and `git fetch` refreshes FETCH_HEAD without touching
	// the working tree — so the signal said "fresh" for a tree twenty commits
	// behind. testscan learnt this on 2026-09-27 and this copy did not, which
	// is why both now call one function.
	stale, furthest := 0, 0
	for _, f := range found {
		if f.behind > 0 {
			stale++
		}
		if f.behind > furthest {
			furthest = f.behind
		}
	}
	if stale > 0 {
		fmt.Fprintf(os.Stderr,
			"\n⚠ %d of the %d findings above come from a checkout BEHIND its remote (furthest: %d commits).\n"+
				"  Pull those clones and RE-RUN this: refreshing without re-deriving makes a stale finding\n"+
				"  look confirmed.\n", stale, len(found), furthest)
	}

	// A scan that cannot read reports zero, and zero reads as good news. Say
	// what was actually looked at, always.
	fmt.Fprintf(os.Stderr, "\n%d repositories scanned, %d name an external tool in a test, %d have one their CI never installs\n",
		scanned, withTools, len(found))
	if n := unparsedFiles.Load(); n > 0 {
		// Said out loud: those files were read by the old text search, which
		// is the instrument this replaces precisely because it cannot tell a
		// call from a string that looks like one.
		fmt.Fprintf(os.Stderr, "⚠ %d test file(s) did not parse and fell back to the text search.\n", n)
	}
}

// defaultRoot is the GitHub checkout root on this machine.
func defaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, "Documents", "VCS", "GIT", "github.com")
}

// findRepos lists org/repo directories that are git checkouts.
func findRepos(root, only string) ([]string, error) {
	if only != "" {
		if _, err := os.Stat(filepath.Join(root, only)); err != nil {
			return nil, fmt.Errorf("%s: %w", only, err)
		}
		return []string{only}, nil
	}
	orgs, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, o := range orgs {
		if !o.IsDir() {
			continue
		}
		names, err := os.ReadDir(filepath.Join(root, o.Name()))
		if err != nil {
			continue
		}
		for _, n := range names {
			if !n.IsDir() {
				continue
			}
			rel := filepath.Join(o.Name(), n.Name())
			// A worktree's .git is a FILE holding `gitdir: ...`, not a
			// directory. It is the same repository checked out again, so
			// counting it inflates the total and prints every finding once per
			// worktree -- openweft/weft-loom-server appeared three times.
			st, err := os.Stat(filepath.Join(root, rel, ".git"))
			if err == nil && st.IsDir() {
				out = append(out, rel)
			}
		}
	}
	return out, nil
}

// scan returns the finding for one repository, and whether its tests name any
// external tool at all.
func scan(root, repo string) (finding, bool) {
	dir := filepath.Join(root, repo)
	tools, dynamic := toolsWanted(dir)
	if len(tools) == 0 && !dynamic {
		return finding{}, false
	}
	f := finding{repo: repo, dynamic: dynamic}
	f.behind, f.fetchAge, f.fetchKnown = checkout.Staleness(dir)
	yaml, n, err := readWorkflows(dir)
	if err != nil || n == 0 {
		f.noCI = "no workflows"
		f.missing = tools
		return f, true
	}
	f.workflows, f.bytesRead = n, len(yaml)
	hay := strings.ToLower(installLines(yaml))
	for _, t := range tools {
		if !mentioned(hay, t) {
			f.missing = append(f.missing, t)
		}
	}
	return f, true
}

// toolsWanted collects the literal tool names a repository's tests look for.
func toolsWanted(dir string) ([]string, bool) {
	seen := map[string]bool{}
	dynamic := false
	unparsed := 0
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		// PARSED, not searched. See parse.go: a LookPath inside a string
		// literal is not a call, and this tool's own test fixtures are full
		// of them.
		gated, _, dyn, ok := lookPaths(b)
		if !ok {
			// A file that does not parse is NOT a file naming no tools. Fall
			// back to the old text search, and count it so the report can say
			// the pass was not uniform.
			unparsed++
			discarded := map[string]bool{}
			for _, m := range discardedErrRE.FindAllSubmatch(b, -1) {
				discarded[string(m[1])] = true
			}
			for _, m := range fallbackRE.FindAllSubmatch(b, -1) {
				discarded[string(m[1])] = true
			}
			for _, m := range lookPathRE.FindAllSubmatch(b, -1) {
				name := string(m[1])
				if !ubiquitous[name] && !discarded[name] {
					seen[name] = true
				}
			}
			if lookPathDynamicRE.Match(b) {
				dynamic = true
			}
			return nil
		}
		for name := range gated {
			if !ubiquitous[name] {
				seen[name] = true
			}
		}
		if dyn {
			dynamic = true
		}
		return nil
	})
	if unparsed > 0 {
		unparsedFiles.Add(int64(unparsed))
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, dynamic
}

// readWorkflows concatenates every workflow file, returning the text and the
// file count. The count is the positive control: a scan that silently read
// nothing would otherwise report every tool as missing, which is what the
// shell version of this did before it was rewritten here.
func readWorkflows(dir string) (string, int, error) {
	wf := filepath.Join(dir, ".github", "workflows")
	entries, err := os.ReadDir(wf)
	if err != nil {
		return "", 0, err
	}
	var sb strings.Builder
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".yml") && !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(wf, e.Name()))
		if err != nil {
			continue
		}
		sb.Write(b)
		sb.WriteByte('\n')
		n++
	}
	return sb.String(), n, nil
}

// installLines keeps only the YAML lines that can actually PUT a tool on the
// runner: a shell step, an action, or the runner image itself.
//
// Searching the whole file instead reads a repository's own name as proof it
// installed a tool of that name. go-filesystems/btrfs checks itself out with
// `path: btrfs`, so a whole-file search finds "btrfs" and concludes btrfs-progs
// is present -- in the one repository where it demonstrably was not. That
// false NEGATIVE is the dangerous direction: it removes a real finding from
// the list and nothing looks wrong.
func installLines(yaml string) string {
	var sb strings.Builder
	for _, line := range strings.Split(yaml, "\n") {
		t := strings.TrimSpace(line)
		// A runner image can be chosen from a matrix, where `runs-on:` says
		// only `${{ matrix.os }}` and the real name sits in a mapping line the
		// filter below drops. go-macos/appbundle has
		//
		//	os: [ubuntu-latest, macos-latest, windows-latest]
		//
		// and so already runs its codesign judge -- reported as missing until
		// this case existed. Any line naming a runner image counts.
		if strings.Contains(t, "macos-") || strings.Contains(t, "windows-") ||
			strings.Contains(t, "ubuntu-") {
			sb.WriteString(t)
			sb.WriteByte('\n')
			continue
		}
		switch {
		case strings.HasPrefix(t, "run:"), strings.HasPrefix(t, "uses:"),
			strings.HasPrefix(t, "runs-on:"), strings.HasPrefix(t, "image:"),
			strings.HasPrefix(t, "- "), strings.HasPrefix(t, "|"):
			sb.WriteString(t)
			sb.WriteByte('\n')
		default:
			// A multi-line `run: |` block: its body is indented under the key
			// and carries no key of its own. Keep anything that looks like a
			// command rather than a YAML mapping.
			if !strings.Contains(t, ": ") && !strings.HasSuffix(t, ":") && t != "" {
				sb.WriteString(t)
				sb.WriteByte('\n')
			}
		}
	}
	return sb.String()
}

// mentioned says whether the workflows install this tool, under its own name
// or under any spelling a workflow would install it by.
func mentioned(lowerInstall, tool string) bool {
	if strings.Contains(lowerInstall, strings.ToLower(tool)) {
		return true
	}
	for _, alias := range packageOf[tool] {
		if strings.Contains(lowerInstall, strings.ToLower(alias)) {
			return true
		}
	}
	return false
}
