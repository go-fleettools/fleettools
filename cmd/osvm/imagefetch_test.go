package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	helloSHA    = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	recordedImg = `{"images":[{"id":"i1","name":"debian-13-genericcloud","status":"active","osvm_sha256":"` + helloSHA + `"}]}`
)

func TestImageDownloadVerifiesAgainstTheRecordedSum(t *testing.T) {
	f := newFake(t)
	f.images = recordedImg
	out := filepath.Join(t.TempDir(), "x.iso")
	got, err := run(cloudArg, "image-download", "--name=debian-13-genericcloud", "--out="+out)
	if err != nil || !strings.Contains(got, "sha256 verified") {
		t.Fatalf("%v\n%s", err, got)
	}
	if b, _ := os.ReadFile(out); string(b) != "hello" {
		t.Fatalf("file holds %q", b)
	}
	if _, err := run(cloudArg, "image-download", "--name=debian-13-genericcloud", "--out="+out); err == nil {
		t.Fatal("an existing output was overwritten")
	}
}

func TestImageDownloadRefusals(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]struct {
		images string
		args   []string
		want   string
	}{
		"no cloud":      {recordedImg, []string{"image-download", "--name=n", "--out=" + dir + "/a"}, "--cloud is required"},
		"no out":        {recordedImg, []string{cloudArg, "image-download", "--name=n"}, "--name and --out are required"},
		"wildcard":      {recordedImg, []string{cloudArg, "image-download", "--name=*", "--out=" + dir + "/b"}, "no wildcard"},
		"no such image": {recordedImg, []string{cloudArg, "image-download", "--name=nope", "--out=" + dir + "/c"}, "found 0"},
		"no sum":        {imagesOK, []string{cloudArg, "image-download", "--name=debian-13-genericcloud", "--out=" + dir + "/d"}, "records no osvm_sha256"},
		"wrong sum":     {recordedImg, []string{cloudArg, "image-download", "--name=debian-13-genericcloud", "--sha256=00", "--out=" + dir + "/e"}, "is not the expected 00"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			f.images = c.images
			_, err := run(c.args...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if _, err := os.Stat(dir + "/e"); name == "wrong sum" && err == nil {
				t.Fatal("a file that failed its checksum was kept")
			}
		})
	}
}
