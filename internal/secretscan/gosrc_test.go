package secretscan

import (
	"sort"
	"strings"
	"testing"
)

const goFixture = `package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/go-authn/secretsource/v2"
	"github.com/spf13/pflag"
)

type Config struct {
	Password   string            ` + "`toml:\"password\"`" + `
	Token      *string           ` + "`yaml:\"token,omitempty\"`" + `
	Blob       []byte            ` + "`json:\"private_key\"`" + `
	Many       []string          ` + "`toml:\"api_tokens\"`" + `
	ByName     map[string]string ` + "`toml:\"secret\"`" + `
	TokenFile  string            ` + "`toml:\"token_file\"`" + `
	PassFrom   string            ` + "`toml:\"password_from\"`" + `
	Second     string            ` + "`toml:\"host\" yaml:\"client_secret\"`" + `
	MaxTokens  string            ` + "`toml:\"max_tokens\"`" + `
	Required   bool              ` + "`toml:\"password_required\"`" + `
	Count      int               ` + "`toml:\"token\"`" + `
	Wrapped    Secret            ` + "`toml:\"passphrase\"`" + `
	Nested     [][]string        ` + "`toml:\"credentials\"`" + `
	Ints       map[string]int    ` + "`toml:\"secret\"`" + `
	Dur        time.Duration     ` + "`toml:\"token\"`" + `
	NoTag      string
	Skipped    string            ` + "`json:\"-\"`" + `
	Unknown    string            ` + "`custom:\"password\"`" + `
	Embedded
	A, B       string            ` + "`toml:\"api_key\"`" + `
}

type Secret string

func main() {
	fs := pflag.NewFlagSet("x", pflag.ContinueOnError)
	var pw string
	fs.StringVarP(&pw, "password", "p", "", "")
	fs.String("token", "", "")
	fs.StringSlice("tokens", nil, "")
	fs.String("token-file", "", "")
	fs.String("max-tokens", "", "")
	fs.String()
	fs.StringVar(&pw)
	name := "password"
	fs.String(name, "", "")
	fs.Bool("password", false, "")
	_ = os.Getenv("DB_PASSWORD")
	_, _ = os.LookupEnv("API_KEY")
	_ = os.Getenv("HOME")
	_ = os.Getenv(name)
	_ = os.Getenv()
	_ = other.Getenv("TOKEN")
	_ = fmt.Sprint("password")
	f := func() {}
	f()
	_ = http.ListenAndServe(":0", nil)
	_ = secretsource.Resolve
	var m os.FileInfo
	_ = m.Mode().Perm()
}

func (c Config) main() {}
`

func TestScanGoReadsFieldsFlagsAndEnvironment(t *testing.T) {
	gs, err := ScanGo("cmd/x/main.go", []byte(goFixture))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range gs.Findings {
		if f.Kind != "input" || f.File != "cmd/x/main.go" || f.Line == 0 || f.Exposure == 0 {
			t.Errorf("malformed finding %+v", f)
		}
		got = append(got, f.Source+":"+f.Name+":"+string(f.Class))
	}
	sort.Strings(got)
	want := []string{
		"config-field:api_key:plaintext",
		"config-field:api_tokens:plaintext",
		"config-field:client_secret:plaintext",
		"config-field:password:plaintext",
		"config-field:password_from:ref",
		"config-field:private_key:plaintext",
		"config-field:secret:plaintext",
		"config-field:token:plaintext",
		"config-field:token_file:path",
		"env:API_KEY:plaintext",
		"env:DB_PASSWORD:plaintext",
		"flag:password:plaintext",
		"flag:token:plaintext",
		"flag:token-file:path",
		"flag:tokens:plaintext",
	}
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("findings\n got %s\nwant %s", strings.Join(got, " "), strings.Join(want, " "))
	}
	if !gs.HasMain || !gs.Serves || !gs.PermEvidence {
		t.Errorf("markers: main=%v serves=%v perm=%v", gs.HasMain, gs.Serves, gs.PermEvidence)
	}
	if len(gs.Imports) != 5 || gs.Imports[3] != SecretsourcePath+"/v2" {
		t.Errorf("imports %v", gs.Imports)
	}
}

func TestAMethodNamedMainIsNotAProgram(t *testing.T) {
	gs, err := ScanGo("a.go", []byte("package a\ntype T struct{}\nfunc (T) main() {}\n"))
	if err != nil || gs.HasMain || gs.Serves || gs.PermEvidence {
		t.Fatalf("%+v %v", gs, err)
	}
}

func TestSecretsourceAloneIsPermissionEvidence(t *testing.T) {
	gs, err := ScanGo("a.go", []byte("package a\nimport _ \""+SecretsourcePath+"\"\n"))
	if err != nil || !gs.PermEvidence {
		t.Fatalf("%+v %v", gs, err)
	}
}

func TestScanGoRefusesWhatDoesNotParse(t *testing.T) {
	if _, err := ScanGo("bad.go", []byte("package {")); err == nil {
		t.Fatal("a file that does not parse was read")
	}
}

func FuzzScanGo(f *testing.F) {
	f.Add([]byte(goFixture))
	f.Add([]byte("package p\nvar _ = os.Getenv(\"TOKEN\")\n"))
	f.Add([]byte("package p\ntype T struct{ A string `toml:\"password\"` }\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, src []byte) {
		gs, err := ScanGo("f.go", src)
		if err != nil {
			return
		}
		for _, fd := range gs.Findings {
			if fd.Class == None || fd.Line < 1 {
				t.Fatalf("finding %+v", fd)
			}
		}
	})
}

func TestAFlagThatTakesAPathIsNotThePlaintextSecret(t *testing.T) {
	src := "package main\n\nimport \"flag\"\n\nfunc main() {\n" +
		"\t_ = flag.String(\"store-ssh-passphrase\", \"\", \"path to the ssh key file\")\n" +
		"\t_ = flag.String(\"content-token\", \"\", \"Token FILE\")\n" +
		"\t_ = flag.String(\"token\", \"~/.github-token\", \"github token\")\n" +
		"\t_ = flag.String(\"abs-token\", \"/etc/t\", \"x\")\n" +
		"\t_ = flag.String(\"rel-token\", \"./t\", \"x\")\n" +
		"\t_ = flag.String(\"up-token\", \"../t\", \"x\")\n" +
		"\t_ = flag.String(\"password\", \"\", \"the password\")\n" +
		"\t_ = flag.String(\"api-token\", \"\", \"filename of nothing\")\n" +
		"\t_ = flag.String(\"dyn-token\", dflt, \"the token\")\n" +
		"\t_ = flag.String(\"use-token\", \"\", \"the credential to use (default ~/.github-token)\")\n" +
		"\t_ = flag.String(\"url-token\", \"\", \"see https://x/y for the token\")\n" +
		"}\n"
	gs, err := ScanGo("m.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Class{
		"store-ssh-passphrase": Path, "content-token": Path, "token": Path,
		"abs-token": Path, "rel-token": Path, "up-token": Path,
		"password": Plaintext, "api-token": Path, "dyn-token": Plaintext,
		"use-token": Path, "url-token": Plaintext,
	}
	got := map[string]Class{}
	for _, f := range gs.Findings {
		got[f.Name] = f.Class
	}
	for n, c := range want {
		if got[n] != c {
			t.Errorf("flag %q = %q, want %q", n, got[n], c)
		}
	}
}

func TestKeyMaterialFlagsAreSecretsButAKeyPathIsNot(t *testing.T) {
	src := "package main\n\nimport \"flag\"\n\nfunc main() {\n" +
		"\t_ = flag.String(\"negotiation-key\", \"\", \"k\")\n" +
		"\t_ = flag.String(\"control-plane-key\", \"\", \"k\")\n" +
		"\t_ = flag.String(\"zone-key\", \"\", \"k\")\n" +
		"\t_ = flag.String(\"key\", \"\", \"k\")\n" +
		"\t_ = flag.String(\"key-file\", \"\", \"k\")\n" +
		"}\n"
	gs, err := ScanGo("m.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Class{}
	for _, f := range gs.Findings {
		got[f.Name] = f.Class
	}
	for n, c := range map[string]Class{
		"negotiation-key": Plaintext, "control-plane-key": Plaintext, "zone-key": Plaintext,
		"key": None, "key-file": None,
	} {
		if got[n] != c {
			t.Errorf("flag %q = %q, want %q", n, got[n], c)
		}
	}
}
