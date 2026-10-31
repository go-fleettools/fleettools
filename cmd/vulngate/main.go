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
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Reads one govulncheck JSON stream and decides what to do about it.

  reachable from this module ................. error
  in a required module, with a RELEASED fix .. error: upgrade rather than carry it
  anything else .............................. a notice, named in the log

⛔ The EXIT STATUS of govulncheck is not the gate: it exits 0 over an advisory it
has decided this module does not call. ⛔ An unread scan is not a clean one:
govulncheck that cannot load the packages still prints its config with no
finding, so a run is believed only when it carried a config AND an SBOM. ⛔ A
scan is only as fresh as its database, so the database's timestamp and age are
printed on every run and an age beyond --max-db-age is refused.

⚠ MIGRATION NOTE. These flags took one dash before the move to cobra and now
take two. Thirteen workflow lanes across the fleet call this command as
` + "`vulngate -go`" + `; the old spelling is still accepted, with a warning on
stderr, and that shim will not outlive the next major. A lane pinning an older
version is unaffected.

Exit status: 0 nothing needs a decision, 1 something does or the scan cannot be
believed, 2 a usage error.`

// The warning goes to stderr and the verdict to stdout, which matters here:
// what this writes on stdout is read by a CI annotation parser, and a gate that
// mixes its own housekeeping into that stream is a gate somebody filters.
func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it through run, below.
func RootCmd() *cobra.Command {
	var (
		maxAge time.Duration
		wantGo string
	)
	c := cli.New("vulngate [flags] findings.json",
		"Decide what one govulncheck stream means", longHelp)
	c.Args = cobra.ExactArgs(1)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		out := cli.Out(cmd)
		f, err := os.Open(args[0])
		if err != nil {
			// Status 1 and not 2, deliberately: a lane whose scan file is not
			// there has not produced a believable scan, and this gate's whole
			// point is that an unread scan must not pass as a clean one.
			fmt.Fprintf(out, "::error::vulngate cannot read %s: %v\n", args[0], err)
			return cli.FromStatus(1)
		}
		defer f.Close()
		return cli.FromStatus(judge(f, out, maxAge, wantGo, time.Now()))
	}
	fl := c.Flags()
	// Three weeks, not three days. The Go vulnerability database is updated on
	// business days, so a tight bound fires on a quiet fortnight and teaches
	// everybody to ignore it; three weeks still catches a mirror that has
	// stopped. Measured 2026-10-11: the live database was three days old and
	// that was simply the weekend.
	fl.DurationVar(&maxAge, "max-db-age", 21*24*time.Hour,
		"refuse a scan whose vulnerability database is older than this")
	fl.StringVar(&wantGo, "go", "",
		"the toolchain the scan must have judged the standard library as, e.g. go1.27.3;\n"+
			"empty accepts whatever it reports, which is one question less than the lane asked")
	return c
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

// run keeps the signature this command's tests were written against.
//
// Everything goes to one writer, as it did before: what this command writes on
// stdout is read by a CI annotation parser, and the staleness note is the only
// thing that belongs on stderr.
func run(args []string, out io.Writer) int {
	return cli.Drive(RootCmd(), args, out, out)
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
