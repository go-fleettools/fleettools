package main

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// monitorServer listens on a short unix socket and sends every line it reads on
// the returned channel.
func monitorServer(t *testing.T) (string, <-chan string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "osvm-t")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "m.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("no unix sockets:", err)
	}
	t.Cleanup(func() { l.Close() })
	lines := make(chan string, 16)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 1024)
			n, _ := c.Read(buf)
			lines <- strings.TrimSpace(string(buf[:n]))
			c.Close()
		}
	}()
	return sock, lines
}

func TestMonitorCommandErrors(t *testing.T) {
	if err := monitorCommand(filepath.Join(t.TempDir(), "none.sock"), "x", 0); err == nil {
		t.Fatal("a missing socket was accepted")
	}
	// a peer that hangs up at once: the write fails for at least one attempt
	dir, err := os.MkdirTemp("", "osvm-t")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "m.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("no unix sockets:", err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	for i := 0; i < 500; i++ {
		if monitorCommand(sock, "x", 0) != nil {
			return
		}
	}
	t.Fatal("writing to a closed monitor never failed")
}

func TestSnapshotNeedsBothMonitorAndScreens(t *testing.T) {
	sock, lines := monitorServer(t)
	screens := t.TempDir()
	root := filepath.Join(t.TempDir(), "root.qcow2")
	if err := os.WriteFile(root, make([]byte, 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	// no screens directory, or no monitor: no screenshot is asked for
	for name, p := range map[string]kvmPaths{
		"no screens": {root: root, monitor: sock},
		"no monitor": {root: root, screens: screens},
	} {
		var w bytes.Buffer
		snapshot(p, 1, &w)
		if !strings.Contains(w.String(), "disk: 2 MiB allocated") || strings.Contains(w.String(), "screenshot") {
			t.Errorf("%s: %q", name, w.String())
		}
	}
	select {
	case l := <-lines:
		t.Fatalf("the monitor was asked %q", l)
	default:
	}
	// both: one screendump, named by its number
	var w bytes.Buffer
	snapshot(kvmPaths{root: root, monitor: sock, screens: screens}, 7, &w)
	want := filepath.Join(screens, "screen-007.png")
	if !strings.Contains(w.String(), "screenshot: "+want) {
		t.Fatalf("output %q", w.String())
	}
	select {
	case l := <-lines:
		if !strings.HasPrefix(l, "screendump "+want) {
			t.Fatalf("monitor line %q", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no screendump reached the monitor")
	}
	// a dead monitor is reported, not hidden, and names no file
	w.Reset()
	snapshot(kvmPaths{root: root, monitor: filepath.Join(t.TempDir(), "none.sock"), screens: screens}, 8, &w)
	if !strings.Contains(w.String(), "screenshot: ") || strings.Contains(w.String(), "screen-008.png") {
		t.Fatalf("output %q", w.String())
	}
}

func TestWriteAnswerISOFailure(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "missing", "answer.iso")
	if err := writeAnswerISO(bad, "Windows 11 Pro", &bytes.Buffer{}); err == nil {
		t.Fatal("an unwritable path was accepted")
	}
}

// TestBuildRefusesBeforeQemu: each early failure of the build is returned, in
// order, before anything is started.
func TestBuildRefusesBeforeQemu(t *testing.T) {
	base := func(t *testing.T) kvmOpts {
		d := t.TempDir()
		for _, n := range []string{"code.fd", "vars.fd"} {
			if err := os.WriteFile(filepath.Join(d, n), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return kvmOpts{
			ISO: "w.iso", Out: filepath.Join(d, "o.qcow2"), Edition: "Windows 11 Pro", NoKVM: true, Timeout: time.Minute,
			OVMFCode: filepath.Join(d, "code.fd"), OVMFVars: filepath.Join(d, "vars.fd"), WorkDir: d,
			SevenZip: "/nonexistent/7z", CPUs: 1, MemoryMB: 4096, DiskGB: 40,
		}
	}
	// the edition check is made, and its refusal returned
	if err := imageBuildWindowsKVM(t.Context(), "", base(t), &bytes.Buffer{}); err != errNoSevenZip {
		t.Fatalf("err = %v, want %v", err, errNoSevenZip)
	}
	// a screenshot directory that cannot be made
	o := base(t)
	file := filepath.Join(o.WorkDir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	o.ScreenDir = filepath.Join(file, "screens")
	if err := imageBuildWindowsKVM(t.Context(), "", o, &bytes.Buffer{}); err == nil || err == errNoSevenZip {
		t.Fatalf("screenshot dir: err = %v", err)
	}
	// a temp directory for the monitor socket that cannot be made
	o = base(t)
	t.Setenv("TMPDIR", filepath.Join(o.WorkDir, "no-such-dir"))
	if err := imageBuildWindowsKVM(t.Context(), "", o, &bytes.Buffer{}); err == nil || err == errNoSevenZip {
		t.Fatalf("monitor dir: err = %v", err)
	}
}
