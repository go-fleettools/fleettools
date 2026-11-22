package secretscan

import (
	"fmt"
	"strings"
	"testing"
)

func TestInlineSecretSeparatesAValueFromEverythingElse(t *testing.T) {
	yes := map[string]string{
		`password = "hunter2xyz"`:           "password=hunter2xyz",
		`password = 'abc'`:                  "password=abc",
		"token = `abc`":                     "token=abc",
		`password: abc123`:                  "password=abc123",
		`  - client_secret: abc # a note`:   "client_secret=abc",
		`"api_key": "k-123",`:               "api_key=k-123",
		`export DB_PASSWORD=s3cr3tvalue`:    "DB_PASSWORD=s3cr3tvalue",
		`TOKEN=abc:def`:                     "TOKEN=abc:def",
		`password = "unterminated`:          "password=unterminated",
		`app --password hunter2 --other x`:  "",
		`--password hunter2`:                "password=hunter2",
		`--token=abc123`:                    "token=abc123",
		`password: "two words"`:             "password=two words",
		`secret: a=b`:                       "secret=a=b",
		`password = "changeme"`:             "password=changeme",
		`passphrase = abc,`:                 "passphrase=abc",
		`api-key: abc`:                      "api-key=abc",
		`private_key = "-----BEGIN"`:        "private_key=-----BEGIN",
		`token: tok_live_0123456789abcdefg`: "token=tok_live_0123456789abcdefg",
	}
	for line, want := range yes {
		name, value, ok := InlineSecret(line)
		got := ""
		if ok {
			got = name + "=" + value
		}
		if got != want {
			t.Errorf("InlineSecret(%q) = %q, want %q", line, got, want)
		}
	}
	for _, line := range []string{
		``, `# password = "x"`, `; password = x`, `// password = x`,
		`password = ""`, `password =`, `password:`, `password: |`, `password: >-`,
		`password = "${DB_PASSWORD}"`, `password = $DB_PASSWORD`, `token = %t`, `token = <your token>`,
		`password = {{ .Password }}`, `token = @file`, `password: true`, `password: null`,
		`password_from = "systemd-creds:db"`, `token_from: keychain:github`, `token: keepassxc:x`,
		`token_file = "/etc/token"`, `max_tokens = 4096`, `key_file = "/etc/k"`, `token_type: bearer-ish`,
		`Set the password: abc`, `the password is two words`, `password`, `--password`,
		`pass word = x`, `= x`, `password = a b c`, `--max-tokens 100`,
		`password: file:/etc/pw`, `token = env:TOKEN`,
	} {
		if name, value, ok := InlineSecret(line); ok {
			t.Errorf("InlineSecret(%q) = %q, %q: not a secret value", line, name, value)
		}
	}
}

func TestScanTextReadsSamplesAndDocs(t *testing.T) {
	toml := "# config\nhost = \"x\"\npassword = \"changeme\"\ntoken = \"tok_9f8e7d6c\"\ntoken_file = \"/etc/t\"\n"
	fs := ScanText("etc/app.toml", []byte(toml))
	if len(fs) != 2 || fs[0].Line != 3 || !fs[0].Placeholder || fs[1].Placeholder ||
		fs[0].Kind != "sample" || fs[0].Source != "sample" || fs[0].Exposure != 1 {
		t.Errorf("toml: %+v", fs)
	}
	md := "password = outside\n```toml\npassword = \"in\"\n```\ntoken = after\n```\ntoken = \"in2\"\n"
	fs = ScanText("README.md", []byte(md))
	if len(fs) != 2 || fs[0].Line != 3 || fs[1].Line != 7 || fs[0].Source != "doc" {
		t.Errorf("md: %+v", fs)
	}
}

func TestPlaceholderRecognisesStandIns(t *testing.T) {
	for v, want := range map[string]bool{
		"changeme": true, "YOUR_TOKEN": true, "xxxxxxxx": true, "********": true, "...": true,
		"hunter2": true, "tok_9f8e7d6c": false, "s3cr3t-Value-77": false, "AbCdEf012345": false,
	} {
		if got := placeholder(v); got != want {
			t.Errorf("placeholder(%q) = %v, want %v", v, got, want)
		}
	}
}

const unitFixture = `[Unit]
Description=x
[Service]
Environment=HOME=/root "DB_PASSWORD=s3cr3t" API_TOKEN=abc LOG=1
Environment=TOKEN=%i
Environment=BADTOKEN
ExecStart=/usr/bin/app --password=hunter2 --token abc --token-file /etc/t --max-tokens 9 --flag
ExecStart=/usr/bin/app --api-key --other
ExecStartPre=/usr/bin/app --secret "quoted"
Restart=always
NoEquals
EnvironmentFile=/etc/app.env
`

func TestScanTextReadsAUnitsEnvironmentAndFlags(t *testing.T) {
	var got []string
	for _, f := range ScanText("deploy/app.service", []byte(unitFixture)) {
		if f.Kind != "input" || f.Class != Plaintext || f.Exposure != 4 {
			t.Errorf("finding %+v", f)
		}
		got = append(got, fmt.Sprintf("%s:%s:%d", f.Source, f.Name, f.Line))
	}
	want := "unit-env:DB_PASSWORD:4 unit-env:API_TOKEN:4 unit-flag:password:7 unit-flag:token:7 unit-flag:secret:9"
	if strings.Join(got, " ") != want {
		t.Errorf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
}

const plistFixture = `<?xml version="1.0"?>
<plist version="1.0"><dict>
	<key>Label</key><string>x</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>DB_PASSWORD</key>
		<string>s3cr3t</string>
		<key>HOME</key>
		<string>/root</string>
		<key>TOKEN</key>
		<string>$TOKEN</string>
	</dict>
	<key>ProgramArguments</key>
	<array>
		<string>/usr/bin/app</string>
		<string>--password=hunter2</string>
		<string>--token</string>
		<string>abc123</string>
		<string>--max-tokens</string>
		<string>9</string>
		<string>--token-file</string>
		<string>--flag</string>
		<string>--api-key=$K</string>
	</array>
</dict></plist>
`

func TestScanTextReadsAPlist(t *testing.T) {
	var got []string
	for _, f := range ScanText("com.x.plist", []byte(plistFixture)) {
		got = append(got, fmt.Sprintf("%s:%d", f.Name, f.Line))
		if f.Kind != "input" || f.Source != "unit-env" {
			t.Errorf("finding %+v", f)
		}
	}
	want := "DB_PASSWORD:7 password:16 token:18"
	if strings.Join(got, " ") != want {
		t.Errorf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
}

func TestTagTextRefusesAnUnclosedTag(t *testing.T) {
	if tagText("<key>a", "key") != "" || tagText("a</key>", "key") != "" || tagText("<key>a</key>", "key") != "a" {
		t.Fatal("tagText")
	}
}

func FuzzInlineSecret(f *testing.F) {
	for _, s := range []string{`password = "x"`, `--token a`, `- k: v`, `"`, `=`, `:`, `password: "`, "\x00=\xff"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		name, value, ok := InlineSecret(line)
		if ok && (Classify(name) != Plaintext || !usable(value)) {
			t.Fatalf("InlineSecret(%q) = %q, %q", line, name, value)
		}
	})
}

func FuzzScanText(f *testing.F) {
	f.Add("a.service", []byte(unitFixture))
	f.Add("a.plist", []byte(plistFixture))
	f.Add("README.md", []byte("```\npassword = x\n```"))
	f.Add("a.toml", []byte("password = \"x\"\n"))
	f.Fuzz(func(t *testing.T, rel string, content []byte) {
		for _, fd := range ScanText(rel, content) {
			if fd.Line < 1 || fd.Class != Plaintext {
				t.Fatalf("finding %+v", fd)
			}
		}
	})
}
