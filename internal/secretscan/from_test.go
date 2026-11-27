package secretscan

import "testing"

// A secret also read by reference (`<NAME>_FROM`, through a secretsource
// resolver) is received by reference; the plain name is the fallback. The
// rule must hold for a const exactly as for a literal.

func TestACompanionFromMakesAConstNameNotPlaintext(t *testing.T) {
	// WEFT_DCS_TOKEN has no companion and stays a finding.
	expect(t, found(t, "from_const.go.txt"), map[string]string{"WEFT_DCS_TOKEN": "env-const"})
}

func TestACompanionFromMakesALiteralNameNotPlaintext(t *testing.T) {
	expect(t, found(t, "from_literal.go.txt"), map[string]string{"WEFT_DCS_TOKEN": "env"})
}

func TestAConstJoinedToFromAndATrimSuffixAreCompanionsToo(t *testing.T) {
	expect(t, found(t, "from_trimsuffix.go.txt"), map[string]string{})
}

func TestACompanionIsExactlyTheNameWithFrom(t *testing.T) {
	for name, src := range map[string]string{
		// the companion of another name does not clear this one
		"other": "package x\nconst a = \"X_PASSWORD\"\nconst b = \"X_TOKEN_FROM\"\n",
		// a suffix that is not exactly _FROM
		"suffix": "package x\nconst a = \"X_PASSWORD\"\nconst b = \"X_PASSWORD_FROMAGE\"\n",
		// the joined const names another value
		"joined": "package x\nconst a = \"X_PASSWORD\"\nconst b = \"Y\"\nvar _ = b + \"_FROM\"\n",
		// a const joined to something else
		"plus": "package x\nconst a = \"X_PASSWORD\"\nvar _ = a + \"_FILE\"\n",
	} {
		gs, err := ScanGo("x.go", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		kept := 0
		for _, f := range gs.Findings {
			if f.Class == Plaintext && f.Name == "X_PASSWORD" {
				kept++
			}
		}
		if kept != 1 {
			t.Errorf("%s: findings %v, want X_PASSWORD kept", name, gs.Findings)
		}
	}
}

func TestACompanionIsFoundWhateverTheDeclarationOrder(t *testing.T) {
	src := "package x\nvar _ = a + \"_FROM\"\nconst a = \"X_PASSWORD\"\n"
	gs, err := ScanGo("x.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(gs.Findings) != 0 {
		t.Errorf("findings %v, want none", gs.Findings)
	}
}

func TestOnlyAnEnvNameIsClearedByACompanion(t *testing.T) {
	// a config field tagged password is still a plaintext input.
	src := "package x\nconst b = \"password_FROM\"\ntype C struct {\n\tPassword string `toml:\"password\"`\n}\n"
	gs, err := ScanGo("x.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range gs.Findings {
		if f.Source == "config-field" && f.Class == Plaintext {
			n++
		}
	}
	if n != 1 {
		t.Errorf("findings %v, want the config field kept", gs.Findings)
	}
}
