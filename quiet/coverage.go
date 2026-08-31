package quiet

import "strings"

// A Renovate runner does not watch the repository it lives in. It watches
// whatever its `autodiscoverFilter` names, which on this fleet is anything from
// one organisation to two hundred and five of them.
//
// Reporting a property of a repository when the property belongs to whatever
// COVERS that repository is how a watcher earns 199 lines nobody reads -- and
// then the one real page is scrolled past with them.

// ParseAutodiscoverFilter reads the patterns out of a Renovate config.js.
//
// It reads only a LITERAL array. `go-attest/renovate-runner` builds its filter
// with `mine.map((o) => `${o}/**`)` over an inline list sliced by an env var,
// and no amount of string scanning makes that knowable. The second return says
// why it could not be read, and an unreadable filter is reported as unknown
// coverage -- never as no coverage, which is the same mistake in a smaller
// coat.
func ParseAutodiscoverFilter(js string) ([]string, string) {
	i := strings.Index(js, "autodiscoverFilter")
	if i < 0 {
		if strings.Contains(js, "autodiscover:") && strings.Contains(js, "true") {
			return nil, "autodiscover with no filter: covers everything the token can see"
		}
		return nil, ""
	}
	rest := js[i+len("autodiscoverFilter"):]
	rest = strings.TrimLeft(rest, " \t")
	if !strings.HasPrefix(rest, ":") {
		return nil, "autodiscoverFilter is not an assignment"
	}
	rest = strings.TrimLeft(rest[1:], " \t\n\r")
	if !strings.HasPrefix(rest, "[") {
		return nil, "autodiscoverFilter is computed, not a literal array"
	}
	end := strings.Index(rest, "]")
	if end < 0 {
		return nil, "autodiscoverFilter array is unterminated"
	}
	var out []string
	for _, quote := range []byte{'\'', '"', '`'} {
		out = append(out, quotedStrings(rest[:end], quote)...)
	}
	if len(out) == 0 {
		return nil, "autodiscoverFilter array holds no string literal"
	}
	return out, ""
}

func quotedStrings(s string, quote byte) []string {
	var out []string
	for {
		i := strings.IndexByte(s, quote)
		if i < 0 {
			return out
		}
		j := strings.IndexByte(s[i+1:], quote)
		if j < 0 {
			return out
		}
		out = append(out, s[i+1:i+1+j])
		s = s[i+2+j:]
	}
}

// MatchesOrg reports whether one autodiscoverFilter pattern reaches an
// organisation. Only the owner half of "owner/repo" decides that: a runner that
// watches `go-ruby-*/**` reaches every go-ruby-* organisation, whatever it then
// does per repository.
func MatchesOrg(pattern, org string) bool {
	owner := pattern
	if i := strings.Index(pattern, "/"); i >= 0 {
		owner = pattern[:i]
	}
	return globMatch(owner, org)
}

// globMatch is `*` against a single segment: the only wildcard these filters
// use, and the only one worth being sure of.
func globMatch(pat, s string) bool {
	parts := strings.Split(pat, "*")
	if len(parts) == 1 {
		return pat == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(s, p)
		if i < 0 {
			return false
		}
		s = s[i+len(p):]
	}
	last := parts[len(parts)-1]
	return len(s) >= len(last) && strings.HasSuffix(s, last)
}

// Live reports whether this runner's schedule still exists and is switched on.
// An archived, disabled or cron-less runner covers nothing -- which is the whole
// point about `go-attest/renovate-runner`, retired and disabled_manually, and
// therefore no longer an answer to "what watches these organisations?".
func (v Verdict) Live() bool {
	switch v {
	case Healthy, NotYetDue, Overdue, NeverFired:
		return true
	}
	return false
}

// ApplyCoverage resolves, fleet-wide, which runner reaches each organisation,
// and turns "this organisation has no runner" into "this organisation is
// covered by X" wherever one does. What is left in NoRunner is then what the
// verdict was built for: an organisation no runner reaches at all.
func ApplyCoverage(rs []Result) []Result {
	type cover struct {
		name    string
		verdict Verdict
	}
	// Prefer a runner that is actually delivering; fall back to one that is
	// enabled but overdue, and SAY that it is overdue rather than calling the
	// organisation covered and moving on.
	best := map[string]cover{}
	for _, r := range rs {
		if r.Repo == "" || !r.Verdict.Live() || len(r.Covers) == 0 {
			continue
		}
		name := r.Org + "/" + r.Repo
		for _, o := range orgsOf(rs) {
			if o == r.Org {
				continue // a runner covering its own org is not "coverage"
			}
			matched := false
			for _, p := range r.Covers {
				if MatchesOrg(p, o) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			cur, seen := best[o]
			if !seen || (cur.verdict.Pages() && !r.Verdict.Pages()) {
				best[o] = cover{name, r.Verdict}
			}
		}
	}
	out := make([]Result, len(rs))
	copy(out, rs)
	for i := range out {
		c, ok := best[out[i].Org]
		if !ok {
			continue
		}
		out[i].CoveredBy = c.name
		out[i].CoveredByVerdict = c.verdict
		if out[i].Verdict == NoRunner {
			out[i].Verdict = Covered
		}
	}
	return out
}

func orgsOf(rs []Result) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rs {
		if !seen[r.Org] {
			seen[r.Org] = true
			out = append(out, r.Org)
		}
	}
	return out
}
