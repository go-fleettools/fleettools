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

	"github.com/go-fleettools/fleettools/internal/fleet"
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
	// awk is POSIX-mandated and sits beside sed and diff above for the same
	// reason: it is on every Unix this fleet builds on, macOS included.
	"awk": true,
	// ⛔ bzip2 is here on EVIDENCE, not on the same reasoning. It was reported
	// against go-compressions/bzip2, whose test comment reads "the one whose
	// acceptance means the archive is a bzip2 archive rather than something
	// two Go packages agree about" — and that judge was already running: its
	// subtests appear in the CI log with no skip, and `bzip2` is in the
	// runner image's own apt-package table.
	//
	// ⛔ ABSENCE FROM THAT TABLE PROVES NOTHING, which is why nothing else was
	// added from it: `git` is not in the apt list either, and `git` is on
	// every runner — it arrives another way. Presence is evidence; absence is
	// silence.
	"bzip2": true,
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
	// libarchive's cpio and tar, and 7-Zip's own binary. MEASURED, not guessed:
	// go-filesystems/unarchive run 36321532531 logged `Setting up
	// 7zip-standalone (23.01+dfsg-11)` and its 7zz judge then passed, and
	// go-filesystems/cpio installs libarchive-tools and passes `pwb`, a format
	// GNU cpio cannot write. Both were reported here as missing tools the day
	// after they were fixed, because a package is not spelt like its program.
	"bsdcpio": {"libarchive-tools", "libarchive"},
	"bsdtar":  {"libarchive-tools", "libarchive"},
	"7zz":     {"7zip-standalone", "7zip"},
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
	behind     int      // commits this checkout is behind its remote default
	skipFatal  bool     // a workflow turns `--- SKIP` into a failing step
	replay     []string // `go test` lines that could replay a cached result
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	root := flag.String("root", defaultRoot(), "directory holding org/repo checkouts")
	doFetch := flag.Bool("fetch", true, "git fetch each repository about to be reported, so its distance is measured and not remembered")
	only := flag.String("repo", "", "scan a single org/repo, verbosely")
	showReplays := flag.Bool("replays", false, "list every repository whose `go test` can replay a cached result")
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

	var found, withTool []finding
	scanned, withTools := 0, 0
	for _, r := range repos {
		f, ok := scan(*root, r)
		scanned++
		if !ok {
			continue
		}
		withTools++
		withTool = append(withTool, f)
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

	// A repository already on the list above has a tool nobody installs, which
	// is the bigger problem; naming it twice buries the one that is only ever
	// replayed. So this class is reported for the repositories whose judge IS
	// installed.
	onList := map[string]bool{}
	for _, f := range found {
		onList[f.repo] = true
	}
	var replays []finding
	for _, f := range withTool {
		if !onList[f.repo] && len(f.replay) > 0 {
			replays = append(replays, f)
		}
	}
	// ⛔ The same ownership question the list above asks. Without it this
	// named tannevaled/purego, a fork, on its first run.
	if *only == "" && len(replays) > 0 {
		names := make([]string, 0, len(replays))
		for _, f := range replays {
			names = append(names, f.repo)
		}
		owners, have := checkout.Owners()
		keep, _ := checkout.Ours(names, owners, have, checkout.Ask)
		var mine []finding
		for _, f := range replays {
			if keep[f.repo] {
				mine = append(mine, f)
			}
		}
		replays = mine
	}
	sort.Slice(replays, func(i, j int) bool { return replays[i].repo < replays[j].repo })

	// Set aside the repositories whose CI would go RED on the very skip this
	// tool is looking for. They are still printed below; they are just not
	// this tool's findings, because nothing about them is quiet.
	var loud []finding
	{
		var quiet []finding
		for _, f := range found {
			if f.skipFatal && f.noCI == "" {
				loud = append(loud, f)
			} else {
				quiet = append(quiet, f)
			}
		}
		found = quiet
	}

	// ⛔ The list is printed AFTER the re-derivation below, not before it. The
	// first version printed here and dropped the finding four lines later, so
	// a stale finding still reached the reader's eyes with a retraction under
	// it. A correction that arrives after the claim is not a correction.
	// How stale is the evidence? This reads the working tree, so a checkout
	// that has not fetched in weeks gives a weeks-old answer that looks exactly
	// like a current one. Say so before anyone acts on the list.
	if len(replays) > 0 {
		fmt.Fprintf(os.Stderr,
			"\n%d install their judge and can still replay a run that did not have it\n"+
				"  (go test without -count=1; the cache key does not see a program appearing):\n", len(replays))
		// ⛔ The COUNT is unconditional and the list is not. 119 lines of
		// census under a list of 5 findings buries the 5, and a scanner whose
		// important answer scrolls off is a scanner nobody reads -- but a
		// number that only appears with a flag is a number nobody knows to
		// ask for.
		if !*showReplays {
			fmt.Fprintln(os.Stderr, "  (-replays to list them)")
		}
		for _, f := range replays {
			if !*showReplays {
				break
			}
			first := f.replay[0]
			if len(first) > 64 {
				first = first[:61] + "..."
			}
			more := ""
			if n := len(f.replay) - 1; n > 0 {
				more = fmt.Sprintf("  (+%d more)", n)
			}
			fmt.Fprintf(os.Stderr, "  %-38s %s%s\n", f.repo, first, more)
		}
	}

	if len(loud) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d left out — their CI FAILS on a `--- SKIP`, so the skip cannot be silent:\n", len(loud))
		for _, f := range loud {
			fmt.Fprintf(os.Stderr, "  %-38s %s\n", f.repo, strings.Join(f.missing, " "))
		}
		fmt.Fprintln(os.Stderr, "  (the tool name is spelt unlike the package that installs it; the run is green)")
	}

	if len(dropped) > 0 {
		sort.Strings(dropped)
		fmt.Fprintf(os.Stderr, "\n%d checkout(s) left out — not ours to fix:\n", len(dropped))
		for _, d := range dropped {
			fmt.Fprintf(os.Stderr, "  %s\n", d)
		}
	}

	// ⛔ RE-DERIVE, do not merely refresh. This tool has been printing
	// "refreshing without re-deriving makes a stale finding look confirmed"
	// while doing exactly that: it fetched, measured the distance, warned, and
	// then reported a finding parsed from the OLD bytes. On 2026-09-27 that
	// warning fired on 2 of 5 findings and, once the clones were pulled by
	// hand, 4 of 8 findings disappeared -- every one of them a workflow that
	// had gained the tool upstream.
	//
	// So a finding whose checkout is behind is re-scanned against the remote
	// default, using this same scan() over a materialised tree. One parser,
	// not two.
	ages := make([]checkout.Age, 0, len(found))
	var survived []finding
	rederived, dropped2 := 0, 0
	for _, f := range found {
		a := checkout.Age{Behind: f.behind, FetchAge: f.fetchAge, FetchKnown: f.fetchKnown}
		dir := filepath.Join(*root, f.repo)
		if *doFetch {
			if n, ok := checkout.Refresh(dir); ok {
				a = checkout.Age{Behind: n, FetchAge: 0, FetchKnown: true}
			}
		}
		keep := true
		if a.Behind > 0 {
			if ref, ok := checkout.DefaultRef(dir); ok {
				if tmp, cleanup, ok := checkout.MaterialiseAt(dir, ref); ok {
					rederived++
					// scan() wants root+repo; the materialised tree IS the repo.
					nf, still := scan(tmp, "")
					if !still || (nf.noCI == "" && len(nf.missing) == 0) {
						keep = false
						dropped2++
					} else {
						nf.repo, nf.behind = f.repo, a.Behind
						f = nf
					}
					cleanup()
				}
			}
		}
		if keep {
			survived = append(survived, f)
			ages = append(ages, a)
		}
	}
	found = survived
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

	if dropped2 > 0 {
		fmt.Fprintf(os.Stderr,
			"\n%d finding(s) dropped: re-derived against the remote default and no longer true\n"+
				"  (the working tree was behind; %d checkout(s) were re-scanned that way).\n", dropped2, rederived)
	}
	fmt.Fprint(os.Stderr, checkout.StalenessWarning(ages, *doFetch))

	// A scan that cannot read reports zero, and zero reads as good news. Say
	// what was actually looked at, always.
	fmt.Fprintf(os.Stderr, "\n%d repositories scanned, %d name an external tool in a test, %d have one their CI never installs\n",
		scanned, withTools, len(found))
	if n := benchOnly.Load(); n > 0 {
		fmt.Fprintf(os.Stderr, "%d tool(s) left out: only a Benchmark reaches them, and `go test` does not run those.\n", n)
	}
	if n := unparsedFiles.Load(); n > 0 {
		// Said out loud: those files were read by the old text search, which
		// is the instrument this replaces precisely because it cannot tell a
		// call from a string that looks like one.
		fmt.Fprintf(os.Stderr, "⚠ %d test file(s) did not parse and fell back to the text search.\n", n)
	}
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
	f.skipFatal = skipIsFatal(yaml)
	f.replay = replayable(yaml)
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
	// Read the package's test files TOGETHER. The LookPath is almost never in
	// the Test itself — it is in a helper — so a file-at-a-time view can say
	// which tool is wanted and never which kind of function wants it.
	files := map[string][]byte{}
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
		if strings.HasSuffix(p, "_test.go") {
			if b, err := os.ReadFile(p); err == nil {
				files[p] = b
			}
		}
		return nil
	})
	if len(files) == 0 {
		return nil, false
	}

	wanted, benchOnlyTools, dynamic, ok := reachability(files)
	if !ok {
		// A package that does not parse is not a package naming no tools. Fall
		// back to the per-file read, and count it so the report can say the
		// pass was not uniform.
		unparsedFiles.Add(1)
		seen := map[string]bool{}
		for _, b := range files {
			gated, _, dyn, ok := lookPaths(b)
			if !ok {
				continue
			}
			for name := range gated {
				if !ubiquitous[name] {
					seen[name] = true
				}
			}
			if dyn {
				dynamic = true
			}
		}
		return sorted(seen), dynamic
	}

	seen := map[string]bool{}
	for name := range wanted {
		if !ubiquitous[name] {
			seen[name] = true
		}
	}
	// ⛔ byBench is deliberately NOT reported. `go test` does not run
	// benchmarks, so a tool only a Benchmark reaches is one the workflow was
	// never going to need. See reach.go: go-tpm2/tpm2 and openweft/weft both
	// named swtpm through a helper, and only one of them had a Test behind it.
	benchOnly.Add(int64(len(benchOnlyTools)))
	return sorted(seen), dynamic
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
// nonZeroExit matches a shell line that ends the step in failure.
//
// `::error::` alone does NOT: it writes an annotation and the step still
// succeeds, so a guard built only from it is a guard that never refuses.
var nonZeroExit = regexp.MustCompile(`\bexit\s+[1-9][0-9]*\b`)

// skipIsFatal says whether a workflow turns a `--- SKIP` in the test output
// into a failing step.
//
// This tool's claim is about SILENCE. A test that names a tool nobody
// installed skips, a skip is green, and nobody ever learns. A run that exits
// non-zero on the literal string `--- SKIP` is not silent: it is green because
// the tool ran, or red, and red is redscan's subject, not this one.
//
// So this EXCLUDES a repository from the list and never puts one on it -- the
// same rule reach.go states for its call graph. Those repositories are still
// printed, under their own heading, because a finding that disappears without
// a word is indistinguishable from one that was never found.
//
// It exists because this tool matched a BINARY name against apt PACKAGE names
// and reported go-filesystems/cpio and go-filesystems/unarchive the day after
// their judges were installed and passing: `bsdcpio` comes from
// `libarchive-tools`, `7zz` from `7zip-standalone`. The alias table above now
// holds those two, and would have inherited the next mismatch; this asks the
// question the table cannot -- whether the failure could stay quiet.
func skipIsFatal(yaml string) bool {
	lines := strings.Split(yaml, "\n")
	for i, ln := range lines {
		if !strings.Contains(ln, "--- SKIP") {
			continue
		}
		// A `case *"--- SKIP"*) ...; exit 1 ;;` puts the test and the exit on
		// one line; an `if grep -q -- "--- SKIP"` spreads them over several.
		// Four lines covers both without reaching into the next step.
		for j := i; j < len(lines) && j <= i+4; j++ {
			if nonZeroExit.MatchString(lines[j]) {
				return true
			}
		}
	}
	return false
}

func installLines(yaml string) string {
	var sb strings.Builder
	for _, line := range strings.Split(yaml, "\n") {
		t := strings.TrimSpace(line)
		// ⛔ A COMMENT IS NOT AN INSTALL, and this is how that was learnt:
		// cloud-boot/docs gained a workflow whose comment reads
		//
		//	# ⛔ qemu-system-riscv64 is DELIBERATELY not installed here
		//
		// explaining, at length, why the judge cannot run. Every line of that
		// explanation sits inside a `run:` block, carries no YAML key, and so
		// survived the filter below -- and the repository dropped off this
		// tool's list. A sentence saying a tool is absent was read as the tool
		// being present, which is the worst direction for a scanner to be
		// wrong in, because nothing is printed when it happens.
		if strings.HasPrefix(t, "#") {
			continue
		}
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

// defaultRoot and findRepos now delegate: both lived here AND in the other
// working-tree sweep, and had begun to drift. The tests below keep pointing at
// these names, so they exercise the shared implementation.
func defaultRoot() string { return checkout.DefaultRoot() }

func findRepos(root, only string) ([]string, error) { return checkout.Repos(root, only) }
