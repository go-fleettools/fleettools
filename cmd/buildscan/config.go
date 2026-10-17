package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// config is one build configuration: what GOOS, GOARCH and build tags some
// step of some workflow compiles under.
//
// A configuration is a PAIR plus tags, never three independent sets. A job
// that runs `go test ./...` on ubuntu and then `GOOS=js GOARCH=wasm go build`
// declares two configurations -- linux/amd64 and js/wasm -- and taking the
// union of the GOOS values and the union of the GOARCH values would invent
// linux/wasm, a configuration nobody compiles and the Go toolchain refuses.
// So each step is read on its own and contributes its own pair.
// A configuration also belongs to a PLACE. `go test ./...` at the root of a
// multi-module repository stops at every nested go.mod, so a lane that
// compiles the root for js/wasm says nothing about a submodule -- and the
// first version of this tool reported two such submodules as broken under
// js/wasm when nothing had ever asked them to build there. Two findings out of
// three were invented. So each configuration carries the directory its step
// runs in, and is compared only against the module that directory is in.
type config struct {
	goos, goarch string
	tags         []string   // sorted, from -tags
	cgo          cgoSetting // what the workflow says about CGO_ENABLED, if anything
	dir          string     // the step's working directory, relative; "." at the root
	where        string     // workflow file and job, for the report
}

// key identifies a configuration for deduplication, without its provenance:
// twenty jobs compiling linux/amd64 are one configuration, named once.
func (c config) key() string {
	return c.goos + "/" + c.goarch + " " + strings.Join(c.tags, ",") + " @" + c.dir
}

func (c config) String() string {
	s := c.goos + "/" + c.goarch
	if len(c.tags) > 0 {
		s += " -tags=" + strings.Join(c.tags, ",")
	}
	if c.dir != "" && c.dir != "." {
		s += " in " + c.dir
	}
	return s
}

// unresolved is something a workflow does that cannot be read here: the
// configuration is real but its value is not in the file.
//
// Same rule as testscan and judgescan: what cannot be resolved is reported as
// unresolved, never guessed. A GOOS read from a variable this tool cannot see
// would otherwise be reported as the literal `${{ env.TARGET }}`, and a file
// that only that configuration compiles would be named as compiled by nobody.
type unresolved struct {
	where string
	what  string
}

// hostOS maps a runs-on label to the GOOS the runner has. The label names an
// image, not a target: windows-latest compiles windows/amd64 unless the step
// says otherwise.
var hostOS = map[string]string{
	"ubuntu":  "linux",
	"macos":   "darwin",
	"windows": "windows",
}

// hostArch is the GOARCH of a runner image. GitHub's arm runners carry it in
// the label (ubuntu-24.04-arm, macos-14-arm64); macos-14 and later are arm64
// whether they say so or not, which is why the default is not simply amd64.
func hostArch(label string) (string, bool) {
	l := strings.ToLower(label)
	switch {
	case strings.Contains(l, "arm64"), strings.HasSuffix(l, "-arm"):
		return "arm64", true
	case strings.HasPrefix(l, "macos"):
		// macos-13 is the last amd64 image; macos-14, macos-15 and
		// macos-latest are arm64.
		if strings.HasPrefix(l, "macos-12") || strings.HasPrefix(l, "macos-13") {
			return "amd64", true
		}
		return "arm64", true
	case strings.HasPrefix(l, "ubuntu"), strings.HasPrefix(l, "windows"):
		return "amd64", true
	}
	return "", false
}

// goCmdRE finds a step that compiles: the configuration of a `go test`,
// `go build`, `go vet`, `go install` or `go run` is what decides which files
// exist.
//
// ⛔ `go run` belongs here, and leaving it out cost a false finding on the
// first real run: go-crdt/collab's tools/ module was reported as compiled by
// nobody, and the lane that compiles it says `cd tools && go run ./covergate`.
// It compiles only the package it names rather than ./..., so counting it as
// cover for the module says slightly more than the lane does -- which is the
// direction this tool errs in deliberately, since the alternative was naming a
// module that is built.
//
// `go mod download` and `go generate` are deliberately not here: the first
// compiles nothing, and what the second runs is not in the file.
var goCmdRE = regexp.MustCompile(`\bgo\s+(?:test|build|vet|install|run)\b`)

// assignRE finds a shell assignment on the same command: `GOOS=js go build`,
// and also the `GOOS: js` of an env block.
func assignRE(name string) *regexp.Regexp {
	return regexp.MustCompile(`\b` + name + `\s*[:=]\s*["']?([A-Za-z0-9_.]+)["']?`)
}

// anyAssignRE matches an assignment to ANY value, readable or not.
//
// ⛔ The pair of regexps is the point. A strict one that does not match says
// nothing about whether the variable was set: `GOOS=$TARGET go build ./...`
// gave no value, fell through to the job's env, found nothing there either,
// and was reported as the RUNNER's configuration -- a step that compiles
// something nobody here can name, reported as linux/amd64. Caught by its own
// test before it ever ran on a repository, which is the only reason this
// comment is about a fixture and not about a false report.
func anyAssignRE(name string) *regexp.Regexp {
	return regexp.MustCompile(`\b` + name + `\s*[:=]\s*(\S+)`)
}

var (
	goosRE      = assignRE("GOOS")
	goarchRE    = assignRE("GOARCH")
	goosAnyRE   = anyAssignRE("GOOS")
	goarchAnyRE = anyAssignRE("GOARCH")
	// tagsRE: -tags=a,b or -tags a,b, the second being what gofmt-ed
	// workflows tend to carry.
	tagsRE = regexp.MustCompile(`-tags[= ]["']?([A-Za-z0-9_.,]+)["']?`)
	// exprRE marks a value this tool cannot resolve: a GitHub expression that
	// is not a matrix key already expanded, or a shell variable.
	exprRE = regexp.MustCompile(`\$\{\{|\$[A-Za-z_{(]`)
)

// workflow is as much of a workflow file as this tool reads.
type workflow struct {
	Env  map[string]string `yaml:"env"`
	Jobs map[string]job    `yaml:"jobs"`
}

type job struct {
	Name     string            `yaml:"name"`
	RunsOn   yaml.Node         `yaml:"runs-on"`
	Env      map[string]string `yaml:"env"`
	Defaults struct {
		Run struct {
			WorkingDirectory string `yaml:"working-directory"`
		} `yaml:"run"`
	} `yaml:"defaults"`
	Strategy struct {
		Matrix yaml.Node `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []struct {
		Name             string            `yaml:"name"`
		Run              string            `yaml:"run"`
		Env              map[string]string `yaml:"env"`
		WorkingDirectory string            `yaml:"working-directory"`
	} `yaml:"steps"`
}

// configsOf reads every workflow in dir/.github/workflows and returns the
// configurations they compile under, deduplicated, plus what could not be
// resolved and the number of workflow files seen.
func configsOf(dir string) (cfgs []config, open []unresolved, files int, err error) {
	paths, err := filepath.Glob(filepath.Join(dir, ".github", "workflows", "*.y*ml"))
	if err != nil {
		return nil, nil, 0, err
	}
	seen := map[string]config{}
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, 0, err
		}
		files++
		got, un := configsOfWorkflow(filepath.Base(p), src)
		for _, c := range got {
			if _, ok := seen[c.key()]; !ok {
				seen[c.key()] = c
			}
		}
		open = append(open, un...)
	}
	for _, c := range seen {
		cfgs = append(cfgs, c)
	}
	sort.Slice(cfgs, func(i, j int) bool { return cfgs[i].key() < cfgs[j].key() })
	return cfgs, open, files, nil
}

// configsOfWorkflow reads one workflow's text.
func configsOfWorkflow(name string, src []byte) (cfgs []config, open []unresolved) {
	var wf workflow
	if err := yaml.Unmarshal(src, &wf); err != nil {
		// A workflow this tool cannot parse is reported, not skipped: a file
		// read as empty would make every configuration in it invisible, and
		// "compiled by no lane" is then a statement about the parser.
		return nil, []unresolved{{where: name, what: fmt.Sprintf("could not be parsed: %v", err)}}
	}
	for jobID, j := range wf.Jobs {
		where := name + ": " + jobID
		if j.Name != "" && !strings.Contains(j.Name, "${{") {
			where = name + ": " + j.Name
		}
		for _, m := range expand(j.Strategy.Matrix) {
			got, un := configsOfJob(where, wf.Env, j, m)
			cfgs = append(cfgs, got...)
			open = append(open, un...)
		}
	}
	return cfgs, open
}

// configsOfJob reads one job under one matrix combination.
func configsOfJob(where string, wfEnv map[string]string, j job, m map[string]string) (cfgs []config, open []unresolved) {
	host := substAll(runsOnLabels(j.RunsOn), m)
	// The job's defaults: the workflow's env, then the job's, each substituted.
	base := merge(subst(wfEnv, m), subst(j.Env, m))

	for _, st := range j.Steps {
		run := substText(st.Run, m)
		if !goCmdRE.MatchString(run) {
			continue
		}
		env := merge(base, subst(st.Env, m))
		goos := firstOf(goosRE, run, env["GOOS"])
		goarch := firstOf(goarchRE, run, env["GOARCH"])
		tags := tagsOf(run, env)
		cgo := cgoOf(run, env["CGO_ENABLED"])
		wd := workingDir(substText(st.WorkingDirectory, m), substText(j.Defaults.Run.WorkingDirectory, m), run)

		what := st.Name
		if what == "" {
			what = firstLine(run)
		}
		if exprRE.MatchString(goos) || exprRE.MatchString(goarch) ||
			set(goosAnyRE, run, env["GOOS"]) != (goos != "") ||
			set(goarchAnyRE, run, env["GOARCH"]) != (goarch != "") {
			open = append(open, unresolved{where: where, what: "GOOS/GOARCH comes from a value not in the file: " + what})
			continue
		}
		if goos != "" && goarch == "" {
			// GOOS alone is a real configuration: the arch stays the host's.
			// js is the exception that matters, since js only has wasm.
			if goos == "js" {
				goarch = "wasm"
			}
		}
		switch {
		case goos != "" && goarch != "":
			cfgs = append(cfgs, config{goos: goos, goarch: goarch, tags: tags, cgo: cgo, dir: wd, where: where})
		case goos != "" || goarch != "":
			// One half given, the other the runner's: one configuration per
			// host this job runs on.
			for _, h := range host {
				os_, arch, ok := hostPair(h)
				if !ok {
					open = append(open, unresolved{where: where, what: "cannot tell what a " + h + " runner compiles"})
					continue
				}
				if goos != "" {
					os_ = goos
				}
				if goarch != "" {
					arch = goarch
				}
				cfgs = append(cfgs, config{goos: os_, goarch: arch, tags: tags, cgo: cgo, dir: wd, where: where})
			}
		default:
			for _, h := range host {
				os_, arch, ok := hostPair(h)
				if !ok {
					open = append(open, unresolved{where: where, what: "cannot tell what a " + h + " runner compiles"})
					continue
				}
				cfgs = append(cfgs, config{goos: os_, goarch: arch, tags: tags, cgo: cgo, dir: wd, where: where})
			}
		}
	}
	return cfgs, open
}

// cdRE catches the other way a step changes where it compiles: `cd gitstore &&
// go test ./...`, which no YAML key records.
var cdRE = regexp.MustCompile(`(?m)^\s*cd\s+["']?([^\s"'&;|]+)`)

// workingDir is where a step runs: its own working-directory, the job's
// default, or a `cd` inside the command itself. "." when none of those says.
//
// A path that leaves the checkout (an absolute one, or one starting ..) is
// taken as unknown rather than followed: it is not a module of this
// repository, and treating it as the root would attribute its configuration
// to code it never compiles.
func workingDir(step, jobDefault, run string) string {
	for _, cand := range []string{step, jobDefault} {
		if d := clean(cand); d != "" {
			return d
		}
	}
	if m := cdRE.FindStringSubmatch(run); m != nil {
		if d := clean(m[1]); d != "" {
			return d
		}
	}
	return "."
}

func clean(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p == "." || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") {
		return ""
	}
	if exprRE.MatchString(p) {
		return ""
	}
	p = filepath.Clean(p)
	if p == "." || strings.HasPrefix(p, "..") {
		return ""
	}
	return p
}

// hostPair is the GOOS/GOARCH a runner label compiles by default.
func hostPair(label string) (goos, goarch string, ok bool) {
	l := strings.ToLower(label)
	for prefix, os_ := range hostOS {
		if strings.HasPrefix(l, prefix) {
			arch, ok := hostArch(l)
			return os_, arch, ok
		}
	}
	// A self-hosted label says nothing about its platform. Reported, not
	// guessed: this fleet has arm64 self-hosted runners whose labels do not
	// say so.
	return "", "", false
}

// runsOnLabels reads runs-on, which is a string, a list, or a group object.
func runsOnLabels(n yaml.Node) []string {
	switch n.Kind {
	case yaml.ScalarNode:
		return []string{n.Value}
	case yaml.SequenceNode:
		var out []string
		for _, c := range n.Content {
			if c.Kind == yaml.ScalarNode {
				out = append(out, c.Value)
			}
		}
		return out
	case yaml.MappingNode:
		// { group: …, labels: [...] }
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == "labels" {
				return runsOnLabels(*n.Content[i+1])
			}
		}
	}
	return nil
}

// expand turns a strategy matrix into the combinations it runs, each a map
// from key to value. A job with no matrix yields one empty combination, so
// callers need no special case.
//
// It expands the cartesian product of the list-valued keys and appends each
// `include` entry, which is how a lane like "go 1.27 on windows only" is
// written. `exclude` is NOT applied: an over-broad set names configurations
// that are not compiled, and claiming a file is compiled when it is not is
// the error this tool exists to avoid -- so it errs towards the CI covering
// MORE, and under-reports rather than invents.
func expand(matrix yaml.Node) []map[string]string {
	if matrix.Kind != yaml.MappingNode {
		return []map[string]string{{}}
	}
	combos := []map[string]string{{}}
	var includes []map[string]string
	for i := 0; i+1 < len(matrix.Content); i += 2 {
		key, val := matrix.Content[i].Value, matrix.Content[i+1]
		switch key {
		case "include":
			for _, entry := range val.Content {
				includes = append(includes, flatten(entry))
			}
			continue
		case "exclude", "fail-fast", "max-parallel":
			continue
		}
		var values []string
		switch val.Kind {
		case yaml.ScalarNode:
			values = []string{val.Value}
		case yaml.SequenceNode:
			for _, c := range val.Content {
				if c.Kind == yaml.ScalarNode {
					values = append(values, c.Value)
				} else {
					// A list of objects: each object's fields are the
					// combination, as `include` entries are.
					includes = append(includes, flatten(c))
				}
			}
		}
		if len(values) == 0 {
			continue
		}
		next := make([]map[string]string, 0, len(combos)*len(values))
		for _, c := range combos {
			for _, v := range values {
				m := make(map[string]string, len(c)+1)
				for k, old := range c {
					m[k] = old
				}
				m[key] = v
				next = append(next, m)
			}
		}
		combos = next
		if len(combos) > maxCombos {
			// A matrix this large is a tool problem, not a repository one.
			// Stopping keeps the report honest about what was read.
			combos = combos[:maxCombos]
		}
	}
	// A matrix written as `include:` alone -- which is how a lane like
	// `{os: ubuntu-latest, label: amd64}` is spelt -- has no list keys, so the
	// product above is still the single empty combination. Keeping it would
	// leave one pass with nothing substituted, and `${{ matrix.os }}` would be
	// reported as a runner whose platform cannot be told. It was, for
	// go-crdt/collab's arch-native job, on this tool's first real run.
	if len(includes) > 0 {
		if len(combos) == 1 && len(combos[0]) == 0 {
			combos = includes
		} else {
			combos = append(combos, includes...)
		}
	}
	return combos
}

// maxCombos bounds the expansion. go-pkgx/packages has matrices in the
// hundreds; the product of six keys with five values each is 15 625, and every
// one of them would be scanned against every file.
const maxCombos = 512

// flatten reads a mapping node as a flat map of scalars.
func flatten(n *yaml.Node) map[string]string {
	m := map[string]string{}
	if n == nil || n.Kind != yaml.MappingNode {
		return m
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i+1].Kind == yaml.ScalarNode {
			m[n.Content[i].Value] = n.Content[i+1].Value
		}
	}
	return m
}

// substText replaces ${{ matrix.key }} with the combination's value.
func substText(s string, m map[string]string) string {
	if s == "" || len(m) == 0 {
		return s
	}
	for k, v := range m {
		for _, form := range []string{
			"${{ matrix." + k + " }}",
			"${{matrix." + k + "}}",
			"${{ matrix['" + k + "'] }}",
		} {
			s = strings.ReplaceAll(s, form, v)
		}
	}
	return s
}

func subst(env map[string]string, m map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = substText(v, m)
	}
	return out
}

func substAll(ss []string, m map[string]string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, substText(s, m))
	}
	return out
}

func merge(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// firstOf prefers what the command itself says over the env it inherits: a
// step that writes `GOOS=js go build` in a job whose env sets GOOS=linux
// compiles js.
func firstOf(re *regexp.Regexp, run, fallback string) string {
	if m := re.FindStringSubmatch(run); m != nil {
		return m[1]
	}
	return fallback
}

// cgoRE reads CGO_ENABLED from a command or an env block.
var cgoRE = assignRE("CGO_ENABLED")

// cgoOf is what the step says about cgo: on, off, or NOT SAID.
//
// ⛔ "Not said" is a third answer, and collapsing it cost 42 invented findings
// on tannevaled/purego alone. Assuming cgo ON makes a `//go:build cgo` file
// read as compiled -- the forgiving direction -- and at the same instant makes
// every `!cgo` file read as compiled by nobody, which is the inventing one.
// The polarity flips with the file, so no single assumption is the cautious
// one. When the workflow does not say, a file counts as compiled if EITHER
// setting would compile it: a native lane has cgo on, a cross-compiling one
// has it off, and the workflow that pins it is the only one that settles it.
func cgoOf(run, env string) cgoSetting {
	val := env
	if m := cgoRE.FindStringSubmatch(run); m != nil {
		val = m[1]
	}
	switch val {
	case "1", "true":
		return cgoOn
	case "0", "false":
		return cgoOff
	}
	return cgoEither
}

type cgoSetting int

const (
	cgoEither cgoSetting = iota
	cgoOn
	cgoOff
)

// set says whether the variable was assigned at all, readably or not.
func set(re *regexp.Regexp, run, fallback string) bool {
	return re.MatchString(run) || fallback != ""
}

// instrumentTags are the build tags a FLAG sets rather than -tags.
//
// ⛔ `-race` sets the `race` tag, and missing it reported two repositories'
// `//go:build race` files as compiled by nobody when the lane that compiles
// them is `go test -race ./...`. The others are here for the same reason, not
// because this fleet uses them.
var instrumentTags = map[string]string{
	"-race": "race",
	"-msan": "msan",
	"-asan": "asan",
}

// goexpRE reads GOEXPERIMENT, whose values become `goexperiment.X` tags.
//
// ⛔ Without this the tool cannot see the case it was written for.
// go-compressions/blake3's `blake3_simd_arm64.go` is behind
// `//go:build goexperiment.simd && arm64`: the job that sets the experiment is
// amd64, and the only arm64 workflow says in its own header "no GOEXPERIMENT".
// Each half is covered, their conjunction never, so no compiler had ever seen
// the file -- and it does not build. Found on 2026-10-07 by running
// `GOEXPERIMENT=simd go test ./...` by hand on an arm64 machine.
var goexpRE = assignRE("GOEXPERIMENT")

// tagsOf reads the tags a step compiles with, from the command AND from the
// env it inherits: GOEXPERIMENT and GOFLAGS are routinely set in an `env:`
// block rather than written on the line.
func tagsOf(run string, env map[string]string) []string {
	if exp := env["GOEXPERIMENT"]; exp != "" {
		run += " GOEXPERIMENT=" + exp
	}
	if fl := env["GOFLAGS"]; fl != "" {
		run += " " + fl
	}
	var tags []string
	if m := tagsRE.FindStringSubmatch(run); m != nil {
		tags = append(tags, strings.Split(m[1], ",")...)
	}
	if m := goexpRE.FindStringSubmatch(run); m != nil {
		for _, exp := range strings.Split(m[1], ",") {
			exp = strings.TrimSpace(exp)
			// `none` turns them all off, and a `no` prefix turns one off:
			// neither names a tag that is then set.
			if exp == "" || exp == "none" || strings.HasPrefix(exp, "no") {
				continue
			}
			tags = append(tags, "goexperiment."+exp)
		}
	}
	for flag, tag := range instrumentTags {
		if strings.Contains(run, flag+" ") || strings.HasSuffix(run, flag) {
			tags = append(tags, tag)
		}
	}
	if len(tags) == 0 {
		return nil
	}
	sort.Strings(tags)
	return tags
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 60 {
		s = s[:57] + "…"
	}
	return s
}
