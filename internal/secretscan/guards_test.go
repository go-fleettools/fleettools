package secretscan

import (
	"errors"
	"strings"
	"testing"
)

// Each case here holds one guard from both sides: mutsweep found guards that
// the suite only reached by a crash after the guard was removed, or not at all.

func TestClassifyOfWordsThatAreNotANameOrNotAKey(t *testing.T) {
	for name, want := range map[string]Class{
		"": None, "_": None, "file": None, "_file": None,
		"key": None, "key_file": None, "foo_key": None, "monkey": None,
		"api_key": Plaintext, "private_key": Plaintext, "api_key_file": Path,
		"max_tokens": None, "token": Plaintext,
	} {
		if got := Classify(name); got != want {
			t.Errorf("Classify(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestScanGoReadsOnlyTaggedStringFieldsAndLiteralNames(t *testing.T) {
	const src = "package c\n\nimport \"os\"\n\n" +
		"type T struct {\n" +
		"\tPassword string `toml:\"password\"`\n" +
		"\tToken    int    `toml:\"token\"`\n" +
		"\tSecret   string\n" +
		"\tstring   `toml:\"api_key\"`\n" +
		"}\n\n" +
		"func f(fs F, name string) {\n" +
		"\t_ = os.Getenv()\n" +
		"\t_ = os.Getenv(name)\n" +
		"\t_ = os.Getenv(5)\n" +
		"\t_ = os.Getenv(\"DB_PASSWORD\")\n" +
		"\tg(\"token\", \"\", \"\")\n" +
		"}\n"
	gs, err := ScanGo("c.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range gs.Findings {
		got = append(got, f.Name)
	}
	if strings.Join(got, ",") != "password,DB_PASSWORD" {
		t.Fatalf("findings %v, want password and DB_PASSWORD only", got)
	}
}

func TestFindingsAreOrderedByFileThenLine(t *testing.T) {
	root := tree(t, map[string]string{
		"cmd/d/main.go": serverMain,
		"a.go":          "package c\n\n\n\n\ntype C struct {\n\tPassword string `toml:\"password\"`\n}\n",
		"b.go":          "package c\ntype C struct {\n\tToken string `toml:\"token\"`\n\tSecret string `toml:\"secret\"`\n}\n",
	})
	r, err := ScanRepo(root, false)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range r.Findings {
		got = append(got, f.File+":"+f.Name)
	}
	if strings.Join(got, " ") != "a.go:password b.go:token b.go:secret" {
		t.Fatalf("order %v", got)
	}
}

func TestInlineSecretIgnoresCommentsAndLinesWithoutASeparator(t *testing.T) {
	for _, l := range []string{
		"# password = abc12345", "; token = abc12345", "// secret = abc12345",
		"#token=abc12345", "--token", "--", "plain words", "",
	} {
		if n, v, ok := InlineSecret(l); ok {
			t.Errorf("InlineSecret(%q) = %q %q", l, n, v)
		}
	}
	if n, v, ok := InlineSecret("--token abc12345"); !ok || n != "token" || v != "abc12345" {
		t.Errorf("a flag: %q %q %v", n, v, ok)
	}
}

func TestTagTextNeedsBothTags(t *testing.T) {
	for line, want := range map[string]string{
		"<key>x</key>": "x", "<key>x": "", "nothing": "", "x</key>": "", "": "",
	} {
		if got := tagText(line, "key"); got != want {
			t.Errorf("tagText(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestOrgOfIsAnswerOnlyFromAnAnsweredGit(t *testing.T) {
	answer := func(url string, err error) Git {
		return func(string, ...string) (string, error) { return url, err }
	}
	for _, c := range []struct {
		url  string
		err  error
		want string
	}{
		{"https://github.com/acme/tool.git", nil, "acme"},
		{"git@github.com:acme/tool", nil, "acme"},
		{"https://github.com/acme/tool", errors.New("boom"), ""},
		{"nowhere", nil, ""},
		{"", nil, ""},
	} {
		if got := orgOf(answer(c.url, c.err), "d"); got != c.want {
			t.Errorf("orgOf(%q, %v) = %q, want %q", c.url, c.err, got, c.want)
		}
	}
}
