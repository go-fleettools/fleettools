package quiet

import (
	"strings"
	"testing"
)

// The shape the fleet's runner workflows actually have: one secret of its own,
// and GitHub's automatic one beside it.
const renovateWorkflow = `name: Renovate

on:
  schedule:
    - cron: '44 16 * * *'
  workflow_dispatch:

jobs:
  renovate:
    runs-on: ubuntu-latest
    steps:
      - uses: renovatebot/github-action@v46.3.11
        with:
          token: ${{ secrets.RENOVATE_TOKEN }}
        env:
          RENOVATE_PLATFORM_COMMIT: "true"
          GITHUB_COM_TOKEN: ${{ secrets.GITHUB_TOKEN }}
`

// ⛔⛔ WITHOUT THE AUTO-PROVIDED SET THIS CHECK FIRES ON EVERYTHING. Nearly
// every workflow in the fleet references secrets.GITHUB_TOKEN, which GitHub
// injects itself and which appears in NO secret listing. Count it and all 156
// runners are reported as missing a secret -- and a check that fires on
// everything is read as firing on nothing, which is worse than not having it.
func TestGitHubTokenIsNotAMissingSecret(t *testing.T) {
	got := SecretsReferenced(renovateWorkflow)
	if len(got) != 1 || got[0] != "RENOVATE_TOKEN" {
		t.Fatalf("SecretsReferenced = %v, want exactly [RENOVATE_TOKEN]", got)
	}
	// And the control, so this test measures the exclusion rather than the
	// regexp: the reference IS present in the text.
	if !strings.Contains(renovateWorkflow, "secrets.GITHUB_TOKEN") {
		t.Fatal("the fixture no longer references GITHUB_TOKEN, so this test proves nothing")
	}
}

func TestSecretsReferencedForms(t *testing.T) {
	for _, c := range []struct {
		name, src, want string
	}{
		{"spaced", `${{ secrets.A }}`, "A"},
		{"tight", `${{secrets.B}}`, "B"},
		{"wrapped in a function", `${{ fromJSON(secrets.C).x }}`, "C"},
		{"underscores and digits", `${{ secrets.MY_TOKEN_2 }}`, "MY_TOKEN_2"},
		// Deduplicated and sorted, so a report line is stable.
		{"repeated", `${{ secrets.Z }} ${{ secrets.Z }}`, "Z"},
		{"none at all", `runs-on: ubuntu-latest`, ""},
		// ⚠ A name must not be cut short by a dot that is not part of it.
		{"followed by a field", `${{ secrets.TOK.y }}`, "TOK"},
	} {
		got := strings.Join(SecretsReferenced(c.src), ",")
		if got != c.want {
			t.Errorf("%s: SecretsReferenced(%q) = %q, want %q", c.name, c.src, got, c.want)
		}
	}
}

// ⚠ AN ORGANISATION SECRET IS STILL A SECRET. Looking only at the repository's
// listing would report every organisation-wide credential as missing.
func TestAnOrganisationSecretCounts(t *testing.T) {
	if got := MissingSecrets([]string{"TOK"}, nil, []string{"TOK"}); len(got) != 0 {
		t.Errorf("MissingSecrets = %v, want none: it is held at the organisation", got)
	}
	if got := MissingSecrets([]string{"TOK"}, []string{"TOK"}, nil); len(got) != 0 {
		t.Errorf("MissingSecrets = %v, want none: it is held at the repository", got)
	}
	if got := MissingSecrets([]string{"TOK"}, []string{"OTHER"}, []string{"ELSE"}); len(got) != 1 {
		t.Errorf("MissingSecrets = %v, want [TOK]", got)
	}
}

// ⛔⛔ THE SIXTEEN, NAMED INSTEAD OF POINTED AT. go-approx listed NO secrets at
// all while its workflow asked for RENOVATE_TOKEN, and every one of its runs
// failed in ten seconds. `broken` would say "read that run's logs and check the
// secret"; this says which secret, having checked.
func TestAWorkflowAskingForASecretNobodyHolds(t *testing.T) {
	r := healthyRunner()
	r.SecretsWanted = SecretsReferenced(renovateWorkflow)
	r.RepoSecrets = nil // go-approx listed NO secrets at all
	r.OrgSecrets = nil
	// The runs were failing, which is what makes the absence a diagnosis rather
	// than a guess -- see TestAHealthyRunnerProvesItsOwnSecretResolves.
	r.LastConclusion = "failure"

	got := classify(r, now)
	if got.Verdict != MissingSecret {
		t.Fatalf("verdict = %s, want missing_secret", got.Verdict)
	}
	if len(got.MissingSecrets) != 1 || got.MissingSecrets[0] != "RENOVATE_TOKEN" {
		t.Errorf("MissingSecrets = %v, want [RENOVATE_TOKEN]", got.MissingSecrets)
	}
	if !MissingSecret.Pages() {
		t.Error("missing_secret must page")
	}
	// The control: hand it the secret and the verdict falls back to the failure
	// itself, so the upgrade is driven by the secret and not by the fixture.
	r.RepoSecrets = []string{"RENOVATE_TOKEN"}
	if was := classify(r, now); was.Verdict != Broken {
		t.Fatalf("with the secret present the runner is %s, want broken", was.Verdict)
	}
}

// ⛔⛔ THE MEASUREMENT THAT CHANGED THIS DESIGN. Only the REPOSITORY's secret
// listing can be read: `GET /orgs/{org}/actions/secrets` answers 403 for this
// fleet's token -- "You must be an org admin or have the actions secrets
// fine-grained permission", "needs the admin:org scope" -- measured 2026-11-01
// on go-rouge and every other organisation tried.
//
// The first version of this check asked for the organisation listing too, and
// would have been DEAD FLEET-WIDE. Widening the token to admin:org so a
// read-only health check can enumerate every organisation's credentials is a
// bad trade. It is also unnecessary: a scheduled run that SUCCEEDED has already
// proved the secret resolves, wherever it lives. So a working runner stays
// healthy, and nothing is reported missing -- otherwise every organisation-level
// credential in the fleet reads as absent, and the check that fires on
// everything is read as firing on nothing.
func TestAHealthyRunnerProvesItsOwnSecretResolves(t *testing.T) {
	r := healthyRunner()
	r.SecretsWanted = []string{"RENOVATE_TOKEN"}
	r.RepoSecrets = nil // absent HERE, because it lives at the organisation
	r.OrgSecrets = nil  // unreadable without admin:org

	got := classify(r, now)
	if got.Verdict != Healthy {
		t.Fatalf("verdict = %s, want healthy: a run that succeeded proves the secret resolves", got.Verdict)
	}
	// ⚠ But the fact is NOT hidden: it is still on the Result, so the report
	// line can say it. It is the one thing that would explain a sudden failure
	// here later.
	if len(got.MissingSecrets) != 1 {
		t.Errorf("MissingSecrets = %v, want [RENOVATE_TOKEN] recorded though not paged on", got.MissingSecrets)
	}
	if got.Verdict.Pages() {
		t.Error("a working runner must not page over an organisation-level secret")
	}
}

// ⛔⛔ AN UNREAD LISTING IS UNREAD, NOT EMPTY. Reading repository secrets needs
// collaborator access, so a token that cannot see them gets a refusal -- and
// "I was not allowed to look" must never be published as "the secret is not
// there". That would invent the exact finding this check exists to make, on
// every repository the token cannot fully see.
func TestARefusedListingDoesNotInventAMissingSecret(t *testing.T) {
	r := healthyRunner()
	r.SecretsWanted = []string{"RENOVATE_TOKEN"}
	r.RepoSecrets, r.OrgSecrets = nil, nil
	r.LastConclusion = "failure" // so only the note can suppress the verdict
	r.SecretsNote = "repository secrets: gh: Not Found (HTTP 404)"

	got := classify(r, now)
	if got.Verdict == MissingSecret {
		t.Fatal("a refused listing was reported as a missing secret")
	}
	if len(got.MissingSecrets) != 0 {
		t.Errorf("MissingSecrets = %v, want none when the listing was not read", got.MissingSecrets)
	}
	// And the control: with the note cleared and nothing else changed, it DOES
	// fire -- so the suppression is what this test measured.
	r.SecretsNote = ""
	if was := classify(r, now); was.Verdict != MissingSecret {
		t.Fatalf("with the note cleared the verdict is %s, want missing_secret", was.Verdict)
	}
}

// The absent secret outranks the failing run it causes: "read that run's logs"
// would send the reader to a log that only says what the other line says.
func TestTheAbsentSecretOutranksTheFailingRun(t *testing.T) {
	r := healthyRunner()
	r.LastConclusion = "failure"
	r.SecretsWanted = []string{"RENOVATE_TOKEN"}

	if got := classify(r, now); got.Verdict != MissingSecret {
		t.Fatalf("verdict = %s, want missing_secret to outrank broken", got.Verdict)
	}
	// The control: give it the secret and the failure speaks for itself again.
	r.RepoSecrets = []string{"RENOVATE_TOKEN"}
	if was := classify(r, now); was.Verdict != Broken {
		t.Fatalf("with the secret present the verdict is %s, want broken", was.Verdict)
	}
}

// A workflow that asks for nothing can never be missing anything, and must not
// cost a request either -- the reader only looks when something is referenced.
func TestAWorkflowWithNoSecretsIsNotJudged(t *testing.T) {
	r := healthyRunner()
	r.SecretsWanted = nil
	if got := classify(r, now); got.Verdict != Healthy {
		t.Errorf("verdict = %s, want healthy", got.Verdict)
	}
}
