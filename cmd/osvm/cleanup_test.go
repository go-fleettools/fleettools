package main

import (
	"strings"
	"testing"
)

func TestVolumeDeleteDryRunThenConfirm(t *testing.T) {
	fastDelete(t)
	f := newFake(t)
	f.track = true
	out, err := run(cloudArg, "volume-delete", "--id=v1", "--name=vm-data")
	if err != nil || !strings.Contains(out, "plan: delete volume vm-data id=v1") || !strings.Contains(out, "dry run") {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if d := f.deletes(); len(d) != 0 {
		t.Fatalf("a dry run deleted %v", d)
	}
	out, err = run(cloudArg, "volume-delete", "--id=v1", "--name=vm-data", "--confirm")
	if err != nil || !strings.Contains(out, "volume deleted: vm-data id=v1") {
		t.Fatalf("confirm: %v\n%s", err, out)
	}
	if d := f.deletes(); len(d) != 1 || !strings.HasSuffix(d[0], "/volumes/v1") {
		t.Fatalf("deletes %v", d)
	}
}

func TestVolumeDeleteRefusals(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"no cloud":   {[]string{"volume-delete", "--id=v1", "--name=vm-data", "--confirm"}, "--cloud is required"},
		"no name":    {[]string{cloudArg, "volume-delete", "--id=v1", "--confirm"}, "--id and --name are required"},
		"wildcard":   {[]string{cloudArg, "volume-delete", "--id=v1", "--name=*", "--confirm"}, "no wildcard"},
		"wrong name": {[]string{cloudArg, "volume-delete", "--id=v1", "--name=other", "--confirm"}, `is named "vm-data", not "other"`},
		"unknown":    {[]string{cloudArg, "volume-delete", "--id=nope", "--name=x", "--confirm"}, "nope"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			_, err := run(c.args...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if d := f.deletes(); len(d) != 0 {
				t.Fatalf("a refusal deleted %v", d)
			}
		})
	}
}

func TestVolumeDeleteRefusesAnAttachedVolume(t *testing.T) {
	f := newFake(t)
	f.attachedVolume = true
	_, err := run(cloudArg, "volume-delete", "--id=v1", "--name=vm-data", "--confirm")
	if err == nil || !strings.Contains(err.Error(), "only an unattached volume is deleted") {
		t.Fatalf("err = %v", err)
	}
	if d := f.deletes(); len(d) != 0 {
		t.Fatalf("deleted %v", d)
	}
}

func TestImageDeleteDryRunThenConfirm(t *testing.T) {
	f := newFake(t)
	out, err := run(cloudArg, "image-delete", "--id=img1", "--name=new")
	if err != nil || !strings.Contains(out, "plan: delete image new id=img1") || !strings.Contains(out, "dry run") {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if d := f.deletes(); len(d) != 0 {
		t.Fatalf("a dry run deleted %v", d)
	}
	out, err = run(cloudArg, "image-delete", "--id=img1", "--name=new", "--confirm")
	if err != nil || !strings.Contains(out, "image deleted: new id=img1") {
		t.Fatalf("confirm: %v\n%s", err, out)
	}
	if d := f.deletes(); len(d) != 1 || !strings.HasSuffix(d[0], "/images/img1") {
		t.Fatalf("deletes %v", d)
	}
}

func TestImageDeleteRefusals(t *testing.T) {
	cases := map[string]struct {
		args       []string
		visibility string
		want       string
	}{
		"no cloud":   {[]string{"image-delete", "--id=img1", "--name=new", "--confirm"}, "private", "--cloud is required"},
		"no id":      {[]string{cloudArg, "image-delete", "--name=new", "--confirm"}, "private", "--id and --name are required"},
		"wildcard":   {[]string{cloudArg, "image-delete", "--id=img?", "--name=new", "--confirm"}, "private", "no wildcard"},
		"wrong name": {[]string{cloudArg, "image-delete", "--id=img1", "--name=old", "--confirm"}, "private", `is named "new", not "old"`},
		"public":     {[]string{cloudArg, "image-delete", "--id=img1", "--name=new", "--confirm"}, "public", "not private"},
		"unknown":    {[]string{cloudArg, "image-delete", "--id=nope", "--name=new", "--confirm"}, "private", "nope"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			f.imgVisibility = c.visibility
			_, err := run(c.args...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if d := f.deletes(); len(d) != 0 {
				t.Fatalf("a refusal deleted %v", d)
			}
		})
	}
}
