package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

func TestLastRecordIsTheState(t *testing.T) {
	m := last([]string{
		`{"org":"a","status":"open","pr":1}`,
		`{"org":"a","status":"merged","pr":1}`,
		`{"org":"b","status":"red"}`,
		`garbage`,
	})
	if len(m) != 2 || m["a"].Status != "merged" || m["b"].Status != "red" {
		t.Fatal(m)
	}
}

func TestCommandListsTheNotMergedAndRefusesAnEmptyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(p, []byte("{\"org\":\"a\",\"status\":\"merged\"}\n{\"org\":\"b\",\"status\":\"red\",\"pr\":7,\"detail\":\"FAIL x\"}\n"), 0o644)
	var out, errOut bytes.Buffer
	if code := cli.Drive(RootCmd(), []string{p}, &out, &errOut); code != 0 {
		t.Fatalf("%d %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "red\tb\t#7\tFAIL x") || !strings.Contains(out.String(), "total    2") {
		t.Fatal(out.String())
	}
	os.WriteFile(p, []byte("garbage\n"), 0o644)
	if code := cli.Drive(RootCmd(), []string{p}, &out, &errOut); code != 2 {
		t.Fatalf("no readable record is unanswerable, got %d", code)
	}
}
