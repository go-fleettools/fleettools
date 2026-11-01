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

// ⛔⛔ THE UNIT OF A CAMPAIGN IS A PULL REQUEST, AND THREE DIFFERENT go.mod
// FILES CAN SHARE ONE. Measured 2026-11-01 in go-pdfkit: five "consumers" of
// go-images/images were THREE repositories — `app` with its worktree
// `app.wt-verbs2`, and `html2pdf` with the module nested at
// `html2pdf/corpus`. Reporting only the pin count invites a plan about 40%
// too large, and the error is invisible because every row is real.
func TestAWorktreeAndANestedModuleAreOneRepository(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "org", "app")
	nested := filepath.Join(repo, "corpus")
	worktree := filepath.Join(root, "org", "app.wt-verbs2")

	for _, d := range []string{filepath.Join(repo, ".git"), nested, worktree} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	config := "[core]\n\tbare = false\n[remote \"origin\"]\n" +
		"\turl = https://github.com/org/app.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	if err := os.WriteFile(filepath.Join(repo, ".git", "config"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	// A linked worktree's .git is a FILE pointing into the main checkout.
	gitFile := "gitdir: " + filepath.Join(repo, ".git", "worktrees", "verbs2") + "\n"
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte(gitFile), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := map[string]string{}
	for _, dir := range []string{repo, nested, worktree} {
		if got := gitRepoOf(dir, cache); got != "org/app" {
			t.Errorf("gitRepoOf(%s) = %q, want org/app", dir, got)
		}
	}

	// ⭐ The negative half: a checkout with no remote must still count ONCE
	// rather than vanish, or a campaign would under-count instead.
	loose := filepath.Join(root, "loose")
	if err := os.MkdirAll(filepath.Join(loose, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := gitRepoOf(loose, cache); got != loose {
		t.Errorf("gitRepoOf(%s) = %q, want the directory itself", loose, got)
	}
	// And a directory under no repository at all is its own identity.
	bare := filepath.Join(root, "nowhere")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := gitRepoOf(bare, cache); got == "" {
		t.Error("a directory under no repository resolved to nothing")
	}
}

// ⛔⛔ DIRECT AND INHERITED ARE DIFFERENT WORK, and the campaign is the direct
// set. Measured 2026-11-01: go-freedesktop/notifications pins
// go-images/images at a 2026-08-31 pseudo-version marked `// indirect`, and
// `go mod why` gives notifications/cmd/notifyd -> go-widgets/toolkit ->
// go-images/images/ops — toolkit requires it DIRECTLY. Raising it in the
// consumer treats a symptom; releasing toolkit reaches every consumer.
//
// ⛔ AND A CHECKOUT UNDER THESE ROOTS IS NOT OURS BECAUSE IT IS ON THIS DISK.
// Origin's owner decides, so `third-party/app` is counted apart: we cannot
// open a pull request against it.
func TestDirectAndInheritedAreCountedApart(t *testing.T) {
	dir := t.TempDir()
	fakeProxy(t,
		map[string][]string{"github.com/go-images/images": {"v0.3.0"}},
		map[string]string{"github.com/go-images/images v0.3.0": "2026-10-01T00:00:00Z"},
		nil)

	const old = "v0.0.0-20260620120000-aaaaaaaaaaaa"
	// A library that DECLARES the stale pin: the root of the problem.
	write(t, dir, "go-widgets/toolkit/go.mod",
		"module github.com/go-widgets/toolkit\n\ngo 1.27\n\nrequire github.com/go-images/images "+old+"\n")
	write(t, dir, "go-widgets/toolkit/.git/config",
		"[remote \"origin\"]\n\turl = https://github.com/go-widgets/toolkit.git\n")
	// A consumer that INHERITS it.
	write(t, dir, "go-freedesktop/notifications/go.mod",
		"module github.com/go-freedesktop/notifications\n\ngo 1.27\n\nrequire github.com/go-images/images "+old+" // indirect\n")
	write(t, dir, "go-freedesktop/notifications/.git/config",
		"[remote \"origin\"]\n\turl = https://github.com/go-freedesktop/notifications.git\n")
	// A repository that is NOT ours, declaring it directly.
	write(t, dir, "third-party/app/go.mod",
		"module example.com/third-party/app\n\ngo 1.27\n\nrequire github.com/go-images/images "+old+"\n")
	write(t, dir, "third-party/app/.git/config",
		"[remote \"origin\"]\n\turl = https://example.com/third-party/app.git\n")

	// ⛔ A checkout with NO readable origin is a third class: the first
	// version of this called it "not ours", and 5 of the 7 it so named were
	// simply checkouts whose remote could not be read.
	write(t, dir, "nameless/go.mod",
		"module example.com/nameless\n\ngo 1.27\n\nrequire github.com/go-images/images "+old+"\n")
	if err := os.MkdirAll(filepath.Join(dir, "nameless", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, code := pinscan(t, dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	for _, want := range []string{
		"4 pin(s) older than a release",
		"3 declared by the consumer, 1 inherited through a dependency",
		"the campaign is 3 repositories that DECLARE one: " +
			"1 in a fleet org, 1 in another org, 1 with no origin this could read",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	// ⭐ --repos names the set, and distinguishes what it cannot attribute
	// from what it attributed elsewhere.
	named, code := pinscan(t, "--repos", dir)
	if code != 1 {
		t.Fatalf("exit %d with --repos\n%s", code, named)
	}
	if !strings.Contains(named, "go-widgets/toolkit\n") {
		t.Errorf("--repos does not name the declaring repository:\n%s", named)
	}
	if !strings.Contains(named, "third-party/app   (another org)") {
		t.Errorf("--repos does not mark the other org's repository:\n%s", named)
	}
	if !strings.Contains(named, "(no origin read)") {
		t.Errorf("--repos does not separate the unattributable checkout:\n%s", named)
	}
	if strings.Contains(named, "go-freedesktop/notifications\n") {
		t.Errorf("--repos names a repository that only INHERITS the pin:\n%s", named)
	}
}

// ⛔ wasmdesk was missing from the fleet list until pinscan's own --repos
// output called wasmdesk/wasmaqua and wasmdesk/wasmbox "not ours". A name list
// cannot tell "not in the list" from "not ours", which is why the report now
// says "another org" and keeps the unattributable ones apart.
func TestTheFleetListKnowsItsOwnOrgs(t *testing.T) {
	for _, repo := range []string{
		"github.com/go-images/images",
		"github.com/wasmdesk/wasmaqua",
		"github.com/cloud-boot/init",
		"github.com/openweft/weft",
		"github.com/deskplan/deskplan",
		"github.com/nano-container-linux/ncl",
	} {
		if !ours(repo) {
			t.Errorf("ours(%q) = false: the fleet list does not know its own org", repo)
		}
	}
	for _, repo := range []string{
		"github.com/third-party/app",
		"example.com/go-images/images",
		"github.com/",
		"",
	} {
		if ours(repo) {
			t.Errorf("ours(%q) = true", repo)
		}
	}
}

// ⭐ One repository named four ways must come out once, or the count is of
// spellings rather than of repositories.
func TestOwnerNameAgreesAcrossRemoteForms(t *testing.T) {
	for _, url := range []string{
		"https://github.com/org/app.git",
		"https://github.com/org/app",
		"git@github.com:org/app.git",
		"ssh://git@github.com/org/app.git",
		"https://github.com/org/app/",
	} {
		if got := ownerName(url); got != "org/app" {
			t.Errorf("ownerName(%q) = %q, want org/app", url, got)
		}
	}
	for _, url := range []string{"", "app", "/"} {
		if got := ownerName(url); got != "" {
			t.Errorf("ownerName(%q) = %q, want empty", url, got)
		}
	}
}
