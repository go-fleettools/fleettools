package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// Terms turns a subject into what the surfaces are actually searched for: the
// whole phrase, plus every word of it long enough to mean something on its own.
//
// ⛔ THE WHOLE PHRASE IS NOT ENOUGH, and this is the defect that cost the day.
// The two branches that already held the work were `unrounded-space` and
// `space-refresh`; the subject in hand was "unrounded space width". No
// substring search for that phrase finds either. A branch is named for its
// mechanism, an issue titled for its symptom, and a commit written for its
// cause — three vocabularies for one subject, and only the words overlap.
//
// Words of three letters or fewer are dropped: "the", "and", "pt" match
// everything, and a surface that matches everything says nothing. Stop words of
// four letters and more that are common in this fleet are dropped for the same
// reason — "fix" is three letters, but "with", "that", "when" would each hit
// half a tracker.
func Terms(subject string) []string {
	phrase := strings.ToLower(strings.TrimSpace(subject))
	if phrase == "" {
		return nil
	}
	out := []string{phrase}
	seen := map[string]bool{phrase: true}
	for _, w := range strings.FieldsFunc(phrase, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len(w) <= 3 || stopWord[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// stopWord holds the words that would make a term match everything. Kept short
// and explicit: a long list silently narrows the search, which is the failure
// this command exists to prevent.
var stopWord = map[string]bool{
	"with": true, "that": true, "when": true, "this": true, "from": true,
	"into": true, "then": true, "than": true, "does": true, "make": true,
	"should": true, "would": true, "could": true, "about": true,
}

// hit reports which term matched a piece of text, and "" when none did.
func hit(terms []string, texts ...string) string {
	for _, t := range terms {
		for _, s := range texts {
			if strings.Contains(strings.ToLower(s), t) {
				return t
			}
		}
	}
	return ""
}

// A Branch is prior art that no issue list shows.
type Branch struct {
	Name    string
	Subject string // its head commit's subject line
	Hit     string // the term that matched
	Default bool   // it is the repository's default branch
}

// branchQuery reads every head ref of a repository with its head-commit
// subject in ONE call.
//
// ⛔ It is GraphQL and not REST on purpose, for two reasons. The REST branch
// listing gives a name and a sha but no commit message, so matching on the
// head-commit subject would cost one further call per branch — twenty calls for
// one gate, which is how a check becomes too expensive to run and therefore not
// run. And GraphQL has its own hourly budget, separate from REST (measured when
// a sweep of mine starved the fleet's Renovate runners), so a gate placed in
// front of every piece of work does not eat the budget the work itself needs.
//
// ⛔ IT PAGINATES, and the reason is a near miss rather than a hypothesis: the
// first repository this gate was pointed at, go-tex/engine, has 93 branches
// against the page size of 100. At 101 a `first:100` query would have returned
// a clean, confident, SEVEN-BRANCH-SHORT answer, and the gate would have opened
// on the surface it had not finished reading. A cap is only safe when something
// checks whether it was reached.
const branchQuery = `query($owner:String!,$name:String!,$after:String){
  repository(owner:$owner,name:$name){
    defaultBranchRef{ name }
    refs(refPrefix:"refs/heads/",first:100,after:$after,orderBy:{field:ALPHABETICAL,direction:ASC}){
      pageInfo{ hasNextPage endCursor }
      nodes{ name target{ ... on Commit{ messageHeadline } } }
    }
  }
}`

// branchPageLimit bounds the pagination. A repository with more than 5000
// branches is not a case to answer quietly: the surface is reported unread
// rather than half-read, which holds the gate shut.
const branchPageLimit = 50

// Branches reports the branches of a repository whose name or head-commit
// subject carries one of the terms.
func Branches(repo string, terms []string) ([]Branch, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return nil, fmt.Errorf("repository %q: want owner/name", repo)
	}
	var out []Branch
	after := ""
	for page := 0; ; page++ {
		if page >= branchPageLimit {
			return nil, fmt.Errorf("branches of %s: more than %d, not read to the end",
				repo, page*100)
		}
		args := []string{"api", "graphql",
			"-f", "query=" + branchQuery, "-f", "owner=" + owner, "-f", "name=" + name}
		if after != "" {
			args = append(args, "-f", "after="+after)
		}
		raw, err := gh(args...)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Data struct {
				Repository *struct {
					DefaultBranchRef *struct {
						Name string `json:"name"`
					} `json:"defaultBranchRef"`
					Refs struct {
						PageInfo struct {
							HasNextPage bool   `json:"hasNextPage"`
							EndCursor   string `json:"endCursor"`
						} `json:"pageInfo"`
						Nodes []struct {
							Name   string `json:"name"`
							Target struct {
								MessageHeadline string `json:"messageHeadline"`
							} `json:"target"`
						} `json:"nodes"`
					} `json:"refs"`
				} `json:"repository"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("branches of %s: %w", repo, err)
		}
		// ⛔ A GraphQL refusal arrives with HTTP 200 and an `errors` array, so the
		// call "succeeded". Reading past it would turn a refused query into an
		// empty branch list — a clean gate on a surface never read.
		if len(resp.Errors) > 0 {
			return nil, fmt.Errorf("branches of %s refused: %s", repo, resp.Errors[0].Message)
		}
		if resp.Data.Repository == nil {
			return nil, fmt.Errorf("branches of %s: no repository in the answer", repo)
		}
		def := ""
		if d := resp.Data.Repository.DefaultBranchRef; d != nil {
			def = d.Name
		}
		refs := resp.Data.Repository.Refs
		for _, n := range refs.Nodes {
			if h := hit(terms, n.Name, n.Target.MessageHeadline); h != "" {
				out = append(out, Branch{
					Name:    n.Name,
					Subject: n.Target.MessageHeadline,
					Hit:     h,
					Default: def != "" && n.Name == def,
				})
			}
		}
		if !refs.PageInfo.HasNextPage {
			return out, nil
		}
		// A page that says there is more but hands back no cursor cannot be
		// continued; that is an unread surface, not the end of one.
		if refs.PageInfo.EndCursor == "" {
			return nil, fmt.Errorf("branches of %s: more pages, but no cursor to reach them", repo)
		}
		after = refs.PageInfo.EndCursor
	}
}

// An Item is a tracker entry: an issue or a pull request, open or closed.
type Item struct {
	Number int
	Title  string
	State  string
	Repo   string
	IsPR   bool
	Hit    string
}

// Kind distinguishes a pull request from an issue, which changes what the entry
// means as prior art: a closed PR is work that was WRITTEN and rejected.
func (i Item) Kind() string {
	if i.IsPR {
		return "pr"
	}
	return "issue"
}

// Tracker reports the issues and pull requests under a scope — "repo:owner/name"
// or "org:name" — that carry one of the terms.
//
// ⛔ NO is:open QUALIFIER, deliberately. The entry that would have saved the day
// was closed, titled "(NOT MERGED — see #352)", and holds the reason the obvious
// fix was not taken. A search for open items only is the one search guaranteed
// to miss the most valuable prior art there is.
func Tracker(scope string, terms []string) ([]Item, int, error) {
	// The terms are OR-ed: GitHub's search treats space-separated words as AND,
	// which for a three-word subject finds only an entry that uses all three.
	var or []string
	for _, t := range terms {
		or = append(or, `"`+t+`"`)
	}
	q := scope + " " + strings.Join(or, " OR ")
	raw, err := gh("api", "-X", "GET", "search/issues",
		"-f", "q="+q, "-f", "per_page=100")
	if err != nil {
		return nil, 0, err
	}
	var page struct {
		Total int `json:"total_count"`
		Items []struct {
			Number      int    `json:"number"`
			Title       string `json:"title"`
			State       string `json:"state"`
			Body        string `json:"body"`
			URL         string `json:"repository_url"`
			PullRequest *struct {
				URL string `json:"url"`
			} `json:"pull_request"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, 0, fmt.Errorf("tracker %s: %w", scope, err)
	}
	var out []Item
	for _, it := range page.Items {
		h := hit(terms, it.Title, it.Body)
		if h == "" {
			// Search reads comments; this reads title and body. Still prior art,
			// and labelled for what it is rather than dropped.
			h = "comment"
		}
		out = append(out, Item{
			Number: it.Number,
			Title:  it.Title,
			State:  it.State,
			Repo:   repoOf(it.URL),
			IsPR:   it.PullRequest != nil,
			Hit:    h,
		})
	}
	// ⛔⛔ THE ORDER IS GITHUB'S BEST-MATCH ORDER, AND MINE WAS MEASURABLY WORSE.
	//
	// A first version re-ranked these by a score of my own — a term in the title
	// worth three in the body, the whole phrase worth more again. It was written
	// because the first real run printed 25 entries for "unrounded space width"
	// and a refusal nobody reads is not a control. The second real run, on the
	// same repository and subject, is what refuted it:
	//
	//	GitHub's order     #352, #612, #722 in the first six of 238
	//	my score           #95, #476, #177 first — #352 fourth
	//
	// #95's title holds two of the terms ("unrounded", "width") and #352's holds
	// one, so the score is right about the words and wrong about the subject.
	// GitHub's relevance already weighs term rarity, which is exactly what a
	// count of matches cannot. Replacing a ranking that works with a proxy that
	// counts is how a measure stops measuring; `sort` stays out of this file.
	return out, page.Total, nil
}

// repoOf names the repository an entry belongs to, from the repository_url the
// search returns — needed because one `org:` search spans many repositories.
func repoOf(apiURL string) string {
	parts := strings.Split(strings.TrimSuffix(apiURL, "/"), "/")
	if len(parts) < 2 {
		return apiURL
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1]
}
