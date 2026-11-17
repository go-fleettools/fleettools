package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// The defect of v0.131.0 (measured 2026-11-17 on the VM): inheritance was
// removed from the directory before it had an explicit entry, so the
// administrator was locked out and the next icacls on the file failed with
// Access is denied. These tests pin the ORDER of what is sent.

// vm models the one fact that mattered: once inheritance is removed from a path
// the administrator has no access to it, so any later script that touches it
// without recovering first fails.
func TestOrderOfTheACLSteps(t *testing.T) {
	c := config{host: "localhost", port: 2222, admin: "reader", user: "std"}
	w, l := c.write("ssh-ed25519 AAAA x").script, c.lock().script

	if strings.Contains(w, "/inheritance") {
		t.Error("the write step removes inheritance: the administrator would lose access before the result is checked")
	}
	grant, owner := strings.Index(w, "/grant:r"), strings.Index(w, "/setowner")
	if grant < 0 || owner < 0 {
		t.Fatalf("write step lacks the grant or the owner change:\n%s", w)
	}
	if i := strings.Index(w, "/reset"); i >= 0 && i > grant {
		t.Error("/reset comes after the explicit grants and would erase them")
	}
	if i := strings.Index(w, "/reset"); i >= 0 && i < strings.Index(w, "$denied) {") {
		t.Error("/reset runs outside the recovery branch")
	}
	// The ACL is read back inside the write step, before the lock exists.
	if strings.Index(w, "Get-Acl") < grant {
		t.Error("the ACL is read before it is set")
	}
	f, d := strings.Index(l, `authorized_keys' /inheritance:r`), strings.Index(l, `.ssh' /inheritance:r`)
	if f < 0 || d < 0 || f > d {
		t.Errorf("the lock must remove inheritance from the file first, then the directory:\n%s", l)
	}
	if strings.Contains(l, "/reset") || strings.Contains(l, "takeown") {
		t.Error("the lock step must only remove inheritance")
	}
}

func TestCallOrderAndLockOnlyAfterTheCheck(t *testing.T) {
	_, line := newKey(t)
	f := &fake{outs: []string{inspectOK, verifyOut("std", line), lockOut}}
	if err := run(context.Background(), cfg(t, line), f.run, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 3 {
		t.Fatalf("%d calls", len(f.calls))
	}
	for i, want := range []string{"Get-LocalUser", "[Console]::In.ReadToEnd()", "/inheritance:r"} {
		if s := f.calls[i][len(f.calls[i])-1]; !strings.Contains(s, want) {
			t.Errorf("call %d does not contain %q", i, want)
		}
	}
	if strings.Contains(f.calls[1][len(f.calls[1])-1], "/inheritance") {
		t.Error("call 2 (write) removes inheritance")
	}
}

func TestRecoveryIsReported(t *testing.T) {
	_, line := newKey(t)
	f := &fake{outs: []string{inspectOK + "KEYS|unreadable\r\n", "RECOVER|the administrator had no access to C:\\Users\\std\\.ssh; taking ownership back\r\n" + verifyOut("std", line), lockOut}}
	var out bytes.Buffer
	if err := run(context.Background(), cfg(t, line), f.run, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "recovered: the administrator had no access") {
		t.Errorf("recovery not reported:\n%s", out.String())
	}
}

func TestLockMustConfirmBothPaths(t *testing.T) {
	_, line := newKey(t)
	f := &fake{outs: []string{inspectOK, verifyOut("std", line), "LOCKED|one\n"}}
	if err := run(context.Background(), cfg(t, line), f.run, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "1 of 2") {
		t.Errorf("err = %v", err)
	}
}

func TestHostKeyHint(t *testing.T) {
	_, line := newKey(t)
	c := cfg(t, line)
	c.host = "127.0.0.1"
	f := &fake{err: errors.New("ssh: exit status 255: Host key verification failed.")}
	err := run(context.Background(), c, f.run, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "try --host localhost") {
		t.Errorf("err = %v", err)
	}
}

func TestDefaultHostIsLocalhost(t *testing.T) {
	cmd, _, err := RootCmd().Find([]string{"install"})
	if err != nil {
		t.Fatal(err)
	}
	if v := cmd.Flags().Lookup("host").DefValue; v != "localhost" {
		t.Errorf("default host = %q", v)
	}
}
