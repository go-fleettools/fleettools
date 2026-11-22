// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The --json document. It is built on the SAME readings the text report uses
// (hitLines for the recurrence sentences, markOf for the fingerprint, triage for
// the exit status); a second parser would be a second opinion about what counts
// as a recurrence.

type jsonDoc struct {
	Lessons    []jsonLesson     `json:"lessons"`
	Totals     jsonTotals       `json:"totals"`
	Unreadable []jsonUnreadable `json:"unreadable"`
}

type jsonLesson struct {
	Name            string           `json:"name"`
	OriginSessionID string           `json:"originSessionId"`
	Modified        string           `json:"modified"`
	Recurrences     []jsonRecurrence `json:"recurrences"`
	Ledger          jsonLedger       `json:"ledger"`
	// FingerprintMatches is null when there is nothing to compare: no ledger
	// entry, or an entry written before fingerprints existed. False means the
	// lesson repeated after it was accounted for.
	FingerprintMatches *bool `json:"fingerprintMatches"`
}

type jsonRecurrence struct {
	// Date is YYYY-MM-DD, or null when the sentence and its section heading
	// state none. It is never guessed.
	Date *string `json:"date"`
	// YearInferred is true when the text gave day and month only and the year
	// is the one in the memory's frontmatter `modified` stamp (the latest such
	// date not after it). A consumer that wants only stated dates filters on it.
	YearInferred bool   `json:"yearInferred,omitempty"`
	Line         string `json:"line"`
}

type jsonLedger struct {
	// Decision is gate, pending, no-gate, untriaged (backlog), none (no entry),
	// or unknown (a state word this binary does not know).
	Decision string `json:"decision"`
	Text     string `json:"text"`
	// Entry is the id --replaces must carry to replace this entry. ⛔ It is not
	// Fingerprint: that one is the MEMORY's, shared by every session deciding
	// about the same memory, so two sessions' different decisions carry the same
	// Fingerprint and different Entry ids. Empty when there is no entry.
	Entry       string         `json:"entry"`
	WrittenAt   *string        `json:"writtenAt"`
	Fingerprint string         `json:"fingerprint"`
	Downgrade   *jsonDowngrade `json:"downgrade,omitempty"`
}

type jsonDowngrade struct {
	From   string `json:"from"`
	Reason string `json:"reason"`
}

type jsonTotals struct {
	Lessons               int            `json:"lessons"`
	Decisions             map[string]int `json:"decisions"`
	Recurrences           int            `json:"recurrences"`
	RecurrencesDated      int            `json:"recurrencesDated"`
	RecurrencesUndated    int            `json:"recurrencesUndated"`
	RepeatedAfterAccepted int            `json:"repeatedAfterAccounted"`
	Unreadable            int            `json:"unreadable"`
}

type jsonUnreadable struct {
	File  string `json:"file"`
	Error string `json:"error"`
}

var decisionOf = map[string]string{"gate": "gate", "pending": "pending", "no-gate": "no-gate", "backlog": "untriaged"}

const downgradeMark = " ⛔ DOWNGRADED from "

func jsonReport(out, errOut io.Writer, dir string, known map[string]entry) int {
	ents, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintln(errOut, "recur:", err)
		return 2
	}
	doc := jsonDoc{Lessons: []jsonLesson{}, Unreadable: []jsonUnreadable{}}
	doc.Totals.Decisions = map[string]int{"gate": 0, "pending": 0, "no-gate": 0, "untriaged": 0, "none": 0, "unknown": 0}
	var found []recorded
	for _, de := range ents {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".md") || name == ledgerName || name == "MEMORY.md" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			doc.Unreadable = append(doc.Unreadable, jsonUnreadable{name, err.Error()})
			continue
		}
		hits := hitLines(b, dir, name)
		if len(hits) == 0 {
			continue
		}
		lesson := strings.TrimSuffix(name, ".md")
		mark := markOf(hits)
		found = append(found, recorded{lesson, mark})
		org, err := readOrigin(dir, lesson)
		if err != nil {
			doc.Unreadable = append(doc.Unreadable, jsonUnreadable{name, err.Error()})
			continue
		}
		yearFrom := time.Time{}
		if org.stated {
			yearFrom = org.modified
		}
		l := jsonLesson{
			Name:            lesson,
			OriginSessionID: org.session,
			Modified:        org.modified.UTC().Format(time.RFC3339),
			Recurrences:     []jsonRecurrence{},
		}
		lines := strings.Split(string(b), "\n")
		for _, h := range hits {
			r := jsonRecurrence{Line: h.text}
			if d, inferred, ok := dateOfHit(lines, h.idx, yearFrom); ok {
				r.Date, r.YearInferred = &d, inferred
				doc.Totals.RecurrencesDated++
			} else {
				doc.Totals.RecurrencesUndated++
			}
			l.Recurrences = append(l.Recurrences, r)
		}
		doc.Totals.Recurrences += len(hits)

		e, ok := known[lesson]
		switch {
		case !ok:
			l.Ledger = jsonLedger{Decision: "none"}
		default:
			dec := decisionOf[e.state]
			if dec == "" {
				dec = "unknown"
			}
			l.Ledger = jsonLedger{Decision: dec, Text: e.detail, Entry: entryID(e), Fingerprint: e.mark}
			if e.when != "" {
				w := e.when
				l.Ledger.WrittenAt = &w
			}
			if i := strings.Index(e.detail, downgradeMark); i >= 0 {
				from, reason, _ := strings.Cut(e.detail[i+len(downgradeMark):], ": ")
				l.Ledger.Downgrade = &jsonDowngrade{From: from, Reason: reason}
			}
			if e.mark != "" {
				m := e.mark == mark
				l.FingerprintMatches = &m
				if !m {
					doc.Totals.RepeatedAfterAccepted++
				}
			}
		}
		doc.Totals.Decisions[l.Ledger.Decision]++
		doc.Lessons = append(doc.Lessons, l)
	}
	sort.Slice(doc.Lessons, func(i, j int) bool { return doc.Lessons[i].Name < doc.Lessons[j].Name })
	sort.Slice(doc.Unreadable, func(i, j int) bool { return doc.Unreadable[i].File < doc.Unreadable[j].File })
	doc.Totals.Lessons = len(doc.Lessons)
	doc.Totals.Unreadable = len(doc.Unreadable)

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		fmt.Fprintln(errOut, "recur:", err)
		return 2
	}
	// Same verdicts as the text report; and a blind scan is never a clean one.
	if len(doc.Unreadable) > 0 {
		return 2
	}
	if open, _, _ := triage(known, found); len(open) > 0 {
		return 1
	}
	return 0
}

// dateOfHit dates the sentence at lines[idx]: its own text first, else the
// nearest heading above it. Nothing stated, nothing returned.
func dateOfHit(lines []string, idx int, modified time.Time) (string, bool, bool) {
	if d, inf, ok := parseDate(lines[idx], modified); ok {
		return d, inf, true
	}
	for i := idx - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); strings.HasPrefix(t, "#") {
			return parseDate(t, modified)
		}
	}
	return "", false, false
}

var monthNames = map[string]int{
	"janvier": 1, "février": 2, "fevrier": 2, "mars": 3, "avril": 4, "mai": 5, "juin": 6,
	"juillet": 7, "août": 8, "aout": 8, "septembre": 9, "octobre": 10, "novembre": 11,
	"décembre": 12, "decembre": 12,
	"january": 1, "february": 2, "march": 3, "april": 4, "may": 5, "june": 6,
	"july": 7, "august": 8, "september": 9, "october": 10,
	"november": 11, "december": 12,
}

var monthAlt = func() string {
	var ns []string
	for n := range monthNames {
		ns = append(ns, n)
	}
	sort.Strings(ns)
	return strings.Join(ns, "|")
}()

var (
	// 2026-11-18
	isoRe = regexp.MustCompile(`(?:^|[^\d])(\d{4})-(\d{2})-(\d{2})(?:[^\d]|$)`)
	// 18-11-2026, 18/11/2026
	dmyRe = regexp.MustCompile(`(?:^|[^\d./-])(\d{1,2})[-/](\d{1,2})[-/](\d{4})(?:[^\d]|$)`)
	// le 18 novembre [2026], 18 November 2026, 1er mai
	dMonRe = regexp.MustCompile(`(?i)(?:^|[^\p{L}\d])(\d{1,2})(?:er|st|nd|rd|th)?\s+(` + monthAlt + `)(?:\s+(\d{4}))?(?:[^\p{L}\d]|$)`)
	// November 18[, 2026]
	monDRe = regexp.MustCompile(`(?i)(?:^|[^\p{L}\d])(` + monthAlt + `)\s+(\d{1,2})(?:st|nd|rd|th)?(?:,?\s+(\d{4}))?(?:[^\p{L}\d]|$)`)
	// 18-11, 18/11: two digits each, so "2-3 fois" is not a date
	dmRe = regexp.MustCompile(`(?:^|[^\d./-])(\d{2})[-/](\d{2})(?:[^\d./-]|$)`)
)

// parseDate returns the first date a text states, as YYYY-MM-DD.
//
// ⛔ It never guesses. A form with no year (18-11, le 18 novembre) takes its
// year from `modified` ONLY when the memory's frontmatter stated one (a zero
// time here means it did not): the latest such date not after the stamp, and
// inferred is then true so the consumer can drop it. A date that does not exist
// (31-02), or that would fall after `modified`, is not returned.
func parseDate(s string, modified time.Time) (date string, inferred, ok bool) {
	type cand struct {
		pos     int
		y, m, d int
		yearOK  bool
	}
	var best *cand
	offer := func(c cand) {
		if best == nil || c.pos < best.pos {
			best = &c
		}
	}
	num := func(x string) int { n, _ := strconv.Atoi(x); return n }
	for _, m := range isoRe.FindAllStringSubmatchIndex(s, -1) {
		offer(cand{m[0], num(s[m[2]:m[3]]), num(s[m[4]:m[5]]), num(s[m[6]:m[7]]), true})
	}
	for _, m := range dmyRe.FindAllStringSubmatchIndex(s, -1) {
		offer(cand{m[0], num(s[m[6]:m[7]]), num(s[m[4]:m[5]]), num(s[m[2]:m[3]]), true})
	}
	for _, m := range dMonRe.FindAllStringSubmatchIndex(s, -1) {
		c := cand{pos: m[0], d: num(s[m[2]:m[3]]), m: monthNames[strings.ToLower(s[m[4]:m[5]])]}
		if m[6] >= 0 {
			c.y, c.yearOK = num(s[m[6]:m[7]]), true
		}
		offer(c)
	}
	for _, m := range monDRe.FindAllStringSubmatchIndex(s, -1) {
		c := cand{pos: m[0], m: monthNames[strings.ToLower(s[m[2]:m[3]])], d: num(s[m[4]:m[5]])}
		if m[6] >= 0 {
			c.y, c.yearOK = num(s[m[6]:m[7]]), true
		}
		offer(c)
	}
	for _, m := range dmRe.FindAllStringSubmatchIndex(s, -1) {
		offer(cand{pos: m[0], d: num(s[m[2]:m[3]]), m: num(s[m[4]:m[5]])})
	}
	if best == nil {
		return "", false, false
	}
	c := *best
	valid := func(y int) bool {
		t := time.Date(y, time.Month(c.m), c.d, 0, 0, 0, 0, time.UTC)
		return y >= 1 && y <= 9999 && t.Year() == y && int(t.Month()) == c.m && t.Day() == c.d
	}
	if c.yearOK {
		if !valid(c.y) {
			return "", false, false
		}
		return fmt.Sprintf("%04d-%02d-%02d", c.y, c.m, c.d), false, true
	}
	if modified.IsZero() {
		return "", false, false
	}
	end := modified.UTC().Truncate(24 * time.Hour)
	for y := end.Year(); y >= end.Year()-1; y-- {
		if !valid(y) {
			continue
		}
		if t := time.Date(y, time.Month(c.m), c.d, 0, 0, 0, 0, time.UTC); !t.After(end) {
			return fmt.Sprintf("%04d-%02d-%02d", y, c.m, c.d), true, true
		}
	}
	return "", false, false
}
