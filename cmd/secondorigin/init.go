package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/spf13/cobra"
)

// SkeletonCSP is the policy of the skeleton shell, and the default "csp" of the
// declaration init writes: nothing is allowed but the shell's own script and
// inline style.
const SkeletonCSP = "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'"

// runGhnew creates the Pages repository; a test replaces it.
var runGhnew = func(args []string, out io.Writer) error {
	c := exec.Command("ghnew", args...)
	c.Stdin = nil
	c.Stdout, c.Stderr = out, out
	return c.Run()
}

type initOptions struct {
	org, shell, dir, decl string
	parents               []string
	create, local         bool
	timeout               time.Duration
}

func initCmd() *cobra.Command {
	var o initOptions
	c := cli.New("init --org O-usercontent --parent https://O.github.io --dir DIR [flags]",
		"Prepare the Pages repository of a second origin whose organisation already exists",
		`Writes the shell skeleton for the second origin O-usercontent into DIR: index.html,
shell.html, shell.js (which answers only the declared parents, checking
event.origin both ways and a nonce), a Pages workflow and a README, and the
declaration `+DeclFile+` for the parent repository. With --create it first creates
the repository O-usercontent/O-usercontent.github.io with ghnew.

It NEVER creates the organisation: GitHub asks a human for that. When the
organisation is missing, init prints exactly what to create and exits 1.`)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return cli.Unanswerable("init takes no operand, got %q", args[0])
		}
		return runInit(cli.Out(cmd), o)
	}
	f := c.Flags()
	f.StringVar(&o.org, "org", "", "the usercontent organisation, <org>-usercontent (required)")
	f.StringArrayVar(&o.parents, "parent", nil, "a parent origin the shell may talk to, https://host (repeatable, required)")
	f.StringVar(&o.shell, "shell", "/shell.html", "the shell path")
	f.StringVar(&o.dir, "dir", "", "where the skeleton is written (required; no existing file is overwritten)")
	f.StringVar(&o.decl, "decl", DeclFile, "where the declaration is written for the parent repository; \"\" writes none")
	f.BoolVar(&o.create, "create", false, "create the Pages repository with ghnew first, when it does not exist")
	f.BoolVar(&o.local, "local", false, "allow loopback http parents (tests)")
	f.DurationVar(&o.timeout, "timeout", 20*time.Second, "timeout of one request")
	return c
}

func orgMissing(org string) string {
	return fmt.Sprintf(`The organisation %[1]s does not exist, and this tool does not create
organisations: GitHub requires a person to. What to create:

  1. Open https://github.com/account/organizations/new?plan=free
  2. Organization account name: %[1]s
     Plan: Free. Contact email: yours. This organisation belongs to: "A personal account".
  3. Run this command again. It then creates %[1]s/%[1]s.github.io and
     writes the shell.
`, org)
}

func runInit(out io.Writer, o initOptions) error {
	d := Decl{Org: o.org, Repo: o.org + ".github.io", Parents: o.parents, Shells: []string{o.shell}, CSP: SkeletonCSP}
	if o.dir == "" {
		return cli.Unanswerable("--dir is required")
	}
	if err := d.Validate(o.local); err != nil {
		return cli.Unanswerable("%v", err)
	}
	if sh := d.Shells[0]; strings.Count(sh, "/") != 1 || sh == "/index.html" || sh == "/shell.js" {
		return cli.Unanswerable("--shell %q: one file name at the root, not index.html or shell.js", sh)
	}
	vo := options{timeout: o.timeout}
	r := get(apiBase+"/orgs/"+d.Org, vo, "", false)
	switch {
	case r.err != nil:
		return cli.Unanswerable("cannot tell whether %s exists: %v", d.Org, r.err)
	case r.code == http.StatusNotFound:
		fmt.Fprint(out, orgMissing(d.Org))
		return cli.Found("%s does not exist; nothing was written", d.Org)
	case r.code != http.StatusOK:
		return cli.Unanswerable("cannot tell whether %s exists: the API answered %d", d.Org, r.code)
	}

	// Refuse before any side effect: no file of the skeleton may exist.
	files := skeleton(d)
	for name := range files {
		if _, err := os.Lstat(filepath.Join(o.dir, name)); err == nil {
			return cli.Unanswerable("%s already exists: init does not overwrite", filepath.Join(o.dir, name))
		}
	}

	rr := get(apiBase+"/repos/"+d.Org+"/"+d.Repo, vo, "", false)
	switch {
	case rr.err != nil:
		return cli.Unanswerable("cannot tell whether %s/%s exists: %v", d.Org, d.Repo, rr.err)
	case rr.code == http.StatusNotFound:
		args := []string{"-public", d.Org + "/" + d.Repo, "Second origin for " + strings.Join(d.Parents, ", ")}
		if !o.create {
			fmt.Fprintf(out, "The repository %s/%s does not exist. Create it with:\n\n  ghnew %s %q %q\n\n(or run init again with --create)\n\n",
				d.Org, d.Repo, args[0], args[1], args[2])
		} else if err := runGhnew(args, out); err != nil {
			return cli.Unanswerable("ghnew: %v", err)
		}
	case rr.code != http.StatusOK:
		return cli.Unanswerable("cannot tell whether %s/%s exists: the API answered %d", d.Org, d.Repo, rr.code)
	}

	for name, content := range files {
		p := filepath.Join(o.dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return cli.Wrap(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return cli.Wrap(err)
		}
		fmt.Fprintln(out, "wrote", p)
	}
	if o.decl != "" {
		b, _ := json.MarshalIndent(d, "", "  ")
		switch err := writeNew(o.decl, append(b, '\n')); {
		case err == nil:
			fmt.Fprintln(out, "wrote", o.decl)
		case errors.Is(err, os.ErrExist):
			fmt.Fprintf(out, "%s exists and was left alone: it must say what %s says\n", o.decl, filepath.Join(o.dir, "shell.js"))
		default:
			return cli.Wrap(err)
		}
	}
	fmt.Fprintf(out, `
Next, by hand, because each is a write outside this tool's reach:
  1. In %[1]s/%[2]s, commit these files on a branch and merge them through a pull request.
  2. Settings > Pages > Build and deployment > Source: GitHub Actions.
  3. Add the gate to the parent repository's ci.yml:
       - run: go run github.com/go-fleettools/fleettools/cmd/secondorigin@<tag> verify --retry 3
     It reads %[3]s and fails when the second origin drifts or disappears.
`, d.Org, d.Repo, DeclFile)
	return nil
}

func writeNew(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// skeleton is the repository of the second origin: path -> content.
func skeleton(d Decl) map[string]string {
	allow, _ := json.Marshal(d.Parents)
	r := strings.NewReplacer(
		"@@ALLOW@@", string(allow),
		"@@CSP@@", SkeletonCSP,
		"@@ORG@@", d.Org,
		"@@ORIGIN@@", d.Origin(),
		"@@PARENTS@@", strings.Join(d.Parents, ", "),
		"@@SHELL@@", d.Shells[0],
	)
	return map[string]string{
		"index.html":                         r.Replace(skelIndex),
		strings.TrimPrefix(d.Shells[0], "/"): r.Replace(skelShell),
		"shell.js":                           r.Replace(skelJS),
		"README.md":                          r.Replace(skelReadme),
		".github/workflows/pages.yml":        r.Replace(skelPages),
	}
}

const skelIndex = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="@@CSP@@">
<title>Second origin</title>
</head>
<body><p>This is the second origin of @@PARENTS@@. There is nothing to see here directly.</p></body>
</html>
`

const skelShell = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="@@CSP@@">
<meta name="referrer" content="no-referrer">
<title>Frame</title>
<script src="shell.js" defer></script>
</head>
<body></body>
</html>
`

const skelJS = `// The shell of the second origin @@ORIGIN@@.
//
// It talks to exactly these origins, written in at init time and checked by
// ` + "`secondorigin verify`" + ` against the parent's ` + DeclFile + `, and runs that
// check in a browser. A message is believed only when its origin is one of them,
// its source is the framing window, and it carries the nonce of this page's URL
// fragment, which only the framing page knows. The frame says nothing until the
// parent has said {t:"hello", nonce}: it does not ask the browser who framed it
// (location.ancestorOrigins is missing in Firefox, and the referrer is not sent),
// it is TOLD, and believes the message only by those three tests. Every reply
// goes to the origin that said hello. Commands are added to HANDLERS; nothing
// else answers.
(function () {
  "use strict";
  const ALLOW = @@ALLOW@@;
  const HANDLERS = {
    ping: function () { return { pong: true }; },
  };

  const nonce = location.hash.slice(1);
  history.replaceState(null, "", location.pathname + location.search);

  function refuse(why) {
    document.body.textContent = "Refused: " + why;
  }
  if (window.parent === window) { return refuse("not framed"); }
  if (!/^[A-Za-z0-9_-]{16,}$/.test(nonce)) { return refuse("no nonce"); }
  let from = ""; // the parent's origin, known once it has said hello

  window.addEventListener("message", function (e) {
    if (ALLOW.indexOf(e.origin) < 0 || e.source !== window.parent) { return; }
    const m = e.data;
    if (!m || m.nonce !== nonce || typeof m.t !== "string") { return; }
    if (m.t === "hello") {
      from = e.origin;
      e.source.postMessage({ t: "ready" }, from);
      return;
    }
    if (from !== e.origin || typeof m.id !== "string") { return; }
    const h = Object.prototype.hasOwnProperty.call(HANDLERS, m.t) ? HANDLERS[m.t] : null;
    if (!h) { return e.source.postMessage({ t: "error", id: m.id, error: "unknown command" }, from); }
    Promise.resolve().then(function () { return h(m); }).then(
      function (r) { e.source.postMessage({ t: m.t, id: m.id, result: r }, from); },
      function (err) { e.source.postMessage({ t: "error", id: m.id, error: String(err && err.message || err) }, from); }
    );
  });
})();
`

const skelReadme = `# @@ORG@@.github.io

The second origin of @@PARENTS@@: a site of its own (github.io is on the
Public Suffix List), so that what runs here cannot read the parent's storage.

- ` + "`@@SHELL@@`" + ` and ` + "`shell.js`" + `: the shell. It answers only @@PARENTS@@.
- The parent repository declares this origin in ` + "`" + DeclFile + "`" + ` and its CI runs
  ` + "`secondorigin verify`" + `, which fails when this site drifts or disappears.
`

const skelPages = `name: pages

on:
  push:
    branches: [main]
  pull_request:
  workflow_dispatch:

permissions: {}

jobs:
  build:
    runs-on: ubuntu-latest
    permissions:
      contents: read
    steps:
      - uses: actions/checkout@v7
      - name: stage the site
        run: |
          mkdir "$RUNNER_TEMP/_site"
          cp index.html shell.js ".@@SHELL@@" "$RUNNER_TEMP/_site/"
      - if: github.event_name != 'pull_request'
        uses: actions/upload-pages-artifact@v5
        with: { path: "${{ runner.temp }}/_site" }

  deploy:
    if: github.event_name != 'pull_request' && github.ref == 'refs/heads/main'
    needs: build
    runs-on: ubuntu-latest
    concurrency:
      group: pages
      cancel-in-progress: false
    permissions:
      pages: write
      id-token: write
    environment:
      name: github-pages
      url: ${{ steps.deploy.outputs.page_url }}
    steps:
      - id: deploy
        uses: actions/deploy-pages@v5
`
