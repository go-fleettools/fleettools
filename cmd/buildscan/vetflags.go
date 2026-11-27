package main

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v4"
)

// vetExemptRE matches a CI line that vets named packages with flags turned off:
//
//	go vet -stdmethods=false ./runtime/wazero
//
// Only `-name=false` flags and only explicit `./dir` packages: that is the shape
// a repository uses to say "this package cannot satisfy this one check", and
// anything else (a pattern, a shell substitution) is left to the plain vet.
var vetExemptRE = regexp.MustCompile(`^go vet((?:\s+-[A-Za-z]+=false)+)((?:\s+\./[A-Za-z0-9_./-]+)+)\s*$`)

// vetExemptions reads the repository's workflows and returns, for each package
// directory the CI vets with checks turned off, those flags. ⛔ buildscan --vet
// vetted such a package with every check on, so a repository whose own CI is green
// read as a vet failure here, nothing was recorded, and the push gate left no way
// forward but BUILDSCAN_SKIP (fleettools#408: go-wasmcontainers/engine, whose
// runtime/wazero Seek returns an Errno because wazero's sys.File says so).
func vetExemptions(dir string) map[string][]string {
	out := map[string][]string{}
	paths, _ := filepath.Glob(filepath.Join(dir, ".github", "workflows", "*.y*ml"))
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for pkg, flags := range exemptionsOf(src) {
			out[pkg] = flags
		}
	}
	return out
}

// exemptionsOf reads one workflow's text. Steps that change directory are
// skipped: their ./paths are not the module root's.
func exemptionsOf(src []byte) map[string][]string {
	var wf workflow
	if yaml.Unmarshal(src, &wf) != nil {
		return nil
	}
	out := map[string][]string{}
	for _, j := range wf.Jobs {
		for _, st := range j.Steps {
			if st.WorkingDirectory != "" || j.Defaults.Run.WorkingDirectory != "" {
				continue
			}
			joined := strings.ReplaceAll(st.Run, "\\\n", " ")
			for _, line := range strings.Split(joined, "\n") {
				m := vetExemptRE.FindStringSubmatch(strings.TrimSpace(line))
				if m == nil {
					continue
				}
				flags := strings.Fields(m[1])
				for _, pkg := range strings.Fields(m[2]) {
					out[path.Clean(pkg)] = flags
				}
			}
		}
	}
	return out
}

// vetListRE matches a CI line that vets the package list minus what a grep drops:
//
//	go vet $(go list -e ./... | grep -vE '/(reference)($|/)|/quake-tamago$')
//
// The regular expression is the CI's own statement of which packages it judges.
var vetListRE = regexp.MustCompile(`^go vet\s+\$\(\s*go list(?:\s+-e)?\s+\./\.\.\.\s*\|\s*grep\s+(?:-vE|-Ev|-v\s+-E|-E\s+-v)\s+(?:'([^']*)'|"([^"]*)")\s*\)\s*$`)

// vetPlainRE matches a CI line that vets the whole tree.
var vetPlainRE = regexp.MustCompile(`^go vet(?:\s+-[A-Za-z]+(?:=\S+)?)*\s+\./\.\.\.\s*$`)

// vetExclusions returns, for each list-form vet line at the root of the CI, the
// pattern it drops. ⛔ buildscan --vet ran `go vet ./...`, so a package the CI
// excludes because it cannot build on a runner (go-quake1/engine's quake-tamago
// needs the TamaGo toolchain) failed here, nothing was recorded, and the push
// gate left no way forward but BUILDSCAN_SKIP. The same class as fleettools#408.
//
// A package counts as excluded only if EVERY vet line drops it (see excluded):
// a line that vets the whole tree means nothing is excluded, so none is returned.
func vetExclusions(dir string) []*regexp.Regexp {
	paths, _ := filepath.Glob(filepath.Join(dir, ".github", "workflows", "*.y*ml"))
	var out []*regexp.Regexp
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		res, plain := exclusionsOf(src)
		if plain {
			return nil
		}
		out = append(out, res...)
	}
	return out
}

// excluded reports whether every vet line of the CI leaves the import path out.
func excluded(res []*regexp.Regexp, importPath string) bool {
	if len(res) == 0 {
		return false
	}
	for _, re := range res {
		if !re.MatchString(importPath) {
			return false
		}
	}
	return true
}

// exclusionsOf reads one workflow: the grep pattern of each list-form vet line,
// and whether a whole-tree vet line exists. Steps that change directory are
// skipped: their packages are not the module root's.
func exclusionsOf(src []byte) (res []*regexp.Regexp, plain bool) {
	var wf workflow
	if yaml.Unmarshal(src, &wf) != nil {
		return nil, false
	}
	for _, j := range wf.Jobs {
		for _, st := range j.Steps {
			if st.WorkingDirectory != "" || j.Defaults.Run.WorkingDirectory != "" {
				continue
			}
			joined := strings.ReplaceAll(st.Run, "\\\n", " ")
			for _, line := range strings.Split(joined, "\n") {
				line = strings.TrimSpace(line)
				if m := vetListRE.FindStringSubmatch(line); m != nil {
					// An unparsable pattern is no statement: treat the line as
					// vetting everything.
					re, err := regexp.Compile(m[1] + m[2])
					if err != nil {
						plain = true
						continue
					}
					res = append(res, re)
				} else if vetPlainRE.MatchString(line) {
					plain = true
				}
			}
		}
	}
	return res, plain
}
