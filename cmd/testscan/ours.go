package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// ⛔ A CHECKOUT ON DISK IS NOT NECESSARILY ONE OF OUR REPOSITORIES, and this
// reads the disk. Of nineteen findings on 2026-09-27, SIX were noise:
//
//	tannevaled/tamago-go   fork of usbarmory/tamago-go
//	tannevaled/hcl         fork of hashicorp/hcl
//	tannevaled/ldap        fork of glauth/ldap
//	tannevaled/tamago      fork of usbarmory/tamago
//	tannevaled/x-sys       not on GitHub at all — a local-only checkout
//	usbarmory/tamago       upstream's own project, in upstream's own account
//
// Telling somebody that hashicorp/hcl has no CI is worse than saying nothing:
// it is not ours to fix, and a third of a list that is not actionable is a
// list that stops being read. redscan does not have this problem because it
// starts from the API, which says `fork` and lists only our organisations.
//
// The confirmation costs ONE call per FINDING, not per repository — nineteen,
// not nine hundred and twenty-five. The scan itself still reads nothing but
// the working tree.
//
// ⛔ It fails OPEN. With no gh, no network or no token, every finding is kept:
// an unanswered question must not delete a real one, which is the direction
// that costs something.
type ownership struct {
	fork    bool
	missing bool // GitHub does not have it
	ok      bool // the question was answered at all
}

func askGitHub(repo string) ownership {
	cmd := exec.Command("gh", "api", "repos/"+repo, "--jq", `{f: .fork, o: .owner.login}`)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		// ⛔ A 404 IS AN ANSWER; EVERY OTHER FAILURE IS NOT. gh exits 1 for a
		// rate limit and for a missing token as readily as for a missing
		// repository, so keying on the exit status would delete real findings
		// during exactly the minutes when the API is refusing — which is when
		// a sweep is most likely to be running. The message has to say so.
		if msg := errb.String(); strings.Contains(msg, "Not Found") || strings.Contains(msg, "HTTP 404") {
			return ownership{missing: true, ok: true}
		}
		return ownership{}
	}
	var got struct {
		F bool   `json:"f"`
		O string `json:"o"`
	}
	if json.Unmarshal(out, &got) != nil {
		return ownership{}
	}
	return ownership{fork: got.F, ok: true}
}

// whyNotOurs names the reason a finding does not belong on the list, or "".
func whyNotOurs(o ownership) string {
	switch {
	case !o.ok:
		return ""
	case o.missing:
		return "not on GitHub"
	case o.fork:
		return "a fork"
	}
	return ""
}

// ourOwners is every account whose repositories are ours: the organisations
// this token belongs to, AND the user themself. A checkout under somebody
// else's account — upstream's own project, cloned for reference — is not ours
// to have an opinion about.
//
// ⛔ The user is in this set deliberately. Leaving them out drops every
// personal repository, and on this machine that is where several real modules
// live; the four findings under that account which ARE noise are forks, and
// the fork test catches them on its own.
func ourOwners() (map[string]bool, bool) {
	set := map[string]bool{}
	if out, err := exec.Command("gh", "api", "user/orgs", "--paginate", "--jq", ".[].login").Output(); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				set[l] = true
			}
		}
	}
	if out, err := exec.Command("gh", "api", "user", "--jq", ".login").Output(); err == nil {
		if l := strings.TrimSpace(string(out)); l != "" {
			set[l] = true
		}
	}
	return set, len(set) > 0
}

func ownerOf(repo string) string {
	o, _, _ := strings.Cut(repo, "/")
	return o
}

// sift drops the findings that are not ours and says which and why.
//
// ask is a parameter so this can be tested without a network: the first
// version of the test reached the real GitHub, got a real 404 for its made-up
// repository, and failed for a reason that had nothing to do with the logic.
func sift(found []finding, owners map[string]bool, haveOwners bool, ask func(string) ownership) (kept []finding, dropped []string) {
	for _, f := range found {
		reason := ""
		if haveOwners && !owners[ownerOf(f.repo)] {
			reason = "not one of our accounts"
		} else if r := whyNotOurs(ask(f.repo)); r != "" {
			reason = r
		}
		if reason == "" {
			kept = append(kept, f)
			continue
		}
		dropped = append(dropped, fmt.Sprintf("%s (%s)", f.repo, reason))
	}
	return kept, dropped
}
