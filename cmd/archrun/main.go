// Command archrun cross-compiles every test binary of a Go repository for one
// architecture, copies each to a remote machine of that architecture over ssh,
// runs it there under a timeout and a memory limit, and reports PASS or FAIL per
// package.
//
// ⛔ WHY THIS EXISTS. GitHub has no POWER or Z runners, so CI covers ppc64le and
// s390x under qemu-user at best, and qemu masks real SIGILL, SIGBUS and
// wrong-output bugs (a POWER8 SIGILL, an RVV SIGBUS, both found only on silicon).
// The real hosts have no usable Go, so the recipe is always the same: `go test
// -c` for the target, scp, run, delete. It was done by hand, per package, in
// shell loops that guard-bash refuses; nothing in the fleet did it for a whole
// repository.
//
// ⛔ A COMPILED TEST BINARY DOES NOT CARRY ITS FIXTURES: the package's testdata
// directory is copied beside it, and the binary runs with that directory as its
// working directory, as `go test` would. A test that resolves files UP from its
// package (go.mod, ../../scripts) is not served by this and will say so by
// failing; so will a test that shells out to `go`, which the host lacks.
//
// ⛔ THE HOST IS CHECKED BEFORE ANYTHING RUNS: `uname -m` must be the machine
// that --arch names, so a run labelled s390x cannot silently happen on the wrong
// box. Binaries run with `-race` off (it needs cgo on these targets), so a pass
// says nothing about data races.
//
// ⛔ A BUILD FAILURE IS NOT A TEST FAILURE, and neither is an ssh failure: the
// statuses are PASS, FAIL, TIMEOUT, BUILD, SKIP (no test file for this target)
// and ERROR (the host could not be reached or the copy failed). ERROR means the
// question was not answered, never that the code is fine.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

// uname -m for each GOARCH this command accepts.
var machines = map[string]string{
	"s390x":    "s390x",
	"ppc64le":  "ppc64le",
	"ppc64":    "ppc64",
	"riscv64":  "riscv64",
	"loong64":  "loongarch64",
	"arm64":    "aarch64",
	"amd64":    "x86_64",
	"386":      "i686",
	"arm":      "armv7l",
	"mips64le": "mips64",
}

var (
	reHost = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]*$`)
	reDir  = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	reMem  = regexp.MustCompile(`^[0-9]+(B|KiB|MiB|GiB|TiB)?$`)

	reBenchTime = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?(ns|us|ms|s|m|x)$`)
	outCap      = 4 << 20
	tailCap     = 4 << 10
)

// The seams, replaced by tests and by nothing in main.
var (
	now   = time.Now
	sleep = time.Sleep
	exe   = execute
)

// result is one package's outcome, as printed and as written to --report.
type result struct {
	Package  string  `json:"package"`
	Status   string  `json:"status"`
	Seconds  float64 `json:"seconds"`
	Detail   string  `json:"detail,omitempty"`
	Output   string  `json:"output,omitempty"`
	BenchOut string  `json:"-"`
}

// report is the whole run.
type report struct {
	Host      string   `json:"host"`
	Arch      string   `json:"arch"`
	Machine   string   `json:"machine"`
	StealPct  *float64 `json:"steal_percent,omitempty"`
	Load      string   `json:"load,omitempty"`
	Mode      string   `json:"mode"`
	Results   []result `json:"results"`
	StartedAt string   `json:"started_at"`
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

type options struct {
	host, arch, clone, remoteDir, memlimit, work, reportPath string
	port                                                     int
	timeout                                                  time.Duration
	retries                                                  int
	bench, benchtime                                         string
	only, skip                                               *regexp.Regexp
	keep                                                     bool
}

// RootCmd is the command, driven by the tests through cli.Drive.
func RootCmd() *cobra.Command {
	c := cli.New("archrun --host HOST --arch GOARCH [flags] <clone>...",
		"Cross-compile a repository's tests, run them on a real remote machine",
		`Lists the packages of <clone> that have tests for the target, cross-compiles
each test binary (GOOS=linux GOARCH=<arch> CGO_ENABLED=0 go test -c), copies it
and its testdata to HOST over ssh, runs it there with GOMEMLIMIT and a timeout,
collects PASS or FAIL per package, and deletes what it copied.

HOST is anything ssh accepts (an alias, or user@host); --port sets the port.
ssh runs with BatchMode, so a key that needs a prompt fails instead of hanging.
A connection that fails (a banner timeout is common on a busy shared host) is
retried --retries times before the package is called ERROR.

Several clones run in turn over one shared ssh connection (a "== clone" line
precedes each; --report FILE.json becomes FILE.<clone>.json per clone); exit 1
if any package of any clone did not pass, else 2 if any could not be run.

With --bench REGEXP the binaries run as benchmarks (-test.run '^$' -test.bench
REGEXP -test.benchtime T) and the raw output is written to --report's directory,
one file per package, for benchstat; the status is still PASS or FAIL.

The remote CPU steal share over the whole run is printed (from /proc/stat), since
the LPARs these hosts are carved from are shared: a time is not a measurement
without it.

exit 0 every package PASSed or had no test file for this target
exit 1 a package FAILed, TIMED OUT, or did not BUILD
exit 2 it refuses to decide: a missing flag, the host unreachable or not the
       machine --arch names, a copy that failed`)
	c.Args = cobra.MinimumNArgs(1)
	var o options
	var only, skip string
	f := c.Flags()
	f.StringVar(&o.host, "host", "", "ssh destination (required)")
	f.StringVar(&o.arch, "arch", "", "GOARCH of the host: s390x, ppc64le, ... (required)")
	f.IntVar(&o.port, "port", 0, "ssh port (default: ssh's own)")
	f.DurationVar(&o.timeout, "timeout", 10*time.Minute, "per-package run limit")
	f.StringVar(&o.memlimit, "memlimit", "2GiB", "GOMEMLIMIT for each remote run")
	f.StringVar(&o.remoteDir, "remote-dir", "", "scratch directory on the host (default archrun-<timestamp>, relative to $HOME)")
	f.BoolVar(&o.keep, "keep", false, "leave the remote directory in place")
	f.StringVar(&o.work, "work", "", "local directory for the binaries (default ~/.cache/archrun)")
	f.StringVar(&o.reportPath, "report", "", "write the JSON report here (benchmark output goes beside it)")
	f.IntVar(&o.retries, "retries", 3, "attempts per ssh/scp when the connection itself fails")
	f.StringVar(&o.bench, "bench", "", "run benchmarks matching this regexp instead of the tests")
	f.StringVar(&o.benchtime, "benchtime", "200ms", "-test.benchtime for --bench")
	f.StringVar(&only, "only", "", "only packages whose import path matches this regexp")
	f.StringVar(&skip, "skip", "", "skip packages whose import path matches this regexp")

	c.RunE = func(c *cobra.Command, args []string) error {
		if cli.HelpRequested(c) {
			return nil
		}
		var err error
		if o.only, err = optRe("--only", only); err != nil {
			return err
		}
		if o.skip, err = optRe("--skip", skip); err != nil {
			return err
		}
		if err := o.validate(); err != nil {
			return err
		}
		return runAll(c, o, args)
	}
	return c
}

var reUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// runAll runs each clone in turn over one shared connection and answers with
// the worst outcome: a finding (1) outranks "no answer" (2), because a package
// that failed is something to act on whatever else could not be run.
func runAll(c *cobra.Command, o options, clones []string) error {
	defer func() {
		r := &runner{o: o, ctx: context.Background()}
		if len(clones) > 1 && !o.keep {
			r.ssh(time.Minute, "rm -rf "+o.remoteDir)
		}
		if m := r.muxOpts(); m != nil {
			exe(context.Background(), nil, "", "ssh", append(append([]string{}, m...), "-O", "exit", o.host)...)
		}
	}()
	var found, unanswered error
	for _, cl := range clones {
		oo := o
		oo.clone = cl
		if len(clones) > 1 {
			base := reUnsafe.ReplaceAllString(filepath.Base(filepath.Clean(cl)), "_")
			oo.remoteDir = o.remoteDir + "/" + base
			if o.reportPath != "" {
				oo.reportPath = strings.TrimSuffix(o.reportPath, filepath.Ext(o.reportPath)) + "." + base + ".json"
			}
			fmt.Fprintf(cli.Out(c), "== %s\n", cl)
		}
		err := run(c, oo)
		if err != nil && len(clones) > 1 {
			// Only the worst error reaches cobra; each clone's reason is said here.
			fmt.Fprintf(cli.Err(c), "archrun: %s: %v\n", cl, err)
		}
		var e *cli.Exit
		switch {
		case err == nil:
		case errors.As(err, &e) && e.Code == 1:
			found = err
		default:
			unanswered = err
		}
	}
	if found != nil {
		return found
	}
	return unanswered
}

func optRe(name, s string) (*regexp.Regexp, error) {
	if s == "" {
		return nil, nil
	}
	re, err := regexp.Compile(s)
	if err != nil {
		return nil, cli.Unanswerable("%s is not a regexp: %v", name, err)
	}
	return re, nil
}

func (o *options) validate() error {
	if !reHost.MatchString(o.host) {
		return cli.Unanswerable("--host is required and must be a plain ssh destination, got %q", o.host)
	}
	if _, ok := machines[o.arch]; !ok {
		return cli.Unanswerable("--arch is required and must be one of the GOARCH values this command knows, got %q", o.arch)
	}
	if o.port < 0 || o.port > 65535 {
		return cli.Unanswerable("--port %d is not a port", o.port)
	}
	if o.timeout < time.Second {
		return cli.Unanswerable("--timeout %s is shorter than a second", o.timeout)
	}
	if !reMem.MatchString(o.memlimit) {
		return cli.Unanswerable("--memlimit %q is not a Go memory limit (e.g. 2GiB)", o.memlimit)
	}
	if o.retries < 1 || o.retries > 20 {
		return cli.Unanswerable("--retries must be 1..20, got %d", o.retries)
	}
	if o.remoteDir == "" {
		o.remoteDir = "archrun-" + strconv.FormatInt(now().Unix(), 10)
	}
	if !reDir.MatchString(o.remoteDir) || strings.HasPrefix(o.remoteDir, "/") && len(o.remoteDir) < 5 || strings.Contains(o.remoteDir, "..") {
		return cli.Unanswerable("--remote-dir %q is not a safe scratch path", o.remoteDir)
	}
	// --bench is single-quoted on the remote side, so only a quote or a line
	// break can leave the quotes; --benchtime is a duration or Nx, nothing else.
	if strings.ContainsAny(o.bench, "'\n\x00") || !reBenchTime.MatchString(o.benchtime) {
		return cli.Unanswerable("--bench must not contain a quote or line break, --benchtime must be like 200ms or 5x")
	}
	if o.work == "" {
		// ~/.cache, not the platform's config or cache directory: on macOS
		// those are long and one has a space ("Application Support"), and a
		// Unix socket path holds 104 bytes. These are rebuildable binaries.
		d, err := os.UserHomeDir()
		if err != nil {
			return cli.Unanswerable("no --work given and no home directory: %v", err)
		}
		o.work = filepath.Join(d, ".cache", "archrun")
	}
	if inGit(o.work) {
		return cli.Unanswerable("--work %s is inside a git directory: binaries do not belong in a repository", o.work)
	}
	return nil
}

// inGit reports whether any component of the path is a .git directory.
func inGit(p string) bool {
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		if part == ".git" {
			return true
		}
	}
	return false
}

// cmdResult is what the exec seam answers.
type cmdResult struct {
	out  []byte
	code int // the exit status; -1 when the process did not start or was killed
}

// execute runs a command with a deadline and returns its combined output,
// capped, and its exit status.
func execute(ctx context.Context, env []string, dir string, name string, args ...string) cmdResult {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Stdin = nil
	buf := &capped{limit: outCap}
	cmd.Stdout, cmd.Stderr = buf, buf
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = -1
			fmt.Fprintf(buf, "\n%s: %v\n", name, err)
		}
	}
	return cmdResult{out: buf.b.Bytes(), code: code}
}

type capped struct {
	b     bytes.Buffer
	limit int
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.limit - c.b.Len(); room > 0 {
		if len(p) > room {
			c.b.Write(p[:room])
		} else {
			c.b.Write(p)
		}
	}
	return len(p), nil
}

type runner struct {
	o   options
	ctx context.Context
}

// muxOpts shares ONE ssh connection between every call of a run. A busy shared
// host throttles new connections (a banner timeout is the usual symptom, and
// several agents on one machine make it worse), and a run makes about five calls
// per package: opening the connection once is what keeps it going. The socket
// path must fit in 104 bytes, so a long --work disables the sharing rather than
// breaking ssh.
func (r *runner) muxOpts() []string {
	p := filepath.Join(r.o.work, "m-%C")
	// %C expands to 40 hex characters and ssh appends a 17-character suffix
	// while it sets the master up: measure the path as ssh will build it.
	if len(p)-2+40+17 > 104 || strings.ContainsAny(p, " \t\"'\\") {
		return nil
	}
	return []string{"-o", "ControlMaster=auto", "-o", "ControlPath=" + p, "-o", "ControlPersist=120"}
}

func (r *runner) sshArgs(extra ...string) []string {
	// -x: some hosts (cfarm112) print "X11 forwarding request failed" into the
	// output otherwise, and a program is run here, not a desktop.
	a := append([]string{"-x", "-o", "BatchMode=yes", "-o", "ConnectTimeout=30", "-o", "ServerAliveInterval=30"}, r.muxOpts()...)
	if r.o.port != 0 {
		a = append(a, "-p", strconv.Itoa(r.o.port))
	}
	return append(a, extra...)
}

func (r *runner) scpArgs(extra ...string) []string {
	// IPQoS=none: scp to the z17 host stalls with the default QoS marking.
	a := append([]string{"-q", "-o", "BatchMode=yes", "-o", "ConnectTimeout=30", "-o", "IPQoS=none"}, r.muxOpts()...)
	if r.o.port != 0 {
		a = append(a, "-P", strconv.Itoa(r.o.port))
	}
	return append(a, extra...)
}

// withRetry runs an ssh or scp command again while it ends with status 255, which
// both use for "the connection itself failed" (banner timeout, refused, reset).
// Any other status is the remote command's own and is never retried.
func (r *runner) withRetry(limit time.Duration, name string, args ...string) cmdResult {
	var res cmdResult
	for i := 1; i <= r.o.retries; i++ {
		ctx, cancel := context.WithTimeout(r.ctx, limit)
		res = exe(ctx, nil, "", name, args...)
		cancel()
		if res.code != 255 || i == r.o.retries {
			return res
		}
		sleep(time.Duration(i) * 5 * time.Second)
	}
	return res
}

func (r *runner) ssh(limit time.Duration, remote string) cmdResult {
	return r.withRetry(limit, "ssh", r.sshArgs(r.o.host, remote)...)
}

func run(c *cobra.Command, o options) error {
	out := cli.Out(c)
	r := &runner{o: o, ctx: c.Context()}
	if r.ctx == nil {
		r.ctx = context.Background()
	}
	rep := report{Host: o.host, Arch: o.arch, Mode: "test", StartedAt: now().UTC().Format(time.RFC3339)}
	if o.bench != "" {
		rep.Mode = "bench:" + o.bench
	}

	// The ssh control socket lives in --work: it must exist before the first call.
	if err := os.MkdirAll(o.work, 0o700); err != nil {
		return cli.Unanswerable("cannot create --work: %v", err)
	}

	// 1. The host is the machine it is said to be.
	un := r.ssh(2*time.Minute, "uname -m")
	if un.code != 0 {
		return cli.Unanswerable("cannot reach %s over ssh (status %d): %s", o.host, un.code, firstLine(un.out))
	}
	rep.Machine = strings.TrimSpace(string(un.out))
	if rep.Machine != machines[o.arch] {
		return cli.Unanswerable("%s reports machine %q, but --arch %s is %q: refusing to label a run with the wrong architecture", o.host, rep.Machine, o.arch, machines[o.arch])
	}

	// 2. The packages that have tests for this target.
	pkgs, err := listPackages(r)
	if err != nil {
		return err
	}
	if len(pkgs) == 0 {
		return cli.Unanswerable("%s: no package with test files for linux/%s matched", o.clone, o.arch)
	}

	if st := r.ssh(time.Minute, "mkdir -p "+o.remoteDir); st.code != 0 {
		return cli.Unanswerable("cannot create %s on the host: %s", o.remoteDir, firstLine(st.out))
	}
	defer func() {
		if !o.keep {
			r.ssh(time.Minute, "rm -rf "+o.remoteDir)
		}
	}()

	before := r.stat()
	for i, p := range pkgs {
		res := r.one(i, p)
		rep.Results = append(rep.Results, res)
		fmt.Fprintf(out, "%-7s %-60s %6.1fs %s\n", res.Status, p.path, res.Seconds, res.Detail)
		if o.bench != "" && o.reportPath != "" && res.BenchOut != "" {
			writeBench(o.reportPath, p.path, res.BenchOut)
		}
	}
	if after := r.stat(); before != nil && after != nil {
		if s, ok := stealBetween(before, after); ok {
			rep.StealPct = &s
			fmt.Fprintf(out, "steal %.1f%% over the run on %s\n", s, o.host)
		}
	}
	if l := r.ssh(time.Minute, "cat /proc/loadavg"); l.code == 0 {
		rep.Load = strings.TrimSpace(string(l.out))
		fmt.Fprintf(out, "load %s\n", rep.Load)
	}

	counts := map[string]int{}
	for _, res := range rep.Results {
		counts[res.Status]++
	}
	fmt.Fprintf(out, "archrun %s/%s: %d PASS, %d FAIL, %d TIMEOUT, %d BUILD, %d SKIP, %d ERROR\n",
		o.host, o.arch, counts["PASS"], counts["FAIL"], counts["TIMEOUT"], counts["BUILD"], counts["SKIP"], counts["ERROR"])
	for _, res := range rep.Results {
		if (res.Status == "FAIL" || res.Status == "TIMEOUT" || res.Status == "BUILD" || res.Status == "ERROR") && res.Output != "" {
			fmt.Fprintf(out, "\n--- %s %s\n%s\n", res.Status, res.Package, res.Output)
		}
	}
	if o.reportPath != "" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(o.reportPath, append(b, '\n'), 0o600); err != nil {
			return cli.Unanswerable("cannot write --report: %v", err)
		}
	}
	if counts["ERROR"] > 0 {
		return cli.Unanswerable("%d package(s) could not be run: that is no answer about them", counts["ERROR"])
	}
	if counts["FAIL"]+counts["TIMEOUT"]+counts["BUILD"] > 0 {
		return cli.Found("%d package(s) did not pass on %s", counts["FAIL"]+counts["TIMEOUT"]+counts["BUILD"], o.host)
	}
	return nil
}

type pkg struct{ path, dir string }

func listPackages(r *runner) ([]pkg, error) {
	env := []string{"GOOS=linux", "GOARCH=" + r.o.arch, "CGO_ENABLED=0"}
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Minute)
	defer cancel()
	res := exe(ctx, env, r.o.clone, "go", "list", "-f",
		"{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}\t{{.Dir}}{{end}}", "./...")
	if res.code != 0 {
		return nil, cli.Unanswerable("go list failed in %s (status %d): %s", r.o.clone, res.code, firstLine(res.out))
	}
	var pkgs []pkg
	for _, line := range strings.Split(string(res.out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		ip, dir, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, cli.Unanswerable("go list printed a line without a directory: %q", line)
		}
		if r.o.only != nil && !r.o.only.MatchString(ip) || r.o.skip != nil && r.o.skip.MatchString(ip) {
			continue
		}
		pkgs = append(pkgs, pkg{ip, dir})
	}
	return pkgs, nil
}

// one builds, copies and runs a single package.
func (r *runner) one(i int, p pkg) result {
	start := now()
	res := result{Package: p.path}
	done := func(status, detail, output string) result {
		res.Status, res.Detail, res.Output = status, detail, tail(output)
		res.Seconds = now().Sub(start).Seconds()
		return res
	}
	name := fmt.Sprintf("p%d", i)
	local := filepath.Join(r.o.work, name+".test")
	os.Remove(local)
	defer os.Remove(local)

	env := []string{"GOOS=linux", "GOARCH=" + r.o.arch, "CGO_ENABLED=0"}
	ctx, cancel := context.WithTimeout(r.ctx, 15*time.Minute)
	b := exe(ctx, env, r.o.clone, "go", "test", "-c", "-o", local, p.path)
	cancel()
	if b.code != 0 {
		return done("BUILD", "go test -c failed", string(b.out))
	}
	if _, err := os.Stat(local); err != nil {
		return done("SKIP", "no test binary for linux/"+r.o.arch, "")
	}

	dir := r.o.remoteDir + "/" + name
	if m := r.ssh(time.Minute, "mkdir -p "+dir); m.code != 0 {
		return done("ERROR", "mkdir on the host", string(m.out))
	}
	if cp := r.withRetry(10*time.Minute, "scp", r.scpArgs(local, r.o.host+":"+dir+"/"+name+".test")...); cp.code != 0 {
		return done("ERROR", "scp of the binary failed", string(cp.out))
	}
	if td := filepath.Join(p.dir, "testdata"); isDir(td) {
		if cp := r.withRetry(10*time.Minute, "scp", r.scpArgs("-r", td, r.o.host+":"+dir+"/")...); cp.code != 0 {
			return done("ERROR", "scp of testdata failed", string(cp.out))
		}
	}

	secs := int(r.o.timeout / time.Second)
	flags := "-test.count=1 -test.timeout=" + strconv.Itoa(secs) + "s"
	if r.o.bench != "" {
		flags += " -test.run='^$' -test.bench='" + r.o.bench + "' -test.benchmem -test.benchtime=" + r.o.benchtime
	}
	cmd := fmt.Sprintf("cd %s && GOMEMLIMIT=%s timeout -k 10 %d ./%s.test %s", dir, r.o.memlimit, secs+30, name, flags)
	t := r.ssh(r.o.timeout+2*time.Minute, cmd)
	r.ssh(time.Minute, "rm -rf "+dir)
	switch {
	case t.code == 0:
		if r.o.bench != "" {
			res.BenchOut = string(t.out)
		}
		return done("PASS", "", "")
	case t.code == 124 || t.code == 137 || strings.Contains(string(t.out), "panic: test timed out"):
		return done("TIMEOUT", fmt.Sprintf("over %s", r.o.timeout), string(t.out))
	case t.code == 255:
		return done("ERROR", "the connection dropped during the run", string(t.out))
	default:
		return done("FAIL", fmt.Sprintf("exit %d", t.code), string(t.out))
	}
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// tail keeps the last tailCap bytes: the failure is at the end.
func tail(s string) string {
	if len(s) <= tailCap {
		return strings.TrimSpace(s)
	}
	return "…" + strings.TrimSpace(s[len(s)-tailCap:])
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

func writeBench(reportPath, pkgPath, text string) {
	name := strings.NewReplacer("/", "_", ".", "_").Replace(pkgPath) + ".bench.txt"
	_ = os.WriteFile(filepath.Join(filepath.Dir(reportPath), name), []byte(text), 0o600)
}

// stat reads the first line of /proc/stat from the host, nil if it cannot.
func (r *runner) stat() []uint64 {
	res := r.ssh(time.Minute, "head -1 /proc/stat")
	if res.code != 0 {
		return nil
	}
	return parseCPU(string(res.out))
}

// parseCPU reads "cpu user nice system idle iowait irq softirq steal ...".
func parseCPU(line string) []uint64 {
	f := strings.Fields(line)
	if len(f) < 9 || f[0] != "cpu" {
		return nil
	}
	v := make([]uint64, 0, len(f)-1)
	for _, s := range f[1:] {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return nil
		}
		v = append(v, n)
	}
	return v
}

// stealBetween is the steal share of the ticks between two readings; ok is false
// when no tick passed, which is no reading rather than 0%.
func stealBetween(a, b []uint64) (float64, bool) {
	if len(a) < 8 || len(b) < 8 {
		return 0, false
	}
	var ta, tb uint64
	for i := 0; i < len(a) && i < len(b) && i < 8; i++ {
		ta += a[i]
		tb += b[i]
	}
	if tb <= ta || b[7] < a[7] {
		return 0, false
	}
	return 100 * float64(b[7]-a[7]) / float64(tb-ta), true
}

var _ io.Writer = (*capped)(nil)
