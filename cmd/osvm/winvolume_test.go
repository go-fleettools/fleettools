package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestVolumeFromImageCreatesAndWaits(t *testing.T) {
	f := newFake(t)
	c, err := connect(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	var w bytes.Buffer
	id, err := volumeFromImage(context.Background(), c, "x-iso", "img1", 12, &w)
	if err != nil || id != "v1" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	all := strings.Join(f.created, "\n")
	for _, want := range []string{`"imageRef":"img1"`, `"size":12`, `"name":"x-iso"`} {
		if !strings.Contains(all, want) {
			t.Errorf("request lacks %s:\n%s", want, all)
		}
	}
	if !strings.Contains(w.String(), "available") {
		t.Errorf("no availability line:\n%s", w.String())
	}
}

func TestVolumeFromImageCreateFailure(t *testing.T) {
	f := newFake(t)
	f.fail["POST /volume/v3/p1/volumes"] = 0
	c, err := connect(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	id, err := volumeFromImage(context.Background(), c, "x", "img1", 1, &bytes.Buffer{})
	if err == nil || id != "" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestVolumeFromImageWaitFailureStillReturnsTheID(t *testing.T) {
	f := newFake(t)
	f.fail["GET /volume/v3/p1/volumes/v1"] = 0
	c, err := connect(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	id, err := volumeFromImage(context.Background(), c, "x", "img1", 1, &bytes.Buffer{})
	if err == nil || id != "v1" {
		t.Fatalf("the caller needs the id to delete it: id=%q err=%v", id, err)
	}
}
