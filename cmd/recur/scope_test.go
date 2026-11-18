// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	ownerSession = "cae751b5-607d-4d15-a96a-50d738a07218"
	otherSession = "7ba8e34e-0000-4000-8000-000000000001"
)

var scopeNow = time.Date(2026, 11, 18, 22, 0, 0, 0, time.UTC)

// lesson is a memory recording a recurrence, with the frontmatter mem-add writes.
func lesson(origin, modified string) string {
	fm := "---\nname: x\nmetadata:\n  type: feedback\n"
	if origin != "" {
		fm += "  originSessionId: " + origin + "\n"
	}
	if modified != "" {
		fm += "  modified: " + modified + "\n"
	}
	return fm + "---\n\nTroisième récidive.\n"
}

func hookAs(t *testing.T, dir, session string, grace time.Duration) (denied bool, out, errOut string) {
	t.Helper()
	old := clock
	clock = func() time.Time { return scopeNow }
	t.Cleanup(func() { clock = old })
	payload := `{"session_id":"` + session + `","tool_input":{"command":"gitpush -u origin topic"}}`
	if session == "" {
		payload = `{"tool_input":{"command":"gitpush -u origin topic"}}`
	}
	var o, e bytes.Buffer
	if code := hookMode(&o, &e, opts{memory: dir, grace: grace}, strings.NewReader(payload)); code != 0 {
		t.Fatalf("code %d", code)
	}
	return strings.Contains(o.String(), `"permissionDecision":"deny"`), o.String(), e.String()
}

func stamp(ago time.Duration) string {
	return scopeNow.Add(-ago).Format("2006-01-02T15:04:05.000Z07:00")
}

// The two sides of the boundary, on one young unaccounted recurrence.
func TestAYoungRecurrenceRefusesItsOwnerAndOnlyWarnsOthers(t *testing.T) {
	dir := mem(t, map[string]string{"grep-c.md": lesson(ownerSession, stamp(10*time.Minute))})

	if denied, _, _ := hookAs(t, dir, ownerSession, time.Hour); !denied {
		t.Error("the session that wrote the recurrence must be refused at once")
	}

	denied, out, errOut := hookAs(t, dir, otherSession, time.Hour)
	if denied {
		t.Fatalf("another session was refused a 10-minute-old lesson inside the grace: %s", out)
	}
	for _, text := range []string{errOut, out} {
		for _, want := range []string{"grep-c", "cae751b5"} {
			if !strings.Contains(text, want) {
				t.Errorf("the warning must name %q; got %q", want, text)
			}
		}
	}
}

func TestAnOldRecurrenceRefusesEverySession(t *testing.T) {
	dir := mem(t, map[string]string{"grep-c.md": lesson(ownerSession, stamp(61*time.Minute))})
	for _, s := range []string{ownerSession, otherSession} {
		if denied, _, _ := hookAs(t, dir, s, time.Hour); !denied {
			t.Errorf("session %s allowed past a recurrence unaccounted for 61 minutes", s)
		}
	}
}

// What the hook cannot read refuses everyone, as before.
func TestWhatTheScopeCannotReadRefusesEveryone(t *testing.T) {
	young := stamp(5 * time.Minute)
	for name, body := range map[string]string{
		"no originSessionId": lesson("", young),
		"no frontmatter":     "Troisième récidive.\n",
	} {
		dir := mem(t, map[string]string{"x.md": body})
		if err := os.Chtimes(filepath.Join(dir, "x.md"), scopeNow, scopeNow); err != nil {
			t.Fatal(err)
		}
		if denied, _, _ := hookAs(t, dir, otherSession, time.Hour); !denied {
			t.Errorf("%s: a stranger was allowed past a lesson with no owner on record", name)
		}
	}
	dir := mem(t, map[string]string{"x.md": lesson(ownerSession, young)})
	if denied, _, _ := hookAs(t, dir, "", time.Hour); !denied {
		t.Error("a payload with no session_id was allowed: the hook cannot tell whether it is the owner")
	}
	if denied, _, _ := hookAs(t, dir, otherSession, 0); !denied {
		t.Error("--grace 0 must refuse everyone, as before")
	}
	if denied, _, _ := hookAs(t, dir, otherSession, -time.Minute); !denied {
		t.Error("a negative grace must refuse everyone")
	}
}

func TestAnAgeThatCannotBeReadRefusesEveryone(t *testing.T) {
	refuse, warn := scopeByOwner([]finding{{"x", "w"}}, opts{memory: "m", grace: time.Hour}, otherSession, scopeNow,
		func(dir, l string) (origin, error) { return origin{}, errors.New("stat failed") })
	if len(refuse) != 1 || len(warn) != 0 {
		t.Fatalf("refuse=%v warn=%v", refuse, warn)
	}
}

// With no `modified`, the file's mtime is the age, on both sides of the grace.
func TestTheMtimeStandsInForAMissingModified(t *testing.T) {
	dir := mem(t, map[string]string{"x.md": lesson(ownerSession, "")})
	p := filepath.Join(dir, "x.md")
	if err := os.Chtimes(p, scopeNow.Add(-5*time.Minute), scopeNow.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if denied, _, _ := hookAs(t, dir, otherSession, time.Hour); denied {
		t.Error("a 5-minute-old mtime inside the grace refused a stranger")
	}
	if err := os.Chtimes(p, scopeNow.Add(-2*time.Hour), scopeNow.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if denied, _, _ := hookAs(t, dir, otherSession, time.Hour); !denied {
		t.Error("a 2-hour-old mtime past the grace allowed a stranger")
	}
}

// One young foreign lesson and one old one: the old one still refuses.
func TestAnOldLessonRefusesEvenBesideAYoungForeignOne(t *testing.T) {
	dir := mem(t, map[string]string{
		"young.md": lesson(ownerSession, stamp(1*time.Minute)),
		"old.md":   lesson(ownerSession, stamp(3*time.Hour)),
	})
	denied, out, _ := hookAs(t, dir, otherSession, time.Hour)
	if !denied || !strings.Contains(out, "old") || strings.Contains(out, "young ") {
		t.Fatalf("denied=%v out=%s", denied, out)
	}
}

func TestTheDefaultGraceIsSixtyMinutesAndConfigurable(t *testing.T) {
	f := RootCmd().Flags().Lookup("grace")
	if f == nil || f.DefValue != "1h0m0s" {
		t.Fatalf("grace flag = %+v", f)
	}
}
