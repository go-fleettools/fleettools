package main

import (
	"errors"
	"strings"
	"testing"
)

const hitSearch = `{"total_count":1,"items":[{"number":9,"title":"windows image chain","state":"closed","repository_url":"https://api.github.com/repos/cloud-boot/windows-image"}]}`

func noPace(t *testing.T) *int {
	t.Helper()
	saved, n := orgPace, 0
	orgPace = func() { n++ }
	t.Cleanup(func() { orgPace = saved })
	return &n
}

// A hit in ONE organisation of three is reported under that organisation, the
// clean ones in a single line, and the status is 1.
func TestAllOrgsReportsPerOrganisation(t *testing.T) {
	stubOrgs(t, []string{"go-diskimages", "cloud-boot", "go-tex"}, nil)
	paced := noPace(t)
	stub(t, func(args []string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "org:cloud-boot") {
			return []byte(hitSearch), nil
		}
		return []byte(emptySearch), nil
	})
	code, out, _ := drive(t, "-all-orgs", "windows image")
	if code != 1 {
		t.Fatalf("status %d, want 1\n%s", code, out)
	}
	for _, want := range []string{"org:cloud-boot", "windows image chain", "2 organisation tracker(s) clean", "go-diskimages, go-tex"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if *paced != 2 {
		t.Errorf("3 searches are spaced by 2 waits, got %d", *paced)
	}
}

// ⛔ An organisation whose search failed is NOT READ: status 2, never clean.
func TestAllOrgsAnUnreadOrganisationHoldsTheGateShut(t *testing.T) {
	stubOrgs(t, []string{"a", "b"}, nil)
	noPace(t)
	stub(t, func(args []string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "org:b") {
			return nil, errors.New("HTTP 403")
		}
		return []byte(emptySearch), nil
	})
	code, out, _ := drive(t, "-all-orgs", "windows image")
	if code != 2 || !strings.Contains(out, "org:b tracker NOT READ") || !strings.Contains(out, "1 organisation tracker(s) clean") {
		t.Errorf("status %d\n%s", code, out)
	}
}

func TestAllOrgsAnUnreadableOrEmptyListIsNotAnAbsence(t *testing.T) {
	for name, tc := range map[string]struct {
		names []string
		err   error
		want  string
	}{
		"listing fails": {nil, errors.New("no network"), "was NOT READ"},
		"no org":        {nil, nil, "NO organisation"},
	} {
		t.Run(name, func(t *testing.T) {
			stubOrgs(t, tc.names, tc.err)
			noPace(t)
			stub(t, answer)
			code, out, _ := drive(t, "-all-orgs", "windows image")
			if code != 2 || !strings.Contains(out, tc.want) {
				t.Errorf("status %d\n%s", code, out)
			}
		})
	}
}

// Clean everywhere is status 0 and counts the organisations read.
func TestAllOrgsCleanEverywhere(t *testing.T) {
	stubOrgs(t, []string{"a", "b"}, nil)
	noPace(t)
	stub(t, answer)
	code, out, _ := drive(t, "-all-orgs", "windows image")
	if code != 0 || !strings.Contains(out, "on 2 surface(s) read") {
		t.Errorf("status %d\n%s", code, out)
	}
}

// -all-orgs alone is a surface; with -repo it adds to it.
func TestAllOrgsCombinesWithRepo(t *testing.T) {
	stubOrgs(t, []string{"a"}, nil)
	noPace(t)
	calls := stub(t, answer)
	code, out, _ := drive(t, "-all-orgs", "-repo", "o/r", "windows image")
	if code != 0 {
		t.Errorf("status %d\n%s", code, out)
	}
	if len(*calls) < 3 {
		t.Errorf("repo tracker, repo branches and the org search expected, got %d calls", len(*calls))
	}
}
