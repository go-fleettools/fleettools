// Command landingmigrate moves an organisation's landing onto the landing
// kit (go-fleettools/landingkit), one repository at a time, and says what it
// changed.
//
//	landingmigrate inventory --out landing-migration.json
//	landingmigrate migrate ./go-icons.github.io --org go-icons --kit v0.6.0 --data "graphics: CC-BY-4.0, MIT, CC0-1.0" --report r.json
//	landingmigrate verify --old old/ --on on/ --off off/ --report r.json
//	landingmigrate repeats layouts/index.html --write
//
// # WHY IT EXISTS
//
// Seventeen landings import the kit. The rest carry a copy of the fleet's
// first landing layout, each with its own stylesheet, so a fix to the
// design (a dark-mode contrast, the mantra strip) reaches none of them.
// Moving one by hand is an afternoon; this moves one in a command and
// proves what it did.
//
// # WHAT IT DOES TO ONE LANDING
//
// It parses the template layout by its markers and refuses anything in it
// it does not know, rather than dropping it. It writes the kit's layout
// around the same content: the header's h1, tagline, buttons and pills
// become the kit's hero, the first code snippet its terminal, <main> stays
// as written under the template's own rules, scoped to it. It removes the
// copies of the kit's partials, adds go.mod, kit.go and the hugo.toml
// import, reads [params.brand] from the logo, and gives the workflow that
// installs hugo the go command it now needs.
//
// The strip says LICENSE=BSD-3-Clause and CGO_ENABLED=0, so a pill, badge,
// sentence or footer segment that says nothing else is hidden while the
// strip shows ({{ if not (partial "mantra-on.html" $) }}). One that says
// more is kept and listed in the report, for the pull request.
//
// # HOW IT IS PROVED
//
// verify reads three builds: the old page, the new page, and the new page
// with the strip turned off. Every text node, link and picture of the old
// page must be in the strip-off page (the licence badge excepted), and the
// strip-on page must be the strip-off page minus exactly the hidden
// repeats. A difference is printed and fails the command.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

var osExit = os.Exit

// orgs lists the organisations (fleet.Orgs), replaced in tests.
var orgs = fleet.Orgs

// failure: the command ran and found something wrong (exit 1).
type failure struct{ err error }

func (f *failure) Error() string { return f.err.Error() }
func (f *failure) Unwrap() error { return f.err }

// RootCmd is the command.
func RootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "landingmigrate",
		Short:         "Move a landing onto the landing kit, and prove what changed",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(inventoryCmd(), migrateCmd(), verifyCmd(), repeatsCmd(), markCmd())
	return root
}

func inventoryCmd() *cobra.Command {
	var out, kit string
	c := &cobra.Command{
		Use:   "inventory [ORG...]",
		Short: "Classify every organisation's landing (kit, template, copied, no-landing) into a JSON file",
		RunE: func(cmd *cobra.Command, args []string) error {
			if out == "" {
				return errors.New("--out is required")
			}
			list := args
			if len(list) == 0 {
				var err error
				if list, err = orgs(); err != nil {
					return &failure{err}
				}
			}
			es, err := inventory(list)
			if err != nil {
				return &failure{err}
			}
			es = merge(es, out)
			count := map[string]int{}
			for _, e := range es {
				count[e.State]++
			}
			if err := writeJSON(out, inventoryFile{Generated: stamp(), LatestKit: kit, States: statesDoc, Orgs: es}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%d organisations: %d kit, %d template, %d copied, %d no-landing, %d archived -> %s\n",
				len(es), count["kit"], count["template"], count["copied"], count["no-landing"], count["archived"], out)
			return nil
		},
	}
	c.Flags().StringVarP(&out, "out", "o", "", "the inventory file to write (an earlier one's migrations are kept)")
	c.Flags().StringVar(&kit, "kit", "", "the kit's latest version, recorded in the file")
	return c
}

func migrateCmd() *cobra.Command {
	var org, kit, rep string
	var data []string
	c := &cobra.Command{
		Use:   "migrate DIR",
		Short: "Move the template landing checked out at DIR onto the kit",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if org == "" || kit == "" {
				return errors.New("--org and --kit are required")
			}
			r, err := migrate(args[0], org, kit, data)
			if err != nil {
				return &failure{err}
			}
			if rep != "" {
				if err := writeJSON(rep, r); err != nil {
					return err
				}
			}
			fmt.Fprint(cmd.OutOrStdout(), summary(r))
			return nil
		},
	}
	c.Flags().StringVar(&org, "org", "", "the organisation")
	c.Flags().StringVar(&kit, "kit", "", "the kit version to require (vX.Y.Z)")
	c.Flags().StringArrayVar(&data, "data", nil, `a data licence chip for the strip, e.g. "fonts: OFL-1.1" (repeatable, at most two)`)
	c.Flags().StringVar(&rep, "report", "", "write what was done here, for verify and the pull request")
	return c
}

// summary is the migration in words, for a pull request body.
func summary(r report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s landing moved onto landingkit %s\n", r.Org, r.Shape, r.Kit)
	fmt.Fprintf(&b, "  written: %s\n", strings.Join(r.Written, ", "))
	if len(r.Removed) > 0 {
		fmt.Fprintf(&b, "  removed (the kit ships them): %s\n", strings.Join(r.Removed, ", "))
	}
	if len(r.Workflows) > 0 {
		fmt.Fprintf(&b, "  setup-go added to: %s\n", strings.Join(r.Workflows, ", "))
	}
	if len(r.Data) > 0 {
		fmt.Fprintf(&b, "  data chips: %s\n", strings.Join(r.Data, " | "))
	}
	for _, f := range r.Fragments {
		state := "KEPT, says more: review"
		if f.Hidden {
			state = "hidden while the strip shows"
		}
		fmt.Fprintf(&b, "  %-8s %-30s %s\n", f.Where, state, f.Text)
	}
	return b.String()
}

func verifyCmd() *cobra.Command {
	var oldDir, onDir, offDir, rep, file string
	c := &cobra.Command{
		Use:   "verify",
		Short: "Prove a migration: nothing of the old page lost, only the hidden repeats gone",
		RunE: func(cmd *cobra.Command, args []string) error {
			if oldDir == "" || onDir == "" || offDir == "" || rep == "" {
				return errors.New("--old, --on, --off and --report are required")
			}
			var r report
			b, err := os.ReadFile(rep)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(b, &r); err != nil {
				return err
			}
			read := func(d string) (page, error) {
				f, err := os.Open(filepath.Join(d, file))
				if err != nil {
					return page{}, err
				}
				defer f.Close()
				return readPage(f)
			}
			old, err := read(oldDir)
			if err != nil {
				return err
			}
			on, err := read(onDir)
			if err != nil {
				return err
			}
			off, err := read(offDir)
			if err != nil {
				return err
			}
			v := judge(old, on, off, r.Fragments)
			w := cmd.OutOrStdout()
			for _, s := range v.Lost {
				fmt.Fprintf(w, "LOST     %s\n", s)
			}
			for _, s := range v.LostLinks {
				fmt.Fprintf(w, "LOST     %s\n", s)
			}
			for _, s := range v.Shown {
				fmt.Fprintf(w, "SHOWN    a hidden repeat is on the strip-on page: %s\n", s)
			}
			for _, s := range v.Extra {
				fmt.Fprintf(w, "DIFFERS  %s\n", s)
			}
			if !v.ok() {
				return &failure{errors.New("the migration changed more than it says")}
			}
			fmt.Fprintf(w, "VERIFIED %d text nodes, %d links, %d pictures of the old page kept; the strip-on page is the strip-off page minus %d hidden repeat(s)\n",
				len(old.Nodes), len(old.Hrefs), len(old.Srcs), hiddenCount(r.Fragments))
			return nil
		},
	}
	c.Flags().StringVar(&oldDir, "old", "", "the old site, built")
	c.Flags().StringVar(&onDir, "on", "", "the migrated site, built")
	c.Flags().StringVar(&offDir, "off", "", "the migrated site, built with [params.mantra] show = false")
	c.Flags().StringVar(&rep, "report", "", "the migration's report (migrate --report)")
	c.Flags().StringVar(&file, "page", "index.html", "the page to compare, relative to each site")
	return c
}

func hiddenCount(fs []fragment) int {
	n := 0
	for _, f := range fs {
		if f.Hidden {
			n++
		}
	}
	return n
}

func repeatsCmd() *cobra.Command {
	var write bool
	var rep string
	c := &cobra.Command{
		Use:   "repeats LAYOUT",
		Short: "Find (and with --write hide) what a layout says that the mantra strip already says",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			out, frags := repeats(string(b))
			fmt.Fprint(cmd.OutOrStdout(), summary(report{Org: filepath.Base(args[0]), Shape: "layout", Kit: "(unchanged)", Fragments: frags}))
			if rep != "" {
				if err := writeJSON(rep, report{Fragments: frags}); err != nil {
					return err
				}
			}
			if write && out != string(b) {
				return os.WriteFile(args[0], []byte(out), 0o644)
			}
			return nil
		},
	}
	c.Flags().BoolVarP(&write, "write", "w", false, "hide the repeats in the file")
	c.Flags().StringVar(&rep, "report", "", "write the fragments found, for verify")
	return c
}

func markCmd() *cobra.Command {
	var inv string
	c := &cobra.Command{
		Use:   "mark ORG URL",
		Short: "Record in the inventory the pull request that migrated ORG",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := os.ReadFile(inv)
			if err != nil {
				return err
			}
			var f inventoryFile
			if err := json.Unmarshal(b, &f); err != nil {
				return err
			}
			for i := range f.Orgs {
				if f.Orgs[i].Org == args[0] {
					f.Orgs[i].Migrated = args[1]
					f.Orgs[i].State = "kit"
					return writeJSON(inv, f)
				}
			}
			return &failure{fmt.Errorf("%s is not in %s", args[0], inv)}
		},
	}
	c.Flags().StringVar(&inv, "inventory", "", "the inventory file")
	return c
}

func execute(c *cobra.Command) int {
	err := c.Execute()
	if err == nil {
		return 0
	}
	fmt.Fprintf(c.ErrOrStderr(), "landingmigrate: %v\n", err)
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
