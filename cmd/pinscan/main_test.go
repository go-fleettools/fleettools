package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔⛔ EVERY CASE BELOW IS A MISTAKE THE SHELL VERSION OF THIS MEASUREMENT MADE.
// It was written as a pipeline of grep, cut, sort and uniq on 2026-10-18, and
// it got three things wrong in one sitting:
//
//   - a proxy reply of `fatal: could not read Username for 'https://github.com'`
//     sorted to the end of the version list and was reported as the module's
//     latest release;
//   - `v0.0.0-00010101000000-000000000000`, which is Go's way of saying there
//     is no commit at all, was reported as "pinned since 0001-01-01";
//   - a column-aligned intermediate file was re-read with the wrong offsets, so
//     the whole pass reported ZERO findings — indistinguishable from a clean
//     fleet.
//
// Each is a test here, and that is the reason the tool exists rather than the
// pipeline.

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeProxy answers like proxy.golang.org, and 404s for anything it was not
// given — which is how the proxy says "never released".
func fakeProxy(t *testing.T, versions map[string][]string, times map[string]string, raw map[string]string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mod, rest, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/@v/")
		if !ok {
			http.NotFound(w, r)
			return
		}
		if rest == "list" {
			if body, ok := raw[mod]; ok {
				_, _ = w.Write([]byte(body))
				return
			}
			vs, ok := versions[mod]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(strings.Join(vs, "\n") + "\n"))
			return
		}
		if v, ok := strings.CutSuffix(rest, ".info"); ok {
			if ts, ok := times[mod+" "+v]; ok {
				_, _ = w.Write([]byte(`{"Version":"` + v + `","Time":"` + ts + `"}`))
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	old := proxy
	proxy = srv.URL
	t.Cleanup(func() { proxy = old })
}

func pinscan(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return out.String() + errb.String(), code
}

// ⛔ THE DEFECT THE TOOL WAS WRITTEN FOR: go-downloader pinned
// go-macos/appbundle at an untagged August commit while Sign shipped in v0.5.0,
// so a published, documented, tested API was unreachable for seven weeks.
func TestAPinOlderThanAReleaseIsReported(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go-downloader/go-downloader/go.mod", `module github.com/go-downloader/go-downloader

go 1.27

require github.com/go-macos/appbundle v0.0.0-20260829193321-834ae4610126
`)
	fakeProxy(t,
		map[string][]string{"github.com/go-macos/appbundle": {"v0.4.0", "v0.5.0", "v0.6.0"}},
		map[string]string{"github.com/go-macos/appbundle v0.6.0": "2026-09-30T10:00:00Z"},
		nil)

	out, code := pinscan(t, "-v", dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	for _, want := range []string{
		"1 pin(s) older than a release",
		"github.com/go-macos/appbundle",
		"go-downloader/go-downloader",
		"v0.6.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
}

// ⭐ The half that keeps the tool from naming the whole fleet. Measured
// 2026-10-18: of 262 fleet modules pinned by pseudo-version, 234 had NEVER been
// released — so a pseudo-version was the only way to name them, and calling
// that a defect would have reported 262 problems of which 234 were not.
func TestAModuleNeverReleasedIsNotADefect(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "org/consumer/go.mod", `module github.com/org/consumer

go 1.27

require github.com/go-newthing/newthing v0.0.0-20260101000000-aaaaaaaaaaaa
`)
	fakeProxy(t, nil, nil, nil) // 404 for everything: nothing is released

	out, code := pinscan(t, dir)
	if code != 0 {
		t.Fatalf("exit %d, want 0 — an unreleased module cannot be pinned any other way:\n%s", code, out)
	}
	if !strings.Contains(out, "1 pin(s) of a module that was NEVER released") {
		t.Errorf("the unreleased pin is not accounted for out loud:\n%s", out)
	}
}

// ⛔⛔ "BEHIND" IS NOT "DIFFERENT". A sibling tool in this repository reported 52
// stale pins by testing inequality and 35 were NEWER than the tag: raising those
// would have been a downgrade.
func TestAPinNewerThanTheTagIsNotBehind(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "org/consumer/go.mod", `module github.com/org/consumer

go 1.27

require github.com/go-thing/thing v0.0.0-20261015120000-bbbbbbbbbbbb
`)
	fakeProxy(t,
		map[string][]string{"github.com/go-thing/thing": {"v0.1.0"}},
		map[string]string{"github.com/go-thing/thing v0.1.0": "2026-10-01T00:00:00Z"},
		nil)

	out, code := pinscan(t, dir)
	if code != 0 {
		t.Fatalf("exit %d, want 0 — the pin is newer than the release:\n%s", code, out)
	}
	if !strings.Contains(out, "1 pin(s) NEWER than the newest tag") {
		t.Errorf("a pin ahead of the tag is not reported as such:\n%s", out)
	}
	if strings.Contains(out, "1 pin(s) older") {
		t.Errorf("a pin ahead of the tag was counted as behind:\n%s", out)
	}
}

// ⛔ A PROXY ERROR IS NOT A VERSION. This exact reply sorted to the end of the
// shell version's list and was reported as the module's latest release, which
// made an unreachable module look released.
func TestAProxyErrorIsNotTakenForAVersion(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "org/consumer/go.mod", `module github.com/org/consumer

go 1.27

require github.com/go-grub/grub v0.0.0-20260101000000-cccccccccccc
`)
	fakeProxy(t, nil, nil, map[string]string{
		"github.com/go-grub/grub": "fatal: could not read Username for 'https://github.com': terminal prompts disabled\n",
	})

	out, code := pinscan(t, dir)
	if code != 0 {
		t.Fatalf("exit %d, want 0 — an error is not a release:\n%s", code, out)
	}
	if strings.Contains(out, "older than a release") && !strings.Contains(out, "0 pin(s) older") {
		t.Errorf("a proxy error was read as a released version:\n%s", out)
	}
}

// ⛔ v0.0.0-00010101000000-000000000000 is Go saying there is NO commit — a
// module required but absent, or replaced by a local path. The shell version
// reported it as "pinned since 0001-01-01".
func TestTheZeroTimePseudoVersionIsNotAPin(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "org/consumer/go.mod", `module github.com/org/consumer

go 1.27

require github.com/openweft/weft-microvm-agent v0.0.0-00010101000000-000000000000
`)
	fakeProxy(t,
		map[string][]string{"github.com/openweft/weft-microvm-agent": {"v0.4.2"}},
		map[string]string{"github.com/openweft/weft-microvm-agent v0.4.2": "2026-06-20T00:00:00Z"},
		nil)

	out, code := pinscan(t, dir)
	if code != 0 {
		t.Fatalf("exit %d, want 0 — the zero time is not a pin:\n%s", code, out)
	}
	if !strings.Contains(out, "0 require(s) of a fleet module at an untagged commit") {
		t.Errorf("the zero-time pseudo-version was counted as a pin:\n%s", out)
	}
}

// ⛔ A SCAN THAT READ NOTHING MUST NOT PASS. The shell version reported zero
// findings from a parsing mistake, and zero findings is what a clean fleet
// looks like too.
func TestAWalkThatFoundNoGoModRefuses(t *testing.T) {
	out, code := pinscan(t, t.TempDir())
	if code != 2 {
		t.Errorf("exit %d, want 2 — no go.mod means the roots are wrong, not that the fleet is clean:\n%s", code, out)
	}
	if !strings.Contains(out, "read 0 go.mod file(s)") {
		t.Errorf("it does not say it read nothing:\n%s", out)
	}
}

// ⭐ And a module that is not ours is not our release to make.
func TestAThirdPartyPinIsNotOurs(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "org/consumer/go.mod", `module github.com/org/consumer

go 1.27

require (
	github.com/hashicorp/go-plugin v0.0.0-20260101000000-dddddddddddd
	golang.org/x/net v0.0.0-20260101000000-eeeeeeeeeeee
)
`)
	fakeProxy(t, nil, nil, nil)
	out, code := pinscan(t, dir)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "0 require(s) of a fleet module") {
		t.Errorf("a third party's pseudo-version was counted as ours:\n%s", out)
	}
}

// ⛔ `-h` is not a usage error, and a fleet tool that RAN on -h has already cost
// a full sweep in this repository.
func TestHelpExitsZero(t *testing.T) {
	if _, code := pinscan(t, "-h"); code != 0 {
		t.Errorf("exit %d for -h, want 0", code)
	}
}
