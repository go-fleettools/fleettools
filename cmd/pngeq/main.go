// Command pngeq says whether the PNG images of two trees hold the same
// pixels:
//
//	pngeq GENERATED REFERENCE
//
// Every .png under GENERATED is decoded, with its counterpart under REFERENCE,
// to 8-bit non-premultiplied RGBA, and the two are compared channel by channel.
//
// ⛔ It exists because byte equality answers the wrong question. A brand kit
// moved to its own repository and regenerated there differed in 65 of 73
// files, byte for byte -- and in none of its 51 PNGs, pixel for pixel: a newer
// encoder had compressed the same pictures differently. Without this, "the
// kit does not reproduce" would have been the verdict, and it was false.
package main

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

var osExit = os.Exit

type failure struct{ err error }

func (f *failure) Error() string { return f.err.Error() }

const longHelp = `pngeq compares the decoded pixels of the PNG images of two trees.

Every .png under GENERATED is decoded with its counterpart under REFERENCE to
8-bit RGBA (straight alpha) and compared channel by channel. --tolerance N
accepts channel differences up to N (0 by default: exact).

A pair of different sizes, a missing counterpart or an image that does not
decode is a difference. A tree with no PNG at all is exit 2: a comparison of
nothing is not a match.

Exits 0 when every pair matches, 1 when one does not, 2 on a usage error.`

// Pair is the comparison of one image.
type Pair struct {
	Rel      string
	Problem  string // empty when the pixels match
	Differ   int    // channel values beyond the tolerance
	MaxDelta int
}

func RootCmd() *cobra.Command {
	var tolerance, show int
	c := &cobra.Command{
		Use:           "pngeq GENERATED REFERENCE [flags]",
		Short:         "Do the PNGs of two trees hold the same pixels?",
		Long:          longHelp,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ExactArgs(2),
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if tolerance < 0 || tolerance > 255 || show < 0 {
			return errors.New("--tolerance must be 0..255 and --show not negative")
		}
		pairs, err := Compare(args[0], args[1], tolerance)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		bad, worst, shown := 0, 0, 0
		for _, p := range pairs {
			worst = max(worst, p.MaxDelta)
			if p.Problem == "" {
				continue
			}
			bad++
			if shown < show {
				fmt.Fprintf(out, "%s: %s\n", p.Rel, p.Problem)
				shown++
			}
		}
		fmt.Fprintf(out, "pixel-identical %d, differing %d, largest channel difference %d\n", len(pairs)-bad, bad, worst)
		if bad > 0 {
			return &failure{fmt.Errorf("%d of %d images differ", bad, len(pairs))}
		}
		return nil
	}
	c.Flags().IntVarP(&tolerance, "tolerance", "t", 0, "largest channel difference still counted as equal")
	c.Flags().IntVarP(&show, "show", "n", 20, "how many differing images to print")
	return c
}

func execute(c *cobra.Command) int {
	err := c.Execute()
	if err == nil {
		return 0
	}
	fmt.Fprintf(c.ErrOrStderr(), "pngeq: %v\n", err)
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

func decode(p string) (*image.NRGBA, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	out := image.NewNRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
	draw.Draw(out, out.Bounds(), img, img.Bounds().Min, draw.Src)
	return out, nil
}

// Compare compares every PNG under gen with its counterpart under ref.
func Compare(gen, ref string, tolerance int) ([]Pair, error) {
	var rels []string
	err := filepath.WalkDir(gen, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && strings.EqualFold(filepath.Ext(p), ".png") {
			rel, err := filepath.Rel(gen, p)
			if err != nil {
				return err
			}
			rels = append(rels, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(rels) == 0 {
		return nil, fmt.Errorf("%s holds no PNG: a comparison of nothing is not a match", gen)
	}
	sort.Strings(rels)
	pairs := make([]Pair, 0, len(rels))
	for _, rel := range rels {
		pairs = append(pairs, comparePair(filepath.Join(gen, rel), filepath.Join(ref, rel), filepath.ToSlash(rel), tolerance))
	}
	return pairs, nil
}

func comparePair(a, b, rel string, tolerance int) Pair {
	p := Pair{Rel: rel}
	ia, err := decode(a)
	if err != nil {
		p.Problem = "generated: " + err.Error()
		return p
	}
	ib, err := decode(b)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			p.Problem = "missing in reference"
		} else {
			p.Problem = "reference: " + err.Error()
		}
		return p
	}
	if ia.Bounds() != ib.Bounds() {
		p.Problem = fmt.Sprintf("size %dx%d vs %dx%d", ia.Bounds().Dx(), ia.Bounds().Dy(), ib.Bounds().Dx(), ib.Bounds().Dy())
		return p
	}
	for i := range ia.Pix {
		d := int(ia.Pix[i]) - int(ib.Pix[i])
		if d < 0 {
			d = -d
		}
		p.MaxDelta = max(p.MaxDelta, d)
		if d > tolerance {
			p.Differ++
		}
	}
	if p.Differ > 0 {
		p.Problem = fmt.Sprintf("%d channel values differ, largest by %d", p.Differ, p.MaxDelta)
	}
	return p
}
