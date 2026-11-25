package secretscan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// found reads a fixture of testdata/missed and returns name -> source of its
// plaintext inputs.
func found(t *testing.T, fixture string) map[string]string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", "missed", fixture))
	if err != nil {
		t.Fatal(err)
	}
	gs, err := ScanGo(strings.TrimSuffix(fixture, ".txt"), src)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range gs.Findings {
		if f.Kind == "input" && f.Class == Plaintext {
			got[f.Name] = f.Source
		}
	}
	return got
}

func expect(t *testing.T, got map[string]string, want map[string]string) {
	t.Helper()
	for n, s := range want {
		if got[n] != s {
			t.Errorf("%s: source %q, want %q (all: %v)", n, got[n], s, got)
		}
	}
	for n := range got {
		if _, ok := want[n]; !ok {
			t.Errorf("unexpected finding %s (%s)", n, got[n])
		}
	}
}

func TestMissedAPassAndAnAccessKeyWrittenIntoAField(t *testing.T) {
	expect(t, found(t, "loom_main.go.txt"), map[string]string{
		"WEFT_LOOM_SMTP_PASS": "env", "WEFT_CUBEFS_ACCESS_KEY": "env",
	})
}

func TestMissedAnEnvNameBehindAnIndirection(t *testing.T) {
	// the Getenv argument is a variable; the name is spelled in the fallback.
	expect(t, found(t, "block_driver.go.txt"), map[string]string{
		"WEFT_BACKUP_PASSPHRASE": "env-const",
	})
}

func TestMissedAnEnvNameHeldInAConst(t *testing.T) {
	// TOKEN_EXPIRED and MAX_TOKENS are not secrets; WEFT_SFTP_KEY_FILE is a
	// path, so it is no plaintext input.
	expect(t, found(t, "block_sftp.go.txt"), map[string]string{
		"WEFT_SFTP_PASSWORD": "env-const", "WEFT_SFTP_KEY_PASSPHRASE": "env-const",
		"AWS_ACCESS_KEY_ID": "env-const", "AWS_SECRET_ACCESS_KEY": "env-const",
		"AWS_SESSION_TOKEN": "env-const", "CIFS_PASSWORD": "env-const",
	})
}

func TestMissedTheAWSDefaultCredentialChainIsAnImplicitEnvSecret(t *testing.T) {
	expect(t, found(t, "block_s3.go.txt"), map[string]string{
		"AWS_SECRET_ACCESS_KEY": "env-implicit",
	})
}

func TestAnExplicitCredentialsProviderIsNotTheDefaultChain(t *testing.T) {
	for _, marker := range awsExplicit {
		src := "package x\n\nimport (\n\t\"context\"\n\n\tawsconfig \"github.com/aws/aws-sdk-go-v2/config\"\n)\n\n" +
			"func f(ctx context.Context) {\n\t// " + marker + "\n\t_, _ = awsconfig.LoadDefaultConfig(ctx)\n}\n"
		gs, err := ScanGo("x.go", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range gs.Findings {
			t.Errorf("%s: %+v", marker, f)
		}
	}
	// without the SDK import, a function of that name is nobody's chain.
	gs, _ := ScanGo("y.go", []byte("package y\n\nfunc g(c cfg) { c.LoadDefaultConfig() }\n"))
	if len(gs.Findings) != 0 {
		t.Errorf("findings without the SDK: %+v", gs.Findings)
	}
}

func TestMissedTheCookieKeyOfAWebUI(t *testing.T) {
	expect(t, found(t, "webui_config.go.txt"), map[string]string{"WEBUI_SESSION_KEY": "env"})
}

func TestAPasswordInAURLIsFoundInGoTextAndUnits(t *testing.T) {
	src := "package main\n\nvar a = \"sftp://backup:s3cr3tvalue@host:22/x\"\n" +
		"var b = \"sftp://user:pass@host/x\"\n" +
		"var c = \"sftp://user@host/x\"\n" +
		"var d = \"redis://:%s@host\"\n" +
		"var e = \"postgres://u:$PW@h/db\"\n" +
		"var h = \"a://u:{{.Pw}}@h\"\n" +
		"var i = \"a://u:x%vy@h\"\n" +
		"var j = \"a://u:x%sy@h\"\n" +
		"var k = 'x'\n" +
		"var g = \"https://example.com:8443/x\"\n"
	gs, err := ScanGo("u.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var inputs, samples int
	for _, f := range gs.Findings {
		switch {
		case f.Kind == "input" && f.Source == "url-userinfo" && f.Name == "sftp://user:***@" && f.Line == 3:
			inputs++
		case f.Kind == "sample" && f.Placeholder && f.Line == 4:
			samples++
		default:
			t.Errorf("unexpected %+v", f)
		}
	}
	if inputs != 1 || samples != 1 {
		t.Errorf("inputs %d samples %d, want 1 and 1", inputs, samples)
	}
	if strings.Contains(gs.Findings[0].Name, "s3cr3t") {
		t.Error("the password is reported")
	}

	unit := ScanText("a.service", []byte("[Service]\nEnvironment=\"DB=postgres://app:hunter9x@db/x\"\nEnvironment=\"DB=postgres://app:$PW@db/x\"\n"))
	if len(unit) != 1 || unit[0].Source != "unit-env" || unit[0].Line != 2 {
		t.Errorf("unit: %+v", unit)
	}
	sample := ScanText("c.yaml", []byte("dsn: mysql://root:changeme@h/db\nurl: mysql://root:{{ .Pw }}@h/db\n"))
	if len(sample) != 1 || !sample[0].Placeholder || sample[0].Kind != "sample" {
		t.Errorf("sample: %+v", sample)
	}
}

func TestEnvLike(t *testing.T) {
	for s, want := range map[string]bool{
		"WEFT_SFTP_PASSWORD": true, "A_B": false, "NOUNDERSCORE": false, "lower_case": false,
		"_LEAD_X": false, "WITH-DASH_X": false, "AB_1": false, "ABC_1": true, "": false,
	} {
		if got := envLike(s); got != want {
			t.Errorf("envLike(%q) = %v, want %v", s, got, want)
		}
	}
}

func FuzzURLSecret(f *testing.F) {
	for _, s := range []string{"", "a://b:c@d", "sftp://u:p@h:22/x", "://:@", "x://:@@", "a://b:{c@d", "%s://u:%s@h"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		scheme, pw, ok := URLSecret(s)
		if !ok {
			return
		}
		if scheme == "" || pw == "" || !strings.Contains(s, pw+"@") {
			t.Fatalf("URLSecret(%q) = %q, %q", s, scheme, pw)
		}
		if strings.ContainsAny(pw, "/@ \t\n") {
			t.Fatalf("password %q runs past its delimiter", pw)
		}
	})
}
