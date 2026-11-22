package secretscan

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MaxFile bounds what is read of any one file. A bigger one is reported as
// unread, never skipped quietly: a scan that could not read says so.
const MaxFile = 4 << 20

// Unread is a file the scan could not read, and why.
type Unread struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

// Repo is what one clone says.
type Repo struct {
	Daemon           bool      `json:"daemon"`
	Daemons          []string  `json:"daemons,omitempty"` // main packages that serve, and unit/plist files
	GoFiles          int       `json:"go_files"`
	FilesRead        int       `json:"files_read"`
	Unread           []Unread  `json:"unread,omitempty"`
	UsesSecretsource bool      `json:"uses_secretsource"`
	Findings         []Finding `json:"findings,omitempty"` // empty unless Daemon
}

var skipDirs = map[string]bool{
	".git": true, ".github": true, "vendor": true, "node_modules": true,
	"testdata": true, "third_party": true, "dist": true, "target": true,
	".cache": true, "_build": true,
	// agent worktrees are copies of other clones, counted once already.
	".claude": true,
}

// skipDir: module caches (`pkg/mod`), vendored trees and fixtures hold other
// people's code and deliberately wrong samples.
func skipDir(rel string) bool {
	base := filepath.Base(rel)
	return skipDirs[base] || (base == "mod" && filepath.Base(filepath.Dir(rel)) == "pkg")
}

type fileKind int

const (
	kindNone fileKind = iota
	kindGo
	kindMod
	kindUnit
	kindSample
)

func classify(rel string) fileKind {
	base, ext := filepath.Base(rel), strings.ToLower(filepath.Ext(rel))
	lower := strings.ToLower(base)
	switch {
	case ext == ".go" && !strings.HasSuffix(base, "_test.go"):
		return kindGo
	case base == "go.mod":
		return kindMod
	case ext == ".service" || ext == ".plist":
		return kindUnit
	case ext == ".md" && (strings.HasPrefix(lower, "readme") || strings.HasPrefix(rel, "docs"+string(filepath.Separator))):
		return kindSample
	}
	switch ext {
	case ".toml", ".yaml", ".yml", ".example", ".sample", ".env", ".ini", ".conf":
		return kindSample
	}
	if strings.HasPrefix(lower, ".env") || strings.Contains(lower, ".example.") || strings.Contains(lower, ".sample.") {
		return kindSample
	}
	return kindNone
}

type dirInfo struct{ main, serves, perm bool }

// ScanRepo reads one clone. A clone is a daemon when it ships a systemd unit
// or a launchd plist, or has a main package that serves or waits for a signal
// in its own directory; a main that only calls into a library server is found
// through its unit file or not at all, which is a stated limit. Findings are
// kept for a daemon only, unless all is set: a client's `--token` flag is not
// a daemon's secret.
//
// perm_checked on a path finding is a HEURISTIC: the package (directory)
// contains a `.Perm()` or `Mode()&` test, a 0o077-style mask, or imports
// secretsource. It says "something checks permissions here", not that this
// path is checked.
func ScanRepo(root string, all bool) (Repo, error) {
	var r Repo
	dirs := map[string]*dirInfo{}
	var found []Finding
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		rel, _ := filepath.Rel(root, p)
		if err != nil {
			if rel == "." {
				return err // the clone itself is unreadable: nothing was read
			}
			r.Unread = append(r.Unread, Unread{File: rel, Reason: err.Error()})
			return filepath.SkipDir // only a directory that cannot be listed gets here
		}
		if d.IsDir() {
			if rel != "." && skipDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		kind := classify(rel)
		if kind == kindNone || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err == nil && info.Size() > MaxFile {
			err = fmt.Errorf("larger than %d bytes", MaxFile)
		}
		var src []byte
		if err == nil {
			src, err = os.ReadFile(p)
		}
		if err != nil {
			r.Unread = append(r.Unread, Unread{File: rel, Reason: err.Error()})
			return nil
		}
		r.FilesRead++
		switch kind {
		case kindGo:
			r.GoFiles++
			gs, perr := ScanGo(rel, src)
			if perr != nil {
				r.Unread = append(r.Unread, Unread{File: rel, Reason: perr.Error()})
				return nil
			}
			di := dirs[filepath.Dir(rel)]
			if di == nil {
				di = &dirInfo{}
				dirs[filepath.Dir(rel)] = di
			}
			di.main = di.main || gs.HasMain
			di.serves = di.serves || gs.Serves
			di.perm = di.perm || gs.PermEvidence
			for _, im := range gs.Imports {
				r.UsesSecretsource = r.UsesSecretsource || strings.HasPrefix(im, SecretsourcePath)
			}
			found = append(found, gs.Findings...)
		case kindMod:
			for _, l := range strings.Split(string(src), "\n") {
				if strings.Contains(l, SecretsourcePath) && !strings.HasPrefix(strings.TrimSpace(l), "module ") {
					r.UsesSecretsource = true
				}
			}
		case kindUnit:
			r.Daemons = append(r.Daemons, rel)
			found = append(found, ScanText(rel, src)...)
		case kindSample:
			found = append(found, ScanText(rel, src)...)
		}
		return nil
	})
	if walkErr != nil {
		return Repo{}, walkErr
	}
	for dir, di := range dirs {
		if di.main && di.serves {
			r.Daemons = append(r.Daemons, dir)
		}
	}
	sort.Strings(r.Daemons)
	r.Daemon = len(r.Daemons) > 0
	if !r.Daemon && !all {
		return r, nil
	}
	for i := range found {
		if found[i].Class == Path {
			c := dirs[filepath.Dir(found[i].File)] != nil && dirs[filepath.Dir(found[i].File)].perm
			found[i].PermChecked = &c
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].File != found[j].File {
			return found[i].File < found[j].File
		}
		return found[i].Line < found[j].Line
	})
	r.Findings = found
	return r, nil
}

// Plaintext returns the findings a gate refuses: a plaintext secret INPUT.
func (r Repo) Plaintext() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Kind == "input" && f.Class == Plaintext {
			out = append(out, f)
		}
	}
	return out
}
