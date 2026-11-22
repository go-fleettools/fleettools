package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// candidates are the places Renovate looks for a repository's own config, in
// ITS order (lib/config/app-strings.ts configFileNames). The first one that
// exists wins, so that is the only one read. package.json's "renovate" key is
// the last of Renovate's list and is deliberately not read: the stub files that
// caused the defect are renovate.json, and a package.json key would cost one
// more Blob per repository.
var candidates = []string{
	"renovate.json",
	"renovate.json5",
	".github/renovate.json",
	".github/renovate.json5",
	".gitlab/renovate.json",
	".renovaterc",
	".renovaterc.json",
	".renovaterc.json5",
}

// Inherited names the preset Renovate merges into every repository of an
// organisation: Repo is lower-cased "org/repo", File the file inside it.
type Inherited struct {
	Repo string
	File string
}

// Renovate's documented defaults when config.js sets inheritConfig but not the
// names (docs/usage/self-hosted-configuration.md).
const (
	defaultInheritRepo = "{{parentOrg}}/renovate-config"
	defaultInheritFile = "org-inherited-config.json"
)

var (
	blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	reOn         = regexp.MustCompile(`(?:^|[\s{,'"])inheritConfig['"]?\s*:\s*([^\s,}]+)`)
	reRepoName   = regexp.MustCompile("inheritConfigRepoName['\"]?\\s*:\\s*['\"`]([^'\"`]*)['\"`]")
	reFileName   = regexp.MustCompile("inheritConfigFileName['\"]?\\s*:\\s*['\"`]([^'\"`]*)['\"`]")
)

// stripComments removes block comments and whole-line // comments, so a
// commented-out `inheritConfig: true` is not read as on. A // inside a string
// on a code line is left alone.
func stripComments(s string) string {
	s = blockComment.ReplaceAllString(s, "")
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "//") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// ParseOrgConfig reads an organisation's self-hosted config.js. on is whether
// inheritConfig is the literal true. A value that is neither true nor false (a
// variable, an expression) is an error: guessing it would either invent a
// duplicate or hide one.
func ParseOrgConfig(text, org string) (on bool, in Inherited, err error) {
	text = stripComments(text)
	m := reOn.FindStringSubmatch(text)
	if m == nil {
		return false, Inherited{}, nil // key absent: Renovate's default is false
	}
	switch m[1] {
	case "true":
		on = true
	case "false":
		return false, Inherited{}, nil
	default:
		return false, Inherited{}, fmt.Errorf("inheritConfig is %q, not a literal true or false", m[1])
	}
	repo, file := defaultInheritRepo, defaultInheritFile
	if m := reRepoName.FindStringSubmatch(text); m != nil {
		repo = m[1]
	}
	if m := reFileName.FindStringSubmatch(text); m != nil {
		file = m[1]
	}
	repo = strings.ReplaceAll(repo, "{{parentOrg}}", org)
	repo = strings.ReplaceAll(repo, "{{topLevelOrg}}", org)
	return true, Inherited{Repo: strings.ToLower(repo), File: file}, nil
}

// Matches reports whether an extends entry names the inherited file: the
// github> or local> forms, the repository compared case-insensitively, an
// optional :preset and an optional #ref. `github>ORG/.github` is the preset
// "default", which is default.json.
func (in Inherited) Matches(entry string) bool {
	e := strings.TrimSpace(entry)
	switch {
	case strings.HasPrefix(e, "github>"):
		e = e[len("github>"):]
	case strings.HasPrefix(e, "local>"):
		e = e[len("local>"):]
	default:
		return false
	}
	if i := strings.IndexByte(e, '#'); i >= 0 {
		e = e[:i]
	}
	repo, file := e, ""
	if i := strings.IndexByte(e, ':'); i >= 0 {
		repo, file = e[:i], e[i+1:]
	}
	if file == "" {
		file = "default"
	}
	if !strings.HasSuffix(file, ".json") && !strings.HasSuffix(file, ".json5") {
		file += ".json"
	}
	return strings.EqualFold(repo, in.Repo) && strings.EqualFold(file, in.File)
}

// Kind classifies a hit.
type Kind int

const (
	None  Kind = iota // no extends entry names the inherited preset
	Stub              // the file holds nothing but that entry (and $schema)
	Mixed             // other keys or other extends entries too
)

func (k Kind) String() string {
	switch k {
	case Stub:
		return "STUB"
	case Mixed:
		return "MIXED"
	}
	return "none"
}

// Classify reads one Renovate config file. It returns an error, never None,
// for text it cannot parse: strict JSON only, so JSON5 features (comments,
// trailing commas, unquoted keys) are unread, with the parser's reason.
func Classify(text string, in Inherited) (Kind, string, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return None, "", fmt.Errorf("not strict JSON (%v)", err)
	}
	var entries []string
	if raw, ok := doc["extends"]; ok {
		var list []any
		if json.Unmarshal(raw, &list) != nil {
			var one string
			if json.Unmarshal(raw, &one) != nil {
				return None, "", errors.New("extends is neither a string nor a list")
			}
			list = []any{one}
		}
		for _, v := range list {
			if s, ok := v.(string); ok {
				entries = append(entries, s)
			}
		}
	}
	hit, others := "", 0
	for _, e := range entries {
		if hit == "" && in.Matches(e) {
			hit = e
		} else {
			others++
		}
	}
	if hit == "" {
		return None, "", nil
	}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k != "$schema" && k != "extends" {
			return Mixed, hit, nil
		}
	}
	if others > 0 {
		return Mixed, hit, nil
	}
	return Stub, hit, nil
}
