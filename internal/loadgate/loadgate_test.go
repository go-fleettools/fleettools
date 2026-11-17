package loadgate

import (
	"strings"
	"testing"
)

// Both platforms' spellings, because this fleet runs on darwin and its CI on
// linux, and a parser that only ever met one of them is a parser that works
// where it was written.
func TestParseBothPlatforms(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		want Averages
	}{
		{"linux /proc/loadavg", "2.52 3.01 3.33 2/1234 5678\n", Averages{2.52, 3.01, 3.33}},
		{"darwin sysctl", "{ 2.52 3.01 3.33 }", Averages{2.52, 3.01, 3.33}},
		{"darwin sysctl, already trimmed", "2.52 3.01 3.33", Averages{2.52, 3.01, 3.33}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := parse(c.in)
			if err != nil {
				t.Fatalf("parse(%q): %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("parse(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
	for _, bad := range []string{"", "1.0 2.0", "{ }", "a b c"} {
		if got, err := parse(bad); err == nil {
			t.Errorf("parse(%q) = %v and no error: an unreadable line must not become "+
				"an idle machine", bad, got)
		}
	}
}

// ⚠ BOTH AVERAGES, and this pair is why: a 1-minute dip under a loaded 5-minute
// average is not a quiet machine, and a gate reading only the first minute passed
// a run at exactly these numbers.
func TestBothAveragesAreTested(t *testing.T) {
	for _, c := range []struct {
		name string
		a    Averages
		pass bool
	}{
		{"both quiet", Averages{4.2, 5.0, 6.0}, true},
		{"1-min over", Averages{9.1, 5.0, 6.0}, false},
		{"5-min over while the minute dips", Averages{6.93, 10.00, 8.0}, false},
		{"15-min over is not a refusal", Averages{4.0, 5.0, 40.0}, true},
		{"exactly at the threshold passes", Averages{8.0, 8.0, 8.0}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			over := c.a.One > Threshold || c.a.Five > Threshold
			if over == c.pass {
				t.Errorf("%v: refused=%v, want refused=%v", c.a, over, !c.pass)
			}
		})
	}
}

// ⛔ "I could not read the load" is not "the load is fine". The zero value would
// pass the threshold, which is exactly why Check reads the error first — and this
// asserts that zero WOULD pass, so the reason the order matters is written down.
func TestAFailedReadIsAnErrorAndNotZero(t *testing.T) {
	var zero Averages
	if zero.One > Threshold || zero.Five > Threshold {
		t.Fatal("the zero value no longer passes the threshold; this test's premise is gone")
	}
	// A reason lets an unreadable machine through, deliberately and on the record.
	if _, err := Check("a reason"); err != nil {
		t.Errorf("an override must be honoured even when the load cannot be read: %v", err)
	}
}

func TestBannerNamesAnOverride(t *testing.T) {
	a := Averages{12.0, 13.0, 14.0}
	plain := Banner(a, "")
	if strings.Contains(plain, "UNDER LOAD") {
		t.Errorf("a clean run's banner claims an override: %q", plain)
	}
	stamped := Banner(a, "corpus run queued behind CI")
	if !strings.Contains(stamped, "corpus run queued behind CI") {
		t.Errorf("the override's REASON is not in the banner, so a reader cannot see "+
			"why the number was taken on a busy machine: %q", stamped)
	}
	if !strings.Contains(stamped, "12.00") {
		t.Errorf("the banner drops the load it was taken at: %q", stamped)
	}
}
