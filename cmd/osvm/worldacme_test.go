package main

import (
	"strings"
	"testing"
)

const oneServerWeb = `{"servers":[{"id":"s1","name":"ci","security_groups":[{"name":"web"}]}]}`

func worldFake(t *testing.T) *fakeCloud {
	f := newFake(t)
	f.serversBody = oneServerWeb
	f.groups = `{"security_groups":[{"id":"g1","name":"web"}]}`
	return f
}

func TestWorldACMEOpensPort80FromTheWorldOnly(t *testing.T) {
	f := worldFake(t)
	out, err := run(cloudArg, "allow", "--group=web", "--world-acme", "--port=80")
	if err != nil {
		t.Fatal(err)
	}
	rs := ruleBodies(f)
	if len(rs) != 2 {
		t.Fatalf("rules = %v", rs)
	}
	for i, src := range []string{"0.0.0.0/0", "::/0"} {
		for _, want := range []string{`"remote_ip_prefix":"` + src + `"`, `"port_range_min":80`, `"port_range_max":80`, `"protocol":"tcp"`, `"direction":"ingress"`} {
			if !strings.Contains(rs[i], want) {
				t.Errorf("rule %d lacks %s: %s", i, want, rs[i])
			}
		}
		if !strings.Contains(out, "rule added: tcp/80 from "+src+" in web id=r") {
			t.Errorf("no report for %s:\n%s", src, out)
		}
	}
}

func TestWorldACMEIsIdempotent(t *testing.T) {
	f := worldFake(t)
	f.groups = `{"security_groups":[{"id":"g1","name":"web","security_group_rules":[` +
		`{"id":"w4","direction":"ingress","protocol":"tcp","remote_ip_prefix":"0.0.0.0/0","port_range_min":80,"port_range_max":80},` +
		`{"id":"w6","direction":"ingress","protocol":"tcp","remote_ip_prefix":"::/0","port_range_min":80,"port_range_max":80}]}]}`
	out, err := run(cloudArg, "allow", "--group=web", "--world-acme", "--port=80")
	if err != nil || len(ruleBodies(f)) != 0 || strings.Count(out, "rule exists: tcp/80") != 2 {
		t.Fatalf("%v %v\n%s", err, ruleBodies(f), out)
	}
}

func TestWorldACMERefusals(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--world-acme", "--port=80"}, "--group is required"},
		{[]string{"--group=web", "--world-acme", "--port=80", "--from=192.168.1.94/32"}, "exclusive with --from"},
		{[]string{"--group=web", "--world-acme", "--port=80", "--from-json-url=https://x/y.json"}, "exclusive with --from-json-url"},
		{[]string{"--group=web", "--world-acme", "--port=80", "--allow-shared"}, "exclusive with --allow-shared"},
		{[]string{"--group=web", "--world-acme", "--port=80", "--create-group"}, "exclusive with --create-group"},
		{[]string{"--group=web", "--world-acme", "--port=80", "--proto=udp"}, "tcp only"},
		{[]string{"--group=web", "--world-acme", "--port=443"}, "port 80 only"},
		{[]string{"--group=web", "--world-acme", "--port=80", "--port=443"}, "port 80 only"},
		{[]string{"--group=web", "--world-acme"}, "port 80 only"},
	}
	for _, c := range cases {
		t.Run(c.want+strings.Join(c.args, " "), func(t *testing.T) {
			f := worldFake(t)
			_, err := run(append([]string{cloudArg, "allow"}, c.args...)...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if len(f.created) != 0 {
				t.Errorf("wrote: %v", f.created)
			}
		})
	}
}

func TestWorldACMERefusesAGroupNotCarriedByExactlyOneServer(t *testing.T) {
	for name, servers := range map[string]string{
		"two":  `{"servers":[{"id":"s1","name":"a","security_groups":[{"name":"web"}]},{"id":"s2","name":"b","security_groups":[{"name":"web"}]}]}`,
		"none": `{"servers":[{"id":"s1","name":"a","security_groups":[{"name":"other"}]}]}`,
	} {
		f := worldFake(t)
		f.serversBody = servers
		_, err := run(cloudArg, "allow", "--group=web", "--world-acme", "--port=80")
		if err == nil || !strings.Contains(err.Error(), "exactly one server") {
			t.Errorf("%s: %v", name, err)
		}
		if len(ruleBodies(f)) != 0 {
			t.Errorf("%s: wrote a rule", name)
		}
	}
}

func TestWorldACMERevokeClosesBothRules(t *testing.T) {
	f := worldFake(t)
	f.groups = `{"security_groups":[{"id":"g1","name":"web","security_group_rules":[` +
		`{"id":"w4","direction":"ingress","protocol":"tcp","remote_ip_prefix":"0.0.0.0/0","port_range_min":80,"port_range_max":80},` +
		`{"id":"w6","direction":"ingress","protocol":"tcp","remote_ip_prefix":"::/0","port_range_min":80,"port_range_max":80},` +
		`{"id":"r22","direction":"ingress","protocol":"tcp","remote_ip_prefix":"0.0.0.0/0","port_range_min":22,"port_range_max":22}]}]}`
	out, err := run(cloudArg, "revoke", "--group=web", "--world-acme", "--port=80")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"rule deleted: tcp/80 from 0.0.0.0/0 in web id=w4", "rule deleted: tcp/80 from ::/0 in web id=w6"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if len(f.created) != 2 || strings.Contains(strings.Join(f.created, " "), "r22") {
		t.Errorf("deleted: %v", f.created)
	}
}
