package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/imagedata"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/spf13/cobra"
)

type fetchOpts struct {
	Name, Out, SHA string
}

func (o fetchOpts) validate() error {
	if o.Name == "" || o.Out == "" {
		return errors.New("--name and --out are required")
	}
	if strings.ContainsAny(o.Name, "*?[]") {
		return errors.New("--name is exact: no wildcard")
	}
	return nil
}

func imageDownloadCmd(cloud *string) *cobra.Command {
	var o fetchOpts
	c := &cobra.Command{
		Use:   "image-download",
		Short: "download the one image of this project with an exact name to a new file and check its SHA-256",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if *cloud == "" {
				return errors.New("--cloud is required")
			}
			if err := o.validate(); err != nil {
				return err
			}
			return imageDownload(cmd.Context(), *cloud, o, cmd.OutOrStdout())
		},
	}
	f := c.Flags()
	f.StringVar(&o.Name, "name", "", "image name (exact, required; two images of that name are refused)")
	f.StringVar(&o.Out, "out", "", "new file to write (required; must not exist)")
	f.StringVar(&o.SHA, "sha256", "", "expected SHA-256 (default: the osvm_sha256 property the upload recorded; one of the two must exist)")
	return c
}

func imageDownload(ctx context.Context, cloud string, o fetchOpts, w io.Writer) error {
	c, err := connect(ctx, cloud)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "project:", projectName(c.provider))
	have, err := collect(ctx, images.List(c.image, images.ListOpts{Name: o.Name}), images.ExtractImages)
	if err != nil {
		return fmt.Errorf("image list: %w", err)
	}
	if len(have) != 1 {
		return fmt.Errorf("want exactly one image named %q, found %d", o.Name, len(have))
	}
	im := have[0]
	want := o.SHA
	if want == "" {
		want, _ = im.Properties[sha256Property].(string)
	}
	if want == "" {
		return fmt.Errorf("image %q records no %s: pass --sha256", o.Name, sha256Property)
	}
	if im.Status != images.ImageStatusActive {
		return fmt.Errorf("image %q (id=%s) is %s, not active", o.Name, im.ID, im.Status)
	}
	f, err := os.OpenFile(o.Out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	body, err := imagedata.Download(ctx, c.image, im.ID).Extract()
	if err != nil {
		f.Close()
		os.Remove(o.Out)
		return fmt.Errorf("download %s: %w", o.Name, err)
	}
	defer body.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(o.Out)
		return fmt.Errorf("download %s: %w", o.Name, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(o.Out)
		return fmt.Errorf("image %q: sha256 %s is not the expected %s", o.Name, got, want)
	}
	fmt.Fprintf(w, "downloaded %s id=%s to %s (%d bytes, sha256 verified)\n", o.Name, im.ID, o.Out, n)
	return nil
}
