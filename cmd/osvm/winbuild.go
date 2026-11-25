package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/blockstorage/v3/volumes"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/networks"
	"github.com/spf13/cobra"
)

// windowsImageProps make Nova boot a Windows 11 guest: UEFI on a q35 machine,
// SATA disks and CD and an e1000e NIC so Setup and the first boot need no
// driver that is not inbox (virtio drivers are a later optimisation).
var windowsImageProps = map[string]string{
	"os_type": "windows", "os_distro": "windows", "hw_firmware_type": "uefi", "hw_machine_type": "q35",
	"hw_disk_bus": "sata", "hw_cdrom_bus": "sata", "hw_vif_model": "e1000e",
}

// The source images keep one stable name each; the recorded SHA-256 decides
// whether a rerun may reuse them.
const (
	windowsISOName  = "osvm-windows-installer-iso"
	blankRootName   = "osvm-windows-blank-root"
	blankRootBytes  = 1 << 20
	builderRAMMB    = 8192
	builderDiskGB   = 80
	bootDiskExtraMB = 256
)

type winOpts struct {
	Name, ISO, ISOSHA, SevenZip, WorkDir, BootDiskOut string
	Network, Group, Flavor, Edition                   string
	Timeout                                           time.Duration
	UploadOnly, KeepBuilder, DryRun                   bool
	Resume                                            string
}

func (o winOpts) validate() error {
	if o.ISO == "" {
		return errNoISO
	}
	if o.Edition == "" {
		return errors.New("--edition must not be empty")
	}
	if o.BootDiskOut == "" && !o.UploadOnly && (o.Network == "" || o.Group == "") {
		return errors.New("--network and --security-group are required")
	}
	if o.Timeout <= 0 {
		return errors.New("--timeout must be positive")
	}
	return nil
}

func imageBuildWindowsCmd(cloud *string) *cobra.Command {
	o := winOpts{}
	c := &cobra.Command{
		Use:   "image-build-windows",
		Short: "upload the Windows installer ISO, install Windows unattended on a builder VM, install the CI tools and cloudbase-init, sysprep, snapshot to a private image",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if *cloud == "" && o.BootDiskOut == "" {
				return errors.New("--cloud is required")
			}
			if o.Name == "" {
				o.Name = "garm-runner-windows-" + now().UTC().Format("20060102-1504")
			}
			if err := o.validate(); err != nil {
				return err
			}
			return imageBuildWindows(cmd.Context(), *cloud, o, cmd.OutOrStdout())
		},
	}
	f := c.Flags()
	f.StringVar(&o.Name, "name", "", "name of the image to create (default garm-runner-windows-<UTC date-time>)")
	f.StringVar(&o.ISO, "iso", "", "local Windows 11 x64 installer ISO (a licence you hold; the system stays unactivated)")
	f.StringVar(&o.ISOSHA, "iso-sha256", "", "expected SHA-256 of --iso (refuse to build from a different file)")
	f.StringVar(&o.SevenZip, "seven-zip", "7zz", "7-Zip binary that reads the ISO's UDF file system")
	f.StringVar(&o.WorkDir, "work-dir", "", "directory for the extracted boot files and the boot disk (default <user config dir>/osvm/windows-build)")
	f.StringVar(&o.BootDiskOut, "boot-disk-out", "", "write the boot disk image to this new file and stop (needs no cloud)")
	f.StringVar(&o.Network, "network", "", "network the builder boots on (it needs outbound access)")
	f.StringVar(&o.Group, "security-group", "", "existing security group of the builder (egress only)")
	f.StringVar(&o.Flavor, "flavor", "", "builder flavor (default: smallest with 8 GB RAM and an 80 GB root disk)")
	f.StringVar(&o.Edition, "edition", "Windows 11 Pro", "install.wim image name to install")
	f.DurationVar(&o.Timeout, "timeout", 2*time.Hour, "bound on the whole build")
	f.StringVar(&o.Resume, "resume-builder", "", "server id of a builder that is still installing: watch it, snapshot it as --name, delete it")
	f.BoolVar(&o.UploadOnly, "upload-only", false, "upload the installer ISO and stop")
	f.BoolVar(&o.KeepBuilder, "keep-builder", false, "leave the builder and its disks after a failure, for inspection")
	f.BoolVar(&o.DryRun, "dry-run", false, "resolve everything and print the answer file (password masked), create nothing")
	return c
}

// prepareBootDisk extracts the boot files from the ISO and writes the boot
// disk (boot files + answer file + provisioning script) to dst, returning the
// Administrator password the answer file carries.
func prepareBootDisk(ctx context.Context, o winOpts, dst string, w io.Writer) (string, error) {
	if _, err := exec.LookPath(o.SevenZip); err != nil {
		return "", errNoSevenZip
	}
	work, err := workDir(o)
	if err != nil {
		return "", err
	}
	tree, err := os.MkdirTemp(work, "boot-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tree)
	fmt.Fprintln(w, "extracting boot files from", o.ISO)
	if err := extractBootFiles(ctx, o.SevenZip, o.ISO, tree); err != nil {
		return "", err
	}
	wim, err := os.Stat(filepath.Join(tree, "sources", "boot.wim"))
	if err != nil {
		return "", err
	}
	pw, err := randomPassword(24)
	if err != nil {
		return "", err
	}
	size := (wim.Size()/(1<<20) + bootDiskExtraMB) << 20
	fmt.Fprintf(w, "building boot disk %s (%d MiB)\n", dst, size>>20)
	if err := buildBootDisk(dst, tree, nil, size); err != nil {
		os.Remove(dst)
		return "", err
	}
	answer := answerISOPath(dst)
	fmt.Fprintf(w, "building answer ISO %s\n", answer)
	files := map[string][]byte{"autounattend.xml": []byte(unattendXML(o.Edition, pw)), provisionName: []byte(provisionScript)}
	if err := buildAnswerISO(answer, files); err != nil {
		os.Remove(dst)
		os.Remove(answer)
		return "", err
	}
	return pw, nil
}

// answerISOPath is where prepareBootDisk writes the answer ISO for boot disk dst.
func answerISOPath(dst string) string {
	return strings.TrimSuffix(dst, filepath.Ext(dst)) + "-answer.iso"
}

func imageBuildWindows(ctx context.Context, cloud string, o winOpts, w io.Writer) error {
	if o.DryRun {
		fmt.Fprint(w, strings.Replace(unattendXML(o.Edition, "PASSWORD"), "\r", "", -1))
		return nil
	}
	if o.BootDiskOut != "" {
		_, err := prepareBootDisk(ctx, o, o.BootDiskOut, w)
		return err
	}
	c, err := connect(ctx, cloud)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "project:", projectName(c.provider))
	bctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	if o.Resume != "" {
		fmt.Fprintln(w, "resuming builder:", o.Resume)
		return finishBuild(bctx, c, o.Resume, o.Name, w)
	}
	isoID, err := ensureSourceImage(bctx, c, sourceImage{Name: windowsISOName, Path: o.ISO, Format: "iso", Props: windowsImageProps}, o.ISOSHA, w)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "installer iso:", isoID)
	if o.UploadOnly {
		return nil
	}

	ns, err := collect(bctx, networks.List(c.network, networks.ListOpts{Name: o.Network}), networks.ExtractNetworks)
	if err != nil || len(ns) != 1 {
		return fmt.Errorf("network %q: want exactly one, got %d (%v)", o.Network, len(ns), err)
	}
	sg, err := oneGroup(bctx, c, o.Group)
	if err != nil {
		return err
	}
	fl, err := collect(bctx, flavors.ListDetail(c.compute, nil), flavors.ExtractFlavors)
	if err != nil {
		return err
	}
	flavor, err := pickFlavor(fl, o.Flavor, builderRAMMB, 0)
	if err != nil {
		return err
	}
	have, err := collect(bctx, images.List(c.image, images.ListOpts{Name: o.Name}), images.ExtractImages)
	if err != nil {
		return err
	}
	if len(have) != 0 {
		return fmt.Errorf("an image named %q already exists (id=%s)", o.Name, have[0].ID)
	}
	fmt.Fprintf(w, "plan: flavor=%s network=%s group=%s image=%s edition=%q\n", flavor.Name, ns[0].Name, sg.Name, o.Name, o.Edition)

	bootPath, cleanup, err := newBootDiskPath(o)
	if err != nil {
		return err
	}
	defer cleanup()
	if _, err := prepareBootDisk(bctx, o, bootPath, w); err != nil {
		return err
	}
	bootImg, err := ensureSourceImage(bctx, c, sourceImage{Name: o.Name + "-boot", Path: bootPath, Format: "raw", Props: windowsImageProps}, "", w)
	if err != nil {
		return err
	}
	answerImg, err := ensureSourceImage(bctx, c, sourceImage{Name: o.Name + "-answer", Path: answerISOPath(bootPath), Format: "iso", Props: windowsImageProps}, "", w)
	if err != nil {
		return err
	}
	defer func() {
		if o.KeepBuilder {
			return
		}
		_ = images.Delete(context.WithoutCancel(ctx), c.image, bootImg).ExtractErr()
		_ = images.Delete(context.WithoutCancel(ctx), c.image, answerImg).ExtractErr()
	}()
	rootPath, rootCleanup, err := newBlankRoot()
	if err != nil {
		return err
	}
	defer rootCleanup()
	rootImg, err := ensureSourceImage(bctx, c, sourceImage{Name: blankRootName, Path: rootPath, Format: "raw", Props: windowsImageProps}, "", w)
	if err != nil {
		return err
	}

	// Nova gives a boot volume about three minutes to be created from its image
	// and aborts the build after that; a 9 GiB installer ISO takes longer, so
	// the volumes are created here, waited for, and handed to Nova ready.
	if c.volume == nil {
		return errors.New("the project has no block storage service: the builder boots from volumes")
	}
	var vols []string
	attached := false
	defer func() {
		if attached || o.KeepBuilder {
			return
		}
		for _, v := range vols {
			if err := volumes.Delete(context.WithoutCancel(ctx), c.volume, v, volumes.DeleteOpts{}).ExtractErr(); err != nil {
				fmt.Fprintf(w, "WARNING: volume %s was NOT deleted: %v\n", v, err)
			}
		}
	}()
	ids := map[string]string{}
	for _, src := range []struct {
		key, img string
		gb       int
	}{{"root", rootImg, builderDiskGB}, {"boot", bootImg, 2}, {"iso", isoID, 12}, {"answer", answerImg, 1}} {
		v, err := volumeFromImage(bctx, c, o.Name+"-"+src.key, src.img, src.gb, w)
		if v != "" {
			vols = append(vols, v)
		}
		if err != nil {
			return err
		}
		ids[src.key] = v
	}

	srv, err := servers.Create(bctx, c.compute, servers.CreateOpts{
		Name: o.Name + "-builder", FlavorRef: flavor.ID,
		Networks:       []servers.Network{{UUID: ns[0].ID}},
		SecurityGroups: []string{sg.Name},
		BlockDevice:    builderBlockDevices(ids["root"], ids["boot"], ids["iso"], ids["answer"]),
	}, nil).Extract()
	if err != nil {
		return fmt.Errorf("builder: %w", err)
	}
	attached = true
	fmt.Fprintln(w, "builder created:", srv.ID)
	deleted := false
	defer func() {
		if deleted || o.KeepBuilder {
			if o.KeepBuilder && !deleted {
				fmt.Fprintf(w, "builder %s kept (--keep-builder)\n", srv.ID)
			}
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

	if err := finishBuild(bctx, c, srv.ID, o.Name, w); err != nil {
		return err
	}
	deleted = true
	return nil
}

// finishBuild follows a running builder to its power-off, snapshots it to the
// image name and deletes it. --resume-builder enters here for a builder whose
// watching client died (a keystone token lapsed, a laptop slept).
func finishBuild(ctx context.Context, c *clients, serverID, name string, w io.Writer) error {
	if err := watchWindowsBuild(ctx, c, serverID, w); err != nil {
		return err
	}
	id, err := servers.CreateImage(ctx, c.compute, serverID, servers.CreateImageOpts{Name: name}).ExtractImageID()
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	fmt.Fprintf(w, "snapshot started: %s id=%s\n", name, id)
	if err := waitImageActive(ctx, c, id); err != nil {
		return err
	}
	fmt.Fprintf(w, "image active: %s id=%s\n", name, id)
	if err := servers.Delete(ctx, c.compute, serverID).ExtractErr(); err != nil {
		return fmt.Errorf("delete builder %s: %w", serverID, err)
	}
	fmt.Fprintln(w, "builder deleted:", serverID)
	return nil
}

// builderBlockDevices lays the builder out: disk 0 (sda) is the blank root the
// image is installed onto and the first boot device; sdb is the boot disk Setup
// starts from (UEFI falls through the empty root to it, and after the install
// boots the root); sdc is the installer ISO as a CD, where Setup finds
// install.wim. The two Cinder volumes die with the builder.
func builderBlockDevices(root, boot, iso, answer string) []servers.BlockDevice {
	return []servers.BlockDevice{
		{SourceType: servers.SourceVolume, UUID: root, DestinationType: servers.DestinationVolume, BootIndex: 0, DiskBus: "sata", DeleteOnTermination: true},
		{SourceType: servers.SourceVolume, UUID: boot, DestinationType: servers.DestinationVolume, BootIndex: 1, DiskBus: "sata", DeleteOnTermination: true},
		{SourceType: servers.SourceVolume, UUID: iso, DestinationType: servers.DestinationVolume, BootIndex: -1, DeviceType: "cdrom", DiskBus: "sata", DeleteOnTermination: true},
		{SourceType: servers.SourceVolume, UUID: answer, DestinationType: servers.DestinationVolume, BootIndex: -1, DeviceType: "cdrom", DiskBus: "sata", DeleteOnTermination: true},
	}
}

// volumeFromImage creates a Cinder volume of gb GB from a Glance image and
// waits until it is available. It returns the volume id even on a wait failure,
// so the caller can delete it.
func volumeFromImage(ctx context.Context, c *clients, name, image string, gb int, w io.Writer) (string, error) {
	v, err := volumes.Create(ctx, c.volume, volumes.CreateOpts{Name: name, Size: gb, ImageID: image}, nil).Extract()
	if err != nil {
		return "", fmt.Errorf("volume %s: %w", name, err)
	}
	fmt.Fprintf(w, "volume %s: %s (%d GB from image %s), waiting\n", name, v.ID, gb, image)
	start := time.Now()
	if err := volumes.WaitForStatus(ctx, c.volume, v.ID, "available"); err != nil {
		return v.ID, fmt.Errorf("volume %s: %w", name, err)
	}
	fmt.Fprintf(w, "volume %s available (%s)\n", name, time.Since(start).Round(time.Second))
	return v.ID, nil
}

// newBootDiskPath returns a path for the boot disk (it must not exist: the
// disk library creates it) and a function that removes it.
func newBootDiskPath(o winOpts) (string, func(), error) {
	work, err := workDir(o)
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp(work, "disk-")
	if err != nil {
		return "", nil, err
	}
	return filepath.Join(dir, "boot.img"), func() { os.RemoveAll(dir) }, nil
}

// workDir returns the (created) directory for extracted boot files and disks.
func workDir(o winOpts) (string, error) {
	work := o.WorkDir
	if work == "" {
		cfg, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		work = filepath.Join(cfg, "osvm", "windows-build")
	}
	return work, os.MkdirAll(work, 0o700)
}

// newBlankRoot writes the 1 MiB zero file that is the builder's root image:
// Nova extends it to the flavor's disk size, Setup installs onto it.
func newBlankRoot() (string, func(), error) {
	work, err := workDir(winOpts{})
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp(work, "root-")
	if err != nil {
		return "", nil, err
	}
	p := filepath.Join(dir, "root.img")
	if err := os.WriteFile(p, make([]byte, blankRootBytes), 0o600); err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}
	return p, func() { os.RemoveAll(dir) }, nil
}

// watchWindowsBuild follows the guest's COM1 output (Nova's console log): it
// prints each new "OSVM:" line, fails at once on a FAILED line, and returns
// when the guest has printed the marker AND then powered off (sysprep
// /shutdown). A poweroff without the marker, or an ERROR status, is a failure.
func watchWindowsBuild(ctx context.Context, c *clients, id string, w io.Writer) error {
	seen := map[string]bool{}
	marker := false
	for {
		got, err := servers.Get(ctx, c.compute, id).Extract()
		if err != nil {
			return fmt.Errorf("builder status: %w", err)
		}
		if got.Status == "ERROR" {
			return fmt.Errorf("builder ended in status ERROR: %s (code %d)", got.Fault.Message, got.Fault.Code)
		}
		out, cerr := servers.ShowConsoleOutput(ctx, c.compute, id, servers.ShowConsoleOutputOpts{Length: 2000}).Extract()
		if cerr == nil {
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(strings.ReplaceAll(line, "\r", ""))
				i := strings.Index(line, "OSVM: ")
				if i < 0 || seen[line] {
					continue
				}
				seen[line] = true
				fmt.Fprintln(w, "guest:", line[i+len("OSVM: "):])
				if strings.Contains(line, "FAILED:") {
					return fmt.Errorf("the guest reported a failure: %s", line[i:])
				}
				if strings.Contains(line, buildMarker) {
					marker = true
				}
			}
		}
		if got.Status == "SHUTOFF" {
			if !marker {
				return fmt.Errorf("the builder powered off without %s on its console (console read: %v): no image taken", buildMarker, cerr)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("builder did not finish (status %s, marker seen: %t): %w", got.Status, marker, ctx.Err())
		case <-time.After(10 * time.Second):
		}
	}
}
