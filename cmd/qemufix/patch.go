package main

import (
	"fmt"
	"strings"
)

// Shape names the arrangement a workflow uses to install a qemu user-mode
// emulator and to run a test binary under it. Detection is a PAIR -- how the
// package is installed, and how the emulator is named at `go test -exec` time
// -- because the two vary independently across the fleet.
type Shape string

const (
	// ShapeOnelineMatrix: `- run: sudo apt-get update && sudo apt-get install
	// -y qemu-user-static`, and a test step naming `${{ matrix.qemu }}`.
	ShapeOnelineMatrix Shape = "oneline+matrix"
	// ShapeRetryMatrix: the bounded/retried `apt=(…)` install loop, and a test
	// step naming `${{ matrix.qemu }}`.
	ShapeRetryMatrix Shape = "retry+matrix"
	// ShapeRetryCase: the retry loop, and a test step whose `case
	// "${{ matrix.goarch }}" in` arms each name a qemu binary literally. This
	// shape was NOT in the brief; it is a third of go-* repositories that have
	// the retry loop and it was found by reading the files rather than trusting
	// the list.
	ShapeRetryCase Shape = "retry+case"
	// ShapeOnelineArch: the one-line install, and a test step that derives
	// `q="qemu-${arch}-static"` itself and skips when `command -v` fails.
	ShapeOnelineArch Shape = "oneline+arch"
	// ShapeFixed is a file that already carries the fix. Reported, not patched.
	ShapeFixed Shape = "already-fixed"
)

// The three invariants every rewrite must satisfy, asserted by every test in
// patch_test.go:
//
//  1. ⚠ the packages installed are `qemu-user qemu-user-binfmt`, never
//     `qemu-user-static`. On Ubuntu 26.04 `qemu-user-static` is a VIRTUAL
//     package and apt refuses to choose between `qemu-user-binfmt-hwe` and
//     `qemu-user-binfmt`:
//
//     E: Package 'qemu-user-static' has no installation candidate
//
//     Installing a provider is NOT the fix either: the providers register
//     binfmt handlers, they do not ship the `qemu-<arch>-static` binaries
//     these lanes invoke. `qemu-user` ships the dynamic `qemu-<arch>`, which
//     is all `go test -exec` needs.
//
//  2. the lookup accepts EITHER binary name, `qemu-<arch>-static` first then
//     `qemu-<arch>`, because the two packages ship different names and a lane
//     pinned to one of them breaks on the other image.
//
//  3. ⛔ a missing emulator FAILS the lane, and the lane PRINTS which emulator
//     it ran (`running <arch> under <binary>`). Without that line, "ran under
//     emulation" and "skipped" are indistinguishable from a green tick -- the
//     one outcome an architecture matrix exists to rule out.
const (
	wantPackages = "qemu-user qemu-user-binfmt"
	wantRanLine  = `echo "running `
	wantFailLine = "::error::no qemu user emulator for "
)

// badPackage is the literal apt package name the fix removes. It is matched in
// `apt-get install` context only: a `matrix.qemu: qemu-riscv64-static` value
// is not this, and neither is the `-static` suffix a resolver strips.
const badPackage = "qemu-user-static"

// installComment is prepended to the install step. It is deliberately short:
// it runs in 333 repositories, and the full account lives in the pull request
// it points at.
var installComment = []string{
	"# ⛔ `qemu-user-static` is a VIRTUAL package on Ubuntu 26.04, which",
	"# `ubuntu-latest` became on 2026-10-19, and apt refuses to choose between its",
	"# two providers:",
	"#",
	"#   E: Package 'qemu-user-static' has no installation candidate",
	"#",
	"# ⚠ Installing a provider is NOT the fix: `qemu-user-binfmt` registers binfmt",
	"# handlers, it does not ship the `qemu-<arch>-static` binaries this lane",
	"# invokes. `qemu-user` ships the dynamic `qemu-<arch>`, which is all that",
	"# `go test -exec` needs, so both are installed and BOTH NAMES are accepted",
	"# below. go-tex/engine#645 has the full account.",
}

// Patch rewrites one workflow file. It returns the new content, the shape it
// recognised, and an error naming what it could not read. A file already
// carrying the fix comes back BYTE-IDENTICAL with ShapeFixed: that is what
// makes a second run over 333 repositories a no-op rather than a churn.
func Patch(name string, src []byte) ([]byte, Shape, error) {
	lines := splitLines(src)

	inst, instFixed, err := findInstall(lines)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", name, err)
	}
	tests, testsFixed, err := findTests(lines)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", name, err)
	}

	switch {
	case instFixed && testsFixed:
		return src, ShapeFixed, nil
	case instFixed != testsFixed:
		// ⛔ Never guess at a half-patched file. One of the two halves carrying
		// the fix means somebody was interrupted, and finishing the job from
		// here would have to assume which half -- exactly the guess this tool
		// refuses to make.
		which := "the install step is fixed but a test step is not"
		if testsFixed {
			which = "a test step is fixed but the install step is not"
		}
		return nil, "", fmt.Errorf("%s: half-patched: %s; fix it by hand", name, which)
	}

	shape := Shape(string(inst.kind) + "+" + string(tests[0].kind))
	// A file with two emulated lanes of DIFFERENT test shapes is not a shape
	// this tool knows; patching the first and guessing the second is how a
	// sweep reports success for a lane it never touched.
	for _, t := range tests[1:] {
		if t.kind != tests[0].kind {
			return nil, "", fmt.Errorf("%s: two test steps of different shapes (%s at line %d, %s at line %d)",
				name, tests[0].kind, tests[0].step.start+1, t.kind, t.step.start+1)
		}
	}

	// Rewrite from the BOTTOM UP so earlier line numbers stay valid.
	edits := []edit{}
	for _, t := range tests {
		repl, err := rewriteTest(lines, t)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", name, err)
		}
		edits = append(edits, edit{t.step.start, t.step.end, repl})
	}
	edits = append(edits, edit{inst.step.start, inst.step.end, rewriteInstall(lines, inst)})
	out := applyEdits(lines, edits)
	return []byte(strings.Join(out, "\n")), shape, nil
}

type edit struct {
	from, to int
	repl     []string
}

func applyEdits(lines []string, edits []edit) []string {
	// Highest `from` first: an edit never moves a line an earlier edit needs.
	for i := 0; i < len(edits); i++ {
		for j := i + 1; j < len(edits); j++ {
			if edits[j].from > edits[i].from {
				edits[i], edits[j] = edits[j], edits[i]
			}
		}
	}
	out := lines
	for _, e := range edits {
		tail := append([]string{}, out[e.to:]...)
		out = append(append(append([]string{}, out[:e.from]...), e.repl...), tail...)
	}
	return out
}

// ---------------------------------------------------------------- the install

type installSite struct {
	step step
	kind Shape // "oneline" or "retry"
	// nameLine is the index of the `- name:` / `- run:` line; comments above
	// it are left in place and the new comment goes directly before it.
	nameLine int
}

func findInstall(lines []string) (installSite, bool, error) {
	var hit []int
	for i, l := range lines {
		if strings.Contains(l, badPackage) && strings.Contains(l, "apt-get install") {
			hit = append(hit, i)
		}
	}
	if len(hit) == 0 {
		// Nothing to install-fix. Is it fixed, or is there no qemu here at all?
		for _, l := range lines {
			if strings.Contains(l, wantPackages) {
				return installSite{}, true, nil
			}
		}
		return installSite{}, false, fmt.Errorf("no apt-get install of %s and no install of %q either: not a shape this tool knows", badPackage, wantPackages)
	}
	st, err := enclosingStep(lines, hit[0])
	if err != nil {
		return installSite{}, false, err
	}
	// Every hit must sit in the SAME step: the retry loop names the package
	// twice. Two separate install steps is a shape to look at by hand.
	for _, h := range hit[1:] {
		if h < st.start || h >= st.end {
			return installSite{}, false, fmt.Errorf("%s is installed in two different steps (lines %d and %d)", badPackage, hit[0]+1, h+1)
		}
	}
	kind := Shape("oneline")
	if containsIn(lines, st, "apt=(") {
		kind = "retry"
	} else if !containsIn(lines, st, "apt-get update") {
		return installSite{}, false, fmt.Errorf("install step at line %d is neither the one-line form nor the retry loop", st.start+1)
	}
	return installSite{step: st, kind: kind, nameLine: st.start}, false, nil
}

func rewriteInstall(lines []string, s installSite) []string {
	ind := strings.Repeat(" ", s.step.indent)
	var out []string
	for _, c := range installComment {
		out = append(out, ind+c)
	}
	if s.kind == "oneline" {
		// The one-line form is replaced wholesale: a named step with a bound,
		// because an apt step that hangs for six hours is read as a test that
		// hangs.
		out = append(out,
			ind+"- name: install qemu user-mode emulators",
			ind+"  timeout-minutes: 10",
			ind+"  run: sudo apt-get update && sudo apt-get install -y "+wantPackages,
		)
		return out
	}
	// The retry loop is KEPT -- it answers a measured apt outage, and that is
	// not this defect. Only the package names and the messages change.
	for i := s.step.start; i < s.step.end; i++ {
		l := lines[i]
		switch {
		case strings.Contains(l, "apt-get install") && strings.Contains(l, badPackage):
			l = strings.Replace(l, badPackage, wantPackages, 1)
		case strings.Contains(l, "::error::"+badPackage+" could not be installed"):
			l = strings.Replace(l, "::error::"+badPackage+" could not be installed",
				"::error::"+wantPackages+" could not be installed", 1)
		case strings.Contains(l, "- name:") && strings.Contains(l, badPackage):
			l = strings.Replace(l, badPackage, "qemu user-mode emulators", 1)
		}
		out = append(out, l)
	}
	return out
}

// ------------------------------------------------------------- the test steps

type testSite struct {
	step step
	kind Shape // "matrix", "case" or "arch"
	run  int   // index of the `run:` key line
}

func findTests(lines []string) ([]testSite, bool, error) {
	var sites []testSite
	seen := map[int]bool{}
	fixed, unfixed := 0, 0
	for i, l := range lines {
		if !strings.Contains(l, "-exec=") {
			continue
		}
		var kind Shape
		switch {
		case strings.Contains(l, "${{ matrix.qemu }}"):
			kind = "matrix"
		case strings.Contains(l, "-exec=qemu-") && strings.Contains(l, "-static"),
			strings.Contains(l, `-exec="$e"`):
			// `-exec="$e"` is what this tool's own case rewrite leaves behind.
			// It is recognised so a SECOND run sees the step and reports it
			// already fixed, instead of reporting the file has no qemu lane.
			kind = "case"
		case strings.Contains(l, `-exec="$q"`):
			kind = "arch"
		default:
			continue // a wasm lane's go_js_wasm_exec, or something else entirely
		}
		st, err := enclosingStep(lines, i)
		if err != nil {
			return nil, false, err
		}
		if seen[st.start] {
			continue
		}
		seen[st.start] = true
		if containsIn(lines, st, wantRanLine) && containsIn(lines, st, wantFailLine) {
			fixed++
			continue
		}
		if kind == "arch" && !containsIn(lines, st, `q="qemu-${arch}-static"`) {
			return nil, false, fmt.Errorf(`test step at line %d uses -exec="$q" but does not derive q the way this tool knows`, st.start+1)
		}
		unfixed++
		run, err := runKeyLine(lines, st)
		if err != nil {
			return nil, false, err
		}
		sites = append(sites, testSite{step: st, kind: kind, run: run})
	}
	if fixed+unfixed == 0 {
		return nil, false, fmt.Errorf("no `go test -exec=` step naming a qemu emulator: not a shape this tool knows")
	}
	if unfixed == 0 {
		return nil, true, nil
	}
	if fixed > 0 {
		return nil, false, fmt.Errorf("%d test step(s) fixed and %d not: fix it by hand", fixed, unfixed)
	}
	return sites, false, nil
}

func rewriteTest(lines []string, t testSite) ([]string, error) {
	st := t.step
	keyInd := indentOf(lines[t.run])
	bodyInd := strings.Repeat(" ", keyInd+2)
	// ⛔ `run:` must be the step's LAST key. A sibling key after it would be
	// swallowed by the block scalar this writes.
	for i := t.run + 1; i < st.end; i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if indentOf(lines[i]) <= keyInd {
			return nil, fmt.Errorf("test step at line %d has a key after `run:` (line %d); rewriting it would swallow that key", st.start+1, i+1)
		}
	}

	head := append([]string{}, lines[st.start:t.run]...)
	switch t.kind {
	case "matrix":
		cmd, err := matrixCommand(lines, t)
		if err != nil {
			return nil, err
		}
		body := []string{
			`base="${{ matrix.qemu }}"`,
			`q=""`,
			`for cand in "$base" "${base%-static}"; do`,
			`  command -v "$cand" >/dev/null 2>&1 && q="$cand" && break`,
			`done`,
			`# ⛔ A missing emulator FAILS this lane. A lane that reports success for`,
			`# an architecture it never ran is the one outcome this matrix exists to`,
			`# rule out.`,
			`if [ -z "$q" ]; then`,
			`  echo "` + wantFailLine + `${{ matrix.goarch }} (looked for $base and ${base%-static})"`,
			`  exit 1`,
			`fi`,
			`# Printed on purpose: without it, "ran under emulation" and "skipped" are`,
			`# indistinguishable from a green tick.`,
			`echo "running ${{ matrix.goarch }} under $q"`,
			cmd,
		}
		return append(head, block(keyInd, bodyInd, body)...), nil

	case "arch":
		cmd, err := archCommand(lines, t)
		if err != nil {
			return nil, err
		}
		body := []string{
			`arch="${{ matrix.goarch }}"`,
			`# Go's GOARCH names mostly match qemu's user binary names; loong64 does not.`,
			`name="$arch"`,
			`[ "$arch" = loong64 ] && name=loongarch64`,
			`q=""`,
			`for cand in "qemu-${name}-static" "qemu-${name}"; do`,
			`  command -v "$cand" >/dev/null 2>&1 && q="$cand" && break`,
			`done`,
			`# ⛔ A missing emulator FAILS the lane. It used to warn and succeed, which`,
			`# is a lane that reports success for an architecture it never ran.`,
			`if [ -z "$q" ]; then`,
			`  echo "` + wantFailLine + `$arch (looked for qemu-${name}-static and qemu-${name})"`,
			`  exit 1`,
			`fi`,
			`echo "running $arch under $q"`,
			cmd,
		}
		return append(head, block(keyInd, bodyInd, body)...), nil

	case "case":
		return rewriteCase(lines, t)
	}
	return nil, fmt.Errorf("unreachable shape %q", t.kind)
}

// block renders `run: |` and an indented body.
func block(keyInd int, bodyInd string, body []string) []string {
	out := []string{strings.Repeat(" ", keyInd) + "run: |"}
	for _, b := range body {
		out = append(out, bodyInd+b)
	}
	return out
}

// matrixCommand finds the single `go test … -exec=${{ matrix.qemu }} …` line
// and returns it with the emulator replaced by the resolved one. Every other
// token -- `CGO_ENABLED=0`, `-timeout 20m`, the package list -- is PRESERVED:
// those were decisions somebody took, and this defect is not about them.
func matrixCommand(lines []string, t testSite) (string, error) {
	cmd, err := soleLine(lines, t, "${{ matrix.qemu }}")
	if err != nil {
		return "", err
	}
	out := strings.Replace(cmd, "-exec=${{ matrix.qemu }}", `-exec="$q"`, 1)
	if out == cmd {
		return "", fmt.Errorf("test step at line %d names matrix.qemu somewhere other than -exec=", t.step.start+1)
	}
	return out, nil
}

func archCommand(lines []string, t testSite) (string, error) {
	return soleLine(lines, t, `-exec="$q"`)
}

// soleLine returns the one command in the step containing marker, with the
// `run:` key and the step's indentation stripped. More than one is a refusal:
// which of them the fix belongs to is a guess.
func soleLine(lines []string, t testSite, marker string) (string, error) {
	var found []string
	for i := t.run; i < t.step.end; i++ {
		if !strings.Contains(lines[i], marker) {
			continue
		}
		s := strings.TrimSpace(lines[i])
		s = strings.TrimPrefix(s, "run:")
		found = append(found, strings.TrimSpace(s))
	}
	if len(found) != 1 {
		return "", fmt.Errorf("test step at line %d has %d commands naming %s; expected exactly one", t.step.start+1, len(found), marker)
	}
	return found[0], nil
}

// qemuForHelper resolves an emulator by its qemu NAME (not the GOARCH: they
// differ for loong64) and fails loudly when neither binary is installed.
var qemuForHelper = []string{
	`# ⛔ Resolve the emulator, accept EITHER binary name, and FAIL when neither`,
	`# is there -- a lane that reports success for an architecture it never ran is`,
	`# the one outcome this matrix exists to rule out. qemu-user ships the dynamic`,
	`# qemu-<arch>; qemu-user-static, now a virtual package, shipped the static one.`,
	`qemu_for() {`,
	`  for cand in "qemu-$1-static" "qemu-$1"; do`,
	`    command -v "$cand" >/dev/null 2>&1 && { printf '%s\n' "$cand"; return 0; }`,
	`  done`,
	`  echo "` + wantFailLine + `$1 (looked for qemu-$1-static and qemu-$1)" >&2`,
	`  return 1`,
	`}`,
}

// rewriteCase patches a `case "${{ matrix.goarch }}" in` dispatch in place:
// the helper goes in before the `case`, and each arm that names a qemu binary
// resolves it, prints it, and keeps everything else it had -- the openweft
// lane's `$(pkgs)` word-splitting among it.
func rewriteCase(lines []string, t testSite) ([]string, error) {
	st := t.step
	caseAt := -1
	for i := t.run; i < st.end; i++ {
		if strings.Contains(lines[i], "case ") && strings.HasSuffix(strings.TrimSpace(lines[i]), " in") {
			caseAt = i
			break
		}
	}
	if caseAt < 0 {
		return nil, fmt.Errorf("test step at line %d names qemu binaries but has no `case … in` to put the resolver in front of", st.start+1)
	}
	caseInd := strings.Repeat(" ", indentOf(lines[caseAt]))

	out := append([]string{}, lines[st.start:caseAt]...)
	for _, h := range qemuForHelper {
		out = append(out, caseInd+h)
	}
	arms := 0
	for i := caseAt; i < st.end; i++ {
		l := lines[i]
		qn, ok := qemuNameIn(l)
		if !ok {
			out = append(out, l)
			continue
		}
		label, ok := armLabel(l)
		if !ok {
			return nil, fmt.Errorf("line %d names %s but is not a case arm this tool can read", i+1, "qemu-"+qn+"-static")
		}
		ind := strings.Repeat(" ", indentOf(l))
		rest := strings.TrimSpace(l)
		rest = strings.TrimPrefix(rest, label+")")
		rest = strings.TrimSpace(rest)
		rest = strings.Replace(rest, "-exec=qemu-"+qn+"-static", `-exec="$e"`, 1)
		out = append(out, ind+label+`) e="$(qemu_for `+qn+`)" || exit 1; echo "running `+label+` under $e"; `+rest)
		arms++
	}
	if arms == 0 {
		return nil, fmt.Errorf("test step at line %d: no case arm named a qemu binary", st.start+1)
	}
	return out, nil
}

// qemuNameIn pulls `riscv64` out of `-exec=qemu-riscv64-static`.
func qemuNameIn(l string) (string, bool) {
	const pre = "-exec=qemu-"
	const post = "-static"
	i := strings.Index(l, pre)
	if i < 0 {
		return "", false
	}
	rest := l[i+len(pre):]
	j := strings.Index(rest, post)
	if j <= 0 {
		return "", false
	}
	return rest[:j], true
}

// armLabel reads `riscv64` out of `  riscv64)  GOARCH=… ;;`. A multi-pattern
// arm (`amd64|arm64)`) is readable too, but such an arm never names a qemu
// binary, so it is never reached.
func armLabel(l string) (string, bool) {
	s := strings.TrimSpace(l)
	i := strings.Index(s, ")")
	if i <= 0 {
		return "", false
	}
	label := s[:i]
	if strings.ContainsAny(label, " \t\"'$") {
		return "", false
	}
	return label, true
}

// ------------------------------------------------------------- line mechanics

type step struct {
	start, end int // [start, end)
	indent     int
}

func splitLines(src []byte) []string { return strings.Split(string(src), "\n") }

func indentOf(l string) int { return len(l) - len(strings.TrimLeft(l, " ")) }

func isStepStart(l string) bool { return strings.HasPrefix(strings.TrimLeft(l, " "), "- ") }

// enclosingStep finds the `- …` list entry that line i belongs to.
//
// It climbs OUTWARD one indentation level at a time: from a shell line inside
// a `run: |` block to the `run:` key, and from there to the `- name:` that
// opens the step. A single backward scan for "the nearest step start" is NOT
// enough -- a `case` arm at column 12 sits under a `run: |` at column 8, which
// is less indented and is not a step start, and the first version of this
// refused every one of those files.
func enclosingStep(lines []string, i int) (step, error) {
	if isStepStart(lines[i]) {
		return stepFrom(lines, i), nil
	}
	for j := i - 1; j >= 0; j-- {
		if strings.TrimSpace(lines[j]) == "" {
			continue
		}
		if indentOf(lines[j]) >= indentOf(lines[i]) {
			continue
		}
		// The first line strictly less indented than i encloses it.
		if isStepStart(lines[j]) {
			st := stepFrom(lines, j)
			if i >= st.start && i < st.end {
				return st, nil
			}
			return step{}, fmt.Errorf("line %d is not inside the step at line %d", i+1, st.start+1)
		}
		// A key, not a step: climb to ITS enclosing step. Each hop strictly
		// decreases the indent, so this terminates.
		return enclosingStep(lines, j)
	}
	return step{}, fmt.Errorf("line %d is not inside a workflow step", i+1)
}

// stepFrom measures the step that opens at line start: it runs to the first
// following non-blank line indented no further than the `- `.
func stepFrom(lines []string, start int) step {
	ind := indentOf(lines[start])
	end := len(lines)
	for j := start + 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "" {
			continue
		}
		if indentOf(lines[j]) <= ind {
			end = j
			break
		}
	}
	// Trailing blank lines belong to whatever follows, not to this step.
	for end > start+1 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return step{start: start, end: end, indent: ind}
}

func containsIn(lines []string, s step, sub string) bool {
	for i := s.start; i < s.end; i++ {
		if strings.Contains(lines[i], sub) {
			return true
		}
	}
	return false
}

// runKeyLine finds the step's `run:` key.
func runKeyLine(lines []string, s step) (int, error) {
	for i := s.start; i < s.end; i++ {
		t := strings.TrimSpace(lines[i])
		t = strings.TrimPrefix(t, "- ")
		if strings.HasPrefix(t, "run:") {
			return i, nil
		}
	}
	return 0, fmt.Errorf("step at line %d has no `run:` key", s.start+1)
}
