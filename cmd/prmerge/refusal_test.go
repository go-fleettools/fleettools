package main

import (
	"errors"
	"testing"
)

func TestRefusalKindSeparatesCausesThatNeedDifferentAnswers(t *testing.T) {
	cases := []struct{ msg, want string }{
		{"GraphQL: refusing to allow an OAuth App to create or update workflow `.github/workflows/docs.yml` without `workflow` scope (mergePullRequest)", "workflow-scope"},
		{"GraphQL: Pull Request has merge conflicts (mergePullRequest)", "conflict"},
		{"GraphQL: Base branch was modified. Review and try the merge again.", "base-moved"},
		{"To have the pull request merged after all the requirements have been met, add the `--auto` flag.", "requirements-unmet"},
		{"something nobody has seen before", "other"},
	}
	for _, c := range cases {
		// A summary that counts these together is what let a conflict read as
		// a scope refusal twice in one day.
		if got := refusalKind(errors.New(c.msg)); got != c.want {
			t.Errorf("refusalKind(%.44q…) = %q, want %q", c.msg, got, c.want)
		}
	}
}
