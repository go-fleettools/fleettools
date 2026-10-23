// Command mantracheck asks whether an organisation's repositories bear out
// the fleet's mantra, LICENSE=BSD-3-Clause and CGO_ENABLED=0, before a
// landing page says so.
//
//	mantracheck go-slides go-fft go-macos
//
// # WHY IT EXISTS
//
// The landing kit (go-fleettools/landingkit) prints the mantra on every
// landing that uses it. A page that says CGO_ENABLED=0 over an organisation
// that binds a C library publishes a false claim to everyone who reads it,
// and nothing in a build fails when it does. So the claim is checked against
// the repositories, per organisation, before it is shown.
//
// # WHAT IT READS, AND WHAT IT CANNOT
//
// The licence is GitHub's own detection (license.spdx_id in the listing): an
// unrecognised or missing LICENSE file reads as NOASSERTION or none, and is
// reported as such, never as BSD-3-Clause.
//
// cgo is read from the FILE TREE, without fetching a single source file:
// C, C++, Objective-C or Swift sources, and Go files whose names say cgo.
// Those are SIGNALS, not proof: a parser's C test fixture is a .c file
// too, which is why testdata/ and vendor/ are not looked at, and why a
// signal is reported with the files that raised it, for a person to read.
// The other direction holds firmly: no signal in a tree that was read whole
// is a repository with no C sources to link. A tree GitHub truncated is
// reported UNREAD, never clean.
//
// Exits 1 when an organisation does not bear the mantra out (or could not be
// read), 2 on a usage error.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

var osExit = os.Exit

// gh is the GitHub call (fleet.GH), replaced in tests.
var gh = fleet.GH

// failure is a check that ran and found the mantra not borne out, or could
// not read what it needed (exit 1); anything else is a usage error (exit 2).
type failure struct{ err error }

func (f *failure) Error() string { return f.err.Error() }
func (f *failure) Unwrap() error { return f.err }

// repo is one repository of the listing, with its licence.
type repo struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Archived      bool   `json:"archived"`
	Fork          bool   `json:"fork"`
	Private       bool   `json:"private"`
	License       *struct {
		SPDX string `json:"spdx_id"`
	} `json:"license"`
}

func (r repo) spdx() string {
	if r.License == nil || r.License.SPDX == "" {
		return "none"
	}
	return r.License.SPDX
}

// tree is a repository's file tree as the API returns it.
type tree struct {
	Truncated bool `json:"truncated"`
	Tree      []struct {
		Path string `json:"path"`
		Type string `json:"type"`
	} `json:"tree"`
}

// cgoSources are the extensions of sources a cgo build compiles.
var cgoSources = map[string]bool{".c": true, ".h": true, ".m": true, ".mm": true, ".cc": true, ".cpp": true, ".cxx": true, ".hpp": true, ".swift": true}

// cgoSignals are the files in a tree that suggest cgo: C-family sources and
// Go files named for cgo, outside testdata/ and vendor/ (a parser's C
// fixture is not a build input). hasGo says whether the tree holds Go code
// at all: the mantra is a claim about code, and a repository without any
// (the organisation's .github, its brand, its landing site) is judged apart.
func cgoSignals(t tree) (out []string, hasGo bool) {
	for _, e := range t.Tree {
		if e.Type != "blob" {
			continue
		}
		p := e.Path
		if strings.HasPrefix(p, "testdata/") || strings.Contains(p, "/testdata/") || strings.HasPrefix(p, "vendor/") || strings.Contains(p, "/vendor/") {
			continue
		}
		base := path.Base(p)
		ext := path.Ext(base)
		if ext == ".go" {
			hasGo = true
		}
		switch {
		case cgoSources[ext]:
			out = append(out, p)
		case ext == ".go" && (strings.Contains(base, "_cgo") || strings.HasPrefix(base, "cgo_") || base == "cgo.go"):
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, hasGo
}

// verdict is what one repository says about the mantra.
type verdict struct {
	Repo    string
	License string
	Signals []string
	Code    bool   // the tree holds Go code
	Unread  string // why the tree could not be read whole ("" when it was)
}

// licenseOK: a repository with Go code is BSD-3-Clause. One without code
// (an unread tree counts as code: unknown is not exempt) carries no claim.
func (v verdict) licenseOK() bool { return v.License == "BSD-3-Clause" || (!v.Code && v.Unread == "") }

// check reads one organisation.
func check(org string) ([]verdict, error) {
	b, err := gh("api", "orgs/"+org+"/repos?per_page=100&type=public", "--paginate")
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	var rs []repo
	for {
		var page []repo
		if err := dec.Decode(&page); err != nil {
			break
		}
		for _, r := range page {
			if !r.Archived && !r.Fork && !r.Private {
				rs = append(rs, r)
			}
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].FullName < rs[j].FullName })
	var out []verdict
	for _, r := range rs {
		v := verdict{Repo: r.FullName, License: r.spdx()}
		tb, err := gh("api", "repos/"+r.FullName+"/git/trees/"+r.DefaultBranch+"?recursive=1")
		switch {
		case err != nil && strings.Contains(err.Error(), "409"):
			v.Unread = "" // an empty repository: nothing to build, nothing to link
		case err != nil:
			v.Unread = err.Error()
		default:
			var t tree
			if err := json.Unmarshal(tb, &t); err != nil {
				v.Unread = "the tree did not decode: " + err.Error()
			} else if t.Truncated {
				v.Unread = "GitHub truncated the tree"
			} else {
				v.Signals, v.Code = cgoSignals(t)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// report prints an organisation's verdicts and says whether it bears the
// mantra out.
func report(w *strings.Builder, org string, vs []verdict, verbose bool) bool {
	var lic, cgo, unread, nocode []verdict
	for _, v := range vs {
		if !v.licenseOK() {
			lic = append(lic, v)
		}
		if !v.Code && v.Unread == "" && v.License != "BSD-3-Clause" {
			nocode = append(nocode, v)
		}
		if len(v.Signals) > 0 {
			cgo = append(cgo, v)
		}
		if v.Unread != "" {
			unread = append(unread, v)
		}
	}
	ok := len(vs) > 0 && len(lic) == 0 && len(cgo) == 0 && len(unread) == 0
	state := "HOLDS"
	switch {
	case len(vs) == 0:
		state = "NO PUBLIC REPOSITORY"
	case !ok:
		state = "DOES NOT HOLD"
	}
	fmt.Fprintf(w, "%-28s %-20s %3d repos, %d with code not BSD-3-Clause, %d with cgo signals, %d unread\n",
		org, state, len(vs), len(lic), len(cgo), len(unread))
	for _, v := range lic {
		fmt.Fprintf(w, "    licence  %-48s %s\n", v.Repo, v.License)
	}
	if len(nocode) > 0 {
		names := make([]string, len(nocode))
		for i, v := range nocode {
			names[i] = strings.TrimPrefix(v.Repo, org+"/")
		}
		// said, not counted against the mantra: no code, no claim about it
		fmt.Fprintf(w, "    no Go code and no BSD-3-Clause licence (no claim made about them): %s\n", strings.Join(names, ", "))
	}
	for _, v := range cgo {
		shown := v.Signals
		if !verbose && len(shown) > 3 {
			shown = append(shown[:3:3], fmt.Sprintf("… %d more", len(v.Signals)-3))
		}
		fmt.Fprintf(w, "    cgo?     %-48s %s\n", v.Repo, strings.Join(shown, ", "))
	}
	for _, v := range unread {
		fmt.Fprintf(w, "    UNREAD   %-48s %s\n", v.Repo, v.Unread)
	}
	return ok
}

const longHelp = `Checks that an organisation's public repositories bear out the fleet's
mantra, LICENSE=BSD-3-Clause and CGO_ENABLED=0, before a landing page shows it.

The licence is GitHub's detection (NOASSERTION or none is reported, never
taken for BSD-3-Clause). cgo is read from each file tree: C, C++, Objective-C
and Swift sources and Go files named for cgo, outside testdata/ and vendor/.
Those are signals for a person to read, listed with the files that raised
them; a tree read whole with no signal has no C sources to link. A truncated
tree is UNREAD, never clean.

Exits 1 when an organisation does not bear the mantra out or could not be
read, 2 on a usage error.`

// RootCmd is the command.
func RootCmd() *cobra.Command {
	var verbose bool
	c := &cobra.Command{
		Use:           "mantracheck ORG [ORG...]",
		Short:         "Do these organisations bear out LICENSE=BSD-3-Clause and CGO_ENABLED=0?",
		Long:          longHelp,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MinimumNArgs(1),
	}
	c.Flags().BoolVarP(&verbose, "verbose", "v", false, "list every cgo signal, not the first three")
	c.RunE = func(cmd *cobra.Command, orgs []string) error {
		var bad []string
		for _, org := range orgs {
			vs, err := check(org)
			if err != nil {
				// one unreadable organisation does not hide the others
				fmt.Fprintf(cmd.OutOrStdout(), "%-28s %-20s %v\n", org, "UNREAD", err)
				bad = append(bad, org)
				continue
			}
			var b strings.Builder
			if !report(&b, org, vs, verbose) {
				bad = append(bad, org)
			}
			fmt.Fprint(cmd.OutOrStdout(), b.String())
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%d of %d organisation(s) bear the mantra out\n", len(orgs)-len(bad), len(orgs))
		if len(bad) > 0 {
			return &failure{fmt.Errorf("not borne out by %s", strings.Join(bad, ", "))}
		}
		return nil
	}
	return c
}

func execute(c *cobra.Command) int {
	err := c.Execute()
	if err == nil {
		return 0
	}
	fmt.Fprintf(c.ErrOrStderr(), "mantracheck: %v\n", err)
	var f *failure
	if errors.As(err, &f) {
		return 1
	}
	return 2
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	osExit(execute(RootCmd()))
}
