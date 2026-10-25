package main

import (
	"fmt"
	"strings"
	"testing"
)

// ---------------------------------------------------------------- invariants
//
// Three things make this fix a fix rather than a green tick, and EVERY shape's
// test asserts all three. They are checked by reading the rewritten file,
// because the question is not "did the tool run" but "does the lane now test
// the architecture it claims to".

// invInstallsRealPackages: the apt line names qemu-user and qemu-user-binfmt,
// and no apt-get install line names the virtual qemu-user-static any more.
func invInstallsRealPackages(out string) error {
	if !strings.Contains(out, wantPackages) {
		return errf("no apt-get install names %q", wantPackages)
	}
	for i, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "apt-get install") && strings.Contains(l, badPackage) {
			return errf("line %d still installs the virtual package %q: %s", i+1, badPackage, strings.TrimSpace(l))
		}
	}
	return nil
}

// invAcceptsEitherBinaryName: the resolver tries TWO candidates, one with the
// -static suffix and one without, and tests each with command -v. The two
// packages ship different binary names; a lane pinned to one breaks on the
// other image.
func invAcceptsEitherBinaryName(out string) error {
	if !strings.Contains(out, `command -v "$cand"`) {
		return errf(`no command -v "$cand" lookup`)
	}
	for _, l := range strings.Split(out, "\n") {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "for cand in ") {
			continue
		}
		t = strings.TrimPrefix(t, "for cand in ")
		t = strings.TrimSuffix(t, "; do")
		fields := strings.Fields(t)
		if len(fields) != 2 {
			return errf("resolver tries %d candidate(s), not 2: %s", len(fields), t)
		}
		a := strings.Contains(fields[0], "-static")
		b := strings.Contains(fields[1], "-static")
		if a == b {
			return errf("the two candidates are not one -static and one dynamic: %s", t)
		}
		return nil
	}
	return errf("no `for cand in` resolver loop at all")
}

// invFailsAndPrints: a missing emulator FAILS the lane, and the lane prints
// which emulator it ran. Without the print, "ran under emulation" and
// "skipped" are indistinguishable from a green tick.
func invFailsAndPrints(out string) error {
	if !strings.Contains(out, wantFailLine) {
		return errf("no %q error for a missing emulator", wantFailLine)
	}
	if !strings.Contains(out, wantRanLine) {
		return errf("the lane never prints which emulator it ran (%q)", wantRanLine)
	}
	if strings.Contains(out, "skipping") {
		return errf("a skip branch survives: a skipped architecture must not be a green tick")
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, wantFailLine) {
			return nil
		}
	}
	return nil
}

func errf(format string, a ...any) error { return fmt.Errorf(format, a...) }

func assertInvariants(t *testing.T, out string) {
	t.Helper()
	for name, inv := range map[string]func(string) error{
		"packages":           invInstallsRealPackages,
		"either-binary-name": invAcceptsEitherBinaryName,
		"fail-and-print":     invFailsAndPrints,
	} {
		if err := inv(out); err != nil {
			t.Errorf("invariant %s: %v", name, err)
		}
	}
	if t.Failed() {
		t.Logf("rewritten file:\n%s", out)
	}
}

// ⛔ TestTheInvariantCheckersCanSeeAFailure is the control. Three checkers that
// pass on everything would make every shape's test pass while proving nothing,
// which is the exact failure mode this whole sweep is about. They are run on
// the UNFIXED input, where all three must fail.
func TestTheInvariantCheckersCanSeeAFailure(t *testing.T) {
	for shape, src := range map[string]string{
		"oneline+matrix":     srcOnelineMatrix,
		"retry+matrix":       srcRetryMatrix,
		"retry+case":         srcRetryCase,
		"oneline+arch":       srcOnelineArch,
		"oneline+matrixflag": srcOnelineMatrixFlag,
		"oneline+qemuvar":    srcOnelineQemuVar,
		"guard+pkgxenv":      srcGuardPkgxEnv,
	} {
		if err := invFailsAndPrints(src); err == nil {
			t.Errorf("%s: the fail-and-print checker passes on the UNFIXED file", shape)
		}
		if err := invInstallsRealPackages(src); err == nil {
			t.Errorf("%s: the package checker passes on the UNFIXED file", shape)
		}
		if err := invAcceptsEitherBinaryName(src); err == nil {
			t.Errorf("%s: the either-name checker passes on the UNFIXED file", shape)
		}
	}
	// The unfixed arch shape is the one that has a skip branch AND no print.
	if err := invFailsAndPrints(srcOnelineArch); err == nil {
		t.Error("the fail-and-print checker passes on a lane that skips silently")
	}
	// And one planted defect per checker, so each is pinned on its own:
	if err := invAcceptsEitherBinaryName(`for cand in "qemu-$1-static"; do` + "\n" + `command -v "$cand"`); err == nil {
		t.Error("a one-candidate resolver must fail the either-name checker")
	}
}

// ---------------------------------------------------------------- the shapes

func TestOnelineMatrix(t *testing.T) {
	out, shape, err := Patch("ci.yml", []byte(srcOnelineMatrix))
	if err != nil {
		t.Fatal(err)
	}
	if shape != ShapeOnelineMatrix {
		t.Fatalf("shape = %q, want %q", shape, ShapeOnelineMatrix)
	}
	got := string(out)
	assertInvariants(t, got)
	// ⚠ The lane's own decisions are PRESERVED: this defect is not about them,
	// and a sweep that quietly dropped a timeout or CGO_ENABLED would be
	// changing what the lane tests.
	for _, keep := range []string{"-timeout 20m", "CGO_ENABLED=0", "./..."} {
		if !strings.Contains(got, keep) {
			t.Errorf("the rewrite dropped %q from the test command", keep)
		}
	}
	if !strings.Contains(got, `-exec="$q"`) {
		t.Error("the test command does not use the resolved emulator")
	}
	assertIdempotent(t, got, "ci.yml")
}

func TestRetryMatrix(t *testing.T) {
	out, shape, err := Patch("ci.yml", []byte(srcRetryMatrix))
	if err != nil {
		t.Fatal(err)
	}
	if shape != ShapeRetryMatrix {
		t.Fatalf("shape = %q, want %q", shape, ShapeRetryMatrix)
	}
	got := string(out)
	assertInvariants(t, got)
	// The retry loop answers a MEASURED apt outage -- an hour of silence from
	// apt-get update -- and that is a different defect. It must survive.
	for _, keep := range []string{"apt=(", "Acquire::Retries=3", "dpkg --configure -a", "for attempt in 1 2", "timeout 240"} {
		if !strings.Contains(got, keep) {
			t.Errorf("the rewrite destroyed the retry loop: %q is gone", keep)
		}
	}
	if strings.Count(got, wantPackages) < 2 {
		t.Error("the retry loop installs the package twice; both occurrences must be rewritten")
	}
	assertIdempotent(t, got, "ci.yml")
}

func TestRetryCase(t *testing.T) {
	out, shape, err := Patch("ci.yml", []byte(srcRetryCase))
	if err != nil {
		t.Fatal(err)
	}
	if shape != ShapeRetryCase {
		t.Fatalf("shape = %q, want %q", shape, ShapeRetryCase)
	}
	got := string(out)
	assertInvariants(t, got)
	// Each emulated arm resolves, prints and runs; the native arm is untouched.
	for _, arm := range []string{"riscv64", "loong64", "ppc64le", "s390x"} {
		if !strings.Contains(got, `echo "running `+arm+` under $e"`) {
			t.Errorf("arm %s never prints which emulator it ran", arm)
		}
	}
	// ⚠ loong64's GOARCH and its qemu binary name DIFFER. The arm must ask for
	// loongarch64 while printing loong64, or the lane fails to find an
	// emulator that is installed.
	if !strings.Contains(got, "e=\"$(qemu_for loongarch64)\"") {
		t.Error("loong64 must resolve qemu-loongarch64, not qemu-loong64")
	}
	if !strings.Contains(got, "amd64|arm64) go test ./... ;;") {
		t.Error("the native arm must be left alone")
	}
	// The openweft lane's package filter is a function call whose word
	// splitting matters; it is carried through verbatim.
	if !strings.Contains(got, "$(pkgs)") {
		t.Error("the rewrite dropped the lane's package filter")
	}
	if strings.Contains(got, "-exec=qemu-") {
		t.Error("a literal emulator name survives in -exec=")
	}
	assertIdempotent(t, got, "ci.yml")
}

func TestOnelineArch(t *testing.T) {
	out, shape, err := Patch("ci.yml", []byte(srcOnelineArch))
	if err != nil {
		t.Fatal(err)
	}
	if shape != ShapeOnelineArch {
		t.Fatalf("shape = %q, want %q", shape, ShapeOnelineArch)
	}
	got := string(out)
	assertInvariants(t, got)
	if !strings.Contains(got, "[ \"$arch\" = loong64 ] && name=loongarch64") {
		t.Error("loong64's different qemu name must still be handled")
	}
	// ⛔ The skip-with-success branch must be GONE, not reworded. It is matched
	// on a line of its own: "exit 0" also occurs in prose about it, and a
	// substring search over the whole file reported this fix as broken when it
	// was my own explanatory comment it had found.
	for i, l := range strings.Split(got, "\n") {
		if strings.TrimSpace(l) == "exit 0" {
			t.Errorf("line %d still exits 0 without running anything", i+1)
		}
	}
	if strings.Contains(got, "::warning::") {
		t.Error("the warn-and-continue branch must be gone")
	}
	assertIdempotent(t, got, "ci.yml")
}

func TestOnelineMatrixFlag(t *testing.T) {
	out, shape, err := Patch("ci.yml", []byte(srcOnelineMatrixFlag))
	if err != nil {
		t.Fatal(err)
	}
	if shape != ShapeOnelineMatrixFlag {
		t.Fatalf("shape = %q, want %q", shape, ShapeOnelineMatrixFlag)
	}
	got := string(out)
	assertInvariants(t, got)
	// ⛔ THE TRAP IN THIS SHAPE. The same job carries a NATIVE lane: 386 has
	// `exec: ""` on purpose, because a 386 ELF executes directly on the x86-64
	// runner and qemu-i386 was measured to flake. A rewrite that treats an
	// empty exec as "no emulator found" fails a lane that is correct, so the
	// rewrite must branch on the empty string -- and this is the assertion that
	// says so.
	if !strings.Contains(got, `if [ -n "$flag" ]; then`) {
		t.Error("the rewrite does not branch on an EMPTY matrix.exec: the native 386 lane would fail for having no emulator")
	}
	if !strings.Contains(got, `echo "running ${{ matrix.goarch }} natively"`) {
		t.Error("the native lane never says it ran natively")
	}
	// ⚠ $ex must be UNQUOTED in the command: quoted, an empty value becomes an
	// empty argument and `go test ""` is not `go test`.
	if !strings.Contains(got, "go test $ex ./...") {
		t.Error(`the command must pass $ex unquoted so it vanishes on a native lane`)
	}
	// The matrix is left exactly as written: the -static name there is the
	// FIRST candidate, not a bug.
	if !strings.Contains(got, `- { goarch: "386",   exec: "", via: native }`) {
		t.Error("the matrix was rewritten; it is the input to the resolver, not the defect")
	}
	assertIdempotent(t, got, "ci.yml")
}

func TestOnelineQemuVar(t *testing.T) {
	out, shape, err := Patch("ci.yml", []byte(srcOnelineQemuVar))
	if err != nil {
		t.Fatal(err)
	}
	if shape != ShapeOnelineQemuVar {
		t.Fatalf("shape = %q, want %q", shape, ShapeOnelineQemuVar)
	}
	got := string(out)
	assertInvariants(t, got)
	// ⛔ The install step is guarded by `if: matrix.qemu != ''` so the native
	// lane does not install an emulator it will not use. The one-line install
	// rewrite replaces the step WHOLESALE, so the `if:` has to be carried over
	// explicitly -- dropping it would change what the lane does under cover of
	// fixing something else.
	if !strings.Contains(got, "if: matrix.qemu != ''") {
		t.Error("the install step's `if:` was dropped by the wholesale rewrite")
	}
	// The emulated branch resolves and runs the resolved name...
	if !strings.Contains(got, `"$q" ./appicon.test -test.count=1 -test.v`) {
		t.Error("the test binary is not run under the RESOLVED emulator")
	}
	if strings.Contains(got, `"$QEMU" ./appicon.test`) {
		t.Error("the unresolved $QEMU invocation survives")
	}
	// ...and everything else in the step is left alone: QEMU_CPU must be
	// exported ONLY when it has a value (qemu-user refuses an empty -cpu), and
	// the native branch is correct as it stands.
	for _, keep := range []string{
		`if [ -n "$QEMU_CPU_WANTED" ]; then`,
		`export QEMU_CPU="$QEMU_CPU_WANTED"`,
		`if [ -z "$QEMU" ]; then`,
		`./appicon.test -test.count=1 -test.v`,
	} {
		if !strings.Contains(got, keep) {
			t.Errorf("the rewrite dropped %q", keep)
		}
	}
	assertIdempotent(t, got, "ci.yml")
}

func TestGuardPkgxEnv(t *testing.T) {
	out, shape, err := Patch("ci.yml", []byte(srcGuardPkgxEnv))
	if err != nil {
		t.Fatal(err)
	}
	if shape != ShapeGuardPkgxEnv {
		t.Fatalf("shape = %q, want %q", shape, ShapeGuardPkgxEnv)
	}
	got := string(out)
	assertInvariants(t, got)
	// The emulator is resolved ONCE, in the step whose job is to put it there,
	// and crosses to the test step through $GITHUB_ENV. Two resolvers in two
	// steps would be two chances to disagree.
	if !strings.Contains(got, `echo "QEMU_EXEC=$q" >> "$GITHUB_ENV"`) {
		t.Error("the resolved emulator never reaches the test step")
	}
	if !strings.Contains(got, `-exec "$QEMU_EXEC" ./...`) {
		t.Error("the test command does not use the resolved emulator")
	}
	if strings.Contains(got, "-exec ${{ matrix.qemu }}") {
		t.Error("the test command still names the matrix emulator directly")
	}
	// ⛔ The old guard was a brace group spanning two lines. Half of it left
	// behind is a syntax error, and a syntax error in a `run:` block is a red
	// lane that looks like this fix failing.
	if strings.Contains(got, "is not in this image") {
		t.Error("the old guard's message survives")
	}
	for _, l := range strings.Split(got, "\n") {
		if strings.Contains(l, "exit 1; }") {
			t.Errorf("the old guard's closing brace survives: %s", strings.TrimSpace(l))
		}
	}
	// The lane's own decisions survive: -count=1 is there so a cached PASS from
	// the host architecture cannot stand in for a run that never happened.
	for _, keep := range []string{"-count=1", "CGO_ENABLED:", "apt-get update -qq"} {
		if !strings.Contains(got, keep) {
			t.Errorf("the rewrite dropped %q", keep)
		}
	}
	assertIdempotent(t, got, "ci.yml")
}

// ⛔ TestRefusesAMultiLineInstallItCannotReplace pins the reason the guard shape
// needed its own install kind. A multi-line `run:` is not the one-line form,
// and the one-line rewrite replaces the step wholesale -- it would have thrown
// the rest of the script away silently.
func TestRefusesAMultiLineInstallItCannotReplace(t *testing.T) {
	src := `jobs:
  arch:
    steps:
      - name: install
        run: |
          sudo apt-get update -qq
          sudo apt-get install -y -qq qemu-user-static
          ./scripts/something-else-that-matters.sh
      - name: Test under qemu
        run: GOARCH=${{ matrix.goarch }} go test -exec=${{ matrix.qemu }} ./...
`
	_, _, err := Patch("badshape.yml", []byte(src))
	if err == nil {
		t.Fatal("accepted a multi-line install the one-line rewrite would have destroyed")
	}
	if !strings.Contains(err.Error(), "badshape.yml") {
		t.Errorf("the refusal does not name the file: %v", err)
	}
}

// ------------------------------------------------- the planted positive

// ⛔ TestAlreadyFixedFileComesBackUntouched is the planted positive, and it is
// the test that makes a 333-repository sweep re-runnable. A file already
// carrying the fix must come back BYTE-IDENTICAL and be REPORTED as
// already-fixed -- not silently "patched", which would mean the tool cannot
// tell a fixed file from a broken one and every second run churns 333 commits.
//
// The fixtures are this tool's OWN OUTPUT for each shape, so the test cannot
// drift from what the tool writes.
func TestAlreadyFixedFileComesBackUntouched(t *testing.T) {
	for shape, src := range map[string]string{
		"oneline+matrix":     srcOnelineMatrix,
		"retry+matrix":       srcRetryMatrix,
		"retry+case":         srcRetryCase,
		"oneline+arch":       srcOnelineArch,
		"oneline+matrixflag": srcOnelineMatrixFlag,
		"oneline+qemuvar":    srcOnelineQemuVar,
		"guard+pkgxenv":      srcGuardPkgxEnv,
	} {
		fixed, _, err := Patch("ci.yml", []byte(src))
		if err != nil {
			t.Fatalf("%s: %v", shape, err)
		}
		again, got, err := Patch("ci.yml", fixed)
		if err != nil {
			t.Errorf("%s: a fixed file was refused: %v", shape, err)
			continue
		}
		if got != ShapeFixed {
			t.Errorf("%s: a fixed file was reported %q, want %q -- the tool cannot tell fixed from broken",
				shape, got, ShapeFixed)
		}
		if string(again) != string(fixed) {
			t.Errorf("%s: a second run CHANGED the file; it is not idempotent", shape)
		}
	}
}

func assertIdempotent(t *testing.T, fixed, name string) {
	t.Helper()
	again, shape, err := Patch(name, []byte(fixed))
	if err != nil {
		t.Fatalf("second run refused the file it just wrote: %v", err)
	}
	if shape != ShapeFixed {
		t.Errorf("second run reported %q, want %q", shape, ShapeFixed)
	}
	if string(again) != fixed {
		t.Error("second run changed the file")
	}
}

// --------------------------------------------------------------- the refusals

func TestRefusesWhatItCannotRead(t *testing.T) {
	for name, src := range map[string]string{
		// ⛔ Half-patched: one half carrying the fix means somebody was
		// interrupted, and finishing from here would have to assume which half.
		"half-patched install only": `jobs:
  arch:
    steps:
      - run: sudo apt-get update && sudo apt-get install -y qemu-user qemu-user-binfmt
      - name: Test under qemu
        run: GOARCH=${{ matrix.goarch }} go test -exec=${{ matrix.qemu }} ./...
`,
		// No qemu lane at all: a file this tool has no business editing.
		"no qemu lane": `jobs:
  test:
    steps:
      - run: go test ./...
`,
		// An install shape that is neither the one-line form nor the loop.
		"unknown install": `jobs:
  arch:
    steps:
      - run: nix-env -iA nixpkgs.qemu-user-static && sudo apt-get install qemu-user-static
      - name: Test under qemu
        run: GOARCH=${{ matrix.goarch }} go test -exec=${{ matrix.qemu }} ./...
`,
		// ⛔ A key after `run:` would be swallowed by the block scalar the
		// rewrite writes, so the rewrite refuses instead.
		"key after run": `jobs:
  arch:
    steps:
      - run: sudo apt-get update && sudo apt-get install -y qemu-user-static
      - name: Test under qemu
        run: GOARCH=${{ matrix.goarch }} go test -exec=${{ matrix.qemu }} ./...
        env:
          CGO_ENABLED: "0"
`,
		// Two install steps: which one the lane uses is a guess.
		"two install steps": `jobs:
  a:
    steps:
      - run: sudo apt-get install -y qemu-user-static
      - name: Test under qemu
        run: GOARCH=${{ matrix.goarch }} go test -exec=${{ matrix.qemu }} ./...
  b:
    steps:
      - run: sudo apt-get install -y qemu-user-static
`,
	} {
		_, _, err := Patch("badshape.yml", []byte(src))
		if err == nil {
			t.Errorf("%s: accepted a shape it should have refused", name)
			continue
		}
		// A refusal in a 333-file sweep is only useful if it names the file.
		if !strings.Contains(err.Error(), "badshape.yml") {
			t.Errorf("%s: the refusal does not name the file: %v", name, err)
		}
	}
}

// A wasm lane's -exec= must not be mistaken for a qemu lane.
func TestLeavesAWasmExecAlone(t *testing.T) {
	src := `jobs:
  arch:
    steps:
      - run: sudo apt-get update && sudo apt-get install -y qemu-user-static
      - name: Test under qemu
        run: GOARCH=${{ matrix.goarch }} go test -exec=${{ matrix.qemu }} ./...
  wasm:
    steps:
      - name: js/wasm
        run: GOOS=js GOARCH=wasm go test -exec="$(go env GOROOT)/lib/wasm/go_js_wasm_exec" ./...
`
	out, shape, err := Patch("ci.yml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if shape != ShapeOnelineMatrix {
		t.Fatalf("shape = %q", shape)
	}
	if !strings.Contains(string(out), `go_js_wasm_exec" ./...`) {
		t.Error("the wasm lane was rewritten; it has nothing to do with this defect")
	}
}

// ------------------------------------------------------------------- fixtures
//
// Each fixture is the real pre-fix text of a repository in the fleet, so what
// the tests exercise is what the sweep will meet.

// go-ruby-capybara/capybara and go-ruby-bbolt/bbolt: the one-line install and
// a matrix-named emulator. 300-odd repositories have this shape.
const srcOnelineMatrix = `name: ci
jobs:
  arch-qemu:
    name: arch ${{ matrix.goarch }} (qemu)
    runs-on: ubuntu-latest
    strategy:
      matrix:
        include:
          - { goarch: riscv64, qemu: qemu-riscv64-static }
          - { goarch: loong64, qemu: qemu-loongarch64-static }
          - { goarch: ppc64le, qemu: qemu-ppc64le-static }
          - { goarch: s390x,   qemu: qemu-s390x-static }
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version: stable
      - run: sudo apt-get update && sudo apt-get install -y qemu-user-static
      - name: Test under qemu (${{ matrix.goarch }})
        run: CGO_ENABLED=0 GOARCH=${{ matrix.goarch }} go test -timeout 20m -exec=${{ matrix.qemu }} ./...
`

// go-crdt/crdt and go-crdt/collab: the bounded, retried install -- written
// after apt sat on this step for the better part of an hour -- with the same
// matrix-named emulator.
const srcRetryMatrix = `name: ci
jobs:
  arch-qemu:
    strategy:
      matrix:
        include:
          - { goarch: riscv64, qemu: qemu-riscv64-static }
          - { goarch: loong64, qemu: qemu-loongarch64-static }
    steps:
      - uses: actions/checkout@v7
      # apt has sat on this step for the better part of an hour, more than once,
      # on nothing this repository does. It is bounded and retried rather than
      # waited on.
      - name: Install qemu-user-static
        run: |
          apt=(-y -qq -o Acquire::Retries=3 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30)
          if timeout 240 sudo apt-get install "${apt[@]}" qemu-user-static; then
            exit 0
          fi
          for attempt in 1 2; do
            echo "::warning::the runner's package lists were not enough; refreshing them (attempt $attempt)"
            timeout 240 sudo apt-get update "${apt[@]}" || true
            sudo rm -f /var/lib/apt/lists/lock /var/cache/apt/archives/lock /var/lib/dpkg/lock*
            sudo dpkg --configure -a || true
            if timeout 240 sudo apt-get install "${apt[@]}" qemu-user-static; then
              exit 0
            fi
          done
          echo "::error::qemu-user-static could not be installed"
          exit 1
      - name: Test under qemu (${{ matrix.goarch }})
        run: GOARCH=${{ matrix.goarch }} go test -exec=${{ matrix.qemu }} ./...
`

// openweft/weft-microvm-agent, go-macos/appbundle, go-deltasync/chunk: the
// retry loop, and a case dispatch that names each emulator LITERALLY. ⚠ This
// shape was not in the brief for the sweep; it was found by reading the files.
const srcRetryCase = `name: ci
jobs:
  arch:
    strategy:
      matrix:
        goarch: [amd64, arm64, riscv64, loong64, ppc64le, s390x]
    steps:
      - uses: actions/checkout@v7
      - name: Install qemu-user-static
        if: matrix.goarch != 'amd64' && matrix.goarch != 'arm64'
        run: |
          apt=(-y -qq -o Acquire::Retries=3)
          if timeout 240 sudo apt-get install "${apt[@]}" qemu-user-static; then
            exit 0
          fi
          for attempt in 1 2; do
            timeout 240 sudo apt-get update "${apt[@]}" || true
            sudo dpkg --configure -a || true
            if timeout 240 sudo apt-get install "${apt[@]}" qemu-user-static; then
              exit 0
            fi
          done
          echo "::error::qemu-user-static could not be installed"
          exit 1
      - name: Test
        run: |
          SKIP='github.com/openweft/weft-microvm-agent/pkg/firewallstatus'
          pkgs() { go list ./... | grep -v "^${SKIP}$"; }
          case "${{ matrix.goarch }}" in
            amd64|arm64) go test ./... ;;
            riscv64)  GOARCH=riscv64 go test -exec=qemu-riscv64-static $(pkgs) ;;
            loong64)  GOARCH=loong64 go test -exec=qemu-loongarch64-static $(pkgs) ;;
            ppc64le)  GOARCH=ppc64le go test -exec=qemu-ppc64le-static $(pkgs) ;;
            s390x)    GOARCH=s390x   go test -exec=qemu-s390x-static $(pkgs) ;;
          esac
`

// go-images/{gif,jpeg,png}, go-ooxml/*, go-opentype/*, go-pdfkit/*: the matrix
// carries the WHOLE flag, and one of its lanes carries the EMPTY string --
// which is the trap. Reported fleet-wide as "step at line 75 has no `run:`
// key", because line 75 is a matrix datum and the tool read it as a command.
const srcOnelineMatrixFlag = `name: ci
jobs:
  arch-qemu:
    name: arch ${{ matrix.goarch }} (${{ matrix.via }})
    runs-on: ubuntu-latest
    strategy:
      fail-fast: false
      matrix:
        include:
          - { goarch: riscv64, exec: "-exec=qemu-riscv64-static", via: qemu }
          - { goarch: s390x,   exec: "-exec=qemu-s390x-static", via: qemu }
          # 386 runs with NO emulator: a 386 ELF executes directly on the
          # x86-64 runner. Measured on one commit of go-gfx/gfx, eight lanes
          # each: under qemu-i386 five failures out of eight, native zero.
          # qemu-i386 loses the guest's floating-point state across the signal
          # Go delivers for asynchronous preemption.
          - { goarch: "386",   exec: "", via: native }
          - { goarch: arm,     exec: "-exec=qemu-arm-static", via: qemu }
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version: '1.27.2'
      - run: sudo apt-get update && sudo apt-get install -y qemu-user-static
      - name: Test under qemu (${{ matrix.goarch }})
        run: GOARCH=${{ matrix.goarch }} go test ${{ matrix.exec }} ./...
`

// The twelve go-macos repositories: a CROSS-BUILT test binary run under the
// emulator the matrix named. There is no `go test -exec=` anywhere, which is
// why the sweep reported these as lanes that never emulate -- the opposite of
// true, and the reason this fixture exists.
const srcOnelineQemuVar = `name: ci
jobs:
  linux-arch:
    runs-on: ubuntu-latest
    strategy:
      fail-fast: false
      matrix:
        include:
          - { goarch: amd64, qemu: "", qemu_cpu: "" }
          - { goarch: arm64, qemu: qemu-aarch64-static, qemu_cpu: "" }
          - { goarch: loong64, qemu: qemu-loongarch64-static, qemu_cpu: "" }
          - { goarch: ppc64le, qemu: qemu-ppc64le-static, qemu_cpu: power10 }
    steps:
      - uses: actions/checkout@v7
      - name: Install qemu-user
        if: matrix.qemu != ''
        run: sudo apt-get update && sudo apt-get install -y qemu-user-static
      - name: Build the test binary
        run: GOOS=linux GOARCH=${{ matrix.goarch }} go test -c -o appicon.test ./...
      # QEMU_CPU must be EXPORTED ONLY when it has a value: qemu-user refuses to
      # start with an empty -cpu, and an env: key with an empty value is still a
      # string, which is what ` + "`env: QEMU_CPU: ${{ matrix.qemu_cpu }}`" + ` does for the
      # lanes that do not want one.
      - name: Run it
        env:
          QEMU: ${{ matrix.qemu }}
          QEMU_CPU_WANTED: ${{ matrix.qemu_cpu }}
        run: |
          if [ -n "$QEMU_CPU_WANTED" ]; then
            export QEMU_CPU="$QEMU_CPU_WANTED"
          fi
          if [ -z "$QEMU" ]; then
            ./appicon.test -test.count=1 -test.v
          else
            "$QEMU" ./appicon.test -test.count=1 -test.v
          fi
`

// The five go-pkgx repositories: the install step ALREADY guards with
// command -v, and the test step spells the flag with a SPACE
// (` + "`-exec ${{ matrix.qemu }}`" + `), which is why a search for ` + "`-exec=`" + ` missed it.
const srcGuardPkgxEnv = `name: ci
jobs:
  test-qemu:
    name: test (${{ matrix.goarch }}, qemu)
    runs-on: ubuntu-latest
    strategy:
      fail-fast: false
      matrix:
        include:
          - goarch: arm64
            qemu: qemu-aarch64-static
          - goarch: s390x
            qemu: qemu-s390x-static
    steps:
      - uses: actions/checkout@v7
      - name: qemu-user
        run: |
          sudo apt-get update -qq
          sudo apt-get install -y -qq qemu-user-static
          # Named, not assumed: a missing emulator must report itself as a
          # missing tool rather than as a failure on that architecture.
          command -v ${{ matrix.qemu }} || {
            echo "::error::${{ matrix.qemu }} is not in this image" >&2; exit 1; }
      - name: go test
        env:
          CGO_ENABLED: "0"
          GOARCH: ${{ matrix.goarch }}
          GOOS: linux
        # -count=1 so a cached PASS from the host architecture cannot stand in
        # for a run that never happened under emulation.
        run: go test -count=1 -exec ${{ matrix.qemu }} ./...
`

// go-tex/engine before #645: the lane derives the emulator name itself and --
// the defect inside the defect -- WARNS and exits 0 when it is not there, so
// an architecture that never ran reports success.
const srcOnelineArch = `name: ci
jobs:
  arch:
    strategy:
      matrix:
        goarch: [riscv64, loong64, ppc64le, s390x]
    steps:
      - uses: actions/checkout@v7
      - name: install qemu-user-static
        timeout-minutes: 10
        run: sudo apt-get update && sudo apt-get install -y qemu-user-static
      - name: test under qemu
        run: |
          arch="${{ matrix.goarch }}"
          # Go's GOARCH names mostly match qemu's user binary names; loong64 does not.
          q="qemu-${arch}-static"
          [ "$arch" = loong64 ] && q="qemu-loongarch64-static"
          if ! command -v "$q" >/dev/null 2>&1; then
            echo "::warning::$q not available on this runner; skipping $arch"
            exit 0
          fi
          GOARCH="$arch" go test -exec="$q" ./...
`
