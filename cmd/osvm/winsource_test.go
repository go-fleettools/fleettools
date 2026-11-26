package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeSource(t *testing.T, body string) (string, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "src.iso")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	sum, _, err := hashFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return p, sum
}

func TestEnsureSourceImageUploadsANewImage(t *testing.T) {
	fastPolls(t)
	f := newFake(t)
	curT = t
	p, sum := writeSource(t, "installer")
	c, err := connect(t.Context(), "test")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	id, err := ensureSourceImage(t.Context(), c, sourceImage{Name: "brand-new", Path: p, Format: "iso", Props: map[string]string{"hw_machine_type": "q35"}}, sum, &out)
	if err != nil || id != "img1" {
		t.Fatalf("id=%q err=%v\n%s", id, err, out.String())
	}
	create := has(f, "POST /image/v2/images ")
	for _, want := range []string{`"osvm_sha256":"` + sum + `"`, `"hw_machine_type":"q35"`, `"disk_format":"iso"`, `"visibility":"private"`} {
		if !strings.Contains(create, want) {
			t.Errorf("create request lacks %s: %s", want, create)
		}
	}
	if has(f, "PUT /image/v2/images/img1/file") == "" {
		t.Error("nothing was uploaded")
	}
}

func TestEnsureSourceImageRefusesAWrongExpectedSHA(t *testing.T) {
	f := newFake(t)
	curT = t
	p, _ := writeSource(t, "installer")
	c, err := connect(t.Context(), "test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = ensureSourceImage(t.Context(), c, sourceImage{Name: "brand-new", Path: p, Format: "iso"}, strings.Repeat("0", 64), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not the expected") {
		t.Fatalf("err = %v", err)
	}
	if has(f, "POST /image/v2/images ") != "" {
		t.Error("an image was created from the wrong file")
	}
}

func TestEnsureSourceImageReusesOnlyAMatchingChecksum(t *testing.T) {
	f := newFake(t)
	curT = t
	p, sum := writeSource(t, "installer")
	c, err := connect(t.Context(), "test")
	if err != nil {
		t.Fatal(err)
	}
	// the fake's only image is named debian-13-genericcloud
	f.images = `{"images":[{"id":"i1","name":"debian-13-genericcloud","status":"active","osvm_sha256":"` + sum + `"}]}`
	var out bytes.Buffer
	id, err := ensureSourceImage(t.Context(), c, sourceImage{Name: "debian-13-genericcloud", Path: p, Format: "iso"}, "", &out)
	if err != nil || id != "i1" || !strings.Contains(out.String(), "image reused") {
		t.Fatalf("id=%q err=%v\n%s", id, err, out.String())
	}
	if has(f, "POST /image/v2/images ") != "" {
		t.Error("a reusable image was uploaded again")
	}

	f.images = `{"images":[{"id":"i1","name":"debian-13-genericcloud","status":"active","osvm_sha256":"other"}]}`
	if _, err := ensureSourceImage(t.Context(), c, sourceImage{Name: "debian-13-genericcloud", Path: p, Format: "iso"}, "", &out); err == nil || !strings.Contains(err.Error(), "records sha256") {
		t.Fatalf("different checksum: err = %v", err)
	}

	f.images = `{"images":[{"id":"i1","name":"debian-13-genericcloud","status":"queued","osvm_sha256":"` + sum + `"}]}`
	if _, err := ensureSourceImage(t.Context(), c, sourceImage{Name: "debian-13-genericcloud", Path: p, Format: "iso"}, "", &out); err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("queued image: err = %v", err)
	}

	f.images = `{"images":[{"id":"i1","name":"debian-13-genericcloud","status":"active"},{"id":"i2","name":"debian-13-genericcloud","status":"active"}]}`
	if _, err := ensureSourceImage(t.Context(), c, sourceImage{Name: "debian-13-genericcloud", Path: p, Format: "iso"}, "", &out); err == nil || !strings.Contains(err.Error(), "refusing to pick") {
		t.Fatalf("two images: err = %v", err)
	}
}

func TestProgressReaderPrintsEachDecile(t *testing.T) {
	var out bytes.Buffer
	pr := &progressReader{r: strings.NewReader(strings.Repeat("x", 100)), total: 100, w: &out, start: time.Now()}
	buf := make([]byte, 10)
	for {
		if _, err := pr.Read(buf); err != nil {
			break
		}
	}
	if n := strings.Count(out.String(), "upload "); n != 10 {
		t.Fatalf("%d progress lines:\n%s", n, out.String())
	}
}

func TestHashFileMissing(t *testing.T) {
	if _, _, err := hashFile(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("no error for a missing file")
	}
}

func TestWatchWindowsBuild(t *testing.T) {
	for name, tc := range map[string]struct {
		status, console, want string
	}{
		"marker then poweroff": {"SHUTOFF", "x\nOSVM: step 1\nOSVM: " + buildMarker + "\n", ""},
		"poweroff no marker":   {"SHUTOFF", "OSVM: step 1\n", "powered off without"},
		"guest failure":        {"ACTIVE", "OSVM: FAILED: sysprep\n", "guest reported a failure"},
		"error status":         {"ERROR", "", "ERROR"},
	} {
		fastPolls(t)
		f := newFake(t)
		curT = t
		f.srvStatus = tc.status
		f.console = tc.console
		c, err := connect(t.Context(), "test")
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		err = watchWindowsBuild(t.Context(), c, "s1", &out)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: err = %v", name, err)
		}
		if tc.want == "" && !strings.Contains(out.String(), "guest: step 1") {
			t.Errorf("%s: guest lines not relayed:\n%s", name, out.String())
		}
	}
}
