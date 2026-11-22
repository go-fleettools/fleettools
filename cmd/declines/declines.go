// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

// Staging module for go-fleettools/fleettools cmd/declines: the lines a tool
// prints when it DID NOT do something, surfaced out of an output file nobody
// reads whole.
//
// ⛔ The mistake it exists for: `buildscan --dir <clone> --vet` exited 0 and
// its summary said "go vet passed under all 2", while line 2 of 20 said
// "no record: the working tree has uncommitted changes". I read `tail -6`,
// saw the summary, and wrote that the tool had said nothing. A decline is
// printed where the decision is TAKEN — near the top — and the summary at the
// bottom is about what the run FOUND, so the two never meet in a tail.
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
)

// declineWords are the words a program uses to say it did not do something.
// Lower case; a line matches when it contains one, case-insensitively.
//
// ⛔ Kept TIGHT on purpose. A vocabulary that also catches "error", "failed" or
// "warning" turns every build log into a report, and a report that fires on
// every run is read as noise — which is the state this tool exists to leave.
// Each entry below was taken from a line a fleet tool actually prints.
var declineWords = []string{
	"no record",
	"not counted",
	"not judged",
	"not read",
	"never read",
	"allowing",
	"skipped",
	"skipping",
	// ⛔ NOT "declin": the stem matches this tool's OWN name, so every
	// `go test ./cmd/declines` line fired. A vocabulary that matches the
	// reporter is a reporter that always reports. (Caught by
	// TestOrdinaryOutputIsQuiet before this left the staging module.)
	"declined",
	"declining to",
	"nothing to",
	"could not read",
	"cannot read",
	"unreadable",
	"not published",
	"waived",
	"not measured",
	"stopped at the budget",
	"disproved nothing",
}

// maxLineBytes bounds one line of a log: an assembly dump or a minified blob
// can be a single enormous line, and a hook must not read it into memory.
var maxLineBytes = 1 << 20

// maxFileBytes bounds one file. A var so a test can drive the edge.
var maxFileBytes int64 = 64 << 20

// hit is one declining line.
type hit struct {
	File string
	Line int
	Text string
}

// scan returns the declining lines of r, and the number of lines it read. The
// count is returned so a caller can tell "no declines" from "nothing read":
// an empty file otherwise reads as a clean run, which is the same mistake in
// another costume.
// ⛔ A bufio.Scanner, not a ReadString loop. The loop needed an `if err != nil`
// whose deletion did not fail a test — it HUNG, because ReadString keeps
// returning the same error forever once nobody reads it. A hang is a worse
// verdict than a survivor: it answers nothing after five minutes. The Scanner
// puts the error AFTER the loop, where a test can assert on it.
func scan(name string, r io.Reader, words []string) ([]hit, int, error) {
	sc := bufio.NewScanner(r)
	// ⛔ The INITIAL buffer has to be bounded too, not just the maximum:
	// bufio.Scanner only consults maxTokenSize when it must GROW, so a 64 KiB
	// starting buffer swallowed a line far past an 8-byte bound and returned
	// it with a nil error. Caught by TestScanRefusesAnEnormousLine.
	bufSize := min(max(maxLineBytes, 1), 64<<10)
	sc.Buffer(make([]byte, 0, bufSize), maxLineBytes)
	var out []hit
	n := 0
	total := int64(0)
	// ⛔ One reused fold buffer, and sc.Bytes() rather than sc.Text(): the
	// first version allocated TWICE PER LINE (the string copy and
	// strings.ToLower), which measured 188,071 allocations over an 8 MiB log —
	// for a hook that runs after every Bash call in about twenty-five
	// sessions. Allocation counts are exact under load even when times are
	// not, which is why this was fixable before the machine went quiet.
	fold := make([]byte, 0, 4096)
	needles := wordBytes(words)
	for sc.Scan() {
		line := sc.Bytes()
		n++
		total += int64(len(line)) + 1
		if total > maxFileBytes {
			return out, n, fmt.Errorf("%s: more than the %d bytes this reads", name, maxFileBytes)
		}
		fold = appendLower(fold[:0], line)
		if w := matchesBytes(fold, needles); w != "" {
			out = append(out, hit{File: name, Line: n, Text: strings.TrimRight(string(line), "\r")})
		}
	}
	if err := sc.Err(); err != nil {
		// ⛔ Reported WITH what was read: a partial read must never come back
		// looking like a clean file. A line past maxLineBytes arrives here as
		// bufio.ErrTooLong, which is a refusal and not a silent truncation.
		return out, n, fmt.Errorf("%s:%d: %w", name, n, err)
	}
	return out, n, nil
}

// matches returns the word that made the line a decline, or "". It allocates,
// so scan() uses matchesBytes with a reused buffer; this is the readable form
// and the one the tests drive.
func matches(line string, words []string) string {
	return matchesBytes(appendLower(nil, []byte(line)), wordBytes(words))
}

// matchesBytes returns the word found in an ALREADY FOLDED line, or "".
//
// ⛔ The needles arrive as bytes, built once per file by wordBytes: written as
// `bytes.Contains(low, []byte(w))` it allocated a needle PER WORD PER LINE,
// which is eighteen allocations a line in a tool whose whole job is to read
// logs.
func matchesBytes(low []byte, words [][]byte) string {
	for _, w := range words {
		if bytes.Contains(low, w) {
			return string(w)
		}
	}
	return ""
}

// wordBytes turns the vocabulary into needles, once.
func wordBytes(words []string) [][]byte {
	out := make([][]byte, 0, len(words))
	for _, w := range words {
		out = append(out, []byte(w))
	}
	return out
}

// appendLower appends line to dst with ASCII upper case folded down.
//
// ⛔ ASCII only, on purpose, and it is a limit rather than an oversight: every
// decline word is English ASCII, and a Unicode fold would have to allocate for
// the cases where the folded form is a different length. A line's non-ASCII
// bytes are copied untouched, so a UTF-8 log is read, just not case-folded
// outside ASCII — and a log written in another encoding cannot make this
// mismatch a crash, because nothing here indexes beyond the slice.
func appendLower(dst, line []byte) []byte {
	for _, c := range line {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		dst = append(dst, c)
	}
	return dst
}

// scanFile reads one file under the size bound.
//
// ⛔ There is no os.Stat here, and that is deliberate. A stat-then-read pair
// needs two nil checks whose deletion can only ever crash, which a mutation
// sweep reports as "caught by PANIC" — luck, not a test. The bound is enforced
// on the bytes actually read, which is also the truthful place for it: a log
// being appended to grows between the stat and the read. A directory, a device
// or a socket is refused by the read error it produces, with the path in it.
func scanFile(path string, words []string) ([]hit, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	return scan(path, f, words)
}
