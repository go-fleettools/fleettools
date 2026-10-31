package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// prefilterBatch is how many repositories one GraphQL query reads.
const prefilterBatch = 20

// ⛔ WHY A PREFILTER: the inventory lists every repository, and `gopin` reads
// each one over REST -- the workflows directory, every workflow file, go.mod:
// about six calls a repository, against an hourly budget of 5000 that Renovate
// and every other session share. Two thousand repositories is a quota
// exhaustion and a starved Renovate. GraphQL has its OWN budget, and one query
// reads the workflow TEXT of twenty repositories, so the common answer -- this
// repository names nothing to move -- costs a twentieth of a call.
//
// The prefilter only ever removes a repository for which it HAS the text and
// found nothing. A repository it could not read is passed through as a
// candidate, so the REST read decides: a filter that drops what it could not
// see would report an unreadable repository as a clean one.
const prefilterQuery = `query {%s}`

type prefilterStats struct {
	Read, Candidates, NoWorkflows, Passed int
}

// prefilter prints the candidate repositories, one per line, and a count line.
func prefilter(cfg config, repos []string, out, errw io.Writer) error {
	var st prefilterStats
	cand := map[string]bool{}
	var elsewhere []string
	var read func(batch []string) error
	read = func(batch []string) error {
		got, err := graphqlWorkflows(batch)
		switch {
		case err == nil:
			for _, repo := range batch {
				texts, ok := got[repo]
				if !ok {
					st.NoWorkflows++
					continue
				}
				switch {
				case wantsMove(texts, cfg):
					cand[repo] = true
				case namesFromElsewhere(texts, cfg):
					elsewhere = append(elsewhere, repo)
				}
			}
			return nil
		case isQuota(err.Error()):
			return err
		case len(batch) > 1:
			// One unreadable repository must not hide nineteen readable ones.
			mid := len(batch) / 2
			if err := read(batch[:mid]); err != nil {
				return err
			}
			return read(batch[mid:])
		}
		// A single repository GraphQL cannot read: leave it to the REST read.
		cand[batch[0]] = true
		st.Passed++
		return nil
	}
	for i := 0; i < len(repos); i += prefilterBatch {
		end := min(i+prefilterBatch, len(repos))
		if err := read(repos[i:end]); err != nil {
			return &exitErr{1, fmt.Sprintf("prefilter stopped at %d/%d: %v", i, len(repos), err)}
		}
	}
	for _, repo := range repos {
		st.Read++
		if cand[repo] {
			fmt.Fprintln(out, repo)
			st.Candidates++
		}
	}
	for _, repo := range elsewhere {
		fmt.Fprintf(errw, "elsewhere %s: names %s outside a go-version line (matrix, env:, input default); not touched\n", repo, cfg.from)
	}
	// read = candidates + nothing to move, by construction of the loop above,
	// and the line says both so a reader can check the sum.
	fmt.Fprintf(errw, "prefilter: %d read · %d candidates · %d nothing to move (%d without a workflows directory, %d naming %s elsewhere) · %d unreadable by GraphQL (passed on to the REST read)\n",
		st.Read, st.Candidates, st.Read-st.Candidates, st.NoWorkflows, len(elsewhere), cfg.from, st.Passed)
	return nil
}

// wantsMove says whether any workflow text names something this run would move.
func wantsMove(texts []string, cfg config) bool {
	for _, t := range texts {
		if !cfg.noAlias && reStable.MatchString(t) {
			return true
		}
		if cfg.from != "" && reExplicit(cfg.from).MatchString(t) {
			return true
		}
		if cfg.gomod && reVersionFile.MatchString(t) {
			return true
		}
	}
	return false
}

// namesFromElsewhere says whether the workflows spell the --from version
// somewhere gopin does not move it: a matrix list, an env: variable, an input
// default. Such a repository is not touched, and a campaign must LIST it
// rather than let "not a candidate" read as "not affected".
func namesFromElsewhere(texts []string, cfg config) bool {
	if cfg.from == "" {
		return false
	}
	for _, t := range texts {
		if strings.Contains(t, cfg.from) {
			return true
		}
	}
	return false
}

// graphqlWorkflows reads the workflow files of a batch of repositories in one
// query. A repository with no workflows directory is absent from the result.
func graphqlWorkflows(repos []string) (map[string][]string, error) {
	var q strings.Builder
	for i, repo := range repos {
		owner, name, _ := strings.Cut(repo, "/")
		fmt.Fprintf(&q, `r%d: repository(owner: %q, name: %q) { object(expression: "HEAD:.github/workflows") { ... on Tree { entries { name object { ... on Blob { text } } } } } } `, i, owner, name)
	}
	b, err := ghJSON("api", "graphql", "-f", "query="+fmt.Sprintf(prefilterQuery, q.String()))
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data map[string]*struct {
			Object *struct {
				Entries []struct {
					Name   string
					Object *struct{ Text string }
				}
			}
		}
		Errors []struct{ Message, Type string }
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, fmt.Errorf("graphql: %w", err)
	}
	if len(resp.Errors) > 0 {
		return nil, fmt.Errorf("graphql: %s (%s)", resp.Errors[0].Message, resp.Errors[0].Type)
	}
	out := map[string][]string{}
	for i, repo := range repos {
		r := resp.Data[fmt.Sprintf("r%d", i)]
		if r == nil {
			return nil, fmt.Errorf("graphql: no answer for %s", repo)
		}
		if r.Object == nil {
			continue
		}
		texts := []string{}
		for _, e := range r.Object.Entries {
			if e.Object != nil && (strings.HasSuffix(e.Name, ".yml") || strings.HasSuffix(e.Name, ".yaml")) {
				texts = append(texts, e.Object.Text)
			}
		}
		out[repo] = texts
	}
	return out, nil
}
