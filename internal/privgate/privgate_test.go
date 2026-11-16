package privgate

import (
	"strings"
	"testing"
)

// The stamp is appended, not substituted, and reads as a sentence.
func TestStampReads(t *testing.T) {
	got := stamp("  because the path is the defect  ")
	if !strings.HasPrefix(got, "⚠ ") || !strings.HasSuffix(got, "because the path is the defect") {
		t.Errorf("stamp = %q", got)
	}
}

const private = "see `/Users/alicia/Documents/thesis/main.tex`\n"

func TestBodyCleanPassesUnchanged(t *testing.T) {
	got, err := Body("a clean body", "")
	if err != nil || got != "a clean body" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestBodyRefusesWithoutWaiverAndStampsWith(t *testing.T) {
	if _, err := Body(private, ""); err == nil || strings.Contains(err.Error(), "alicia") {
		t.Fatalf("want a refusal that does not reprint the path, got %v", err)
	}
	got, err := Body(private, "the path is the subject")
	if err != nil || !strings.Contains(got, "the path is the subject") || !strings.Contains(got, "alicia") {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestTitle(t *testing.T) {
	if err := Title("a clean title", ""); err != nil {
		t.Fatal(err)
	}
	if err := Title("crash in /Users/alicia/x/y.tex", ""); err == nil {
		t.Fatal("a private title passed")
	}
	err := Title("crash in /Users/alicia/x/y.tex", "why")
	if err == nil || !strings.Contains(err.Error(), "does not cover a title") || strings.Contains(err.Error(), "alicia") {
		t.Fatalf("the waiver covered a title or the refusal leaked: %v", err)
	}
}

func TestRefusalTruncatesAfterEightFindings(t *testing.T) {
	text := strings.Repeat("/Users/alicia/Documents/a/b.tex\n", 12)
	_, err := Body(text, "")
	if err == nil || !strings.Contains(err.Error(), "and ") || !strings.Contains(err.Error(), "more") {
		t.Fatalf("want a truncated list, got %v", err)
	}
}
