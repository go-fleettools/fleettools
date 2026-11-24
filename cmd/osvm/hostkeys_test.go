package main

import (
	"reflect"
	"strings"
	"testing"
)

const goodConsole = "boot noise\r\n" +
	"-----BEGIN SSH HOST KEY KEYS-----\r\n" +
	"ecdsa-sha2-nistp256 AAAAold root@vm\r\n" +
	"-----END SSH HOST KEY KEYS-----\r\n" +
	"reboot\r\n" +
	"-----BEGIN SSH HOST KEY KEYS-----\r\n" +
	"ecdsa-sha2-nistp256 AAAAe root@vm\r\n" +
	"\r\n" +
	"ssh-ed25519 AAAAf root@vm\r\n" +
	"-----END SSH HOST KEY KEYS-----\r\n"

func TestParseHostKeys(t *testing.T) {
	got, err := parseHostKeys(goodConsole)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ecdsa-sha2-nistp256 AAAAe", "ssh-ed25519 AAAAf"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q (the LAST block, comment dropped)", got, want)
	}
}

func TestParseHostKeysRefusals(t *testing.T) {
	for name, c := range map[string]struct{ in, want string }{
		"none":      {"just boot noise\n", "no SSH HOST KEY KEYS block"},
		"unclosed":  {hostKeysBegin + "\nssh-ed25519 AAAA x\n", "never closed"},
		"empty":     {hostKeysBegin + "\n\n" + hostKeysEnd + "\n", "is empty"},
		"not a key": {hostKeysBegin + "\nrm -rf /\n" + hostKeysEnd + "\n", "not a host key line"},
		"one field": {hostKeysBegin + "\nssh-ed25519\n" + hostKeysEnd + "\n", "not a host key line"},
		"end first": {hostKeysEnd + "\n" + hostKeysBegin + "\nssh-ed25519 AAAA x\n", "never closed"},
		"reopened":  {hostKeysBegin + "\nssh-ed25519 AAAA x\n" + hostKeysEnd + "\n" + hostKeysBegin + "\n", "never closed"},
	} {
		_, err := parseHostKeys(c.in)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to contain %q", name, err, c.want)
		}
	}
}

func TestHostKeysCommand(t *testing.T) {
	f := newFake(t)
	f.console = goodConsole
	out, err := run(cloudArg, "hostkeys", "--server=vm", "--host=203.0.113.9")
	if err != nil {
		t.Fatal(err)
	}
	want := "203.0.113.9 ecdsa-sha2-nistp256 AAAAe\n203.0.113.9 ssh-ed25519 AAAAf\n"
	if out != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

func TestHostKeysCommandRefusals(t *testing.T) {
	f := newFake(t)
	f.console = "nothing yet"
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"no cloud":     {[]string{"hostkeys", "--server=vm", "--host=h"}, "--cloud is required"},
		"no server":    {[]string{cloudArg, "hostkeys", "--host=h"}, "--server and --host are required"},
		"no host":      {[]string{cloudArg, "hostkeys", "--server=vm"}, "--server and --host are required"},
		"bad host":     {[]string{cloudArg, "hostkeys", "--server=vm", "--host=a b"}, "one address or name"},
		"comma host":   {[]string{cloudArg, "hostkeys", "--server=vm", "--host=a,b"}, "one address or name"},
		"hash host":    {[]string{cloudArg, "hostkeys", "--server=vm", "--host=a#b"}, "one address or name"},
		"unknown vm":   {[]string{cloudArg, "hostkeys", "--server=nope", "--host=h"}, "want exactly one, got 0"},
		"no key block": {[]string{cloudArg, "hostkeys", "--server=vm", "--host=h"}, "no SSH HOST KEY KEYS block"},
	} {
		_, err := run(c.args...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to contain %q", name, err, c.want)
		}
	}
	f.fail["GET /compute/v2.1/servers/detail"] = 0
	if _, err := run(cloudArg, "hostkeys", "--server=vm", "--host=h"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("server list failure: err = %v", err)
	}
	delete(f.fail, "GET /compute/v2.1/servers/detail")
	f.fail["POST /compute/v2.1/servers/s1/action"] = 0
	if _, err := run(cloudArg, "hostkeys", "--server=vm", "--host=h"); err == nil || !strings.Contains(err.Error(), "console log of vm") {
		t.Errorf("api failure: err = %v", err)
	}
}

func TestHostKeysRefusedAuth(t *testing.T) {
	f := newFake(t)
	f.denyAuth = true
	if _, err := run(cloudArg, "hostkeys", "--server=vm", "--host=h"); err == nil {
		t.Fatal("a denied login must fail the command")
	}
}
