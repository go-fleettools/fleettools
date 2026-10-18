// Command vulngate reads one govulncheck JSON stream and decides what to do
// about it.
//
// It is the per-repository half of a pair. [cmd/vulnscan] asks "which of these
// 1800 repositories reach a known vulnerability", cloning each one; this judges
// the scan a single repository's own CI job has just produced, in that job,
// against that checkout. Neither replaces the other and the judging rules are
// shared, so each rule below says which wrong answer it came from.
//
//   - ⛔ THE EXIT STATUS IS NOT THE GATE. Measured on 2026-10-04: govulncheck
//     exits 0 over an advisory it has decided this module does not call --
//     GO-2026-6443, a gRPC server that panics on a request with no :authority
//     or Host header, in a version the module required. A lane reading the
//     status is silent about exactly the finding somebody has to decide about.
//
//   - ⛔ UNREAD IS NOT CLEAN. govulncheck that cannot load the packages still
//     prints its config, with no finding, and exits non-zero. Read as JSON
//     alone that is a clean repository. A run is believed only when it carried
//     a config AND an SBOM, because the SBOM is written only once the packages
//     loaded. Taken from vulnscan, which learned it on a fleet sweep.
//
//   - ⛔ AN EMPTY ADVISORY LIST IS NOT A FAILED SCAN. An earlier version of
//     this gate refused a stream carrying no `osv` message at all, as a proxy
//     for "it did not run". That is a false failure waiting for a module whose
//     dependencies and toolchain genuinely have nothing open against them --
//     and it was the wrong proxy, since the SBOM says what it was trying to
//     say. The count is printed as information now.
//
//   - ⛔ A SCAN IS ONLY AS FRESH AS ITS DATABASE, and nothing in the stream
//     makes that visible unless somebody prints it. A frozen mirror answers
//     "nothing found" forever. So the database's own timestamp and its age are
//     printed on every run, and an age beyond -max-db-age is refused. Found on
//     2026-10-11 by reading this gate's output and noticing it had no idea.
//
//   - ⛔ THE STANDARD LIBRARY IS JUDGED BY ONE VERSION -- the toolchain the
//     scan ran under. -go holds the scan to it, so a lane that silently
//     resolved a different toolchain is refused rather than believed. Also
//     from vulnscan.
//
// What it does with what it believes:
//
//	reachable from this module                      error
//	in a required module, with a RELEASED fix       error -- upgrade rather than carry it
//	anything else                                   a notice, named in the log
//
// Exit status: 0 when nothing needs a decision, 1 when something does or when
// the scan cannot be believed, 2 on a usage error.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

// The warning goes to stderr and the verdict to stdout, which matters here:
// what this writes on stdout is read by a CI annotation parser, and a gate that
// mixes its own housekeeping into that stream is a gate somebody filters.
func main() {
	fleet.WarnIfStale(os.Stderr)
	os.Exit(run(os.Args[1:], os.Stdout))
}

// govulncheck writes a stream of JSON objects; only these fields matter here.
type message struct {
	Config *struct {
		GoVersion      string `json:"go_version"`
		ScannerVersion string `json:"scanner_version"`
		DBLastModified string `json:"db_last_modified"`
	} `json:"config"`
	// SBOM is written only once the packages have loaded, which is what makes
	// it the evidence that they did. Its contents are not read.
	SBOM *struct{} `json:"SBOM"`
	OSV  *struct {
		ID string `json:"id"`
	} `json:"osv"`
	Finding *struct {
		OSV          string `json:"osv"`
		FixedVersion string `json:"fixed_version"`
		Trace        []struct {
			Module   string `json:"module"`
			Function string `json:"function"`
		} `json:"trace"`
	} `json:"finding"`
}

var released = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

func run(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("vulngate", flag.ContinueOnError)
	fs.SetOutput(out)
	// Three weeks, not three days. The Go vulnerability database is updated on
	// business days, so a tight bound fires on a quiet fortnight and teaches
	// everybody to ignore it; three weeks still catches a mirror that has
	// stopped. Measured 2026-10-11: the live database was three days old and
	// that was simply the weekend.
	maxAge := fs.Duration("max-db-age", 21*24*time.Hour, "refuse a scan whose vulnerability database is older than this")
	wantGo := fs.String("go", "", "the `toolchain` the scan must have judged the standard library as, e.g. go1.27.2;\n"+
		"empty accepts whatever it reports, which is one question less than the lane asked")
	if code, ok := fleet.ParseFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(out, "usage: vulngate [-max-db-age 21d] [-go go1.27.2] findings.json")
		return 2
	}
	f, err := os.Open(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(out, "::error::vulngate cannot read %s: %v\n", fs.Arg(0), err)
		return 1
	}
	defer f.Close()
	return judge(f, out, *maxAge, *wantGo, time.Now())
}

func judge(r io.Reader, out io.Writer, maxAge time.Duration, wantGo string, now time.Time) int {
	type seen struct {
		osv    string
		called bool
	}
	var (
		sawConfig, sawSBOM bool
		goVersion, dbStamp string
		advisories         int
		said               = map[seen]bool{}
		bad                bool
	)

	dec := json.NewDecoder(r)
	for {
		var m message
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			fmt.Fprintf(out, "::error::vulngate cannot parse govulncheck's output: %v\n", err)
			return 1
		}
		switch {
		case m.Config != nil:
			sawConfig = true
			goVersion, dbStamp = m.Config.GoVersion, m.Config.DBLastModified
			continue
		case m.SBOM != nil:
			sawSBOM = true
			continue
		case m.OSV != nil:
			advisories++
			continue
		case m.Finding == nil:
			continue
		}

		called, where := false, ""
		for _, fr := range m.Finding.Trace {
			if fr.Function != "" {
				called, where = true, fr.Module+"."+fr.Function
				break
			}
		}
		key := seen{m.Finding.OSV, called}
		if said[key] {
			continue // govulncheck reports a finding once per trace; say it once
		}
		said[key] = true

		switch {
		case called:
			fmt.Fprintf(out, "::error::%s is reachable from this module: %s (fixed in %s)\n",
				m.Finding.OSV, where, m.Finding.FixedVersion)
			bad = true
		case released.MatchString(m.Finding.FixedVersion):
			fmt.Fprintf(out, "::error::%s is not called from here, and a RELEASED fix exists (%s): upgrade rather than carry it\n",
				m.Finding.OSV, m.Finding.FixedVersion)
			bad = true
		default:
			fmt.Fprintf(out, "::notice::%s is in a module this one requires, not called from here, and has no released fix yet (upstream fix: %s)\n",
				m.Finding.OSV, fixOrNone(m.Finding.FixedVersion))
		}
	}

	// Believability first: everything above is worthless if the scan did not
	// run, and a scan that did not run prints no finding, which reads clean.
	if !sawConfig || !sawSBOM {
		fmt.Fprintln(out, "::error::govulncheck exited without reporting that it loaded the packages "+
			"(no SBOM in its output): the scan did not run, which is not the same as finding nothing")
		return 1
	}
	if wantGo != "" && goVersion != wantGo {
		fmt.Fprintf(out, "::error::the scan judged the standard library as %s, not the %s this lane asked for: "+
			"stdlib findings are decided by that one version, so this answers a different question\n", goVersion, wantGo)
		return 1
	}

	age, ageErr := dbAge(dbStamp, now)
	switch {
	case ageErr != nil:
		fmt.Fprintf(out, "::error::the scan does not say when its vulnerability database was last modified (%v): "+
			"a frozen database answers \"nothing found\" for ever\n", ageErr)
		return 1
	case age > maxAge:
		fmt.Fprintf(out, "::error::the vulnerability database was last modified %s, %s ago, which is beyond the %s allowed: "+
			"this is a stale mirror answering, not a quiet week\n", dbStamp, round(age), round(maxAge))
		return 1
	}

	fmt.Fprintf(out, "scan: go %s, database %s (%s old); advisories in it: %d; findings judged: %d\n",
		goVersion, dbStamp, round(age), advisories, len(said))
	if bad {
		return 1
	}
	return 0
}

// dbAge reads the database timestamp the scan reported. A missing or unreadable
// one is an error rather than an age of zero: zero is the freshest answer there
// is, and it would be given to a stream that never said.
func dbAge(stamp string, now time.Time) (time.Duration, error) {
	if strings.TrimSpace(stamp) == "" {
		return 0, errors.New("the config carried no db_last_modified")
	}
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return 0, fmt.Errorf("db_last_modified %q: %w", stamp, err)
	}
	if d := now.Sub(t); d > 0 {
		return d, nil
	}
	// A database stamped in the future is not stale, and not this gate's
	// argument to have.
	return 0, nil
}

// round renders an age for a human reading a CI log. Go's own rendering gives
// "59h0m0s" for two and a half days, which is a number somebody has to divide
// before it means anything, and this string is printed on every single run.
func round(d time.Duration) string {
	d = d.Round(time.Hour)
	if d < 48*time.Hour {
		return d.String()
	}
	days := int(d / (24 * time.Hour))
	hours := int((d % (24 * time.Hour)) / time.Hour)
	if hours == 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dd%dh", days, hours)
}

func fixOrNone(v string) string {
	if strings.TrimSpace(v) == "" {
		return "none published"
	}
	return v
}
