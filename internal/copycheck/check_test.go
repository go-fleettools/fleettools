package copycheck

import (
	"strings"
	"testing"
)

type refSpec struct {
	name, license, role string
	files               map[string][]byte
}

// scenario runs the whole gate: our tree against references served over TLS.
func scenario(t *testing.T, ours map[string][]byte, refs ...refSpec) (*Report, error) {
	t.Helper()
	return scenarioR(t, ours, nil, refs...)
}

// scenarioR adds per-file restrictions, by reference name.
func scenarioR(t *testing.T, ours map[string][]byte, restr map[string][]Restriction, refs ...refSpec) (*Report, error) {
	t.Helper()
	routes := map[string][]byte{}
	for _, r := range refs {
		files := map[string][]byte{}
		for n, b := range r.files {
			files[r.name+"-v1/"+n] = b
		}
		routes["/"+r.name+"-v1.tar.gz"] = tarGz(t, files)
	}
	srv, cl := serve(t, routes)
	f := newFetcher(t, cl)
	m := &Manifest{Version: 1, Notice: "NOTICE"}
	for _, r := range refs {
		ref := Reference{Name: r.name, URL: srv.URL + "/" + r.name + "-v1.tar.gz", Pin: "v1", License: r.license, Role: r.role, Restrictions: restr[r.name]}
		m.References = append(m.References, pinned(t, f, ref))
	}
	if testMod != nil {
		testMod(m)
	}
	return Check(Options{Root: writeTree(t, ours), Manifest: m, Fetcher: f, Thresholds: DefaultThresholds()})
}

// testMod lets one test adjust the manifest a scenario builds (values entries,
// the require_declared rule) without every scenario growing a parameter.
var testMod func(*Manifest)

func withManifest(t *testing.T, mod func(*Manifest)) {
	t.Helper()
	testMod = mod
	t.Cleanup(func() { testMod = nil })
}

func kinds(rep *Report) map[string]int {
	k := map[string]int{}
	for _, f := range rep.Findings {
		k[f.Kind]++
	}
	return k
}

var cRef = map[string][]byte{"libavcodec/dec.c": nil}

func ref(t *testing.T) map[string][]byte {
	return map[string][]byte{"libavcodec/dec.c": fixture(t, "ref_dec.c.txt")}
}

// ⛔ THE PLANTED POSITIVE. A hurried C to Go translation of one function, with
// its tables retyped, against a GPL reference that was only "studied". A gate
// that has not been shown to fire is a gate nobody knows is switched on.
func TestPlantedTranslationFires(t *testing.T) {
	rep, err := scenario(t, map[string][]byte{"dec.go": fixture(t, "ours_translated.go.txt")},
		refSpec{"ffmpeg-like", "GPL-2.0-or-later", RoleStudied, ref(t)})
	if err != nil {
		t.Fatal(err)
	}
	k := kinds(rep)
	if k[KindTable] == 0 || k[KindShape] == 0 {
		t.Fatalf("the translation was not caught: %v\n%+v", k, rep.Findings)
	}
	for _, f := range rep.Findings {
		if f.Ours == nil || f.Theirs == nil || f.Ours.File != "dec.go" || f.Theirs.File != "libavcodec/dec.c" {
			t.Errorf("a finding must name both ends: %+v", f)
		}
		if f.Kind == KindShape {
			t.Logf("shape: %d tokens, %.0f%% ours %d-%d theirs %d-%d", f.Tokens, f.Similarity*100, f.Ours.Start, f.Ours.End, f.Theirs.Start, f.Theirs.End)
		}
		if f.Kind == KindTable {
			t.Logf("table: %d numbers ours %d-%d theirs %d-%d", f.Numbers, f.Ours.Start, f.Ours.End, f.Theirs.Start, f.Theirs.End)
		}
	}
}

// The CONTROL: the same job, written from the description. It must stay quiet.
func TestIndependentImplementationIsQuiet(t *testing.T) {
	rep, err := scenario(t, map[string][]byte{"dec.go": fixture(t, "ours_independent.go.txt")},
		refSpec{"ffmpeg-like", "GPL-2.0-or-later", RoleStudied, ref(t)})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 0 {
		t.Fatalf("false positive on an independent implementation: %+v", rep.Findings)
	}
}

// A retyped table is caught across spelling: hex against the C's hex is not the
// test here, decimal-in-C against hex-in-Go is.
func TestTableAloneIsCaughtAcrossSpelling(t *testing.T) {
	rep, err := scenario(t, map[string][]byte{"edges.go": fixture(t, "ours_tables.go.txt")},
		refSpec{"ffmpeg-like", "LGPL-2.1-or-later", RoleOracle, ref(t)})
	if err != nil {
		t.Fatal(err)
	}
	k := kinds(rep)
	if k[KindTable] != 1 || k[KindShape] != 0 {
		t.Fatalf("want one table finding and no shape finding, got %v: %+v", k, rep.Findings)
	}
	if n := rep.Findings[0].Numbers; n != 25 {
		t.Errorf("the whole table is 25 numbers, matched %d", n)
	}
}

// A table that is also in the permissive reference the author declared taking
// tables from is attributed to it, and the attribution is checked.
func TestAttributedToPermissiveTablesFrom(t *testing.T) {
	gpl := refSpec{"faad2-like", "GPL-2.0-only", RoleStudied, ref(t)}
	apache := refSpec{"packetvideo-like", "Apache-2.0", RoleTablesFrom, map[string][]byte{"aacdec/tables.c": fixture(t, "ref_dec.c.txt")}}
	ours := map[string][]byte{"edges.go": fixture(t, "ours_tables.go.txt")}

	rep, err := scenario(t, ours, gpl, apache)
	if err != nil {
		t.Fatal(err)
	}
	k := kinds(rep)
	if k[KindTable] != 0 || k[KindAttribution] != 1 {
		t.Fatalf("want the GPL hit shielded and one missing-attribution finding, got %v: %+v", k, rep.Findings)
	}
	if !strings.Contains(rep.Findings[0].Message, "NOTICE") {
		t.Errorf("the message must name the notice file: %s", rep.Findings[0].Message)
	}

	ours["NOTICE"] = []byte("Numeric tables in edges.go: packetvideo-like, Apache-2.0.\n")
	rep, err = scenario(t, ours, gpl, apache)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 0 {
		t.Fatalf("attributed and noticed, yet: %+v", rep.Findings)
	}
}

// tables-from does not license translating code; ported-from does, with notice.
func TestRolesDecideWhatMayBeTaken(t *testing.T) {
	ours := map[string][]byte{"dec.go": fixture(t, "ours_translated.go.txt"), "NOTICE": []byte("ported from apache-like\n")}
	mk := func(role string) refSpec {
		return refSpec{"apache-like", "Apache-2.0", role, ref(t)}
	}
	for _, role := range []string{RoleStudied, RoleOracle} {
		rep, err := scenario(t, ours, mk(role))
		if err != nil {
			t.Fatal(err)
		}
		if k := kinds(rep); k[KindTable] == 0 || k[KindShape] == 0 {
			t.Errorf("role %s allows no copying, got %v", role, k)
		}
	}
	rep, err := scenario(t, ours, mk(RoleTablesFrom))
	if err != nil {
		t.Fatal(err)
	}
	if k := kinds(rep); k[KindTable] != 0 || k[KindShape] == 0 {
		t.Errorf("tables-from allows the table and not the code, got %v", k)
	}
	rep, err = scenario(t, ours, mk(RolePortedFrom))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 0 {
		t.Errorf("ported-from, permissive, noticed: want clean, got %+v", rep.Findings)
	}
}

// ⛔ A per-file notice beats the reference's role: PacketVideo is Apache-2.0 and
// declared tables-from, yet a file that also carries an MPEG notice is
// read-only. The same table in an unrestricted file of the same reference is
// still allowed.
func TestPerFileRestrictionRefusesTablesFrom(t *testing.T) {
	tab := fixture(t, "ref_dec.c.txt")
	// the notice sits BELOW the code, as PacketVideo's does (huffdecode.cpp:152)
	// and it is re-wrapped, with doubled spaces, as PacketVideo's is
	restricted := append(append([]byte{}, tab...), []byte("\n/* ... ISO/IEC  gives users of the MPEG-2 NBC/MPEG-4 Audio\n   standards free license to this software module */\n")...)
	restr := map[string][]Restriction{"pv-like": {{Name: "MPEG notice", Contains: []string{"gives users of the MPEG-2 NBC/MPEG-4 Audio standards"}}}}
	ours := map[string][]byte{"edges.go": fixture(t, "ours_tables.go.txt"), "NOTICE": []byte("pv-like\n")}

	rep, err := scenarioR(t, ours, restr, refSpec{"pv-like", "Apache-2.0", RoleTablesFrom, map[string][]byte{"aac/tables.c": restricted}})
	if err != nil {
		t.Fatal(err)
	}
	if kinds(rep)[KindTable] != 1 || !strings.Contains(rep.Findings[0].Message, "MPEG notice") {
		t.Fatalf("a restricted file must be refused under tables-from: %+v", rep.Findings)
	}

	both := map[string][]byte{"aac/tables.c": restricted, "aac/other.c": tab}
	rep, err = scenarioR(t, ours, restr, refSpec{"pv-like", "Apache-2.0", RoleTablesFrom, both})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 0 {
		t.Fatalf("the table is also in an unrestricted file: attributed to it, want clean, got %+v", rep.Findings)
	}
}

func TestRestrictionManifestValidation(t *testing.T) {
	base := "version: 1\nreferences:\n  - name: a\n    url: https://x/a-v1.tar.gz\n    pin: v1\n    tree_sha256: " + strings.Repeat("a", 64) + "\n    license: Apache-2.0\n    role: studied\n"
	if _, err := ParseManifest([]byte(base + "    restrictions:\n      - name: MPEG\n        contains: [\"MPEG\"]\n")); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"    restrictions:\n      - name: MPEG\n",
		"    restrictions:\n      - name: MPEG\n        contains: [\"\"]\n",
		"    restrictions:\n      - contains: [\"MPEG\"]\n",
	} {
		if _, err := ParseManifest([]byte(base + bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestPolicyRefusesCopyingFromCopyleft(t *testing.T) {
	rep, err := scenario(t, map[string][]byte{"dec.go": fixture(t, "ours_independent.go.txt")},
		refSpec{"faad2-like", "GPL-2.0-only", RoleTablesFrom, ref(t)})
	if err != nil {
		t.Fatal(err)
	}
	if kinds(rep)[KindPolicy] != 1 {
		t.Fatalf("tables-from a GPL reference must be refused: %+v", rep.Findings)
	}
}

// MPL is file-level: allowed into a file that says it is MPL, refused elsewhere.
func TestMPLNeedsTheFileToStateMPL(t *testing.T) {
	mpl := refSpec{"mpl-like", "MPL-2.0", RoleTablesFrom, ref(t)}
	plain := fixture(t, "ours_tables.go.txt")
	rep, err := scenario(t, map[string][]byte{"edges.go": plain}, mpl)
	if err != nil {
		t.Fatal(err)
	}
	if kinds(rep)[KindTable] != 1 {
		t.Fatalf("a file that does not state MPL took MPL tables: %+v", rep.Findings)
	}
	stated := append([]byte("// SPDX-License-Identifier: MPL-2.0\n"), plain...)
	rep, err = scenario(t, map[string][]byte{"edges.go": stated, "NOTICE": []byte("mpl-like\n")}, mpl)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 0 {
		t.Fatalf("an MPL file taking MPL tables, noticed: %+v", rep.Findings)
	}
}

// ⛔ A reference that cannot be read is an error, never a clean run.
func TestUnreadableReferenceIsAnErrorNotClean(t *testing.T) {
	srv, cl := serve(t, map[string][]byte{})
	f := newFetcher(t, cl)
	m := &Manifest{Version: 1, Notice: "NOTICE", References: []Reference{{
		Name: "gone", URL: srv.URL + "/gone-v1.tar.gz", Pin: "v1", Tree: strings.Repeat("0", 64), License: "GPL-2.0-only", Role: RoleStudied}}}
	_, err := Check(Options{Root: writeTree(t, map[string][]byte{"a.go": []byte("package a\n")}), Manifest: m, Fetcher: f, Thresholds: DefaultThresholds()})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("want an error naming the 404, got %v", err)
	}
}

func TestEmptyOurTreeIsAnErrorNotClean(t *testing.T) {
	r := refSpec{"x", "MIT", RoleStudied, ref(t)}
	if _, err := scenario(t, map[string][]byte{"README.md": []byte("hi")}, r); err == nil {
		t.Fatal("a tree with no source is an empty population")
	}
}

func TestThresholdsRefuseASampler(t *testing.T) {
	d := DefaultThresholds()
	for name, mut := range map[string]func(*Thresholds){
		"k<8":      func(x *Thresholds) { x.TableK = 7 },
		"run<k+w":  func(x *Thresholds) { x.TableMinRun = x.TableK + x.TableW - 2 },
		"sim=0":    func(x *Thresholds) { x.ShapeMinSim = 0 },
		"block<kw": func(x *Thresholds) { x.ShapeMinToks = 3 },
	} {
		x := d
		mut(&x)
		if x.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
}
