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
		"oneline+matrix": srcOnelineMatrix,
		"retry+matrix":   srcRetryMatrix,
		"retry+case":     srcRetryCase,
		"oneline+arch":   srcOnelineArch,
	} {
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
		"oneline+matrix": srcOnelineMatrix,
		"retry+matrix":   srcRetryMatrix,
		"retry+case":     srcRetryCase,
		"oneline+arch":   srcOnelineArch,
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
