package secretscan

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// The committed corpus is the contract: every line is a name and the class the
// heuristic must give it. It holds the false positives on purpose.
func TestTheCorpusOfNamesIsClassifiedAsWritten(t *testing.T) {
	b, err := os.ReadFile("testdata/names.tsv")
	if err != nil {
		t.Fatal(err)
	}
	var n int
	counts := map[string]int{}
	for _, l := range strings.Split(string(b), "\n") {
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		name, want, ok := strings.Cut(l, "\t")
		if !ok {
			t.Fatalf("corpus line %q has no tab", l)
		}
		if want == "none" {
			want = ""
		}
		counts[want]++
		n++
		if got := Classify(name); string(got) != want {
			t.Errorf("Classify(%q) = %q, want %q", name, got, want)
		}
	}
	// A corpus that lost a class would pass by testing nothing.
	for _, c := range []string{"plaintext", "path", "ref", "env", ""} {
		if counts[c] < 2 {
			t.Errorf("corpus holds %d names of class %q: too few to constrain it", counts[c], c)
		}
	}
	t.Logf("%d names", n)
}

func TestTokensSplitsAtSeparatorsAndCase(t *testing.T) {
	for in, want := range map[string][]string{
		"APIKey":             {"api", "key"},
		"clientSecret":       {"client", "secret"},
		"client_secret-file": {"client", "secret", "file"},
		"GITHUB_TOKEN":       {"github", "token"},
		"a.b":                {"a", "b"},
		"S3Key":              {"s3", "key"},
		"":                   nil,
		"__":                 nil,
	} {
		if got := Tokens(in); !reflect.DeepEqual(got, want) {
			t.Errorf("Tokens(%q) = %v, want %v", in, got, want)
		}
	}
}

func FuzzClassify(f *testing.F) {
	for _, s := range []string{"password", "max_tokens", "APIKey", "", "ü_Token", "a__b", "KEY_FILE", "TOKEN_FROM"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		c := Classify(s)
		switch c {
		case None, Plaintext, Path, Ref, Env:
		default:
			t.Fatalf("Classify(%q) = %q, not a class", s, c)
		}
		for _, tok := range Tokens(s) {
			if tok == "" || tok != strings.ToLower(tok) {
				t.Fatalf("Tokens(%q) holds %q", s, tok)
			}
		}
	})
}
