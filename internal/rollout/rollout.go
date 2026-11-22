// Package rollout holds what the commands that land one change as a pull request
// per repository (`dashfix`, `orgedit`) share: the subprocess seam, the hourly
// ledger, the REST reserve check and the wait for a pushed head.
package rollout

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Call runs a program with stdin closed. A non-zero exit is an error carrying
// the program's own words. Tests fake the programs by putting stand-ins first
// on PATH.
func Call(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		return buf.String(), fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(buf.String()))
	}
	return buf.String(), nil
}

// Ledger records one merged pull request per line: unix seconds, a space, the
// target.
type Ledger struct {
	Path string
	Now  func() time.Time
}

// InLastHour counts lines newer than an hour. A missing ledger is zero; an
// unreadable line is an error.
func (l Ledger) InLastHour() (int, error) {
	b, err := os.ReadFile(l.Path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		sec, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("ledger line %q is unreadable", line)
		}
		if l.Now().Sub(time.Unix(sec, 0)) < time.Hour {
			n++
		}
	}
	return n, nil
}

// Record appends a line for target.
func (l Ledger) Record(target string) error {
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(l.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%d %s\n", l.Now().Unix(), target)
	return err
}

// Budget refuses to start another pull request when the REST reserve is spent
// or the hourly cap is reached; an unreadable answer is a refusal too. It
// returns 0 to go on and 2 to stop, after saying why on stderr as `name: ...`.
func Budget(stderr io.Writer, name string, l Ledger, reserve, max int) int {
	out, err := Call("", "gh", "api", "rate_limit")
	if err != nil {
		fmt.Fprintf(stderr, "%s: could not read the rate limit: %v\n", name, err)
		return 2
	}
	var rl struct {
		Resources struct {
			Core struct {
				Remaining *int `json:"remaining"`
			} `json:"core"`
		} `json:"resources"`
	}
	if json.Unmarshal([]byte(out), &rl) != nil || rl.Resources.Core.Remaining == nil {
		fmt.Fprintf(stderr, "%s: the rate limit answer was unreadable\n", name)
		return 2
	}
	if *rl.Resources.Core.Remaining < reserve {
		fmt.Fprintf(stderr, "%s: stopped at the budget reserve: %d left, %d reserved\n",
			name, *rl.Resources.Core.Remaining, reserve)
		return 2
	}
	n, err := l.InLastHour()
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 2
	}
	if n >= max {
		fmt.Fprintf(stderr, "%s: stopped at the hourly cap: %d merged in the last hour, cap %d\n", name, n, max)
		return 2
	}
	return 0
}

// Overridable by tests.
var (
	HeadPollEvery = 5 * time.Second
	HeadPollTries = 24
)

// HeadIs waits until the pull request's head is the commit just pushed.
// ⛔ prwait judges the head it reads, and right after a push GitHub still
// answers the old one: measured on go-ndarray/.github#25, where prwait judged
// the previous, red head and the run stopped on a fix that was in fact green.
func HeadIs(repo, number, want string) error {
	for i := 0; i < HeadPollTries; i++ {
		out, err := Call("", "gh", "pr", "view", number, "-R", repo, "--json", "headRefOid")
		if err != nil {
			return err
		}
		var v struct {
			Head string `json:"headRefOid"`
		}
		if json.Unmarshal([]byte(out), &v) == nil && v.Head == want {
			return nil
		}
		time.Sleep(HeadPollEvery)
	}
	return fmt.Errorf("the head of %s#%s has not moved to the pushed commit %.7s", repo, number, want)
}
