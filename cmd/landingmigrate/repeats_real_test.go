package main

import (
	"os"
	"strings"
	"testing"
)

// Two real non-ruby landings, vendored verbatim with their commits
// (testdata/repeats-*): go-approx at 17271549beab09425ff2a0f5a2b79abe4cadf831
// and go-ansible at 27e218222cf42d8f88cfe7d12b9e029f6b36a74b, layouts/index.html
// as published on 2026-11-15. `landingkit check` failed both on repeats of the
// strip: footers, and feature cards ("Pure Go, CGO=0", "BSD-3-Clause").
func TestRepeatsRealNonRubyLandings(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	approx, frags := repeats(read("testdata/repeats-approx/index.html"))
	kinds := map[string]int{}
	for _, f := range frags {
		if f.Hidden {
			kinds[f.Where]++
		}
	}
	for _, k := range []string{"card", "footer"} {
		if kinds[k] == 0 {
			t.Errorf("go-approx: no hidden %s (%v)", k, kinds)
		}
	}
	// one case per repeat kind
	for _, want := range []string{
		ifOff + `<div class="card"><div class="ico">📦</div><h3>Pure Go, CGO=0</h3>`,
		ifOff + `<div class="card"><div class="ico">⚖️</div><h3>BSD-3-Clause</h3>`,
		ifOff + ` · BSD-3-Clause` + end, // the footer's licence segment
	} {
		if !strings.Contains(approx, want) {
			t.Errorf("go-approx lacks %q", want)
		}
	}
	// a card about something else is kept
	if strings.Contains(approx, ifOff+`<div class="card"><div class="ico">📉</div>`) {
		t.Error("a card about fits was hidden")
	}
	ansible, _ := repeats(read("testdata/repeats-ansible/index.html"))
	if !strings.Contains(ansible, ifOff) || strings.Count(ansible, `<div class="card">`) < 10 {
		t.Error("go-ansible")
	}
	if i := strings.Index(ansible, "<footer"); i < 0 || !strings.Contains(ansible[i:], ifOff) {
		t.Errorf("go-ansible's footer repeats Pure Go and was left: %s", ansible[max(i, 0):])
	}
	// idempotent: a second pass finds nothing new to hide
	again, _ := repeats(approx)
	if again != approx {
		t.Error("a second pass changed the page")
	}
}

func TestSubjectIsTheStrip(t *testing.T) {
	for in, want := range map[string]bool{
		"Pure Go, CGO=0": true, "BSD-3-Clause": true, "No cgo": true, "Pure Go": true,
		"Fits": false, "Pure Go speed": false, "Licensing model": false, "": false,
	} {
		if got := subjectIsTheStrip(in); got != want {
			t.Errorf("subjectIsTheStrip(%q) = %v", in, got)
		}
	}
}
