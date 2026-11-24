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
	// a mutant that never sees "active" must fail within seconds, not hang
	// for the production 15 minutes.
	pe, at := pollEvery, activeTimeout
	pollEvery, activeTimeout = time.Millisecond, 5*time.Second
	t.Cleanup(func() { pollEvery, activeTimeout = pe, at })
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
	if _, err := run(imgArgs(base, filepath.Join(blocker, "sub"))...); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("a directory under a file: %v", err)
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "img.qcow2"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := run(imgArgs(base, dir)...); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("an unwritable destination: %v", err)
	}
	// the directory is refused before the image is requested at all
	bad := publisher(t, sha(imgBody)+"  img.qcow2\n", 404)
	if _, err := run(imgArgs(bad, filepath.Join(blocker, "sub"))...); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("a directory under a file, image absent: %v", err)
	}
	if _, err := run(cloudArg, "image-upload", "--url=https://x/%zz", "--sums-url=https://x/s", "--name=n", "--dir="+dir); err == nil || !strings.Contains(err.Error(), "escape") {
		t.Errorf("a malformed URL: %v", err)
	}
	if _, err := run(cloudArg, "image-upload", "--url=https://x y/i", "--sums-url=https://x/s", "--name=n", "--dir="+dir); err == nil || !strings.Contains(err.Error(), "invalid character") {
		t.Errorf("a URL with a space: %v", err)
	}
	// a sums URL the HTTP library refuses to turn into a request
	if _, err := run(cloudArg, "image-upload", "--url=https://x/i", "--sums-url=https://x/s\x7f", "--name=n", "--dir="+dir); err == nil || !strings.Contains(err.Error(), "control character") {
		t.Errorf("a control character in the sums URL: %v", err)
	}
}

func TestSumForRefusesAnOverlongLine(t *testing.T) {
	if _, err := sumFor(strings.NewReader(strings.Repeat("a", 1<<20)+"\n"), "img.qcow2"); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("err=%v", err)
	}
}

func TestImageUploadAndQuotaRefusedAuth(t *testing.T) {
	f := newFake(t)
	f.denyAuth = true
	base := publisher(t, sha(imgBody)+"  img.qcow2\n", 200)
	for _, args := range [][]string{imgArgs(base, t.TempDir()), {cloudArg, "quota"}} {
		if out, err := run(args...); err == nil || strings.Contains(out, "image created") {
			t.Errorf("%v: err=%v\n%s", args, err, out)
		}
	}
	if len(f.created) != 0 {
		t.Errorf("created after a refused login: %v", f.created)
	}
}

func TestImageUploadLosesTheVerifiedFile(t *testing.T) {
	newFake(t)
	base := publisher(t, sha(imgBody)+"  img.qcow2\n", 200)
	old := openImage
	openImage = func(string) (*os.File, error) { return nil, os.ErrNotExist }
	t.Cleanup(func() { openImage = old })
	if _, err := run(imgArgs(base, t.TempDir())...); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err=%v", err)
	}
}

func TestImageUploadValidation(t *testing.T) {
	newFake(t)
	ok := []string{cloudArg, "image-upload", "--url=https://h/i.qcow2", "--sums-url=https://h/SHA512SUMS", "--name=n", "--dir=" + t.TempDir()}
	cases := []struct {
		args []string
		want string
	}{
		{[]string{cloudArg, "image-upload"}, "are required"},
		{[]string{cloudArg, "image-upload", "--sums-url=https://h/s", "--name=n", "--dir=d"}, "are required"},
		{[]string{cloudArg, "image-upload", "--url=https://h/i", "--name=n", "--dir=d"}, "are required"},
		{[]string{cloudArg, "image-upload", "--url=https://h/i", "--sums-url=https://h/s", "--dir=d"}, "are required"},
		{[]string{cloudArg, "image-upload", "--url=https://h/i", "--sums-url=https://h/s", "--name=n"}, "are required"},
		{[]string{"image-upload", "--url=https://h/i", "--sums-url=https://h/s", "--name=n", "--dir=d"}, "--cloud is required"},
		{append(append([]string{}, ok...), "--disk-format=vmdk"), "qcow2 or raw"},
		{append(append([]string{}, ok...), "--min-disk=-1"), "must not be negative"},
		{[]string{cloudArg, "image-upload", "--url=http://h/i.qcow2", "--sums-url=https://h/s", "--name=n", "--dir=d"}, "https only"},
		{[]string{cloudArg, "image-upload", "--url=https://h/i.qcow2", "--sums-url=http://h/s", "--name=n", "--dir=d"}, "https only"},
	}
	for _, c := range cases {
		if _, err := run(c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: err=%v, want %q", c.args, err, c.want)
		}
	}
}

func TestImageUploadFromALocalFile(t *testing.T) {
	f := newFake(t)
	pe, at := pollEvery, activeTimeout
	pollEvery, activeTimeout = time.Millisecond, 5*time.Second
	t.Cleanup(func() { pollEvery, activeTimeout = pe, at })
	file := filepath.Join(t.TempDir(), "local.qcow2")
	if err := os.WriteFile(file, []byte(imgBody), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := run(cloudArg, "image-upload", "--file="+file, "--name=obsd-test", "--min-disk=20")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"local file: " + file, "sha512 " + sha(imgBody), "image active: obsd-test id=img1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if all := strings.Join(f.created, "\n"); !strings.Contains(all, imgBody) || !strings.Contains(all, `"min_disk":20`) {
		t.Errorf("requests:\n%s", all)
	}
}

func TestImageUploadFileRefusals(t *testing.T) {
	newFake(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{cloudArg, "image-upload", "--file=f", "--url=https://h/i"}, "excludes"},
		{[]string{cloudArg, "image-upload", "--file=f", "--dir=d"}, "excludes"},
		{[]string{cloudArg, "image-upload", "--file=f"}, "needs --name"},
		{[]string{cloudArg, "image-upload", "--file=" + filepath.Join(t.TempDir(), "absent"), "--name=n"}, "no such file"},
	}
	for _, c := range cases {
		if _, err := run(c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: err=%v, want %q", c.args, err, c.want)
		}
	}
}

func TestImageUploadLocalFileUnreadableOrOversized(t *testing.T) {
	f := newFake(t)
	pe, at := pollEvery, activeTimeout
	pollEvery, activeTimeout = time.Millisecond, 5*time.Second
	t.Cleanup(func() { pollEvery, activeTimeout = pe, at })
	// a directory opens but cannot be read: refused before any image is created
	if _, err := run(cloudArg, "image-upload", "--file="+t.TempDir(), "--name=n"); err == nil {
		t.Error("a directory was accepted as an image")
	}
	if len(f.created) != 0 {
		t.Errorf("an image was created from an unreadable file: %v", f.created)
	}
	file := filepath.Join(t.TempDir(), "big.qcow2")
	if err := os.WriteFile(file, []byte(imgBody), 0o600); err != nil {
		t.Fatal(err)
	}
	old := maxImageBytes
	maxImageBytes = int64(len(imgBody)) - 1
	t.Cleanup(func() { maxImageBytes = old })
	if _, err := run(cloudArg, "image-upload", "--file="+file, "--name=n"); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("oversized: %v", err)
	}
	maxImageBytes = int64(len(imgBody)) // exactly the limit is allowed
	if _, err := run(cloudArg, "image-upload", "--file="+file, "--name=n"); err != nil {
		t.Errorf("at the limit: %v", err)
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
	if _, err := run("quota"); err == nil || !strings.Contains(err.Error(), "--cloud is required") {
		t.Errorf("--cloud not required: %v", err)
	}
}
