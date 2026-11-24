package main

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	targetNoRules = `{"security_groups":[{"id":"g1","name":"garm-ci","security_group_rules":[]}]}`
	sourceGroup   = `{"security_groups":[{"id":"g9","name":"garm-runner","security_group_rules":[{"id":"e1","direction":"egress","protocol":"","port_range_min":0,"port_range_max":0}]}]}`
)

var fromGroupArgs = []string{cloudArg, "allow", "--group=garm-ci", "--from-group=garm-runner", "--port=9997"}

func twoGroups(f *fakeCloud, target string) {
	f.groupsByName = map[string]string{"garm-ci": target, "garm-runner": sourceGroup}
}

func TestAllowFromGroupAddsARemoteGroupRuleForBothEtherTypes(t *testing.T) {
	f := newFake(t)
	twoGroups(f, targetNoRules)
	out, err := run(fromGroupArgs...)
	if err != nil {
		t.Fatal(err)
	}
	rs := ruleBodies(f)
	if len(rs) != 2 {
		t.Fatalf("rules = %v", rs)
	}
	for i, et := range []string{"IPv4", "IPv6"} {
		for _, want := range []string{`"remote_group_id":"g9"`, `"ethertype":"` + et + `"`, `"port_range_min":9997`, `"port_range_max":9997`, `"direction":"ingress"`, `"protocol":"tcp"`, `"security_group_id":"g1"`} {
			if !strings.Contains(rs[i], want) {
				t.Errorf("rule %d lacks %s: %s", i, want, rs[i])
			}
		}
		if strings.Contains(rs[i], "remote_ip_prefix") {
			t.Errorf("rule %d also names a prefix: %s", i, rs[i])
		}
		if !strings.Contains(out, "rule added: tcp/9997 from group garm-runner ("+et+") in garm-ci") {
			t.Errorf("no report for %s:\n%s", et, out)
		}
	}
}

func TestAllowFromGroupIsIdempotentPerEtherTypeAndPort(t *testing.T) {
	f := newFake(t)
	rule := func(id, et, proto, group string, port int) string {
		p := strconv.Itoa(port)
		return `{"id":"` + id + `","direction":"ingress","ethertype":"` + et + `","protocol":"` + proto + `","remote_group_id":"` + group + `","port_range_min":` + p + `,"port_range_max":` + p + `}`
	}
	// The IPv4 rule is there; a udp, another-group and another-port rule do not count.
	twoGroups(f, `{"security_groups":[{"id":"g1","name":"garm-ci","security_group_rules":[`+
		rule("v4", "IPv4", "tcp", "g9", 9997)+`,`+rule("u", "IPv6", "udp", "g9", 9997)+`,`+rule("o", "IPv6", "tcp", "gX", 9997)+`,`+rule("p", "IPv6", "tcp", "g9", 9998)+`]}]}`)
	out, err := run(fromGroupArgs...)
	if err != nil {
		t.Fatal(err)
	}
	rs := ruleBodies(f)
	if len(rs) != 1 || !strings.Contains(rs[0], `"ethertype":"IPv6"`) {
		t.Fatalf("rules = %v", rs)
	}
	if !strings.Contains(out, "rule exists: tcp/9997 from group garm-runner (IPv4) in garm-ci id=v4") {
		t.Errorf("out:\n%s", out)
	}
}

func TestAllowFromGroupRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"with --from":         {[]string{cloudArg, "allow", "--group=g", "--from-group=s", "--from=1.2.3.4/32", "--port=1"}, "exclusive with --from"},
		"with --json-url":     {[]string{cloudArg, "allow", "--group=g", "--from-group=s", "--from-json-url=https://x", "--json-key=k", "--port=1"}, "exclusive with --from"},
		"with --world-acme":   {[]string{cloudArg, "allow", "--group=g", "--from-group=s", "--world-acme", "--port=80"}, "exclusive with --from-group"},
		"without a port":      {[]string{cloudArg, "allow", "--group=g", "--from-group=s"}, "at least one --port"},
		"a bad protocol":      {[]string{cloudArg, "allow", "--group=g", "--from-group=s", "--port=1", "--proto=sctp"}, "tcp or udp"},
		"a port out of range": {[]string{cloudArg, "allow", "--group=g", "--from-group=s", "--port=0"}, "out of range"},
	} {
		f := newFake(t)
		if _, err := run(tc.args...); err == nil || !strings.Contains(err.Error(), tc.want) || len(f.calls) != 0 {
			t.Errorf("%s: %v (calls %v)", name, err, f.calls)
		}
	}
	f := newFake(t)
	f.groupsByName = map[string]string{"garm-ci": targetNoRules}
	if _, err := run(fromGroupArgs...); err == nil || !strings.Contains(err.Error(), `"garm-runner": want exactly one, got 0`) || len(ruleBodies(f)) != 0 {
		t.Errorf("an absent source group: %v %v", err, ruleBodies(f))
	}
	f = newFake(t)
	twoGroups(f, targetNoRules)
	f.fail["POST /network/v2.0/security-group-rules"] = 0
	if _, err := run(fromGroupArgs...); err == nil || !strings.Contains(err.Error(), "from group garm-runner") {
		t.Errorf("a rule creation failure: %v", err)
	}
}

func TestGroupCreatesAnEgressOnlyGroupOnce(t *testing.T) {
	f := newFake(t)
	f.groupsByName = map[string]string{"x": `{"security_groups":[]}`}
	out, err := run(cloudArg, "group", "--name=garm-runner")
	if err != nil || !strings.Contains(out, "group created: vm-ssh id=g2") {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(f.created) != 1 || !strings.Contains(f.created[0], `"name":"garm-runner"`) || len(ruleBodies(f)) != 0 {
		t.Errorf("created = %v", f.created)
	}
	// Present and egress-only: nothing is created.
	f = newFake(t)
	twoGroups(f, targetNoRules)
	out, err = run(cloudArg, "group", "--name=garm-runner")
	if err != nil || !strings.Contains(out, "group exists: garm-runner id=g9") || len(f.created) != 0 {
		t.Errorf("not idempotent: %v %v\n%s", err, f.created, out)
	}
}

func TestGroupRefusals(t *testing.T) {
	f := newFake(t)
	f.groupsByName = map[string]string{"open": `{"security_groups":[{"id":"g3","name":"open","security_group_rules":[{"id":"i1","direction":"ingress","protocol":"tcp"}]}]}`,
		"twice": `{"security_groups":[{"id":"a","name":"twice"},{"id":"b","name":"twice"}]}`}
	if _, err := run(cloudArg, "group", "--name=open"); err == nil || !strings.Contains(err.Error(), "not egress-only") || len(f.created) != 0 {
		t.Errorf("a group with ingress: %v %v", err, f.created)
	}
	if _, err := run(cloudArg, "group", "--name=twice"); err == nil || !strings.Contains(err.Error(), "at most one, got 2") || len(f.created) != 0 {
		t.Errorf("a name carried twice: %v %v", err, f.created)
	}
	if _, err := run(cloudArg, "group"); err == nil || !strings.Contains(err.Error(), "--name is required") {
		t.Errorf("no name: %v", err)
	}
	if _, err := run("group", "--name=x"); err == nil || !strings.Contains(err.Error(), "--cloud is required") {
		t.Errorf("no cloud: %v", err)
	}
	f = newFake(t)
	f.groupsByName = map[string]string{"x": `{"security_groups":[]}`}
	f.fail["POST /network/v2.0/security-groups"] = 0
	if _, err := run(cloudArg, "group", "--name=x"); err == nil || !strings.Contains(err.Error(), "create security group") {
		t.Errorf("creation failure: %v", err)
	}
	f = newFake(t)
	f.denyAuth = true
	if _, err := run(cloudArg, "group", "--name=x"); err == nil || !strings.Contains(err.Error(), "authenticate") || len(f.created) != 0 {
		t.Errorf("auth failure: %v %v", err, f.created)
	}
	f = newFake(t)
	f.fail["GET /network/v2.0/security-groups"] = 0
	if _, err := run(cloudArg, "group", "--name=x"); err == nil || !strings.Contains(err.Error(), "security group") {
		t.Errorf("listing failure: %v", err)
	}
}

// --- image-build

func fastPolls(t *testing.T) {
	t.Helper()
	old := pollEvery
	pollEvery = 5 * time.Millisecond
	t.Cleanup(func() { pollEvery = old })
}

var buildArgs = []string{cloudArg, "image-build", "--name=snap", "--network=private-network", "--security-group=default"}

func builtFake(t *testing.T) *fakeCloud {
	t.Helper()
	fastPolls(t)
	f := newFake(t)
	f.srvStatus = "SHUTOFF"
	f.console = "boot...\n" + buildMarker + "\npowering off\n"
	return f
}

func has(f *fakeCloud, prefix string) string {
	for _, c := range f.created {
		if strings.HasPrefix(c, prefix) {
			return c
		}
	}
	return ""
}

func TestImageBuildSnapshotsAVerifiedBuilderAndDeletesIt(t *testing.T) {
	f := builtFake(t)
	out, err := run(buildArgs...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	srv := has(f, "POST /compute/v2.1/servers ")
	var body struct {
		Server struct {
			Name     string                  `json:"name"`
			UserData string                  `json:"user_data"`
			Flavor   string                  `json:"flavorRef"`
			Image    string                  `json:"imageRef"`
			SGs      []struct{ Name string } `json:"security_groups"`
		} `json:"server"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(srv, "POST /compute/v2.1/servers ")), &body); err != nil {
		t.Fatalf("%v: %s", err, srv)
	}
	ud, err := base64.StdEncoding.DecodeString(body.Server.UserData)
	if err != nil {
		t.Fatal(err)
	}
	if body.Server.Name != "snap-builder" || body.Server.Flavor != "f2" || body.Server.Image != "i1" ||
		len(body.Server.SGs) != 1 || body.Server.SGs[0].Name != "default" || string(ud) != buildUserData(defaultPackages) {
		t.Errorf("builder = %+v\nuser data:\n%s", body.Server, ud)
	}
	img := has(f, "POST /compute/v2.1/servers/s1/action ")
	if !strings.Contains(img, `"createImage"`) || !strings.Contains(img, `"name":"snap"`) {
		t.Errorf("snapshot request = %q", img)
	}
	if has(f, "DELETE /compute/v2.1/servers/s1") == "" {
		t.Error("the builder was not deleted")
	}
	for _, want := range []string{"image active: snap id=img1", "builder deleted: s1", "builder verified"} {
		if !strings.Contains(out, want) {
			t.Errorf("out lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "builder deleted") != 1 {
		t.Errorf("deleted twice:\n%s", out)
	}
}

func TestImageBuildWithoutTheMarkerTakesNoImage(t *testing.T) {
	f := builtFake(t)
	f.console = "boot...\npowering off\n"
	_, err := run(append(buildArgs, "--timeout=3s")...)
	if err == nil || !strings.Contains(err.Error(), buildMarker) {
		t.Fatalf("err = %v", err)
	}
	if has(f, "POST /compute/v2.1/servers/s1/action ") != "" {
		t.Error("an image was taken from an unverified builder")
	}
	if has(f, "DELETE /compute/v2.1/servers/s1") == "" {
		t.Error("the failed builder was left running")
	}
}

func TestImageBuildFailuresDeleteTheBuilder(t *testing.T) {
	for name, tc := range map[string]struct {
		prep func(*fakeCloud)
		want string
	}{
		"console unreadable":      {func(f *fakeCloud) { f.fail["POST /compute/v2.1/servers/s1/action"] = 0 }, "console read: "},
		"snapshot refused":        {func(f *fakeCloud) { f.fail["POST /compute/v2.1/servers/s1/action"] = 1 }, "snapshot:"},
		"snapshot killed":         {func(f *fakeCloud) { f.imgStates = []string{"queued", "killed"} }, "ended killed"},
		"snapshot status fails":   {func(f *fakeCloud) { f.fail["GET /image/v2/images/img1"] = 0 }, "image status"},
		"status unreadable":       {func(f *fakeCloud) { f.fail["GET /compute/v2.1/servers/s1"] = 0; f.console = "booting\n" }, "builder status"},
		"status lost at poweroff": {func(f *fakeCloud) { f.fail["GET /compute/v2.1/servers/s1"] = 1 }, "builder status"},
		"builder in error":        {func(f *fakeCloud) { f.srvStatus = "ERROR"; f.console = "booting\n" }, "ended in status ERROR"},
		"off without marker":      {func(f *fakeCloud) { f.console = "booting\n" }, "powered off without " + buildMarker},
	} {
		f := builtFake(t)
		tc.prep(f)
		out, err := run(append(buildArgs, "--timeout=3s")...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v\n%s", name, err, out)
		}
		if has(f, "DELETE /compute/v2.1/servers/s1") == "" {
			t.Errorf("%s: the builder was left running", name)
		}
	}
}

func TestImageBuildTimesOutAndStillDeletes(t *testing.T) {
	f := builtFake(t)
	f.srvStatus = "ACTIVE" // never powers off
	f.console = "booting...\n"
	out, err := run(append(buildArgs, "--timeout=1500ms")...)
	if err == nil || !strings.Contains(err.Error(), "yet (status ACTIVE") {
		t.Fatalf("err = %v", err)
	}
	if has(f, "DELETE /compute/v2.1/servers/s1") == "" || !strings.Contains(out, "builder deleted: s1") {
		t.Errorf("the timed-out builder was left running:\n%s", out)
	}
}

// Nova serves no console once the guest is off: the marker must be read while
// the builder still runs, before the wait for the power-off.
func TestImageBuildReadsTheMarkerWhileTheBuilderStillRuns(t *testing.T) {
	f := builtFake(t)
	f.srvStatus = "ACTIVE" // marker on the console, never powers off
	out, err := run(append(buildArgs, "--timeout=1500ms")...)
	if err == nil || !strings.Contains(err.Error(), "never powered off") || !strings.Contains(out, "builder verified") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if has(f, "POST /compute/v2.1/servers/s1/action ") != "" {
		t.Error("an image was taken from a builder that never powered off")
	}
}

func TestImageBuildReportsADeletionThatFails(t *testing.T) {
	f := builtFake(t)
	f.fail["DELETE /compute/v2.1/servers/s1"] = 0
	_, err := run(buildArgs...)
	if err == nil || !strings.Contains(err.Error(), "delete builder s1") {
		t.Errorf("a failed deletion after the snapshot: %v", err)
	}
	f = builtFake(t)
	f.console = ""
	f.fail["DELETE /compute/v2.1/servers/s1"] = 0
	out, err := run(append(buildArgs, "--timeout=3s")...)
	if err == nil || !strings.Contains(out, "WARNING: builder s1 was NOT deleted") {
		t.Errorf("a failed cleanup must be loud: %v\n%s", err, out)
	}
}

func TestImageBuildDryRunPrintsTheCloudConfigAndCreatesNothing(t *testing.T) {
	f := builtFake(t)
	out, err := run(append(buildArgs, "--dry-run")...)
	if err != nil || len(f.created) != 0 {
		t.Fatalf("%v %v", err, f.created)
	}
	for _, want := range []string{"plan: base=debian-13-genericcloud flavor=os.6 network=private-network group=default image=snap", "#cloud-config", "  - qemu-system-x86", `KERNEL=="kvm"`, "poweroff"} {
		if !strings.Contains(out, want) {
			t.Errorf("out lacks %q:\n%s", want, out)
		}
	}
}

func TestImageBuildDefaultNameIsTheUTCTimeAndAnExistingNameIsRefused(t *testing.T) {
	f := builtFake(t)
	old := now
	now = func() time.Time { return time.Date(2026, 11, 24, 23, 59, 0, 0, time.FixedZone("x", 3600)) }
	t.Cleanup(func() { now = old })
	out, err := run(cloudArg, "image-build", "--network=private-network", "--security-group=default", "--dry-run")
	if err != nil || !strings.Contains(out, "image=garm-runner-20261124-2259") {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := run(cloudArg, "image-build", "--name=debian-13-genericcloud", "--network=private-network", "--security-group=default"); err == nil || !strings.Contains(err.Error(), "already exists") || len(f.created) != 0 {
		t.Errorf("an existing name: %v %v", err, f.created)
	}
}

func TestImageBuildValidationRefusesBeforeAnyCall(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no network":   {[]string{cloudArg, "image-build", "--security-group=g"}, "--network and --security-group"},
		"no group":     {[]string{cloudArg, "image-build", "--network=n"}, "--network and --security-group"},
		"bad package":  {[]string{cloudArg, "image-build", "--network=n", "--security-group=g", "--packages=ok,a;b"}, `"a;b" is not a Debian package name`},
		"upper case":   {[]string{cloudArg, "image-build", "--network=n", "--security-group=g", "--packages=Foo"}, `"Foo"`},
		"leading dash": {[]string{cloudArg, "image-build", "--network=n", "--security-group=g", "--packages=-x"}, `"-x"`},
		"no packages":  {[]string{cloudArg, "image-build", "--network=n", "--security-group=g", "--packages="}, "at least one package"},
		"no timeout":   {[]string{cloudArg, "image-build", "--network=n", "--security-group=g", "--timeout=0s"}, "--timeout must be positive"},
		"no cloud":     {[]string{"image-build", "--network=n", "--security-group=g"}, "--cloud is required"},
	} {
		f := newFake(t)
		args := tc.args
		if name != "no timeout" {
			// bounded: a guard that stops refusing must fail here, not hang for 25 minutes
			args = append(args[:len(args):len(args)], "--timeout=2s")
		}
		if _, err := run(args...); err == nil || !strings.Contains(err.Error(), tc.want) || len(f.calls) != 0 {
			t.Errorf("%s: %v (calls %v)", name, err, f.calls)
		}
	}
}

func TestImageBuildResolutionRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		prep func(*fakeCloud)
		args []string
		want string
	}{
		"network": {func(f *fakeCloud) { f.nets = `{"networks":[]}` }, buildArgs, `network "private-network"`},
		"group":   {func(f *fakeCloud) { f.groupsByName = map[string]string{"x": "{}"} }, buildArgs, `security group "default"`},
		"flavor":  {func(f *fakeCloud) {}, append(buildArgs, "--flavor=nope"), `flavor "nope" not found`},
		"image":   {func(f *fakeCloud) {}, append(buildArgs, "--base-image-match=nope"), `no active image matches "nope"`},
		"flavors": {func(f *fakeCloud) { f.fail["GET /compute/v2.1/flavors/detail"] = 0 }, buildArgs, "boom"},
		"images":  {func(f *fakeCloud) { f.fail["GET /image/v2/images"] = 0 }, buildArgs, "boom"},
		"server":  {func(f *fakeCloud) { f.fail["POST /compute/v2.1/servers"] = 0 }, buildArgs, "builder:"},
		"auth":    {func(f *fakeCloud) { f.denyAuth = true }, buildArgs, "authenticate"},
	} {
		f := builtFake(t)
		tc.prep(f)
		if _, err := run(tc.args...); err == nil || !strings.Contains(err.Error(), tc.want) || has(f, "POST /compute/v2.1/servers/s1/action ") != "" {
			t.Errorf("%s: %v", name, err)
		}
	}
}
