package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/blockstorage/v3/volumes"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/spf13/cobra"
)

// idName is the pair every deletion is addressed by: one exact id AND its exact
// name, never a pattern.
type idName struct {
	ID, Name string
	Confirm  bool
	// Unnamed addresses a resource that has NO name (a volume Nova leaked): the
	// id alone, and the resource must really be unnamed.
	Unnamed bool
}

func (o idName) validate() error {
	if o.Unnamed {
		if o.ID == "" || o.Name != "" || strings.ContainsAny(o.ID, "*?[]") {
			return errors.New("--unnamed takes one exact --id and no --name")
		}
		return nil
	}
	if o.ID == "" || o.Name == "" {
		return errors.New("--id and --name are required: a resource is deleted by its id AND its name")
	}
	if strings.ContainsAny(o.ID+o.Name, "*?[]") {
		return errors.New("--id and --name are exact: no wildcard")
	}
	return nil
}

func volumeDeleteCmd(cloud *string) *cobra.Command {
	var o idName
	c := &cobra.Command{
		Use:   "volume-delete",
		Short: "delete one unattached Cinder volume by id and name (dry run unless --confirm)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if *cloud == "" {
				return errors.New("--cloud is required")
			}
			if err := o.validate(); err != nil {
				return err
			}
			return volumeDelete(cmd.Context(), *cloud, o, cmd.OutOrStdout())
		},
	}
	f := c.Flags()
	f.StringVar(&o.ID, "id", "", "volume id (exact, required)")
	f.StringVar(&o.Name, "name", "", "volume name (exact, required, must match the id's volume)")
	f.BoolVar(&o.Unnamed, "unnamed", false, "the volume has no name (Nova leaks such volumes): give --id only; a volume that has a name is refused")
	f.BoolVar(&o.Confirm, "confirm", false, "act; without it the plan is printed and nothing is deleted")
	return c
}

func volumeDelete(ctx context.Context, cloud string, o idName, w io.Writer) error {
	c, err := connect(ctx, cloud)
	if err != nil {
		return err
	}
	if c.volume == nil {
		return errors.New("the project has no block storage service")
	}
	fmt.Fprintln(w, "project:", projectName(c.provider))
	v, err := volumes.Get(ctx, c.volume, o.ID).Extract()
	if err != nil {
		return fmt.Errorf("volume %s: %w", o.ID, err)
	}
	if v.Name != o.Name {
		return fmt.Errorf("volume %s is named %q, not %q", o.ID, v.Name, o.Name)
	}
	if len(v.Attachments) != 0 || v.Status != "available" {
		return fmt.Errorf("volume %s is %s with %d attachment(s): only an unattached volume is deleted", o.ID, v.Status, len(v.Attachments))
	}
	fmt.Fprintf(w, "plan: delete volume %s id=%s size=%dGB status=%s\n", v.Name, v.ID, v.Size, v.Status)
	if !o.Confirm {
		fmt.Fprintln(w, "dry run: nothing deleted; pass --confirm to act")
		return nil
	}
	if err := deleteVolume(ctx, c, *v, 10*time.Minute); err != nil {
		return err
	}
	fmt.Fprintf(w, "volume deleted: %s id=%s\n", v.Name, v.ID)
	return nil
}

func imageDeleteCmd(cloud *string) *cobra.Command {
	var o idName
	c := &cobra.Command{
		Use:   "image-delete",
		Short: "delete one private image of this project by id and name (dry run unless --confirm)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if *cloud == "" {
				return errors.New("--cloud is required")
			}
			if err := o.validate(); err != nil {
				return err
			}
			return imageDelete(cmd.Context(), *cloud, o, cmd.OutOrStdout())
		},
	}
	f := c.Flags()
	f.StringVar(&o.ID, "id", "", "image id (exact, required)")
	f.StringVar(&o.Name, "name", "", "image name (exact, required, must match the id's image)")
	f.BoolVar(&o.Confirm, "confirm", false, "act; without it the plan is printed and nothing is deleted")
	return c
}

func imageDelete(ctx context.Context, cloud string, o idName, w io.Writer) error {
	c, err := connect(ctx, cloud)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "project:", projectName(c.provider))
	im, err := images.Get(ctx, c.image, o.ID).Extract()
	if err != nil {
		return fmt.Errorf("image %s: %w", o.ID, err)
	}
	if im.Name != o.Name {
		return fmt.Errorf("image %s is named %q, not %q", o.ID, im.Name, o.Name)
	}
	if im.Visibility != images.ImageVisibilityPrivate {
		return fmt.Errorf("image %s is %q, not private: only a private image is deleted", o.ID, im.Visibility)
	}
	if pid := projectID(c.provider); pid == "" || im.Owner != pid {
		return fmt.Errorf("image %s belongs to project %q, not %q", o.ID, im.Owner, pid)
	}
	fmt.Fprintf(w, "plan: delete image %s id=%s status=%s\n", im.Name, im.ID, im.Status)
	if !o.Confirm {
		fmt.Fprintln(w, "dry run: nothing deleted; pass --confirm to act")
		return nil
	}
	if err := images.Delete(ctx, c.image, im.ID).ExtractErr(); err != nil {
		return fmt.Errorf("delete image %s: %w", im.Name, err)
	}
	fmt.Fprintf(w, "image deleted: %s id=%s\n", im.Name, im.ID)
	return nil
}
