// brandcheck says whether a brand repository carries everything it claims to.
//
//	brandcheck -org go-ooxml .
//
// A brand repository is assets and a README, so it has no tests and nothing
// compiles — which means it had no CI at all, and `ghmerge` refuses a pull
// request no check has run against. That refusal is right: a half-generated
// asset set is exactly the kind of thing that ships silently, because the
// missing file is one nobody opens until the day they need a 48 px icon.
//
// ⛔ What it checks is the INVENTORY and the GRAMMAR, not the pixels. Whether
// a logo is any good is a question for a person; whether the 512 px variant
// exists, whether the three colour variants are all there, and whether the
// mark follows the family grammar are questions with answers.
//
// The grammar is the brand system's own, and it is checked because it drifts
// by hand: a 256-unit plate, a rounded corner, a gradient with two stops, and
// a glyph that is stroked rather than filled. A mark that fails these is not
// wrong so much as not in the family, and the family is the point.
package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Checks the INVENTORY and the GRAMMAR of a brand repository, not the pixels:
whether the 512 px variant exists, whether the three colourways are all there,
and whether the mark follows the family grammar.

With no directory it reads the current one, and takes the organisation's name
from the directory's parent unless --org says otherwise — which is how CI says
it, since there the checkout is the repository alone.

Exit status: 0 everything the README promises is present and in the family,
1 something is missing or out of grammar, 2 the check could not be made.`

// sizes are the raster sizes every brand repository carries, and variants the
// three colourways. Both are what logos/brandgen writes; this is the list a
// reader of the README is promised.
var (
	sizes    = []string{"16", "32", "48", "64", "128", "256", "512", "1024"}
	variants = []string{"color", "white", "black"}
)

func main() {
	// Every command here says whether the binary is behind the head it was
	// built from, and a repository-wide test enumerates cmd/ on disk to make
	// sure none forgets. It caught this one.
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it through run, below.
func RootCmd() *cobra.Command {
	var org string
	c := cli.New("brandcheck [flags] [directory]",
		"Whether a brand repository carries everything it claims to", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return cli.FromStatus(checkBrand(cli.Out(cmd), cli.Err(cmd), org, args))
	}
	c.Flags().StringVar(&org, "org", "",
		"the organisation the assets are for (default: the directory's name)")
	return c
}

// run keeps the signature this command's tests were written against, and now
// routes them through the real command: pflag parsing, the single-dash
// compatibility shim and the exit-status mapping included. Before the migration
// these tests drove a hand-rolled FlagSet that no user ever reached.
func run(args []string, out, errOut io.Writer) int {
	return cli.Drive(RootCmd(), args, out, errOut)
}

// checkBrand is the command's work. The name avoids `check`, which this
// package's tests already use for a helper of their own.
func checkBrand(out, errOut io.Writer, org string, args []string) int {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	name := org
	if name == "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			fmt.Fprintln(errOut, "brandcheck:", err)
			return 2
		}
		// A brand repository is <org>/brand, so the organisation is the
		// parent directory — which is right when it is checked out that way
		// and wrong in CI, where the checkout is the repository alone. -org
		// is how CI says it.
		name = filepath.Base(filepath.Dir(abs))
	}

	missing := missingFiles(dir, name)
	problems := grammar(filepath.Join(dir, "svg", "color", name+".svg"))

	fmt.Fprintf(out, "brandcheck %s in %s\n", name, dir)
	for _, m := range missing {
		fmt.Fprintln(out, "  MISSING  ", m)
	}
	for _, p := range problems {
		fmt.Fprintln(out, "  GRAMMAR  ", p)
	}
	if len(missing) == 0 && len(problems) == 0 {
		fmt.Fprintf(out, "  every file the README promises is here, and the mark is in the family\n")
		return 0
	}
	fmt.Fprintf(out, "\n%d file(s) missing, %d grammar problem(s)\n", len(missing), len(problems))
	return 1
}

// missingFiles is every path the README promises and the repository does not
// have.
func missingFiles(dir, org string) []string {
	var want []string
	for _, v := range variants {
		want = append(want, filepath.Join("svg", v, org+".svg"))
		for _, s := range sizes {
			want = append(want, filepath.Join("png", v, s, org+".png"))
		}
	}
	for _, s := range sizes {
		want = append(want, filepath.Join("jpg", s, org+".jpg"))
	}
	want = append(want,
		filepath.Join("ico", org+".ico"),
		filepath.Join("icns", org+".icns"),
		filepath.Join("avatar", org+".png"),
		filepath.Join("social", org+".png"),
		"README.md",
		"LICENSE",
	)
	var missing []string
	for _, w := range want {
		if _, err := os.Stat(filepath.Join(dir, w)); err != nil {
			missing = append(missing, w)
		}
	}
	return missing
}

// an svgDoc is as much of a mark as this has an opinion about.
type svgDoc struct {
	ViewBox string `xml:"viewBox,attr"`
	Rects   []struct {
		RX string `xml:"rx,attr"`
	} `xml:"rect"`
	Defs struct {
		Gradients []struct {
			Stops []struct {
				Color string `xml:"stop-color,attr"`
			} `xml:"stop"`
		} `xml:"linearGradient"`
	} `xml:"defs"`
	Groups []struct {
		Fill   string `xml:"fill,attr"`
		Stroke string `xml:"stroke,attr"`
		Width  string `xml:"stroke-width,attr"`
		Cap    string `xml:"stroke-linecap,attr"`
	} `xml:"g"`
}

// grammar reports where a mark departs from the family's.
//
// ⛔ It does NOT check the stroke width against a number. Measured over the
// 312 organisation marks there is no fleet-wide one — 13 is the commonest, 14
// and 15 and 22 all appear, and 10 appears once — so a check against a
// constant would fail most of the fleet and teach everyone to ignore it. What
// it checks is that the glyph is STROKED at all, which is the thing that
// distinguishes the family's line marks from a filled shape.
func grammar(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return []string{"the colour SVG is not readable: " + err.Error()}
	}
	var doc svgDoc
	if err := xml.Unmarshal(b, &doc); err != nil {
		return []string{"the colour SVG does not parse: " + err.Error()}
	}
	var out []string
	if doc.ViewBox != "0 0 256 256" {
		out = append(out, fmt.Sprintf("the viewBox is %q, and the family's plate is 256 units square", doc.ViewBox))
	}
	rounded := false
	for _, r := range doc.Rects {
		if r.RX != "" && r.RX != "0" {
			rounded = true
			break
		}
	}
	if !rounded {
		out = append(out, "no rounded rectangle: the family's plate has one")
	}
	stops := 0
	for _, g := range doc.Defs.Gradients {
		stops += len(g.Stops)
	}
	if stops < 2 {
		out = append(out, fmt.Sprintf("%d gradient stop(s): the family's colour is a gradient of two", stops))
	}
	stroked := false
	for _, g := range doc.Groups {
		if g.Stroke != "" && g.Stroke != "none" && g.Width != "" {
			stroked = true
			if !strings.EqualFold(g.Cap, "round") {
				out = append(out, fmt.Sprintf("the glyph's caps are %q, and the family's are round", g.Cap))
			}
		}
	}
	if !stroked {
		out = append(out, "the glyph is not stroked: the family's marks are drawn in lines, not filled shapes")
	}
	return out
}
