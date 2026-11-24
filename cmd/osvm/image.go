package main

import (
	"bufio"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/blockstorage/v3/quotasets"
	cquotas "github.com/gophercloud/gophercloud/v2/openstack/compute/v2/quotasets"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/imagedata"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/spf13/cobra"
)

// downloadClient fetches the image itself (no overall timeout: the context
// bounds it); tests replace it.
var downloadClient = &http.Client{}

// maxImageBytes bounds one image download.
var maxImageBytes int64 = 16 << 30

type imageOpts struct {
	URL, SumsURL, Name, Dir, DiskFormat, File string
	MinDisk                                   int
	DryRun                                    bool
}

func (o imageOpts) validate() error {
	if o.File != "" {
		// A file built locally (an installer run, a converted disk) has no
		// publisher checksum: its SHA-512 is printed instead.
		if o.URL != "" || o.SumsURL != "" || o.Dir != "" {
			return errors.New("--file excludes --url, --sums-url and --dir")
		}
		if o.Name == "" {
			return errors.New("--file needs --name")
		}
	} else {
		if o.URL == "" || o.SumsURL == "" || o.Name == "" || o.Dir == "" {
			return errors.New("--url, --sums-url, --name and --dir are required (or --file and --name)")
		}
		for _, u := range []string{o.URL, o.SumsURL} {
			if !strings.HasPrefix(u, "https://") {
				return fmt.Errorf("%s: https only", u)
			}
		}
	}
	if o.DiskFormat != "qcow2" && o.DiskFormat != "raw" {
		return fmt.Errorf("--disk-format %q: qcow2 or raw", o.DiskFormat)
	}
	if o.MinDisk < 0 {
		return errors.New("--min-disk must not be negative")
	}
	return nil
}

func imageCmd(cloud *string) *cobra.Command {
	var o imageOpts
	c := &cobra.Command{
		Use:   "image-upload",
		Short: "download an image, verify it against a published SHA512SUMS, upload it as a private image",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if *cloud == "" {
				return errors.New("--cloud is required")
			}
			if err := o.validate(); err != nil {
				return err
			}
			return imageUpload(cmd.Context(), *cloud, o, cmd.OutOrStdout())
		},
	}
	f := c.Flags()
	f.StringVar(&o.URL, "url", "", "https URL of the image file")
	f.StringVar(&o.SumsURL, "sums-url", "", "https URL of the SHA512SUMS file that lists the image (fetch it from the publisher, not a mirror)")
	f.StringVar(&o.Name, "name", "", "name of the private image to create")
	f.StringVar(&o.Dir, "dir", "", "durable directory the download is kept in")
	f.StringVar(&o.File, "file", "", "upload this local image file instead of downloading one (prints its SHA-512)")
	f.StringVar(&o.DiskFormat, "disk-format", "qcow2", "qcow2 or raw")
	f.IntVar(&o.MinDisk, "min-disk", 0, "smallest root disk the image boots on, GB")
	f.BoolVar(&o.DryRun, "dry-run", false, "download and verify, upload nothing")
	return c
}

func quotaCmd(cloud *string) *cobra.Command {
	return &cobra.Command{
		Use:   "quota",
		Short: "list the project's compute and volume quota with what is in use (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if *cloud == "" {
				return errors.New("--cloud is required")
			}
			return quota(cmd.Context(), *cloud, cmd.OutOrStdout())
		},
	}
}

// sumFor finds the SHA512 of file in a coreutils-style SHA512SUMS body.
func sumFor(body io.Reader, file string) (string, error) {
	sc := bufio.NewScanner(body)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == file {
			if len(f[0]) != sha512.Size*2 {
				return "", fmt.Errorf("the sum listed for %s is not a SHA-512 (%d hex digits)", file, len(f[0]))
			}
			return strings.ToLower(f[0]), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s is not listed in the sums file", file)
}

func get(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s answered %d", u, resp.StatusCode)
	}
	return resp, nil
}

// fetchVerified downloads the image into dir and returns its path once its
// SHA-512 equals the published one; a mismatching file is deleted.
func fetchVerified(ctx context.Context, o imageOpts, w io.Writer) (string, error) {
	pu, err := url.Parse(o.URL)
	if err != nil {
		return "", err
	}
	file := path.Base(pu.Path)
	sresp, err := get(ctx, o.SumsURL)
	if err != nil {
		return "", fmt.Errorf("sums: %w", err)
	}
	want, err := sumFor(io.LimitReader(sresp.Body, 1<<20), file)
	sresp.Body.Close()
	if err != nil {
		return "", err
	}
	fmt.Fprintf(w, "published sha512 of %s: %s\n", file, want)

	if err := os.MkdirAll(o.Dir, 0o750); err != nil {
		return "", err
	}
	dst := filepath.Join(o.Dir, file)
	resp, err := get(ctx, o.URL)
	if err != nil {
		return "", fmt.Errorf("image: %w", err)
	}
	defer resp.Body.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return "", err
	}
	h := sha512.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(resp.Body, maxImageBytes+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxImageBytes {
		err = fmt.Errorf("image larger than %d bytes", maxImageBytes)
	}
	if err != nil {
		os.Remove(dst)
		return "", fmt.Errorf("image: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(dst)
		return "", fmt.Errorf("sha512 mismatch for %s: downloaded %s, published %s (file deleted)", file, got, want)
	}
	fmt.Fprintf(w, "verified: %s %d bytes\n", dst, n)
	return dst, nil
}

// localSum prints the SHA-512 and size of a local image file and returns its path.
func localSum(file string, w io.Writer) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha512.New()
	n, err := io.Copy(h, io.LimitReader(f, maxImageBytes+1))
	if err != nil {
		return "", err
	}
	if n > maxImageBytes {
		return "", fmt.Errorf("image larger than %d bytes", maxImageBytes)
	}
	fmt.Fprintf(w, "local file: %s %d bytes sha512 %s\n", file, n, hex.EncodeToString(h.Sum(nil)))
	return file, nil
}

func imageUpload(ctx context.Context, cloud string, o imageOpts, w io.Writer) error {
	var dst string
	var err error
	if o.File != "" {
		dst, err = localSum(o.File, w)
	} else {
		dst, err = fetchVerified(ctx, o, w)
	}
	if err != nil {
		return err
	}
	if o.DryRun {
		return nil
	}
	c, err := connect(ctx, cloud)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "project:", projectName(c.provider))
	have, err := collect(ctx, images.List(c.image, images.ListOpts{Name: o.Name}), images.ExtractImages)
	if err != nil {
		return err
	}
	if len(have) != 0 {
		return fmt.Errorf("an image named %q already exists (id=%s)", o.Name, have[0].ID)
	}
	im, err := images.Create(ctx, c.image, images.CreateOpts{
		Name: o.Name, Visibility: &privateVisibility, DiskFormat: o.DiskFormat, ContainerFormat: "bare",
		MinDisk: o.MinDisk, Tags: []string{"osvm-image-upload"},
	}).Extract()
	if err != nil {
		return fmt.Errorf("image: %w", err)
	}
	fmt.Fprintf(w, "image created: %s id=%s\n", o.Name, im.ID)
	f, err := openImage(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := imagedata.Upload(ctx, c.image, im.ID, f).ExtractErr(); err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	dctx, cancel := context.WithTimeout(ctx, activeTimeout)
	defer cancel()
	for {
		got, err := images.Get(dctx, c.image, im.ID).Extract()
		if err != nil {
			return fmt.Errorf("image status: %w", err)
		}
		if got.Status == images.ImageStatusActive {
			fmt.Fprintf(w, "image active: %s id=%s\n", o.Name, im.ID)
			return nil
		}
		if got.Status == images.ImageStatusKilled || got.Status == images.ImageStatusDeleted {
			return fmt.Errorf("image %s ended %s", im.ID, got.Status)
		}
		select {
		case <-dctx.Done():
			return fmt.Errorf("image %s not active: %w", im.ID, dctx.Err())
		case <-time.After(pollEvery):
		}
	}
}

// openImage reopens the verified file for upload; tests replace it.
var openImage = os.Open

var (
	privateVisibility = images.ImageVisibilityPrivate
	pollEvery         = 3 * time.Second
	activeTimeout     = 15 * time.Minute
)

func quota(ctx context.Context, cloud string, w io.Writer) error {
	c, err := connect(ctx, cloud)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "project:", projectName(c.provider))
	id := projectID(c.provider)
	d, err := cquotas.GetDetail(ctx, c.compute, id).Extract()
	if err != nil {
		return fmt.Errorf("compute quota: %w", err)
	}
	for _, q := range []struct {
		n string
		d cquotas.QuotaDetail
	}{{"instances", d.Instances}, {"cores", d.Cores}, {"ram_mb", d.RAM}} {
		fmt.Fprintf(w, "compute %s in_use=%d reserved=%d limit=%d\n", q.n, q.d.InUse, q.d.Reserved, q.d.Limit)
	}
	if c.volume == nil {
		fmt.Fprintln(w, "volume: the catalog has no block storage service")
		return nil
	}
	v, err := quotasets.GetUsage(ctx, c.volume, id).Extract()
	if err != nil {
		return fmt.Errorf("volume quota: %w", err)
	}
	for _, q := range []struct {
		n string
		u quotasets.QuotaUsage
	}{{"volumes", v.Volumes}, {"gigabytes", v.Gigabytes}} {
		fmt.Fprintf(w, "volume %s in_use=%d reserved=%d limit=%d\n", q.n, q.u.InUse, q.u.Reserved, q.u.Limit)
	}
	return nil
}
