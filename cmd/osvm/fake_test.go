package main

import (
	"bytes"
	"fmt"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeCloud is a minimal OpenStack: keystone v3 plus the compute, network and
// image calls osvm makes. fail maps "METHOD /path" to the number of calls that
// succeed before every later one answers 500; services lists the catalog.
type fakeCloud struct {
	srv      *httptest.Server
	mu       sync.Mutex
	fail     map[string]int
	calls    map[string]int
	services []string
	groups   string
	failNth  map[string]int    // key -> the one call number that fails
	garbled  map[string]string // key -> body that lists fine but does not decode
	denyAuth bool
	nets     string
	flavors  string
	images   string
	created  []string
	// serversBody and portsBody replace the default listings when set.
	serversBody, portsBody string
}

const (
	netsOne     = `{"networks":[{"id":"n1","name":"private-network"}]}`
	flavorsOK   = `{"flavors":[{"id":"f1","name":"vd.1","ram":2048,"vcpus":1,"disk":10},{"id":"f2","name":"os.6","ram":12268,"vcpus":6,"disk":20}]}`
	imagesOK    = `{"images":[{"id":"i1","name":"debian-13-genericcloud","status":"active","created_at":"2026-08-03T00:00:00Z"}]}`
	serverBody  = `{"server":{"id":"s1","name":"vm","status":"ACTIVE","key_name":"k","addresses":{"private-network":[{"addr":"192.168.1.94","OS-EXT-IPS:type":"fixed"}]}}}`
	serversList = `{"servers":[{"id":"s1","name":"vm","status":"ACTIVE","key_name":"k","security_groups":[{"name":"sg-a"},{"name":"sg-b"}],"addresses":{"private-network":[{"addr":"192.168.1.94","OS-EXT-IPS:type":"fixed"}]}}]}`
)

func newFake(t *testing.T) *fakeCloud {
	t.Helper()
	f := &fakeCloud{
		fail: map[string]int{}, calls: map[string]int{}, garbled: map[string]string{}, failNth: map[string]int{},
		services: []string{"compute", "network", "image", "identity"},
		nets:     netsOne, flavors: flavorsOK, images: imagesOK,
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	dir := t.TempDir()
	yml := fmt.Sprintf(`clouds:
  test:
    auth:
      auth_url: %s/v3
      username: u
      password: not-a-real-password
      project_name: demo
      user_domain_name: Default
      project_domain_name: Default
    identity_api_version: 3
    region_name: RegionOne
`, f.srv.URL)
	p := filepath.Join(dir, "clouds.yaml")
	if err := os.WriteFile(p, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OS_CLIENT_CONFIG_FILE", p)
	curT = t
	return f
}

func (f *fakeCloud) token() string {
	var cat []string
	paths := map[string]string{"compute": "/compute/v2.1/", "network": "/network/", "image": "/image/", "identity": "/v3/"}
	for _, s := range f.services {
		cat = append(cat, fmt.Sprintf(`{"type":%q,"name":%q,"id":%q,"endpoints":[{"interface":"public","region":"RegionOne","region_id":"RegionOne","url":%q,"id":"e"}]}`,
			s, s, s, f.srv.URL+paths[s]))
	}
	return `{"token":{"expires_at":"2099-01-01T00:00:00.000000Z","project":{"id":"p1","name":"demo"},"catalog":[` + strings.Join(cat, ",") + `]}}`
}

func (f *fakeCloud) handle(w http.ResponseWriter, r *http.Request) {
	key := r.Method + " " + r.URL.Path
	f.mu.Lock()
	f.calls[key]++
	n := f.calls[key]
	limit, failing := f.fail[key]
	f.mu.Unlock()
	if failing && n > limit || f.failNth[key] == n {
		http.Error(w, `{"error":"boom"}`, 500)
		return
	}
	reply := func(code int, body string) {
		if bad, ok := f.garbled[key]; ok {
			body = bad
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		fmt.Fprint(w, body)
	}
	switch key {
	case "POST /v3/auth/tokens":
		if f.denyAuth {
			reply(401, `{"error":"no"}`)
			return
		}
		w.Header().Set("X-Subject-Token", "tok")
		reply(201, f.token())
	case "GET /network/":
		reply(200, `{"versions":[{"id":"v2.0","status":"CURRENT","links":[{"rel":"self","href":"`+f.srv.URL+`/network/v2.0/"}]}]}`)
	case "GET /image/":
		reply(200, `{"versions":[{"id":"v2.0","status":"CURRENT","links":[{"rel":"self","href":"`+f.srv.URL+`/image/v2/"}]}]}`)
	case "GET /compute/v2.1/servers/detail":
		if f.serversBody != "" {
			reply(200, f.serversBody)
			return
		}
		reply(200, serversList)
	case "GET /network/v2.0/ports":
		if f.portsBody != "" {
			reply(200, f.portsBody)
			return
		}
		reply(200, `{"ports":[{"id":"p1","security_groups":["g0"]}]}`)
	case "PUT /network/v2.0/ports/p1":
		f.record(r)
		reply(200, `{"port":{"id":"p1"}}`)
	case "DELETE /network/v2.0/security-group-rules/r88", "DELETE /network/v2.0/security-group-rules/r445", "DELETE /network/v2.0/security-group-rules/u1":
		f.record(r)
		reply(204, ``)
	case "GET /network/v2.0/networks":
		reply(200, f.nets)
	case "GET /network/v2.0/subnets":
		reply(200, `{"subnets":[{"id":"sb","name":"private-subnet","cidr":"192.168.1.0/24","network_id":"n1","enable_dhcp":true}]}`)
	case "GET /network/v2.0/floatingips":
		reply(200, `{"floatingips":[{"floating_ip_address":"157.136.254.110","fixed_ip_address":"192.168.1.67","status":"ACTIVE"}]}`)
	case "GET /compute/v2.1/os-keypairs":
		reply(200, `{"keypairs":[{"keypair":{"name":"k","fingerprint":"aa:bb"}}]}`)
	case "GET /network/v2.0/security-groups":
		if f.groups != "" {
			reply(200, f.groups)
			return
		}
		reply(200, `{"security_groups":[{"id":"g1","name":"default","security_group_rules":[]}]}`)
	case "GET /compute/v2.1/flavors/detail":
		reply(200, f.flavors)
	case "GET /image/v2/images":
		reply(200, f.images)
	case "POST /compute/v2.1/os-keypairs":
		f.record(r)
		reply(200, `{"keypair":{"name":"vm-key"}}`)
	case "POST /network/v2.0/security-groups":
		f.record(r)
		reply(201, `{"security_group":{"id":"g2","name":"vm-ssh"}}`)
	case "POST /network/v2.0/security-group-rules":
		f.record(r)
		reply(201, `{"security_group_rule":{"id":"r1"}}`)
	case "POST /compute/v2.1/servers":
		f.record(r)
		reply(202, `{"server":{"id":"s1"}}`)
	case "GET /compute/v2.1/servers/s1":
		reply(200, serverBody)
	default:
		reply(404, `{}`)
	}
}

func (f *fakeCloud) record(r *http.Request) {
	var b bytes.Buffer
	_, _ = b.ReadFrom(r.Body)
	f.mu.Lock()
	f.created = append(f.created, r.Method+" "+r.URL.Path+" "+b.String())
	f.mu.Unlock()
}

// curT is the running test; run reports a crash of the code under test as an
// assertion failure, so a guard whose removal crashes is still caught by a
// stated expectation and not by luck. Tests here do not run in parallel.
var curT *testing.T

func run(args ...string) (string, error) {
	cmd := RootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	defer func() {
		if r := recover(); r != nil {
			curT.Fatalf("a refusal was missing: the command crashed past it (%v)", r)
		}
	}()
	err := cmd.Execute()
	return out.String(), err
}

const cloudArg = "--cloud=test"

var createArgs = []string{cloudArg, "create", "--name=vm", "--network=private-network", "--keypair=k", "--ssh-from=192.168.1.0/24"}

func TestInventoryHappy(t *testing.T) {
	newFake(t)
	out, err := run(cloudArg, "inventory")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"project: demo (p1)", "server vm id=s1", "secgroups=sg-a,sg-b", "private-network:192.168.1.94(fixed)",
		"network private-network", "subnet private-subnet cidr=192.168.1.0/24", "floatingip 157.136.254.110 fixed=192.168.1.67",
		"keypair k", "secgroup default", "flavor vd.1", "image debian-13-genericcloud"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestInventoryEachCallFails(t *testing.T) {
	for _, key := range []string{
		"GET /compute/v2.1/servers/detail", "GET /network/v2.0/networks", "GET /network/v2.0/subnets",
		"GET /network/v2.0/floatingips", "GET /compute/v2.1/os-keypairs", "GET /network/v2.0/security-groups",
		"GET /compute/v2.1/flavors/detail", "GET /image/v2/images",
	} {
		t.Run(key, func(t *testing.T) {
			f := newFake(t)
			f.fail[key] = 0
			if _, err := run(cloudArg, "inventory"); err == nil {
				t.Fatal("error swallowed")
			}
		})
	}
}

func TestConnectFailures(t *testing.T) {
	f := newFake(t)
	f.denyAuth = true
	if _, err := run(cloudArg, "inventory"); err == nil || !strings.Contains(err.Error(), "authenticate") {
		t.Fatalf("auth err = %v", err)
	}
	for _, drop := range []string{"compute", "network", "image"} {
		t.Run("no "+drop, func(t *testing.T) {
			f := newFake(t)
			var keep []string
			for _, s := range f.services {
				if s != drop {
					keep = append(keep, s)
				}
			}
			f.services = keep
			if _, err := run(cloudArg, "inventory"); err == nil {
				t.Fatal("missing service accepted")
			}
			if _, err := run(append(createArgs, "--dry-run")...); err == nil {
				t.Fatal("create: missing service accepted")
			}
		})
	}
}

func TestCreateDryRunCreatesNothing(t *testing.T) {
	f := newFake(t)
	out, err := run(append(createArgs, "--dry-run")...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "plan: flavor=vd.1 image=debian-13-genericcloud network=private-network ssh-from=192.168.1.0/24") {
		t.Errorf("plan:\n%s", out)
	}
	if len(f.created) != 0 {
		t.Errorf("dry run wrote: %v", f.created)
	}
}

func TestCreateHappyOnlyOpensSSH(t *testing.T) {
	f := newFake(t)
	out, err := run(createArgs...)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"secgroup created: vm-ssh g2", "server created: s1", "address private-network 192.168.1.94", "server vm id=s1 status=ACTIVE"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	var rule, srv string
	for _, c := range f.created {
		switch {
		case strings.Contains(c, "security-group-rules"):
			rule = c
		case strings.Contains(c, "/servers "):
			srv = c
		}
		if strings.Contains(c, "os-keypairs") {
			t.Errorf("--keypair must not register a key: %s", c)
		}
	}
	for _, want := range []string{`"port_range_min":22`, `"port_range_max":22`, `"remote_ip_prefix":"192.168.1.0/24"`, `"direction":"ingress"`} {
		if !strings.Contains(rule, want) {
			t.Errorf("rule lacks %s: %s", want, rule)
		}
	}
	for _, port := range []string{"88", "445"} {
		if strings.Contains(rule, `"port_range_min":`+port) {
			t.Errorf("port %s opened", port)
		}
	}
	for _, want := range []string{`"key_name":"k"`, `"flavorRef":"f1"`, `"imageRef":"i1"`, `"user_data":"`, `"uuid":"n1"`} {
		if !strings.Contains(srv, want) {
			t.Errorf("server body lacks %s: %s", want, srv)
		}
	}
}

func TestCreatePubkeyRegistersKey(t *testing.T) {
	f := newFake(t)
	pub := filepath.Join(t.TempDir(), "k.pub")
	if err := os.WriteFile(pub, []byte("ssh-ed25519 AAAAC3 test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{cloudArg, "create", "--name=vm", "--network=private-network", "--pubkey-file=" + pub, "--ssh-from=192.168.1.0/24"}
	out, err := run(args...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "keypair created: vm-key") {
		t.Error(out)
	}
	if !strings.Contains(strings.Join(f.created, "\n"), `"public_key":"ssh-ed25519 AAAAC3 test"`) {
		t.Errorf("key not sent: %v", f.created)
	}
	if _, err := run(cloudArg, "create", "--name=vm", "--network=private-network", "--pubkey-file="+pub+".missing", "--ssh-from=192.168.1.0/24"); err == nil {
		t.Error("missing pubkey file accepted")
	}
}

func TestCreateEachCallFails(t *testing.T) {
	pub := filepath.Join(t.TempDir(), "k.pub")
	if err := os.WriteFile(pub, []byte("ssh-ed25519 AAAAC3 test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// absent is progress the command must NOT have reported when that call fails.
	cases := []struct {
		key    string
		limit  int
		absent string
	}{
		{"GET /network/v2.0/networks", 0, "plan:"}, {"GET /compute/v2.1/flavors/detail", 0, "plan:"}, {"GET /image/v2/images", 0, "plan:"},
		{"POST /compute/v2.1/os-keypairs", 0, "secgroup created"}, {"POST /network/v2.0/security-groups", 0, "secgroup created"},
		{"POST /network/v2.0/security-group-rules", 0, "secgroup created"}, {"POST /compute/v2.1/servers", 0, "server created"},
		{"GET /compute/v2.1/servers/s1", 0, "server vm id="}, {"GET /compute/v2.1/servers/s1", 1, "server vm id="},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s after %d", c.key, c.limit), func(t *testing.T) {
			f := newFake(t)
			f.fail[c.key] = c.limit
			args := []string{cloudArg, "create", "--name=vm", "--network=private-network", "--pubkey-file=" + pub, "--ssh-from=192.168.1.0/24"}
			out, err := run(args...)
			if err == nil {
				t.Fatal("error swallowed")
			}
			if !strings.Contains(err.Error(), "500") {
				t.Errorf("the refusal is not the server's failure: %v", err)
			}
			if strings.Contains(out, c.absent) {
				t.Errorf("went on past the failure (%q reported):\n%s", c.absent, out)
			}
		})
	}
}

func TestWaitFailureIsRefusedEvenIfTheNextReadWorks(t *testing.T) {
	f := newFake(t)
	f.failNth["GET /compute/v2.1/servers/s1"] = 1
	out, err := run(createArgs...)
	if err == nil || strings.Contains(out, "server vm id=") {
		t.Fatalf("a failed wait was reported as success: %v\n%s", err, out)
	}
}

var allowArgs = []string{cloudArg, "allow", "--group=default", "--from=192.168.1.94/32", "--port=88", "--port=445"}

func ruleBodies(f *fakeCloud) []string {
	var r []string
	for _, c := range f.created {
		if strings.Contains(c, "security-group-rules") {
			r = append(r, c)
		}
	}
	return r
}

func TestAllowAddsOneRulePerPortFromOneHost(t *testing.T) {
	f := newFake(t)
	out, err := run(allowArgs...)
	if err != nil {
		t.Fatal(err)
	}
	rs := ruleBodies(f)
	if len(rs) != 2 {
		t.Fatalf("rules = %v", rs)
	}
	for i, port := range []string{"88", "445"} {
		for _, want := range []string{`"port_range_min":` + port, `"port_range_max":` + port, `"remote_ip_prefix":"192.168.1.94/32"`, `"direction":"ingress"`, `"protocol":"tcp"`, `"security_group_id":"g1"`} {
			if !strings.Contains(rs[i], want) {
				t.Errorf("rule %d lacks %s: %s", i, want, rs[i])
			}
		}
		if !strings.Contains(out, "rule added: tcp/"+port+" from 192.168.1.94/32 in default") {
			t.Errorf("no report for %s:\n%s", port, out)
		}
	}
}

func TestAllowUDPAddsAUDPRuleAndSeesOnlyUDPAsExisting(t *testing.T) {
	f := newFake(t)
	// A TCP rule on the same port and host is not the UDP one.
	f.groups = `{"security_groups":[{"id":"g1","name":"default","security_group_rules":[` +
		`{"id":"t1","direction":"ingress","protocol":"tcp","remote_ip_prefix":"192.168.1.94/32","port_range_min":51820,"port_range_max":51820}]}]}`
	out, err := run(cloudArg, "allow", "--group=default", "--from=192.168.1.94/32", "--port=51820", "--proto=udp")
	if err != nil {
		t.Fatal(err)
	}
	rs := ruleBodies(f)
	if len(rs) != 1 || !strings.Contains(rs[0], `"protocol":"udp"`) || !strings.Contains(rs[0], `"port_range_min":51820`) {
		t.Fatalf("rules = %v", rs)
	}
	if !strings.Contains(out, "rule added: udp/51820 from 192.168.1.94/32 in default") {
		t.Errorf("out:\n%s", out)
	}
	f = newFake(t)
	f.groups = `{"security_groups":[{"id":"g1","name":"default","security_group_rules":[` +
		`{"id":"u1","direction":"ingress","protocol":"udp","remote_ip_prefix":"192.168.1.94/32","port_range_min":51820,"port_range_max":51820}]}]}`
	out, err = run(cloudArg, "allow", "--group=default", "--from=192.168.1.94/32", "--port=51820", "--proto=udp")
	if err != nil || len(ruleBodies(f)) != 0 || !strings.Contains(out, "rule exists: udp/51820") {
		t.Errorf("not idempotent: %v %v\n%s", err, ruleBodies(f), out)
	}
	f = newFake(t)
	if _, err := run(cloudArg, "allow", "--group=default", "--from=192.168.1.94/32", "--port=1", "--proto=sctp"); err == nil || !strings.Contains(err.Error(), "tcp or udp") || len(f.calls) != 0 {
		t.Errorf("a bad --proto: %v %v", err, f.calls)
	}
}

func TestRevokeUDPDeletesOnlyTheUDPRule(t *testing.T) {
	f := newFake(t)
	f.groups = `{"security_groups":[{"id":"g1","name":"mine","security_group_rules":[` +
		`{"id":"t1","direction":"ingress","protocol":"tcp","remote_ip_prefix":"192.168.1.94/32","port_range_min":51820,"port_range_max":51820},` +
		`{"id":"u1","direction":"ingress","protocol":"udp","remote_ip_prefix":"192.168.1.94/32","port_range_min":51820,"port_range_max":51820}]}]}`
	out, err := run(cloudArg, "revoke", "--group=mine", "--from=192.168.1.94/32", "--port=51820", "--proto=udp")
	if err != nil || !strings.Contains(out, "rule deleted: udp/51820 from 192.168.1.94/32 in mine id=u1") || len(f.created) != 1 || strings.Contains(f.created[0], "t1") {
		t.Fatalf("%v %v\n%s", err, f.created, out)
	}
}

func TestAllowIsIdempotent(t *testing.T) {
	f := newFake(t)
	f.groups = `{"security_groups":[{"id":"g1","name":"default","security_group_rules":[` +
		`{"id":"r88","direction":"ingress","protocol":"tcp","remote_ip_prefix":"192.168.1.94/32","port_range_min":88,"port_range_max":88},` +
		`{"direction":"ingress","protocol":"tcp","remote_ip_prefix":"10.0.0.1/32","port_range_min":445,"port_range_max":445},` +
		`{"direction":"egress","protocol":"tcp","remote_ip_prefix":"192.168.1.94/32","port_range_min":445,"port_range_max":445}]}]}`
	out, err := run(allowArgs...)
	if err != nil {
		t.Fatal(err)
	}
	rs := ruleBodies(f)
	if len(rs) != 1 || !strings.Contains(rs[0], `"port_range_min":445`) {
		t.Fatalf("rules = %v", rs)
	}
	if !strings.Contains(out, "rule exists: tcp/88") || !strings.Contains(out, "rule added: tcp/445") {
		t.Errorf("out:\n%s", out)
	}
}

func TestAllowRefusals(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--from=192.168.1.94/32", "--port=88"}, "--group and"},
		{[]string{"--group=default", "--from=192.168.1.94/32"}, "--group and"},
		{[]string{"--group=default", "--from=192.168.1.0/24", "--port=88"}, "/32"},
		{[]string{"--group=default", "--from=0.0.0.0/0", "--port=88"}, "/32"},
		{[]string{"--group=default", "--from=fe80::1/128", "--port=88"}, "/32"},
		{[]string{"--group=default", "--from=junk", "--port=88"}, "ParsePrefix"},
		{[]string{"--group=default", "--port=88"}, "--from"},
		{[]string{"--group=default", "--from=192.168.1.94/32", "--port=0"}, "out of range"},
		{[]string{"--group=default", "--from=192.168.1.94/32", "--port=65536"}, "out of range"},
	}
	for _, c := range cases {
		t.Run(c.want+strings.Join(c.args, " "), func(t *testing.T) {
			f := newFake(t)
			_, err := run(append([]string{cloudArg, "allow"}, c.args...)...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if len(f.calls) != 0 {
				t.Errorf("reached the cloud: %v", f.calls)
			}
		})
	}
	newFake(t)
	if _, err := run("allow", "--group=default", "--from=192.168.1.94/32", "--port=88"); err == nil || !strings.Contains(err.Error(), "--cloud") {
		t.Errorf("allow without --cloud: %v", err)
	}
}

func TestAllowGroupAndCallFailures(t *testing.T) {
	f := newFake(t)
	f.groups = `{"security_groups":[]}`
	if _, err := run(allowArgs...); err == nil || !strings.Contains(err.Error(), "want exactly one") {
		t.Errorf("no group: %v", err)
	}
	f = newFake(t)
	f.groups = `{"security_groups":[{"id":"a","name":"default"},{"id":"b","name":"default"}]}`
	if _, err := run(allowArgs...); err == nil || !strings.Contains(err.Error(), "want exactly one") {
		t.Errorf("two groups: %v", err)
	}
	if len(ruleBodies(f)) != 0 {
		t.Error("wrote a rule to an ambiguous group")
	}
	f = newFake(t)
	f.fail["GET /network/v2.0/security-groups"] = 0
	if _, err := run(allowArgs...); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("list failure: %v", err)
	}
	f = newFake(t)
	f.fail["POST /network/v2.0/security-group-rules"] = 0
	out, err := run(allowArgs...)
	if err == nil || !strings.Contains(err.Error(), "tcp/88") || strings.Contains(out, "rule added") {
		t.Errorf("rule failure: %v\n%s", err, out)
	}
	f = newFake(t)
	f.fail["POST /network/v2.0/security-group-rules"] = 1
	out, err = run(allowArgs...)
	if err == nil || !strings.Contains(err.Error(), "tcp/445") || !strings.Contains(out, "rule added: tcp/88") {
		t.Errorf("second rule failure: %v\n%s", err, out)
	}
	f = newFake(t)
	f.denyAuth = true
	if _, err := run(allowArgs...); err == nil || !strings.Contains(err.Error(), "authenticate") {
		t.Errorf("auth: %v", err)
	}
}

const sharedServers = `{"servers":[` +
	`{"id":"s1","name":"staging","security_groups":[{"name":"shared"}]},` +
	`{"id":"s2","name":"prod","security_groups":[{"name":"shared"},{"name":"other"}]}]}`

func TestAllowRefusesAGroupMoreThanOneServerCarries(t *testing.T) {
	f := newFake(t)
	f.serversBody = sharedServers
	f.groups = `{"security_groups":[{"id":"g1","name":"shared"}]}`
	args := []string{cloudArg, "allow", "--group=shared", "--from=192.168.1.94/32", "--port=88"}
	_, err := run(args...)
	if err == nil || !strings.Contains(err.Error(), "attached to 2 servers (staging, prod)") || !strings.Contains(err.Error(), "--allow-shared") {
		t.Fatalf("shared group accepted: %v", err)
	}
	if len(ruleBodies(f)) != 0 {
		t.Error("wrote a rule into a shared group")
	}
	out, err := run(append(args, "--allow-shared")...)
	if err != nil || !strings.Contains(out, "rule added: tcp/88 from 192.168.1.94/32 in shared id=r1") {
		t.Fatalf("explicit flag: %v\n%s", err, out)
	}
	f = newFake(t)
	f.serversBody = sharedServers
	f.groups = `{"security_groups":[{"id":"g1","name":"other"}]}`
	if out, err := run(cloudArg, "allow", "--group=other", "--from=192.168.1.94/32", "--port=88"); err != nil {
		t.Fatalf("group of one server refused: %v\n%s", err, out)
	}
	f = newFake(t)
	f.fail["GET /compute/v2.1/servers/detail"] = 0
	if _, err := run(allowArgs...); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("server listing failure: %v", err)
	}
}

func TestAllowCreatesTheGroupOnlyWhenAsked(t *testing.T) {
	f := newFake(t)
	f.groups = `{"security_groups":[]}`
	args := []string{cloudArg, "allow", "--group=mine", "--from=192.168.1.94/32", "--port=88"}
	if _, err := run(args...); err == nil || !strings.Contains(err.Error(), "want exactly one, got 0") {
		t.Fatalf("created without being asked: %v", err)
	}
	if len(f.created) != 0 {
		t.Errorf("wrote: %v", f.created)
	}
	out, err := run(append(args, "--create-group")...)
	if err != nil || !strings.Contains(out, "group created: vm-ssh id=g2") || !strings.Contains(out, "id=r1") {
		t.Fatalf("create-group: %v\n%s", err, out)
	}
	if !strings.Contains(f.created[0], `"name":"mine"`) || !strings.Contains(f.created[1], `"security_group_id":"g2"`) {
		t.Errorf("calls: %v", f.created)
	}
	f = newFake(t)
	f.groups = `{"security_groups":[]}`
	f.fail["POST /network/v2.0/security-groups"] = 0
	if _, err := run(append(args, "--create-group")...); err == nil || !strings.Contains(err.Error(), "create security group") {
		t.Errorf("create failure: %v", err)
	}
}

func TestAttachKeepsTheOtherGroups(t *testing.T) {
	f := newFake(t)
	f.serversBody = sharedServers
	f.groups = `{"security_groups":[{"id":"g1","name":"mine"}]}`
	out, err := run(cloudArg, "attach", "--group=mine", "--server=staging")
	if err != nil || !strings.Contains(out, "attached: mine to staging port=p1 groups=2") {
		t.Fatalf("attach: %v\n%s", err, out)
	}
	if len(f.created) != 1 || !strings.Contains(f.created[0], `"security_groups":["g0","g1"]`) {
		t.Errorf("update: %v", f.created)
	}
	f = newFake(t)
	f.serversBody = sharedServers
	f.groups = `{"security_groups":[{"id":"g0","name":"mine"}]}`
	out, err = run(cloudArg, "attach", "--group=mine", "--server=staging")
	if err != nil || len(f.created) != 0 || !strings.Contains(out, "groups=1") {
		t.Errorf("already attached: %v %v\n%s", err, f.created, out)
	}
}

func TestAttachRefusalsAndFailures(t *testing.T) {
	newFake(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"attach", "--server=staging"}, "--group and --server"},
		{[]string{"attach", "--group=mine"}, "--group and --server"},
	} {
		if _, err := run(append([]string{cloudArg}, c.args...)...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v", c.args, err)
		}
	}
	if _, err := run("attach", "--group=mine", "--server=staging"); err == nil || !strings.Contains(err.Error(), "--cloud") {
		t.Errorf("no cloud: %v", err)
	}
	mk := func() *fakeCloud {
		f := newFake(t)
		f.serversBody = sharedServers
		f.groups = `{"security_groups":[{"id":"g1","name":"mine"}]}`
		return f
	}
	at := []string{cloudArg, "attach", "--group=mine", "--server=staging"}
	f := mk()
	f.groups = `{"security_groups":[]}`
	if _, err := run(at...); err == nil || !strings.Contains(err.Error(), "want exactly one") {
		t.Errorf("no group: %v", err)
	}
	mk()
	if _, err := run(cloudArg, "attach", "--group=mine", "--server=nobody"); err == nil || !strings.Contains(err.Error(), `server "nobody"`) {
		t.Errorf("no server: %v", err)
	}
	f = mk()
	f.portsBody = `{"ports":[]}`
	if _, err := run(at...); err == nil || !strings.Contains(err.Error(), "has no port") {
		t.Errorf("no port: %v", err)
	}
	f = mk()
	f.fail["PUT /network/v2.0/ports/p1"] = 0
	if _, err := run(at...); err == nil || !strings.Contains(err.Error(), "port p1") {
		t.Errorf("update failure: %v", err)
	}
	f = mk()
	f.fail["GET /network/v2.0/ports"] = 0
	if _, err := run(at...); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("port listing failure: %v", err)
	}
	f = mk()
	f.fail["GET /compute/v2.1/servers/detail"] = 0
	if _, err := run(at...); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("server listing failure: %v", err)
	}
	f = mk()
	f.denyAuth = true
	if _, err := run(at...); err == nil || !strings.Contains(err.Error(), "authenticate") {
		t.Errorf("auth: %v", err)
	}
}

const ruledGroup = `{"security_groups":[{"id":"g1","name":"mine","security_group_rules":[` +
	`{"id":"r88","direction":"ingress","protocol":"tcp","remote_ip_prefix":"192.168.1.94/32","port_range_min":88,"port_range_max":88},` +
	`{"id":"r445","direction":"ingress","protocol":"tcp","remote_ip_prefix":"192.168.1.94/32","port_range_min":445,"port_range_max":445},` +
	`{"id":"r22","direction":"ingress","protocol":"tcp","remote_ip_prefix":"192.168.1.94/32","port_range_min":22,"port_range_max":22},` +
	`{"id":"rw","direction":"ingress","protocol":"tcp","remote_ip_prefix":"0.0.0.0/0","port_range_min":88,"port_range_max":88}]}]}`

func TestRevokeDeletesOnlyTheMatchingRules(t *testing.T) {
	f := newFake(t)
	f.groups = ruledGroup
	out, err := run(cloudArg, "revoke", "--group=mine", "--from=192.168.1.94/32", "--port=88", "--port=445")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"rule deleted: tcp/88 from 192.168.1.94/32 in mine id=r88", "rule deleted: tcp/445 from 192.168.1.94/32 in mine id=r445"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if len(f.created) != 2 || strings.Contains(strings.Join(f.created, " "), "r22") || strings.Contains(strings.Join(f.created, " "), "rw") {
		t.Errorf("deleted: %v", f.created)
	}
	for _, args := range [][]string{
		{"revoke", "--group=mine", "--from=0.0.0.0/0", "--port=88"},
		{"revoke", "--from=192.168.1.94/32", "--port=88"},
	} {
		f = newFake(t)
		f.groups = ruledGroup
		if _, err := run(append([]string{cloudArg}, args...)...); err == nil {
			t.Errorf("%v accepted", args)
		}
		if len(f.created) != 0 {
			t.Errorf("%v deleted: %v", args, f.created)
		}
	}
	newFake(t)
	if _, err := run("revoke", "--group=mine", "--from=192.168.1.94/32", "--port=88"); err == nil || !strings.Contains(err.Error(), "--cloud") {
		t.Errorf("no cloud: %v", err)
	}
	f = newFake(t)
	f.groups = ruledGroup
	f.fail["DELETE /network/v2.0/security-group-rules/r88"] = 0
	if _, err := run(cloudArg, "revoke", "--group=mine", "--from=192.168.1.94/32", "--port=88"); err == nil || !strings.Contains(err.Error(), "delete rule r88") {
		t.Errorf("delete failure: %v", err)
	}
	f = newFake(t)
	f.groups = `{"security_groups":[]}`
	if _, err := run(cloudArg, "revoke", "--group=mine", "--from=192.168.1.94/32", "--port=88"); err == nil || !strings.Contains(err.Error(), "want exactly one") {
		t.Errorf("no group: %v", err)
	}
	f = newFake(t)
	f.denyAuth = true
	if _, err := run(cloudArg, "revoke", "--group=mine", "--from=192.168.1.94/32", "--port=88"); err == nil || !strings.Contains(err.Error(), "authenticate") {
		t.Errorf("auth: %v", err)
	}
}

func TestGroupListingFailureIsReportedAsSuch(t *testing.T) {
	for _, args := range [][]string{
		{"rules", "--group=mine"},
		{"attach", "--group=mine", "--server=staging"},
		{"revoke", "--group=mine", "--from=192.168.1.94/32", "--port=88"},
	} {
		f := newFake(t)
		f.fail["GET /network/v2.0/security-groups"] = 0
		if _, err := run(append([]string{cloudArg}, args...)...); err == nil || !strings.Contains(err.Error(), "500") {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestRulesListsRulesAndServers(t *testing.T) {
	f := newFake(t)
	f.groups = ruledGroup
	f.serversBody = sharedServers
	out, err := run(cloudArg, "rules", "--group=mine")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"group mine id=g1 rules=4", "rule id=r88 ingress tcp 88-88 from=192.168.1.94/32", "attached to: "} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if len(f.created) != 0 {
		t.Errorf("a read wrote: %v", f.created)
	}
	if _, err := run(cloudArg, "rules"); err == nil || !strings.Contains(err.Error(), "--group") {
		t.Errorf("no group flag: %v", err)
	}
	if _, err := run("rules", "--group=mine"); err == nil || !strings.Contains(err.Error(), "--cloud") {
		t.Errorf("no cloud: %v", err)
	}
	f = newFake(t)
	f.groups = `{"security_groups":[]}`
	if _, err := run(cloudArg, "rules", "--group=mine"); err == nil {
		t.Error("missing group accepted")
	}
	f = newFake(t)
	f.groups = ruledGroup
	f.fail["GET /compute/v2.1/servers/detail"] = 0
	if _, err := run(cloudArg, "rules", "--group=mine"); err == nil {
		t.Error("server listing failure accepted")
	}
	f = newFake(t)
	f.denyAuth = true
	if _, err := run(cloudArg, "rules", "--group=mine"); err == nil || !strings.Contains(err.Error(), "authenticate") {
		t.Errorf("auth: %v", err)
	}
}

var undecodable = map[string]string{
	"GET /compute/v2.1/servers/detail":  `{"servers":[{"id":5}]}`,
	"GET /network/v2.0/networks":        `{"networks":[{"id":5}]}`,
	"GET /network/v2.0/subnets":         `{"subnets":[{"id":5}]}`,
	"GET /network/v2.0/floatingips":     `{"floatingips":[{"id":5}]}`,
	"GET /compute/v2.1/os-keypairs":     `{"keypairs":[{"keypair":{"name":5}}]}`,
	"GET /network/v2.0/security-groups": `{"security_groups":[{"id":5}]}`,
	"GET /compute/v2.1/flavors/detail":  `{"flavors":[{"id":5}]}`,
	"GET /image/v2/images":              `{"images":[{"id":5}]}`,
}

func TestInventoryUndecodableReplies(t *testing.T) {
	for key, body := range undecodable {
		t.Run(key, func(t *testing.T) {
			f := newFake(t)
			f.garbled[key] = body
			_, err := run(cloudArg, "inventory")
			if err == nil {
				t.Fatal("undecodable reply accepted")
			}
			t.Logf("refused with: %v", err)
		})
	}
}

func TestCreateUndecodableReplies(t *testing.T) {
	for _, key := range []string{"GET /network/v2.0/networks", "GET /compute/v2.1/flavors/detail", "GET /image/v2/images"} {
		t.Run(key, func(t *testing.T) {
			f := newFake(t)
			f.garbled[key] = undecodable[key]
			out, err := run(append(createArgs, "--dry-run")...)
			if err == nil || strings.Contains(out, "plan:") {
				t.Fatalf("undecodable reply accepted: %v\n%s", err, out)
			}
		})
	}
}

func TestCreateValidationRefusesBeforeAnyCall(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--network=n", "--keypair=k", "--ssh-from=10.0.0.0/8"}, "--name and --network"},
		{[]string{"--name=a", "--keypair=k", "--ssh-from=10.0.0.0/8"}, "--name and --network"},
		{[]string{"--name=a", "--network=n", "--ssh-from=10.0.0.0/8"}, "exactly one"},
		{[]string{"--name=a", "--network=n", "--keypair=k", "--pubkey-file=f", "--ssh-from=10.0.0.0/8"}, "exactly one"},
		{[]string{"--name=a", "--network=n", "--keypair=k"}, "--ssh-from is required"},
		{[]string{"--name=a", "--network=n", "--keypair=k", "--ssh-from=0.0.0.0/0"}, "world-open"},
		{[]string{"--name=a", "--network=n", "--keypair=k", "--ssh-from=junk"}, "--ssh-from"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			f := newFake(t)
			_, err := run(append([]string{cloudArg, "create"}, c.args...)...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if len(f.calls) != 0 {
				t.Errorf("reached the cloud: %v", f.calls)
			}
		})
	}
	newFake(t)
	if _, err := run("create", "--name=a", "--network=n", "--keypair=k", "--ssh-from=10.0.0.0/8"); err == nil || !strings.Contains(err.Error(), "--cloud") {
		t.Errorf("create without --cloud: %v", err)
	}
}

func TestPickFlavorTieBreaks(t *testing.T) {
	// Every pair is listed in the order that makes the wrong rule pick wrongly.
	cases := []struct {
		name string
		all  []flavors.Flavor
		want string
	}{
		{"ram first", []flavors.Flavor{{Name: "more-ram", RAM: 4096, VCPUs: 1, Disk: 10}, {Name: "less-ram", RAM: 2048, VCPUs: 8, Disk: 90}}, "less-ram"},
		{"then vcpus", []flavors.Flavor{{Name: "two", RAM: 2048, VCPUs: 2, Disk: 10}, {Name: "one", RAM: 2048, VCPUs: 1, Disk: 90}}, "one"},
		{"then disk", []flavors.Flavor{{Name: "big", RAM: 2048, VCPUs: 1, Disk: 50}, {Name: "small", RAM: 2048, VCPUs: 1, Disk: 10}}, "small"},
	}
	for _, c := range cases {
		f, err := pickFlavor(c.all, "", 1024, 8)
		if err != nil || f.Name != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.name, f.Name, err, c.want)
		}
	}
}

func TestCreateResolutionRefusals(t *testing.T) {
	cases := map[string]func(*fakeCloud){
		"two networks": func(f *fakeCloud) {
			f.nets = `{"networks":[{"id":"a","name":"private-network"},{"id":"b","name":"private-network"}]}`
		},
		"no network":  func(f *fakeCloud) { f.nets = `{"networks":[]}` },
		"no flavor":   func(f *fakeCloud) { f.flavors = `{"flavors":[]}` },
		"no image":    func(f *fakeCloud) { f.images = `{"images":[]}` },
		"queued only": func(f *fakeCloud) { f.images = `{"images":[{"id":"i","name":"debian-13","status":"queued"}]}` },
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			mod(f)
			if _, err := run(append(createArgs, "--dry-run")...); err == nil {
				t.Fatal("accepted")
			}
			if len(f.created) != 0 {
				t.Errorf("wrote: %v", f.created)
			}
		})
	}
}

func TestCloudsListsNamesOnly(t *testing.T) {
	newFake(t)
	out, err := run("clouds")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "test" || strings.Contains(out, "password") {
		t.Errorf("out = %q", out)
	}
}

func TestCloudsBadFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(p, []byte("clouds: [unterminated"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OS_CLIENT_CONFIG_FILE", p)
	if _, err := run("clouds"); err == nil {
		t.Fatal("bad clouds.yaml accepted")
	}
}
