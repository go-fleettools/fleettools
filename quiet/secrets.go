package quiet

import (
	"regexp"
	"sort"
	"strings"
)

// ⛔⛔ THE FAILURE THIS FILE EXISTS TO CATCH COST TEN DAYS AND SIXTEEN
// ORGANISATIONS. Their runner workflow said
//
//	token: ${{ secrets.RENOVATE_TOKEN }}
//
// and the secret did not exist. An unset secret expands to the EMPTY STRING --
// GitHub does not refuse the workflow, it runs it with nothing there -- so the
// action failed in about ten seconds every night and none of those sixteen
// organisations ever received a dependency update.
//
// ⭐ AND THE WORKFLOW SAYS WHICH SECRETS IT NEEDS, so the name of the fix is
// free: it is in the file this tool already fetches for the cron. Only the
// listing it is compared against costs a request.
//
// ⛔ WHAT CANNOT BE READ, AND WHY THAT SHAPED THE ANSWER. Only the REPOSITORY's
// secret listing is available: `GET /orgs/{org}/actions/secrets` answers 403 --
// "You must be an org admin or have the actions secrets fine-grained
// permission", "needs the admin:org scope" -- measured across this fleet on
// 2026-11-01. Widening the fleet's token to admin:org so a read-only health
// check could enumerate every organisation's credentials is a bad trade, and
// the measurement showed it is unnecessary: a runner whose scheduled run
// SUCCEEDED has already proved its secret resolves, from wherever. So absence
// from the repository listing is reported only on a runner that is failing or
// has never run, where it is the diagnosis -- and merely printed on one that
// works. See Classify.

// secretRef matches a `secrets.NAME` reference in a workflow expression.
//
// It deliberately does not try to parse `${{ ... }}` as an expression. The
// reference is looked for anywhere, because the forms in the wild are
// `${{ secrets.X }}`, `${{secrets.X}}`, `${{ fromJSON(secrets.X) }}` and
// `secrets: inherit` adjacent lines -- and a reference missed is a check not
// made, which is the failure mode this file is about.
var secretRef = regexp.MustCompile(`\bsecrets\.([A-Za-z_][A-Za-z0-9_]*)`)

// autoProvided are the secrets GitHub injects itself. They never appear in any
// repository's or organisation's secret listing.
//
// ⚠ WITHOUT THIS SET THE CHECK IS WORSE THAN USELESS: almost every workflow in
// the fleet references `secrets.GITHUB_TOKEN`, so every single one of them would
// be reported as missing a secret, and a check that fires on everything is read
// as firing on nothing.
var autoProvided = map[string]bool{
	"GITHUB_TOKEN": true,
}

// SecretsReferenced returns the secret names a workflow asks GitHub to inject,
// sorted and deduplicated, excluding the ones GitHub provides by itself.
func SecretsReferenced(workflow string) []string {
	seen := map[string]bool{}
	for _, m := range secretRef.FindAllStringSubmatch(workflow, -1) {
		if name := m[1]; !autoProvided[name] {
			seen[name] = true
		}
	}
	return sortedKeys(seen)
}

// MissingSecrets returns the referenced names that appear in NEITHER the
// repository's nor the organisation's secret listing.
//
// ⚠ IT IS DELIBERATELY BIASED TOWARDS SAYING NOTHING. An organisation secret
// can be scoped to selected repositories, and this does not ask which -- so a
// secret listed at the organisation but not shared with THIS repository reads
// here as present. That is a false negative, and it is the right direction for
// a check that pages: the `broken` verdict still catches the run that fails,
// whereas a watcher that cries wolf over organisation-wide secrets gets muted
// and then catches nothing at all.
//
// The caller must not call this at all when either listing could not be read.
// An unread listing is unread, never empty -- the whole fleet's rule.
func MissingSecrets(referenced, repoSecrets, orgSecrets []string) []string {
	have := map[string]bool{}
	for _, s := range repoSecrets {
		have[s] = true
	}
	for _, s := range orgSecrets {
		have[s] = true
	}
	var missing []string
	for _, name := range referenced {
		if !have[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SecretsSummary renders the finding for a report line.
func SecretsSummary(missing []string) string {
	return strings.Join(missing, ", ")
}
