// relverify checks that a tagged release is what its tag says it is: every
// published file listed in SHA256SUMS and matching it, every file attested by
// the repository's own workflow, every Go binary built from the tag's commit
// with an unmodified tree, and the binary for this machine answering --version
// with the tag. Every Go binary also has its toolchain reported (--min-go
// refuses an old one) and is scanned for known vulnerabilities (vuln.go).
//
//	relverify go-authn/authnd v0.10.2
//	relverify --wait 40m go-authn/bridge v0.23.0   # the release is still building
//
// It waits for the tag's workflow runs to finish first, and stops at once when
// there is nothing to wait for. It exists because the check was a zsh function
// with awk in it, and on 2026-10-11 that function waited on a release.yml that
// go-fileshare/fileshare does not have: a 404 every 45 seconds, forever, with
// nothing printed. A tag nothing builds is an answer, and this says it.
//
// The files go to the user cache directory (relverify/<owner>/<repo>/<tag>),
// never inside a git work tree: a release is binaries, and a binary inside a
// checkout is one `git add -A` from being committed.
//
// Exit status: 0 every check passed; 1 a check failed; 2 the release could not
// be checked at all (no release, no runs, gh failing).
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

// gh is the seam a test replaces.
var gh = fleet.GH

// errUnverifiable is a release that could not be checked, as opposed to one
// that failed a check: exit status 2 rather than 1.
var errUnverifiable = errors.New("cannot verify")

// osExit is the seam a test replaces.
var osExit = os.Exit

// errFailed is a release that was checked and failed a check: exit 1.
var errFailed = errors.New("FAILED")

const longHelp = `relverify checks that a tagged GitHub release is what its tag says it is.

It waits for the tag's workflow runs, downloads the release into a directory
of the run's own (the user cache directory by default, never inside a git work
tree), and holds every file to SHA256SUMS and to gh attestation verify. Every
Go binary, bare or inside a .tar.gz/.tgz/.zip, is held to its build
information (module version = the tag, vcs.revision = the tag's commit,
vcs.modified=false), has its toolchain reported (and refused below --min-go),
and is scanned against the Go vulnerability database the way
govulncheck -mode=binary does. Last, this machine's binary is run with
--version -- only once it has passed every other check.

A vulnerability found in a binary fails the file unless --allow-vuln names it;
an allowed one is still reported. --no-vuln skips the scan, and says so.

Exit status: 0 every check passed; 1 a check failed; 2 the release could not
be checked at all (no release, no runs, gh failing, the vulnerability database
unreachable).`

// RootCmd is relverify's command line.
func RootCmd() *cobra.Command {
	var (
		wait      time.Duration
		dir       string
		norun     bool
		minGo     string
		allowVuln []string
		noVuln    bool
		vulnDB    string
	)
	c := &cobra.Command{
		Use:           "relverify [flags] owner/repo tag",
		Short:         "Verify that a tagged GitHub release is what its tag says it is",
		Long:          longHelp,
		Example:       "  relverify go-authn/authnd v0.12.0\n  relverify --wait 40m --min-go go1.27.3 go-authn/bridge v0.23.0",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 || strings.Count(args[0], "/") != 1 {
				return errors.New("want two arguments: owner/repo tag")
			}
			return nil
		},
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		o := vulnOptions{skip: noVuln, db: vulnDB, allow: map[string]bool{}}
		if minGo != "" {
			v, err := normGo(minGo)
			if err != nil {
				return err
			}
			o.minGo = v
		}
		for _, id := range allowVuln {
			if !strings.HasPrefix(id, "GO-") {
				return fmt.Errorf("--allow-vuln %q is not a Go vulnerability ID (GO-YYYY-NNNN)", id)
			}
			o.allow[id] = true
		}
		repo, tag := args[0], args[1]
		ok, err := verify(cmd.OutOrStdout(), repo, tag, dir, wait, !norun, o)
		if err != nil {
			return fmt.Errorf("%s %s: %w", repo, tag, err)
		}
		if !ok {
			return errFailed
		}
		return nil
	}
	f := c.Flags()
	f.DurationVarP(&wait, "wait", "w", 30*time.Minute, "how long to wait for the tag's workflow runs to finish")
	f.StringVarP(&dir, "dir", "d", "", "where to download the release (default: the user cache directory)")
	f.BoolVarP(&norun, "norun", "n", false, "do not run this machine's binary with --version")
	f.StringVar(&minGo, "min-go", "", "fail a Go binary built with a toolchain older than this (e.g. go1.27.3)")
	f.StringArrayVar(&allowVuln, "allow-vuln", nil, "accept this vulnerability ID (repeatable); it is still reported")
	f.BoolVar(&noVuln, "no-vuln", false, "do not scan the binaries for known vulnerabilities")
	f.StringVar(&vulnDB, "vuln-db", defaultVulnDB, "the Go vulnerability database (https:// or file://)")
	return c
}

// oldLongFlags are the flags relverify v0.22.0 took with one dash, through
// the flag package. pflag reads "-wait" as -w -a -i -t, so they are rewritten
// to "--wait" before parsing, with a note -- for one release, then dropped.
var oldLongFlags = []string{"wait", "dir", "norun"}

// legacyArgs rewrites the single-dash spellings of oldLongFlags, up to a "--".
func legacyArgs(args []string, warn io.Writer) []string {
	out := make([]string, 0, len(args))
	for i, a := range args {
		if a == "--" {
			return append(out, args[i:]...)
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(a, "-"), "=")
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && slices.Contains(oldLongFlags, name) {
			fmt.Fprintf(warn, "relverify: -%s is deprecated, write --%s (the single-dash spelling goes in the next release)\n", name, name)
			a = "-" + a
		}
		out = append(out, a)
	}
	return out
}

// execute runs the command and turns its error into the exit status.
func execute(c *cobra.Command, args []string) int {
	c.SetArgs(legacyArgs(args, c.ErrOrStderr()))
	err := c.Execute()
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errFailed):
		return 1
	case errors.Is(err, errUnverifiable):
		fmt.Fprintf(c.ErrOrStderr(), "relverify: %v\n", err)
		return 2
	}
	fmt.Fprintf(c.ErrOrStderr(), "relverify: %v\n", err)
	return 2 // usage
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	osExit(execute(RootCmd(), os.Args[1:]))
}

func verify(w io.Writer, repo, tag, dir string, wait time.Duration, runHost bool, o vulnOptions) (bool, error) {
	commit, err := tagCommit(repo, tag)
	if err != nil {
		return false, err
	}
	fmt.Fprintf(w, "%s %s -> %s\n", repo, tag, commit)

	runs, err := waitForRuns(w, repo, tag, commit, wait)
	if err != nil {
		return false, err
	}
	ok := true
	for _, r := range runs {
		fmt.Fprintf(w, "  run  %-24s %s\n", r.Name, r.Conclusion)
		if r.Conclusion != "success" && r.Conclusion != "skipped" {
			ok = false
		}
	}

	if dir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return false, fmt.Errorf("%w: no cache directory: %v", errUnverifiable, err)
		}
		dir = filepath.Join(cache, "relverify", repo, tag)
	}
	if tree := insideWorkTree(dir); tree != "" {
		return false, fmt.Errorf("%w: %s is inside the git work tree %s, where a downloaded binary could be committed", errUnverifiable, dir, tree)
	}
	if !released(repo, tag) {
		names := make([]string, 0, len(runs))
		for _, r := range runs {
			names = append(names, r.Name)
		}
		return false, fmt.Errorf("%w: no GitHub release for %s; the tag's runs (%s) published none", errUnverifiable, tag, strings.Join(names, ", "))
	}
	final := dir
	dir, err = runDir(final)
	if err != nil {
		return false, err
	}
	published := false
	defer func() {
		if !published {
			os.RemoveAll(dir)
		}
	}()
	if _, err := gh("release", "download", tag, "-R", repo, "-D", dir); err != nil {
		if strings.Contains(err.Error(), "no assets") {
			return false, fmt.Errorf("%w: the release for %s publishes no files: release notes only", errUnverifiable, tag)
		}
		return false, fmt.Errorf("%w: download: %v", errUnverifiable, err)
	}
	files, err := checkFiles(w, repo, tag, commit, dir, o)
	if err != nil {
		return false, err
	}
	anyGo := false
	seen := map[string]bool{}
	for _, f := range files {
		if !f.ok() {
			ok = false
		}
		anyGo = anyGo || f.goBinary
		for _, id := range f.allowedIDs {
			seen[id] = true
		}
	}
	switch {
	case anyGo && o.skip:
		fmt.Fprintf(w, "  --   vulnerabilities NOT scanned (--no-vuln)\n")
	case anyGo:
		fmt.Fprintf(w, "  note %s\n", binaryCaveat)
	}
	for _, id := range sortedSet(o.allow) {
		if !seen[id] && !o.skip {
			fmt.Fprintf(w, "  note --allow-vuln %s: not found in any binary of this release\n", id)
		}
	}
	// ⛔ SCAN FIRST, RUN LAST. The host binary is executed only after
	// checkFiles has held it to every check, the vulnerability scan included:
	// runHostBinary refuses a file that is not ok().
	if runHost {
		if !runHostBinary(w, dir, tag, files) {
			ok = false
		}
	}
	if err := publishRun(dir, final); err != nil {
		published = true // kept where it is, and said so
		fmt.Fprintf(w, "  kept %s (%v)\n", dir, err)
	} else {
		published = true
		fmt.Fprintf(w, "  kept %s\n", final)
	}
	verdict := "verified"
	if !ok {
		verdict = "FAILED"
	}
	fmt.Fprintf(w, "%s %s: %d file(s), %s\n", repo, tag, len(files), verdict)
	return ok, nil
}

// runDir is a directory of this run's own, beside final.
//
// ⛔ Two runs on one release used to share final: each removed it and
// downloaded into it, so the second emptied the first mid-check, and the first
// reported "no such file" for files the release did publish (2026-10-18,
// fileshare v0.30.0, two runs a minute apart). A run now downloads and checks
// in a directory nobody else knows, and only the finished result is moved to
// final -- where a later reader, a page's transcript, expects it.
func runDir(final string) (string, error) {
	parent := filepath.Dir(final)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, filepath.Base(final)+".run-*")
}

// publishRun replaces final with a finished run. Two runs finishing together
// each leave a complete download there, which is all a reader needs.
func publishRun(run, final string) error {
	if err := os.RemoveAll(final); err != nil {
		return err
	}
	return os.Rename(run, final)
}

// tagCommit is the commit a tag names, through an annotated tag if it is one.
func tagCommit(repo, tag string) (string, error) {
	var ref struct {
		Object struct{ Type, SHA string }
	}
	if err := ghJSON(&ref, "api", "repos/"+repo+"/git/ref/tags/"+tag); err != nil {
		return "", fmt.Errorf("%w: no tag %s: %v", errUnverifiable, tag, err)
	}
	for range 4 {
		if ref.Object.Type != "tag" {
			break
		}
		if err := ghJSON(&ref, "api", "repos/"+repo+"/git/tags/"+ref.Object.SHA); err != nil {
			return "", fmt.Errorf("%w: annotated tag %s: %v", errUnverifiable, tag, err)
		}
	}
	if ref.Object.Type != "commit" {
		return "", fmt.Errorf("%w: %s names a %s, not a commit", errUnverifiable, tag, ref.Object.Type)
	}
	return ref.Object.SHA, nil
}

type run struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HeadBranch string `json:"head_branch"`
	HeadSHA    string `json:"head_sha"`
}

// sleep and now are seams a test replaces.
var (
	sleep = time.Sleep
	now   = time.Now
)

// appear is how long a tag may go without any run before that is the answer:
// a push starts its workflows within seconds.
const appear = 3 * time.Minute

// waitForRuns waits until every workflow run for the tag has finished, and
// says so when there are none.
func waitForRuns(w io.Writer, repo, tag, commit string, wait time.Duration) ([]run, error) {
	start := now()
	said := false
	for {
		var page struct {
			Runs []run `json:"workflow_runs"`
		}
		if err := ghJSON(&page, "api", "repos/"+repo+"/actions/runs?branch="+tag+"&per_page=100"); err != nil {
			return nil, fmt.Errorf("%w: the tag's runs: %v", errUnverifiable, err)
		}
		runs := tagRuns(page.Runs, tag, commit)
		pending := 0
		for _, r := range runs {
			if r.Status != "completed" {
				pending++
			}
		}
		waited := now().Sub(start)
		// No run but a release already there: published some other way. Its
		// files are checked all the same -- an attestation only the
		// repository's workflow can make is what will say how it was built.
		if len(runs) == 0 && released(repo, tag) {
			fmt.Fprintf(w, "  no workflow run for %s; checking the release as published\n", tag)
			return nil, nil
		}
		switch {
		case len(runs) == 0 && waited >= appear:
			return nil, fmt.Errorf("%w: no workflow ran for %s in %s: nothing builds this tag", errUnverifiable, tag, appear)
		case len(runs) > 0 && pending == 0:
			return runs, nil
		case waited >= wait:
			return nil, fmt.Errorf("%w: %d run(s) for %s still not finished after %s", errUnverifiable, pending, tag, wait)
		}
		if !said {
			fmt.Fprintf(w, "  waiting for %s's workflow runs\n", tag)
			said = true
		}
		sleep(30 * time.Second)
	}
}

func released(repo, tag string) bool {
	_, err := gh("release", "view", tag, "-R", repo, "--json", "tagName")
	return err == nil
}

// tagRuns keeps the runs of this tag at this commit: a tag moved after a
// failed release leaves the old commit's runs under the same name.
func tagRuns(all []run, tag, commit string) []run {
	var out []run
	for _, r := range all {
		if r.HeadBranch == tag && r.HeadSHA == commit {
			out = append(out, r)
		}
	}
	return out
}

func ghJSON(v any, args ...string) error {
	b, err := gh(args...)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// insideWorkTree returns the work tree dir is in, or "".
func insideWorkTree(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for d := abs; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// A file is one published asset and what was found about it.
type file struct {
	name     string
	listed   bool   // named in SHA256SUMS
	sum      string // "" when it matched, else what went wrong
	attested string // "" when gh attestation verify accepted it
	build    string // "" for a correct Go binary or a file that is not one
	goBinary bool
	// inner are the Go binaries an archive holds, by their names in it.
	inner []string
	// hostName and hostData are the Go binary inside this machine's archive,
	// kept to be run once every check has passed.
	hostName string
	hostData []byte
	// toolchains is each Go binary's buildinfo.GoVersion, prefixed by its
	// name in the archive for a member: always reported.
	toolchains []string
	toolchain  string   // "" when every toolchain is at least --min-go
	vulns      []string // vulnerabilities found and not allowed
	allowed    []string // vulnerabilities found and allowed by --allow-vuln
	allowedIDs []string
	scanned    bool // the vulnerability scan ran on every Go binary in it
}

func (f file) ok() bool {
	return f.listed && f.sum == "" && f.attested == "" && f.build == "" &&
		f.toolchain == "" && len(f.vulns) == 0
}

// scanTimeout bounds one binary's vulnerability scan.
const scanTimeout = 10 * time.Minute

// judgeGo holds one Go binary -- path on disk, or data read out of an archive
// -- to --min-go and to the vulnerability scan. label is its name inside an
// archive, "" for a bare binary.
func judgeGo(f *file, dir, path, label string, data []byte, goVersion string, o vulnOptions) error {
	tc := cmp(toolchainOf(goVersion), "(none)")
	if label != "" {
		tc = label + " " + tc
	}
	f.toolchains = append(f.toolchains, tc)
	if p := olderThan(goVersion, o.minGo); p != "" {
		if label != "" {
			p = label + ": " + p
		}
		if f.toolchain != "" {
			p = f.toolchain + "; " + p
		}
		f.toolchain = p
	}
	if o.skip {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
	defer cancel()
	var problems, allowed []string
	var err error
	if data != nil {
		problems, allowed, err = scanFile(ctx, dir, label, data, o)
	} else {
		problems, allowed, err = checkVulns(ctx, path, label, o)
	}
	if err != nil {
		return err
	}
	f.scanned = true
	f.vulns = append(f.vulns, problems...)
	f.allowed = append(f.allowed, allowed...)
	for _, a := range allowed {
		id := a
		if label != "" {
			id = strings.TrimPrefix(id, label+": ")
		}
		id, _, _ = strings.Cut(id, " ")
		f.allowedIDs = append(f.allowedIDs, id)
	}
	return nil
}

// checkFiles holds every downloaded file to SHA256SUMS, its attestation and,
// for a Go binary, its build information, its toolchain and the vulnerability
// database.
func checkFiles(w io.Writer, repo, tag, commit, dir string, o vulnOptions) ([]file, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return nil, fmt.Errorf("%w: the release has no SHA256SUMS", errUnverifiable)
	}
	sums, err := parseSums(raw)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []file
	for _, e := range entries {
		if e.Name() == "SHA256SUMS" || e.IsDir() {
			continue
		}
		f := file{name: e.Name()}
		path := filepath.Join(dir, e.Name())
		want, listed := sums[e.Name()]
		f.listed = listed
		if listed {
			if got, err := sha256File(path); err != nil {
				f.sum = err.Error()
			} else if got != want {
				f.sum = "sha256 " + got + ", SHA256SUMS says " + want
			}
		}
		if _, err := gh("attestation", "verify", path, "-R", repo); err != nil {
			f.attested = firstLine(err.Error())
		}
		if bi, err := buildinfo.ReadFile(path); err == nil {
			f.goBinary = true
			f.build = checkBuild(bi.Main.Version, bi.Settings, tag, commit)
			if err := judgeGo(&f, dir, path, "", nil, bi.GoVersion, o); err != nil {
				return nil, err
			}
		} else if isArchive(e.Name()) {
			if err := checkArchive(&f, dir, path, tag, commit, o); err != nil {
				return nil, err
			}
		}
		files = append(files, f)
		report(w, f)
	}
	// A sum for a file that was not published is a release that lost a file.
	for _, name := range sortedKeys(sums) {
		if !slices.ContainsFunc(files, func(f file) bool { return f.name == name }) {
			f := file{name: name, listed: true, sum: "listed in SHA256SUMS, not published"}
			files = append(files, f)
			report(w, f)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: the release publishes nothing but SHA256SUMS", errUnverifiable)
	}
	return files, nil
}

// checkArchive holds every Go binary inside an archive to the build checks.
// An archive that cannot be read fails: a release file nobody can open is not
// one anybody can install.
func checkArchive(f *file, dir, path, tag, commit string, o vulnOptions) error {
	ms, err := goBinariesIn(path)
	if err != nil {
		f.build = "archive: " + err.Error()
		return nil
	}
	var problems []string
	for _, m := range ms {
		f.goBinary = true
		f.inner = append(f.inner, m.name)
		if p := checkBuild(m.bi.Main.Version, m.bi.Settings, tag, commit); p != "" {
			problems = append(problems, m.name+": "+p)
		}
		if err := judgeGo(f, dir, "", m.name, m.data, m.bi.GoVersion, o); err != nil {
			return err
		}
		if isHost(f.name) && f.hostData == nil {
			f.hostName, f.hostData = m.name, m.data
		}
	}
	f.build = strings.Join(problems, "; ")
	return nil
}

func report(w io.Writer, f file) {
	if f.goBinary {
		fmt.Fprintf(w, "  go   %-32s %s\n", f.name, strings.Join(f.toolchains, ", "))
	}
	for _, a := range f.allowed {
		fmt.Fprintf(w, "  allow %-31s %s, accepted by --allow-vuln\n", f.name, a)
	}
	if f.ok() {
		what := "sum, attestation"
		switch {
		case len(f.inner) > 0:
			what += ", build info of " + strings.Join(f.inner, ", ")
		case f.goBinary:
			what += ", build info"
		case isArchive(f.name):
			what += " (no Go binary inside)"
		}
		if f.goBinary {
			switch {
			case f.scanned && len(f.allowed) > 0:
				what += fmt.Sprintf(", no vulnerability but the %d allowed above", len(f.allowed))
			case f.scanned:
				what += ", no known vulnerability"
			default:
				what += ", vulnerabilities not scanned"
			}
		}
		fmt.Fprintf(w, "  ok   %-32s %s\n", f.name, what)
		return
	}
	problems := []string{
		map[bool]string{false: "not listed in SHA256SUMS"}[f.listed],
		f.sum, f.attested, f.build, f.toolchain,
	}
	for _, v := range f.vulns {
		problems = append(problems, "vulnerability "+v)
	}
	for _, p := range problems {
		if p != "" {
			fmt.Fprintf(w, "  FAIL %-32s %s\n", f.name, p)
		}
	}
}

// parseSums reads sha256sum's format: 64 hex digits, a space, a space or a
// star, a file name. A name with a path in it is refused: a release asset has
// none, and one here would hash a file outside the download.
func parseSums(b []byte) (map[string]string, error) {
	sums := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			continue
		}
		if len(line) < 67 || line[64] != ' ' || (line[65] != ' ' && line[65] != '*') {
			return nil, fmt.Errorf("SHA256SUMS line %d is not \"<sha256>  <name>\"", n)
		}
		sum, name := strings.ToLower(line[:64]), line[66:]
		if _, err := hex.DecodeString(sum); err != nil {
			return nil, fmt.Errorf("SHA256SUMS line %d: %q is not hex", n, sum)
		}
		if name != filepath.Base(name) || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			return nil, fmt.Errorf("SHA256SUMS line %d names a path, %q", n, name)
		}
		if _, dup := sums[name]; dup {
			return nil, fmt.Errorf("SHA256SUMS lists %q twice", name)
		}
		sums[name] = sum
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(sums) == 0 {
		return nil, errors.New("SHA256SUMS lists nothing")
	}
	return sums, nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// checkBuild says what is wrong with a Go binary's build information, or "".
// It wants the module at the tag, the tag's commit, and a clean tree: a
// binary whose tree was modified is not the tag's code, whatever it is called.
func checkBuild(version string, settings []debug.BuildSetting, tag, commit string) string {
	var problems []string
	if version != tag {
		problems = append(problems, fmt.Sprintf("module version %s", version))
	}
	got := map[string]string{}
	for _, s := range settings {
		got[s.Key] = s.Value
	}
	switch rev, ok := got["vcs.revision"]; {
	case !ok:
		problems = append(problems, "no vcs.revision")
	case rev != commit:
		problems = append(problems, "vcs.revision "+rev)
	}
	if m := got["vcs.modified"]; m != "false" {
		problems = append(problems, "vcs.modified="+cmp(m, "(absent)"))
	}
	if len(problems) == 0 {
		return ""
	}
	return "build info: " + strings.Join(problems, ", ")
}

func cmp(s, otherwise string) string {
	if s == "" {
		return otherwise
	}
	return s
}

// isHost says whether a published file is this machine's: its name holds
// GOOS and GOARCH, joined the way one release or another joins them --
// tool-darwin-arm64, tool_v1.2.3_darwin_arm64.tar.gz -- and followed by
// nothing, an extension, or another field. arm must not match arm64, nor
// amd64 amd64v3.
func isHost(name string) bool {
	for _, sep := range []string{"-", "_"} {
		key := sep + runtime.GOOS + sep + runtime.GOARCH
		for rest := name; ; {
			i := strings.Index(rest, key)
			if i < 0 {
				break
			}
			rest = rest[i+len(key):]
			if rest == "" || rest[0] == '.' || rest[0] == '_' || rest[0] == '-' {
				return true
			}
		}
	}
	return false
}

// runHostBinary runs this machine's binary with --version and wants the tag
// in what it prints. It runs only a file every other check accepted -- for an
// archive, the Go binary read out of it, written under its base name alone so
// that a member called ../../x lands nowhere but here.
func runHostBinary(w io.Writer, dir, tag string, files []file) bool {
	i := slices.IndexFunc(files, func(f file) bool { return isHost(f.name) && f.goBinary })
	if i < 0 {
		fmt.Fprintf(w, "  --   no %s/%s Go binary to run\n", runtime.GOOS, runtime.GOARCH)
		return true
	}
	f := files[i]
	if !f.ok() {
		fmt.Fprintf(w, "  --   %s not run: it failed a check above\n", f.name)
		return false
	}
	path := filepath.Join(dir, f.name)
	if f.hostData != nil {
		host := filepath.Join(dir, ".host")
		if err := os.MkdirAll(host, 0o755); err != nil {
			fmt.Fprintf(w, "  FAIL %-32s %v\n", f.name, err)
			return false
		}
		path = filepath.Join(host, filepath.Base(filepath.Clean("/"+f.hostName)))
		if err := os.WriteFile(path, f.hostData, 0o755); err != nil {
			fmt.Fprintf(w, "  FAIL %-32s %v\n", f.name, err)
			return false
		}
	}
	if err := os.Chmod(path, 0o755); err != nil {
		fmt.Fprintf(w, "  FAIL %-32s %v\n", f.name, err)
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	line := firstLine(string(out))
	if err != nil || !saysVersion(line, tag) {
		fmt.Fprintf(w, "  FAIL %-32s --version: %q %v\n", f.name, line, cmpErr(err))
		return false
	}
	fmt.Fprintf(w, "  ok   %-32s --version: %s\n", f.name, line)
	return true
}

func cmpErr(err error) string {
	if err == nil {
		return ""
	}
	return "(" + err.Error() + ")"
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// saysVersion is whether a --version line names this tag, with or without the
// leading `v`, as a WORD.
//
// ⛔ THE `v` BELONGS TO THE TAG, NOT TO THE VERSION. Until 2026-10-20 this
// was `strings.Contains(line, tag)`, which demanded it in the printed output
// — and the fleet does not agree about it. go-pkgx/pkgx's own release
// workflow compares `--version` against `${TAG#v}` on purpose, so `pkgs
// --version` prints "pkgs 0.23.2" and a v0.23.2 release of it was reported
// FAILED by this tool while being correct by the rule its own CI enforces.
// go-pkgx/bk prints "bk v0.23.2". Both are ordinary; a verifier that insists
// on one of them is borrowing a convention rather than checking a fact.
//
// ⛔ AND THE OLD CHECK WAS LOOSE IN THE OTHER DIRECTION, which is why this
// is a word match and not a second Contains: "pkgs 0.23.25" CONTAINS
// "0.23.2", so a binary stamped with a different version passed. Fixing the
// false negative without fixing that would have traded a tool that cries
// wolf for one that nods along.
func saysVersion(line, tag string) bool {
	bare := strings.TrimPrefix(tag, "v")
	if bare == "" {
		return false
	}
	for _, f := range strings.FieldsFunc(line, func(r rune) bool {
		// A version is delimited by space or by the punctuation that
		// surrounds it in the shapes seen here -- "fileshare version 0.29.2
		// (abcdef)", "pkgs 0.23.2," -- never by '.' or '-', which are part
		// of a semantic version and of its prerelease.
		return r == ' ' || r == '\t' || r == '(' || r == ')' ||
			r == ',' || r == ';' || r == ':' || r == '"' || r == '\'' ||
			r == '[' || r == ']'
	}) {
		if f == tag || f == bare {
			return true
		}
	}
	return false
}

func sortedSet(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
