package rollout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The ledger's directory cannot be made when a plain file is in its way: the
// record must say so, not go on to a write that would fail for another reason.
func TestRecordFailsWhenTheLedgerDirectoryCannotBeMade(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := Ledger{Path: filepath.Join(blocker, "sub", "ledger.txt"), Now: time.Now}
	if err := l.Record("o/r#1"); err == nil || !strings.Contains(err.Error(), "mkdir") {
		t.Fatalf("want the mkdir failure, got %v", err)
	}
}
