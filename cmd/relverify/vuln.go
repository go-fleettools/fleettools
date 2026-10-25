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
	"path/filepath"
	"slices"
	"strings"

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
	"a binary carries no call graph, so this cannot say whether it is reachable " +
	"(and a stripped binary is reported for every vulnerable symbol of an affected module)"

// vulnOptions is what the vulnerability and toolchain checks were asked for.
type vulnOptions struct {
	skip  bool            // --no-vuln
	db    string          // --vuln-db
	allow map[string]bool // --allow-vuln
	minGo string          // --min-go, normalised to "go1.x.y"; "" for none
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

// checkVulns scans one Go binary's bytes, already on disk at path, and
// returns what fails the file, what was allowed, and an error when the scan
// could not be made at all.
func checkVulns(ctx context.Context, path, label string, o vulnOptions) (problems, allowed []string, err error) {
	vs, err := scanBinary(ctx, path, o.db)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: vulnerabilities in %s could not be checked: %v", errUnverifiable, label, err)
	}
	for _, v := range vs {
		line := v.ID + " (" + strings.Join(v.Symbols, ", ")
		if v.Fixed != "" {
			line += "; fixed in " + v.Fixed
		}
		line += ")"
		if label != "" {
			line = label + ": " + line
		}
		if o.allow[v.ID] {
			allowed = append(allowed, line)
		} else {
			problems = append(problems, line)
		}
	}
	return problems, allowed, nil
}

// scanFile writes data to a scratch file under dir, unexecutable, scans it,
// and removes it. Archive members are scanned this way: govulncheck reads a
// path.
func scanFile(ctx context.Context, dir, name string, data []byte, o vulnOptions) (problems, allowed []string, err error) {
	scratch, err := os.MkdirTemp(dir, ".scan-*")
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", errUnverifiable, err)
	}
	defer os.RemoveAll(scratch)
	path := filepath.Join(scratch, filepath.Base(filepath.Clean("/"+name)))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", errUnverifiable, err)
	}
	return checkVulns(ctx, path, name, o)
}
