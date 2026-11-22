package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

// drive runs the command and returns its exit status with everything it printed.
func drive(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := cli.Drive(rootCmd(), args, &out, &errb)
	return code, out.String() + errb.String()
}

// tree makes a directory holding one subdirectory per name, plus a regular file,
// which is NOT a command and must not be counted.
func tree(t *testing.T, names ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cmd")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.Mkdir(filepath.Join(dir, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("not a command\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func doc(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "page.md")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAListThatMatchesTheTree(t *testing.T) {
	dir := tree(t, "abbench", "brief", "recur")
	page := doc(t, "# p\n\n```text\nabbench brief recur\n```\n")
	code, got := drive(t, "--tree", dir, "--marker", "abbench", page)
	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, got)
	}
	if !strings.Contains(got, "every name agrees") {
		t.Errorf("no agreement line:\n%s", got)
	}
	// ⛔ A regular file in cmd/ is not a command, and counting it would make an
	// agreeing page look stale.
	if !strings.Contains(got, "3 subdirectories") {
		t.Errorf("README.md was counted as a command:\n%s", got)
	}
}

// ⛔ The founding case: the tree grew and the page did not.
func TestANameInTheTreeThatNobodyWroteDown(t *testing.T) {
	dir := tree(t, "abbench", "brief", "recur", "secretscan")
	page := doc(t, "```text\nabbench brief recur\n```\n")
	code, got := drive(t, "--tree", dir, "--marker", "abbench", page)
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, got)
	}
	if !strings.Contains(got, "NOT written down (1): secretscan") {
		t.Errorf("the missing name is not named:\n%s", got)
	}
	// The fix must be a paste, not a transcription.
	if !strings.Contains(got, "to paste:\n\nabbench brief recur secretscan") {
		t.Errorf("the tree's own names are not printed ready to paste:\n%s", got)
	}
}

func TestANameWrittenDownThatTheTreeNoLongerHas(t *testing.T) {
	dir := tree(t, "abbench", "brief")
	page := doc(t, "```text\nabbench brief oldtool\n```\n")
	code, got := drive(t, "--tree", dir, "--marker", "abbench", page)
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, got)
	}
	if !strings.Contains(got, "NOT in the tree (1): oldtool") {
		t.Errorf("the stale name is not named:\n%s", got)
	}
}

// ⛔ A count that is right while the list is wrong, or the other way round, is
// how one of these pages rots: the prose and the block are two claims.
func TestTheCountIsCheckedApartFromTheList(t *testing.T) {
	dir := tree(t, "abbench", "brief", "recur")
	page := doc(t, "```text\nabbench brief recur\n```\n")

	code, got := drive(t, "--tree", dir, "--marker", "abbench", "--count", "102", page)
	if code != 1 {
		t.Fatalf("a wrong count exited %d, want 1:\n%s", code, got)
	}
	if !strings.Contains(got, "--count 102, but the tree holds 3") {
		t.Errorf("the count is not contradicted in both numbers:\n%s", got)
	}

	if code, got := drive(t, "--tree", dir, "--marker", "abbench", "--count", "3", page); code != 0 {
		t.Errorf("the right count exited %d, want 0:\n%s", code, got)
	}
}

// ⛔ An UNREAD list must never read as an agreeing one. This is the whole
// difference between this program and a grep: a marker that matches nothing is
// exit 2, not exit 0.
func TestAMarkerThatFindsNoBlockIsUnanswerable(t *testing.T) {
	dir := tree(t, "abbench")
	page := doc(t, "# p\n\nno block here, only prose naming abbench.\n")
	code, got := drive(t, "--tree", dir, "--marker", "abbench", page)
	if code != 2 {
		t.Fatalf("exit %d, want 2:\n%s", code, got)
	}
	if !strings.Contains(got, "no fenced block holds the token") {
		t.Errorf("the refusal does not say what was looked for:\n%s", got)
	}
}

// ⛔ And a marker in TWO blocks is also unanswerable: picking either would be
// picking by position, which is what --marker exists to avoid.
func TestAMarkerInTwoBlocksIsUnanswerable(t *testing.T) {
	dir := tree(t, "abbench", "brief")
	page := doc(t, "```bash\nabbench --help\n```\n\n```text\nabbench brief\n```\n")
	code, got := drive(t, "--tree", dir, "--marker", "abbench", page)
	if code != 2 {
		t.Fatalf("exit %d, want 2:\n%s", code, got)
	}
	if !strings.Contains(got, "2 fenced blocks hold the token") {
		t.Errorf("the refusal does not say how many:\n%s", got)
	}
}

// ⛔ The case that made --block necessary, found by running this command on the
// real page: a document that DOCUMENTS listcheck holds the marker twice, once in
// the list and once in the example command. Nothing but the kind of block tells
// them apart — position would, and position is what --marker exists to avoid.
func TestABlockKindTellsTheListFromTheExampleThatDocumentsIt(t *testing.T) {
	dir := tree(t, "abbench", "brief")
	page := doc(t, "```bash\nlistcheck --tree cmd --marker abbench page.md\n```\n\n```text\nabbench brief\n```\n")

	code, got := drive(t, "--tree", dir, "--marker", "abbench", page)
	if code != 2 {
		t.Fatalf("without --block, exit %d, want 2:\n%s", code, got)
	}
	if !strings.Contains(got, "(`bash`, `text`)") || !strings.Contains(got, "--block") {
		t.Errorf("the refusal names neither the kinds it found nor the flag that resolves it:\n%s", got)
	}

	if code, got := drive(t, "--tree", dir, "--marker", "abbench", "--block", "text", page); code != 0 {
		t.Fatalf("with --block text, exit %d, want 0:\n%s", code, got)
	}
}

// The info string is the first word, so a block carrying attributes is still of
// its kind.
func TestTheInfoStringIsTheFirstWord(t *testing.T) {
	got := fenced("```text {linenos=false}\nabbench\n```\n")
	if len(got) != 1 || got[0].lang != "text" {
		t.Fatalf("fenced = %+v, want one `text` block", got)
	}
}

// A block is found by what it HOLDS, so an example block above it may be added
// or removed without changing which block is the list.
func TestTheBlockIsFoundByWhatItHoldsNotByPosition(t *testing.T) {
	dir := tree(t, "abbench", "brief")
	page := doc(t, "```bash\nlistcheck --tree cmd page.md\n```\n\n```go\nfmt.Println(1)\n```\n\n```text\nabbench brief\n```\n")
	if code, got := drive(t, "--tree", dir, "--marker", "brief", page); code != 0 {
		t.Fatalf("exit %d, want 0 — the third block is the list:\n%s", code, got)
	}
}

func TestSkipLeavesADirectoryOut(t *testing.T) {
	dir := tree(t, "abbench", "brief", "internaltool")
	page := doc(t, "```text\nabbench brief\n```\n")
	code, got := drive(t, "--tree", dir, "--marker", "abbench", "--skip", "internaltool", page)
	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, got)
	}
	if !strings.Contains(got, "(1 skipped)") {
		t.Errorf("the skip is not stated, so the count cannot be checked by a reader:\n%s", got)
	}
}

// ⛔ A flag with no default and no value refuses BEFORE reading anything: the
// answer to "which tree?" cannot be guessed, and a guess here is a verdict
// about the wrong tree — which is how the first recount of cmd/ answered 112.
func TestTheRequiredFlagsRefuseBeforeReading(t *testing.T) {
	page := doc(t, "```text\nabbench\n```\n")
	for _, c := range []struct {
		name, want string
		args       []string
	}{
		{"no tree", "--tree is required", []string{"--marker", "abbench", page}},
		{"no marker", "--marker is required", []string{"--tree", ".", page}},
		{"no file", "name at least one Markdown file", []string{"--tree", ".", "--marker", "abbench"}},
	} {
		code, got := drive(t, c.args...)
		if code != 2 {
			t.Errorf("%s: exit %d, want 2:\n%s", c.name, code, got)
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: the refusal does not say what is missing: %q", c.name, got)
		}
	}
}

func TestATreeThatCannotBeReadIsUnanswerable(t *testing.T) {
	page := doc(t, "```text\nabbench\n```\n")
	code, got := drive(t, "--tree", filepath.Join(t.TempDir(), "nope"), "--marker", "abbench", page)
	if code != 2 {
		t.Fatalf("exit %d, want 2:\n%s", code, got)
	}
}

func TestWrapKeepsTheWidth(t *testing.T) {
	got := wrap([]string{"aaa", "bbb", "ccc", "ddd"}, 8)
	if got != "aaa bbb\nccc ddd" {
		t.Errorf("wrap = %q", got)
	}
	// One name longer than the width is still on a line of its own, never cut.
	if got := wrap([]string{"abcdefghij", "k"}, 4); got != "abcdefghij\nk" {
		t.Errorf("a name longer than the width came out as %q", got)
	}
}

// An unterminated fence at the end of a file still holds names someone wrote.
func TestAnUnterminatedBlockIsStillABlock(t *testing.T) {
	dir := tree(t, "abbench", "brief")
	page := doc(t, "# p\n\n```text\nabbench brief\n")
	if code, got := drive(t, "--tree", dir, "--marker", "abbench", page); code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, got)
	}
}
