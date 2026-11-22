package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
)

func TestCheckCIDR(t *testing.T) {
	for _, bad := range []string{"", "0.0.0.0/0", "::/0", "nonsense"} {
		if checkCIDR(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := checkCIDR("192.168.1.0/24"); err != nil {
		t.Error(err)
	}
}

func TestPickFlavorSmallestThatFits(t *testing.T) {
	all := []flavors.Flavor{
		{Name: "big", RAM: 8192, VCPUs: 4, Disk: 40},
		{Name: "tiny", RAM: 512, VCPUs: 1, Disk: 5},
		{Name: "small", RAM: 2048, VCPUs: 1, Disk: 10},
		{Name: "small2", RAM: 2048, VCPUs: 2, Disk: 10},
	}
	f, err := pickFlavor(all, "", 1024, 8)
	if err != nil || f.Name != "small" {
		t.Fatalf("got %v %v", f.Name, err)
	}
	if _, err := pickFlavor(all, "", 99999, 8); err == nil {
		t.Error("expected no fit")
	}
	if f, _ := pickFlavor(all, "big", 0, 0); f.Name != "big" {
		t.Error("named flavor ignored")
	}
	if _, err := pickFlavor(all, "nope", 0, 0); err == nil {
		t.Error("missing named flavor accepted")
	}
}

func TestPickImageNewestActive(t *testing.T) {
	now := time.Now()
	all := []images.Image{
		{Name: "Debian-13-old", Status: "active", CreatedAt: now.Add(-48 * time.Hour)},
		{Name: "debian-13-new", Status: "active", CreatedAt: now},
		{Name: "debian-13-queued", Status: "queued", CreatedAt: now.Add(time.Hour)},
		{Name: "ubuntu", Status: "active", CreatedAt: now.Add(time.Hour)},
	}
	im, err := pickImage(all, "DEBIAN-13")
	if err != nil || im.Name != "debian-13-new" {
		t.Fatalf("got %v %v", im.Name, err)
	}
	if _, err := pickImage(all, "fedora"); err == nil {
		t.Error("expected no match")
	}
}

func TestCreateValidation(t *testing.T) {
	cases := [][]string{
		{"create"},
		{"create", "--name", "a", "--network", "n", "--ssh-from", "10.0.0.0/8"},
		{"create", "--name", "a", "--network", "n", "--keypair", "k", "--pubkey-file", "f", "--ssh-from", "10.0.0.0/8"},
		{"create", "--name", "a", "--network", "n", "--keypair", "k", "--ssh-from", "0.0.0.0/0"},
	}
	for _, args := range cases {
		cmd := RootCmd()
		cmd.SetArgs(append([]string{"--cloud", "x"}, args...))
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		if cmd.Execute() == nil {
			t.Errorf("%v accepted", args)
		}
	}
}

func TestCloudRequired(t *testing.T) {
	cmd := RootCmd()
	cmd.SetArgs([]string{"inventory"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--cloud") {
		t.Fatalf("err = %v", err)
	}
}

func TestUserDataUpgrades(t *testing.T) {
	if !strings.Contains(userData, "package_upgrade: true") {
		t.Error("no upgrade on first boot")
	}
}
