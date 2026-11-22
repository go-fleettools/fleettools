package main

import (
	"strings"
	"testing"
)

var fuzzIn = Inherited{Repo: "acme/.github", File: "default.json"}

// FuzzClassify: whatever the text, it must not panic, and a verdict must be
// self-consistent -- a hit names an entry that really matches, a non-hit names
// nothing. Seeds live in testdata/fuzz/FuzzClassify.
func FuzzClassify(f *testing.F) {
	for _, s := range []string{
		`{"extends":["github>acme/.github"]}`,
		`{"$schema":"x","extends":"local>ACME/.github:default#v1"}`,
		`{"extends":["config:base","github>acme/.github"],"packageRules":[]}`,
		`{"extends":[1,null,{"a":1}]}`,
		`{"extends":{}}`, `[]`, `null`, ``, `{`, `{"extends":["github>acme/.github"]} trailing`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		kind, entry, err := Classify(text, fuzzIn)
		switch {
		case err != nil:
			if kind != None || entry != "" {
				t.Fatalf("error with a verdict: %v %q", kind, entry)
			}
		case kind == None:
			if entry != "" {
				t.Fatalf("no hit but entry %q", entry)
			}
		default:
			if !fuzzIn.Matches(entry) {
				t.Fatalf("%v names %q, which does not match", kind, entry)
			}
		}
	})
}

// FuzzParseOrgConfig: on implies a lower-cased repository and a file; off
// implies neither; and no input may panic. Seeds: testdata/fuzz/FuzzParseOrgConfig.
func FuzzParseOrgConfig(f *testing.F) {
	for _, s := range []string{
		cfgOn,
		`module.exports = { inheritConfig: true }`,
		`{ "inheritConfig": false }`,
		"// inheritConfig: true\n/* x */ inheritConfig:true,inheritConfigRepoName:`{{parentOrg}}/Shared`",
		`inheritConfig: process.env.X`, `inheritConfig`, ``, `/*`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		on, in, err := ParseOrgConfig(text, "Acme")
		switch {
		case err != nil:
			if on || in != (Inherited{}) {
				t.Fatalf("error with a result: %v %+v", on, in)
			}
		case on:
			if in.Repo == "" || in.Repo != strings.ToLower(in.Repo) || in.File == "" && !strings.Contains(text, "inheritConfigFileName") {
				t.Fatalf("on with %+v", in)
			}
		default:
			if in != (Inherited{}) {
				t.Fatalf("off with %+v", in)
			}
		}
	})
}
