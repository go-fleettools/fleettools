package copycheck

import (
	"fmt"
	"strings"
)

// Class is what a licence lets a derivative do.
type Class int

const (
	// Permissive: copying is allowed with attribution.
	Permissive Class = iota
	// FileCopyleft: copying is allowed only into a file that is itself under the
	// same licence (MPL, EPL, CDDL). The gate checks the FILE, not the repository.
	FileCopyleft
	// Copyleft: copying or translating makes the result a derivative under the
	// reference's licence. The project does not do that, by decision.
	Copyleft
	// Unknown: not in the table. Treated as Copyleft for every decision and
	// reported as unknown, so that the table is extended on purpose.
	Unknown
)

func (c Class) String() string {
	return [...]string{"permissive", "file-copyleft", "copyleft", "unknown"}[c]
}

// ⛔ THE LICENCE LIST IS DATA, AND TESTED. An id is matched whole or by the
// prefix of a family, case-insensitively, after SPDX's "-only"/"-or-later"
// suffixes are accepted as part of the family.
var permissiveIDs = []string{
	"mit", "mit-0", "isc", "0bsd", "bsd-1-clause", "bsd-2-clause", "bsd-3-clause",
	"apache-2.0", "apache-1.1", "zlib", "unlicense", "cc0-1.0", "bsl-1.0",
	"blueoak-1.0.0", "python-2.0", "libpng-2.0", "x11", "ncsa", "wtfpl",
}

var fileCopyleftPrefixes = []string{"mpl-", "epl-", "cddl-"}

var copyleftPrefixes = []string{
	"gpl-", "lgpl-", "agpl-", "gpl", "lgpl", "agpl", "eupl-", "osl-", "sspl-",
	"cc-by-sa-", "cc-by-nc", "cc-by-nd", "rpl-", "cpal-", "cecill",
}

func classOfID(id string) Class {
	id = strings.ToLower(strings.TrimSpace(id))
	id = strings.TrimSuffix(id, "+")
	for _, p := range permissiveIDs {
		if id == p {
			return Permissive
		}
	}
	for _, p := range fileCopyleftPrefixes {
		if strings.HasPrefix(id, p) {
			return FileCopyleft
		}
	}
	for _, p := range copyleftPrefixes {
		if strings.HasPrefix(id, p) {
			return Copyleft
		}
	}
	return Unknown
}

// ClassOf classifies an SPDX expression. `A OR B` is the licensee's choice, so
// the most permissive operand wins; `A AND B` binds both, so the strictest
// wins; `X WITH exception` is X (the exception narrows X, and the gate
// declines to guess how). An expression it cannot read is Unknown.
func ClassOf(expr string) Class {
	toks := strings.Fields(strings.NewReplacer("(", " ( ", ")", " ) ").Replace(expr))
	if len(toks) == 0 {
		return Unknown
	}
	p := &spdx{toks: toks}
	c := p.or()
	if p.i != len(p.toks) || p.bad {
		return Unknown
	}
	return c
}

type spdx struct {
	toks []string
	i    int
	bad  bool // a syntax error anywhere makes the whole expression Unknown
}

func (p *spdx) peek() string {
	if p.i < len(p.toks) {
		return p.toks[p.i]
	}
	return ""
}

func (p *spdx) or() Class {
	c := p.and()
	for strings.EqualFold(p.peek(), "or") {
		p.i++
		if d := p.and(); d < c {
			c = d
		}
	}
	return c
}

func (p *spdx) and() Class {
	c := p.atom()
	for strings.EqualFold(p.peek(), "and") {
		p.i++
		if d := p.atom(); d > c {
			c = d
		}
	}
	return c
}

func (p *spdx) atom() Class {
	t := p.peek()
	if t == "" {
		p.bad = true
		return Unknown
	}
	p.i++
	if t == "(" {
		c := p.or()
		if p.peek() != ")" {
			p.bad = true
			return Unknown
		}
		p.i++
		return c
	}
	if t == ")" || strings.EqualFold(t, "or") || strings.EqualFold(t, "and") {
		p.bad = true
		return Unknown
	}
	c := classOfID(t)
	if strings.EqualFold(p.peek(), "with") {
		p.i += 2
	}
	return c
}

// Copies reports whether a role is a promise to have taken something: tables
// (tables-from) or code (ported-from).
func tablesAllowedByRole(role string) bool {
	return role == RoleTablesFrom || role == RolePortedFrom
}
func codeAllowedByRole(role string) bool { return role == RolePortedFrom }

// PolicyFinding is a refusal that needs no source to be read.
type PolicyFinding struct {
	Reference string
	Message   string
}

// CheckPolicy refuses the declarations that cannot be honoured: taking from a
// licence that does not allow it, and licences the table does not know.
func CheckPolicy(m *Manifest) []PolicyFinding {
	var out []PolicyFinding
	for _, r := range m.References {
		c := ClassOf(r.License)
		switch {
		// An unlisted licence (LicenseRef-3GPP-copyright, a custom notice) is
		// fine for a reference that is only studied or run: nothing is taken, so
		// nothing needs the licence's permission. It is refused the moment the
		// role says something was taken, and any match against it is a finding.
		case c == Unknown && tablesAllowedByRole(r.Role):
			out = append(out, PolicyFinding{r.Name, fmt.Sprintf("role %s is refused for licence %q, which copycheck does not know to permit copying; add it to the table with a test, rather than guessing", r.Role, r.License)})
		case tablesAllowedByRole(r.Role) && c == Copyleft:
			out = append(out, PolicyFinding{r.Name, fmt.Sprintf("role %s is refused for a copyleft licence (%s): copyleft code is read to understand, never copied, translated, or tabulated", r.Role, r.License)})
		}
	}
	return out
}

// statesMPL reports whether a file's header says it is under the licence family
// that allows file-level reuse.
func statesFileCopyleft(src []byte) bool {
	h := src
	if len(h) > 2048 {
		h = h[:2048]
	}
	s := strings.ToLower(string(h))
	for _, m := range []string{"mpl-", "mozilla public license", "epl-", "eclipse public license", "cddl-"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}
