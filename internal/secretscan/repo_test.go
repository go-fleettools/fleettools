package secretscan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const serverMain = `package main

import "net/http"

func main() { _ = http.ListenAndServe(":8080", nil) }
`

const configGo = `package config

import "os"

type Config struct {
	Password  string ` + "`toml:\"password\"`" + `
	TokenFile string ` + "`toml:\"token_file\"`" + `
	From      string ` + "`toml:\"api_key_from\"`" + `
}

func check(fi os.FileInfo) bool { return fi.Mode()&0o077 == 0 }
`

const otherConfigGo = `package other

type Config struct {
	KeyFile  string ` + "`toml:\"private_key_file\"`" + `
	Token    string ` + "`toml:\"token\"`" + `
}
`

func TestScanRepoFindsADaemonAndItsInputs(t *testing.T) {
	root := tree(t, map[string]string{
		"cmd/d/main.go":            serverMain,
		"internal/config/c.go":     configGo,
		"internal/other/o.go":      otherConfigGo,
		"internal/other/o_test.go": "package other\nvar _ = os.Getenv(\"TEST_TOKEN\")\n",
		"README.md":                "# d\n```toml\ntoken = \"abc123def\"\n```\n",
		"docs/guide.md":            "```\npassword = \"changeme\"\n```\n",
		"notes.md":                 "```\npassword = \"ignored-not-a-readme\"\n```\n",
		"etc/d.example.json":       "{\n  \"password\": \"x1y2z3\"\n}\n",
		".env.example":             "API_KEY=abc123\n",
		"config.sample":            "token = \"zzz999\"\n",
		"testdata/x.toml":          "password = \"fixture\"\n",
		"vendor/v/v.go":            "package v\nvar _ = os.Getenv(\"VENDOR_TOKEN\")\n",
		"pkg/mod/m/m.go":           "package m\nvar _ = os.Getenv(\"MOD_TOKEN\")\n",
		".github/ci.yml":           "password: abc123\n",
		"go.mod":                   "module example.com/d\n\nrequire github.com/go-authn/secretsource v0.8.0\n",
		"data.bin":                 "password = x",
	})
	if err := os.Symlink(filepath.Join(root, "cmd/d/main.go"), filepath.Join(root, "link.go")); err != nil {
		t.Fatal(err)
	}
	r, err := ScanRepo(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Daemon || len(r.Daemons) != 1 || r.Daemons[0] != filepath.Join("cmd", "d") {
		t.Errorf("daemons %v", r.Daemons)
	}
	if !r.UsesSecretsource || r.GoFiles != 3 || len(r.Unread) != 0 {
		t.Errorf("uses=%v go=%d unread=%v", r.UsesSecretsource, r.GoFiles, r.Unread)
	}
	var got []string
	for _, f := range r.Findings {
		pc := "-"
		if f.PermChecked != nil {
			pc = map[bool]string{true: "checked", false: "unchecked"}[*f.PermChecked]
		}
		got = append(got, strings.Join([]string{f.Kind, f.Source, string(f.Class), f.Name, filepath.ToSlash(f.File), pc}, ":"))
	}
	want := []string{
		"sample:sample:plaintext:API_KEY:.env.example:-",
		"sample:sample:plaintext:token:config.sample:-",
		"sample:doc:plaintext:password:docs/guide.md:-",
		"sample:sample:plaintext:password:etc/d.example.json:-",
		"input:config-field:plaintext:password:internal/config/c.go:-",
		"input:config-field:path:token_file:internal/config/c.go:checked",
		"input:config-field:ref:api_key_from:internal/config/c.go:-",
		"input:config-field:path:private_key_file:internal/other/o.go:unchecked",
		"input:config-field:plaintext:token:internal/other/o.go:-",
		"sample:doc:plaintext:token:README.md:-",
	}
	// The order is by file name, then line.
	if len(got) != len(want) {
		t.Fatalf("got %d findings:\n%s", len(got), strings.Join(got, "\n"))
	}
	have := map[string]bool{}
	for _, g := range got {
		have[g] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("missing %s\n in %s", w, strings.Join(got, "\n"))
		}
	}
	if !(r.Findings[0].File <= r.Findings[len(r.Findings)-1].File) {
		t.Errorf("findings are not ordered by file")
	}
	if n := len(r.Plaintext()); n != 2 {
		t.Errorf("Plaintext() has %d findings, want the two plaintext INPUTS", n)
	}
}

func TestAClientIsNotADaemonAndKeepsNoFindings(t *testing.T) {
	root := tree(t, map[string]string{
		"main.go": "package main\nimport (\"flag\"; \"os\")\nfunc main() { flag.String(\"token\", \"\", \"\"); _ = os.Args }\n",
		"go.mod":  "module github.com/go-authn/secretsource\n",
	})
	r, err := ScanRepo(root, false)
	if err != nil || r.Daemon || len(r.Findings) != 0 || r.UsesSecretsource || len(r.Plaintext()) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = ScanRepo(root, true)
	if err != nil || r.Daemon || len(r.Plaintext()) != 1 {
		t.Fatalf("with all: %+v %v", r, err)
	}
}

func TestAUnitMakesADaemonOfAnyRepo(t *testing.T) {
	root := tree(t, map[string]string{
		"deploy/app.service": "[Service]\nExecStart=/usr/bin/app --token abc123\n",
		"pkg.go":             "package p\n",
	})
	r, err := ScanRepo(root, false)
	if err != nil || !r.Daemon || len(r.Plaintext()) != 1 || r.Plaintext()[0].Exposure != 4 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestScanRepoReportsWhatItCouldNotRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-0 file; the refusal cannot be provoked")
	}
	root := tree(t, map[string]string{
		"bad.go":           "package {",
		"big.toml":         strings.Repeat("a", MaxFile+1),
		"locked.toml":      "password = \"abc123\"\n",
		"closed/inside.go": "package c\n",
		"unit/app.service": "[Service]\n",
	})
	if err := os.Chmod(filepath.Join(root, "locked.toml"), 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "closed"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(root, "closed"), 0o755) })
	r, err := ScanRepo(root, false)
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]string{}
	for _, u := range r.Unread {
		reasons[u.File] = u.Reason
	}
	for _, f := range []string{"bad.go", "big.toml", "locked.toml", "closed"} {
		if reasons[f] == "" {
			t.Errorf("%s not reported unread: %v", f, r.Unread)
		}
	}
	if !strings.Contains(reasons["big.toml"], "larger than") {
		t.Errorf("big.toml: %q", reasons["big.toml"])
	}
	if r.FilesRead != 2 {
		t.Errorf("files read = %d, want 2 (bad.go was read and refused by the parser, the unit)", r.FilesRead)
	}
}

func TestScanRepoOfNothingIsAnErrorNotAnEmptyAnswer(t *testing.T) {
	if _, err := ScanRepo(filepath.Join(t.TempDir(), "absent"), false); err == nil {
		t.Fatal("a clone that is not there was read as empty")
	}
}

func TestClassifyFilesBySuffix(t *testing.T) {
	for name, want := range map[string]fileKind{
		"a.go": kindGo, "a_test.go": kindNone, "go.mod": kindMod, "a.service": kindUnit,
		"x.plist": kindUnit, "README.md": kindSample, "readme.md": kindSample, "other.md": kindNone,
		"docs/a.md": kindSample, "c.yaml": kindSample, ".env": kindSample, ".env.local": kindSample,
		"a.example.json": kindSample, "a.sample.json": kindSample, "a.json": kindNone, "a.txt": kindNone,
	} {
		if got := classify(filepath.FromSlash(name)); got != want {
			t.Errorf("classify(%q) = %d, want %d", name, got, want)
		}
	}
}
