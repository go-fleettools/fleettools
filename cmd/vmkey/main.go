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

	"github.com/go-fleettools/fleettools/internal/fleet"
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
	f.StringVar(&c.host, "host", "localhost", "loopback name or address of the port-forwarded VM (localhost, 127.0.0.0/8 or ::1 only); the name must be the one known_hosts has")
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
	fleet.WarnIfStale(os.Stderr)
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
		// An earlier locked-down state leaves the administrator without
		// access: report it instead of failing, the write step recovers.
		`try { if (Test-Path '` + f + `') { Get-Content '` + f + `' -ErrorAction Stop | ForEach-Object { 'KEY|'+$_ } } } catch { 'KEYS|unreadable' }`}
}

// write is the first of two steps and never removes inheritance: removing it
// from a directory that has no explicit entry yet locks the administrator out
// (measured 2026-11-17: icacls /reset on the file then failed with Access is
// denied). It recovers an earlier lock-out, appends the key once, grants the
// user and SYSTEM EXPLICIT entries and the ownership while the administrator
// still has access through inheritance, and prints what it set so that Go can
// check it before lock() is allowed to run.
func (c config) write(line string) step {
	d, f := c.dir(), c.dir()+`\authorized_keys`
	return step{what: "recover access if an earlier run locked the administrator out, append the key once to " + f + ", grant " + c.user + ":F and SYSTEM:F explicitly, set owner " + c.user + ", and print the result (inheritance is NOT removed yet)",
		stdin: []byte(line + "\n"),
		script: `$ErrorActionPreference='Stop'; ` +
			`function I { icacls @args | Out-Null; if ($LASTEXITCODE -ne 0) { throw "icacls failed: $args" } }; ` +
			`$k=[Console]::In.ReadToEnd().Trim(); $d='` + d + `'; $f='` + f + `'; ` +
			`$denied=$false; try { if (Test-Path -LiteralPath $d) { Get-ChildItem -Force -LiteralPath $d -ErrorAction Stop | Out-Null; ` +
			`if (Test-Path -LiteralPath $f) { Get-Content -Raw -LiteralPath $f -ErrorAction Stop | Out-Null } } } catch { $denied=$true }; ` +
			`if ($denied) { 'RECOVER|the administrator had no access to '+$d+'; taking ownership back and resetting its permissions'; ` +
			`takeown /f $d /r /d y | Out-Null; if ($LASTEXITCODE -ne 0) { throw 'takeown failed' }; I $d /reset /T }; ` +
			`New-Item -ItemType Directory -Force -Path $d | Out-Null; ` +
			`$kb=($k -split '\s+')[1]; $seen=@{}; $out=@(); ` +
			`if (Test-Path -LiteralPath $f) { foreach ($l in @(Get-Content -LiteralPath $f | Where-Object { $_.Trim() })) { ` +
			`if ($l.TrimStart().StartsWith('#')) { $out+=$l; continue }; $b=($l -split '\s+')[1]; ` +
			`if (-not $b) { $out+=$l; continue }; if (-not $seen[$b]) { $seen[$b]=$true; $out+=$l } } }; ` +
			`if (-not $seen[$kb]) { $out+=$k }; Set-Content -LiteralPath $f -Value $out -Encoding ascii; ` +
			`I $f /grant:r '` + c.user + `:F' 'SYSTEM:F'; I $f /setowner '` + c.user + `'; ` +
			`I $d /grant:r '` + c.user + `:(OI)(CI)F' 'SYSTEM:(OI)(CI)F'; I $d /setowner '` + c.user + `'; ` +
			`foreach ($p in $d,$f) { $a=Get-Acl $p; 'OWNER|'+$p+'|'+$a.Owner; ` +
			`foreach ($r in $a.Access) { if (-not $r.IsInherited) { 'ACE|'+$p+'|'+$r.IdentityReference.Value+'|'+$r.FileSystemRights+'|'+$r.AccessControlType } } }; ` +
			`Get-Content -LiteralPath $f | ForEach-Object { 'KEY|'+$_ }`}
}

// lock removes inheritance, file first and then the directory, once write's
// result has been checked. Explicit entries exist by then, so nothing but the
// inherited entries (the administrator's) goes away.
func (c config) lock() step {
	d, f := c.dir(), c.dir()+`\authorized_keys`
	return step{what: "remove inheritance from " + f + " and then " + d + " (the administrator loses access; only " + c.user + " and SYSTEM keep it)",
		script: `$ErrorActionPreference='Stop'; ` +
			`function I { icacls @args | Out-Null; if ($LASTEXITCODE -ne 0) { throw "icacls failed: $args" } }; ` +
			`I '` + f + `' /inheritance:r; 'LOCKED|` + f + `'; I '` + d + `' /inheritance:r; 'LOCKED|` + d + `'`}
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

// checkVerify confirms what the write step printed, BEFORE inheritance is
// removed: owner, exactly the user and SYSTEM as EXPLICIT entries with full
// control, and the key present exactly once.
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
		if owners != 1 || len(seen) != 2 {
			return fmt.Errorf("%s: ACL not as expected (owner lines %d, explicit principals %d)", p, owners, len(seen))
		}
	}
	if n := countKey(out, key); n != 1 {
		return fmt.Errorf("the key appears %d times in authorized_keys after the write, want exactly 1", n)
	}
	return nil
}

func countKey(out []byte, key ssh.PublicKey) int {
	n := 0
	for _, l := range tag(out, "KEY") {
		if k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(l)); err == nil && bytes.Equal(k.Marshal(), key.Marshal()) {
			n++
		}
	}
	return n
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
		o, err := r(ctx, c.argv(s.script), s.stdin)
		if err != nil && strings.Contains(err.Error(), "Host key verification failed") {
			err = fmt.Errorf("%w\n  hint: known_hosts may know this VM only under another name; ssh checks the name you give it (--host %s), try --host localhost", err, c.host)
		}
		return o, err
	}

	got, err := do(c.inspect())
	if err != nil {
		return err
	}
	if c.dryRun {
		fmt.Fprintln(out, "dry run: nothing was sent; the write, its verification and then the lock would follow if the checks pass")
		fmt.Fprintf(out, "remote action: %s\n  %s\n", c.write(line).what, c.write(line).script)
		fmt.Fprintf(out, "remote action: %s\n  %s\n", c.lock().what, c.lock().script)
		return nil
	}
	if err := c.checkInspect(got); err != nil {
		return err
	}
	if hasKey(got, key) {
		fmt.Fprintln(out, "the key is already in authorized_keys: nothing to do")
		return nil
	}
	got, err = do(c.write(line))
	if err != nil {
		return err
	}
	for _, m := range tag(got, "RECOVER") {
		fmt.Fprintf(out, "recovered: %s\n", strings.TrimSpace(m))
	}
	// The lock is only allowed to run on a result that has been checked: once
	// inheritance is gone the administrator can no longer read it back.
	if err := c.checkVerify(got, key); err != nil {
		return fmt.Errorf("%w (inheritance was NOT removed; nothing was locked)", err)
	}
	got, err = do(c.lock())
	if err != nil {
		return err
	}
	if n := len(tag(got, "LOCKED")); n != 2 {
		return fmt.Errorf("the lock step confirmed %d of 2 paths", n)
	}
	fmt.Fprintf(out, "installed: %s\\authorized_keys holds the key once; owner %s, explicit ACL %s and SYSTEM only, inheritance removed (the administrator can no longer read it, by design)\n", c.dir(), c.user, c.user)
	return nil
}
