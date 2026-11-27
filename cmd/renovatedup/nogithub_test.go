package main

import (
	"errors"
	"strings"
	"testing"
)

// withErrors puts an `errors` array in front of the data of a fixture answer.
func withErrors(body []byte, errs string) []byte {
	return []byte(strings.Replace(string(body), `{"data":`, `{"errors":[`+errs+`],"data":`, 1))
}

const notFoundCfg = `{"type":"NOT_FOUND","path":["organization","cfg"],"message":"Could not resolve to a Repository with the name 'acme/.github'."}`

// The shape GitHub answers for an organisation with no .github repository:
// data present, cfg null, one NOT_FOUND on organization.cfg.
func TestOrgWithoutGithubRepoIsReadAndCleanExit0(t *testing.T) {
	body := withErrors(pageJSON(nil, false, "", repo{name: "a", files: map[string]string{
		"renovate.json": `{"extends":["github>acme/.github"]}`,
	}}), notFoundCfg)
	code, out, errs := drive(t, one(body), "--orgs", "acme")
	if code != 0 {
		t.Fatalf("code %d\n%s\n%s", code, out, errs)
	}
	for _, want := range []string{
		"acme  no .github repository (nothing inherited, nothing to duplicate)",
		"1 org(s) read, 0 unread, 0 with inheritConfig, 0 repo(s) read, 0 STUB, 0 MIXED, 0 UNREAD",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestOtherErrorsOnTheCfgAliasStayUnread(t *testing.T) {
	for name, e := range map[string]string{
		"another path":      `{"type":"NOT_FOUND","path":["organization","repositories"],"message":"gone"}`,
		"a longer path":     `{"type":"NOT_FOUND","path":["organization","cfg","object"],"message":"gone"}`,
		"a shorter path":    `{"type":"NOT_FOUND","path":["organization"],"message":"gone"}`,
		"another root":      `{"type":"NOT_FOUND","path":["user","cfg"],"message":"gone"}`,
		"no path":           `{"type":"NOT_FOUND","message":"gone"}`,
		"another type":      `{"type":"FORBIDDEN","path":["organization","cfg"],"message":"gone"}`,
		"no type":           `{"path":["organization","cfg"],"message":"gone"}`,
		"tolerated + other": notFoundCfg + `,{"type":"FORBIDDEN","path":["organization","cfg"],"message":"gone"}`,
	} {
		body := withErrors(pageJSON(nil, false, "", repo{name: "a"}), e)
		code, out, _ := drive(t, one(body), "--orgs", "acme")
		if code != 2 || !strings.Contains(out, "acme  UNREAD  GraphQL: ") || strings.Contains(out, "no .github repository") {
			t.Errorf("%s: code %d\n%s", name, code, out)
		}
	}
}

func TestNotFoundOnTheOrganisationItselfStaysUnread(t *testing.T) {
	body := `{"data":{"organization":null},"errors":[{"type":"NOT_FOUND","path":["organization"],"message":"Could not resolve to an Organization"}]}`
	code, out, _ := drive(t, one([]byte(body)), "--orgs", "acme")
	if code != 2 || !strings.Contains(out, "UNREAD  GraphQL: Could not resolve to an Organization") {
		t.Fatalf("code %d\n%s", code, out)
	}
	// The tolerated error with no organisation in the data is not tolerated.
	body = `{"data":{"organization":null},"errors":[` + notFoundCfg + `]}`
	code, out, _ = drive(t, one([]byte(body)), "--orgs", "acme")
	if code != 2 || !strings.Contains(out, "GraphQL answered no organisation") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestNotFoundOnCfgWithACfgAnswerIsUnread(t *testing.T) {
	body := withErrors(pageJSON(str(cfgOn), false, ""), notFoundCfg)
	code, out, _ := drive(t, one(body), "--orgs", "acme")
	if code != 2 || !strings.Contains(out, "UNREAD  GraphQL: NOT_FOUND on .github yet a .github answer") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

// fetchGraphQL: gh exits non-zero with the answer on stdout; that answer is
// kept when it is JSON, and the failure kept when it is not.
func TestFetchGraphQLKeepsTheAnswerGhPrintsWithItsFailure(t *testing.T) {
	old := ghAnswer
	t.Cleanup(func() { ghAnswer = old })
	fail := errors.New("gh: Could not resolve")

	ghAnswer = func(args ...string) ([]byte, error) { return []byte(`{"data":{}}`), fail }
	if b, err := fetchGraphQL("acme", "", 100); err != nil || string(b) != `{"data":{}}` {
		t.Errorf("a JSON answer with a failing exit gave %q, %v", b, err)
	}
	ghAnswer = func(args ...string) ([]byte, error) { return []byte(`<html>`), fail }
	if _, err := fetchGraphQL("acme", "", 100); err != fail {
		t.Errorf("a non-JSON answer gave %v, want the failure", err)
	}
	ghAnswer = func(args ...string) ([]byte, error) { return nil, fail }
	if _, err := fetchGraphQL("acme", "", 100); err != fail {
		t.Errorf("no answer gave %v, want the failure", err)
	}
	var seen []string
	ghAnswer = func(args ...string) ([]byte, error) { seen = args; return []byte(`{}`), nil }
	if _, err := fetchGraphQL("acme", "CUR", 25); err != nil ||
		!strings.Contains(strings.Join(seen, " "), "cursor=CUR") ||
		!strings.Contains(strings.Join(seen, " "), "-F first=25") {
		t.Errorf("a clean answer: %v, args %v", err, seen)
	}
}
