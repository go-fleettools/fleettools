package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateWithDedicatedGroupAndDataVolume(t *testing.T) {
	f := newFake(t)
	args := append(append([]string{}, createArgs...), "--sg-name=garm-ci", "--volume-gb=200")
	out, err := run(args...)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"volume created: vm-data id=v1 size=200GB", "volume attached: v1 to s1", "server vm id=s1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	all := strings.Join(f.created, "\n")
	for _, want := range []string{`"name":"garm-ci"`, `"size":200`, `"volumeId":"v1"`} {
		if !strings.Contains(all, want) {
			t.Errorf("request lacks %s:\n%s", want, all)
		}
	}
}

func TestCreateWithoutVolumeFlagTouchesNoVolume(t *testing.T) {
	f := newFake(t)
	if _, err := run(createArgs...); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(f.created, "\n"), "volume") {
		t.Errorf("a volume was touched: %v", f.created)
	}
}

func TestCreateVolumeNeedsBlockStorage(t *testing.T) {
	f := newFake(t)
	f.services = []string{"compute", "network", "image", "identity"}
	out, err := run(append(append([]string{}, createArgs...), "--volume-gb=10")...)
	if err == nil || !strings.Contains(err.Error(), "block storage") {
		t.Fatalf("err=%v", err)
	}
	if len(f.created) != 0 || strings.Contains(out, "plan:") {
		t.Errorf("went on: %v\n%s", f.created, out)
	}
}

func TestCreateVolumeCallFailures(t *testing.T) {
	cases := []struct{ key, absent string }{
		{"POST /volume/v3/p1/volumes", "volume created"},
		{"GET /volume/v3/p1/volumes/v1", "volume attached"},
		{"POST /compute/v2.1/servers/s1/os-volume_attachments", "volume attached"},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			f := newFake(t)
			f.fail[c.key] = 0
			out, err := run(append(append([]string{}, createArgs...), "--volume-gb=10")...)
			if err == nil || !strings.Contains(err.Error(), "500") {
				t.Fatalf("err=%v", err)
			}
			if strings.Contains(out, c.absent) || strings.Contains(out, "server vm id=") {
				t.Errorf("went on past the failure:\n%s", out)
			}
		})
	}
}

func TestCreateVolumeSizeRefused(t *testing.T) {
	newFake(t)
	for _, v := range []string{"-1", "16001"} {
		if _, err := run(append(append([]string{}, createArgs...), "--volume-gb="+v)...); err == nil {
			t.Errorf("--volume-gb=%s accepted", v)
		}
	}
}

// metaServer answers every request with body and points httpClient at it.
func metaServer(t *testing.T, code int, body string) string {
	t.Helper()
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(s.Close)
	old := httpClient
	httpClient = s.Client()
	t.Cleanup(func() { httpClient = old })
	return s.URL
}

func fromURLArgs(url string) []string {
	return []string{cloudArg, "allow", "--group=default", "--from-json-url=" + url, "--json-key=hooks", "--port=9997"}
}

func TestAllowFromJSONURLOpensEveryListedPrefixByFamily(t *testing.T) {
	f := newFake(t)
	url := metaServer(t, 200, `{"hooks":["192.30.252.0/22","2a0a:a440::/29","192.30.252.0/22"],"web":["1.2.3.4/32"]}`)
	out, err := run(fromURLArgs(url)...)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "rule added: tcp/9997"); n != 2 {
		t.Errorf("want 2 rules (duplicate dropped), got %d:\n%s", n, out)
	}
	rs := strings.Join(ruleBodies(f), "\n")
	for _, want := range []string{`"remote_ip_prefix":"192.30.252.0/22"`, `"ethertype":"IPv4"`, `"remote_ip_prefix":"2a0a:a440::/29"`, `"ethertype":"IPv6"`} {
		if !strings.Contains(rs, want) {
			t.Errorf("rules lack %s:\n%s", want, rs)
		}
	}
	if strings.Contains(rs, "1.2.3.4") {
		t.Error("a prefix of another key was opened")
	}
}

func TestAllowFromJSONURLIsIdempotent(t *testing.T) {
	f := newFake(t)
	f.groups = `{"security_groups":[{"id":"g1","name":"default","security_group_rules":[{"id":"x1","direction":"ingress","protocol":"tcp","port_range_min":9997,"port_range_max":9997,"remote_ip_prefix":"192.30.252.0/22"}]}]}`
	url := metaServer(t, 200, `{"hooks":["192.30.252.0/22","185.199.108.0/22"]}`)
	out, err := run(fromURLArgs(url)...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "rule exists: tcp/9997 from 192.30.252.0/22 in default id=x1") || strings.Count(out, "rule added") != 1 {
		t.Errorf("not idempotent:\n%s", out)
	}
}

func TestAllowFromJSONURLRefusals(t *testing.T) {
	many := make([]string, maxSources+1)
	for i := range many {
		many[i] = fmt.Sprintf(`"10.%d.%d.0/24"`, i/256, i%256)
	}
	cases := []struct {
		name, body string
		code       int
		want       string
	}{
		{"world v4", `{"hooks":["0.0.0.0/0"]}`, 200, "world-open"},
		{"world v6", `{"hooks":["::/0"]}`, 200, "world-open"},
		{"malformed", `{"hooks":["not-a-cidr"]}`, 200, "--from-json-url"},
		{"absent key", `{"web":["1.2.3.4/32"]}`, 200, `no key "hooks"`},
		{"empty list", `{"hooks":[]}`, 200, "is empty"},
		{"not a list", `{"hooks":"1.2.3.4/32"}`, 200, "not a list of strings"},
		{"not json", `<html>`, 200, "invalid character"},
		{"http error", `{}`, 503, "answered 503"},
		{"too many", `{"hooks":[` + strings.Join(many, ",") + `]}`, 200, "more than"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFake(t)
			url := metaServer(t, c.code, c.body)
			_, err := run(fromURLArgs(url)...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err=%v, want %q", err, c.want)
			}
			if len(ruleBodies(f)) != 0 || f.calls["POST /v3/auth/tokens"] != 0 {
				t.Errorf("acted before refusing: %v", f.created)
			}
		})
	}
}

func TestAllowFromJSONURLRejectsPlainHTTPAndBadFlags(t *testing.T) {
	newFake(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{cloudArg, "allow", "--group=default", "--from-json-url=http://api.github.com/meta", "--json-key=hooks", "--port=9997"}, "https"},
		{[]string{cloudArg, "allow", "--group=default", "--from-json-url=https://x.invalid/meta", "--port=9997"}, "needs --json-key"},
		{[]string{cloudArg, "allow", "--group=default", "--from=192.168.1.94/32", "--from-json-url=https://x.invalid/meta", "--json-key=hooks", "--port=9997"}, "exclusive"},
		{[]string{cloudArg, "allow", "--group=default", "--from-json-url=https://127.0.0.1:1/meta", "--json-key=hooks", "--port=9997"}, "--from-json-url"},
		{[]string{cloudArg, "allow", "--group=default", "--from-json-url=https://x y/meta", "--json-key=hooks", "--port=9997"}, "--from-json-url"},
	}
	for _, c := range cases {
		if _, err := run(c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: err=%v, want %q", c.args, err, c.want)
		}
	}
}
