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

// AppCoverage is what a read of `orgs/<org>/installations` said. A GitHub App
// installation covers an organisation through a mechanism no workflow census
// can see: there is no runner, no cron and no repository to be quiet, and
// Renovate opens pull requests all the same.
type AppCoverage int

const (
	// AppUnread: the installations endpoint was not readable. The token may
	// simply not see installations on this organisation.
	AppUnread AppCoverage = iota
	// AppAbsent: read, and no Renovate App is installed.
	AppAbsent
	// AppPresent: read, and the Renovate App is installed.
	AppPresent
)

// AppCoveredBy is what the covered line says when an App is the reason.
const AppCoveredBy = "the Renovate App"

// ApplyAppCoverage is the third and last form of coverage, after an
// organisation's own runner and another runner's autodiscoverFilter. It is
// consulted ONLY for organisations still reading NoRunner, which is both
// cheaper and the only place it can change an answer.
//
// An organisation whose installations could not be read does not become
// "uncovered": it becomes CoverageUnknown, with a line of its own. Reading a
// refusal as an absence is how openweft -- which has the App installed and 16
// open pull requests from it -- was reported as a gap.
func ApplyAppCoverage(rs []Result, app map[string]AppCoverage) []Result {
	out := make([]Result, len(rs))
	copy(out, rs)
	for i := range out {
		if out[i].Verdict != NoRunner {
			continue
		}
		switch app[out[i].Org] {
		case AppPresent:
			out[i].Verdict = Covered
			out[i].CoveredBy = AppCoveredBy
			out[i].CoveredByVerdict = Healthy
		case AppUnread:
			if _, attempted := app[out[i].Org]; attempted {
				out[i].Verdict = CoverageUnknown
			}
		}
	}
	return out
}

// UnreadFilters names every LIVE runner whose autodiscoverFilter could not be
// read. A disabled runner's unread filter creates no doubt, because a disabled
// runner covers nothing either way.
func UnreadFilters(rs []Result) []string {
	var out []string
	for _, r := range rs {
		if r.WorkflowFound && r.Verdict.Live() && r.FilterNote != "" {
			out = append(out, r.Org+"/"+r.Repo)
		}
	}
	return out
}

// ApplyUncertainty is the rule that a failed read must never become a verdict.
//
// If some live runner's filter could not be read, then an organisation "no
// filter reaches" might in fact be reached by that filter, and calling it
// no_runner is a verdict derived from a call that did not answer. It becomes
// CoverageUnknown instead, naming what could not be read.
//
// This matters most exactly when it is least convenient: a rate-limited pass
// fails many reads at once, and a watcher that turns those failures into
// "nothing watches these 204 organisations" is worse than one that says
// nothing at all.
func ApplyUncertainty(rs []Result, unread []string) []Result {
	if len(unread) == 0 {
		return rs
	}
	why := "a live runner's filter could not be read: " + strings.Join(unread, ", ")
	out := make([]Result, len(rs))
	copy(out, rs)
	for i := range out {
		if out[i].Verdict == NoRunner {
			out[i].Verdict = CoverageUnknown
			out[i].UnknownWhy = why
		}
	}
	return out
}
