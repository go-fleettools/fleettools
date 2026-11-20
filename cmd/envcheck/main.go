// Command envcheck runs an environment's checks, from a file kept in its deploy
// repository, the same way against every environment, and writes the result as
// VALIDATION.md.
//
// It exists because the checks of a new environment were typed by hand: the
// page requests, `authn-bridge check`, and a turnprobe whose username has to be
// minted first with `turnd mint-credentials`. Typed by hand they differ between
// local, staging and production, and a validation record nobody can reproduce
// is not a gate.
//
// Every check runs ON THE TARGET, through an "exec" prefix the file names:
// `[tart, exec, vm, sudo]` for a VM the host cannot reach by IP, `[ssh, host]`
// for a server, empty for this machine. So one file shape serves all three.
// A file with `protected: true` (production) is refused unless --confirm names
// its environment: production is checked only when somebody asked for it.
//
// Exit status: 0 every check passed, 1 a check failed, 2 the checks could not
// be run (unreadable file, a command that would not start, a refused
// protected environment).
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

const longHelp = `Reads a checks file (kept in the deploy repository, for instance
environments/<env>/checks.yml) and runs every check on the target through the
file's "exec" prefix:

    environment: local
    protected: false                    # true for production: needs --confirm local
    exec: [tart, exec, mesocentre-local, sudo]
    quote_remote: false                 # true for ssh: join the command into one quoted string
    ca_cert: /mnt/shared/artifacts/root.crt   # curl --cacert, optional
    pages:
      - {url: https://login.example.test/, expect: 200}
      - {url: https://login.example.test/nope, expect: 404}
    turn:
      turnd: /usr/local/bin/turnd
      turnprobe: /usr/local/bin/turnprobe
      secret_file: /etc/turnd/turn.secret
      realm: turn.example.test
      servers: ["turn:turn.example.test:3478?transport=udp", "turns:turn.example.test:443?transport=tcp"]
    commands:
      - {name: authn-bridge check, run: [runuser, -u, authn-bridge, --, /usr/local/bin/authn-bridge, check, --config, /etc/authn-bridge]}

Pages are fetched with curl on the target. For TURN it mints its own
short-lived credentials with "turnd mint-credentials" on the target (the
password goes to a 0600 file made for this run and removed at the end, and is
never printed), then runs turnprobe over every server. The result is written
to VALIDATION.md beside the checks file (--out changes it).

Exit status: 0 every check passed, 1 a check failed, 2 they could not be run.`

// spec is the checks file. Unknown keys are refused: a misspelt "expect" must
// not become a check that expects nothing.
type spec struct {
	Environment string    `yaml:"environment"`
	Protected   bool      `yaml:"protected"`
	Exec        []string  `yaml:"exec"`
	QuoteRemote bool      `yaml:"quote_remote"`
	CACert      string    `yaml:"ca_cert"`
	Pages       []page    `yaml:"pages"`
	Turn        *turnSpec `yaml:"turn"`
	Commands    []command `yaml:"commands"`
}

type page struct {
	URL    string `yaml:"url"`
	Expect int    `yaml:"expect"`
}

type turnSpec struct {
	Turnd      string   `yaml:"turnd"`
	Turnprobe  string   `yaml:"turnprobe"`
	SecretFile string   `yaml:"secret_file"`
	Realm      string   `yaml:"realm"`
	Servers    []string `yaml:"servers"`
	TTL        string   `yaml:"ttl"`
	PwDir      string   `yaml:"password_dir"`
}

type command struct {
	Name       string   `yaml:"name"`
	Run        []string `yaml:"run"`
	ExpectExit int      `yaml:"expect_exit"`
}

// runner runs argv on the target. err is for a command that could not be
// started, which is not a failed check but a check that could not be made.
type runner func(argv []string) (stdout, stderr string, code int, err error)

// Seams for the tests: the runner, the clock, the random source.
var (
	newRunner = execRunner
	now       = time.Now
	randHex   = func() string {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			panic(err) // no entropy: nothing safe to do
		}
		return hex.EncodeToString(b)
	}
)

const outputCap = 64 << 10

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command; tests drive it through run.
func RootCmd() *cobra.Command {
	var out, confirm string
	c := cli.New("envcheck [flags] <checks.yml>",
		"Run an environment's checks on its target and write VALIDATION.md", longHelp)
	c.Args = cobra.ExactArgs(1)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return cli.FromStatus(check(cli.Out(cmd), cli.Err(cmd), args[0], out, confirm))
	}
	c.Flags().StringVar(&out, "out", "", "report path (default VALIDATION.md beside the checks file)")
	c.Flags().StringVar(&confirm, "confirm", "", "the environment name, required when the file says protected: true")
	return c
}

func run(args []string, out, errOut io.Writer) int {
	return cli.Drive(RootCmd(), args, out, errOut)
}

type result struct {
	Name   string
	Shown  string // the command, with credentials redacted
	Output string
	Pass   bool
	Detail string // why it failed
}

func check(stdout, stderr io.Writer, file, outPath, confirm string) int {
	raw, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(stderr, "envcheck: %v\n", err)
		return 2
	}
	var s spec
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		fmt.Fprintf(stderr, "envcheck: %s: %v\n", file, err)
		return 2
	}
	if err := s.validate(); err != nil {
		fmt.Fprintf(stderr, "envcheck: %s: %v\n", file, err)
		return 2
	}
	if s.Protected && confirm != s.Environment {
		fmt.Fprintf(stderr, "envcheck: %q is protected: it is checked only when asked for; pass --confirm %s\n", s.Environment, s.Environment)
		return 2
	}
	if outPath == "" {
		outPath = filepath.Join(filepath.Dir(file), "VALIDATION.md")
	}
	run := newRunner(s.Exec, s.QuoteRemote)

	var results []result
	add := func(r result, err error) bool {
		if err != nil {
			fmt.Fprintf(stderr, "envcheck: %s: could not run: %v\n", r.Name, err)
			return false
		}
		results = append(results, r)
		return true
	}
	for _, p := range s.Pages {
		if !add(checkPage(run, s.CACert, p)) {
			return 2
		}
	}
	if s.Turn != nil {
		rs, err := checkTurn(run, s.Turn)
		if err != nil {
			fmt.Fprintf(stderr, "envcheck: turn: could not run: %v\n", err)
			return 2
		}
		results = append(results, rs...)
	}
	for _, c := range s.Commands {
		if !add(checkCommand(run, c)) {
			return 2
		}
	}

	failed := 0
	for _, r := range results {
		verdict := "PASS"
		if !r.Pass {
			verdict = "FAIL"
			failed++
		}
		fmt.Fprintf(stdout, "%s %s\n", verdict, r.Name)
		if !r.Pass {
			fmt.Fprintf(stdout, "     %s\n", r.Detail)
		}
	}
	if err := writeReport(outPath, &s, results, now()); err != nil {
		fmt.Fprintf(stderr, "envcheck: %v\n", err)
		return 2
	}
	if failed > 0 {
		fmt.Fprintf(stdout, "%d of %d checks FAILED; report: %s\n", failed, len(results), outPath)
		return 1
	}
	fmt.Fprintf(stdout, "all %d checks passed; report: %s\n", len(results), outPath)
	return 0
}

func (s *spec) validate() error {
	if s.Environment == "" {
		return errors.New("environment: missing")
	}
	n := len(s.Pages) + len(s.Commands)
	if s.Turn != nil {
		n++
		t := s.Turn
		if t.Turnd == "" || t.Turnprobe == "" || t.SecretFile == "" || len(t.Servers) == 0 {
			return errors.New("turn: turnd, turnprobe, secret_file and servers are all required")
		}
		if t.TTL != "" {
			if _, err := time.ParseDuration(t.TTL); err != nil {
				return fmt.Errorf("turn.ttl: %v", err)
			}
		}
	}
	if n == 0 {
		return errors.New("no checks: a file that checks nothing would always pass")
	}
	for _, p := range s.Pages {
		if p.URL == "" || p.Expect < 100 || p.Expect > 599 {
			return fmt.Errorf("page %q: needs a url and an expect between 100 and 599", p.URL)
		}
	}
	for _, c := range s.Commands {
		if c.Name == "" || len(c.Run) == 0 {
			return errors.New("command: needs a name and a run list")
		}
	}
	return nil
}

func checkPage(run runner, ca string, p page) (result, error) {
	argv := []string{"curl", "-sS", "--max-time", "20", "-o", "/dev/null", "-w", "%{http_code}"}
	if ca != "" {
		argv = append(argv, "--cacert", ca)
	}
	argv = append(argv, p.URL)
	r := result{Name: fmt.Sprintf("GET %s expects %d", p.URL, p.Expect), Shown: display(argv)}
	stdout, stderr, code, err := run(argv)
	if err != nil {
		return r, err
	}
	r.Output = strings.TrimSpace(stdout + stderr)
	got := strings.TrimSpace(stdout)
	switch {
	case code != 0:
		r.Detail = fmt.Sprintf("curl exited %d: %s", code, strings.TrimSpace(stderr))
	case got != fmt.Sprint(p.Expect):
		r.Detail = fmt.Sprintf("got %s, expected %d", got, p.Expect)
	default:
		r.Pass = true
	}
	return r, nil
}

func checkCommand(run runner, c command) (result, error) {
	r := result{Name: c.Name, Shown: display(c.Run)}
	stdout, stderr, code, err := run(c.Run)
	if err != nil {
		return r, err
	}
	r.Output = strings.TrimSpace(stdout + stderr)
	if code != c.ExpectExit {
		r.Detail = fmt.Sprintf("exit %d, expected %d", code, c.ExpectExit)
		return r, nil
	}
	r.Pass = true
	return r, nil
}

// checkTurn mints credentials on the target, probes every server, and removes
// the password file whatever happened. Minting and removing are steps of the
// check, not checks of their own: a failed mint is reported as the probe's
// failure, and removal that fails is reported too.
func checkTurn(run runner, t *turnSpec) ([]result, error) {
	ttl := t.TTL
	if ttl == "" {
		ttl = "10m"
	}
	dir := t.PwDir
	if dir == "" {
		dir = "/run"
	}
	pw := strings.TrimRight(dir, "/") + "/envcheck-" + randHex() + ".pw"
	mint := []string{t.Turnd, "mint-credentials", "--turn-secret-file", t.SecretFile,
		"--subject", "envcheck", "--ttl", ttl, "--password-out", pw}
	r := result{Name: "turnprobe: " + strings.Join(t.Servers, ", ")}

	stdout, stderr, code, err := run(mint)
	if err != nil {
		return nil, err
	}
	// The file may exist even when minting failed half way; remove it anyway.
	defer func() { _, _, _, _ = run([]string{"rm", "-f", pw}) }()
	user := strings.TrimSpace(stdout)
	if code != 0 || user == "" || strings.ContainsAny(user, " \t\n") {
		r.Shown = display(mint)
		r.Output = strings.TrimSpace(stderr)
		r.Detail = fmt.Sprintf("turnd mint-credentials exited %d with username %q", code, user)
		return []result{r}, nil
	}
	probe := []string{t.Turnprobe, "--username", user, "--password-file", pw}
	if t.Realm != "" {
		probe = append(probe, "--realm", t.Realm)
	}
	for _, sv := range t.Servers {
		probe = append(probe, "--server", sv)
	}
	r.Shown = display(probe)
	stdout, stderr, code, err = run(probe)
	if err != nil {
		return nil, err
	}
	r.Output = strings.TrimSpace(stdout + stderr)
	pass, fail := 0, 0
	for _, line := range strings.Split(stdout, "\n") {
		switch {
		case strings.HasPrefix(line, "PASS "):
			pass++
		case strings.HasPrefix(line, "FAIL "):
			fail++
		}
	}
	switch {
	case code != 0:
		r.Detail = fmt.Sprintf("turnprobe exited %d", code)
	case fail > 0 || pass != len(t.Servers):
		// Exit 0 with a missing or failing line is not a pass: count the lines.
		r.Detail = fmt.Sprintf("%d PASS and %d FAIL lines for %d servers", pass, fail, len(t.Servers))
	default:
		r.Pass = true
	}
	return []result{r}, nil
}

// secretFlags take a value that must not reach the report. The username is not
// secret once expired, but nothing in a record needs it.
var secretFlags = map[string]bool{"--username": true, "--password-file": true, "--password-out": true}

func display(argv []string) string {
	parts := make([]string, len(argv))
	redact := false
	for i, a := range argv {
		if redact {
			parts[i], redact = "<redacted>", false
			continue
		}
		parts[i] = a
		redact = secretFlags[a]
	}
	return strings.Join(parts, " ")
}

// quote joins argv into one string a POSIX shell reads back as the same words.
func quote(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(parts, " ")
}

func execRunner(prefix []string, quoteRemote bool) runner {
	return func(argv []string) (string, string, int, error) {
		full := append([]string{}, prefix...)
		if quoteRemote {
			full = append(full, quote(argv))
		} else {
			full = append(full, argv...)
		}
		cmd := exec.Command(full[0], full[1:]...)
		var o, e capped
		cmd.Stdout, cmd.Stderr = &o, &e
		cmd.Stdin = nil
		err := cmd.Run()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return o.String(), e.String(), ee.ExitCode(), nil
		}
		if err != nil {
			return "", "", -1, err
		}
		return o.String(), e.String(), 0, nil
	}
}

// capped keeps the first outputCap bytes and drops the rest.
type capped struct{ b bytes.Buffer }

func (c *capped) Write(p []byte) (int, error) {
	if room := outputCap - c.b.Len(); room > 0 {
		c.b.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}
func (c *capped) String() string { return c.b.String() }

func writeReport(path string, s *spec, rs []result, at time.Time) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Validation of `%s`\n\n", s.Environment)
	fmt.Fprintf(&b, "Run by envcheck on %s", at.Format("2006-01-02 15:04 MST"))
	if len(s.Exec) > 0 {
		fmt.Fprintf(&b, ", on the target through `%s`", strings.Join(s.Exec, " "))
	}
	b.WriteString(".\n\n")
	failed := 0
	for _, r := range rs {
		if !r.Pass {
			failed++
		}
	}
	if failed == 0 {
		fmt.Fprintf(&b, "**Result: all %d checks passed.**\n\n", len(rs))
	} else {
		fmt.Fprintf(&b, "**Result: %d of %d checks FAILED.**\n\n", failed, len(rs))
	}
	for _, r := range rs {
		v := "PASS"
		if !r.Pass {
			v = "FAIL"
		}
		fmt.Fprintf(&b, "## %s %s\n\n```text\n$ %s\n%s\n```\n\n", v, r.Name, r.Shown, r.Output)
		if !r.Pass {
			fmt.Fprintf(&b, "%s\n\n", r.Detail)
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".VALIDATION-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
