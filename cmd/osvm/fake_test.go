package main

import (
	"bytes"
	"fmt"
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
	denyAuth bool
	nets     string
	flavors  string
	images   string
	created  []string
}

const (
	netsOne     = `{"networks":[{"id":"n1","name":"private-network"}]}`
	flavorsOK   = `{"flavors":[{"id":"f1","name":"vd.1","ram":2048,"vcpus":1,"disk":10},{"id":"f2","name":"os.6","ram":12268,"vcpus":6,"disk":20}]}`
	imagesOK    = `{"images":[{"id":"i1","name":"debian-13-genericcloud","status":"active","created_at":"2026-08-03T00:00:00Z"}]}`
	serverBody  = `{"server":{"id":"s1","name":"vm","status":"ACTIVE","key_name":"k","addresses":{"private-network":[{"addr":"192.168.1.94","OS-EXT-IPS:type":"fixed"}]}}}`
	serversList = `{"servers":[{"id":"s1","name":"vm","status":"ACTIVE","key_name":"k","addresses":{"private-network":[{"addr":"192.168.1.94","OS-EXT-IPS:type":"fixed"}]}}]}`
)

func newFake(t *testing.T) *fakeCloud {
	t.Helper()
	f := &fakeCloud{
		fail: map[string]int{}, calls: map[string]int{},
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
	if failing && n > limit {
		http.Error(w, `{"error":"boom"}`, 500)
		return
	}
	reply := func(code int, body string) {
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
		reply(200, serversList)
	case "GET /network/v2.0/networks":
		reply(200, f.nets)
	case "GET /network/v2.0/subnets":
		reply(200, `{"subnets":[{"id":"sb","name":"private-subnet","cidr":"192.168.1.0/24","network_id":"n1","enable_dhcp":true}]}`)
	case "GET /network/v2.0/floatingips":
		reply(200, `{"floatingips":[{"floating_ip_address":"157.136.254.110","fixed_ip_address":"192.168.1.67","status":"ACTIVE"}]}`)
	case "GET /compute/v2.1/os-keypairs":
		reply(200, `{"keypairs":[{"keypair":{"name":"k","fingerprint":"aa:bb"}}]}`)
	case "GET /network/v2.0/security-groups":
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

func run(args ...string) (string, error) {
	cmd := RootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
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
	for _, want := range []string{"project: demo (p1)", "server vm id=s1", "private-network:192.168.1.94(fixed)",
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
	cases := []struct {
		key   string
		limit int
	}{
		{"GET /network/v2.0/networks", 0}, {"GET /compute/v2.1/flavors/detail", 0}, {"GET /image/v2/images", 0},
		{"POST /compute/v2.1/os-keypairs", 0}, {"POST /network/v2.0/security-groups", 0},
		{"POST /network/v2.0/security-group-rules", 0}, {"POST /compute/v2.1/servers", 0},
		{"GET /compute/v2.1/servers/s1", 0}, {"GET /compute/v2.1/servers/s1", 1},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s after %d", c.key, c.limit), func(t *testing.T) {
			f := newFake(t)
			f.fail[c.key] = c.limit
			args := []string{cloudArg, "create", "--name=vm", "--network=private-network", "--pubkey-file=" + pub, "--ssh-from=192.168.1.0/24"}
			if _, err := run(args...); err == nil {
				t.Fatal("error swallowed")
			}
		})
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
