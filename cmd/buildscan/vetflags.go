package main

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
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
