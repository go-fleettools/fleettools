package main

import (
	"strings"
	"testing"
	"time"
)

func srvWith(status, flavor string) string {
	return `{"servers":[{"id":"s1","name":"vm","status":"` + status + `","flavor":{"id":"` + flavor + `"}}]}`
}

func init() { resizeWait = 3 * time.Second }

var resizeArgs = []string{cloudArg, "resize", "--server=vm", "--flavor=os.6"}

func actions(f *fakeCloud) []string {
	var a []string
	for _, c := range f.created {
		if strings.Contains(c, "/action ") {
			a = append(a, c)
		}
	}
	return a
}

func TestResizeStopsAtVerifyWithoutConfirm(t *testing.T) {
	f := newFake(t)
	f.serversBody = srvWith("ACTIVE", "f1")
	out, err := run(resizeArgs...)
	if err != nil {
		t.Fatal(err)
	}
	a := actions(f)
	if len(a) != 1 || !strings.Contains(a[0], `"flavorRef":"f2"`) || strings.Contains(a[0], "confirmResize") {
		t.Fatalf("actions = %v", a)
	}
	for _, want := range []string{"plan: server vm id=s1 status=ACTIVE flavor=f1 -> os.6 vcpus=6 ram=12268 disk=20", "resized: vm awaits confirmation"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "confirmed") {
		t.Errorf("confirmed without --confirm:\n%s", out)
	}
}

func TestResizeConfirm(t *testing.T) {
	f := newFake(t)
	f.serversBody = srvWith("ACTIVE", "f1")
	out, err := run(append(resizeArgs, "--confirm")...)
	if err != nil {
		t.Fatal(err)
	}
	a := actions(f)
	if len(a) != 2 || !strings.Contains(a[0], `"resize"`) || !strings.Contains(a[1], "confirmResize") {
		t.Fatalf("actions = %v", a)
	}
	if !strings.Contains(out, "confirmed: vm is ACTIVE") {
		t.Errorf("out:\n%s", out)
	}
}

func TestResizeConfirmsAServerAlreadyAwaitingIt(t *testing.T) {
	f := newFake(t)
	f.serversBody = srvWith("VERIFY_RESIZE", "f2")
	f.srvStatus = "VERIFY_RESIZE"
	out, err := run(append(resizeArgs, "--confirm")...)
	a := actions(f)
	if err != nil || len(a) != 1 || !strings.Contains(a[0], "confirmResize") || !strings.Contains(out, "confirmed") {
		t.Fatalf("%v %v\n%s", err, a, out)
	}
	f = newFake(t)
	f.serversBody = srvWith("VERIFY_RESIZE", "f2")
	if _, err := run(resizeArgs...); err != nil || len(actions(f)) != 0 {
		t.Errorf("no --confirm must change nothing: %v %v", err, actions(f))
	}
}

func TestResizeIsIdempotentAndDryRunChangesNothing(t *testing.T) {
	f := newFake(t)
	f.serversBody = srvWith("ACTIVE", "f2")
	out, err := run(append(resizeArgs, "--confirm")...)
	if err != nil || len(actions(f)) != 0 || !strings.Contains(out, "already has flavor os.6") {
		t.Fatalf("%v %v\n%s", err, actions(f), out)
	}
	f = newFake(t)
	f.serversBody = srvWith("ACTIVE", "f1")
	out, err = run(append(resizeArgs, "--dry-run")...)
	if err != nil || len(actions(f)) != 0 || !strings.Contains(out, "plan:") || strings.Contains(out, "resized") {
		t.Fatalf("dry run: %v %v\n%s", err, actions(f), out)
	}
}

func TestResizeRefusals(t *testing.T) {
	cases := []struct {
		name string
		args []string
		mod  func(*fakeCloud)
		want string
	}{
		{"no cloud", []string{"resize", "--server=vm", "--flavor=os.6"}, nil, "--cloud is required"},
		{"no server", []string{cloudArg, "resize", "--flavor=os.6"}, nil, "--server and --flavor"},
		{"no flavor", []string{cloudArg, "resize", "--server=vm"}, nil, "--server and --flavor"},
		{"unknown server", []string{cloudArg, "resize", "--server=nope", "--flavor=os.6"}, nil, "want exactly one, got 0"},
		{"unknown flavor", []string{cloudArg, "resize", "--server=vm", "--flavor=zz"}, nil, `flavor "zz" not found`},
		{"building", resizeArgs, func(f *fakeCloud) { f.serversBody = srvWith("BUILD", "f1") }, "only an ACTIVE or SHUTOFF"},
		{"list failure", resizeArgs, func(f *fakeCloud) { f.fail["GET /compute/v2.1/servers/detail"] = 0 }, "500"},
		{"flavor list failure", resizeArgs, func(f *fakeCloud) { f.fail["GET /compute/v2.1/flavors/detail"] = 0 }, "500"},
		{"resize failure", resizeArgs, func(f *fakeCloud) { f.fail["POST /compute/v2.1/servers/s1/action"] = 0 }, "resize vm"},
		{"auth", resizeArgs, func(f *fakeCloud) { f.denyAuth = true }, "authenticate"},
		{"wait failure", resizeArgs, func(f *fakeCloud) { f.fail["GET /compute/v2.1/servers/s1"] = 0 }, "wait for VERIFY_RESIZE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFake(t)
			f.serversBody = srvWith("ACTIVE", "f1")
			if c.mod != nil {
				c.mod(f)
			}
			_, err := run(c.args...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestResizeConfirmFailureIsReported(t *testing.T) {
	f := newFake(t)
	f.serversBody = srvWith("VERIFY_RESIZE", "f2")
	f.fail["POST /compute/v2.1/servers/s1/action"] = 0
	out, err := run(append(resizeArgs, "--confirm")...)
	if err == nil || !strings.Contains(err.Error(), "confirm resize of vm") || strings.Contains(out, "confirmed") {
		t.Fatalf("%v\n%s", err, out)
	}
	f = newFake(t)
	f.serversBody = srvWith("VERIFY_RESIZE", "f2")
	f.fail["GET /compute/v2.1/servers/s1"] = 0
	if _, err := run(append(resizeArgs, "--confirm")...); err == nil || !strings.Contains(err.Error(), "wait for ACTIVE") {
		t.Errorf("wait failure: %v", err)
	}
}
