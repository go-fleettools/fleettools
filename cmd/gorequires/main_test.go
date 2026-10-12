package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequiresReadsDirectAndIndirect(t *testing.T) {
	gm := []byte("module github.com/go-images/jpeg2000\n\ngo 1.27\n\nrequire github.com/ajroetker/go-highway v0.0.12\n\nrequire golang.org/x/sys v0.41.0 // indirect\n")
	h, ok, err := Requires(gm, "github.com/ajroetker/go-highway")
	if err != nil || !ok || h.Version != "v0.0.12" || h.Indirect || h.Module != "github.com/go-images/jpeg2000" {
		t.Fatalf("%+v %v %v", h, ok, err)
	}
	h, ok, _ = Requires(gm, "golang.org/x/sys")
	if !ok || !h.Indirect {
		t.Fatalf("indirect: %+v", h)
	}
	if _, ok, _ := Requires(gm, "github.com/ajroetker/go-highway/hwy"); ok {
		t.Fatal("a package path below the module is not the module")
	}
	if _, _, err := Requires([]byte("module m\nrequire (\n"), "x"); err == nil {
		t.Fatal("an unparsable go.mod is an error, not a 'no'")
	}
}

func inv(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "inventory.json")
	src := `{"taken":"2026-10-10T21:15:51+02:00","orgs":2,"repos":[
	 {"org":"go-images","name":"jpeg2000"},
	 {"org":"go-widgets","name":"toolkit"},
	 {"org":"go-widgets","name":"brand"},
	 {"org":"go-widgets","name":"old","archived":true},
	 {"org":"go-widgets","name":"flaky"}]}`
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func fake(org, repo string) ([]byte, error) {
	switch org + "/" + repo {
	case "go-images/jpeg2000":
		return []byte("module github.com/go-images/jpeg2000\nrequire github.com/ajroetker/go-highway v0.0.12\n"), nil
	case "go-widgets/toolkit":
		return []byte("module github.com/go-widgets/toolkit\nrequire github.com/ajroetker/go-highway v0.0.11 // indirect\n"), nil
	case "go-widgets/brand":
		return nil, errNoGoMod
	case "go-widgets/old":
		panic("an archived repository must not be fetched without -archived")
	}
	return nil, errors.New("HTTP 503")
}

func TestRunReportsHitsAndRefusesAnUnreadOne(t *testing.T) {
	var out, errb bytes.Buffer
	o := options{module: "github.com/ajroetker/go-highway", inventory: inv(t), jobs: 2}
	if rc := run(o, &out, &errb, fake); rc != 2 {
		t.Fatalf("an unread repository must fail the run: rc %d", rc)
	}
	s := out.String()
	if !strings.Contains(s, "go-images/jpeg2000") || !strings.Contains(s, "go-widgets/toolkit") ||
		!strings.Contains(s, "4 repositories (inventory of 2026-10-10 21:15), 2 root go.mod read, 1 answered 404 (no root go.mod, or private), 1 UNREAD: 2 require") {
		t.Fatal(s)
	}
	if !strings.Contains(errb.String(), "unread: go-widgets/flaky: HTTP 503") {
		t.Fatal(errb.String())
	}
	out.Reset()
	o.version = "v0.0.12"
	run(o, &out, &errb, fake)
	if strings.Contains(out.String(), "go-widgets/toolkit") || !strings.Contains(out.String(), "1 require github.com/ajroetker/go-highway@v0.0.12") {
		t.Fatal("-version filters:", out.String())
	}
}

func TestRunWithoutAnInventoryFails(t *testing.T) {
	var out, errb bytes.Buffer
	if rc := run(options{module: "m", inventory: filepath.Join(t.TempDir(), "none.json"), jobs: 1}, &out, &errb, fake); rc != 2 {
		t.Fatalf("rc %d", rc)
	}
	empty := filepath.Join(t.TempDir(), "e.json")
	os.WriteFile(empty, []byte(`{"repos":[]}`), 0o644)
	if rc := run(options{module: "m", inventory: empty, jobs: 1}, &out, &errb, fake); rc != 2 {
		t.Fatalf("an empty inventory is not 'nobody requires it': rc %d", rc)
	}
}

func TestParseArgs(t *testing.T) {
	for _, c := range []struct {
		args    []string
		code    int
		proceed bool
	}{
		{[]string{"-h"}, 0, false},
		{nil, 2, false},
		{[]string{"-module", "m", "extra"}, 2, false},
		{[]string{"-module", "m", "-j", "0"}, 2, false},
		{[]string{"-module", "m"}, 0, true},
	} {
		var errb bytes.Buffer
		if _, code, proceed := parseArgs(c.args, &errb); code != c.code || proceed != c.proceed {
			t.Errorf("%q: got (%d, %v)", c.args, code, proceed)
		}
	}
}
