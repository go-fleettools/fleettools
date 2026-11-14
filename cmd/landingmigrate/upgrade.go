package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

// upgrade brings a landing that is ALREADY on the kit to what the kit now
// asks of one (the batch-C recipe of the 2026-11 landing sweep):
//
//   - the pin: go.mod at --kit, go 1.27.4;
//   - the hero row: a hand-written <div class="cta"> becomes [[params.cta]] and
//     {{ partial "cta.html" . }} (btn-try-style.html for a playground button);
//   - a call to commons-ribbon.html is dropped, fork-ribbon.html draws the band
//     since v0.29.0 and a second call doubles it;
//   - the hero tagline's repeat of the strip is hidden (wrapTagline);
//   - [params.mantra] goarch, when --goarch is given;
//   - a workflow that runs `landingkit check public` after a Hugo build.
//
// Anything it cannot express is a refusal before the first write.

var (
	ctaRowRe     = regexp.MustCompile(`(?s)[ \t]*<div class="cta"[^>]*>.*?</div>\n?`)
	commonsCall  = regexp.MustCompile(`\{\{\s*partial "commons-ribbon\.html" \.\s*\}\}`)
	subRe        = regexp.MustCompile(`(?s)(<p class="sub">)(.*?)(</p>)`)
	mantraTable  = regexp.MustCompile(`(?m)^\[params\.mantra\]\s*$`)
	goarchLineRe = regexp.MustCompile(`(?m)^\s*goarch\s*=`)
)

const checkWorkflow = `name: landingkit-check

# The landing kit's own rules (a hand-written hero row, a playground link that is
# not btn-try, a fact said twice beside the mantra strip, a fork ribbon with no
# Digital Commons band), read from the BUILT site.

on:
  pull_request:
    branches: [main]
  push:
    branches: [main]
  workflow_dispatch:

permissions:
  contents: read

jobs:
  check:
    name: the page follows the kit
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version-file: go.mod
          cache: false
      - uses: peaceiris/actions-hugo@v3
        with:
          hugo-version: "0.169.0"
          extended: true
      - run: hugo --minify --destination ./public
      - name: Check
        env:
          GOWORK: "off"
        run: go run github.com/go-fleettools/landingkit/cmd/landingkit check public
`

// upgrade changes the landing at dir in place and returns what it did.
func upgrade(dir, kit string, goarch []string) ([]string, error) {
	read := func(p string) (string, error) {
		b, err := os.ReadFile(filepath.Join(dir, p))
		return string(b), err
	}
	gm, err := read("go.mod")
	if err != nil || !strings.Contains(gm, "github.com/go-fleettools/landingkit") {
		return nil, errors.New("not on the kit: no go.mod requiring landingkit (use migrate)")
	}
	src, err := read("layouts/index.html")
	if err != nil {
		return nil, err
	}
	conf, err := read("hugo.toml")
	if err != nil {
		return nil, err
	}
	var did []string

	// the hero row
	if m := ctaRowRe.FindString(src); m != "" && !strings.Contains(m, "data-kit-cta") {
		var ctas []cta
		for _, re := range []*regexp.Regexp{btnRe, btnRe2} {
			for _, b := range re.FindAllStringSubmatch(m, -1) {
				ctas = append(ctas, cta{Href: b[1], Inner: strings.TrimSpace(b[2])})
			}
		}
		if len(ctas) == 0 {
			return nil, errors.New("a .cta row with no button this tool can read")
		}
		for _, c := range ctas {
			if strings.Contains(c.Href, "{{") || ctaLabel(c) == "" {
				return nil, fmt.Errorf("the button %q is built from a Hugo expression, which [[params.cta]] cannot hold", clip(c.Href+" "+c.Inner))
			}
		}
		if strings.Count(src, `<div class="cta"`) != 1 {
			return nil, errors.New("more than one .cta row: a person should look")
		}
		src = strings.Replace(src, m, "      {{/* [[params.cta]] in hugo.toml: the kit renders the row */}}\n      {{ partial \"cta.html\" . }}\n", 1)
		conf = strings.TrimRight(conf, "\n") + "\n\n" + ctaToml(ctas)
		for _, c := range ctas {
			if ctaKind(c) == "try" && !strings.Contains(src, "btn-try-style.html") {
				src = strings.Replace(src, `{{ partial "styles.html" . }}`, `{{ partial "styles.html" . }}`+"\n"+`{{ partial "btn-try-style.html" . }}`, 1)
			}
		}
		did = append(did, "hero row from [[params.cta]]")
	}
	// a second Digital Commons band
	if commonsCall.MatchString(src) {
		src = commonsCall.ReplaceAllString(src, "")
		did = append(did, "explicit commons-ribbon.html call dropped")
	}
	// the tagline
	if m := subRe.FindStringSubmatchIndex(src); m != nil {
		inner := src[m[4]:m[5]]
		if w := wrapTagline(inner); w != inner {
			src = src[:m[4]] + w + src[m[5]:]
			did = append(did, "tagline's repeat of the strip hidden")
		}
	}
	// the architectures
	if len(goarch) > 0 && !goarchLineRe.MatchString(conf) {
		var q []string
		for _, a := range goarch {
			q = append(q, fmt.Sprintf("%q", a))
		}
		line := "  goarch = [" + strings.Join(q, ", ") + "]\n"
		if loc := mantraTable.FindStringIndex(conf); loc != nil {
			conf = conf[:loc[1]] + "\n" + strings.TrimRight(line, "\n") + conf[loc[1]:]
		} else {
			conf = strings.TrimRight(conf, "\n") + "\n\n# The architectures the tests RUN on, read from the library's CI.\n[params.mantra]\n" + line
		}
		did = append(did, "goarch "+strings.Join(goarch, ","))
	}
	// every refusal is above; now the writes
	if err := os.WriteFile(filepath.Join(dir, "layouts/index.html"), []byte(src), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "hugo.toml"), []byte(conf), 0o644); err != nil {
		return nil, err
	}
	if _, err := goCmd(dir, "mod", "edit", "-go=1.27.4"); err != nil {
		return nil, err
	}
	if _, err := goCmd(dir, "get", "github.com/go-fleettools/landingkit@"+kit); err != nil {
		return nil, err
	}
	if _, err := goCmd(dir, "mod", "tidy"); err != nil {
		return nil, err
	}
	did = append(did, "landingkit "+kit+", go 1.27.4")
	if !hasKitCheck(dir) {
		p := filepath.Join(dir, ".github/workflows/landingkit-check.yml")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, []byte(checkWorkflow), 0o644); err != nil {
			return nil, err
		}
		did = append(did, "workflow landingkit-check.yml")
	}
	return did, nil
}

// hasKitCheck: a workflow already runs `landingkit check`.
func hasKitCheck(dir string) bool {
	wfs, _ := filepath.Glob(filepath.Join(dir, ".github/workflows/*.yml"))
	for _, w := range wfs {
		if b, err := os.ReadFile(w); err == nil && strings.Contains(string(b), "cmd/landingkit check") {
			return true
		}
	}
	return false
}

func upgradeCmd() *cobra.Command {
	var kit string
	var goarch []string
	c := &cobra.Command{
		Use:   "upgrade DIR",
		Short: "Bring a landing already on the kit to the current recipe (pin, hero row, commons call, tagline, goarch, check workflow)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if kit == "" {
				return errors.New("--kit is required")
			}
			did, err := upgrade(args[0], kit, goarch)
			if err != nil {
				return &failure{fmt.Errorf("not upgraded, a person should look: %v", err)}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "upgraded: %s\n", strings.Join(did, "; "))
			return nil
		},
	}
	c.Flags().StringVar(&kit, "kit", "", "the kit version to require (vX.Y.Z)")
	c.Flags().StringSliceVar(&goarch, "goarch", nil, "the architectures the tests run on, e.g. amd64,arm64")
	return c
}
