package main

import (
	"fmt"
	"regexp"
	"strings"
)

// A Surface is one place an organisation advertises its repositories.
type Surface struct {
	Name string // "landing", "docs", "profile"
	Org  string // the organisation it belongs to
	Body string // everything that place holds, concatenated
	// Entry reports whether this surface names repo AS AN ENTRY.
	Entry func(body, repo string) bool
}

// ⛔ Every matcher below looks for the shape of a LISTING, never for the bare
// name. The bare name matches prose, and prose is how a sweep convinces itself
// a module is advertised when it is only mentioned.
var (
	// hugo.toml: name = "lz4"      data/*.yaml: - name: lz4
	tomlEntry = `(?m)^\s*name\s*=\s*"%s"\s*$`
	yamlEntry = `(?m)^\s*-?\s*name:\s*"?%s"?\s*$`
	// ⛔ AND THE SAME LIST WRITTEN ON ONE LINE. go-browserhttp's landing holds
	//   - { name: "gitcorsproxy", kind: "proxy", desc: "…" }
	// a YAML flow mapping, and the matcher above requires the line to end after
	// the name. It matched nothing there, so the landing named none of the
	// organisation's modules, so it was dropped as "not an index" — and the
	// organisation read as having one surface when it has two. A format the
	// reader cannot parse looks exactly like a page that says nothing.
	yamlFlowEntry = `(?m)^\s*-\s*\{[^}\n]*\bname:\s*"?%s"?\s*[,}]`
	// mkdocs.yml nav: - loop/: loop.md   (or bare loop.md)
	navEntry = `(?m)^\s*-?\s*[^:\n]*:?\s*%s\.md\s*$`
	// ⛔ A NAV LINE CAN NAME THE MODULE IN ITS TITLE instead of its filename.
	// go-reddit's documentation says `- The client (reddit): client.md`, and
	// matching only the filename called that organisation's one documented
	// module undocumented.
	//
	// Only the PARENTHESISED form counts. Accepting the name anywhere in the
	// title looked tidier and is the wrong trade: repository names here are
	// ordinary words — client, engine, core, docs — and a title containing one
	// by accident would mark a module documented when it is not. A false
	// "documented" is silence, which is the direction this tool exists to
	// break; a false "missing" only costs a pull request that gets closed.
	navTitle = `(?m)^\s*-\s*[^:\n]*\(%s\)[^:\n]*:\s*\S+\.(?:md|yml)\s*$`
	// a profile table links to the repository itself, IN THIS ORGANISATION.
	//
	// ⛔ It used to accept any owner, and a profile that says "pairs with
	// go-fileshare" then read as an organisation listing a module it does not
	// have. Cross-organisation links are the NORM in these profiles; matching
	// them turned every neighbour into a phantom.
	linkEntry = `\(https://github\.com/%s/%s\)`
)

func matches(pattern, body, repo string) bool {
	re, err := regexp.Compile(fmt.Sprintf(pattern, regexp.QuoteMeta(repo)))
	if err != nil {
		return false
	}
	return re.MatchString(body)
}

// landingEntry accepts either spelling, because the fleet uses both: hugo.toml
// holds the list in some organisations and data/*.yaml in others. go-filesystems
// keeps it in data/fs.yaml, not data/repos.yaml — a sweep that guessed the
// filename read nothing and reported all thirty modules missing.
func landingEntry(body, repo string) bool {
	return matches(tomlEntry, body, repo) ||
		matches(yamlEntry, body, repo) ||
		matches(yamlFlowEntry, body, repo)
}

func docsEntry(body, repo string) bool {
	return matches(navEntry, body, repo) || matches(navTitle, body, repo)
}

// profileEntry needs the organisation, so it is built per organisation rather
// than being a bare function like the other two.
func profileEntryFor(org string) func(string, string) bool {
	return func(body, repo string) bool {
		re, err := regexp.Compile(fmt.Sprintf(linkEntry, regexp.QuoteMeta(org), regexp.QuoteMeta(repo)))
		if err != nil {
			return false
		}
		return re.MatchString(body)
	}
}

// Advertised is the surfaces that name repo.
func Advertised(surfaces []Surface, repo string) []string {
	var on []string
	for _, s := range surfaces {
		if s.Body != "" && s.Entry(s.Body, repo) {
			on = append(on, s.Name)
		}
	}
	return on
}

// Stale is every repository a surface names that is not in have.
//
// It reads the entries out of the surface rather than testing a known list,
// because the interesting case is the name nobody remembers: a module that
// moved away and left its row behind.
//
// ⛔ ONLY the landing and the profile are asked, and running it is what taught
// me that. A documentation nav legitimately holds pages that were never
// repositories -- contributing.md, methodology.md -- so every one of them read
// as a module that had vanished. Worse, mkdocs.yml carries `theme:\n  name:
// material`, and a matcher looking for `name:` anywhere reported the THEME as
// a deleted repository, in two organisations at once.
//
// A nav entry is evidence of a page. It is not evidence of a repository, and
// the two are only the same in the organisations that happen to be laid out
// that way.
func Stale(s Surface, have map[string]bool) []string {
	var re *regexp.Regexp
	switch s.Name {
	case "landing":
		// ⛔ Only names under a REPOSITORY list count. A landing may list other
		// things with the same key: go-fileshare declares its five protocols as
		// [[params.protocols]] with `name = "smb"`, and a matcher that took
		// every `name` reported smb, nfs, sftp, s3 and webdav as repositories
		// the organisation had lost.
		return listedRepos(s.Body, s.Org, have)
	case "profile":
		re = regexp.MustCompile(`\(https://github\.com/` + regexp.QuoteMeta(s.Org) +
			`/([a-z0-9][a-z0-9._-]*)\)`)
	default:
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(s.Body, -1) {
		n := m[1]
		if n == "" || have[n] || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// infra is what an organisation has that is not a module: the places the
// advertising itself lives.
// listedRepos is the names under a repository list, and nothing else.
//
// TOML groups them under a [[params.repos]] header; YAML under a `modules:` or
// `repos:` key. Either way the KEY above the entries is what says "these are
// repositories", so that is what is followed.
func listedRepos(body, org string, have map[string]bool) []string {
	var out []string
	seen := map[string]bool{}
	inRepos := false
	curName, curOrg := "", ""
	name := regexp.MustCompile(`^\s*(?:-\s*)?name\s*[:=]\s*"?([a-z0-9][a-z0-9._-]*)"?\s*$`)
	// ⛔ AN ENTRY MAY NAME ANOTHER ORGANISATION, and then it is not a claim
	// about this one. go-composites' landing carries nonnil and respondto with
	// org = "go-vet-analyzers" and the words "Now maintained in the
	// go-vet-analyzers org" — which is exactly what a moved module should look
	// like — and both were reported as names this organisation had lost. The
	// page was right and the reader was wrong, which is the expensive
	// direction: a check that cries about correct pages stops being read.
	owner := regexp.MustCompile(`^\s*(?:-\s*)?(?:org|owner)\s*[:=]\s*"?([A-Za-z0-9][A-Za-z0-9._-]*)"?\s*$`)
	header := regexp.MustCompile(`^\s*\[\[params\.([a-z]+)\]\]`)
	yamlKey := regexp.MustCompile(`^(modules|repos|items):\s*$`)
	flowName := regexp.MustCompile(`^\s*-\s*\{[^}\n]*\bname:\s*"?([a-z0-9][a-z0-9._-]*)"?\s*[,}]`)
	flowOwner := regexp.MustCompile(`\b(?:org|owner):\s*"?([A-Za-z0-9][A-Za-z0-9._-]*)"?\s*[,}]`)

	flush := func() {
		defer func() { curName, curOrg = "", "" }()
		if curName == "" || (curOrg != "" && curOrg != org) {
			return
		}
		if have[curName] || seen[curName] {
			return
		}
		seen[curName] = true
		out = append(out, curName)
	}

	for _, line := range strings.Split(body, "\n") {
		if m := header.FindStringSubmatch(line); m != nil {
			flush()
			inRepos = m[1] == "repos"
			continue
		}
		if yamlKey.MatchString(line) {
			flush()
			inRepos = true
			continue
		}
		// a YAML key at column zero ends the block
		if inRepos && len(line) > 0 && line[0] != ' ' && line[0] != '-' && line[0] != '\t' {
			flush()
			inRepos = false
		}
		if !inRepos {
			continue
		}
		// A whole entry on one line: flush it immediately, since nothing
		// follows it to close it.
		if m := flowName.FindStringSubmatch(line); m != nil {
			flush()
			curName = m[1]
			if o := flowOwner.FindStringSubmatch(line); o != nil {
				curOrg = o[1]
			}
			flush()
			continue
		}
		if m := name.FindStringSubmatch(line); m != nil {
			// A second name in one block is the next entry.
			flush()
			curName = m[1]
			continue
		}
		if m := owner.FindStringSubmatch(line); m != nil {
			curOrg = m[1]
		}
	}
	flush()
	return out
}

func infra(org, repo string) bool {
	switch repo {
	case ".github", "brand", "docs", org + ".github.io":
		return true
	// ⛔ renovate-runner is the repository that RUNS Renovate for the
	// organisation -- about a hundred of them across the fleet. It is
	// machinery, like the three above, and a landing that advertised it would
	// be pointing readers at a cron job. Reported as an unlisted module in
	// three organisations before this line existed.
	case "renovate-runner":
		return true
	}
	return strings.HasSuffix(repo, ".github.io")
}
