package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

// The inventory says, for every organisation, where its landing stands, so
// a migration wave resumes from a file instead of from a sweep: one sweep of
// ~300 organisations costs the REST budget Renovate shares, so this one goes
// through GraphQL (a separate budget), fifteen organisations a query.

// entry is one organisation's landing.
type entry struct {
	Org      string `json:"org"`
	State    string `json:"state"` // kit, template, copied, no-landing, archived
	Kit      string `json:"kit,omitempty"`
	Migrated string `json:"migrated,omitempty"` // the pull request that moved it, once it has
	Skipped  string `json:"skipped,omitempty"`  // why no wave may touch it (a person's decision)
	Note     string `json:"note,omitempty"`
}

type inventoryFile struct {
	Generated string  `json:"generated"`
	LatestKit string  `json:"latest_kit"`
	States    string  `json:"states"`
	Orgs      []entry `json:"orgs"`
}

const statesDoc = "kit: go.mod requires the landing kit; template: the self-contained first-generation layout landingmigrate moves onto the kit; copied: another hand-made layout, migrated by a person; no-landing: no <org>.github.io; archived: the landing repository is archived"

// graphql runs one GraphQL query through gh and returns its data even when
// some aliases failed: a missing repository is an error entry beside the
// others' data, and gh exits non-zero for it.
var graphql = func(query string) ([]byte, error) {
	c := exec.Command("gh", "api", "graphql", "-f", "query="+query)
	c.Stdin = nil
	out, err := c.Output()
	if len(out) > 0 && strings.Contains(string(out), `"data"`) {
		return out, nil
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("gh api graphql: %v: %s %s", err, strings.TrimSpace(string(ee.Stderr)), clip(string(out)))
		}
		return nil, fmt.Errorf("gh api graphql: %v", err)
	}
	return out, nil
}

var kitReq = regexp.MustCompile(`github\.com/go-fleettools/landingkit\s+(v\S+)`)
var validLogin = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)

// classify reads what one landing holds.
func classify(exists, archived bool, gomod, index string) (state, kit string) {
	switch {
	case !exists:
		return "no-landing", ""
	case archived:
		return "archived", ""
	}
	if m := kitReq.FindStringSubmatch(gomod); m != nil {
		return "kit", m[1]
	}
	if _, err := parseTemplate(index); err == nil {
		return "template", ""
	}
	return "copied", ""
}

type repoAnswer struct {
	IsArchived bool `json:"isArchived"`
	GoMod      *struct {
		Text string `json:"text"`
	} `json:"gomod"`
	Index *struct {
		Text string `json:"text"`
	} `json:"index"`
	Styles *struct {
		Typename string `json:"__typename"`
	} `json:"styles"`
}

// inventory classifies every organisation's landing.
func inventory(orgs []string) ([]entry, error) {
	var out []entry
	const batch = 15 // each answer carries 15 layouts of ~10 KB
	for i := 0; i < len(orgs); i += batch {
		part := orgs[i:min(len(orgs), i+batch)]
		var q strings.Builder
		q.WriteString("query{")
		for k, o := range part {
			if !validLogin.MatchString(o) {
				return nil, fmt.Errorf("not an organisation login: %q", o)
			}
			fmt.Fprintf(&q, `r%d: repository(owner:"%s", name:"%s.github.io"){ isArchived gomod: object(expression:"HEAD:go.mod"){... on Blob{text}} index: object(expression:"HEAD:layouts/index.html"){... on Blob{text}} styles: object(expression:"HEAD:layouts/partials/styles.html"){ __typename } } `, k, o, o)
		}
		q.WriteString("}")
		var b []byte
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			// a large answer over a slow link is reset now and then: ask again
			if b, err = graphql(q.String()); err == nil || !fleet.Retryable(err.Error()) && !strings.Contains(err.Error(), "connection reset") {
				break
			}
			time.Sleep(time.Duration(attempt+1) * 3 * time.Second)
		}
		if err != nil {
			return nil, err
		}
		var resp struct {
			Data map[string]*repoAnswer `json:"data"`
		}
		if err := json.Unmarshal(b, &resp); err != nil {
			return nil, fmt.Errorf("the answer did not decode: %v", err)
		}
		if resp.Data == nil {
			return nil, fmt.Errorf("an answer with no data: %s", clip(string(b)))
		}
		for k, o := range part {
			a := resp.Data[fmt.Sprintf("r%d", k)]
			e := entry{Org: o}
			if a == nil {
				e.State, _ = classify(false, false, "", "")
			} else {
				gm, ix := "", ""
				if a.GoMod != nil {
					gm = a.GoMod.Text
				}
				if a.Index != nil {
					ix = a.Index.Text
				}
				e.State, e.Kit = classify(true, a.IsArchived, gm, ix)
				switch {
				case e.State == "copied" && ix == "":
					e.Note = "no layouts/index.html"
				case e.State == "copied" && a.Styles != nil:
					e.Note = "a copy of the kit's partials (layouts/partials/styles.html): swap them for the module"
				case e.State == "copied":
					if _, err := parseTemplate(ix); err != nil {
						e.Note = err.Error()
					}
				}
			}
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Org < out[j].Org })
	return out, nil
}

// merge keeps what an earlier inventory recorded about a migration.
func merge(fresh []entry, path string) []entry {
	b, err := os.ReadFile(path)
	if err != nil {
		return fresh
	}
	var old inventoryFile
	if json.Unmarshal(b, &old) != nil {
		return fresh
	}
	prev := map[string]entry{}
	for _, e := range old.Orgs {
		prev[e.Org] = e
	}
	for i, e := range fresh {
		if p, ok := prev[e.Org]; ok {
			if fresh[i].Migrated == "" {
				fresh[i].Migrated = p.Migrated
			}
			if fresh[i].Note == "" {
				fresh[i].Note = p.Note
			}
			// a person's decision outlives every re-inventory
			if fresh[i].Skipped == "" {
				fresh[i].Skipped = p.Skipped
			}
		}
	}
	return fresh
}

func stamp() string { return time.Now().UTC().Format(time.RFC3339) }
