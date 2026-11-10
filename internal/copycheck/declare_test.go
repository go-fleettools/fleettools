package copycheck

import (
	"strings"
	"testing"
)

func normative(file, sym string) Value {
	return Value{Kind: ValueNormative, File: file, Symbol: sym, Standard: "ISO/IEC 14496-3", Clause: "Table 4.110", Rationale: "bare band offsets of a normative table"}
}

func gpl(t *testing.T) refSpec {
	return refSpec{"faad2-like", "GPL-2.0-or-later", RoleStudied, ref(t)}
}

// ⛔ THE DECISION, as three planted tests. Bare values of a standard's normative
// table may be held; the standard's code never may.

// 1. A copied table nobody declared must fail.
func TestUndeclaredCopiedTableFails(t *testing.T) {
	rep, err := scenario(t, map[string][]byte{"aac_edges.go": fixture(t, "ours_tables.go.txt")}, gpl(t))
	if err != nil {
		t.Fatal(err)
	}
	if kinds(rep)[KindTable] != 1 || len(rep.Accepted) != 0 {
		t.Fatalf("an undeclared copied table must be a finding: %+v", rep)
	}
}

// 2. The same table, declared normative-values, passes; and the exemption is
// listed, not silent.
func TestDeclaredNormativeValuesPass(t *testing.T) {
	v := normative("aac_edges.go", "edges")
	withManifest(t, func(m *Manifest) { m.Values = []Value{v} })
	rep, err := scenario(t, map[string][]byte{"aac_edges.go": fixture(t, "ours_tables.go.txt")}, gpl(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 0 || len(rep.Accepted) != 1 {
		t.Fatalf("declared normative values must pass and be listed: %+v", rep)
	}
	if a := rep.Accepted[0]; a.Kind != KindDeclared || a.Ours == nil || a.Theirs == nil || !strings.Contains(a.Message, "ISO/IEC 14496-3") {
		t.Errorf("the acceptance must name both ends and the standard: %+v", a)
	}
}

// 3. A translated function fails even when its constants are declared: the
// exemption covers numbers, never code.
func TestTranslatedFunctionFailsEvenWithDeclaredConstants(t *testing.T) {
	withManifest(t, func(m *Manifest) {
		m.Values = []Value{normative("aac_dec.go", "bandEdges"), {Kind: ValueDerived, File: "aac_dec.go", Symbol: "windowCoef", Source: "Purnhagen, DAFx'04, eq. 7", Rationale: "window computed from a published formula"}}
	})
	rep, err := scenario(t, map[string][]byte{"aac_dec.go": fixture(t, "ours_translated.go.txt")}, gpl(t))
	if err != nil {
		t.Fatal(err)
	}
	k := kinds(rep)
	if k[KindTable] != 0 || len(rep.Accepted) != 2 {
		t.Errorf("both tables are declared: want 0 table findings and 2 accepted, got %v / %d", k, len(rep.Accepted))
	}
	if k[KindShape] == 0 {
		t.Fatalf("the translated function must still fail: %+v", rep.Findings)
	}
}

// Declaring a symbol does not cover another table in the same file.
func TestDeclarationCoversOnlyItsSymbol(t *testing.T) {
	withManifest(t, func(m *Manifest) { m.Values = []Value{normative("aac_dec.go", "windowCoef")} })
	rep, err := scenario(t, map[string][]byte{"aac_dec.go": fixture(t, "ours_translated.go.txt")}, gpl(t))
	if err != nil {
		t.Fatal(err)
	}
	if kinds(rep)[KindTable] != 1 || len(rep.Accepted) != 1 {
		t.Fatalf("bandEdges is not declared: %+v", rep)
	}
}

const bigTable = "package p\n\nvar rnd = []int{3, 17, 5, 99, 42, 8, 61, 23, 77, 14, 90, 31, 6, 52, 85, 12, 71, 29, 44, 9, 66, 38, 2, 95, 57, 21, 80, 47, 11, 73, 36, 88, 25}\n"

// A table with no declaration at all, in a path that requires one, is a finding
// even when it matches no reference.
func TestEveryTableInTheRequiredPathsNeedsProvenance(t *testing.T) {
	withManifest(t, func(m *Manifest) { m.RequireDeclared = &RequireDeclared{MinNumbers: 32, Paths: []string{"aac"}} })
	ours := map[string][]byte{
		"aac_tables.go":      []byte(bigTable),
		"other.go":           []byte(bigTable),                                                    // outside the required paths
		"aac_tables_test.go": []byte(strings.Replace(bigTable, "rnd", "expect", 1)),               // test data
		"aac_small.go":       []byte("package p\n\nvar s = []int{3, 17, 5, 99, 42, 8, 61, 23}\n"), // below N
	}
	rep, err := scenario(t, ours, refSpec{"unrelated", "GPL-2.0-only", RoleStudied, ref(t)})
	if err != nil {
		t.Fatal(err)
	}
	if k := kinds(rep); k[KindUndeclared] != 1 || rep.Findings[0].Ours.File != "aac_tables.go" {
		t.Fatalf("want exactly aac_tables.go flagged: %+v", rep.Findings)
	}
	if f := rep.Findings[0]; f.Numbers != 33 || !strings.Contains(f.Message, "normative-values") {
		t.Errorf("%+v", f)
	}

	// declared (derived): passes
	withManifest(t, func(m *Manifest) {
		m.RequireDeclared = &RequireDeclared{MinNumbers: 32, Paths: []string{"aac"}}
		m.Values = []Value{{Kind: ValueDerived, File: "aac_tables.go", Symbol: "rnd", Source: "Some Paper, eq. 3", Rationale: "numbers from the paper's formula"}}
	})
	rep, err = scenario(t, ours, refSpec{"unrelated", "GPL-2.0-only", RoleStudied, ref(t)})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 0 {
		t.Fatalf("declared: %+v", rep.Findings)
	}
}

// A table taken from a declared permissive tables-from source is its own
// provenance (and needs the NOTICE); it is not reported as undeclared.
func TestTableAttributedToPermissiveSourceIsNotUndeclared(t *testing.T) {
	withManifest(t, func(m *Manifest) { m.RequireDeclared = &RequireDeclared{MinNumbers: 20, Paths: []string{"aac"}} })
	ours := map[string][]byte{"aac_edges.go": fixture(t, "ours_tables.go.txt"), "NOTICE": []byte("apache-like\n")}
	rep, err := scenario(t, ours, refSpec{"apache-like", "Apache-2.0", RoleTablesFrom, ref(t)})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 0 {
		t.Fatalf("%+v", rep.Findings)
	}
}

// A declaration that points at nothing is a finding: provenance that drifted
// from the code reads as provenance.
func TestStaleDeclarationIsAFinding(t *testing.T) {
	for name, v := range map[string]Value{
		"renamed symbol": normative("aac_edges.go", "gone"),
		"missing file":   normative("nowhere.go", "edges"),
	} {
		v := v
		withManifest(t, func(m *Manifest) { m.Values = []Value{v} })
		rep, err := scenario(t, map[string][]byte{"aac_edges.go": fixture(t, "ours_tables.go.txt")}, refSpec{"x", "MIT", RoleStudied, ref(t)})
		if err != nil {
			t.Fatal(err)
		}
		if kinds(rep)[KindDeclaration] != 1 {
			t.Errorf("%s: %+v", name, rep.Findings)
		}
	}
}

func TestValuesManifestValidation(t *testing.T) {
	base := goodManifest()
	ok := base + "values:\n  - kind: normative-values\n    file: aac/t.go\n    symbol: sfb\n    standard: ISO/IEC 14496-3\n    clause: Table 4.110\n    rationale: bare values\nrequire_declared:\n  min_numbers: 32\n  paths: [aac]\n"
	if _, err := ParseManifest([]byte(ok)); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(string) string{
		"kind":         func(s string) string { return strings.Replace(s, "normative-values", "whatever", 1) },
		"no clause":    func(s string) string { return strings.Replace(s, "    clause: Table 4.110\n", "", 1) },
		"no standard":  func(s string) string { return strings.Replace(s, "    standard: ISO/IEC 14496-3\n", "", 1) },
		"no rationale": func(s string) string { return strings.Replace(s, "    rationale: bare values\n", "", 1) },
		"derived without source": func(s string) string {
			return strings.Replace(s, "kind: normative-values", "kind: derived", 1)
		},
		"not go":    func(s string) string { return strings.Replace(s, "aac/t.go", "aac/t.c", 1) },
		"traversal": func(s string) string { return strings.Replace(s, "aac/t.go", "../t.go", 1) },
		"symbol":    func(s string) string { return strings.Replace(s, "symbol: sfb", "symbol: 'a b'", 1) },
		"small N":   func(s string) string { return strings.Replace(s, "min_numbers: 32", "min_numbers: 3", 1) },
		"no paths":  func(s string) string { return strings.Replace(s, "paths: [aac]", "paths: []", 1) },
		"twice": func(s string) string {
			i := strings.Index(s, "  - kind")
			j := strings.Index(s, "require_declared")
			return s[:j] + s[i:j] + s[j:]
		},
	} {
		if _, err := ParseManifest([]byte(mut(ok))); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
