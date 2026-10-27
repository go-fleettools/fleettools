// Command sitemigrate moves one docs site checkout (a Hugo site on the
// tannevaled/hextra fork) to the fork's native version selector and Go page
// history generator. It edits files only: the theme submodule bump, the
// commit and the pull request are the caller's. It refuses anything it does
// not recognise, with a message naming the file.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

var osExit = os.Exit

var tagRE = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// RootCmd builds the command; tests drive it with SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	var tag string
	cmd := &cobra.Command{
		Use:   "sitemigrate --tag vX.Y.Z <site dir>",
		Short: "move a docs site to the fork's native version selector and Go page history",
		Long: `sitemigrate edits a docs site checkout for tannevaled/hextra >= v0.21.0:

  - removes layouts/_partials/custom/version-select.html and its one-line
    .fs-version-select rule in assets/css/custom.css;
  - hugo.yaml: the menu item uses components/version-select.html; when the
    site has no params.versions.development it adds "dev" (the local copy's
    default, the theme's is "development");
  - the workflow, README and .gitignore: 'sh themes/hextra/scripts/page-history.sh >'
    becomes 'go run github.com/tannevaled/hextra/cmd/pagehistory@TAG >'.

It refuses a hugo.yaml that does not name the local selector exactly once
(a tree already migrated, or one it does not know).`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !tagRE.MatchString(tag) {
				return fmt.Errorf("--tag %q is not vMAJOR.MINOR.PATCH", tag)
			}
			did, err := migrate(args[0], tag)
			for _, d := range did {
				fmt.Fprintln(cmd.OutOrStdout(), d)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "", "the fork's release to pin the page history generator to (required)")
	_ = cmd.MarkFlagRequired("tag")
	return cmd
}

func readFile(dir, rel string) (string, bool, error) {
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	return string(b), err == nil, err
}

func write(dir, rel, s string) error {
	return os.WriteFile(filepath.Join(dir, rel), []byte(s), 0o644)
}

// migrate edits the files of dir and lists what it did.
func migrate(dir, tag string) ([]string, error) {
	var did []string
	local := "layouts/_partials/custom/version-select.html"
	if err := os.Remove(filepath.Join(dir, local)); err == nil {
		did = append(did, "removed "+local)
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	y, ok, err := readFile(dir, "hugo.yaml")
	if err != nil || !ok {
		return nil, fmt.Errorf("hugo.yaml: unreadable (%v)", err)
	}
	if strings.Count(y, "partial: custom/version-select.html") != 1 {
		return did, fmt.Errorf("hugo.yaml: expected exactly one 'partial: custom/version-select.html'")
	}
	y = strings.Replace(y, "partial: custom/version-select.html", "partial: components/version-select.html", 1)
	if !strings.Contains(y, "development:") {
		// The local copy defaulted to "dev", the theme to "development": keep "dev".
		if strings.Count(y, "\nparams:\n") != 1 || strings.Contains(y, "\n  versions:") {
			return did, fmt.Errorf("hugo.yaml: no params.versions.development and cannot add it")
		}
		y = strings.Replace(y, "\nparams:\n", "\nparams:\n  versions:\n    # What the version selector shows with the development icon.\n    development: \"dev\"\n", 1)
		did = append(did, "hugo.yaml: params.versions.development: dev (the previous default)")
	}
	y = strings.ReplaceAll(y, "layouts/_partials/custom/version-select.html", "themes/hextra/layouts/_partials/components/version-select.html")

	cmd := "go run github.com/tannevaled/hextra/cmd/pagehistory@" + tag + " >"
	for _, rel := range []string{".github/workflows/docs.yml", "README.md", ".gitignore"} {
		s, ok, err := readFile(dir, rel)
		if err != nil {
			return did, err
		}
		if !ok {
			continue
		}
		o := s
		s = strings.ReplaceAll(s, "sh themes/hextra/scripts/page-history.sh >", cmd)
		s = strings.ReplaceAll(s, "themes/hextra/scripts/page-history.sh", "the theme's cmd/pagehistory")
		if rel == "README.md" {
			var keep []string
			for _, l := range strings.Split(s, "\n") {
				if strings.Contains(l, "layouts/_partials/custom/version-select.html") {
					did = append(did, "README: dropped the row for the local selector")
					continue
				}
				keep = append(keep, l)
			}
			s = strings.Join(keep, "\n")
		}
		if s != o {
			if err := write(dir, rel, s); err != nil {
				return did, err
			}
			did = append(did, "edited "+rel)
		}
	}
	y = strings.ReplaceAll(y, "themes/hextra/scripts/page-history.sh", "the theme's cmd/pagehistory")
	if err := write(dir, "hugo.yaml", y); err != nil {
		return did, err
	}
	did = append(did, "edited hugo.yaml")

	c, ok, err := readFile(dir, "assets/css/custom.css")
	if err != nil {
		return did, err
	}
	if ok {
		var keep []string
		for _, l := range strings.Split(c, "\n") {
			if strings.Contains(l, "fs-version") {
				if !strings.HasPrefix(l, ".fs-version-select ") || !strings.HasSuffix(strings.TrimSpace(l), "}") {
					return did, fmt.Errorf("custom.css: unexpected rule %q", l)
				}
				did = append(did, "custom.css: dropped "+strings.TrimSpace(l))
				continue
			}
			keep = append(keep, l)
		}
		if err := write(dir, "assets/css/custom.css", strings.Join(keep, "\n")); err != nil {
			return did, err
		}
	}
	return did, nil
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	if err := RootCmd().Execute(); err != nil {
		osExit(1)
	}
}
