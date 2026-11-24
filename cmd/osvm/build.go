package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/networks"
	"github.com/spf13/cobra"
)

// buildMarker is printed on the builder's console by the last cloud-init step,
// after every package and the udev rule were verified; the snapshot is taken
// only when the console shows it, so a build whose apt failed is never published.
const buildMarker = "OSVM-IMAGE-BUILD-OK"

// defaultPackages are what a self-hosted runner job needs without an apt step:
// QEMU under KVM, image unpacking, screen OCR, and the tools GARM's runner
// bootstrap uses.
var defaultPackages = []string{
	"qemu-system-x86", "qemu-utils", "xz-utils", "tesseract-ocr", "imagemagick",
	"curl", "ca-certificates", "git", "jq", "sudo", "tar", "unzip",
}

var packageName = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*$`)

type buildOpts struct {
	Name, Network, Group, BaseMatch, Flavor string
	Packages                                []string
	Timeout                                 time.Duration
	DryRun                                  bool
}

func (o buildOpts) validate() error {
	if o.Network == "" || o.Group == "" {
		return errors.New("--network and --security-group are required")
	}
	if o.Timeout <= 0 {
		return errors.New("--timeout must be positive")
	}
	if len(o.Packages) == 0 {
		return errors.New("--packages: at least one package")
	}
	for _, p := range o.Packages {
		if !packageName.MatchString(p) {
			return fmt.Errorf("--packages: %q is not a Debian package name", p)
		}
	}
	return nil
}

// buildUserData is the builder's cloud-config: install, make /dev/kvm usable
// by every user at boot (a udev rule, so a later package install cannot reset
// it the way a one-off chmod is reset), verify, print the marker, power off.
func buildUserData(pkgs []string) string {
	var b strings.Builder
	b.WriteString("#cloud-config\npackage_update: true\npackage_upgrade: true\npackages:\n")
	for _, p := range pkgs {
		fmt.Fprintf(&b, "  - %s\n", p)
	}
	b.WriteString("write_files:\n  - path: /etc/udev/rules.d/99-kvm-open.rules\n    content: |\n      KERNEL==\"kvm\", GROUP=\"kvm\", MODE=\"0666\"\n")
	b.WriteString("runcmd:\n  - [sh, -ec, \"")
	for _, p := range pkgs {
		fmt.Fprintf(&b, "dpkg -s %s >/dev/null; ", p)
	}
	b.WriteString("test -f /etc/udev/rules.d/99-kvm-open.rules; apt-get clean; echo " + buildMarker + " >/dev/console\"]\n")
	b.WriteString("power_state:\n  mode: poweroff\n  message: osvm image-build done\n  delay: \"+1\"\n  timeout: 30\n  condition: true\n")
	return b.String()
}

func imageBuildCmd(cloud *string) *cobra.Command {
	o := buildOpts{Packages: defaultPackages}
	c := &cobra.Command{
		Use:   "image-build",
		Short: "boot a builder VM from a base image, install the runner packages by cloud-init, snapshot it to a private image, delete the builder",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if *cloud == "" {
				return errors.New("--cloud is required")
			}
			if o.Name == "" {
				o.Name = "garm-runner-" + now().UTC().Format("20060102-1504")
			}
			if err := o.validate(); err != nil {
				return err
			}
			return imageBuild(cmd.Context(), *cloud, o, cmd.OutOrStdout())
		},
	}
	f := c.Flags()
	f.StringVar(&o.Name, "name", "", "name of the image to create (default garm-runner-<UTC date-time>)")
	f.StringVar(&o.Network, "network", "", "network the builder boots on (it needs outbound access for apt)")
	f.StringVar(&o.Group, "security-group", "", "existing security group of the builder (egress only is enough: nothing connects to it)")
	f.StringVar(&o.BaseMatch, "base-image-match", "debian-13-genericcloud", "case-insensitive substring of the base image name (newest active wins)")
	f.StringVar(&o.Flavor, "flavor", "", "builder flavor (default: smallest with 2 GB RAM and 20 GB disk)")
	f.StringSliceVar(&o.Packages, "packages", defaultPackages, "Debian packages to preinstall")
	f.DurationVar(&o.Timeout, "timeout", 25*time.Minute, "bound on the whole build (boot, install, power off, snapshot)")
	f.BoolVar(&o.DryRun, "dry-run", false, "resolve everything and print the cloud-config, create nothing")
	return c
}

// now is replaced by tests.
var now = time.Now

func imageBuild(ctx context.Context, cloud string, o buildOpts, w io.Writer) error {
	c, err := connect(ctx, cloud)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "project:", projectName(c.provider))
	ns, err := collect(ctx, networks.List(c.network, networks.ListOpts{Name: o.Network}), networks.ExtractNetworks)
	if err != nil || len(ns) != 1 {
		return fmt.Errorf("network %q: want exactly one, got %d (%v)", o.Network, len(ns), err)
	}
	sg, err := oneGroup(ctx, c, o.Group)
	if err != nil {
		return err
	}
	fl, err := collect(ctx, flavors.ListDetail(c.compute, nil), flavors.ExtractFlavors)
	if err != nil {
		return err
	}
	flavor, err := pickFlavor(fl, o.Flavor, 2048, 20)
	if err != nil {
		return err
	}
	is, err := collect(ctx, images.List(c.image, images.ListOpts{}), images.ExtractImages)
	if err != nil {
		return err
	}
	base, err := pickImage(is, o.BaseMatch)
	if err != nil {
		return err
	}
	for _, im := range is {
		if im.Name == o.Name {
			return fmt.Errorf("an image named %q already exists (id=%s)", o.Name, im.ID)
		}
	}
	fmt.Fprintf(w, "plan: base=%s flavor=%s network=%s group=%s image=%s\n", base.Name, flavor.Name, ns[0].Name, sg.Name, o.Name)
	ud := buildUserData(o.Packages)
	if o.DryRun {
		fmt.Fprint(w, ud)
		return nil
	}

	bctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	srv, err := servers.Create(bctx, c.compute, servers.CreateOpts{
		Name: o.Name + "-builder", FlavorRef: flavor.ID, ImageRef: base.ID,
		Networks:       []servers.Network{{UUID: ns[0].ID}},
		SecurityGroups: []string{sg.Name},
		UserData:       []byte(ud),
	}, nil).Extract()
	if err != nil {
		return fmt.Errorf("builder: %w", err)
	}
	fmt.Fprintln(w, "builder created:", srv.ID)
	deleted := false
	defer func() {
		if deleted {
			return
		}
		dctx, dcancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer dcancel()
		if err := servers.Delete(dctx, c.compute, srv.ID).ExtractErr(); err != nil {
			fmt.Fprintf(w, "WARNING: builder %s was NOT deleted: %v\n", srv.ID, err)
			return
		}
		fmt.Fprintln(w, "builder deleted:", srv.ID)
	}()

	// Nova serves no console log once the guest is powered off ("Guest does not
	// have a console available", measured on ccdps-ci), so the marker is read
	// while the builder still runs; power_state delays the poweroff to leave
	// time for it.
	if err := waitMarker(bctx, c, srv.ID); err != nil {
		return fmt.Errorf("builder never finished: %w", err)
	}
	fmt.Fprintln(w, "builder verified: every package installed, udev rule present")
	if err := waitPoweredOff(bctx, c, srv.ID); err != nil {
		return fmt.Errorf("builder never powered off: %w", err)
	}

	id, err := servers.CreateImage(bctx, c.compute, srv.ID, servers.CreateImageOpts{Name: o.Name}).ExtractImageID()
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	fmt.Fprintf(w, "snapshot started: %s id=%s\n", o.Name, id)
	if err := waitImageActive(bctx, c, id); err != nil {
		return err
	}
	fmt.Fprintf(w, "image active: %s id=%s\n", o.Name, id)
	if err := servers.Delete(bctx, c.compute, srv.ID).ExtractErr(); err != nil {
		return fmt.Errorf("delete builder %s: %w", srv.ID, err)
	}
	deleted = true
	fmt.Fprintln(w, "builder deleted:", srv.ID)
	return nil
}

// waitMarker polls until the builder's console shows the marker. A console that
// cannot be read yet is not an error while the builder runs; one that is still
// unreadable or markerless once the builder is SHUTOFF is the failure.
func waitMarker(ctx context.Context, c *clients, id string) error {
	for {
		got, err := servers.Get(ctx, c.compute, id).Extract()
		if err != nil {
			return fmt.Errorf("builder status: %w", err)
		}
		if got.Status == "ERROR" {
			return errors.New("builder ended in status ERROR")
		}
		out, cerr := servers.ShowConsoleOutput(ctx, c.compute, id, servers.ShowConsoleOutputOpts{Length: 400}).Extract()
		if cerr == nil && strings.Contains(out, buildMarker) {
			return nil
		}
		if got.Status == "SHUTOFF" {
			return fmt.Errorf("the builder powered off without %s on its console: a package or the udev rule is missing, no image taken (console read: %v)", buildMarker, cerr)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("no %s yet (status %s, console read: %v): %w", buildMarker, got.Status, cerr, ctx.Err())
		case <-time.After(pollEvery):
		}
	}
}

// waitPoweredOff polls until the server is SHUTOFF. It stops at once on ERROR:
// servers.WaitForStatus would wait the whole timeout for a builder that is dead.
func waitPoweredOff(ctx context.Context, c *clients, id string) error {
	for {
		got, err := servers.Get(ctx, c.compute, id).Extract()
		if err != nil {
			return fmt.Errorf("builder status: %w", err)
		}
		switch got.Status {
		case "SHUTOFF":
			return nil
		case "ERROR":
			return fmt.Errorf("builder ended in status ERROR")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("status %s: %w", got.Status, ctx.Err())
		case <-time.After(pollEvery):
		}
	}
}

func waitImageActive(ctx context.Context, c *clients, id string) error {
	for {
		got, err := images.Get(ctx, c.image, id).Extract()
		if err != nil {
			return fmt.Errorf("image status: %w", err)
		}
		switch got.Status {
		case images.ImageStatusActive:
			return nil
		case images.ImageStatusKilled, images.ImageStatusDeleted:
			return fmt.Errorf("image %s ended %s", id, got.Status)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("image %s not active: %w", id, ctx.Err())
		case <-time.After(pollEvery):
		}
	}
}
