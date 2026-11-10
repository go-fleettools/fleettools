package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/checkout"
	"github.com/go-fleettools/fleettools/internal/cli"
)

// writeRepo makes a checkout that checkout.Repos will accept: root/org/repo
// with a .git DIRECTORY, plus the workflows given.
func writeRepo(t *testing.T, root, repo string, workflows map[string]string) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(repo))
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if len(workflows) == 0 {
		return
	}
	wf := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range workflows {
		if err := os.WriteFile(filepath.Join(wf, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunsOnValuesReadsTheShapesThatOccur(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want []string
	}{
		{"plain", "jobs:\n  a:\n    runs-on: ubuntu-latest\n", []string{"ubuntu-latest"}},
		{"quoted", `    runs-on: "macos-15"`, []string{"macos-15"}},
		{"single quoted", "    runs-on: 'windows-2025'", []string{"windows-2025"}},
		{"trailing comment", "    runs-on: ubuntu-24.04 # pinned on purpose", []string{"ubuntu-24.04"}},
		{"two jobs", "    runs-on: ubuntu-latest\n    runs-on: macos-15\n", []string{"ubuntu-latest", "macos-15"}},
		// ⛔ Reported VERBATIM rather than resolved. Resolving means reading the
		// matrix, and a wrong resolution is worse than an honest indirection.
		{"matrix expression", "    runs-on: ${{ matrix.os }}", []string{"${{ matrix.os }}"}},
		{"list form", "    runs-on: [self-hosted, linux]", []string{"[self-hosted, linux]"}},
		// A block form carries its values on following lines; this reads none
		// rather than inventing one.
		{"block form", "    runs-on:\n      - self-hosted\n", nil},
		{"not a runs-on", "    # runs-on: ubuntu-latest\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runsOnValues(tc.in)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("runsOnValues(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// ⛔ The defect this tool shipped with: a repository with several workflows
// counted several times. The question is how much of the fleet moves when an
// image moves, and that is a count of REPOSITORIES.
func TestOneRepositoryWithManyWorkflowsCountsOnce(t *testing.T) {
	counts := tally([]Finding{
		{Repo: "o/r", Workflow: "a.yml", RunsOn: "ubuntu-latest"},
		{Repo: "o/r", Workflow: "b.yml", RunsOn: "ubuntu-latest"},
		{Repo: "o/r", Workflow: "c.yml", RunsOn: "ubuntu-latest"},
		{Repo: "o/s", Workflow: "a.yml", RunsOn: "ubuntu-latest"},
	})
	if len(counts) != 1 {
		t.Fatalf("got %d values, want 1: %+v", len(counts), counts)
	}
	if n := len(counts[0].Repos); n != 2 {
		t.Errorf("ubuntu-latest counted %d repositories, want 2 (o/r has three workflows)", n)
	}
}

func TestTallyPutsTheMostUsedFirst(t *testing.T) {
	counts := tally([]Finding{
		{Repo: "o/a", RunsOn: "macos-15"},
		{Repo: "o/b", RunsOn: "ubuntu-latest"},
		{Repo: "o/c", RunsOn: "ubuntu-latest"},
	})
	if counts[0].Value != "ubuntu-latest" {
		t.Errorf("first value is %q, want ubuntu-latest", counts[0].Value)
	}
}

// ⛔⛔ THE CONTROL THAT MATTERS. Pointed at a root with no checkouts -- the
// flat <org>-<repo> working farm, say, instead of <org>/<repo> -- the command
// must REFUSE. Printing an empty table and exiting 0 would answer "nothing
// asks for ubuntu-latest" when the truth is that nothing was read.
func TestAnEmptyPopulationIsRefusedAndNotReportedAsClean(t *testing.T) {
	root := t.TempDir()
	// The flat shape, which Repos cannot read.
	if err := os.MkdirAll(filepath.Join(root, "go-desktop-catalog", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := cli.Drive(rootCmd(), []string{"--root", root}, &out, &errOut)
	if code != 2 {
		t.Errorf("status %d, want 2 (unanswerable); stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	if strings.Contains(out.String(), "runs-on") {
		t.Errorf("a table was printed for a population that was never read: %q", out.String())
	}
}

func TestItCountsRepositoriesUnderARoot(t *testing.T) {
	root := t.TempDir()
	writeRepo(t, root, "org/one", map[string]string{"ci.yml": "    runs-on: ubuntu-latest\n"})
	writeRepo(t, root, "org/two", map[string]string{
		"ci.yml":      "    runs-on: ubuntu-latest\n",
		"release.yml": "    runs-on: macos-15\n",
	})
	writeRepo(t, root, "org/three", nil) // a checkout with no CI at all

	var out, errOut bytes.Buffer
	code := cli.Drive(rootCmd(), []string{"--root", root, "--no-staleness"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("status %d, want 0; stderr=%q", code, errOut.String())
	}
	s := out.String()
	for _, want := range []string{
		"3 checkouts under",
		"2 with workflows, 1 without",
		"ubuntu-latest",
		"macos-15",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output is missing %q:\n%s", want, s)
		}
	}
	// Without a staleness measurement the output must SAY so, rather than
	// leave a reader to assume the evidence was checked.
	if !strings.Contains(s, "staleness NOT measured") {
		t.Errorf("--no-staleness did not say the evidence was unchecked:\n%s", s)
	}
}

// ⛔ ONLY .yml AND .yaml ARE WORKFLOWS. A README, a notes file or a schema
// sitting in .github/workflows/ can carry the string `runs-on:` without being
// a workflow at all, and counting it inflates whichever repository documents
// its own CI. Carried over from the version of this tool that lived outside a
// repository; the rewrite onto internal/checkout kept the filter and dropped
// the test, which is how a guard quietly becomes an assumption.
func TestOnlyWorkflowFilesAreRead(t *testing.T) {
	root := t.TempDir()
	writeRepo(t, root, "org/one", map[string]string{
		"ci.yml":      "    runs-on: ubuntu-latest\n",
		"deploy.yaml": "    runs-on: macos-15\n",
		"notes.md":    "    runs-on: windows-latest\n",
		"README":      "    runs-on: windows-latest\n",
	})
	found, had := scanRepo(filepath.Join(root, "org", "one"), "org/one")
	if !had {
		t.Fatal("a directory holding workflows reported none")
	}
	if len(found) != 2 {
		t.Fatalf("read %d values, want 2 (.yml and .yaml only): %+v", len(found), found)
	}
	for _, f := range found {
		if f.RunsOn == "windows-latest" {
			t.Errorf("a non-workflow file was read as one: %+v", f)
		}
	}
}

// A checkout with no .github/workflows at all is a repository without CI, not
// a repository that could not be read. The caller counts them apart.
func TestACheckoutWithNoWorkflowsIsNotAReadFailure(t *testing.T) {
	root := t.TempDir()
	writeRepo(t, root, "org/bare", nil)
	found, had := scanRepo(filepath.Join(root, "org", "bare"), "org/bare")
	if had || len(found) != 0 {
		t.Errorf("scanRepo = (%+v, %v), want (nil, false)", found, had)
	}
}

// ⛔ The note counts CHECKOUTS, not findings. runson reports a distribution --
// thousands of findings over hundreds of checkouts -- so borrowing the
// scanners' "N of the M findings above" sentence would have named the wrong
// thing and quoted the wrong total. The shared part is checkout.Judge.
func TestTheStalenessNoteCountsCheckoutsAndSaysWhichWayItIsWrong(t *testing.T) {
	note := stalenessNote([]checkout.Age{
		{Behind: 3, FetchKnown: true},
		{Behind: 188, FetchKnown: true},
		{Behind: 0, FetchKnown: true},
	})
	if !strings.Contains(note, "2 of the 3 CHECKOUTS") {
		t.Errorf("the note does not count checkouts:\n%s", note)
	}
	if !strings.Contains(note, "188") {
		t.Errorf("the note does not say how far the furthest is:\n%s", note)
	}
	// The direction of the error is the point: a stale checkout hides a
	// workflow that was ADDED, so the count is a floor.
	if !strings.Contains(note, "UNDERSTATES") {
		t.Errorf("the note does not say which way it is wrong:\n%s", note)
	}
	if strings.Contains(note, "findings") {
		t.Errorf("the note calls checkouts findings:\n%s", note)
	}
}

func TestANoteIsSaidOnlyWhenThereIsSomethingToSay(t *testing.T) {
	if note := stalenessNote([]checkout.Age{{Behind: 0, FetchKnown: true}}); note != "" {
		t.Errorf("a current checkout produced a warning: %q", note)
	}
}

// A checkout nobody fetched reports distance 0, which must NOT read as
// current -- that is checkout.Judge's rule, and this pins that runson uses it.
func TestAnUnfetchedCheckoutIsUnknownAndNotCurrent(t *testing.T) {
	note := stalenessNote([]checkout.Age{{Behind: 0, FetchAge: 72 * time.Hour, FetchKnown: true}})
	if !strings.Contains(note, "nobody asked") {
		t.Errorf("an unfetched checkout read as current:\n%s", note)
	}
}

// ⭐ The list is the answer; the count is a summary of it.
func TestValueListsTheRepositoriesBehindTheNumber(t *testing.T) {
	root := t.TempDir()
	writeRepo(t, root, "org/one", map[string]string{"ci.yml": "    runs-on: ubuntu-latest\n"})
	writeRepo(t, root, "org/two", map[string]string{"ci.yml": "    runs-on: macos-15\n"})

	var out, errOut bytes.Buffer
	code := cli.Drive(rootCmd(), []string{"--root", root, "--value", "ubuntu-latest", "--no-staleness"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("status %d, want 0; stderr=%q", code, errOut.String())
	}
	s := out.String()
	if !strings.Contains(s, "org/one") {
		t.Errorf("the repository behind the count is not named:\n%s", s)
	}
	if strings.Contains(s, "org/two") {
		t.Errorf("a repository asking for another value was listed:\n%s", s)
	}
}

func TestAValueNobodyAsksForIsAnAnswerNotAnError(t *testing.T) {
	root := t.TempDir()
	writeRepo(t, root, "org/one", map[string]string{"ci.yml": "    runs-on: ubuntu-latest\n"})

	var out, errOut bytes.Buffer
	code := cli.Drive(rootCmd(), []string{"--root", root, "--value", "ubuntu-20.04", "--no-staleness"}, &out, &errOut)
	if code != 0 {
		t.Errorf("status %d, want 0: the question was put and answered; stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "no checkout asks for") {
		t.Errorf("output does not say nobody asks for it:\n%s", out.String())
	}
}

// --fail-on is the gate form: it exits 1 when the thing is still there, so a
// migration can be driven to zero by a check rather than by rereading a table.
func TestFailOnExitsOneOnlyWhileTheValueIsStillAskedFor(t *testing.T) {
	root := t.TempDir()
	writeRepo(t, root, "org/one", map[string]string{"ci.yml": "    runs-on: ubuntu-latest\n"})

	var out, errOut bytes.Buffer
	if code := cli.Drive(rootCmd(), []string{"--root", root, "--fail-on", "ubuntu-latest", "--no-staleness"}, &out, &errOut); code != 1 {
		t.Errorf("status %d, want 1 while a repository still asks for it", code)
	}
	out.Reset()
	errOut.Reset()
	if code := cli.Drive(rootCmd(), []string{"--root", root, "--fail-on", "ubuntu-20.04", "--no-staleness"}, &out, &errOut); code != 0 {
		t.Errorf("status %d, want 0 once nothing asks for it; stderr=%q", code, errOut.String())
	}
}
