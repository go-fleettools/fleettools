package main

import (
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

// drive runs the command over an input string and returns its output and status.
func drive(t *testing.T, in string, args ...string) (string, int) {
	t.Helper()
	c := RootCmd()
	var out, errOut strings.Builder
	c.SetIn(strings.NewReader(in))
	code := cli.Drive(c, args, &out, &errOut)
	return out.String() + errOut.String(), code
}

// A serial package: three top-level tests, one of them with subtests.
const serial = `
{"Action":"run","Package":"ex/p","Test":"TestSlow"}
{"Action":"pass","Package":"ex/p","Test":"TestSlow/a","Elapsed":3}
{"Action":"pass","Package":"ex/p","Test":"TestSlow/b","Elapsed":2}
{"Action":"pass","Package":"ex/p","Test":"TestSlow","Elapsed":5}
{"Action":"pass","Package":"ex/p","Test":"TestMiddle","Elapsed":3}
{"Action":"pass","Package":"ex/p","Test":"TestQuick","Elapsed":2}
{"Action":"pass","Package":"ex/p","Elapsed":10}
`

// ⛔⛔ A PARENT'S Elapsed ALREADY CONTAINS ITS SUBTESTS, and adding every Test
// event up double-counts every table-driven test in the package. Above, the
// three top-level tests are 5+3+2 = 10 s and the package is 10 s; counting the
// subtests too would make 15 s of tests in a 10 s package and print shares over
// 100%, which is the shape of the mistake rather than a rounding error.
func TestASubtestIsNotCountedTwice(t *testing.T) {
	got, code := drive(t, serial)
	if code != 0 {
		t.Fatalf("exit %d, want 0: %s", code, got)
	}
	if !strings.Contains(got, "tests total 10.00s") {
		t.Errorf("the top-level tests must total 10.00s, not 15: subtests are inside "+
			"their parent's elapsed.\n%s", got)
	}
	if strings.Contains(got, "TestSlow/a") {
		t.Errorf("a subtest appeared in the top-level list:\n%s", got)
	}
	for _, want := range []string{"5.00s   50.0%  TestSlow", "3.00s   30.0%  TestMiddle"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// ⛔ SLOWEST FIRST, because the question is which tests account for the clock and
// an alphabetical answer makes the reader do the sorting the tool exists to do.
func TestTheSlowestComesFirst(t *testing.T) {
	got, _ := drive(t, serial)
	slow := strings.Index(got, "TestSlow")
	mid := strings.Index(got, "TestMiddle")
	quick := strings.Index(got, "TestQuick")
	if !(slow < mid && mid < quick) {
		t.Errorf("order is not slowest-first (%d, %d, %d):\n%s", slow, mid, quick, got)
	}
}

// ⛔⛔ PARALLEL TESTS OVERLAP, SO THEIR SUM IS NOT THE CLOCK. Here four seconds of
// tests ran inside a two-second package, which only parallelism explains. A
// percentage of that package would read 200% in total; the report says
// "PARALLEL" and prints both numbers instead, because a share of a clock several
// tests occupied at once is not a share of anything.
func TestParallelOverlapIsSaidRatherThanDivided(t *testing.T) {
	const parallel = `
{"Action":"pass","Package":"ex/q","Test":"TestA","Elapsed":2}
{"Action":"pass","Package":"ex/q","Test":"TestB","Elapsed":2}
{"Action":"pass","Package":"ex/q","Elapsed":2}
`
	got, code := drive(t, parallel)
	if code != 0 {
		t.Fatalf("exit %d, want 0: %s", code, got)
	}
	if !strings.Contains(got, "PARALLEL") {
		t.Errorf("4s of tests in a 2s package is parallel and must say so:\n%s", got)
	}
	if strings.Contains(got, "%") {
		t.Errorf("a share was printed for a parallel run:\n%s", got)
	}
	// ⛔ And the control: the serial fixture must still print shares, or this
	// test would pass for a tool that never prints them at all.
	ser, _ := drive(t, serial)
	if !strings.Contains(ser, "%") {
		t.Errorf("the serial package must still get shares:\n%s", ser)
	}
}

// ⛔ A REPORT OVER NOTHING MUST NOT LOOK LIKE A CLEAN ONE. Exit 2 is the fleet's
// "could not answer", and the two ways to get here — an empty stream and a file
// of something else entirely — are both it.
func TestInputWithNoTestEventsIsRefused(t *testing.T) {
	for _, c := range []struct{ name, in string }{
		{"empty", ""},
		{"not go test -json", "hello\nworld\n"},
		{"package events only", `{"Action":"pass","Package":"ex/p","Elapsed":1}` + "\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, code := drive(t, c.in)
			if code != 2 {
				t.Errorf("exit %d, want 2 for %q:\n%s", code, c.name, got)
			}
		})
	}
}

// ⛔ A RUN WITHOUT A PACKAGE ELAPSED STILL REPORTS, and says why there are no
// shares. `go test -json` of a single test, or a truncated log, has the test
// events and not the package's own line; refusing there would throw away the
// ordering, which is most of the answer.
func TestWithoutAPackageElapsedTheOrderingSurvives(t *testing.T) {
	const noPkg = `
{"Action":"pass","Package":"ex/r","Test":"TestB","Elapsed":1}
{"Action":"pass","Package":"ex/r","Test":"TestA","Elapsed":4}
`
	got, code := drive(t, noPkg)
	if code != 0 {
		t.Fatalf("exit %d, want 0: %s", code, got)
	}
	if !strings.Contains(got, "shares omitted") {
		t.Errorf("want the missing package elapsed said out loud:\n%s", got)
	}
	if strings.Index(got, "TestA") > strings.Index(got, "TestB") {
		t.Errorf("slowest-first must survive a missing package elapsed:\n%s", got)
	}
}

// --subtests answers a DIFFERENT question — where the slowest test's time went —
// and must never mix those rows into the top-level column, since a parent's
// elapsed already contains them.
func TestSubtestsListTheSlowestParentsOwnRows(t *testing.T) {
	got, code := drive(t, serial, "--subtests")
	if code != 0 {
		t.Fatalf("exit %d, want 0: %s", code, got)
	}
	if !strings.Contains(got, "TestSlow  5.00s, by subtest:") {
		t.Errorf("want the slowest parent named with its own elapsed:\n%s", got)
	}
	if !strings.Contains(got, "3.00s  a") || !strings.Contains(got, "2.00s  b") {
		t.Errorf("want both subtests, slowest first:\n%s", got)
	}
	if strings.Contains(got, "TestMiddle") {
		t.Errorf("--subtests must not also print the top-level list:\n%s", got)
	}
}

// ⛔ --top BOUNDS THE LIST AND SAYS WHAT IT LEFT OUT. A truncated report that did
// not say it was truncated is the shape of "the scan reported zero".
func TestTopBoundsTheListAndSaysSo(t *testing.T) {
	got, code := drive(t, serial, "--top", "1")
	if code != 0 {
		t.Fatalf("exit %d, want 0: %s", code, got)
	}
	if !strings.Contains(got, "TestSlow") {
		t.Errorf("the slowest must survive --top 1:\n%s", got)
	}
	if !strings.Contains(got, "… 2 more") {
		t.Errorf("want the count of what was left out:\n%s", got)
	}
}

// Two packages in one stream stay apart, and in the order they were first seen —
// a report whose package order moved between runs could not be diffed.
func TestPackagesStayApartAndInOrder(t *testing.T) {
	const two = `
{"Action":"pass","Package":"ex/first","Test":"TestX","Elapsed":1}
{"Action":"pass","Package":"ex/first","Elapsed":1}
{"Action":"pass","Package":"ex/second","Test":"TestY","Elapsed":1}
{"Action":"pass","Package":"ex/second","Elapsed":1}
`
	got, code := drive(t, two)
	if code != 0 {
		t.Fatalf("exit %d, want 0: %s", code, got)
	}
	if strings.Index(got, "ex/first") > strings.Index(got, "ex/second") {
		t.Errorf("packages must keep the order they were first seen in:\n%s", got)
	}
}

// A failed or skipped test costs wall clock too, so it is counted. Only its
// outcome event carries Elapsed; "run" and "output" do not.
func TestAFailedOrSkippedTestIsCountedToo(t *testing.T) {
	const mixed = `
{"Action":"run","Package":"ex/s","Test":"TestFail"}
{"Action":"fail","Package":"ex/s","Test":"TestFail","Elapsed":3}
{"Action":"skip","Package":"ex/s","Test":"TestSkip","Elapsed":1}
{"Action":"fail","Package":"ex/s","Elapsed":4}
`
	got, code := drive(t, mixed)
	if code != 0 {
		t.Fatalf("exit %d, want 0: %s", code, got)
	}
	if !strings.Contains(got, "tests total 4.00s") {
		t.Errorf("a failed and a skipped test both cost time:\n%s", got)
	}
}
