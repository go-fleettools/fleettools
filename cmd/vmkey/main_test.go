package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func newKey(t *testing.T) (ssh.PublicKey, string) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return k, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k))) + " test-key"
}

func keyFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "k.pub")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const inspectOK = "OS|Win32NT\r\nUSER|std\r\nADMIN|PC\\reader\r\nADMIN|PC\\Administrator\r\nPROFILE|True\r\n"

const lockOut = "LOCKED|C:\\Users\\std\\.ssh\\authorized_keys\r\nLOCKED|C:\\Users\\std\\.ssh\r\n"

func verifyOut(user string, line string) string {
	var b strings.Builder
	for _, p := range []string{`C:\Users\` + user + `\.ssh`, `C:\Users\` + user + `\.ssh\authorized_keys`} {
		b.WriteString("OWNER|" + p + "|PC\\" + user + "\n")
		b.WriteString("ACE|" + p + "|PC\\" + user + "|FullControl|Allow\n")
		b.WriteString("ACE|" + p + "|NT AUTHORITY\\SYSTEM|FullControl|Allow\n")
	}
	b.WriteString("KEY|" + line + "\n")
	return b.String()
}

type fake struct {
	calls [][]string
	stdin [][]byte
	outs  []string
	err   error
}

func (f *fake) run(_ context.Context, argv []string, stdin []byte) ([]byte, error) {
	f.calls = append(f.calls, argv)
	f.stdin = append(f.stdin, stdin)
	if f.err != nil {
		return nil, f.err
	}
	o := f.outs[0]
	f.outs = f.outs[1:]
	return []byte(o), nil
}

func cfg(t *testing.T, content string) config {
	return config{host: "127.0.0.1", port: 2222, admin: "reader", user: "std", pubkey: keyFile(t, content)}
}

func TestInstall(t *testing.T) {
	k, line := newKey(t)
	c := cfg(t, line+"\n")
	f := &fake{outs: []string{inspectOK, verifyOut("std", line), lockOut}}
	var out bytes.Buffer
	if err := run(context.Background(), c, f.run, &out); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 3 {
		t.Fatalf("%d calls", len(f.calls))
	}
	a := f.calls[1]
	want := []string{"ssh", "-p", "2222", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "reader@127.0.0.1"}
	for i, w := range want {
		if a[i] != w {
			t.Errorf("argv[%d] = %q, want %q", i, a[i], w)
		}
	}
	if len(a) != 9 || !strings.Contains(a[8], `C:\Users\std\.ssh`) {
		t.Errorf("script argv = %q", a)
	}
	if string(f.stdin[1]) != line+"\n" {
		t.Errorf("stdin = %q", f.stdin[1])
	}
	for _, s := range []string{"target: reader@127.0.0.1 port 2222", "remote action: recover access", "remote action: remove inheritance", "installed:", ssh.FingerprintSHA256(k)} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("output lacks %q:\n%s", s, out.String())
		}
	}
	if strings.Contains(out.String(), "BEGIN") {
		t.Error("output carries key material")
	}
}

func TestIdempotent(t *testing.T) {
	_, line := newKey(t)
	c := cfg(t, line)
	f := &fake{outs: []string{inspectOK + "KEY|# comment\r\nKEY|" + line + "\r\n"}}
	var out bytes.Buffer
	if err := run(context.Background(), c, f.run, &out); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("calls %d, out %s", len(f.calls), out.String())
	}
}

func TestDryRunSendsNothing(t *testing.T) {
	_, line := newKey(t)
	c := cfg(t, line)
	c.dryRun = true
	f := &fake{}
	var out bytes.Buffer
	if err := run(context.Background(), c, f.run, &out); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 || !strings.Contains(out.String(), "dry run: nothing was sent") {
		t.Errorf("calls %d, out %s", len(f.calls), out.String())
	}
}

func TestRefusals(t *testing.T) {
	_, line := newKey(t)
	_, line2 := newKey(t)
	cases := []struct {
		name string
		edit func(*config)
		file string
		want string
	}{
		{"remote host", func(c *config) { c.host = "10.0.0.5" }, line, "not a loopback"},
		{"remote name", func(c *config) { c.host = "example.com" }, line, "not a loopback"},
		{"port low", func(c *config) { c.port = 0 }, line, "out of range"},
		{"port high", func(c *config) { c.port = 70000 }, line, "out of range"},
		{"admin option", func(c *config) { c.admin = "-oProxyCommand" }, line, "not a valid account"},
		{"user shell", func(c *config) { c.user = "std'; calc" }, line, "not a valid account"},
		{"user long", func(c *config) { c.user = strings.Repeat("a", 21) }, line, "not a valid account"},
		{"same account", func(c *config) { c.user = "Reader" }, line, "same account"},
		{"private key", nil, "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n", "private-key material"},
		{"not a key", nil, "hello world\n", "not an OpenSSH public key"},
		{"two keys", nil, line + "\n" + line2 + "\n", "exactly ONE"},
		{"options", nil, `command="calc" ` + line + "\n", "options"},
		{"control char", nil, line + "\x01x\n", "control character"},
		{"too large", nil, strings.Repeat("#", 17<<10) + "\n", "larger than 16 KiB"},
	}
	for _, tc := range cases {
		c := cfg(t, tc.file)
		if tc.edit != nil {
			tc.edit(&c)
		}
		f := &fake{}
		err := run(context.Background(), c, f.run, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
		if len(f.calls) != 0 {
			t.Errorf("%s: reached ssh", tc.name)
		}
	}
	c := cfg(t, line)
	c.pubkey = filepath.Join(t.TempDir(), "missing")
	if err := run(context.Background(), c, (&fake{}).run, &bytes.Buffer{}); err == nil {
		t.Error("missing file accepted")
	}
	c.pubkey = t.TempDir()
	if err := run(context.Background(), c, (&fake{}).run, &bytes.Buffer{}); err == nil {
		t.Error("directory accepted")
	}
}

func TestRemoteRefusals(t *testing.T) {
	_, line := newKey(t)
	cases := []struct{ name, out, want string }{
		{"not windows", "OS|Unix\n", "not a Windows host"},
		{"no answer", "", "not a Windows host"},
		{"no user", "OS|Win32NT\nUSER|other\n", "does not exist"},
		{"admin", "OS|Win32NT\nUSER|std\nADMIN|PC\\STD\nPROFILE|True\n", "member of Administrators"},
		{"no profile", "OS|Win32NT\nUSER|std\nPROFILE|False\n", "does not exist; the user must log on"},
	}
	for _, tc := range cases {
		f := &fake{outs: []string{tc.out}}
		err := run(context.Background(), cfg(t, line), f.run, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
		if len(f.calls) != 1 {
			t.Errorf("%s: %d calls, want only the read", tc.name, len(f.calls))
		}
	}
}

func TestRunnerErrors(t *testing.T) {
	_, line := newKey(t)
	boom := errors.New("boom")
	f := &fake{err: boom}
	if err := run(context.Background(), cfg(t, line), f.run, &bytes.Buffer{}); !errors.Is(err, boom) {
		t.Errorf("inspect err = %v", err)
	}
	for _, n := range []int{1, 2} {
		outs := []string{inspectOK, verifyOut("std", line), lockOut}
		calls := 0
		r := func(_ context.Context, _ []string, _ []byte) ([]byte, error) {
			calls++
			if calls == n+1 {
				return nil, boom
			}
			o := outs[calls-1]
			return []byte(o), nil
		}
		if err := run(context.Background(), cfg(t, line), r, &bytes.Buffer{}); !errors.Is(err, boom) {
			t.Errorf("step %d err = %v", n, err)
		}
	}
}

func TestVerifyRefusals(t *testing.T) {
	k, line := newKey(t)
	good := verifyOut("std", line)
	c := config{user: "std"}
	if err := c.checkVerify([]byte(good), k); err != nil {
		t.Fatal(err)
	}
	file := `C:\Users\std\.ssh\authorized_keys`
	cases := []struct{ name, out, want string }{
		{"owner", strings.Replace(good, "OWNER|"+file+"|PC\\std", "OWNER|"+file+"|BUILTIN\\Administrators", 1), "owned by"},
		{"extra principal", good + "ACE|" + file + "|BUILTIN\\Administrators|FullControl|Allow\n", "grants access to"},
		{"read only", strings.Replace(good, "PC\\std|FullControl", "PC\\std|ReadAndExecute", 1), "unexpected rule"},
		{"deny", strings.Replace(good, "SYSTEM|FullControl|Allow", "SYSTEM|FullControl|Deny", 1), "unexpected rule"},
		{"missing ace", strings.Replace(good, "ACE|"+file+"|NT AUTHORITY\\SYSTEM|FullControl|Allow\n", "", 1), "ACL not as expected"},
		{"no owner line", strings.Replace(good, "OWNER|"+file+"|PC\\std\n", "", 1), "ACL not as expected"},
		{"short ace", good + "ACE|x\n", ""},
		{"no key", strings.Replace(good, "KEY|"+line+"\n", "KEY|garbage\n", 1), "appears 0 times"},
		{"duplicate key", good + "KEY|" + line + "\n", "appears 2 times"},
	}
	for _, tc := range cases {
		err := c.checkVerify([]byte(tc.out), k)
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
	// A failed verification surfaces from run.
	// A failed check stops BEFORE the lock: the lock call must never be made.
	f := &fake{outs: []string{inspectOK, good + "ACE|" + file + "|BUILTIN\\Administrators|FullControl|Allow\n"}}
	if err := run(context.Background(), cfg(t, line), f.run, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "grants access to") || !strings.Contains(err.Error(), "NOT removed") {
		t.Errorf("run err = %v", err)
	}
	if len(f.calls) != 2 {
		t.Errorf("%d calls: the lock ran after a failed check", len(f.calls))
	}
}

func TestCobra(t *testing.T) {
	_, line := newKey(t)
	old := defaultRunner
	defer func() { defaultRunner = old }()
	f := &fake{outs: []string{inspectOK, verifyOut("std", line), lockOut}}
	defaultRunner = f.run
	root := RootCmd()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs([]string{"install", "--ssh-port", "2222", "--admin", "reader", "--user", "std", "--pubkey", keyFile(t, line)})
	if err := root.Execute(); err != nil || !strings.Contains(out.String(), "installed:") {
		t.Fatalf("err %v out %s", err, out.String())
	}
	root = RootCmd()
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs([]string{"install", "--user", "std"})
	if err := root.Execute(); err == nil {
		t.Error("missing required flags accepted")
	}
}

func TestMain_(t *testing.T) {
	oldArgs, oldExit := os.Args, osExit
	defer func() { os.Args, osExit = oldArgs, oldExit }()
	code := -1
	osExit = func(c int) { code = c }
	os.Args = []string{"vmkey", "install"}
	main()
	if code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	code = -1
	os.Args = []string{"vmkey", "--help"}
	main()
	if code != -1 {
		t.Errorf("--help exited %d", code)
	}
}

func TestExecRunner(t *testing.T) {
	out, err := execRunner(context.Background(), []string{"cat"}, []byte("hi"))
	if err != nil || string(out) != "hi" {
		t.Errorf("out %q err %v", out, err)
	}
	_, err = execRunner(context.Background(), []string{"sh", "-c", "echo " + strings.Repeat("x", 500) + " >&2; exit 3"}, nil)
	if err == nil || len(err.Error()) > 500 {
		t.Errorf("err = %v", err)
	}
	_, err = execRunner(context.Background(), []string{"vmkey-no-such-program"}, nil)
	if err == nil {
		t.Error("missing program accepted")
	}
}

func TestLoopback(t *testing.T) {
	for h, want := range map[string]bool{"127.0.0.1": true, "127.5.5.5": true, "::1": true, "localhost": true, "0.0.0.0": false, "192.168.0.1": false, "": false, "evil.example": false} {
		if loopback(h) != want {
			t.Errorf("loopback(%q) = %v", h, !want)
		}
	}
}
