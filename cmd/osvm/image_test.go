package main

import (
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const imgBody = "QFI\xfb not really a qcow2"

func sha(s string) string { h := sha512.Sum512([]byte(s)); return hex.EncodeToString(h[:]) }

// publisher serves img.qcow2 and SHA512SUMS (sums body given) over TLS and
// points both HTTP clients at it.
func publisher(t *testing.T, sums string, imgCode int) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/img.qcow2", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(imgCode)
		fmt.Fprint(w, imgBody)
	})
	mux.HandleFunc("/SHA512SUMS", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, sums) })
	s := httptest.NewTLSServer(mux)
	t.Cleanup(s.Close)
	od, oh := downloadClient, httpClient
	downloadClient, httpClient = s.Client(), s.Client()
	t.Cleanup(func() { downloadClient, httpClient = od, oh })
	pe := pollEvery
	pollEvery = time.Millisecond
	t.Cleanup(func() { pollEvery = pe })
	return s.URL
}

func imgArgs(base, dir string, extra ...string) []string {
	return append([]string{cloudArg, "image-upload", "--url=" + base + "/img.qcow2", "--sums-url=" + base + "/SHA512SUMS",
		"--name=debian-13-test", "--dir=" + dir, "--min-disk=3"}, extra...)
}

func TestImageUploadHappy(t *testing.T) {
	f := newFake(t)
	base := publisher(t, sha(imgBody)+"  img.qcow2\n"+sha("x")+"  other.qcow2\n", 200)
	dir := filepath.Join(t.TempDir(), "d")
	out, err := run(imgArgs(base, dir)...)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"published sha512 of img.qcow2: " + sha(imgBody), "verified:", "image created: debian-13-test id=img1", "image active: debian-13-test id=img1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	all := strings.Join(f.created, "\n")
	for _, want := range []string{`"visibility":"private"`, `"disk_format":"qcow2"`, `"container_format":"bare"`, `"min_disk":3`, `"name":"debian-13-test"`, imgBody} {
		if !strings.Contains(all, want) {
			t.Errorf("requests lack %q:\n%s", want, all)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "img.qcow2")); err != nil || string(b) != imgBody {
		t.Errorf("kept file: %q %v", b, err)
	}
}

func TestImageUploadWaitsForActive(t *testing.T) {
	f := newFake(t)
	f.imgStates = []string{"saving", "importing", "active"}
	base := publisher(t, sha(imgBody)+" *img.qcow2\n", 200)
	out, err := run(imgArgs(base, t.TempDir())...)
	if err != nil || !strings.Contains(out, "image active") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestImageUploadEndsKilled(t *testing.T) {
	for _, st := range []string{"killed", "deleted"} {
		f := newFake(t)
		f.imgStates = []string{st}
		base := publisher(t, sha(imgBody)+"  img.qcow2\n", 200)
		out, err := run(imgArgs(base, t.TempDir())...)
		if err == nil || !strings.Contains(err.Error(), "ended "+st) || strings.Contains(out, "image active") {
			t.Errorf("%s: %v\n%s", st, err, out)
		}
	}
}

func TestImageUploadMismatchDeletesFileAndTouchesNoCloud(t *testing.T) {
	f := newFake(t)
	base := publisher(t, sha("other")+"  img.qcow2\n", 200)
	dir := t.TempDir()
	_, err := run(imgArgs(base, dir)...)
	if err == nil || !strings.Contains(err.Error(), "sha512 mismatch") {
		t.Fatalf("err=%v", err)
	}
	if _, serr := os.Stat(filepath.Join(dir, "img.qcow2")); serr == nil {
		t.Error("the mismatching file was kept")
	}
	if f.calls["POST /v3/auth/tokens"] != 0 || len(f.created) != 0 {
		t.Errorf("the cloud was touched: %v", f.created)
	}
}

func TestImageUploadSumsRefusals(t *testing.T) {
	cases := []struct{ name, sums, want string }{
		{"unlisted", sha(imgBody) + "  other.qcow2\n", "not listed"},
		{"short sum", "abc  img.qcow2\n", "not a SHA-512"},
		{"empty", "", "not listed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			newFake(t)
			base := publisher(t, c.sums, 200)
			if _, err := run(imgArgs(base, t.TempDir())...); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestImageUploadHTTPFailures(t *testing.T) {
	newFake(t)
	base := publisher(t, sha(imgBody)+"  img.qcow2\n", 404)
	if _, err := run(imgArgs(base, t.TempDir())...); err == nil || !strings.Contains(err.Error(), "answered 404") {
		t.Fatalf("image 404: %v", err)
	}
	if _, err := run(append(imgArgs(base, t.TempDir()), "--sums-url="+base+"/nope")...); err == nil || !strings.Contains(err.Error(), "sums") {
		t.Fatalf("sums 404: %v", err)
	}
	dead := httptest.NewTLSServer(http.NotFoundHandler())
	dead.Close()
	if _, err := run(append(imgArgs(base, t.TempDir()), "--sums-url="+dead.URL+"/SHA512SUMS")...); err == nil {
		t.Fatal("unreachable sums accepted")
	}
}

func TestImageUploadDryRunUploadsNothing(t *testing.T) {
	f := newFake(t)
	base := publisher(t, sha(imgBody)+"  img.qcow2\n", 200)
	out, err := run(imgArgs(base, t.TempDir(), "--dry-run")...)
	if err != nil || !strings.Contains(out, "verified:") {
		t.Fatalf("%v\n%s", err, out)
	}
	if f.calls["POST /v3/auth/tokens"] != 0 {
		t.Error("dry run authenticated")
	}
}

func TestImageUploadRefusesAnExistingName(t *testing.T) {
	f := newFake(t)
	base := publisher(t, sha(imgBody)+"  img.qcow2\n", 200)
	_, err := run(append(imgArgs(base, t.TempDir()), "--name=debian-13-genericcloud")...)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err=%v", err)
	}
	if len(f.created) != 0 {
		t.Errorf("created: %v", f.created)
	}
}

func TestImageUploadCallFailures(t *testing.T) {
	cases := []struct{ key, want string }{
		{"GET /image/v2/images", "500"}, {"POST /image/v2/images", "500"},
		{"PUT /image/v2/images/img1/file", "upload"}, {"GET /image/v2/images/img1", "image status"},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			f := newFake(t)
			f.fail[c.key] = 0
			base := publisher(t, sha(imgBody)+"  img.qcow2\n", 200)
			out, err := run(imgArgs(base, t.TempDir())...)
			if err == nil || !strings.Contains(err.Error(), c.want) || strings.Contains(out, "image active") {
				t.Fatalf("err=%v\n%s", err, out)
			}
		})
	}
}

func TestImageUploadNeverActiveTimesOut(t *testing.T) {
	f := newFake(t)
	f.imgStates = []string{"saving"}
	base := publisher(t, sha(imgBody)+"  img.qcow2\n", 200)
	old := activeTimeout
	activeTimeout = 20 * time.Millisecond
	t.Cleanup(func() { activeTimeout = old })
	if _, err := run(imgArgs(base, t.TempDir())...); err == nil {
		t.Fatal("waited forever")
	}
}

func TestImageUploadOversizedIsRefused(t *testing.T) {
	newFake(t)
	base := publisher(t, sha(imgBody)+"  img.qcow2\n", 200)
	old := maxImageBytes
	maxImageBytes = 4
	t.Cleanup(func() { maxImageBytes = old })
	dir := t.TempDir()
	if _, err := run(imgArgs(base, dir)...); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("err=%v", err)
	}
	if _, serr := os.Stat(filepath.Join(dir, "img.qcow2")); serr == nil {
		t.Error("oversized file kept")
	}
}

func TestImageUploadLocalFailures(t *testing.T) {
	newFake(t)
	base := publisher(t, sha(imgBody)+"  img.qcow2\n", 200)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(imgArgs(base, filepath.Join(blocker, "sub"))...); err == nil {
		t.Error("a directory under a file accepted")
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "img.qcow2"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := run(imgArgs(base, dir)...); err == nil {
		t.Error("an unwritable destination accepted")
	}
	if _, err := run(cloudArg, "image-upload", "--url=https://x/%zz", "--sums-url=https://x/s", "--name=n", "--dir="+dir); err == nil {
		t.Error("a malformed URL accepted")
	}
	if _, err := run(cloudArg, "image-upload", "--url=https://x y/i", "--sums-url=https://x/s", "--name=n", "--dir="+dir); err == nil {
		t.Error("a URL with a space accepted")
	}
}

func TestImageUploadValidation(t *testing.T) {
	newFake(t)
	ok := []string{cloudArg, "image-upload", "--url=https://h/i.qcow2", "--sums-url=https://h/SHA512SUMS", "--name=n", "--dir=" + t.TempDir()}
	cases := [][]string{
		{cloudArg, "image-upload"},
		{"image-upload", "--url=https://h/i", "--sums-url=https://h/s", "--name=n", "--dir=d"},
		append(append([]string{}, ok...), "--disk-format=vmdk"),
		append(append([]string{}, ok...), "--min-disk=-1"),
		{cloudArg, "image-upload", "--url=http://h/i.qcow2", "--sums-url=https://h/s", "--name=n", "--dir=d"},
		{cloudArg, "image-upload", "--url=https://h/i.qcow2", "--sums-url=http://h/s", "--name=n", "--dir=d"},
	}
	for _, args := range cases {
		if _, err := run(args...); err == nil {
			t.Errorf("accepted: %v", args)
		}
	}
}

func TestQuotaListsComputeAndVolume(t *testing.T) {
	newFake(t)
	out, err := run(cloudArg, "quota")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"compute instances in_use=1 reserved=0 limit=10", "compute cores in_use=4 reserved=0 limit=64", "compute ram_mb in_use=8192 reserved=0 limit=131072", "volume volumes in_use=0 reserved=0 limit=10", "volume gigabytes in_use=0 reserved=0 limit=1000"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestQuotaWithoutBlockStorageAndFailures(t *testing.T) {
	f := newFake(t)
	f.services = []string{"compute", "network", "image", "identity"}
	out, err := run(cloudArg, "quota")
	if err != nil || !strings.Contains(out, "no block storage") {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, k := range []string{"GET /compute/v2.1/os-quota-sets/p1/detail", "GET /volume/v3/p1/os-quota-sets/p1"} {
		f := newFake(t)
		f.fail[k] = 0
		if _, err := run(cloudArg, "quota"); err == nil || !strings.Contains(err.Error(), "500") {
			t.Errorf("%s: %v", k, err)
		}
	}
	if _, err := run("quota"); err == nil {
		t.Error("--cloud not required")
	}
}
