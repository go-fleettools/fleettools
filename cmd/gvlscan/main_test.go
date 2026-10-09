package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fixture is a tiny module the tool is pointed at. Every expectation below is
// something I know the answer to by construction, which is the point: a scanner
// run first against real code cannot tell "found nothing" from "read nothing".
const fixture = `package sut

import (
	"bytes"
	"net"
	"time"
)

type VM struct{}

func ioBlock(vm *VM, fn func())            { fn() }
func (vm *VM) threadBlock(fn func())       { fn() }

// WRAPPED: the read is inside the closure handed to ioBlock.
func wrappedRead(vm *VM, c net.Conn, b []byte) (int, error) {
	var n int
	var err error
	ioBlock(vm, func() { n, err = c.Read(b) })
	return n, err
}

// UNWRAPPED: the same call, nothing around it. This is go-embedded-ruby#771.
func unwrappedRead(c net.Conn, b []byte) (int, error) {
	return c.Read(b)
}

// UNWRAPPED, method form.
func (vm *VM) unwrappedMethodRead(c net.Conn, b []byte) {
	vm.threadBlock(func() { _, _ = c.Write(b) })
	_, _ = c.Read(b)
}

// NOT BLOCKING: identical spelling, a type that never parks the thread. A
// text-matching tool reports this; that is why the tool resolves types.
func bufferRead(r *bytes.Reader, b []byte) (int, error) {
	return r.Read(b)
}

// UNWRAPPED package function.
func nap() { time.Sleep(time.Second) }

// WRAPPED package function.
func wrappedNap(vm *VM) { ioBlock(vm, func() { time.Sleep(time.Second) }) }
`

func buildTool(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "gvlscan")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build gvlscan: %v\n%s", err, out)
	}
	return bin
}

func writeFixture(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module sut\n\ngo 1.27.1\n")
	write("sut.go", body)
	return dir
}

func run(t *testing.T, bin, dir string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, append(args, "./...")...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run gvlscan: %v\n%s", err, out)
	}
	return string(out), code
}

// TestItSeparatesTheWrappedFromTheUnwrapped is the whole claim: four blocking
// calls, two wrapped, two not, and one lookalike that must not be counted at
// all.
func TestItSeparatesTheWrappedFromTheUnwrapped(t *testing.T) {
	bin := buildTool(t)
	out, code := run(t, bin, writeFixture(t, fixture))
	if code != 1 {
		t.Errorf("exit = %d, want 1 (something is unwrapped)\n%s", code, out)
	}
	for _, want := range []string{
		"net.Conn.Read waits on", // unwrappedRead
		"time.Sleep waits on",    // nap
		"in unwrappedRead",       //
		"in (*VM).unwrappedMethodRead",
		"in nap",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// 5 blocking calls: 2 wrapped (ioBlock read, threadBlock write, wrapped
	// sleep = 3 wrapped), 3 unwrapped (read, method read, sleep).
	if !strings.Contains(out, "3 wrapped, 3 NOT wrapped") {
		t.Errorf("want the census line to read 3 wrapped, 3 NOT wrapped:\n%s", out)
	}
}

// TestALookalikeIsNotReported: *bytes.Reader.Read is spelled exactly like
// net.Conn.Read and never blocks. If this ever fails, the tool has fallen back
// to matching text and its findings are worthless.
func TestALookalikeIsNotReported(t *testing.T) {
	bin := buildTool(t)
	out, _ := run(t, bin, writeFixture(t, fixture))
	if strings.Contains(out, "bufferRead") {
		t.Errorf("reported a *bytes.Reader read, which cannot block:\n%s", out)
	}
}

// TestCleanCodeExitsZero: the negative side. Without it, a tool that reported
// everything would pass the test above.
func TestCleanCodeExitsZero(t *testing.T) {
	bin := buildTool(t)
	clean := `package sut

import (
	"bytes"
	"net"
)

type VM struct{}

func ioBlock(vm *VM, fn func()) { fn() }

func ok(vm *VM, c net.Conn, b []byte) { ioBlock(vm, func() { _, _ = c.Read(b) }) }
func harmless(r *bytes.Reader, b []byte) (int, error) { return r.Read(b) }
`
	out, code := run(t, bin, writeFixture(t, clean))
	if code != 0 {
		t.Errorf("exit = %d, want 0 for code with nothing unwrapped\n%s", code, out)
	}
	if !strings.Contains(out, "0 NOT wrapped") {
		t.Errorf("want 0 NOT wrapped:\n%s", out)
	}
}

// TestCodeThatDoesNotCompileIsRefused is the one that matters most. A scanner
// whose input failed to type-check finds nothing and prints a reassuring zero,
// which is indistinguishable from clean code -- the exact shape that had me
// report a green fleet off a scan that could not read. It must refuse.
func TestCodeThatDoesNotCompileIsRefused(t *testing.T) {
	bin := buildTool(t)
	broken := `package sut

import "net"

func oops(c net.Conn, b []byte) { c.Read(b) ; this is not go }
`
	out, code := run(t, bin, writeFixture(t, broken))
	if code != 2 {
		t.Errorf("exit = %d, want 2 (refused to answer)\n%s", code, out)
	}
	if strings.Contains(out, "0 NOT wrapped") {
		t.Errorf("reported a clean result for code that does not compile:\n%s", out)
	}
}

// TestTheCensusLineNamesThePopulation: "3 unwrapped" alone cannot be told from
// a scan that read one file out of two hundred.
func TestTheCensusLineNamesThePopulation(t *testing.T) {
	bin := buildTool(t)
	out, _ := run(t, bin, writeFixture(t, fixture))
	if !strings.Contains(out, "package(s) scanned") {
		t.Errorf("census line does not say how much was scanned:\n%s", out)
	}
}

// TestKindsNarrowsTheQuestion: a real run returned 29 unwrapped calls of which
// twelve were short internal mutex locks, and the four unbounded network waits
// were lost among them. The kind has to be selectable, and `sync` has to be off
// by default, or the tool answers a question nobody asked.
func TestKindsNarrowsTheQuestion(t *testing.T) {
	bin := buildTool(t)
	src := `package sut

import (
	"net"
	"sync"
	"time"
)

var mu sync.Mutex

func a(c net.Conn, b []byte) { _, _ = c.Read(b) }
func b2()                    { mu.Lock() }
func c3()                    { time.Sleep(time.Second) }
`
	dir := writeFixture(t, src)

	out, _ := run(t, bin, dir)
	if !strings.Contains(out, "2 blocking call(s) found") {
		t.Errorf("by default sync must be excluded, leaving the net read and the sleep:\n%s", out)
	}
	if strings.Contains(out, "sync.Mutex") {
		t.Errorf("sync reported although it is off by default:\n%s", out)
	}

	out, _ = run(t, bin, dir, "-kinds", "net")
	if !strings.Contains(out, "1 blocking call(s) found") || strings.Contains(out, "time.Sleep") {
		t.Errorf("-kinds net must leave only the net read:\n%s", out)
	}

	out, _ = run(t, bin, dir, "-kinds", "sync")
	if !strings.Contains(out, "sync.Mutex.Lock") {
		t.Errorf("-kinds sync must be able to ask for them:\n%s", out)
	}
}

// TestAnEmptyKindsIsRefused: selecting nothing would scan the whole tree and
// print "0 NOT wrapped", which reads exactly like a clean result.
func TestAnEmptyKindsIsRefused(t *testing.T) {
	bin := buildTool(t)
	out, code := run(t, bin, writeFixture(t, fixture), "-kinds", "nosuchkind")
	if code != 2 {
		t.Errorf("exit = %d, want 2\n%s", code, out)
	}
	if strings.Contains(out, "NOT wrapped") {
		t.Errorf("printed a census for a question that selects nothing:\n%s", out)
	}
}

// TestWrappersAreConfigurable: the names are specific to one codebase, so a
// tool that hardcoded them would be usable in exactly one repository.
func TestWrappersAreConfigurable(t *testing.T) {
	bin := buildTool(t)
	dir := writeFixture(t, fixture)
	out, code := run(t, bin, dir, "-wrappers", "nothingMatchesThis")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	// With no wrapper recognised, every blocking call is unwrapped.
	if !strings.Contains(out, "0 wrapped, 6 NOT wrapped") {
		t.Errorf("want all 6 unwrapped when no wrapper name matches:\n%s", out)
	}
}
