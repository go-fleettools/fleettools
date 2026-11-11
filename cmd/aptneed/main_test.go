package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// fake replaces the three seams for one test and puts them back afterwards.
// calls records every command line, because what several of these assert is
// how MANY times apt was asked, not only what it answered.
type fake struct {
	calls   []string
	answers []answer
	path    map[string]bool
	written map[string]string
	writeNo error
	slept   int
}

type answer struct {
	output string
	status int
	// grants is put on PATH when this answer is given, which is how a
	// successful install becomes a binary that exists.
	grants []string
}

func (f *fake) install(t *testing.T) {
	t.Helper()
	if f.path == nil {
		f.path = map[string]bool{}
	}
	f.written = map[string]string{}

	oldRun, oldLook, oldWrite, oldSleep := runCommand, lookPath, writeFile, sleep
	t.Cleanup(func() { runCommand, lookPath, writeFile, sleep = oldRun, oldLook, oldWrite, oldSleep })

	runCommand = func(name string, args ...string) (string, int) {
		line := name + " " + strings.Join(args, " ")
		f.calls = append(f.calls, line)
		// `update` is not what decides anything, so it always answers 0 here
		// unless a test says otherwise through an answer of its own.
		if len(args) > 0 && args[len(args)-1] == "update" {
			return "Reading package lists...\n", 0
		}
		if len(f.answers) == 0 {
			return "", 0
		}
		a := f.answers[0]
		if len(f.answers) > 1 {
			f.answers = f.answers[1:]
		}
		for _, g := range a.grants {
			f.path[g] = true
		}
		return a.output, a.status
	}
	lookPath = func(name string) (string, error) {
		if f.path[name] {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	writeFile = func(path string, data []byte) error {
		if f.writeNo != nil {
			return f.writeNo
		}
		f.written[path] = string(data)
		return nil
	}
	sleep = func(time.Duration) { f.slept++ }
}

// installs counts the apt-get install calls, which is the number of ATTEMPTS.
func (f *fake) installs() int {
	n := 0
	for _, c := range f.calls {
		if strings.Contains(c, " install ") {
			n++
		}
	}
	return n
}

func drive(t *testing.T, argv ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(argv, &out, &errb)
	return code, out.String(), errb.String()
}

// ⛔ ALREADY THERE IS A PASS, AND IT COSTS NOTHING. A warm runner must not pay
// a network round trip to be told what PATH already says — and a tool that ran
// apt anyway would make every job slower to no end.
func TestABinaryAlreadyOnPathInstallsNothing(t *testing.T) {
	f := &fake{path: map[string]bool{"pdftoppm": true}}
	f.install(t)
	code, out, _ := drive(t, "--need", "pdftoppm", "poppler-utils")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(f.calls) != 0 {
		t.Errorf("it ran %v", f.calls)
	}
	if !strings.Contains(out, "already on PATH") {
		t.Errorf("it said %q", out)
	}
}

func TestItInstallsAndSaysWhatIsNowOnPath(t *testing.T) {
	f := &fake{answers: []answer{{status: 0, grants: []string{"pdftoppm"}}}}
	f.install(t)
	code, out, _ := drive(t, "--need", "pdftoppm", "poppler-utils")
	if code != 0 {
		t.Fatalf("exit %d, said %q", code, out)
	}
	if f.installs() != 1 {
		t.Errorf("%d install attempt(s): %v", f.installs(), f.calls)
	}
	if !strings.Contains(out, "installed poppler-utils") || !strings.Contains(out, "pdftoppm on PATH") {
		t.Errorf("it said %q", out)
	}
}

// ⛔⛔ APT SAYING YES IS NOT THE BINARY BEING THERE. A package renamed upstream
// installs cleanly and leaves the job without its judge — which is how a suite
// comes to skip the very thing it was measuring, and go green doing it.
func TestAPackageThatInstallsWithoutProvidingTheBinary(t *testing.T) {
	f := &fake{answers: []answer{{status: 0}}} // installs, grants nothing
	f.install(t)
	code, _, errb := drive(t, "--need", "pdftoppm", "poppler-utils")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb, "does not provide it") {
		t.Errorf("it said %q", errb)
	}
	// ⛔ And it is not retried: a second install of the same package cannot
	// make it provide a binary it does not have.
	if f.installs() != 1 {
		t.Errorf("%d install attempt(s): %v", f.installs(), f.calls)
	}
}

// ⛔ A WRONG NAME IS NOT AN OUTAGE. Retrying it dresses a typo up as weather,
// and spends the delay on every one of them.
func TestAPackageAptCannotLocateIsNotRetried(t *testing.T) {
	f := &fake{answers: []answer{{
		output: "E: Unable to locate package poppler-util\n", status: 100}}}
	f.install(t)
	code, _, errb := drive(t, "--need", "pdftoppm", "--retries", "5", "poppler-util")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	if f.installs() != 1 {
		t.Errorf("a name was retried %d times: %v", f.installs(), f.calls)
	}
	if f.slept != 0 {
		t.Errorf("it slept %d time(s) over a typo", f.slept)
	}
	if !strings.Contains(errb, "that is a name, not an outage") {
		t.Errorf("it said %q", errb)
	}
}

// ⛔⛔ THE MIRRORLIST IS THE FILE THAT HAS TO CHANGE. Every source on a GitHub
// runner resolves through /etc/apt/apt-mirrors.txt, so naming archive.ubuntu.com
// in a source would change nothing: the source still reads the mirrorlist.
func TestAMirrorOutageIsSurvivedByRewritingTheMirrorlist(t *testing.T) {
	f := &fake{answers: []answer{
		{output: "E: Failed to fetch mirror+file:/etc/apt/apt-mirrors.txt/pool/main/p/poppler/" +
			"poppler-utils_24.02.0_amd64.deb  Temporary failure resolving 'azure.archive.ubuntu.com'\n",
			status: 100},
		{status: 0, grants: []string{"pdftoppm"}},
	}}
	f.install(t)
	code, out, errb := drive(t, "--need", "pdftoppm", "poppler-utils")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if got := f.written[mirrorList]; !strings.Contains(got, "archive.ubuntu.com") {
		t.Errorf("%s was written as %q", mirrorList, got)
	}
	if f.installs() != 2 {
		t.Errorf("%d install attempt(s): %v", f.installs(), f.calls)
	}
	if !strings.Contains(out, "installed poppler-utils") {
		t.Errorf("it said %q", out)
	}
}

// ⛔ The mirrorlist is rewritten ONCE. Writing it again on every later attempt
// would say nothing new and would hide, in the output, that the fallback had
// already been tried and had not helped.
func TestTheMirrorlistIsRewrittenOnlyOnce(t *testing.T) {
	down := answer{output: "Temporary failure resolving 'azure.archive.ubuntu.com'\n", status: 100}
	f := &fake{answers: []answer{down}}
	f.install(t)
	code, _, errb := drive(t, "--need", "pdftoppm", "--retries", "3", "poppler-utils")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	if f.installs() != 4 {
		t.Errorf("%d install attempt(s), want 4: %v", f.installs(), f.calls)
	}
	if n := strings.Count(errb, "now names"); n != 1 {
		t.Errorf("the mirrorlist was announced %d times", n)
	}
	if !strings.Contains(errb, "including the canonical archive") {
		t.Errorf("the verdict does not say the fallback was tried too: %q", errb)
	}
	// ⛔ AND IT BLAMES THE NETWORK, not the package. A job told the wrong one
	// of those goes looking in the wrong repository.
	if !strings.Contains(errb, "this is the network, not the package") {
		t.Errorf("it said %q", errb)
	}
}

// ⛔⛔ AN UNREACHABLE MIRROR ALSO PRINTS "Unable to locate package", because
// the package lists it could not fetch are empty. Reading the names first
// would call every outage a typo — and then NOT retry it, which is the worst
// of the two mistakes.
func TestAnOutageThatAlsoSaysTheNameIsUnknownIsAnOutage(t *testing.T) {
	f := &fake{answers: []answer{
		{output: "Temporary failure resolving 'azure.archive.ubuntu.com'\n" +
			"E: Unable to locate package poppler-utils\n", status: 100},
		{status: 0, grants: []string{"pdftoppm"}},
	}}
	f.install(t)
	code, _, errb := drive(t, "--need", "pdftoppm", "poppler-utils")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if f.installs() != 2 {
		t.Errorf("it treated an outage as a typo: %v", f.calls)
	}
}

func TestTheFallbackCanBeTurnedOff(t *testing.T) {
	f := &fake{answers: []answer{{output: "Could not resolve 'azure.archive.ubuntu.com'\n", status: 100}}}
	f.install(t)
	code, _, _ := drive(t, "--need", "pdftoppm", "--retries", "0",
		"--fallback-mirror=false", "poppler-utils")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	if len(f.written) != 0 {
		t.Errorf("it rewrote %v although the fallback was off", f.written)
	}
}

// A read-only /etc is not a reason to stop: the attempt that follows may work
// anyway, and the reason it could not be written belongs in the output.
func TestAMirrorlistThatCannotBeWritten(t *testing.T) {
	f := &fake{
		answers: []answer{
			{output: "Temporary failure resolving 'azure.archive.ubuntu.com'\n", status: 100},
			{status: 0, grants: []string{"pdftoppm"}},
		},
		writeNo: errors.New("read-only file system"),
	}
	f.install(t)
	code, _, errb := drive(t, "--need", "pdftoppm", "poppler-utils")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if !strings.Contains(errb, "could not rewrite") || !strings.Contains(errb, "read-only") {
		t.Errorf("it said %q", errb)
	}
}

// Something apt said that this does not recognise is printed rather than
// summarised: a verdict invented for an unknown message is a guess.
func TestAFailureThisDoesNotRecogniseIsPrintedWhole(t *testing.T) {
	f := &fake{answers: []answer{{output: "E: dpkg was interrupted\n", status: 100}}}
	f.install(t)
	code, _, errb := drive(t, "--need", "pdftoppm", "--retries", "0", "poppler-utils")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb, "dpkg was interrupted") {
		t.Errorf("it swallowed apt's words: %q", errb)
	}
	if !strings.Contains(errb, "said nothing this recognises") {
		t.Errorf("it said %q", errb)
	}
}

func TestWhatItRefusesToBeAskedFor(t *testing.T) {
	f := &fake{}
	f.install(t)
	for _, c := range []struct {
		why  string
		argv []string
		says string
	}{
		{"no package", []string{"--need", "pdftoppm"}, "name at least one package"},
		{"no --need", []string{"poppler-utils"}, "--need names the binary"},
	} {
		code, _, errb := drive(t, c.argv...)
		if code != 2 {
			t.Errorf("%s: exit %d, want 2", c.why, code)
		}
		if !strings.Contains(errb, c.says) {
			t.Errorf("%s: said %q", c.why, errb)
		}
	}
}

// ⛔ THE SEAM IS NOT THE PROGRAM. Every test above replaces runCommand, so
// without this one the real one — the half that actually starts a process and
// reads its status — would be covered by nothing.
func TestTheRealRunnerReportsStatusAndFailureToStart(t *testing.T) {
	if out, status := runCommand("sh", "-c", "echo fine"); status != 0 || !strings.Contains(out, "fine") {
		t.Errorf("a command that worked reported %d, %q", status, out)
	}
	out, status := runCommand("sh", "-c", "echo hello; exit 3")
	if status != 3 || !strings.Contains(out, "hello") {
		t.Errorf("exit %d, output %q", status, out)
	}
	if _, status := runCommand("aptneed-no-such-program-anywhere"); status != -1 {
		t.Errorf("a program that will not start reported %d", status)
	}
}

// And the real writeFile, for the same reason.
func TestTheRealWriterWrites(t *testing.T) {
	path := t.TempDir() + "/mirrors.txt"
	if err := writeFile(path, []byte(canonicalMirror)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != canonicalMirror {
		t.Errorf("read back %q, %v", b, err)
	}
}

func TestTheRealSleepSleeps(t *testing.T) {
	start := time.Now()
	sleep(time.Millisecond)
	if time.Since(start) < time.Millisecond {
		t.Error("it did not sleep")
	}
}

func TestHelpIsAskedForRatherThanRun(t *testing.T) {
	f := &fake{}
	f.install(t)
	// ⛔ ON STDERR. cli.New puts help there on purpose: stdout is a command's
	// ANSWER, and help on that stream is a line nobody asked for in the middle
	// of the data. A test that looked on stdout would ask for the wrong thing.
	code, out, errb := drive(t, "--help")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb, "mirror") || len(f.calls) != 0 {
		t.Errorf("--help printed %q and ran %v", errb, f.calls)
	}
	if out != "" {
		t.Errorf("help went to stdout: %q", out)
	}
}
