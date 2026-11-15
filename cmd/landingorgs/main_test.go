package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

func TestParsePagesJoinsPages(t *testing.T) {
	got, err := parsePages(strings.NewReader(`[{"login":"b"},{"login":"a"}][{"login":"c"}]`))
	if err != nil || strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := parsePages(strings.NewReader(`[]`)); err == nil {
		t.Fatal("an empty answer is an unread one")
	}
	if _, err := parsePages(strings.NewReader(`{"message":"Bad credentials"}`)); err == nil {
		t.Fatal("an error object is not a list of organisations")
	}
}

func TestFromCache(t *testing.T) {
	in := `{"repos":[{"org":"b","name":"b.github.io"},{"org":"a","name":"a.github.io"},
	{"org":"c","name":"c.github.io","archived":true},{"org":"d","name":"other"}]}`
	got, err := fromCache(strings.NewReader(in))
	if err != nil || strings.Join(got, ",") != "a,b" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := fromCache(strings.NewReader(`{"repos":[]}`)); err == nil {
		t.Fatal("an empty inventory must be refused")
	}
}

func TestLiveReadsTheAPIAndAFailureIsNotAnEmptyList(t *testing.T) {
	old := ghOrgs
	defer func() { ghOrgs = old }()
	ghOrgs = func() ([]byte, error) { return []byte(`[{"login":"go-blas"}]`), nil }
	var out, errOut bytes.Buffer
	if code := cli.Drive(RootCmd(), nil, &out, &errOut); code != 0 || out.String() != "go-blas\n" {
		t.Fatalf("code %d out %q err %q", code, out.String(), errOut.String())
	}
	ghOrgs = func() ([]byte, error) { return nil, errors.New("403") }
	out.Reset()
	if code := cli.Drive(RootCmd(), nil, &out, &errOut); code != 2 || out.Len() != 0 {
		t.Fatalf("an unread API must exit 2 and print nothing, got %d %q", code, out.String())
	}
}

func TestTheCacheIsSaidLoudly(t *testing.T) {
	var out, errOut bytes.Buffer
	cli.Drive(RootCmd(), []string{"--fleetfind", "/nonexistent.json"}, &out, &errOut)
	if !strings.Contains(errOut.String(), "READING A CACHE") {
		t.Fatalf("the cache read was not announced: %q", errOut.String())
	}
}
