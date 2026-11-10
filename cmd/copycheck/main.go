// Command copycheck is the gate behind a rule that used to be a sentence:
// code of a copyleft reference (ffmpeg, FAAD2...) may be READ to understand,
// never copied or translated, and its tables never copied verbatim.
//
// A repository declares what it read in .provenance.yml: name, URL, pinned
// revision, SHA-256 of the extracted tree, SPDX licence, and a role (studied,
// oracle, tables-from, ported-from). copycheck fetches each reference into a
// cache outside any repository, REFUSES a hash that differs, REFUSES a role the
// licence does not allow, and then looks for our code in theirs in two ways that
// do not depend on the language: runs of numeric literals (a table survives
// being retyped) and the shape of the code (a C function survives being
// translated into Go). See internal/copycheck for how, and the README for the
// thresholds and the control they were tuned on.
//
// Exit status: 0 clean, 1 findings, 2 the question could not be answered (a
// reference unreadable, a hash mismatch, a bad manifest). An unreadable
// reference is never clean.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/copycheck"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Checks a repository's source against the references its .provenance.yml says
were read, and fails when it resembles one it was not allowed to copy.

Exit status: 0 clean, 1 findings, 2 could not be answered (never "clean").`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// osExit is replaceable so that a test can observe a status.
var osExit = os.Exit

// RootCmd builds the command; tests drive it with SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	var (
		root, manifest, cache string
		asJSON                bool
		th                    = copycheck.DefaultThresholds()
	)
	c := cli.New("copycheck [flags]", "Fail when our source resembles a reference it may not copy", longHelp)
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		m := manifest
		if m == "" {
			m = filepath.Join(root, copycheck.DefaultManifest)
		}
		mf, err := copycheck.LoadManifest(m)
		if err != nil {
			return cli.Unanswerable("%v", err)
		}
		cd := cache
		if cd == "" {
			if cd, err = copycheck.DefaultCacheDir(); err != nil {
				return cli.Unanswerable("%v", err)
			}
		} else if cd, err = copycheck.CheckCacheDir(cd); err != nil {
			return cli.Unanswerable("%v", err)
		}
		fet := &copycheck.Fetcher{Client: &http.Client{Timeout: 30 * time.Minute}, CacheDir: cd, Limits: copycheck.DefaultLimits()}
		if testClient != nil {
			fet.Client = testClient
		}
		rep, err := copycheck.Check(copycheck.Options{Root: root, Manifest: mf, Fetcher: fet, Thresholds: th})
		if err != nil {
			return cli.Unanswerable("%v", err)
		}
		out := cli.Out(cmd)
		if asJSON {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			if err := enc.Encode(rep); err != nil {
				return cli.Unanswerable("%v", err)
			}
		} else {
			printText(out, rep, cmd)
		}
		if len(rep.Findings) > 0 {
			return &cli.Exit{Code: 1}
		}
		return nil
	}
	f := c.Flags()
	f.StringVar(&root, "root", ".", "the repository to check")
	f.StringVar(&manifest, "manifest", "", "the manifest (default <root>/.provenance.yml)")
	f.StringVar(&cache, "cache-dir", "", "where references are cached (default os.UserCacheDir()/copycheck; never inside a git tree)")
	f.BoolVar(&asJSON, "json", false, "print the report as JSON")
	f.IntVar(&th.TableMinRun, "table-min-run", th.TableMinRun, "matching numbers that make a table finding")
	f.IntVar(&th.ShapeMinToks, "shape-min-tokens", th.ShapeMinToks, "normalised tokens in a block that makes a shape finding")
	f.Float64Var(&th.ShapeMinSim, "shape-min-sim", th.ShapeMinSim, "similarity (0..1) a block needs to be a finding")
	f.IntVar(&th.ShapeK, "shape-k", th.ShapeK, "shape fingerprint window, in tokens")
	f.IntVar(&th.ShapeW, "shape-w", th.ShapeW, "shape winnowing window")
	f.IntVar(&th.TableK, "table-k", th.TableK, "table fingerprint window, in numbers (at least 8)")
	f.IntVar(&th.TableW, "table-w", th.TableW, "table winnowing window")
	c.AddCommand(pinCmd())
	return c
}

// testClient lets a test serve archives from a local TLS server.
var testClient *http.Client

func printText(w io.Writer, rep *copycheck.Report, cmd *cobra.Command) {
	d := copycheck.DefaultThresholds()
	if rep.Thresholds != d {
		fmt.Fprintf(w, "note: thresholds differ from the defaults: %+v\n", rep.Thresholds)
	}
	for _, f := range rep.Findings {
		fmt.Fprintf(w, "FINDING %-11s %s\n", f.Kind, f.Message)
		if f.Ours != nil {
			fmt.Fprintf(w, "    ours:   %s:%d-%d\n", f.Ours.File, f.Ours.Start, f.Ours.End)
		}
		if f.Theirs != nil {
			fmt.Fprintf(w, "    theirs: %s %s:%d-%d (%s, role %s)\n", f.Reference, f.Theirs.File, f.Theirs.Start, f.Theirs.End, f.License, f.Role)
		}
		switch {
		case f.Numbers > 0:
			fmt.Fprintf(w, "    %d matching numbers\n", f.Numbers)
		case f.Tokens > 0:
			fmt.Fprintf(w, "    %d tokens, similarity %.0f%%\n", f.Tokens, f.Similarity*100)
		}
	}
	for _, a := range rep.Accepted {
		fmt.Fprintf(w, "accepted (declared values, numbers only): %s\n", a.Message)
	}
	for _, r := range rep.References {
		fmt.Fprintf(w, "read: %s @ %s, %d files, tree %s\n", r.Name, r.Pin, r.Files, r.Tree[:12])
	}
	fmt.Fprintf(w, "copycheck: %d finding(s); %d of our files against %d references\n", len(rep.Findings), rep.OurFiles, len(rep.References))
}

func pinCmd() *cobra.Command {
	var (
		ref   copycheck.Reference
		paths []string
		cache string
		strip int
	)
	c := cli.New("pin [flags]", "Fetch a reference once and print the manifest entry, tree hash included",
		`Downloads the archive, extracts the source files under --path, and prints the YAML
entry to paste into .provenance.yml. The hash is the one every later run will
demand, so read what was fetched before committing it.`)
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		ref.Paths = paths
		ref.Strip = &strip
		cd := cache
		var err error
		if cd == "" {
			cd, err = copycheck.DefaultCacheDir()
		} else {
			cd, err = copycheck.CheckCacheDir(cd)
		}
		if err != nil {
			return cli.Unanswerable("%v", err)
		}
		if !strings.HasPrefix(ref.URL, "https://") || ref.Pin == "" || !strings.Contains(ref.URL, ref.Pin) {
			return cli.Unanswerable("--url must be https and contain --pin")
		}
		fet := &copycheck.Fetcher{Client: &http.Client{Timeout: 30 * time.Minute}, CacheDir: cd, Limits: copycheck.DefaultLimits()}
		if testClient != nil {
			fet.Client = testClient
		}
		_, tree, err := fet.Fetch(ref)
		if err != nil {
			return cli.Unanswerable("%v", err)
		}
		out := cli.Out(cmd)
		fmt.Fprintf(out, "  - name: %s\n    url: %s\n    pin: %s\n    tree_sha256: %s\n    license: %s\n    role: %s\n", ref.Name, ref.URL, ref.Pin, tree, ref.License, ref.Role)
		if strip != 1 {
			fmt.Fprintf(out, "    strip_components: %d\n", strip)
		}
		if len(paths) > 0 {
			fmt.Fprintln(out, "    paths:")
			for _, p := range paths {
				fmt.Fprintf(out, "      - %s\n", p)
			}
		}
		return nil
	}
	f := c.Flags()
	f.StringVar(&ref.Name, "name", "", "reference name (what NOTICE will say)")
	f.StringVar(&ref.URL, "url", "", "https archive of the pinned revision")
	f.StringVar(&ref.Pin, "pin", "", "commit or tag, contained in --url")
	f.StringVar(&ref.License, "license", "", "SPDX expression")
	f.StringVar(&ref.Role, "role", "studied", "studied, oracle, tables-from or ported-from")
	f.StringArrayVar(&paths, "path", nil, "subtree to keep (repeatable)")
	f.IntVar(&strip, "strip-components", 1, "leading path components to drop (1 for GitHub, 0 for googlesource +archive)")
	f.StringVar(&cache, "cache-dir", "", "cache directory")
	return c
}
