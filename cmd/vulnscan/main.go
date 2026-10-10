// vulnscan runs govulncheck over every repository of a list and says, for each,
// whether a known vulnerability is REACHABLE from its code -- and, as loudly,
// whether the scan could read the code at all.
//
// Written on 2026-10-10, after thirteen standard-library advisories (fixed in
// go1.27.2) and five x/net HTTP/2 ones (GO-2026-6603/6610/6611/6612/6617, fixed
// in golang.org/x/net v0.60.0) were published together, and one organisation
// turned out to reach them in 23 of 31 repositories through gRPC's HTTP/2
// server transport. Dependabot had raised nothing: it matches requirements, not
// reachability, and does not load the code.
//
// Every repository is cloned at depth 1 into a scratch directory, scanned, and
// removed. Each rule below is a wrong answer some earlier sweep gave:
//
//   - ⛔ UNREAD IS NOT CLEAN. govulncheck that cannot load the packages still
//     prints its config to stdout, with no finding, and exits 1. Read as JSON
//     alone that is a clean repository. See judge.
//   - ⛔ GOOS=linux, not the host's. Linux-only files are invisible on darwin,
//     and nineteen findings were hidden that way before. -goos adds more.
//   - ⛔ THE STANDARD LIBRARY IS JUDGED BY ONE VERSION, the toolchain the scan
//     runs under -- so it is set (-go), not inherited from the host, and a scan
//     whose config reports another version is refused.
//   - ⛔ A `replace => ../X` NEEDS X. A sibling repository of the same
//     organisation is cloned next to the repository, or the load fails.
//   - ⛔ EVERY MODULE, not the root one: a nested go.mod is a separate build.
//
// It makes no GitHub REST call per repository -- cloning over https costs the
// API budget nothing, and Renovate shares that budget.
//
// Exit status: 0 when every repository was read and none reaches anything, 1
// when something is reachable, 2 when anything could not be read -- a refusal
// to answer outranks an answer.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

// childMarker is set in the environment of every process this starts.
//
// ⛔ A TOOL THAT RUNS COMMANDS CAN END UP RUNNING ITSELF, and the refusal has
// to be by ENVIRONMENT, not by path: a sibling sweeper relaunched its own test
// suite through a different binary name. Nothing here should ever start
// vulnscan, so a vulnscan that finds this set refuses to start.
const childMarker = "VULNSCAN_CHILD"

// maxJobs is the ceiling on -j. A scan loads every package of a module and
// type-checks it; on the shared host that runs these sweeps the load was ~10
// before one started.
const maxJobs = 3

type config struct {
	goTool      string   // -go: GOTOOLCHAIN for every scan
	goos        []string // GOOS values, each scanned separately
	goarch      string
	govulncheck string
	work        string
	keep        bool
	timeout     time.Duration
}

// moduleResult is one module scanned under one GOOS.
type moduleResult struct {
	Dir  string `json:"dir"`
	GOOS string `json:"goos"`
	scan
}

// result is one repository's answer, and one line of the -state file.
type result struct {
	Repo      string         `json:"repo"`
	Status    string         `json:"status"`
	Called    []Finding      `json:"called,omitempty"`
	Imported  int            `json:"imported_only"`
	Required  int            `json:"required_only"`
	Err       string         `json:"error,omitempty"`
	Modules   []moduleResult `json:"modules,omitempty"`
	Siblings  []string       `json:"siblings,omitempty"`
	Notes     []string       `json:"notes,omitempty"`
	CIGo      []string       `json:"ci_go,omitempty"`
	ScannedAt string         `json:"scanned_at"`
	Seconds   float64        `json:"seconds"`
}

// report is the -json file.
type report struct {
	Generated   string         `json:"generated"`
	Go          string         `json:"go"`
	GOOS        []string       `json:"goos"`
	GOARCH      string         `json:"goarch"`
	Govulncheck string         `json:"govulncheck"`
	Listed      int            `json:"listed"`
	Totals      map[string]int `json:"totals"`
	Pending     int            `json:"pending"` // listed but not scanned yet
	Repos       []result       `json:"repos"`
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	os.Exit(run(os.Stdin, os.Stdout, os.Stderr, os.Args[1:], realRunner))
}

// realRunner is replaced by a panic in TestMain: see runner.
var realRunner runner = execRunner

func execRunner(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), childMarker+"=1"), env...)
	cmd.WaitDelay = 10 * time.Second
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("%w (%v)", ctx.Err(), err)
	}
	return []byte(out.String()), []byte(errb.String()), err
}

func run(stdin io.Reader, stdout, stderr io.Writer, args []string, exe runner) int {
	if os.Getenv(childMarker) != "" {
		fmt.Fprintf(stderr, "vulnscan: refusing to run under %s=1 -- something vulnscan started is starting vulnscan\n", childMarker)
		return 2
	}
	fs := flag.NewFlagSet("vulnscan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home, _ := os.UserHomeDir()
	var (
		list     = fs.String("list", "", "file of org/repo lines (# comments allowed); stdin when empty")
		jsonOut  = fs.String("json", "", "write the per-repository report here (rewritten as the sweep goes)")
		state    = fs.String("state", "", "append each finished repository here, and skip the ones it already holds")
		rescan   = fs.Bool("rescan", false, "with -state, scan every repository again")
		retry    = fs.Bool("retry-unread", false, "with -state, scan again the repositories that were UNREAD")
		jobs     = fs.Int("j", 2, fmt.Sprintf("repositories scanned at once (at most %d)", maxJobs))
		goTool   = fs.String("go", "go1.27.2", "GOTOOLCHAIN for every scan: the toolchain CI builds with, which decides every standard-library finding")
		goosList = fs.String("goos", "linux", "comma-separated GOOS values; each module is scanned under each")
		goarch   = fs.String("goarch", "amd64", "GOARCH for every scan")
		gvc      = fs.String("govulncheck", filepath.Join(home, "go", "bin", "govulncheck"), "govulncheck binary")
		work     = fs.String("work", "", "scratch directory for clones (default: a new one under $TMPDIR)")
		keep     = fs.Bool("keep", false, "keep each clone after scanning it")
		timeout  = fs.Duration("timeout", 20*time.Minute, "per clone and per govulncheck run")
		self     = fs.Bool("selftest", false, "judge the recorded fixtures and exit: a known positive, a known clean, a known load failure")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *self {
		return selftest(stdout)
	}
	if *jobs < 1 || *jobs > maxJobs {
		fmt.Fprintf(stderr, "-j %d: must be 1..%d -- each scan type-checks a whole module, on a host already loaded\n", *jobs, maxJobs)
		return 2
	}
	if err := preflight(*gvc); err != nil {
		fmt.Fprintf(stderr, "govulncheck: %v\n    go install golang.org/x/vuln/cmd/govulncheck@latest, or name one with -govulncheck\n", err)
		return 2
	}
	cfg := config{goTool: *goTool, goarch: *goarch, govulncheck: *gvc, keep: *keep, timeout: *timeout}
	for _, g := range strings.Split(*goosList, ",") {
		if g = strings.TrimSpace(g); g != "" {
			cfg.goos = append(cfg.goos, g)
		}
	}
	if len(cfg.goos) == 0 {
		fmt.Fprintln(stderr, "-goos names nothing")
		return 2
	}

	var in io.Reader = stdin
	if *list != "" {
		f, err := os.Open(*list)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		defer f.Close()
		in = f
	}
	repos, err := readList(in)
	if err != nil {
		fmt.Fprintln(stderr, "list:", err)
		return 2
	}
	if len(repos) == 0 {
		fmt.Fprintln(stderr, "the list names no repository")
		return 2
	}

	done := map[string]result{}
	if *state != "" {
		if done, err = loadState(*state); err != nil {
			fmt.Fprintln(stderr, "state:", err)
			return 2
		}
	}
	var todo []string
	for _, r := range repos {
		prev, ok := done[r]
		if ok && !*rescan && !(*retry && prev.Status == Unread) {
			continue
		}
		todo = append(todo, r)
	}

	if cfg.work == "" {
		if *work != "" {
			cfg.work = *work
		} else if cfg.work, err = os.MkdirTemp("", "vulnscan-"); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}
	if err := os.MkdirAll(cfg.work, 0o755); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fmt.Fprintf(stdout, "listed %d, already in state %d, to scan %d; GOTOOLCHAIN=%s GOOS=%s GOARCH=%s, -j %d, work %s\n",
		len(repos), len(repos)-len(todo), len(todo), cfg.goTool, strings.Join(cfg.goos, ","), cfg.goarch, *jobs, cfg.work)

	var stateFile *os.File
	if *state != "" {
		if stateFile, err = os.OpenFile(*state, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err != nil {
			fmt.Fprintln(stderr, "state:", err)
			return 2
		}
		defer stateFile.Close()
	}

	var (
		mu       sync.Mutex
		finished int
		wg       sync.WaitGroup
		queue    = make(chan int)
	)
	writeReport := func() {
		if *jsonOut == "" {
			return
		}
		if err := writeJSON(*jsonOut, buildReport(cfg, repos, done)); err != nil {
			fmt.Fprintln(stderr, "json:", err)
		}
	}
	for w := 0; w < *jobs; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range queue {
				res := scanRepo(context.Background(), exe, cfg, todo[i], filepath.Join(cfg.work, fmt.Sprint(i)))
				mu.Lock()
				finished++
				done[res.Repo] = res
				if stateFile != nil {
					b, _ := json.Marshal(res)
					stateFile.Write(append(b, '\n'))
				}
				fmt.Fprintf(stdout, "[%d/%d] %s\n", finished, len(todo), line(res))
				if finished%10 == 0 {
					writeReport()
				}
				mu.Unlock()
			}
		}()
	}
	for i := range todo {
		queue <- i
	}
	close(queue)
	wg.Wait()
	if !cfg.keep && *work == "" {
		os.RemoveAll(cfg.work)
	}

	rep := buildReport(cfg, repos, done)
	writeReport()
	return summary(stdout, rep)
}

// readList reads org/repo lines, skipping blanks and # comments, keeping the
// first occurrence of each.
func readList(r io.Reader) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if i := strings.IndexByte(l, '#'); i >= 0 {
			l = strings.TrimSpace(l[:i])
		}
		if l == "" {
			continue
		}
		if strings.Count(l, "/") != 1 || strings.ContainsAny(l, " \t") {
			return nil, fmt.Errorf("%q is not org/repo", l)
		}
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out, sc.Err()
}

// loadState reads the -state file. A later line for a repository replaces an
// earlier one, which is what a rescan appends. A torn last line -- a sweep
// killed mid-write -- is ignored rather than fatal: that repository is simply
// scanned again.
func loadState(path string) (map[string]result, error) {
	out := map[string]result{}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var r result
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.Repo != "" && r.Status != "" {
			out[r.Repo] = r
		}
	}
	return out, sc.Err()
}

// scanRepo clones one repository, its siblings, scans every module under every
// GOOS, and removes the clone.
func scanRepo(ctx context.Context, exe runner, cfg config, repo, jobDir string) (res result) {
	start := time.Now()
	res = result{Repo: repo}
	defer func() {
		res.ScannedAt = start.UTC().Format(time.RFC3339)
		res.Seconds = float64(time.Since(start).Round(100*time.Millisecond)) / float64(time.Second)
		if !cfg.keep {
			os.RemoveAll(jobDir)
		}
	}()
	org, name, _ := strings.Cut(repo, "/")
	orgDir := filepath.Join(jobDir, org)
	repoDir := filepath.Join(orgDir, name)
	if err := os.MkdirAll(orgDir, 0o755); err != nil {
		res.Status, res.Err = Unread, err.Error()
		return res
	}
	cctx, cancel := context.WithTimeout(ctx, cfg.timeout)
	err := clone(cctx, exe, repo, repoDir)
	cancel()
	if err != nil {
		res.Status, res.Err = Unread, err.Error()
		return res
	}
	res.CIGo = ciGo(repoDir)
	mods, err := modules(repoDir)
	if err != nil {
		res.Status, res.Err = Unread, "listing modules: "+err.Error()
		return res
	}
	if len(mods) == 0 {
		res.Status = NoGo
		return res
	}
	cctx, cancel = context.WithTimeout(ctx, cfg.timeout)
	res.Siblings, res.Notes = siblings(cctx, exe, org, orgDir, repoDir, mods)
	cancel()

	wantGo := cfg.goTool
	if strings.ContainsAny(wantGo, "+") || wantGo == "local" || wantGo == "auto" {
		wantGo = "" // the toolchain is chosen per module; nothing to hold the config to
	}
	for _, m := range mods {
		for _, goos := range cfg.goos {
			env := []string{"GOOS=" + goos, "GOARCH=" + cfg.goarch, "GOTOOLCHAIN=" + cfg.goTool}
			cctx, cancel := context.WithTimeout(ctx, cfg.timeout)
			out, errOut, err := exe(cctx, filepath.Join(repoDir, m), env, cfg.govulncheck, "-format", "json", "./...")
			cancel()
			s := judge(out, errOut, err, wantGo)
			// The scratch path is the same for every file and says nothing; on
			// macOS go prints it with /private in front. It ate most of the
			// first real reason this printed.
			s.Err = strings.NewReplacer("/private"+orgDir+"/", "", orgDir+"/", "").Replace(s.Err)
			res.Modules = append(res.Modules, moduleResult{Dir: m, GOOS: goos, scan: s})
		}
	}
	return aggregate(res)
}

// aggregate folds the module scans into the repository's answer.
//
// CALLED wins: a reachable advisory is the finding, whatever else could not be
// read -- but the unread modules are still named in the error, so a partial
// scan never reads as a whole one. Otherwise any UNREAD module makes the
// repository UNREAD: one module read clean says nothing about the other.
func aggregate(res result) result {
	called := map[[2]string]*Finding{}
	imported, required := map[string]bool{}, map[string]bool{}
	var unread []string
	for _, m := range res.Modules {
		where := m.Dir
		if len(res.Modules) > 0 && m.GOOS != "linux" {
			where += "@" + m.GOOS
		}
		if m.Status == Unread {
			unread = append(unread, fmt.Sprintf("%s (%s): %s", m.Dir, m.GOOS, m.Err))
			continue
		}
		for _, f := range m.Called {
			k := [2]string{f.ID, f.Module}
			if called[k] == nil {
				c := f
				c.In = nil
				called[k] = &c
			}
			if !contains(called[k].In, where) {
				called[k].In = append(called[k].In, where)
			}
		}
		for _, k := range m.importedKeys {
			imported[k] = true
		}
		for _, k := range m.requiredKeys {
			required[k] = true
		}
	}
	res.Called = nil
	for k, f := range called {
		res.Called = append(res.Called, *f)
		delete(imported, k[0]+" "+k[1])
		delete(required, k[0]+" "+k[1])
	}
	for k := range imported {
		delete(required, k)
	}
	sortFindings(res.Called)
	res.Imported, res.Required = len(imported), len(required)
	switch {
	case len(res.Called) > 0:
		res.Status = Called
		if len(unread) > 0 {
			res.Err = fmt.Sprintf("%d of %d module scans UNREAD besides: %s", len(unread), len(res.Modules), strings.Join(unread, "; "))
		}
	case len(unread) > 0:
		res.Status = Unread
		res.Err = fmt.Sprintf("%d of %d module scans UNREAD: %s", len(unread), len(res.Modules), strings.Join(unread, "; "))
	default:
		res.Status = Clean
	}
	return res
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// line is the one-line form of a result. An UNREAD line never carries a
// count: there is no zero to print, only a reason.
func line(r result) string {
	switch r.Status {
	case Called:
		var ids []string
		for _, f := range r.Called {
			ids = append(ids, f.ID+"("+shortModule(f.Module)+"→"+f.Fixed+")")
		}
		s := fmt.Sprintf("%-6s %-50s %d: %s", r.Status, r.Repo, len(r.Called), strings.Join(ids, " "))
		if r.Err != "" {
			s += "  [PARTIAL: " + firstLine(r.Err) + "]"
		}
		return s
	case Clean:
		return fmt.Sprintf("%-6s %-50s 0 called (%d imported only, %d required only)", r.Status, r.Repo, r.Imported, r.Required)
	case NoGo:
		return fmt.Sprintf("%-6s %-50s no go.mod", r.Status, r.Repo)
	}
	return fmt.Sprintf("%-6s %-50s could not scan: %s", r.Status, r.Repo, firstLine(r.Err))
}

func shortModule(m string) string {
	return strings.TrimPrefix(m, "golang.org/")
}

func firstLine(s string) string {
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

func buildReport(cfg config, listed []string, done map[string]result) report {
	rep := report{
		Generated: time.Now().UTC().Format(time.RFC3339), Go: cfg.goTool, GOOS: cfg.goos, GOARCH: cfg.goarch,
		Govulncheck: cfg.govulncheck, Listed: len(listed),
		Totals: map[string]int{Called: 0, Clean: 0, NoGo: 0, Unread: 0},
	}
	for _, r := range listed {
		res, ok := done[r]
		if !ok {
			rep.Pending++
			continue
		}
		rep.Totals[res.Status]++
		rep.Repos = append(rep.Repos, res)
	}
	sort.Slice(rep.Repos, func(i, j int) bool { return rep.Repos[i].Repo < rep.Repos[j].Repo })
	return rep
}

// writeJSON replaces path atomically, so a reader never sees half a report.
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// summary prints the totals and returns the exit status.
func summary(w io.Writer, rep report) int {
	t := rep.Totals
	scanned := t[Called] + t[Clean] + t[NoGo] + t[Unread]
	fmt.Fprintf(w, "\n%d listed, %d scanned: CALLED %d, CLEAN %d, NO-GO %d, UNREAD %d",
		rep.Listed, scanned, t[Called], t[Clean], t[NoGo], t[Unread])
	if rep.Pending > 0 {
		fmt.Fprintf(w, ", NOT YET SCANNED %d", rep.Pending)
	}
	fmt.Fprintln(w)
	byKey := map[string][]string{}
	for _, r := range rep.Repos {
		for _, f := range r.Called {
			k := fmt.Sprintf("%s %s (fixed in %s)", f.ID, f.Module, f.Fixed)
			byKey[k] = append(byKey[k], r.Repo)
		}
	}
	var keys []string
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "  %-60s %d repositories\n", k, len(byKey[k]))
	}
	if t[Unread] > 0 || rep.Pending > 0 {
		fmt.Fprintf(w, "INCOMPLETE: %d repositories could not be read and %d were not scanned; this pass says nothing about them.\n", t[Unread], rep.Pending)
		return 2
	}
	if t[Called] > 0 {
		return 1
	}
	return 0
}
