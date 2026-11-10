// Package priorledger records the runs of priorart, so that the act priorart
// is meant to precede can ask whether it did.
//
// priorart (#193) answers "has this already been done?" for whoever runs it.
// Nothing tied that answer to the act of filing: twice in two days
// (go-gitsafe/gitsafe#99, a duplicate of #90 that PR #96 was already
// implementing; then go-quake1/engine#88) priorart and `ghissue --new` ran in
// ONE shell command, so its verdict -- exit 1, "found" -- arrived after the
// issue existed. A check in the same command as the act it guards is a log,
// not a gate. ghissue now asks this ledger instead.
//
// The ledger is append-only JSON lines under the user's cache directory, the
// directory 0700 and the file 0600: it names repositories and subjects, which
// are not secret, but nothing else on the machine needs to write it. It is
// bounded: past MaxBytes it keeps its most recent half.
package priorledger

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Verdicts, as priorart exits.
const (
	None  = 0 // read every surface, found nothing
	Found = 1 // found prior art
	Gap   = 2 // a surface could not be read, or the question could not be put
)

// MaxBytes bounds the ledger file.
const MaxBytes = 1 << 20

// Record is one priorart run.
type Record struct {
	Time    time.Time `json:"time"`
	Repos   []string  `json:"repos,omitempty"` // --repo owner/name, lower-cased
	Org     string    `json:"org,omitempty"`   // --org name, lower-cased
	Subject string    `json:"subject"`
	Verdict int       `json:"verdict"`
}

// Covers reports whether the run read repo (owner/name): named with --repo,
// or inside the --org it read.
func (r Record) Covers(repo string) bool {
	repo = strings.ToLower(repo)
	for _, x := range r.Repos {
		if x == repo {
			return true
		}
	}
	owner, _, _ := strings.Cut(repo, "/")
	return r.Org != "" && r.Org == owner
}

// Path is the ledger file; tests point it elsewhere.
var Path = func() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "fleettools", "priorart.jsonl"), nil
}

// Append adds r to the ledger.
func Append(r Record) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	for i, x := range r.Repos {
		r.Repos[i] = strings.ToLower(x)
	}
	r.Org = strings.ToLower(r.Org)
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if st, err := os.Stat(p); err == nil && st.Size()+int64(len(line))+1 > MaxBytes {
		if err := keepTail(p); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// keepTail rewrites the ledger with its most recent half, whole lines only.
func keepTail(p string) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	b = b[len(b)/2:]
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		b = b[i+1:]
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// ErrNone is returned by Latest when no run covers the repository in time.
var ErrNone = errors.New("no priorart run covers this repository")

// Latest returns the most recent run that covers repo and is no older than
// within, as of now.
func Latest(repo string, within time.Duration, now time.Time) (Record, error) {
	p, err := Path()
	if err != nil {
		return Record{}, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, ErrNone
	}
	if err != nil {
		return Record{}, err
	}
	defer f.Close()
	var best Record
	found := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), MaxBytes)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue // a torn line is skipped, never trusted
		}
		if !r.Covers(repo) || now.Sub(r.Time) > within || r.Time.After(now.Add(time.Minute)) {
			continue
		}
		if !found || r.Time.After(best.Time) {
			best, found = r, true
		}
	}
	if err := sc.Err(); err != nil {
		return Record{}, fmt.Errorf("priorart ledger %s: %w", p, err)
	}
	if !found {
		return Record{}, ErrNone
	}
	return best, nil
}
