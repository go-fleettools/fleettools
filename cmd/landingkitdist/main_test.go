package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

func TestSummarise(t *testing.T) {
	es := []entry{
		{Org: "a", State: "kit", Kit: "v0.9.0"},
		{Org: "b", State: "kit", Kit: "v0.38.0"},
		{Org: "c", State: "kit", Kit: "v0.10.0"},
		{Org: "d", State: "copied", Note: "x"},
	}
	d, behind, other := summarise(es, "v0.38.0")
	if d["v0.9.0"] != 1 || d["v0.38.0"] != 1 {
		t.Fatal(d)
	}
	if len(behind) != 2 || behind[0] != "a" || behind[1] != "c" {
		t.Fatalf("v0.9.0 and v0.10.0 are both below v0.38.0, numerically: %v", behind)
	}
	if len(other) != 1 {
		t.Fatal(other)
	}
	if !less("v0.9.0", "v0.10.0") {
		t.Fatal("numeric, not lexical")
	}
}

func TestCommandWritesBothFilesAndRefusesAnEmptyInventory(t *testing.T) {
	dir := t.TempDir()
	inv := filepath.Join(dir, "i.json")
	os.WriteFile(inv, []byte(`{"orgs":[{"org":"a","state":"kit","kit":"v0.1.0"},{"org":"b","state":"copied","note":"n"}]}`), 0o644)
	var out, errOut bytes.Buffer
	b, o := filepath.Join(dir, "b.txt"), filepath.Join(dir, "o.txt")
	if code := cli.Drive(RootCmd(), []string{inv, "v0.2.0", b, o}, &out, &errOut); code != 0 {
		t.Fatalf("%d %s", code, errOut.String())
	}
	if got, _ := os.ReadFile(b); string(got) != "a\n" {
		t.Fatalf("behind: %q", got)
	}
	if got, _ := os.ReadFile(o); !strings.HasPrefix(string(got), "b copied") {
		t.Fatalf("other: %q", got)
	}
	os.WriteFile(inv, []byte(`{"orgs":[]}`), 0o644)
	if code := cli.Drive(RootCmd(), []string{inv, "v0.2.0", b, o}, &out, &errOut); code != 2 {
		t.Fatalf("an empty inventory is unanswerable, got %d", code)
	}
}
