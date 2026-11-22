package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// theDocsGoMod is go-fft/docs's go.mod verbatim at 883c572, the repository
// #294 was filed for: one requirement, a Hugo theme, and not one .go file in
// the whole repository. `go mod tidy` there deletes the theme from go.mod AND
// go.sum, so the gate's own remedy breaks the site.
const theDocsGoMod = `module github.com/go-fft/docs

go 1.27.1

require github.com/McShelby/hugo-theme-relearn v0.0.0-20260929045444-eb7f9abe01c0 // indirect
`

// theDocsHugoToml mounts the theme through `theme = [...]` and has NO [module]
// block, which is how the real site does it. A rule keyed on that block would
// miss the occurrence it was written for, so the tests pin the real shape.
const theDocsHugoToml = `baseURL = "https://go-fft.github.io/docs/"
title = "go-fft docs"
theme = ["github.com/McShelby/hugo-theme-relearn"]
`

// stubRunner replaces the real `go mod tidy` and records the toolchain of every
// call, so a test can assert that go was not run at all.
func stubRunner(t *testing.T, f func(dir, toolchain string) (string, string, int, error)) *[]string {
	t.Helper()
	var calls []string
	prev := runner
	runner = func(_ context.Context, dir, toolchain string) (string, string, int, error) {
		calls = append(calls, toolchain)
		if len(calls) > 4 {
			// judge retries once at most; answering here ends a loop that forgot
			// that, as a failure instead of a hang.
			t.Errorf("the runner was asked %d times for %s", len(calls), dir)
			return "", "", 0, nil
		}
		return f(dir, toolchain)
	}
	t.Cleanup(func() { runner = prev })
	return &calls
}

func TestNoGoSourceIsSkippedAndGoIsNeverRun(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), theDocsGoMod)
	write(t, filepath.Join(root, "hugo.toml"), theDocsHugoToml)
	write(t, filepath.Join(root, "content", "_index.md"), "# docs\n")
	calls := stubRunner(t, func(_, tc string) (string, string, int, error) {
		t.Errorf("go ran for a module with no Go source (GOTOOLCHAIN=%s)", tc)
		return "", "", 0, nil
	})
	code, out, _ := run(t, root)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "skipped .") {
		t.Fatalf("not reported skipped:\n%s", out)
	}
	if !strings.Contains(out, "hugo.toml") {
		t.Fatalf("the reason does not name the Hugo config:\n%s", out)
	}
	// Counted, not silent: a skip nobody can see is how a scan stops being
	// evidence.
	if !strings.Contains(out, "1 skipped") {
		t.Fatalf("the summary does not count the skip:\n%s", out)
	}
	if len(*calls) != 0 {
		t.Fatalf("go ran %d time(s); the skip must need no toolchain and no network", len(*calls))
	}
}

func TestNestedGoSourceDoesNotAnswerForItsParent(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), theDocsGoMod)
	write(t, filepath.Join(root, "hugo.toml"), theDocsHugoToml)
	write(t, filepath.Join(root, "tools", "go.mod"), "module example.com/tools\n\ngo 1.20\n")
	write(t, filepath.Join(root, "tools", "t.go"), "package tools\n")
	stubRunner(t, func(dir, _ string) (string, string, int, error) {
		if filepath.Base(dir) != "tools" {
			t.Errorf("go ran in %s, which holds no Go source of its own", dir)
		}
		return "", "", 0, nil
	})
	code, out, _ := run(t, root)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "skipped .") || !strings.Contains(out, "tidy    tools") {
		t.Fatalf("want the root skipped and tools judged:\n%s", out)
	}
}

func TestNoGoSourceWithoutAHugoConfigIsStillSkipped(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"),
		"module example.com/nothing\n\ngo 1.20\n\nrequire example.com/dep v1.0.0\n")
	stubRunner(t, func(_, _ string) (string, string, int, error) {
		t.Errorf("go ran for a module with no Go source")
		return "", "", 0, nil
	})
	code, out, _ := run(t, root)
	if code != 0 || !strings.Contains(out, "skipped .") {
		t.Fatalf("exit = %d:\n%s", code, out)
	}
	// The decision is "no Go source", so no Hugo reason may be invented for a
	// module that has no Hugo configuration.
	if strings.Contains(out, "Hugo") {
		t.Fatalf("a Hugo reason was invented:\n%s", out)
	}
}

func TestAFileBehindABuildConstraintStillCounts(t *testing.T) {
	// kit.go in the go-fft.github.io landing: //go:build tools plus a blank
	// import, the documented way to keep a requirement no package uses. It IS
	// Go source -- `go mod tidy` reads every build configuration -- so the
	// module must be judged, not skipped. Skipping it would hide the two dead
	// go.sum lines this gate correctly found there.
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/site\n\ngo 1.20\n")
	write(t, filepath.Join(root, "hugo.toml"), theDocsHugoToml)
	write(t, filepath.Join(root, "kit.go"),
		"//go:build tools\n\npackage site\n\nimport _ \"example.com/kit\"\n")
	calls := stubRunner(t, func(_, _ string) (string, string, int, error) { return "", "", 0, nil })
	code, out, _ := run(t, root)
	if code != 0 || !strings.Contains(out, "tidy    .") {
		t.Fatalf("exit = %d, want the module judged:\n%s", code, out)
	}
	if len(*calls) != 1 {
		t.Fatalf("go ran %d time(s), want 1", len(*calls))
	}
}

func TestADirectiveFloorRetriesWithTheToolchainItNames(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/high\n\ngo 1.27.9\n")
	write(t, filepath.Join(root, "a.go"), "package high\n")
	calls := stubRunner(t, func(_, tc string) (string, string, int, error) {
		if tc == localToolchain {
			return "", "go: go.mod requires go >= 1.27.9 (running go 1.27.3; GOTOOLCHAIN=local)", 1, nil
		}
		return "", "", 0, nil
	})
	code, out, _ := run(t, root)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 once the named toolchain answers\n%s", code, out)
	}
	if len(*calls) != 2 || (*calls)[0] != localToolchain || (*calls)[1] != "go1.27.9" {
		t.Fatalf("toolchains tried = %v, want [local go1.27.9]", *calls)
	}
	// A verdict reached under another toolchain must say so, or a reader cannot
	// tell it from one reached under whatever this host has installed.
	if !strings.Contains(out, "GOTOOLCHAIN=go1.27.9") {
		t.Fatalf("the report does not name the toolchain that answered:\n%s", out)
	}
}

func TestARetryThatAlsoFailsStaysUnread(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/high\n\ngo 1.27.9\n")
	write(t, filepath.Join(root, "a.go"), "package high\n")
	calls := stubRunner(t, func(_, tc string) (string, string, int, error) {
		return "", "go: go.mod requires go >= 1.27.9 (running go 1.27.3; GOTOOLCHAIN=" + tc + ")", 1, nil
	})
	code, out, _ := run(t, root)
	if code != 2 || !strings.Contains(out, "UNREAD") {
		t.Fatalf("exit = %d, want 2 with UNREAD\n%s", code, out)
	}
	// Exactly two attempts: the retry gets one turn and the answer stands,
	// otherwise a floor that never clears becomes a loop.
	if len(*calls) != 2 {
		t.Fatalf("attempts = %v, want exactly two", *calls)
	}
}

func TestHookAllowsAHugoSiteAndSaysWhy(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), theDocsGoMod)
	write(t, filepath.Join(root, "hugo.toml"), theDocsHugoToml)
	gitInit(t, root)
	stubRunner(t, func(_, _ string) (string, string, int, error) {
		t.Errorf("go ran for a module with no Go source")
		return "", "", 0, nil
	})
	out, e := hookRun(t, payloadFor(t, root, "gitpush origin main"))
	if strings.TrimSpace(out) != "{}" {
		t.Fatalf("a Hugo site was refused, and its remedy would delete the theme: %s", out)
	}
	if !strings.Contains(e, "skipped") {
		t.Fatalf("the skip is silent, so a reader cannot tell it from a module that was checked: %q", e)
	}
}
