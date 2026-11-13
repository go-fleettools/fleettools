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
	calls []string
	// update is what `apt-get update` answers. ⛔ IT IS ITS OWN FIELD because
	// the first version of this fake hardcoded update's reply, which made the
	// defect below INVISIBLE BY CONSTRUCTION: a test cannot catch an update
	// failure being read as the install's verdict if update can never fail.
	update  *answer
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
		if len(args) > 0 && args[len(args)-1] == "update" {
			if f.update != nil {
				return f.update.output, f.update.status
			}
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

// updates counts the apt-get update calls.
func (f *fake) updates() int {
	n := 0
	for _, c := range f.calls {
		if strings.HasSuffix(c, " update") {
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

// ⛔ THE LISTS ARE REFRESHED BEFORE INSTALLING. Nothing asserted this, so
// deleting the `apt-get update` call outright left the suite green — on a
// runner whose lists predate the package, the install would then fail for a
// reason the tool would report as a missing package.
func TestTheListsAreRefreshedBeforeInstalling(t *testing.T) {
	f := &fake{answers: []answer{{status: 0, grants: []string{"pdftoppm"}}}}
	f.install(t)
	if code, _, errb := drive(t, "--need", "pdftoppm", "poppler-utils"); code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if f.updates() != 1 {
		t.Errorf("%d update call(s): %v", f.updates(), f.calls)
	}
	// ⛔ BEFORE, not merely somewhere. Counting the call left the order free:
	// moving `update` after the install kept this green, and an install run
	// against lists that are refreshed afterwards is an install against stale
	// lists.
	if len(f.calls) == 0 || !strings.HasSuffix(f.calls[0], " update") {
		t.Errorf("the lists were not refreshed FIRST: %v", f.calls)
	}
}

// ⛔⛔ "CANNOT LOCATE" MEANS NOTHING WHEN THE LISTS COULD NOT BE REFRESHED,
// AND THIS IS THE CASE TWO REVIEWS READ OPPOSITE WAYS. The same two lines —
// a resolver error from `update`, "Unable to locate package" from the install
// — are a broken optional PPA beside a real misspelling, OR a cold cache in
// which the name was never checked against anything. They cannot be told
// apart from the text, so neither reading may be hard-coded: whichever is
// chosen is wrong half the time and confident every time.
//
// What is asserted instead is that the tool does not choose. It retries, like
// the mirror failure it might be, and its closing verdict names BOTH facts.
func TestANameAptCouldNotCheckIsNotCalledATypo(t *testing.T) {
	f := &fake{
		update: &answer{
			output: "Err:1 http://azure.archive.ubuntu.com/ubuntu noble InRelease\n" +
				"  Temporary failure resolving 'azure.archive.ubuntu.com'\n" +
				"E: Some index files failed to download.\n", status: 100},
		answers: []answer{{output: "Reading package lists... Done\n" +
			"E: Unable to locate package poppler-utils\n", status: 100}},
	}
	f.install(t)
	code, _, errb := drive(t, "--need", "pdftoppm", "--retries", "1", "poppler-utils")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	// ⛔ RETRIED, because retrying is cheap and the fallback may settle it,
	// where calling it a typo ends the job on evidence that does not exist.
	if f.installs() != 2 {
		t.Errorf("it decided on its own which of the two this was: %d attempt(s), %v",
			f.installs(), f.calls)
	}
	// ⛔⛔ AND THE RUNNER IS NOT CHANGED ON THIS EVIDENCE. The retry is free;
	// rewriting the mirrorlist is not, because it persists for every later step
	// of the job — and this case MAY BE A MISSPELLING. A first look that cannot
	// tell the two apart does not buy an irreversible act.
	if len(f.written) != 0 {
		t.Errorf("a name this could not check rewrote %s on the first look: %v",
			mirrorList, f.written)
	}
	// ⛔ AND THE VERDICT NAMES BOTH, so the reader is sent to the spelling and
	// to the network, in that order, rather than confidently to one of them.
	for _, want := range []string{"could not locate it AND could not refresh", "check the spelling first"} {
		if !strings.Contains(errb, want) {
			t.Errorf("the verdict does not say %q: %q", want, errb)
		}
	}
	if strings.Contains(errb, "that is a name, not an outage") {
		t.Errorf("it called an unprovable name a typo: %q", errb)
	}
}

// And a name apt COULD check is still refused outright: lists refreshed
// cleanly, so "cannot locate" is evidence rather than a shrug.
//
// ⛔ THE REFRESH HERE SUCCEEDS EXPLICITLY, status 0, while printing a warning.
// It used to rely on the fake's default reply, which made this test a strict
// subset of TestAPackageAptCannotLocateIsNotRetried — it could not go red
// alone, so it defended nothing. The warning is there because a refresh that
// SAYS something alarming and still exits 0 is the case that separates reading
// the status from reading the words.
func TestANameAptCouldCheckIsStillRefusedOutright(t *testing.T) {
	f := &fake{
		update: &answer{output: "W: Skipping acquire of configured file 'main/binary-i386/Packages'\n",
			status: 0},
		answers: []answer{{output: "E: Unable to locate package poppler-util\n", status: 100}},
	}
	f.install(t)
	code, _, errb := drive(t, "--need", "pdftoppm", "--retries", "2", "poppler-util")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	if f.installs() != 1 {
		t.Errorf("a name apt could check was retried %d times: %v", f.installs(), f.calls)
	}
	if !strings.Contains(errb, "that is a name, not an outage") {
		t.Errorf("it said %q", errb)
	}
}

// ⛔⛔ A REFRESH CAN FAIL WITHOUT NAMING THE NETWORK, AND THE LISTS ARE JUST AS
// STALE. This is the case that killed reading update's PHRASES: an unsigned
// repository disables the source (NO_PUBKEY), so apt exits non-zero having
// refreshed nothing, and not one of the mirror phrases appears. The name was
// never checked against anything, exactly as in a dead-mirror cold cache.
//
// A held lists/lock and a full disk are the same shape. What this asserts is
// that the staleness test is `update`'s STATUS and not its words.
func TestARefreshThatFailedWithoutNamingTheNetworkIsStillAStaleList(t *testing.T) {
	f := &fake{
		update: &answer{
			output: "W: GPG error: http://archive.ubuntu.com noble InRelease: The following " +
				"signatures couldn't be verified: NO_PUBKEY 871920D1991BC93C\n" +
				"E: The repository 'http://archive.ubuntu.com noble InRelease' is not signed.\n",
			status: 100},
		answers: []answer{{output: "E: Unable to locate package poppler-utils\n", status: 100}},
	}
	f.install(t)
	code, _, errb := drive(t, "--need", "pdftoppm", "--retries", "1", "poppler-utils")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	if f.installs() != 2 {
		t.Errorf("a name apt never checked was called a typo: %d attempt(s), %v",
			f.installs(), f.calls)
	}
	if !strings.Contains(errb, "could not locate it AND could not refresh") {
		t.Errorf("the verdict does not name both facts: %q", errb)
	}
	if strings.Contains(errb, "that is a name, not an outage") {
		t.Errorf("it called an unchecked name a typo: %q", errb)
	}
}

// ⛔ AND THE RUNNER IS CHANGED ONCE THE SAME UNREADABLE ANSWER COMES BACK. The
// escalation is the whole design: a typo pays one retry and leaves the machine
// as it found it, while a cold cache that is still cold on the second look
// gets the mirrorlist rewritten in time for a third attempt to use it.
//
// Three attempts, so attempt 1 is both a repeat and not the last.
func TestAnUnprovenNameThatRepeatsDoesBuyTheFallback(t *testing.T) {
	stale := answer{output: "E: Unable to locate package poppler-utils\n", status: 100}
	f := &fake{
		update:  &answer{output: "E: Some index files failed to download.\n", status: 100},
		answers: []answer{stale, stale, stale},
	}
	f.install(t)
	code, _, _ := drive(t, "--need", "pdftoppm", "--retries", "2", "poppler-utils")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	if f.installs() != 3 {
		t.Errorf("%d attempt(s): %v", f.installs(), f.calls)
	}
	if f.written[mirrorList] != canonicalMirror {
		t.Errorf("the fallback was never tried although the same unreadable answer came back "+
			"twice: %v", f.written)
	}
}

// ⛔⛔ AN EARLIER FAILURE OF ANOTHER KIND IS NOT "THE SAME ANSWER TWICE".
// Written `attempt > 0`, a first attempt that died of something else — dpkg
// interrupted — let the NEXT unproven name rewrite the mirrorlist on its
// first sighting, spending the permanent change this is meant to withhold
// from a possible typo. Review measured it; --help promised otherwise.
func TestAnEarlierFailureOfAnotherKindIsNotTheSameAnswerTwice(t *testing.T) {
	f := &fake{
		update: &answer{output: "E: Some index files failed to download.\n", status: 100},
		answers: []answer{
			{output: "E: dpkg was interrupted\n", status: 100},
			{output: "E: Unable to locate package poppler-utils\n", status: 100},
			{output: "E: Unable to locate package poppler-utils\n", status: 100},
		},
	}
	f.install(t)
	code, _, _ := drive(t, "--need", "pdftoppm", "--retries", "2", "poppler-utils")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	// Attempt 1 is the FIRST unprovable answer, so nothing may be rewritten
	// yet; attempt 2 is the second, but it is also the last, so there is no
	// attempt left to use a rewrite.
	if len(f.written) != 0 {
		t.Errorf("an unrelated earlier failure was counted as a repeat: %v", f.written)
	}
}

// ⛔ THE ARGUMENTS APT IS ACTUALLY GIVEN, which nothing asserted: review
// removed `-y`, `--no-install-recommends` and both `Acquire::Retries` and the
// suite stayed green. A missing `-y` does not fail a test — it HANGS a CI job
// waiting for an answer nobody is there to give.
func TestWhatAptIsActuallyAskedFor(t *testing.T) {
	f := &fake{answers: []answer{{grants: []string{"pdftoppm"}}}}
	f.install(t)
	code, _, _ := drive(t, "--need", "pdftoppm", "poppler-utils")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var update, install string
	for _, c := range f.calls {
		if strings.HasSuffix(c, " update") {
			update = c
		}
		if strings.Contains(c, " install ") {
			install = c
		}
	}
	for _, want := range []string{"-o Acquire::Retries=3", "update"} {
		if !strings.Contains(update, want) {
			t.Errorf("the update call does not carry %q: %q", want, update)
		}
	}
	for _, want := range []string{
		"-o Acquire::Retries=3", "install", "-y", "--no-install-recommends", "poppler-utils",
	} {
		if !strings.Contains(install, want) {
			t.Errorf("the install call does not carry %q: %q", want, install)
		}
	}
}

// ⛔ AND A RETRY WAITS. Deleting the sleep left the suite green, so nothing
// said that "attempt 2 of 3 after 20s" was anything but a sentence.
func TestARetryWaitsBeforeAskingAgain(t *testing.T) {
	f := &fake{answers: []answer{
		{output: "Temporary failure resolving 'azure.archive.ubuntu.com'\n", status: 100},
		{output: "Temporary failure resolving 'azure.archive.ubuntu.com'\n", status: 100},
	}}
	f.install(t)
	code, _, _ := drive(t, "--need", "pdftoppm", "--retries", "1", "--delay", "5s", "poppler-utils")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	if f.slept != 1 {
		t.Errorf("a retry slept %d time(s), want 1: %v", f.slept, f.calls)
	}
}

// ⛔ EVERY WORD apt USES, each its own case. Review deleted four of the five
// mirror phrases and the second missing-package phrase one at a time, and the
// suite stayed green for all of them: only two phrases were ever exercised,
// so why()'s table was five-sixths decoration.
func TestEveryPhraseAptUsesIsRecognised(t *testing.T) {
	for _, c := range []struct {
		phrase   string
		outage   bool
		attempts int
	}{
		{"Temporary failure resolving 'azure.archive.ubuntu.com'", true, 2},
		{"Could not resolve 'azure.archive.ubuntu.com'", true, 2},
		{"Failed to fetch http://azure.archive.ubuntu.com/x.deb", true, 2},
		{"Connection failed", true, 2},
		{"Unable to connect to azure.archive.ubuntu.com", true, 2},
		{"E: Unable to locate package poppler-utils", false, 1},
		{"E: Package 'poppler-utils' has no installation candidate", false, 1},
	} {
		t.Run(c.phrase, func(t *testing.T) {
			f := &fake{
				update:  &answer{output: "Reading package lists... Done\n", status: 0},
				answers: []answer{{output: c.phrase + "\n", status: 100}},
			}
			f.install(t)
			code, _, errb := drive(t, "--need", "pdftoppm", "--retries", "1", "poppler-utils")
			if code != 1 {
				t.Fatalf("exit %d", code)
			}
			if f.installs() != c.attempts {
				t.Errorf("%d attempt(s), want %d: %v", f.installs(), c.attempts, f.calls)
			}
			saysOutage := strings.Contains(errb, "the runner's mirror could not be reached")
			if saysOutage != c.outage {
				t.Errorf("read as an outage = %v, want %v: %q", saysOutage, c.outage, errb)
			}
		})
	}
}

// ⛔ THE VERDICT COMES FROM THE LAST ATTEMPT, and nothing said so: pinning it
// to the first left the suite green. Here the mirror fails, then apt fails for
// a reason this does not recognise — so the closing sentence must be the
// second one, and must not blame the network.
func TestTheVerdictComesFromTheLastAttempt(t *testing.T) {
	f := &fake{answers: []answer{
		{output: "Temporary failure resolving 'azure.archive.ubuntu.com'\n", status: 100},
		{output: "E: dpkg was interrupted\n", status: 100},
	}}
	f.install(t)
	code, _, errb := drive(t, "--need", "pdftoppm", "--retries", "1", "poppler-utils")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb, "said nothing this recognises") {
		t.Errorf("the verdict is not the last attempt's: %q", errb)
	}
	if strings.Contains(errb, "this is the network, not the package") {
		t.Errorf("it blamed the network for a failure that was not one: %q", errb)
	}
}

// ⛔ AND IT DOES NOT CLAIM A FALLBACK IT NEVER TRIED. The sentence said
// "including the canonical archive" whatever had happened, so with the
// fallback off it described an attempt that was never made.
func TestTheCanonicalArchiveIsNotClaimedWhenItWasNotTried(t *testing.T) {
	down := answer{output: "Temporary failure resolving 'azure.archive.ubuntu.com'\n", status: 100}
	f := &fake{answers: []answer{down}}
	f.install(t)
	code, _, errb := drive(t, "--need", "pdftoppm", "--retries", "1",
		"--fallback-mirror=false", "poppler-utils")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb, "the canonical archive was not tried") {
		t.Errorf("it claimed an untried fallback: %q", errb)
	}
}

// ⛔ THE MIRRORLIST IS NOT REWRITTEN ON THE LAST ATTEMPT. Doing so changes the
// runner permanently, for every later step of the job, and tries nothing with
// it — a side effect with no measurement attached.
func TestTheMirrorlistIsNotRewrittenWithNoAttemptLeftToUseIt(t *testing.T) {
	f := &fake{answers: []answer{
		{output: "Temporary failure resolving 'azure.archive.ubuntu.com'\n", status: 100}}}
	f.install(t)
	code, _, errb := drive(t, "--need", "pdftoppm", "--retries", "0", "poppler-utils")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	if len(f.written) != 0 {
		t.Errorf("it rewrote %v with no attempt left to use it", f.written)
	}
	// ⛔ AND THE VERDICT SAYS SO. Checking only `written` left reason()'s new
	// argument undefended: passing `fallback` instead of `swapped` kept the
	// suite green and brought back the claim of a fallback never made.
	if !strings.Contains(errb, "the canonical archive was not tried") {
		t.Errorf("it claimed a fallback it never made: %q", errb)
	}
}

func TestANegativeRetryCountIsRefused(t *testing.T) {
	f := &fake{}
	f.install(t)
	code, _, errb := drive(t, "--need", "pdftoppm", "--retries", "-1", "poppler-utils")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if len(f.calls) != 0 {
		t.Errorf("it ran %v", f.calls)
	}
	if !strings.Contains(errb, "cannot be -1") {
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

// ⛔⛔ AND IT USED TO PASS FOR THE WRONG REASON. With `--retries 0` the
// rewrite is blocked anyway by `attempt < retries`, so the flag under test
// decided nothing: review removed `fallback &&` from the condition and this
// test stayed green. The retries are 2 now, which is a run where the
// mirrorlist WOULD be rewritten if the flag were on — and the companion
// assertion below says so, because a test that only checks a negative cannot
// tell "the flag worked" from "the case never arose".
func TestTheFallbackCanBeTurnedOff(t *testing.T) {
	outage := answer{output: "Could not resolve 'azure.archive.ubuntu.com'\n", status: 100}
	off := &fake{answers: []answer{outage, outage, outage}}
	off.install(t)
	code, _, _ := drive(t, "--need", "pdftoppm", "--retries", "2",
		"--fallback-mirror=false", "poppler-utils")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	if len(off.written) != 0 {
		t.Errorf("it rewrote %v although the fallback was off", off.written)
	}

	on := &fake{answers: []answer{outage, outage, outage}}
	on.install(t)
	if code, _, _ := drive(t, "--need", "pdftoppm", "--retries", "2", "poppler-utils"); code != 1 {
		t.Fatalf("control: exit %d", code)
	}
	if on.written[mirrorList] != canonicalMirror {
		t.Fatalf("the control did not rewrite the mirrorlist, so the case above "+
			"proves nothing about --fallback-mirror: %v", on.written)
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
