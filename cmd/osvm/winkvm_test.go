package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func goodKVM() kvmOpts {
	return kvmOpts{ISO: "w.iso", Out: "o.qcow2", Edition: "Windows 11 Pro", CPUs: 4, MemoryMB: 6144, DiskGB: 64, Timeout: time.Hour}
}

func TestKVMValidate(t *testing.T) {
	if err := goodKVM().validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*kvmOpts){
		"--iso is required":         func(o *kvmOpts) { o.ISO = "" },
		"--out is required":         func(o *kvmOpts) { o.Out = "" },
		"--edition must not":        func(o *kvmOpts) { o.Edition = "" },
		"--cpus must be at least 1": func(o *kvmOpts) { o.CPUs = 0 },
		"--memory-mb at least 4096": func(o *kvmOpts) { o.MemoryMB = 4095 },
		"--disk-gb at least 40":     func(o *kvmOpts) { o.DiskGB = 39 },
		"--timeout must be":         func(o *kvmOpts) { o.Timeout = 0 },
	}
	for want, mut := range cases {
		o := goodKVM()
		mut(&o)
		if err := o.validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", want, err)
		}
	}
}

func TestQemuArgsLayout(t *testing.T) {
	p := kvmPaths{root: "r.qcow2", boot: "b.img", answer: "a.iso", vars: "v.fd", code: "c.fd", console: "c.log", iso: "w.iso"}
	a := strings.Join(qemuArgs(goodKVM(), p), " ")
	for _, want := range []string{
		"accel=kvm", "-cpu host", "-m 6144", "-smp 4", "-serial file:c.log",
		"readonly=on,file=c.fd", "if=pflash,format=raw,file=v.fd",
		"file=r.qcow2 -device ide-hd,drive=root,bus=ahci.0,bootindex=1",
		"file=b.img -device ide-hd,drive=boot,bus=ahci.1,bootindex=2",
		"file=w.iso -device ide-cd,drive=iso,bus=ahci.2", "file=a.iso -device ide-cd,drive=answer,bus=ahci.3",
		"e1000e,netdev=n0",
	} {
		if !strings.Contains(a, want) {
			t.Errorf("missing %q in %s", want, a)
		}
	}
	if strings.Contains(a, "virtio") {
		t.Errorf("a virtio CD without --virtio-iso: %s", a)
	}
	p.virtio = "v.iso"
	if a := strings.Join(qemuArgs(goodKVM(), p), " "); !strings.Contains(a, "file=v.iso -device ide-cd,drive=virtio,bus=ahci.4") {
		t.Errorf("no virtio CD: %s", a)
	}
	o := goodKVM()
	o.NoKVM = true
	if a := strings.Join(qemuArgs(o, p), " "); !strings.Contains(a, "accel=tcg") || strings.Contains(a, "-cpu host") {
		t.Errorf("--no-kvm: %s", a)
	}
}

func TestConsoleProgress(t *testing.T) {
	seen := map[string]bool{}
	var w bytes.Buffer
	m, f := consoleProgress("boot\r\nOSVM: one\r\nnoise\nOSVM: two\n", seen, &w)
	if m || f != "" || w.String() != "guest: one\nguest: two\n" {
		t.Fatalf("m=%v f=%q out=%q", m, f, w.String())
	}
	w.Reset()
	m, f = consoleProgress("OSVM: one\nOSVM: three\nOSVM: FAILED: boom\nOSVM: "+buildMarker+"\n", seen, &w)
	if !m || f != "OSVM: FAILED: boom" || w.String() != "guest: three\nguest: FAILED: boom\nguest: "+buildMarker+"\n" {
		t.Fatalf("m=%v f=%q out=%q", m, f, w.String())
	}
}

// fakeQemu writes a shell script that appends console to the file named by
// "-serial file:<path>" and exits with code, standing in for qemu-system.
func fakeQemu(t *testing.T, console string, code int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stand-in for qemu")
	}
	dir := t.TempDir()
	body := "#!/bin/sh\nwhile [ $# -gt 0 ]; do case \"$1\" in file:*) f=${1#file:};; esac; shift; done\nprintf '%s' \"$CONSOLE\" >> \"$f\"\nexit " + string(rune('0'+code)) + "\n"
	p := filepath.Join(dir, "qemu")
	if err := os.WriteFile(p, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONSOLE", console)
	return p
}

func TestRunQemuVerdicts(t *testing.T) {
	cases := map[string]struct {
		console string
		code    int
		want    string // "" = success
	}{
		"marker then poweroff": {"OSVM: step\nOSVM: " + buildMarker + "\n", 0, ""},
		"poweroff no marker":   {"OSVM: step\n", 0, "powered off without " + buildMarker},
		"failed line":          {"OSVM: FAILED: git\n", 0, "reported a failure: OSVM: FAILED: git"},
		"qemu error":           {"OSVM: " + buildMarker + "\n", 1, "qemu ended"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			o := goodKVM()
			o.Qemu = fakeQemu(t, c.console, c.code)
			p := kvmPaths{console: filepath.Join(t.TempDir(), "console.log")}
			var w bytes.Buffer
			err := runQemu(context.Background(), o, p, &w)
			switch {
			case c.want == "" && err != nil:
				t.Fatal(err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestKVMDryRunPrintsAndRunsNothing(t *testing.T) {
	out, err := run("image-build-windows-kvm", "--iso=w.iso", "--out=o.qcow2", "--dry-run", "--virtio-iso=v.iso")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "qemu-system-x86_64 -machine q35,accel=kvm") || !strings.Contains(out, "<unattend") || strings.Contains(out, "\r") {
		t.Fatalf("dry run:\n%s", out)
	}
	if _, err := os.Stat("o.qcow2"); err == nil {
		t.Fatal("a dry run wrote the output")
	}
}

func TestKVMRefusals(t *testing.T) {
	existing := filepath.Join(t.TempDir(), "o.qcow2")
	if err := os.WriteFile(existing, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--out=x"}, "--iso is required"},
		{[]string{"--iso=w.iso"}, "--out is required"},
		{[]string{"--iso=w.iso", "--out=x", "--upload"}, "--upload needs --cloud"},
		{[]string{"--iso=w.iso", "--out=" + existing, "--no-kvm"}, "already exists"},
		{[]string{"--iso=" + existing, "--iso-sha256=00", "--out=" + existing + ".new", "--no-kvm"}, "is not the expected"},
	} {
		_, err := run(append([]string{"image-build-windows-kvm"}, c.args...)...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: err = %v, want %q", c.args, err, c.want)
		}
	}
}

func TestFirstExisting(t *testing.T) {
	a := filepath.Join(t.TempDir(), "a.fd")
	if err := os.WriteFile(a, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := firstExisting("", []string{"/nonexistent/x", a}); err != nil || got != a {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, err := firstExisting(a, nil); err != nil || got != a {
		t.Fatalf("given: %q, %v", got, err)
	}
	if _, err := firstExisting("/nonexistent/y", []string{a}); err == nil {
		t.Fatal("a missing given path was accepted")
	}
	if _, err := firstExisting("", []string{"/nonexistent/x"}); err == nil || !slices.Contains(strings.Fields(err.Error()), "/nonexistent/x,") && !strings.Contains(err.Error(), "install ovmf") {
		t.Fatalf("err = %v", err)
	}
}
