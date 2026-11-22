package secretscan

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// Hit is a finding with the clone it was found in.
type Hit struct {
	Clone string
	Org   string
	Finding
}

// Top returns the plaintext secret inputs of daemon clones, most exposed
// first: a value stored in a shipped unit, then a flag (visible to `ps`), then
// an environment variable or a config field. n <= 0 means all of them.
func (r Report) Top(n int) []Hit {
	var hits []Hit
	// one finding per repository: the worktrees of one origin hold the same
	// line, which is counted once, in the first clone by name.
	seen := map[string]bool{}
	for _, c := range r.Clones {
		for _, f := range c.Repo.Plaintext() {
			k := fmt.Sprintf("%s\x00%s\x00%d\x00%s", c.Key(), f.File, f.Line, f.Name)
			if seen[k] {
				continue
			}
			seen[k] = true
			hits = append(hits, Hit{Clone: c.Dir, Org: c.Org, Finding: f})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		switch {
		case a.Exposure != b.Exposure:
			return a.Exposure > b.Exposure
		case a.Clone != b.Clone:
			return a.Clone < b.Clone
		case a.File != b.File:
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	if n > 0 && len(hits) > n {
		hits = hits[:n]
	}
	return hits
}

// Orgs returns the organisations that hold a plaintext secret input, with
// the number of such inputs, most first. An unknown org is named "(unknown)".
func (r Report) Orgs() []OrgCount {
	counts := map[string]int{}
	for _, h := range r.Top(0) {
		org := h.Org
		if org == "" {
			org = "(unknown)"
		}
		counts[org]++
	}
	var out []OrgCount
	for o, n := range counts {
		out = append(out, OrgCount{Org: o, Inputs: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Inputs != out[j].Inputs {
			return out[i].Inputs > out[j].Inputs
		}
		return out[i].Org < out[j].Org
	})
	return out
}

// OrgCount is an organisation and how many plaintext inputs it holds.
type OrgCount struct {
	Org    string `json:"org"`
	Inputs int    `json:"inputs"`
}

// WriteJSON writes the report as indented JSON.
func (r Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func cell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ")
}

func (s Staleness) text() string {
	switch {
	case s.GitError != "":
		return "NOT READ: " + s.GitError
	case s.NeverFetched:
		return fmt.Sprintf("never fetched; +%d/-%d", s.Ahead, s.Behind)
	case s.NoUpstream:
		return "fetched " + s.FetchedAt.Format("2006-01-02") + "; no upstream"
	}
	return fmt.Sprintf("fetched %s; ahead %d, behind %d", s.FetchedAt.Format("2006-01-02"), s.Ahead, s.Behind)
}

// WriteMarkdown writes the report as Markdown tables.
func (r Report) WriteMarkdown(w io.Writer) error {
	var b strings.Builder
	d := r.Denominator
	fmt.Fprintf(&b, "# Plaintext secret inputs of fleet daemons\n\nGenerated %s from `%s`.\n\n",
		r.Generated.Format(time.RFC3339), r.Root)
	b.WriteString("## Denominator\n\n| what | count |\n|---|---|\n")
	for _, row := range []struct {
		k string
		v int
	}{
		{"clones seen", d.Seen}, {"clones read", d.Read}, {"clones skipped (NOT READ)", d.Skipped},
		{"files read", d.FilesRead}, {"files unread (NOT READ)", d.FilesUnread},
		{"git answers missing (NOT READ)", d.GitUnreadable},
		{"entries ignored (loose files, dot-directories)", d.Ignored},
		{"clones never fetched", d.NeverFetched}, {"clones stale (never fetched, old, or behind)", d.Stale},
		{"daemon clones", d.DaemonRepos}, {"daemon clones with a plaintext secret input", d.DaemonsPlaintext},
		{"clones importing secretsource", d.UsesSecretsource},
	} {
		fmt.Fprintf(&b, "| %s | %d |\n", row.k, row.v)
	}
	b.WriteString("\nComplete: " + map[bool]string{true: "yes", false: "NO, see below"}[r.Complete()] + "\n")

	b.WriteString("\n## Plaintext secret inputs, most exposed first\n\n| clone | org | file:line | source | name | exposure |\n|---|---|---|---|---|---|\n")
	for _, h := range r.Top(0) {
		fmt.Fprintf(&b, "| %s | %s | %s:%d | %s | `%s` | %d |\n",
			cell(h.Clone), cell(h.Org), cell(h.File), h.Line, h.Source, cell(h.Name), h.Exposure)
	}

	b.WriteString("\n## Path and reference inputs (not plaintext)\n\n| clone | file:line | class | name | perm checked |\n|---|---|---|---|---|\n")
	for _, c := range r.Clones {
		for _, f := range c.Repo.Findings {
			if f.Kind != "input" || (f.Class != Path && f.Class != Ref) {
				continue
			}
			pc := "n/a"
			if f.PermChecked != nil {
				pc = fmt.Sprint(*f.PermChecked)
			}
			fmt.Fprintf(&b, "| %s | %s:%d | %s | `%s` | %s |\n", cell(c.Dir), cell(f.File), f.Line, f.Class, cell(f.Name), pc)
		}
	}

	b.WriteString("\n## Samples and docs showing an inline secret value\n\n| clone | file:line | name | placeholder |\n|---|---|---|---|\n")
	for _, c := range r.Clones {
		for _, f := range c.Repo.Findings {
			if f.Kind == "sample" {
				fmt.Fprintf(&b, "| %s | %s:%d | `%s` | %v |\n", cell(c.Dir), cell(f.File), f.Line, cell(f.Name), f.Placeholder)
			}
		}
	}

	b.WriteString("\n## Clones importing secretsource\n\n")
	for _, c := range r.Clones {
		if c.Repo.UsesSecretsource {
			fmt.Fprintf(&b, "- %s\n", c.Dir)
		}
	}

	b.WriteString("\n## NOT READ\n\n| clone | what | why |\n|---|---|---|\n")
	for _, c := range r.Clones {
		if !c.Read {
			fmt.Fprintf(&b, "| %s | clone | %s |\n", cell(c.Dir), cell(c.SkipReason))
		}
		for _, u := range c.Repo.Unread {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", cell(c.Dir), cell(u.File), cell(u.Reason))
		}
	}

	b.WriteString("\n## Staleness of the clones read (local refs, no fetch)\n\n| clone | org | daemon | staleness |\n|---|---|---|---|\n")
	for _, c := range r.Clones {
		if c.Read {
			fmt.Fprintf(&b, "| %s | %s | %v | %s |\n", cell(c.Dir), cell(c.Org), c.Repo.Daemon, cell(c.Staleness.text()))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
