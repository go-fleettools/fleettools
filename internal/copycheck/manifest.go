package copycheck

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v4"
)

// DefaultManifest is where a checked repository declares what it read.
//
// ⛔ WHY A YAML FILE AND NOT A MARKDOWN BLOCK. A PROVENANCE.md with a fenced
// block would be read by people, and a gate that parses prose finds the
// paragraph that is easiest to edit by hand and hardest to notice edited. A
// file that is only data cannot be half-updated: it parses or the gate says so.
// YAML over TOML because the module already carries a YAML reader (a new
// dependency for a gate is a new thing to audit), and because it allows the one
// thing a provenance record needs most: a comment saying WHY a reference is
// there. The prose belongs in the README, which can link to this file.
const DefaultManifest = ".provenance.yml"

// Roles of a reference. The role is what the repository's author PROMISES about
// how the reference was used; the gate checks the promise against the bytes.
const (
	RoleStudied    = "studied"     // read to understand; nothing may be copied
	RoleOracle     = "oracle"      // run to compare outputs; nothing may be copied
	RoleTablesFrom = "tables-from" // numeric tables taken, permissive licence only
	RolePortedFrom = "ported-from" // code translated, permissive licence only
)

var roles = map[string]bool{RoleStudied: true, RoleOracle: true, RoleTablesFrom: true, RolePortedFrom: true}

// Reference is one body of source the repository's authors read.
type Reference struct {
	Name    string   `yaml:"name"`
	URL     string   `yaml:"url"` // https archive (tar.gz or zip) of the PINNED revision
	Pin     string   `yaml:"pin"` // commit or tag; must appear in the URL
	Tree    string   `yaml:"tree_sha256"`
	License string   `yaml:"license"` // SPDX expression
	Role    string   `yaml:"role"`
	Paths   []string `yaml:"paths"` // only these subtrees are fetched and compared (a directory prefix, no leading slash)
	Note    string   `yaml:"note"`
	// Strip is how many leading path components of the archive to drop: 1 (the
	// default) for GitHub archives, 0 for android.googlesource.com +archive.
	Strip *int `yaml:"strip_components"`
	// Restrictions express what a licence cannot: a FILE that carries its own
	// notice. PacketVideo's AAC decoder is Apache-2.0 as a whole, yet 114 of
	// its files also carry an MPEG copyright notice, so for those files the
	// role is studied whatever the manifest says for the reference.
	Restrictions []Restriction `yaml:"restrictions"`
}

// Restriction names a notice that, when found anywhere in a reference file,
// makes that file READ-ONLY for us: a match against it is a
// finding under every role, tables-from and ported-from included.
type Restriction struct {
	Name     string   `yaml:"name"`
	Contains []string `yaml:"contains"` // case-insensitive substrings
}

// restrictedBy returns the name of the first restriction whose notice is in
// the head of the file, or "".
func (r *Reference) restrictedBy(src []byte) string {
	if len(r.Restrictions) == 0 {
		return ""
	}
	// The WHOLE file, not its head: PacketVideo's MPEG notice sits at line 152
	// of huffdecode.cpp, below the Apache header and the revision history.
	//
	// Whitespace is collapsed on both sides: a notice is re-wrapped by every
	// editor that ever touched it ("ISO/IEC  gives users of the MPEG-2 NBC/
	// MPEG-4 Audio\n   standards" is one sentence, measured in PacketVideo).
	s := strings.Join(strings.Fields(strings.ToLower(string(src))), " ")
	for _, x := range r.Restrictions {
		for _, sub := range x.Contains {
			if strings.Contains(s, strings.Join(strings.Fields(strings.ToLower(sub)), " ")) {
				return x.Name
			}
		}
	}
	return ""
}

// Kinds of a Value declaration.
const (
	// ValueNormative: the table is the bare numeric content of a standard's
	// normative table. ⛔ THE DECISION (2026-11-10): such values may be
	// reproduced, as FFmpeg and FAAD2 do, because values dictated by technical
	// constraints are not original works (Feist v. Rural, 1991; Football
	// Dataco v. Yahoo, CJEU 2012) -- but never the standard's TEXT and never its
	// reference CODE. The exemption covers numbers; it never covers shape.
	ValueNormative = "normative-values"
	// ValueDerived: the values are computed in code from a published formula,
	// and the declaration names the source.
	ValueDerived = "derived"
)

// Value declares the provenance of one table (or one function that computes
// one): where in OUR source it is, and why its numbers are ours to hold.
type Value struct {
	Kind      string `yaml:"kind"`
	File      string `yaml:"file"`   // our file, relative to the root
	Symbol    string `yaml:"symbol"` // a Go var/const/func name in that file
	Standard  string `yaml:"standard"`
	Clause    string `yaml:"clause"`
	Source    string `yaml:"source"` // derived: the paper and equation number
	Rationale string `yaml:"rationale"`
}

// RequireDeclared turns on the rule that every table above MinNumbers numbers in
// the named files must be declared in Values (or be attributed to a permissive
// tables-from reference). Paths are prefixes of our files' relative paths:
// "aac" covers aac.go, aac_tables.go and aac/anything.go. Absent, the rule is
// off, so a repository adopts it package by package.
type RequireDeclared struct {
	MinNumbers int      `yaml:"min_numbers"`
	Paths      []string `yaml:"paths"`
}

// Manifest is the file a checked repository carries.
type Manifest struct {
	Version         int              `yaml:"version"`
	Notice          string           `yaml:"notice"`  // attribution file, default NOTICE
	Exclude         []string         `yaml:"exclude"` // our paths not to compare: a directory prefix ending in /, or a file
	References      []Reference      `yaml:"references"`
	Values          []Value          `yaml:"values"`
	RequireDeclared *RequireDeclared `yaml:"require_declared"`
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (v *Value) validate() error {
	if v.Kind != ValueNormative && v.Kind != ValueDerived {
		return fmt.Errorf("kind %q is neither normative-values nor derived", v.Kind)
	}
	if v.File == "" || strings.HasPrefix(v.File, "/") || strings.Contains(v.File, "..") || strings.Contains(v.File, "\\") || !strings.HasSuffix(v.File, ".go") {
		return fmt.Errorf("file %q must be a relative .go path without ..", v.File)
	}
	if !identRe.MatchString(v.Symbol) {
		return fmt.Errorf("symbol %q is not a Go identifier", v.Symbol)
	}
	if strings.TrimSpace(v.Rationale) == "" {
		return errors.New("a one-line rationale is required")
	}
	if strings.ContainsAny(v.Rationale, "\n") {
		return errors.New("the rationale is one line")
	}
	switch v.Kind {
	case ValueNormative:
		if strings.TrimSpace(v.Standard) == "" || strings.TrimSpace(v.Clause) == "" {
			return errors.New("normative-values needs the standard and the table or clause number")
		}
	case ValueDerived:
		if strings.TrimSpace(v.Source) == "" {
			return errors.New("derived needs the source: the paper and the equation number")
		}
	}
	return nil
}

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)
var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// LoadManifest reads and validates a manifest. Unknown keys are an error: a
// misspelt `tree_sha256` would otherwise be an unverified reference.
func LoadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseManifest(b)
}

// ParseManifest is LoadManifest over bytes.
func ParseManifest(b []byte) (*Manifest, error) {
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if m.Version != 1 {
		return nil, fmt.Errorf("manifest: version must be 1, got %d", m.Version)
	}
	if m.Notice == "" {
		m.Notice = "NOTICE"
	}
	seenSym := map[string]bool{}
	for i := range m.Values {
		v := &m.Values[i]
		if err := v.validate(); err != nil {
			return nil, fmt.Errorf("manifest: values %d (%s %s): %w", i+1, v.File, v.Symbol, err)
		}
		if k := v.File + "\x00" + v.Symbol; seenSym[k] {
			return nil, fmt.Errorf("manifest: %s %s is declared twice", v.File, v.Symbol)
		} else {
			seenSym[k] = true
		}
	}
	if rd := m.RequireDeclared; rd != nil {
		if rd.MinNumbers < 8 || len(rd.Paths) == 0 {
			return nil, errors.New("manifest: require_declared needs min_numbers >= 8 and at least one path")
		}
		for _, p := range rd.Paths {
			if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
				return nil, fmt.Errorf("manifest: require_declared path %q", p)
			}
		}
	}
	if len(m.References) == 0 {
		return nil, errors.New("manifest: no references (an empty manifest checks nothing; delete the gate instead)")
	}
	seen := map[string]bool{}
	for i := range m.References {
		r := &m.References[i]
		if err := r.validate(); err != nil {
			return nil, fmt.Errorf("manifest: reference %d (%q): %w", i+1, r.Name, err)
		}
		if seen[r.Name] {
			return nil, fmt.Errorf("manifest: reference name %q appears twice", r.Name)
		}
		seen[r.Name] = true
	}
	return &m, nil
}

func (r *Reference) validate() error {
	if !nameRe.MatchString(r.Name) {
		return errors.New("name must be 1-64 of letters, digits, . _ -")
	}
	if !roles[r.Role] {
		return fmt.Errorf("role %q is none of studied, oracle, tables-from, ported-from", r.Role)
	}
	if !strings.HasPrefix(r.URL, "https://") {
		return errors.New("url must be https://")
	}
	if r.Pin == "" {
		return errors.New("pin (a commit or a tag) is required: a reference that moves is not a reference")
	}
	switch strings.ToLower(r.Pin) {
	case "master", "main", "head", "trunk", "latest", "develop":
		return fmt.Errorf("pin %q is a branch, not a revision", r.Pin)
	}
	if !strings.Contains(r.URL, r.Pin) {
		return errors.New("url does not contain the pin: the archive fetched may not be the revision declared")
	}
	if !hex64.MatchString(r.Tree) {
		return errors.New("tree_sha256 must be 64 lowercase hex digits (run `copycheck pin` to compute it)")
	}
	if strings.TrimSpace(r.License) == "" {
		return errors.New("license (SPDX) is required")
	}
	for _, x := range r.Restrictions {
		if x.Name == "" || len(x.Contains) == 0 {
			return errors.New("a restriction needs a name and at least one contains")
		}
		for _, s := range x.Contains {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("restriction %q has an empty contains (it would match every file)", x.Name)
			}
		}
	}
	for _, p := range r.Paths {
		if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "..") || strings.Contains(p, "\\") {
			return fmt.Errorf("path %q must be a relative subtree without ..", p)
		}
	}
	return nil
}
