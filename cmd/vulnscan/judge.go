package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// The four answers a scan can give. They are strings because they are what the
// report prints and what the JSON carries, and a reader must never have to
// translate a number back into one of them.
const (
	Called = "CALLED" // at least one vulnerable symbol is reachable
	Clean  = "CLEAN"  // the scan RAN and found nothing reachable
	NoGo   = "NO-GO"  // no go.mod anywhere: not a Go repository
	Unread = "UNREAD" // the scan could not read the code: NOT clean
)

// Finding is one advisory reachable from one module, in the shape the report
// needs: which advisory, in which module, at which version, fixed where.
//
// The same advisory ID can name two modules. GO-2026-6603 is filed against
// golang.org/x/net AND the standard library's bundled copy of HTTP/2, with two
// different fixes (x/net v0.60.0, go1.27.2). Keying on the ID alone would merge
// them and print one fix for a repository that needs both.
type Finding struct {
	ID     string   `json:"id"`
	Module string   `json:"module"`
	Found  string   `json:"found,omitempty"` // the version the scan saw
	Fixed  string   `json:"fixed,omitempty"` // the first version without it
	In     []string `json:"in,omitempty"`    // module directories (and GOOS) where it is called
}

// scan is what one govulncheck run over one module said.
type scan struct {
	Status    string    `json:"status"`
	GoVersion string    `json:"go_version,omitempty"` // what govulncheck judged the standard library as
	Called    []Finding `json:"called,omitempty"`
	Imported  int       `json:"imported_only"` // advisories whose package is imported but no vulnerable symbol is reached
	Required  int       `json:"required_only"` // advisories whose module is only required
	Err       string    `json:"error,omitempty"`

	// The advisory+module keys behind the two counts, so a repository with
	// several modules counts each advisory once and not once per module.
	importedKeys, requiredKeys []string
}

// message is the subset of govulncheck's -format json stream this reads. The
// stream is a sequence of objects, each holding exactly one of these keys, and
// govulncheck pretty-prints them, so it is decoded as a stream and never split
// on lines.
type message struct {
	Config *struct {
		GoVersion string `json:"go_version"`
		ScanLevel string `json:"scan_level"`
	} `json:"config"`
	SBOM *struct {
		GoVersion string `json:"go_version"`
	} `json:"SBOM"`
	Finding *struct {
		OSV   string `json:"osv"`
		Fixed string `json:"fixed_version"`
		Trace []struct {
			Module   string `json:"module"`
			Version  string `json:"version"`
			Package  string `json:"package"`
			Function string `json:"function"`
		} `json:"trace"`
	} `json:"finding"`
}

// judge turns one govulncheck run into an answer.
//
// ⛔ A SCAN THAT COULD NOT READ MUST NEVER PRINT AS ZERO. When govulncheck
// cannot load the packages -- a `replace => ../sibling` whose directory is not
// there, a go.sum missing an entry, a toolchain that refuses the go directive --
// it still writes its config message to stdout, then exits 1 with the reason on
// stderr. That stdout holds no finding at all, so a judge that reads only the
// JSON calls it clean. fixtures/loadfail.json is exactly that stream, recorded.
//
// So a run is believed only when ALL of these hold: the process exited 0, the
// stream decoded, it carried a config, and it carried an SBOM -- which
// govulncheck writes only after the packages loaded. Anything else is Unread,
// and the tail of stderr goes with it, because a count of unread repositories
// without their reasons invites the reader to assume one.
//
// "Called" is a finding whose trace[0] names a function: govulncheck reports
// every advisory at up to three levels (module required, package imported,
// symbol called) and only the third one is reachable code.
//
// wantGo, when non-empty, is the toolchain the scan was asked to judge the
// standard library as. A scan that judged it as anything else answered a
// different question -- stdlib findings are decided by that one version -- and
// is Unread too.
func judge(stdout, stderr []byte, runErr error, wantGo string) scan {
	if runErr != nil {
		return scan{Status: Unread, Err: fmt.Sprintf("%v: %s", runErr, tail(stderr))}
	}
	var (
		sawConfig, sawSBOM bool
		goVersion          string
		called             = map[[2]string]*Finding{}
		imported           = map[[2]string]bool{}
		required           = map[[2]string]bool{}
	)
	dec := json.NewDecoder(bytes.NewReader(stdout))
	for {
		var m message
		err := dec.Decode(&m)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return scan{Status: Unread, Err: "govulncheck output did not decode: " + err.Error() + ": " + tail(stderr)}
		}
		switch {
		case m.Config != nil:
			sawConfig = true
			goVersion = m.Config.GoVersion
		case m.SBOM != nil:
			sawSBOM = true
		case m.Finding != nil:
			f := m.Finding
			if len(f.Trace) == 0 {
				continue
			}
			t0 := f.Trace[0]
			k := [2]string{f.OSV, t0.Module}
			switch {
			case t0.Function != "":
				if called[k] == nil {
					called[k] = &Finding{ID: f.OSV, Module: t0.Module, Found: t0.Version, Fixed: f.Fixed}
				}
			case t0.Package != "":
				imported[k] = true
			default:
				required[k] = true
			}
		}
	}
	if !sawConfig || !sawSBOM {
		return scan{Status: Unread, GoVersion: goVersion,
			Err: "govulncheck exited 0 but never reported loading the packages (no SBOM): " + tail(stderr)}
	}
	if wantGo != "" && goVersion != wantGo {
		return scan{Status: Unread, GoVersion: goVersion,
			Err: fmt.Sprintf("judged the standard library as %s, not the %s asked for", goVersion, wantGo)}
	}
	s := scan{Status: Clean, GoVersion: goVersion}
	for k, f := range called {
		s.Called = append(s.Called, *f)
		delete(imported, k)
		delete(required, k)
	}
	for k := range imported {
		delete(required, k)
	}
	s.Imported, s.Required = len(imported), len(required)
	for k := range imported {
		s.importedKeys = append(s.importedKeys, k[0]+" "+k[1])
	}
	for k := range required {
		s.requiredKeys = append(s.requiredKeys, k[0]+" "+k[1])
	}
	sortFindings(s.Called)
	if len(s.Called) > 0 {
		s.Status = Called
	}
	return s
}

func sortFindings(fs []Finding) {
	sort.Slice(fs, func(i, j int) bool {
		if fs[i].ID != fs[j].ID {
			return fs[i].ID < fs[j].ID
		}
		return fs[i].Module < fs[j].Module
	})
}

// tail keeps the end of a stderr, where the reason is. govulncheck prints the
// package-pattern boilerplate after it, so blank lines and that boilerplate are
// dropped first.
func tail(b []byte) string {
	var keep []string
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "For details on package patterns") {
			continue
		}
		keep = append(keep, l)
	}
	if len(keep) > 8 {
		keep = keep[len(keep)-8:]
	}
	s := strings.Join(keep, " | ")
	if len(s) > 1500 {
		s = "..." + s[len(s)-1500:]
	}
	if s == "" {
		return "(nothing on stderr)"
	}
	return s
}
