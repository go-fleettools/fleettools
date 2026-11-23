package rollout

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ledgerAt(t *testing.T, now time.Time) Ledger {
	t.Helper()
	return Ledger{Path: filepath.Join(t.TempDir(), "sub", "ledger.txt"), Now: func() time.Time { return now }}
}

func TestMissingLedgerIsZero(t *testing.T) {
	if n, err := ledgerAt(t, time.Now()).InLastHour(); err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
}

func TestOnlyTheLastHourCountsAndTheBoundaryIsOut(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	l := ledgerAt(t, now)
	body := fmt.Sprintf("%d a\n\n%d b\n%d c\n%d d\n", now.Add(-59*time.Minute).Unix(), now.Add(-time.Hour).Unix(),
		now.Add(-3*time.Hour).Unix(), now.Unix())
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.Path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, err := l.InLastHour(); err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
}

func TestAnUnreadableLedgerIsAnErrorNotZero(t *testing.T) {
	l := ledgerAt(t, time.Now())
	if err := os.MkdirAll(l.Path, 0o755); err != nil { // a directory
		t.Fatal(err)
	}
	if _, err := l.InLastHour(); err == nil {
		t.Fatal("a directory read as an empty ledger")
	}
	os.RemoveAll(l.Path)
	if err := os.WriteFile(l.Path, []byte("later x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.InLastHour(); err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("err %v", err)
	}
}

func TestRecordAppendsAndCreatesTheDirectory(t *testing.T) {
	l := ledgerAt(t, time.Unix(1_800_000_000, 0))
	for _, s := range []string{"a/b#1", "a/b#2"} {
		if err := l.Record(s); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(l.Path)
	if string(b) != "1800000000 a/b#1\n1800000000 a/b#2\n" {
		t.Fatalf("%q", b)
	}
	if n, err := l.InLastHour(); err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	// a path under a file cannot be created
	bad := Ledger{Path: filepath.Join(l.Path, "x", "y"), Now: l.Now}
	if bad.Record("z") == nil {
		t.Fatal("recorded under a file")
	}
}

func fakeGH(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

func TestBudget(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for name, tc := range map[string]struct {
		gh, ledger, want string
		reserve, max     int
		code             int
	}{
		"ok":         {gh: `echo '{"resources":{"core":{"remaining":5000}}}'`, reserve: 1000, max: 2},
		"at reserve": {gh: `echo '{"resources":{"core":{"remaining":1000}}}'`, reserve: 1000, max: 2},
		"below":      {gh: `echo '{"resources":{"core":{"remaining":999}}}'`, reserve: 1000, max: 2, code: 2, want: "budget reserve: 999 left, 1000 reserved"},
		"failed":     {gh: `echo no >&2; exit 1`, reserve: 1, max: 2, code: 2, want: "could not read the rate limit"},
		"garbage":    {gh: `echo '<html>'`, reserve: 1, max: 2, code: 2, want: "answer was unreadable"},
		"no field":   {gh: `echo '{"resources":{"core":{}}}'`, reserve: 0, max: 2, code: 2, want: "answer was unreadable"},
		"cap":        {gh: `echo '{"resources":{"core":{"remaining":5000}}}'`, ledger: fmt.Sprintf("%d a\n%d b\n", now.Unix(), now.Unix()), reserve: 1, max: 2, code: 2, want: "hourly cap: 2 merged in the last hour, cap 2"},
		"under cap":  {gh: `echo '{"resources":{"core":{"remaining":5000}}}'`, ledger: fmt.Sprintf("%d a\n", now.Unix()), reserve: 1, max: 2},
		"bad ledger": {gh: `echo '{"resources":{"core":{"remaining":5000}}}'`, ledger: "x y\n", reserve: 1, max: 2, code: 2, want: "unreadable"},
	} {
		fakeGH(t, tc.gh)
		l := ledgerAt(t, now)
		if tc.ledger != "" {
			os.MkdirAll(filepath.Dir(l.Path), 0o755)
			os.WriteFile(l.Path, []byte(tc.ledger), 0o644)
		}
		var errOut bytes.Buffer
		if code := Budget(&errOut, "tool", l, tc.reserve, tc.max); code != tc.code || !strings.Contains(errOut.String(), tc.want) {
			t.Errorf("%s: code %d: %s", name, code, errOut.String())
		}
		if tc.want != "" && !strings.HasPrefix(errOut.String(), "tool: ") {
			t.Errorf("%s: not named: %s", name, errOut.String())
		}
	}
}

func TestHeadIs(t *testing.T) {
	oldE, oldT := HeadPollEvery, HeadPollTries
	HeadPollEvery, HeadPollTries = 0, 3
	defer func() { HeadPollEvery, HeadPollTries = oldE, oldT }()

	counter := filepath.Join(t.TempDir(), "n")
	// the head moves on the third read
	fakeGH(t, `n=$(cat `+counter+` 2>/dev/null || echo 0); n=$((n+1)); echo $n > `+counter+`
if [ $n -ge 3 ]; then echo '{"headRefOid":"new"}'; else echo '{"headRefOid":"old"}'; fi`)
	if err := HeadIs("o/r", "5", "new"); err != nil {
		t.Fatal(err)
	}
	fakeGH(t, `echo '{"headRefOid":"old"}'`)
	if err := HeadIs("o/r", "5", "new"); err == nil || !strings.Contains(err.Error(), "has not moved to the pushed commit") {
		t.Fatalf("err %v", err)
	}
	fakeGH(t, `echo '<html>'`)
	if err := HeadIs("o/r", "5", "new"); err == nil {
		t.Fatal("garbage read as the head")
	}
	fakeGH(t, `echo down >&2; exit 1`)
	if err := HeadIs("o/r", "5", "new"); err == nil || !strings.Contains(err.Error(), "down") {
		t.Fatalf("err %v", err)
	}
}

func TestCallCarriesTheProgramsWords(t *testing.T) {
	fakeGH(t, `echo hello; echo oops >&2; exit 3`)
	out, err := Call("", "gh", "a", "b")
	if err == nil || !strings.Contains(err.Error(), "gh a b") || !strings.Contains(err.Error(), "oops") || !strings.Contains(out, "hello") {
		t.Fatalf("%q %v", out, err)
	}
	dir := t.TempDir()
	fakeGH(t, `pwd`)
	if out, err := Call(dir, "gh"); err != nil || !strings.Contains(out, filepath.Base(dir)) {
		t.Fatalf("%q %v", out, err)
	}
}
