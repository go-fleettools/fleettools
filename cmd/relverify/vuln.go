package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/version"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"

	"golang.org/x/vuln/scan"
)

// ⛔ ON 2026-10-25 go-authn/authnd v0.11.1 PASSED THIS TOOL while every one of
// its binaries was built with go1.27.2, which GO-2026-6809 (crypto/x509) affects.
// relverify checked where a binary came from and never what it was made with.
// It now reports each Go binary's toolchain, can refuse an old one
// (--min-go), and scans every Go binary -- bare or inside an archive -- against
// the Go vulnerability database, the way `govulncheck -mode=binary` does.
//
// ⛔ WHY golang.org/x/vuln/scan AND NOT THE govulncheck COMMAND. scan.Command
// runs govulncheck's own main IN THIS PROCESS (scan.go: Start calls
// scan.RunGovulncheck in a goroutine; no exec), so:
//   - the scanner's version is the one go.mod pins, upgraded by Renovate and
//     judged by this repository's own govulncheck lane -- not whatever binary
//     happens to be first on PATH, or none;
//   - its dependencies (x/tools, x/mod, x/sync are already here; x/telemetry is
//     new, and only counts locally: counter.Inc is a no-op until counter.Open,
//     which nothing here calls) are pure Go and built into relverify;
//   - the answer is the same JSON stream govulncheck prints, read below.
//
// THE DATABASE IS FETCHED, NOT CACHED. internal/client reads
// https://vuln.go.dev/index/modules.json.gz and then ID/<id>.json.gz for each
// advisory of a module the binary holds, over net/http, on every scan; there is
// no on-disk cache. Without network the fetch fails and govulncheck returns an
// error -- which is "could not be checked" (exit 2) here, never "verified".
// --vuln-db takes another database: a mirror, or a file:// directory in the v1
// layout (index/db.json, index/modules.json, ID/<id>.json), which is how the
// tests run offline and deterministically.

const defaultVulnDB = "https://vuln.go.dev"

// binaryCaveat is printed once per run that scanned a binary.
const binaryCaveat = "binary mode: a vulnerability is reported when its symbol is PRESENT in the binary; " +
	"a binary carries no call graph, so this cannot say whether it is reachable. " +
	"A stripped binary shows no symbols at all, so its findings are module-level and are held to " +
	"a source-mode scan of the module at the tag instead"

// vulnOptions is what the vulnerability and toolchain checks were asked for.
type vulnOptions struct {
	skip  bool            // --no-vuln
	db    string          // --vuln-db
	allow map[string]bool // --allow-vuln
	minGo string          // --min-go, normalised to "go1.x.y"; "" for none
	cache *sourceCache    // one source-mode scan per target; nil scans every time
}

// A vuln is one advisory whose vulnerable symbols a binary holds.
type vuln struct {
	ID      string
	Fixed   string   // the module version that fixes it, "" when none
	Symbols []string // package.Symbol, sorted
}

// scanBinary is the seam a test replaces.
var scanBinary = govulncheckBinary

// govulncheckBinary scans one Go binary and returns the advisories whose
// symbols are in it.
//
// ⛔ UNREAD IS NOT CLEAN, the lesson vulngate and vulnscan record: with
// -format json govulncheck exits 0 whatever it finds, so a zero exit says the
// stream was written, not that the binary was read. The stream must carry the
// SBOM message, which vulncheck.Binary emits after reading the binary and
// before fetching the database; without it the scan is an error.
func govulncheckBinary(ctx context.Context, path, db string) ([]vuln, error) {
	var stdout, stderr bytes.Buffer
	c := scan.Command(ctx, "-mode", "binary", "-format", "json", "-db", db, path)
	c.Stdin = strings.NewReader("")
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Start(); err != nil {
		return nil, err
	}
	if err := c.Wait(); err != nil {
		return nil, fmt.Errorf("%v %s", err, firstLine(stderr.String()))
	}
	return parseScan(&stdout)
}

// message is the subset of govulncheck's JSON stream (protocol v1.0.0,
// internal/govulncheck/govulncheck.go) read here.
type message struct {
	Config *struct {
		ScanMode string `json:"scan_mode"`
	} `json:"config"`
	SBOM *struct {
		GoVersion string `json:"go_version"`
	} `json:"SBOM"`
	Finding *struct {
		OSV          string `json:"osv"`
		FixedVersion string `json:"fixed_version"`
		Trace        []struct {
			Module   string `json:"module"`
			Package  string `json:"package"`
			Function string `json:"function"`
			Receiver string `json:"receiver"`
		} `json:"trace"`
	} `json:"finding"`
}

// parseScan reads a binary-mode stream. A finding counts when its first frame
// names a function: govulncheck also emits module- and package-level findings
// for an affected module whose vulnerable symbols the binary does NOT hold
// (Finding's doc comment), and those are not in the binary.
func parseScan(r io.Reader) ([]vuln, error) {
	dec := json.NewDecoder(r)
	read := false
	byID := map[string]*vuln{}
	for {
		var m message
		err := dec.Decode(&m)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("govulncheck's output does not decode: %w", err)
		}
		if m.SBOM != nil {
			read = true
		}
		f := m.Finding
		if f == nil || len(f.Trace) == 0 || f.Trace[0].Function == "" {
			continue
		}
		v := byID[f.OSV]
		if v == nil {
			v = &vuln{ID: f.OSV, Fixed: f.FixedVersion}
			byID[f.OSV] = v
		}
		sym := symbolOf(f.Trace[0].Package, f.Trace[0].Receiver, f.Trace[0].Function)
		if !slices.Contains(v.Symbols, sym) {
			v.Symbols = append(v.Symbols, sym)
		}
	}
	if !read {
		return nil, errors.New("govulncheck never reported reading the binary (no SBOM in its output)")
	}
	out := make([]vuln, 0, len(byID))
	for _, v := range byID {
		slices.Sort(v.Symbols)
		out = append(out, *v)
	}
	slices.SortFunc(out, func(a, b vuln) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// symbolOf names a binary-mode frame as package.Receiver.Function.
//
// ⛔ AN ADVISORY OVER A WHOLE PACKAGE ARRIVES SPLIT AT ITS FIRST DOT. Measured
// on go-authn/authnd v0.11.1, GO-2026-5932 (every symbol of
// golang.org/x/crypto/openpgp): govulncheck v1.9.0 emits the wildcard
// "golang.org/x/crypto/openpgp/*" as receiver "golang", function
// "org/x/crypto/openpgp/*". Glued back naively that printed
// "golang.org/x/crypto/openpgp.golang.org/x/crypto/openpgp/*"; a frame whose
// rejoined name already starts with its package is printed as it stands.
func symbolOf(pkg, recv, fn string) string {
	full := fn
	if recv != "" {
		full = recv + "." + fn
	}
	if strings.HasPrefix(full, pkg+"/") {
		return full
	}
	return pkg + "." + strings.TrimPrefix(full, "*")
}

// normGo turns a --min-go argument into Go toolchain name syntax: "1.27.3" and
// "go1.27.3" both name go1.27.3.
func normGo(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "go") {
		s = "go" + s
	}
	if !version.IsValid(s) {
		return "", fmt.Errorf("--min-go %q is not a Go version (want e.g. go1.27.3)", s)
	}
	return s, nil
}

// toolchainOf is the toolchain part of buildinfo.GoVersion, which is
// runtime.Version() of the compiler and may carry experiments after a space
// ("go1.27.2 X:nodwarf5").
func toolchainOf(goVersion string) string {
	f := strings.Fields(goVersion)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// olderThan says what is wrong with a binary's toolchain against --min-go, or
// "".
//
// go/version.Compare orders go1.27.10 after go1.27.3 (a string comparison
// does not), and sorts an invalid version -- a "devel go1.28-abcdef" build, an
// empty field -- below every valid one: such a binary is refused, because
// nobody can say which fixes it has.
func olderThan(goVersion, minGo string) string {
	if minGo == "" {
		return ""
	}
	tc := toolchainOf(goVersion)
	if !version.IsValid(tc) {
		return fmt.Sprintf("toolchain %q is not a Go release, so not known to be at least %s", goVersion, minGo)
	}
	if version.Compare(tc, minGo) < 0 {
		return fmt.Sprintf("built with %s, older than --min-go %s", tc, minGo)
	}
	return ""
}

// verdicts is what the scan of one binary decided.
type verdicts struct {
	problems   []string // fail the file
	allowed    []string // accepted by --allow-vuln, still reported
	allowedIDs []string
	moduleOnly []string // a stripped binary's findings that are not reachable at the tag
	stripped   bool
}

// checkVulns scans one Go binary, on disk at path, and decides what each
// finding means; err is a scan that could not be made at all.
//
// ⛔ A STRIPPED BINARY IS JUDGED BY ITS MODULES, AND THE STREAM DOES NOT SAY
// SO. The first version of this failed go-authn/authnd v0.12.0 on
// GO-2026-5932 (golang.org/x/crypto/openpgp) and read it as "openpgp linked
// whole". It was not linked at all: `go mod why` says the main module does not
// need it, and the same tree built without -s -w scans clean. authnd's
// release builds with -ldflags="-s -w", like most of this fleet's, and for a
// binary without a symbol table govulncheck falls back to "go.mod-level
// precision": every vulnerable symbol of every affected module version counts
// as present (x/vuln v1.9.0 internal/vulncheck/binary.go:107-111, after
// internal/buildinfo/additions_scan.go:85-90 returns no symbols on
// ErrNoSymbols). No progress message or field marks it; the findings look
// exactly like symbol-level ones. So relverify asks govulncheck's own extract
// mode, which prints the vulncheck.Bin the scan works from: an empty
// pkgSymbols is the stripped case.
//
// For a stripped binary each finding not allowed is held to a source-mode
// scan of the module at the tag -- reachability from the binary's own main
// package, for its GOOS/GOARCH and its toolchain. Reachable fails; not
// reachable is reported, by name, as module-level only. A source scan that
// cannot run is "could not be checked", never a pass.
func checkVulns(ctx context.Context, path, label string, o vulnOptions, t sourceTarget) (verdicts, error) {
	var v verdicts
	vs, err := scanBinary(ctx, path, o.db)
	if err != nil {
		return v, fmt.Errorf("%w: vulnerabilities in %s could not be checked: %v", errUnverifiable, cmpStr(label, "the binary"), err)
	}
	var pending []vuln
	for _, x := range vs {
		if o.allow[x.ID] {
			v.allowed = append(v.allowed, vulnLine(label, x, ""))
			v.allowedIDs = append(v.allowedIDs, x.ID)
		} else {
			pending = append(pending, x)
		}
	}
	if len(pending) == 0 {
		return v, nil
	}
	hasSymbols, err := extractBinary(ctx, path)
	if err != nil {
		return v, fmt.Errorf("%w: whether %s has a symbol table could not be read: %v", errUnverifiable, cmpStr(label, "the binary"), err)
	}
	if hasSymbols {
		for _, x := range pending {
			v.problems = append(v.problems, vulnLine(label, x, ""))
		}
		return v, nil
	}
	v.stripped = true
	reach, err := o.reachable(ctx, t)
	if err != nil {
		return v, fmt.Errorf("%w: %s is stripped, and the source-mode scan of %s@%s could not run: %v",
			errUnverifiable, cmpStr(label, "the binary"), t.Module, t.Version, err)
	}
	for _, x := range pending {
		if reach[x.ID] {
			v.problems = append(v.problems, vulnLine(label, x, "binary stripped; REACHABLE in source at "+t.Version))
		} else {
			v.moduleOnly = append(v.moduleOnly, vulnLine(label, x,
				"binary stripped, so presence cannot be seen; source at "+t.Version+": not reachable"))
		}
	}
	return v, nil
}

func cmpStr(s, otherwise string) string {
	if s == "" {
		return otherwise
	}
	return s
}

func vulnLine(label string, v vuln, why string) string {
	line := v.ID + " (" + strings.Join(v.Symbols, ", ")
	if v.Fixed != "" {
		line += "; fixed in " + v.Fixed
	}
	line += ")"
	if why != "" {
		line += " -- " + why
	}
	if label != "" {
		line = label + ": " + line
	}
	return line
}

// scanFile writes data to a scratch file under dir, unexecutable, scans it,
// and removes it. Archive members are scanned this way: govulncheck reads a
// path.
func scanFile(ctx context.Context, dir, name string, data []byte, o vulnOptions, t sourceTarget) (verdicts, error) {
	scratch, err := os.MkdirTemp(dir, ".scan-*")
	if err != nil {
		return verdicts{}, fmt.Errorf("%w: %v", errUnverifiable, err)
	}
	defer os.RemoveAll(scratch)
	path := filepath.Join(scratch, filepath.Base(filepath.Clean("/"+name)))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return verdicts{}, fmt.Errorf("%w: %v", errUnverifiable, err)
	}
	return checkVulns(ctx, path, name, o, t)
}

// extractBinary is the seam a test replaces: whether govulncheck sees a
// symbol table in the binary at path.
var extractBinary = govulncheckExtract

// govulncheckExtract runs govulncheck -mode extract, which needs no database,
// and reads the vulncheck.Bin it prints after its header
// (internal/scan/extract.go:40-52 -- the same createBin the binary scan uses,
// internal/scan/binary.go:26).
func govulncheckExtract(ctx context.Context, path string) (bool, error) {
	var stdout, stderr bytes.Buffer
	c := scan.Command(ctx, "-mode", "extract", path)
	c.Stdin = strings.NewReader("")
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Start(); err != nil {
		return false, err
	}
	if err := c.Wait(); err != nil {
		return false, fmt.Errorf("%v %s", err, firstLine(stderr.String()))
	}
	dec := json.NewDecoder(&stdout)
	var h struct{ Name, Version string }
	if err := dec.Decode(&h); err != nil || h.Name != "govulncheck-extract" {
		return false, fmt.Errorf("govulncheck -mode extract printed no header (%v)", err)
	}
	var bin struct {
		PkgSymbols []json.RawMessage `json:"pkgSymbols"`
	}
	if err := dec.Decode(&bin); err != nil {
		return false, fmt.Errorf("govulncheck -mode extract: %w", err)
	}
	return len(bin.PkgSymbols) > 0, nil
}

// A sourceTarget is what a source-mode scan reproduces of one binary.
type sourceTarget struct {
	Module, Version string // the module, at the tag
	Package         string // the binary's main package
	GOOS, GOARCH    string
	GoVersion       string // the toolchain that built it: the standard library it holds
}

// sourceCache keeps one source scan per target: six binaries of one release
// on three platforms ask the same question three times over.
type sourceCache struct {
	mu   sync.Mutex
	done map[sourceTarget]sourceResult
}

type sourceResult struct {
	reach map[string]bool
	err   error
}

func (o vulnOptions) reachable(ctx context.Context, t sourceTarget) (map[string]bool, error) {
	if o.cache == nil {
		return scanSource(ctx, t, o.db)
	}
	o.cache.mu.Lock()
	defer o.cache.mu.Unlock()
	if o.cache.done == nil {
		o.cache.done = map[sourceTarget]sourceResult{}
	}
	if r, ok := o.cache.done[t]; ok {
		return r.reach, r.err
	}
	reach, err := scanSource(ctx, t, o.db)
	o.cache.done[t] = sourceResult{reach, err}
	return reach, err
}

// scanSource is the seam a test replaces: the IDs reachable, in source mode,
// from t's main package.
var scanSource = govulncheckSource

// govulncheckSource downloads the module at the tag through the module proxy
// and scans the binary's main package in source mode.
func govulncheckSource(ctx context.Context, t sourceTarget, db string) (map[string]bool, error) {
	if t.Module == "" || t.Package == "" || t.Version == "" {
		return nil, fmt.Errorf("the binary names no module or main package (module %q, package %q)", t.Module, t.Package)
	}
	dir, err := moduleDir(ctx, t.Module+"@"+t.Version)
	if err != nil {
		return nil, err
	}
	return scanSourceDir(ctx, dir, t, db)
}

// moduleDir is `go mod download -json mod@version`'s Dir: the module's files,
// read-only, in the module cache. It runs outside any module.
func moduleDir(ctx context.Context, modVersion string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", "mod", "download", "-json", modVersion)
	cmd.Dir = os.TempDir()
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	out, err := cmd.Output()
	var m struct{ Dir, Error string }
	if jerr := json.Unmarshal(out, &m); jerr != nil {
		return "", fmt.Errorf("go mod download %s: %v %v", modVersion, err, jerr)
	}
	if m.Error != "" || m.Dir == "" {
		return "", fmt.Errorf("go mod download %s: %s", modVersion, cmp(m.Error, "no directory"))
	}
	return m.Dir, nil
}

// scanSourceDir runs govulncheck in source mode over one package of the
// module in dir.
//
// GOVERSION is the binary's toolchain, not this machine's: govulncheck judges
// the standard library by it (internal/scan/run.go:87-93 reads GOVERSION= from
// the environment before asking `go env`), and the question is what the
// RELEASED binary holds. CGO_ENABLED=0 keeps package loading from running a C
// toolchain over downloaded code.
//
// ⛔ GOTOOLCHAIN IS LEFT TO GO. The first version set GOTOOLCHAIN=local, and
// on this machine the `go` on PATH outside any module is go1.27.1 while
// authnd v0.12.0 requires go >= 1.27.3: the source scan could not run, and
// relverify said so (exit 2) for a release that is fine. Go's own selection
// fetches only the official toolchain the module asks for, checked against
// the checksum database (go.dev/doc/toolchain).
func scanSourceDir(ctx context.Context, dir string, t sourceTarget, db string) (map[string]bool, error) {
	var stdout, stderr bytes.Buffer
	c := scan.Command(ctx, "-C", dir, "-format", "json", "-db", db, t.Package)
	c.Stdin = strings.NewReader("")
	c.Stdout, c.Stderr = &stdout, &stderr
	c.Env = append(os.Environ(), "GOFLAGS=", "CGO_ENABLED=0")
	if t.GOOS != "" && t.GOARCH != "" {
		c.Env = append(c.Env, "GOOS="+t.GOOS, "GOARCH="+t.GOARCH)
	}
	if t.GoVersion != "" {
		c.Env = append(c.Env, "GOVERSION="+t.GoVersion)
	}
	if err := c.Start(); err != nil {
		return nil, err
	}
	if err := c.Wait(); err != nil {
		return nil, fmt.Errorf("%v %s", err, firstLine(stderr.String()))
	}
	vs, err := parseScan(&stdout)
	if err != nil {
		return nil, err
	}
	reach := map[string]bool{}
	for _, v := range vs {
		reach[v.ID] = true
	}
	return reach, nil
}

// targetOf is the source scan that reproduces a binary of tag.
func targetOf(bi *debug.BuildInfo, tag string) sourceTarget {
	t := sourceTarget{Module: bi.Main.Path, Version: tag, Package: bi.Path, GoVersion: toolchainOf(bi.GoVersion)}
	for _, s := range bi.Settings {
		switch s.Key {
		case "GOOS":
			t.GOOS = s.Value
		case "GOARCH":
			t.GOARCH = s.Value
		}
	}
	return t
}
