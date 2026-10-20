// Command sitelinks says whether every local link of a BUILT static site
// leads to a file of that build:
//
//	sitelinks [--base /docs/development/] PUBLIC_DIR
//
// It reads every .html under PUBLIC_DIR, takes the href, src, poster and
// srcset attributes, resolves each local one against the page's own URL under
// --base, and checks that the target exists in the build (a directory counts
// when it holds an index.html). External links are not fetched.
//
// ⛔ It parses HTML, it does not grep it. The first version was a regular
// expression over the page and reported two "broken" links on every page of a
// Hugo site: `e.docsRoot+t.version+` and a minified line of a version-selector
// script, both read as an href= inside an inline <script>. Attributes come
// from golang.org/x/net/html's tokenizer here, so text inside <script> is
// never an attribute.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/net/html"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

var osExit = os.Exit

type failure struct{ err error }

func (f *failure) Error() string { return f.err.Error() }

const longHelp = `sitelinks checks the local links of a built static site.

Every href, src, poster and srcset of every .html under PUBLIC_DIR that is not
external (no scheme, no host) is resolved against the page's URL -- the page
sits at --base plus its path under PUBLIC_DIR -- and must name a file of the
build, or a directory holding an index.html.

A root-absolute link outside --base cannot be checked against this build; it
is counted as "outside", printed with --outside, and is not a failure.

Exits 0 when every local link resolves, 1 when one does not, 2 on a usage
error or a build with no page.`

// Report is what one check found.
type Report struct {
	Pages, Links, Outside int
	Broken                map[string][]string // target as written -> pages using it
	OutsideLinks          map[string][]string
}

func RootCmd() *cobra.Command {
	var base string
	var show int
	var outside bool
	c := &cobra.Command{
		Use:           "sitelinks PUBLIC_DIR [flags]",
		Short:         "Does every local link of a built site lead to a file of the build?",
		Long:          longHelp,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ExactArgs(1),
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if !strings.HasPrefix(base, "/") || !strings.HasSuffix(base, "/") {
			return errors.New("--base must start and end with /")
		}
		r, err := Check(args[0], base)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		print := func(label string, m map[string][]string) {
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for i, k := range keys {
				if i == show {
					fmt.Fprintf(out, "… and %d more\n", len(keys)-show)
					break
				}
				fmt.Fprintf(out, "%s %s  (%d pages, e.g. %s)\n", label, k, len(m[k]), m[k][0])
			}
		}
		print("BROKEN", r.Broken)
		if outside {
			print("OUTSIDE", r.OutsideLinks)
		}
		fmt.Fprintf(out, "%d pages, %d local links, %d broken targets, %d links outside %s\n",
			r.Pages, r.Links, len(r.Broken), r.Outside, base)
		if len(r.Broken) > 0 {
			return &failure{fmt.Errorf("%d broken targets", len(r.Broken))}
		}
		return nil
	}
	c.Flags().StringVar(&base, "base", "/", "the URL path PUBLIC_DIR is served at, e.g. /docs/development/")
	c.Flags().IntVarP(&show, "show", "n", 20, "how many targets to print per category")
	c.Flags().BoolVar(&outside, "outside", false, "also list the links outside --base")
	return c
}

func execute(c *cobra.Command) int {
	err := c.Execute()
	if err == nil {
		return 0
	}
	fmt.Fprintf(c.ErrOrStderr(), "sitelinks: %v\n", err)
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

// links returns the URL-valued attributes of a page.
func links(doc []byte) []string {
	var out []string
	z := html.NewTokenizer(strings.NewReader(string(doc)))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return out
		case html.StartTagToken, html.SelfClosingTagToken:
			for {
				key, val, more := z.TagAttr()
				switch string(key) {
				case "href", "src", "poster":
					out = append(out, strings.TrimSpace(string(val)))
				case "srcset":
					// "a.png 1x, b.png 2x": the URL is the first field of each item.
					for _, item := range strings.Split(string(val), ",") {
						if f := strings.Fields(item); len(f) > 0 {
							out = append(out, f[0])
						}
					}
				}
				if !more {
					break
				}
			}
		}
	}
}

// Check reads the build under root, served at base.
func Check(root, base string) (Report, error) {
	r := Report{Broken: map[string][]string{}, OutsideLinks: map[string][]string{}}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		doc, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		r.Pages++
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		pageDir := path.Join(base, path.Dir(rel)) + "/"
		for _, raw := range links(doc) {
			u, err := url.Parse(raw)
			if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" || u.Path == "" {
				continue // external, mailto:, javascript:, data:, or a fragment of this page
			}
			r.Links++
			target := u.Path
			if !strings.HasPrefix(target, "/") {
				target = path.Join(pageDir, target)
				if strings.HasSuffix(u.Path, "/") {
					target += "/"
				}
			}
			if !strings.HasPrefix(target, base) && target+"/" != base {
				r.Outside++
				r.OutsideLinks[raw] = append(r.OutsideLinks[raw], rel)
				continue
			}
			if !exists(root, strings.TrimPrefix(target, base)) {
				r.Broken[raw] = append(r.Broken[raw], rel)
			}
		}
		return nil
	})
	if err != nil {
		return r, err
	}
	if r.Pages == 0 {
		return r, fmt.Errorf("%s holds no .html page: nothing was checked", root)
	}
	return r, nil
}

func exists(root, rel string) bool {
	p := filepath.Join(root, filepath.FromSlash(rel))
	st, err := os.Stat(p)
	if err != nil {
		return false
	}
	if st.IsDir() {
		_, err := os.Stat(filepath.Join(p, "index.html"))
		return err == nil
	}
	return true
}
