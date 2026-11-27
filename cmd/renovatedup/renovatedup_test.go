package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

const cfgOn = `module.exports = {
  inheritConfig: true,
  inheritConfigRepoName: '{{parentOrg}}/.github',
  inheritConfigFileName: 'default.json',
};`

// repo is a fixture repository: files maps a candidate path to its text.
type repo struct {
	name            string
	archived, fork  bool
	files           map[string]string
	truncatedInPath string
}

func pageJSON(cfg *string, next bool, cursor string, repos ...repo) []byte {
	blobOf := func(s string, trunc bool) any {
		return map[string]any{"text": s, "isTruncated": trunc}
	}
	var nodes []any
	for _, r := range repos {
		n := map[string]any{"name": r.name, "isArchived": r.archived, "isFork": r.fork}
		for i, p := range candidates {
			k := fmt.Sprintf("f%d", i)
			if txt, ok := r.files[p]; ok {
				n[k] = blobOf(txt, r.truncatedInPath == p)
			} else {
				n[k] = nil
			}
		}
		nodes = append(nodes, n)
	}
	var cfgObj any
	if cfg != nil {
		cfgObj = map[string]any{"object": blobOf(*cfg, false)}
	}
	b, _ := json.Marshal(map[string]any{"data": map[string]any{"organization": map[string]any{
		"cfg": cfgObj,
		"repositories": map[string]any{
			"pageInfo": map[string]any{"hasNextPage": next, "endCursor": cursor},
			"nodes":    nodes,
		},
	}}})
	return b
}

func str(s string) *string { return &s }

// drive runs the command with a stub fetch and returns status, stdout, stderr.
func drive(t *testing.T, fetch fetchFunc, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	c := rootWith(fetch, func() ([]string, error) { return nil, errors.New("Orgs must not be called here") })
	code := cli.Drive(c, args, &out, &errb)
	return code, out.String(), errb.String()
}

func one(body []byte) fetchFunc {
	return func(org, cursor string, _ int) ([]byte, error) { return body, nil }
}

func TestStubIsFlaggedAsStub(t *testing.T) {
	body := pageJSON(str(cfgOn), false, "", repo{name: "a", files: map[string]string{
		"renovate.json": `{"$schema":"https://docs.renovatebot.com/renovate-schema.json","extends":["github>acme/.github"]}`,
	}})
	code, out, _ := drive(t, one(body), "--orgs", "acme")
	if code != 1 || !strings.Contains(out, "acme/a  STUB  renovate.json") || !strings.Contains(out, "1 STUB, 0 MIXED, 0 UNREAD") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestMixedIsFlaggedAsMixed(t *testing.T) {
	body := pageJSON(str(cfgOn), false, "", repo{name: "a", files: map[string]string{
		".github/renovate.json": `{"extends":["github>acme/.github"],"packageRules":[]}`,
	}}, repo{name: "b", files: map[string]string{
		"renovate.json": `{"extends":["config:base","github>acme/.github"]}`,
	}})
	code, out, _ := drive(t, one(body), "--orgs", "acme")
	if code != 1 || !strings.Contains(out, "acme/a  MIXED  .github/renovate.json") ||
		!strings.Contains(out, "acme/b  MIXED  renovate.json") || !strings.Contains(out, "0 STUB, 2 MIXED") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestSuffixAndLocalForms(t *testing.T) {
	for _, e := range []string{
		"github>acme/.github#v1.2.3", "github>acme/.github:default", "github>acme/.github:default.json",
		"github>ACME/.github", "local>acme/.github", "local>Acme/.github:default#main", "github>acme/.github:default#abc",
	} {
		body := pageJSON(str(cfgOn), false, "", repo{name: "a", files: map[string]string{
			"renovate.json": `{"extends":["` + e + `"]}`}})
		code, out, _ := drive(t, one(body), "--orgs", "acme")
		if code != 1 || !strings.Contains(out, "  STUB  renovate.json") {
			t.Errorf("%s: code %d\n%s", e, code, out)
		}
	}
}

func TestOtherTargetsAreNotFlagged(t *testing.T) {
	for _, e := range []string{
		"github>other/.github", "github>acme/other", "github>acme/.github:other", "config:base",
		"github>acme/.github:default.json5", "gitlab>acme/.github", "acme/.github",
	} {
		body := pageJSON(str(cfgOn), false, "", repo{name: "a", files: map[string]string{
			"renovate.json": `{"extends":["` + e + `"]}`}})
		code, out, _ := drive(t, one(body), "--orgs", "acme")
		if code != 0 || !strings.Contains(out, "0 STUB, 0 MIXED") {
			t.Errorf("%s: code %d\n%s", e, code, out)
		}
	}
}

func TestCustomInheritedNames(t *testing.T) {
	cfg := `module.exports = { inheritConfig: true, inheritConfigRepoName: '{{parentOrg}}/shared', inheritConfigFileName: 'base.json' }`
	hit := pageJSON(str(cfg), false, "", repo{name: "a", files: map[string]string{"renovate.json": `{"extends":["github>acme/shared:base"]}`}})
	if code, out, _ := drive(t, one(hit), "--orgs", "acme"); code != 1 {
		t.Fatalf("custom target not flagged: %d\n%s", code, out)
	}
	miss := pageJSON(str(cfg), false, "", repo{name: "a", files: map[string]string{"renovate.json": `{"extends":["github>acme/.github"]}`}})
	if code, out, _ := drive(t, one(miss), "--orgs", "acme"); code != 0 {
		t.Fatalf("default target flagged although inherited names differ: %d\n%s", code, out)
	}
	def := `module.exports = { inheritConfig: true }`
	hit = pageJSON(str(def), false, "", repo{name: "a", files: map[string]string{"renovate.json": `{"extends":["github>acme/renovate-config:org-inherited-config"]}`}})
	if code, out, _ := drive(t, one(hit), "--orgs", "acme"); code != 1 {
		t.Fatalf("documented defaults not applied: %d\n%s", code, out)
	}
}

func TestInheritConfigOffIsNotFlagged(t *testing.T) {
	stub := repo{name: "a", files: map[string]string{"renovate.json": `{"extends":["github>acme/.github"]}`}}
	for name, cfg := range map[string]*string{
		"false":     str(`module.exports = { inheritConfig: false }`),
		"absent":    str(`module.exports = { platform: 'github' }`),
		"commented": str("module.exports = {\n // inheritConfig: true,\n /* inheritConfig: true */\n}"),
		"no file":   nil,
	} {
		code, out, _ := drive(t, one(pageJSON(cfg, false, "", stub)), "--orgs", "acme")
		if code != 0 || !strings.Contains(out, "inheritConfig off") || !strings.Contains(out, "0 with inheritConfig") {
			t.Errorf("%s: code %d\n%s", name, code, out)
		}
	}
}

func TestInheritConfigNotALiteralIsUnread(t *testing.T) {
	code, out, _ := drive(t, one(pageJSON(str(`module.exports = { inheritConfig: process.env.X }`), false, "")), "--orgs", "acme")
	if code != 2 || !strings.Contains(out, "acme  UNREAD") || !strings.Contains(out, "0 org(s) read, 1 unread") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestUnparsableConfigIsExit2(t *testing.T) {
	body := pageJSON(str(cfgOn), false, "",
		repo{name: "j5", files: map[string]string{"renovate.json5": "{ extends: ['github>acme/.github'], // c\n}"}},
		repo{name: "bad", files: map[string]string{"renovate.json": `{"extends": [`}})
	code, out, _ := drive(t, one(body), "--orgs", "acme")
	if code != 2 || !strings.Contains(out, "acme/j5  UNREAD  renovate.json5: not strict JSON") ||
		!strings.Contains(out, "acme/bad  UNREAD  renovate.json") || !strings.Contains(out, "2 UNREAD") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestUnreadOutranksADuplicate(t *testing.T) {
	body := pageJSON(str(cfgOn), false, "",
		repo{name: "dup", files: map[string]string{"renovate.json": `{"extends":["github>acme/.github"]}`}},
		repo{name: "bad", files: map[string]string{"renovate.json": `nope`}})
	if code, out, _ := drive(t, one(body), "--orgs", "acme"); code != 2 || !strings.Contains(out, "1 STUB") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestTruncatedBlobIsUnread(t *testing.T) {
	body := pageJSON(str(cfgOn), false, "", repo{name: "a", truncatedInPath: "renovate.json",
		files: map[string]string{"renovate.json": `{"extends":["github>acme/.github"]}`}})
	if code, out, _ := drive(t, one(body), "--orgs", "acme"); code != 2 || !strings.Contains(out, "truncated") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestFailedOrgQueryIsExit2AndCounted(t *testing.T) {
	good := pageJSON(str(cfgOn), false, "", repo{name: "a", files: map[string]string{}})
	fetch := func(org, cursor string, _ int) ([]byte, error) {
		if org == "bad" {
			return nil, errors.New("HTTP 502")
		}
		return good, nil
	}
	code, out, errs := drive(t, fetch, "--orgs", "ok,bad")
	if code != 2 || !strings.Contains(out, "1 org(s) read, 1 unread, 1 with inheritConfig, 1 repo(s) read") ||
		!strings.Contains(errs, "bad: HTTP 502") {
		t.Fatalf("code %d\n%s\n%s", code, out, errs)
	}
}

func TestGraphQLErrorsAndMissingOrgAreUnread(t *testing.T) {
	for name, body := range map[string]string{
		"errors":  `{"data":null,"errors":[{"message":"boom"}]}`,
		"no org":  `{"data":{"organization":null}}`,
		"garbage": `<html>`,
	} {
		if code, out, _ := drive(t, one([]byte(body)), "--orgs", "acme"); code != 2 || !strings.Contains(out, "acme  UNREAD") {
			t.Errorf("%s: code %d\n%s", name, code, out)
		}
	}
}

func TestNoOrganisationIsExit2(t *testing.T) {
	var out, errb bytes.Buffer
	c := rootWith(one(nil), func() ([]string, error) { return nil, nil })
	code := cli.Drive(c, nil, &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), "no organisation") {
		t.Fatalf("%d / %s", code, errb.String())
	}
}

func TestOrgListFailureIsExit2(t *testing.T) {
	var out, errb bytes.Buffer
	c := rootWith(one(nil), func() ([]string, error) { return nil, errors.New("gh down") })
	if code := cli.Drive(c, nil, &out, &errb); code != 2 || !strings.Contains(errb.String(), "gh down") {
		t.Fatalf("%d / %s", code, errb.String())
	}
}

func TestArchivedAndForksAreSkipped(t *testing.T) {
	dup := map[string]string{"renovate.json": `{"extends":["github>acme/.github"]}`}
	body := pageJSON(str(cfgOn), false, "", repo{name: "old", archived: true, files: dup}, repo{name: "fk", fork: true, files: dup})
	code, out, _ := drive(t, one(body), "--orgs", "acme")
	if code != 0 || !strings.Contains(out, "0 repo(s) read") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestFirstConfigFileWins(t *testing.T) {
	body := pageJSON(str(cfgOn), false, "", repo{name: "a", files: map[string]string{
		"renovate.json":         `{"extends":["config:base"]}`,
		".github/renovate.json": `{"extends":["github>acme/.github"]}`,
	}})
	if code, out, _ := drive(t, one(body), "--orgs", "acme"); code != 0 {
		t.Fatalf("a later file was judged: %d\n%s", code, out)
	}
}

func TestPaginationFollowsTheCursor(t *testing.T) {
	var cursors []string
	fetch := func(org, cursor string, _ int) ([]byte, error) {
		cursors = append(cursors, cursor)
		if cursor == "" {
			return pageJSON(str(cfgOn), true, "C1", repo{name: "a"}), nil
		}
		return pageJSON(str(cfgOn), false, "", repo{name: "b", files: map[string]string{"renovate.json": `{"extends":"github>acme/.github"}`}}), nil
	}
	code, out, _ := drive(t, fetch, "--orgs", "acme")
	if code != 1 || strings.Join(cursors, ",") != ",C1" || !strings.Contains(out, "2 repo(s) read") || !strings.Contains(out, "acme/b  STUB") {
		t.Fatalf("code %d cursors %v\n%s", code, cursors, out)
	}
}

func TestHalfReadOrgContributesNothing(t *testing.T) {
	fetch := func(org, cursor string, _ int) ([]byte, error) {
		if cursor == "" {
			return pageJSON(str(cfgOn), true, "C1", repo{name: "a", files: map[string]string{"renovate.json": `{"extends":["github>acme/.github"]}`}}), nil
		}
		return nil, errors.New("HTTP 500")
	}
	code, out, _ := drive(t, fetch, "--orgs", "acme")
	if code != 2 || !strings.Contains(out, "0 STUB") || !strings.Contains(out, "0 repo(s) read") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestPositionalArgumentIsRefused(t *testing.T) {
	good := pageJSON(str(cfgOn), false, "", repo{name: "a"})
	code, out, errs := drive(t, one(good), "--orgs", "acme", "acme")
	if code != 2 || out != "" || !strings.Contains(errs, "usage: renovatedup") {
		t.Fatalf("code %d\n%s\n%s", code, out, errs)
	}
}

func TestPartialGraphQLErrorsAreUnread(t *testing.T) {
	// data is present AND errors is set: GitHub answered some of it. Partial
	// is not a clean answer.
	ok := string(pageJSON(str(cfgOn), false, "", repo{name: "a"}))
	body := strings.Replace(ok, `{"data":`, `{"errors":[{"message":"partial"}],"data":`, 1)
	code, out, _ := drive(t, one([]byte(body)), "--orgs", "acme")
	if code != 2 || !strings.Contains(out, "GraphQL: partial") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestUnreadReasonsAreSpecific(t *testing.T) {
	for body, want := range map[string]string{
		`<html>`: "decoding the GraphQL answer",
		`{"data":null,"errors":[{"message":"boom"}]}`: "GraphQL: boom",
		`{"data":{"organization":null}}`:              "GraphQL answered no organisation",
	} {
		code, out, _ := drive(t, one([]byte(body)), "--orgs", "acme")
		if code != 2 || !strings.Contains(out, want) {
			t.Errorf("%s: code %d\n%s", body, code, out)
		}
	}
}

func TestTruncatedOrgConfigIsUnread(t *testing.T) {
	body := strings.Replace(string(pageJSON(str(cfgOn), false, "", repo{name: "a"})), `"isTruncated":false`, `"isTruncated":true`, 1)
	code, out, _ := drive(t, one([]byte(body)), "--orgs", "acme")
	if code != 2 || !strings.Contains(out, "config.js is truncated") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestMissingCursorStopsInsteadOfLooping(t *testing.T) {
	calls := 0
	fetch := func(org, cursor string, _ int) ([]byte, error) {
		if calls++; calls > 3 {
			t.Fatal("kept paging without a cursor")
		}
		return pageJSON(str(cfgOn), true, "", repo{name: "a"}), nil
	}
	code, out, _ := drive(t, fetch, "--orgs", "acme")
	if code != 2 || !strings.Contains(out, "gave no cursor") || calls != 1 {
		t.Fatalf("code %d calls %d\n%s", code, calls, out)
	}
}

func TestQueryAsksForEveryCandidate(t *testing.T) {
	q := buildQuery()
	for i, p := range candidates {
		if !strings.Contains(q, fmt.Sprintf("f%d: object(expression: \"HEAD:%s\")", i, p)) {
			t.Errorf("query lacks %s", p)
		}
	}
	if !strings.Contains(q, `"HEAD:config.js"`) || !strings.Contains(q, "first: $first") || !strings.Contains(q, "$first: Int!") {
		t.Errorf("query: %s", q)
	}
}

func TestHelpSaysPackageJSONIsNotRead(t *testing.T) {
	if !strings.Contains(longHelp, "key of package.json is NOT read") {
		t.Fatal("help must say package.json is ignored")
	}
}

func TestKindString(t *testing.T) {
	if Stub.String() != "STUB" || Mixed.String() != "MIXED" || None.String() != "none" {
		t.Fatal("kind names")
	}
}
