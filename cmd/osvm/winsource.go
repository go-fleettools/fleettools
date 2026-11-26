package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/imagedata"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
)

// sha256Property is the Glance property under which a source image records the
// SHA-256 of the file it was uploaded from, so a rerun can tell "this exact
// file is already there" from "a different file has the same name".
const sha256Property = "osvm_sha256"

// sourceImage is one file the Windows build needs in Glance: an installer ISO,
// the virtio driver ISO, or the answer-file ISO the tool itself builds.
type sourceImage struct {
	Name   string
	Path   string
	Format string            // Glance disk_format: iso or raw
	Props  map[string]string // extra Glance properties (hw_*, os_*)
}

// hashFile returns the lowercase SHA-256 of a file and its size.
func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// ensureSourceImage makes sure a private image named s.Name holds exactly the
// file at s.Path, and returns its id. An image of that name whose recorded
// SHA-256 matches is reused; one whose does not is refused, never overwritten
// or deleted: the person decides. wantSHA, when not empty, is checked against
// the file before anything is created.
func ensureSourceImage(ctx context.Context, c *clients, s sourceImage, wantSHA string, w io.Writer) (string, error) {
	sum, size, err := hashFile(s.Path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", s.Path, err)
	}
	fmt.Fprintf(w, "%s: sha256 %s, %d bytes\n", s.Path, sum, size)
	if wantSHA != "" && sum != wantSHA {
		return "", fmt.Errorf("%s: sha256 %s is not the expected %s", s.Path, sum, wantSHA)
	}
	have, err := collect(ctx, images.List(c.image, images.ListOpts{Name: s.Name}), images.ExtractImages)
	if err != nil {
		return "", fmt.Errorf("image list: %w", err)
	}
	if len(have) > 1 {
		return "", fmt.Errorf("%d images are named %q: refusing to pick one", len(have), s.Name)
	}
	if len(have) == 1 {
		got, _ := have[0].Properties[sha256Property].(string)
		if got != sum {
			return "", fmt.Errorf("image %q (id=%s) exists but records sha256 %q, not %s: delete it or choose another --name", s.Name, have[0].ID, got, sum)
		}
		if have[0].Status != images.ImageStatusActive {
			return "", fmt.Errorf("image %q (id=%s) is %s, not active: delete it and rerun", s.Name, have[0].ID, have[0].Status)
		}
		fmt.Fprintf(w, "image reused: %s id=%s\n", s.Name, have[0].ID)
		return have[0].ID, nil
	}
	props := map[string]string{sha256Property: sum}
	for k, v := range s.Props {
		props[k] = v
	}
	im, err := images.Create(ctx, c.image, images.CreateOpts{
		Name: s.Name, Visibility: &privateVisibility, DiskFormat: s.Format, ContainerFormat: "bare",
		Tags: []string{"osvm-image-build-windows"}, Properties: props,
	}).Extract()
	if err != nil {
		return "", fmt.Errorf("image create: %w", err)
	}
	fmt.Fprintf(w, "image created: %s id=%s\n", s.Name, im.ID)
	f, err := os.Open(s.Path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	start := time.Now()
	if err := imagedata.Upload(ctx, c.image, im.ID, &progressReader{r: f, total: size, w: w, start: start}).ExtractErr(); err != nil {
		return "", fmt.Errorf("upload %s: %w", s.Name, err)
	}
	if err := waitImageActive(ctx, c, im.ID); err != nil {
		return "", err
	}
	fmt.Fprintf(w, "image active: %s id=%s (%s)\n", s.Name, im.ID, time.Since(start).Round(time.Second))
	return im.ID, nil
}

// progressReader prints one line per percent decile so a multi-GB upload shows life.
type progressReader struct {
	r     io.Reader
	total int64
	read  int64
	last  int64
	w     io.Writer
	start time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	if p.total > 0 {
		if d := p.read * 10 / p.total; d > p.last {
			p.last = d
			fmt.Fprintf(p.w, "upload %d%% (%s)\n", d*10, time.Since(p.start).Round(time.Second))
		}
	}
	return n, err
}

var errNoISO = errors.New("--iso is required")
