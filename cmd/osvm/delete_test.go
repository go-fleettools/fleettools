package main

import (
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	twoServers = `{"servers":[` +
		`{"id":"s1","name":"vm","status":"ACTIVE","security_groups":[{"name":"default"},{"name":"sg-a"}]},` +
		`{"id":"s2","name":"other","status":"ACTIVE","security_groups":[{"name":"sg-a"}]}]}`
	ownGroups = `{"servers":[{"id":"s1","name":"vm","status":"ACTIVE","security_groups":[{"name":"default"},{"name":"sg-a"},{"name":"sg-b"}]}]}`
	oneVolume = `{"volumeAttachments":[{"id":"v1","volumeId":"v1","serverId":"s1"}]}`
	twoVols   = `{"volumeAttachments":[{"id":"v1","volumeId":"v1","serverId":"s1"},{"id":"v2","volumeId":"v2","serverId":"s1"}]}`
	oneFIP    = `{"floatingips":[{"id":"fip1","floating_ip_address":"203.0.113.9"}]}`
)

var deleteArgs = []string{cloudArg, "delete", "--id=s1", "--name=vm"}

// withArgs bounds every wait to one second, so a guard whose removal lets a run
// reach a wait that cannot end fails in a second, not in ten minutes; a later
// --timeout in extra overrides it.
func withArgs(extra ...string) []string {
	return append(append(append([]string{}, deleteArgs...), "--timeout=1s"), extra...)
}

// deletes lists the DELETE calls the fake recorded, in order.
func (f *fakeCloud) deletes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var d []string
	for _, c := range f.created {
		if strings.HasPrefix(c, "DELETE ") {
			d = append(d, strings.TrimSpace(c))
		}
	}
	return d
}

func fastDelete(t *testing.T) {
	t.Helper()
	old := deletePoll
	deletePoll = time.Millisecond
	t.Cleanup(func() { deletePoll = old })
}

func TestDeleteDryRunDeletesNothing(t *testing.T) {
	f := newFake(t)
	f.attachments, f.fips = oneVolume, oneFIP
	f.serversBody = ownGroups
	f.groupsByName = map[string]string{"sg-a": `{"security_groups":[{"id":"g1","name":"sg-a"}]}`, "sg-b": `{"security_groups":[{"id":"g2","name":"sg-b"}]}`}
	out, err := run(withArgs("--with-volumes", "--with-group")...)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"plan: delete server vm id=s1", "plan: delete volume vm-data id=v1 size=200GB",
		"plan: delete floating IP 203.0.113.9 id=fip1", "plan: delete security group sg-a id=g1", "plan: delete security group sg-b id=g2",
		"dry run: nothing deleted"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "security group default") {
		t.Errorf("the default group is planned:\n%s", out)
	}
	if d := f.deletes(); len(d) != 0 {
		t.Fatalf("a dry run deleted %v", d)
	}
	if strings.Contains(out, "not-a-real-password") {
		t.Fatal("a credential reached the output")
	}
}

func TestDeleteRefusals(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no cloud", []string{"delete", "--id=s1", "--name=vm", "--confirm"}, "--cloud is required"},
		{"no id", []string{cloudArg, "delete", "--name=vm", "--confirm"}, "--id and --name are required"},
		{"no name", []string{cloudArg, "delete", "--id=s1", "--confirm"}, "--id and --name are required"},
		{"wildcard name", []string{cloudArg, "delete", "--id=s1", "--name=*", "--confirm"}, "no wildcard"},
		{"wildcard id", []string{cloudArg, "delete", "--id=s*", "--name=vm", "--confirm"}, "no wildcard"},
		{"name mismatch", []string{cloudArg, "delete", "--id=s1", "--name=other", "--confirm"}, `is named "vm", not "other"`},
		{"unknown id", []string{cloudArg, "delete", "--id=nope", "--name=vm", "--confirm"}, `no server has id "nope"`},
		{"bad timeout", []string{cloudArg, "delete", "--id=s1", "--name=vm", "--timeout=0s", "--confirm"}, "--timeout must be positive"},
		{"positional", []string{cloudArg, "delete", "all"}, "unknown command"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFake(t)
			args := slices.Clone(c.args)
			if i := slices.Index(args, "delete"); i >= 0 {
				args = slices.Insert(args, i+1, "--timeout=200ms")
			}
			_, err := run(args...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if d := f.deletes(); len(d) != 0 {
				t.Fatalf("a refused delete still deleted %v", d)
			}
		})
	}
}

func TestDeleteRefusesAGroupAnotherServerCarries(t *testing.T) {
	f := newFake(t)
	f.serversBody = twoServers
	f.groupsByName = map[string]string{"sg-a": `{"security_groups":[{"id":"g1","name":"sg-a"}]}`}
	_, err := run(withArgs("--with-group", "--confirm")...)
	if err == nil || !strings.Contains(err.Error(), `"sg-a" is also carried by server other`) {
		t.Fatalf("err = %v", err)
	}
	if d := f.deletes(); len(d) != 0 {
		t.Fatalf("deleted %v despite the refusal", d)
	}
}

func TestDeleteWithoutWithGroupLeavesTheSharedGroupAlone(t *testing.T) {
	f := newFake(t)
	f.track = true
	f.serversBody = twoServers
	if _, err := run(withArgs("--confirm")...); err != nil {
		t.Fatal(err)
	}
	if d := f.deletes(); len(d) != 1 || d[0] != "DELETE /compute/v2.1/servers/s1" {
		t.Fatalf("deletes = %v", d)
	}
}

func TestDeleteConfirmRemovesEverythingInOrder(t *testing.T) {
	fastDelete(t)
	f := newFake(t)
	f.track = true
	f.attachments, f.fips = oneVolume, oneFIP
	f.serversBody = ownGroups
	f.groupsByName = map[string]string{"sg-a": `{"security_groups":[{"id":"g1","name":"sg-a"}]}`, "sg-b": `{"security_groups":[{"id":"g2","name":"sg-b"}]}`}
	out, err := run(withArgs("--with-volumes", "--with-group", "--confirm")...)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"DELETE /network/v2.0/floatingips/fip1", "DELETE /compute/v2.1/servers/s1", "DELETE /volume/v3/p1/volumes/v1",
		"DELETE /network/v2.0/security-groups/g1", "DELETE /network/v2.0/security-groups/g2"}
	got := f.deletes()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("deletes = %v\nwant %v", got, want)
	}
	for _, s := range []string{"server deleted: vm id=s1", "volume deleted: vm-data", "security group deleted: sg-b", "floating IP deleted: 203.0.113.9"} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q in:\n%s", s, out)
		}
	}
}

func TestDeleteKeepsVolumesUnlessAskedAndOnlyTheDataOne(t *testing.T) {
	fastDelete(t)
	f := newFake(t)
	f.track = true
	f.attachments = twoVols
	f.volNames = map[string]string{"v2": "boot-disk"}
	out, err := run(withArgs("--confirm")...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "plan: keep volume vm-data id=v1 (--with-volumes not given)") || len(f.deletes()) != 1 {
		t.Fatalf("volumes touched without --with-volumes: %v\n%s", f.deletes(), out)
	}

	f2 := newFake(t)
	f2.track = true
	f2.attachments = twoVols
	f2.volNames = map[string]string{"v2": "boot-disk"}
	out, err = run(withArgs("--with-volumes", "--confirm")...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "plan: keep volume boot-disk id=v2 (not named vm-data)") {
		t.Fatalf("the other volume is not kept:\n%s", out)
	}
	for _, d := range f2.deletes() {
		if strings.Contains(d, "volumes/v2") {
			t.Fatalf("deleted a volume that is not vm-data: %v", f2.deletes())
		}
	}
}

func TestDeleteFailsFastWhenTheServerStays(t *testing.T) {
	fastDelete(t)
	f := newFake(t)
	f.track, f.stuck = true, true
	f.attachments = oneVolume
	f.serversBody = ownGroups
	f.groupsByName = map[string]string{"sg-a": `{"security_groups":[{"id":"g1","name":"sg-a"}]}`, "sg-b": `{"security_groups":[{"id":"g2","name":"sg-b"}]}`}
	start := time.Now()
	_, err := run(withArgs("--with-volumes", "--with-group", "--confirm", "--timeout=150ms")...)
	if err == nil || !strings.Contains(err.Error(), "server vm is still there after 150ms") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the wait was not bounded")
	}
	if d := f.deletes(); len(d) != 1 {
		t.Fatalf("went on past a server that stayed: %v", d)
	}
}

func TestDeleteReportsAServerDeleteFailure(t *testing.T) {
	f := newFake(t)
	f.fail = map[string]int{"DELETE /compute/v2.1/servers/s1": 0}
	f.serversBody = ownGroups
	f.groupsByName = map[string]string{"sg-a": `{"security_groups":[{"id":"g1","name":"sg-a"}]}`, "sg-b": `{"security_groups":[{"id":"g2","name":"sg-b"}]}`}
	_, err := run(withArgs("--with-group", "--confirm")...)
	if err == nil || !strings.Contains(err.Error(), "delete server vm") {
		t.Fatalf("err = %v", err)
	}
	for _, d := range f.deletes() {
		if strings.Contains(d, "security-groups") {
			t.Fatalf("deleted a group after the server delete failed: %v", d)
		}
	}
}

func TestDeleteReportsAGroupDeleteFailure(t *testing.T) {
	fastDelete(t)
	f := newFake(t)
	f.track = true
	f.fail = map[string]int{"DELETE /network/v2.0/security-groups/g1": 0}
	f.serversBody = ownGroups
	f.groupsByName = map[string]string{"sg-a": `{"security_groups":[{"id":"g1","name":"sg-a"}]}`, "sg-b": `{"security_groups":[{"id":"g2","name":"sg-b"}]}`}
	_, err := run(withArgs("--with-group", "--confirm")...)
	if err == nil || !strings.Contains(err.Error(), "delete security group sg-a") {
		t.Fatalf("err = %v", err)
	}
}

func TestDeleteReportsAFloatingIPDeleteFailureBeforeTouchingTheServer(t *testing.T) {
	f := newFake(t)
	f.fips = oneFIP
	f.fail = map[string]int{"DELETE /network/v2.0/floatingips/fip1": 0}
	_, err := run(withArgs("--confirm")...)
	if err == nil || !strings.Contains(err.Error(), "delete floating IP 203.0.113.9") {
		t.Fatalf("err = %v", err)
	}
	for _, d := range f.deletes() {
		if strings.Contains(d, "servers/s1") {
			t.Fatalf("the server was deleted: %v", f.deletes())
		}
	}
}

// TestDeleteStopsOnEveryFailedRead: a read that fails (or a lookup that finds
// nothing) is an error before anything is deleted, never a plan built on a gap.
func TestDeleteStopsOnEveryFailedRead(t *testing.T) {
	grp := map[string]string{"sg-a": `{"security_groups":[]}`}
	cases := []struct {
		name  string
		setup func(f *fakeCloud)
		extra []string
		want  string
	}{
		{"auth", func(f *fakeCloud) { f.denyAuth = true }, nil, "authenticate"},
		{"server list", func(f *fakeCloud) { f.fail["GET /compute/v2.1/servers/detail"] = 0 }, nil, "500"},
		{"attachment list", func(f *fakeCloud) { f.fail["GET /compute/v2.1/servers/s1/os-volume_attachments"] = 0 }, nil, "500"},
		{"volume read", func(f *fakeCloud) { f.attachments = oneVolume; f.fail["GET /volume/v3/p1/volumes/v1"] = 0 }, nil, "volume v1"},
		{"no block storage", func(f *fakeCloud) {
			f.attachments = oneVolume
			f.services = []string{"compute", "network", "image", "identity"}
		}, nil, "no block storage service"},
		{"port list", func(f *fakeCloud) { f.fail["GET /network/v2.0/ports"] = 0 }, nil, "500"},
		{"floating IP list", func(f *fakeCloud) { f.fail["GET /network/v2.0/floatingips"] = 0 }, nil, "500"},
		{"group missing", func(f *fakeCloud) { f.serversBody = ownGroups; f.groupsByName = grp }, []string{"--with-group"}, `security group "sg-a"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFake(t)
			c.setup(f)
			_, err := run(withArgs(append(c.extra, "--confirm")...)...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if d := f.deletes(); len(d) != 0 {
				t.Fatalf("deleted %v after a failed read", d)
			}
		})
	}
}

func TestDeleteWithoutAttachmentsNeedsNoBlockStorage(t *testing.T) {
	f := newFake(t)
	f.services = []string{"compute", "network", "image", "identity"}
	out, err := run(withArgs()...)
	if err != nil || !strings.Contains(out, "dry run") {
		t.Fatalf("err = %v\n%s", err, out)
	}
}

func TestDeleteVolumeFailures(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *fakeCloud)
		want  string
	}{
		{"never available", func(f *fakeCloud) { f.fail["GET /volume/v3/p1/volumes/v1"] = 1 }, "volume v1 did not become available"},
		{"delete refused", func(f *fakeCloud) { f.fail["DELETE /volume/v3/p1/volumes/v1"] = 0 }, "delete volume v1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fastDelete(t)
			f := newFake(t)
			f.track = true
			f.attachments = oneVolume
			f.serversBody = ownGroups
			f.groupsByName = map[string]string{"sg-a": `{"security_groups":[{"id":"g1","name":"sg-a"}]}`, "sg-b": `{"security_groups":[{"id":"g2","name":"sg-b"}]}`}
			c.setup(f)
			_, err := run(withArgs("--with-volumes", "--with-group", "--confirm")...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			for _, d := range f.deletes() {
				if strings.Contains(d, "security-groups") {
					t.Fatalf("a group was deleted after the volume failed: %v", f.deletes())
				}
			}
		})
	}
}

func TestDeleteAnAnswerThatArrivesTooLateIsTheTimeout(t *testing.T) {
	fastDelete(t)
	f := newFake(t)
	f.track = true
	f.slowGet = 400 * time.Millisecond
	_, err := run(withArgs("--confirm", "--timeout=100ms")...)
	if err == nil || !strings.Contains(err.Error(), "server vm is still there after 100ms") {
		t.Fatalf("err = %v", err)
	}
}

func TestDeleteReportsAFailedLookWhileWaiting(t *testing.T) {
	fastDelete(t)
	f := newFake(t)
	f.fail["GET /compute/v2.1/servers/s1"] = 0
	_, err := run(withArgs("--confirm")...)
	if err == nil || !strings.Contains(err.Error(), "waiting for server vm to be gone") {
		t.Fatalf("err = %v", err)
	}
}
