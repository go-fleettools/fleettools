// Command vmkey installs one SSH public key for one NON-administrator local
// user on a Windows virtual machine reached through a loopback port forward.
//
//	vmkey install --ssh-port 2222 --admin reader --user std \
//	    --pubkey ~/.ssh/win11vm_std_ed25519.pub [--dry-run]
//
// It exists so that this one act can be granted narrowly: the tool can talk
// only to a loopback address, can install only a single parsed public key,
// can never target an account that is in the Administrators group, and writes
// only C:\Users\<user>\.ssh\authorized_keys with an ACL of <user> and SYSTEM.
// ssh runs through exec argv, never a shell string built from input; the key
// travels on stdin; every remote action is printed before it is taken.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

var osExit = os.Exit

// nameRE accepts a Windows account name that can neither start an ssh option
// nor carry anything a PowerShell string would interpret.
var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,19}$`)

// maxKeyFile bounds the public-key file.
const maxKeyFile = 16 << 10

// runner runs argv with stdin and returns its stdout. Tests replace it.
type runner func(ctx context.Context, argv []string, stdin []byte) ([]byte, error)

var defaultRunner runner = execRunner

func execRunner(ctx context.Context, argv []string, stdin []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 400 {
			msg = msg[:400]
		}
		return nil, fmt.Errorf("%s: %w: %s", argv[0], err, msg)
	}
	return stdout.Bytes(), nil
}

type config struct {
	host, admin, user, pubkey string
	port                      int
	dryRun                    bool
}

// RootCmd is the command tree, driven by tests through SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "vmkey",
		Short:         "install an SSH public key for a non-admin user on a loopback Windows VM",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	var c config
	install := &cobra.Command{
		Use:   "install",
		Short: "append one public key to C:\\Users\\<user>\\.ssh\\authorized_keys",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), c, defaultRunner, cmd.OutOrStdout())
		},
	}
	f := install.Flags()
	f.StringVar(&c.host, "host", "127.0.0.1", "loopback address of the port-forwarded VM (127.0.0.0/8, ::1 or localhost only)")
	f.IntVar(&c.port, "ssh-port", 0, "ssh port of the VM on the loopback address")
	f.StringVar(&c.admin, "admin", "", "administrator account used to log in")
	f.StringVar(&c.user, "user", "", "non-administrator local account that receives the key")
	f.StringVar(&c.pubkey, "pubkey", "", "file holding ONE OpenSSH public key")
	f.BoolVar(&c.dryRun, "dry-run", false, "print every action and send nothing")
	for _, n := range []string{"ssh-port", "admin", "user", "pubkey"} {
		_ = install.MarkFlagRequired(n)
	}
	root.AddCommand(install)
	return root
}

func main() {
	cmd := RootCmd()
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "vmkey:", err)
		osExit(1)
	}
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// readKey returns the canonical authorized_keys line of the ONE public key in
// the file. Private-key material, options, several keys and control
// characters are refused.
func readKey(path string) (ssh.PublicKey, string, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer fh.Close()
	b, err := io.ReadAll(io.LimitReader(fh, maxKeyFile+1))
	if err != nil {
		return nil, "", err
	}
	if len(b) > maxKeyFile {
		return nil, "", errors.New("pubkey file is larger than 16 KiB")
	}
	if bytes.Contains(b, []byte("PRIVATE KEY")) {
		return nil, "", errors.New("pubkey file holds private-key material; refusing to read further")
	}
	key, comment, opts, rest, err := ssh.ParseAuthorizedKey(b)
	if err != nil {
		return nil, "", fmt.Errorf("pubkey file is not an OpenSSH public key: %w", err)
	}
	if len(opts) > 0 {
		return nil, "", errors.New("pubkey carries authorized_keys options; refusing")
	}
	if len(bytes.TrimSpace(rest)) > 0 {
		return nil, "", errors.New("pubkey file must hold exactly ONE key")
	}
	for _, r := range comment {
		if unicode.IsControl(r) {
			return nil, "", errors.New("pubkey comment has a control character")
		}
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	if comment != "" {
		line += " " + comment
	}
	return key, line, nil
}

type step struct {
	what   string
	script string
	stdin  []byte
}

func (c config) argv(script string) []string {
	return []string{"ssh", "-p", strconv.Itoa(c.port), "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
		c.admin + "@" + c.host, script}
}

func (c config) dir() string { return `C:\Users\` + c.user + `\.ssh` }

func (c config) inspect() step {
	f := c.dir() + `\authorized_keys`
	return step{what: "read-only: OS, the account, the Administrators members, the profile and the existing keys", script: `$ErrorActionPreference='Stop'; ` +
		`'OS|'+[Environment]::OSVersion.Platform; ` +
		`$u=Get-LocalUser -Name '` + c.user + `'; 'USER|'+$u.Name; ` +
		`Get-LocalGroupMember -Group 'Administrators' | ForEach-Object { 'ADMIN|'+$_.Name }; ` +
		`'PROFILE|'+(Test-Path 'C:\Users\` + c.user + `'); ` +
		`if (Test-Path '` + f + `') { Get-Content '` + f + `' | ForEach-Object { 'KEY|'+$_ } }`}
}

func (c config) write(line string) step {
	d, f := c.dir(), c.dir()+`\authorized_keys`
	return step{what: "append the key to " + f + " and set owner " + c.user + ", ACL " + c.user + ":F SYSTEM:F, inheritance removed",
		stdin: []byte(line + "\n"),
		script: `$ErrorActionPreference='Stop'; ` +
			`function I { icacls @args | Out-Null; if ($LASTEXITCODE -ne 0) { throw "icacls failed: $args" } }; ` +
			`$k=[Console]::In.ReadToEnd().Trim(); $d='` + d + `'; $f='` + f + `'; ` +
			`New-Item -ItemType Directory -Force -Path $d | Out-Null; ` +
			`if (-not (Test-Path $f)) { New-Item -ItemType File -Path $f | Out-Null }; ` +
			`$raw=Get-Content -Raw $f; if ($raw -and -not $raw.EndsWith("` + "`n" + `")) { Add-Content -Path $f -Value '' -Encoding ascii }; ` +
			`Add-Content -Path $f -Value $k -Encoding ascii; ` +
			`foreach ($p in $d,$f) { I $p /reset; I $p /setowner '` + c.user + `'; I $p /inheritance:r }; ` +
			`I $d /grant:r '` + c.user + `:(OI)(CI)F' 'SYSTEM:(OI)(CI)F'; ` +
			`I $f /grant:r '` + c.user + `:F' 'SYSTEM:F'`}
}

func (c config) verify() step {
	d, f := c.dir(), c.dir()+`\authorized_keys`
	return step{what: "read-only: owner, ACL and keys of " + d, script: `$ErrorActionPreference='Stop'; $d='` + d + `'; $f='` + f + `'; ` +
		`foreach ($p in $d,$f) { $a=Get-Acl $p; 'OWNER|'+$p+'|'+$a.Owner; 'PROT|'+$p+'|'+$a.AreAccessRulesProtected; ` +
		`foreach ($r in $a.Access) { 'ACE|'+$p+'|'+$r.IdentityReference.Value+'|'+$r.FileSystemRights+'|'+$r.AccessControlType } }; ` +
		`Get-Content $f | ForEach-Object { 'KEY|'+$_ }`}
}

// tag returns the values of the lines "TAG|value".
func tag(out []byte, t string) []string {
	var vs []string
	for _, l := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimRight(l, "\r"), t+"|"); ok {
			vs = append(vs, v)
		}
	}
	return vs
}

func hasKey(out []byte, key ssh.PublicKey) bool {
	for _, l := range tag(out, "KEY") {
		if k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(l)); err == nil && bytes.Equal(k.Marshal(), key.Marshal()) {
			return true
		}
	}
	return false
}

func account(name string) string {
	if i := strings.LastIndexByte(name, '\\'); i >= 0 {
		name = name[i+1:]
	}
	return strings.ToLower(name)
}

// checkInspect applies the refusals that need the VM's answer.
func (c config) checkInspect(out []byte) error {
	if osv := tag(out, "OS"); len(osv) != 1 || strings.TrimSpace(osv[0]) != "Win32NT" {
		return fmt.Errorf("the target is not a Windows host answering in PowerShell (OS = %q); refusing", osv)
	}
	if u := tag(out, "USER"); len(u) != 1 || !strings.EqualFold(strings.TrimSpace(u[0]), c.user) {
		return fmt.Errorf("local user %q does not exist on the target", c.user)
	}
	for _, m := range tag(out, "ADMIN") {
		if account(strings.TrimSpace(m)) == strings.ToLower(c.user) {
			return fmt.Errorf("%q is a member of Administrators; vmkey never installs a key for an administrator", c.user)
		}
	}
	if p := tag(out, "PROFILE"); len(p) != 1 || strings.TrimSpace(p[0]) != "True" {
		return fmt.Errorf("C:\\Users\\%s does not exist; the user must log on once", c.user)
	}
	return nil
}

// checkVerify confirms what the write did: owner, protected ACL, exactly the
// user and SYSTEM with full control, and the key present.
func (c config) checkVerify(out []byte, key ssh.PublicKey) error {
	paths := []string{c.dir(), c.dir() + `\authorized_keys`}
	for _, p := range paths {
		owners := 0
		for _, v := range tag(out, "OWNER") {
			if path, o, ok := strings.Cut(strings.TrimSpace(v), "|"); ok && path == p {
				owners++
				if account(o) != strings.ToLower(c.user) {
					return fmt.Errorf("%s is owned by %q, not %q", p, o, c.user)
				}
			}
		}
		prot := 0
		for _, v := range tag(out, "PROT") {
			if path, o, ok := strings.Cut(strings.TrimSpace(v), "|"); ok && path == p {
				prot++
				if o != "True" {
					return fmt.Errorf("%s still inherits permissions", p)
				}
			}
		}
		seen := map[string]bool{}
		for _, v := range tag(out, "ACE") {
			parts := strings.Split(strings.TrimSpace(v), "|")
			if len(parts) != 4 || parts[0] != p {
				continue
			}
			who := strings.ToLower(parts[1])
			if who != "nt authority\\system" && account(parts[1]) != strings.ToLower(c.user) {
				return fmt.Errorf("%s grants access to %q", p, parts[1])
			}
			if parts[3] != "Allow" || !strings.Contains(parts[2], "FullControl") {
				return fmt.Errorf("%s: unexpected rule %q", p, v)
			}
			seen[who] = true
		}
		if owners != 1 || prot != 1 || len(seen) != 2 {
			return fmt.Errorf("%s: ACL not as expected (owner lines %d, protection lines %d, principals %d)", p, owners, prot, len(seen))
		}
	}
	if !hasKey(out, key) {
		return errors.New("the key is not in authorized_keys after the write")
	}
	return nil
}

func run(ctx context.Context, c config, r runner, out io.Writer) error {
	if !loopback(c.host) {
		return fmt.Errorf("host %q is not a loopback address; vmkey reaches only port-forwarded local VMs", c.host)
	}
	if c.port < 1 || c.port > 65535 {
		return fmt.Errorf("ssh port %d is out of range", c.port)
	}
	if !nameRE.MatchString(c.admin) {
		return fmt.Errorf("admin %q is not a valid account name", c.admin)
	}
	if !nameRE.MatchString(c.user) {
		return fmt.Errorf("user %q is not a valid account name", c.user)
	}
	if strings.EqualFold(c.admin, c.user) {
		return errors.New("--admin and --user are the same account")
	}
	key, line, err := readKey(c.pubkey)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "target: %s@%s port %d (loopback ssh, BatchMode, no password)\n", c.admin, c.host, c.port)
	fmt.Fprintf(out, "key: %s (%s)\nfor user: %s (must exist, must not be an Administrator)\n", key.Type(), ssh.FingerprintSHA256(key), c.user)

	do := func(s step) ([]byte, error) {
		fmt.Fprintf(out, "remote action: %s\n  %s\n", s.what, s.script)
		if c.dryRun {
			return nil, nil
		}
		return r(ctx, c.argv(s.script), s.stdin)
	}

	got, err := do(c.inspect())
	if err != nil {
		return err
	}
	if c.dryRun {
		fmt.Fprintln(out, "dry run: nothing was sent; the write and the verification would follow if the checks pass")
		fmt.Fprintf(out, "remote action: %s\n  %s\n", c.write(line).what, c.write(line).script)
		fmt.Fprintf(out, "remote action: %s\n  %s\n", c.verify().what, c.verify().script)
		return nil
	}
	if err := c.checkInspect(got); err != nil {
		return err
	}
	if hasKey(got, key) {
		fmt.Fprintln(out, "the key is already in authorized_keys: nothing to do")
		return nil
	}
	if _, err := do(c.write(line)); err != nil {
		return err
	}
	got, err = do(c.verify())
	if err != nil {
		return err
	}
	if err := c.checkVerify(got, key); err != nil {
		return err
	}
	fmt.Fprintf(out, "installed: %s\\authorized_keys now holds the key; owner %s, ACL %s and SYSTEM only\n", c.dir(), c.user, c.user)
	return nil
}
