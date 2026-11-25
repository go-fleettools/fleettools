package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/blockstorage/v3/volumes"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/volumeattach"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/layer3/floatingips"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/groups"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"
	"github.com/spf13/cobra"
)

// deletePoll is the interval between two looks at a resource that is going
// away; a variable so tests do not sleep.
var deletePoll = 2 * time.Second

type deleteOpts struct {
	ID, Name                        string
	WithVolumes, WithGroup, Confirm bool
	Timeout                         time.Duration
}

// validate refuses the forms that could name more than one server: the server
// is one exact id and its exact name, never a pattern, never "all".
func (o deleteOpts) validate() error {
	if o.ID == "" || o.Name == "" {
		return errors.New("--id and --name are required: a server is deleted by its id AND its name")
	}
	if strings.ContainsAny(o.ID+o.Name, "*?[]") {
		return errors.New("--id and --name are exact: no wildcard")
	}
	if o.Timeout <= 0 {
		return errors.New("--timeout must be positive")
	}
	return nil
}

func deleteCmd(cloud *string) *cobra.Command {
	var o deleteOpts
	c := &cobra.Command{
		Use:   "delete",
		Short: "delete one server by id and name (dry run unless --confirm); optionally its data volumes and its own security groups",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if *cloud == "" {
				return errors.New("--cloud is required")
			}
			if err := o.validate(); err != nil {
				return err
			}
			return deleteServer(cmd.Context(), *cloud, o, cmd.OutOrStdout())
		},
	}
	f := c.Flags()
	f.StringVar(&o.ID, "id", "", "server id (exact, required)")
	f.StringVar(&o.Name, "name", "", "server name (exact, required, must match the id's server)")
	f.BoolVar(&o.WithVolumes, "with-volumes", false, "also delete the volumes attached to it that are named <name>-data (the one create --volume-gb makes); any other attached volume is kept")
	f.BoolVar(&o.WithGroup, "with-group", false, "also delete the security groups it carries except \"default\"; refused when another server carries one of them")
	f.BoolVar(&o.Confirm, "confirm", false, "act; without it the plan is printed and nothing is deleted")
	f.DurationVar(&o.Timeout, "timeout", 10*time.Minute, "bound of each wait for a resource to be gone")
	return c
}

// deletePlan is everything one run would remove, read before anything is.
type deletePlan struct {
	srv     servers.Server
	volumes []volumes.Volume // to delete
	kept    []string         // attached volumes that stay, with the reason
	fips    []floatingips.FloatingIP
	groups  []groups.SecGroup
}

func deleteServer(ctx context.Context, cloud string, o deleteOpts, w io.Writer) error {
	c, err := connect(ctx, cloud)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "project:", projectName(c.provider))
	plan, err := planDelete(ctx, c, o)
	if err != nil {
		return err
	}
	srv := plan.srv
	fmt.Fprintf(w, "plan: delete server %s id=%s status=%s\n", srv.Name, srv.ID, srv.Status)
	for _, v := range plan.volumes {
		fmt.Fprintf(w, "plan: delete volume %s id=%s size=%dGB\n", v.Name, v.ID, v.Size)
	}
	for _, k := range plan.kept {
		fmt.Fprintln(w, "plan: keep", k)
	}
	for _, f := range plan.fips {
		fmt.Fprintf(w, "plan: delete floating IP %s id=%s\n", f.FloatingIP, f.ID)
	}
	for _, g := range plan.groups {
		fmt.Fprintf(w, "plan: delete security group %s id=%s\n", g.Name, g.ID)
	}
	if !o.Confirm {
		fmt.Fprintln(w, "dry run: nothing deleted; pass --confirm to act")
		return nil
	}
	for _, f := range plan.fips {
		if err := floatingips.Delete(ctx, c.network, f.ID).ExtractErr(); err != nil {
			return fmt.Errorf("delete floating IP %s: %w", f.FloatingIP, err)
		}
		fmt.Fprintf(w, "floating IP deleted: %s\n", f.FloatingIP)
	}
	if err := servers.Delete(ctx, c.compute, srv.ID).ExtractErr(); err != nil {
		return fmt.Errorf("delete server %s: %w", srv.Name, err)
	}
	if err := waitGone(ctx, o.Timeout, "server "+srv.Name, func(ctx context.Context) error {
		return servers.Get(ctx, c.compute, srv.ID).Err
	}); err != nil {
		return err
	}
	fmt.Fprintf(w, "server deleted: %s id=%s\n", srv.Name, srv.ID)
	for _, v := range plan.volumes {
		if err := deleteVolume(ctx, c, v, o.Timeout); err != nil {
			return err
		}
		fmt.Fprintf(w, "volume deleted: %s id=%s\n", v.Name, v.ID)
	}
	for _, g := range plan.groups {
		if err := groups.Delete(ctx, c.network, g.ID).ExtractErr(); err != nil {
			return fmt.Errorf("delete security group %s: %w", g.Name, err)
		}
		fmt.Fprintf(w, "security group deleted: %s id=%s\n", g.Name, g.ID)
	}
	return nil
}

// planDelete reads the server and refuses unless id and name designate the same
// one server; it lists, but removes nothing.
func planDelete(ctx context.Context, c *clients, o deleteOpts) (*deletePlan, error) {
	ss, err := collect(ctx, servers.List(c.compute, servers.ListOpts{}), servers.ExtractServers)
	if err != nil {
		return nil, err
	}
	var p deletePlan
	found := false
	for _, s := range ss {
		if s.ID == o.ID {
			p.srv, found = s, true
		}
	}
	if !found {
		return nil, fmt.Errorf("no server has id %q", o.ID)
	}
	if p.srv.Name != o.Name {
		return nil, fmt.Errorf("server %s is named %q, not %q: refusing", o.ID, p.srv.Name, o.Name)
	}
	if err := planVolumes(ctx, c, o, &p); err != nil {
		return nil, err
	}
	ps, err := collect(ctx, ports.List(c.network, ports.ListOpts{DeviceID: o.ID}), ports.ExtractPorts)
	if err != nil {
		return nil, err
	}
	for _, port := range ps {
		fs, err := collect(ctx, floatingips.List(c.network, floatingips.ListOpts{PortID: port.ID}), floatingips.ExtractFloatingIPs)
		if err != nil {
			return nil, err
		}
		p.fips = append(p.fips, fs...)
	}
	if o.WithGroup {
		if err := planGroups(ctx, c, ss, &p); err != nil {
			return nil, err
		}
	}
	return &p, nil
}

func planVolumes(ctx context.Context, c *clients, o deleteOpts, p *deletePlan) error {
	atts, err := collect(ctx, volumeattach.List(c.compute, o.ID), volumeattach.ExtractVolumeAttachments)
	if err != nil {
		return err
	}
	if len(atts) == 0 {
		return nil
	}
	if c.volume == nil {
		return errors.New("the server has attached volumes and the catalog has no block storage service to read them")
	}
	for _, a := range atts {
		v, err := volumes.Get(ctx, c.volume, a.VolumeID).Extract()
		if err != nil {
			return fmt.Errorf("volume %s: %w", a.VolumeID, err)
		}
		switch {
		case !o.WithVolumes:
			p.kept = append(p.kept, fmt.Sprintf("volume %s id=%s (--with-volumes not given)", v.Name, v.ID))
		case v.Name != p.srv.Name+"-data":
			p.kept = append(p.kept, fmt.Sprintf("volume %s id=%s (not named %s-data)", v.Name, v.ID, p.srv.Name))
		default:
			p.volumes = append(p.volumes, *v)
		}
	}
	return nil
}

// planGroups selects the groups the server carries, except "default", and
// refuses when any other server carries one: deleting it would fail there, and
// a group two servers share is not this server's own.
func planGroups(ctx context.Context, c *clients, all []servers.Server, p *deletePlan) error {
	for _, sg := range p.srv.SecurityGroups {
		name, _ := sg["name"].(string)
		if name == "" || name == "default" {
			continue
		}
		for _, other := range all {
			if other.ID == p.srv.ID {
				continue
			}
			for _, og := range other.SecurityGroups {
				if og["name"] == name {
					return fmt.Errorf("--with-group: security group %q is also carried by server %s (%s): refusing", name, other.Name, other.ID)
				}
			}
		}
		g, err := oneGroup(ctx, c, name)
		if err != nil {
			return err
		}
		p.groups = append(p.groups, g)
	}
	return nil
}

// deleteVolume waits for the volume to be detached (available), deletes it and
// waits until it is gone.
func deleteVolume(ctx context.Context, c *clients, v volumes.Volume, timeout time.Duration) error {
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := volumes.WaitForStatus(wctx, c.volume, v.ID, "available"); err != nil {
		return fmt.Errorf("volume %s did not become available: %w", v.ID, err)
	}
	if err := volumes.Delete(ctx, c.volume, v.ID, volumes.DeleteOpts{}).ExtractErr(); err != nil {
		return fmt.Errorf("delete volume %s: %w", v.ID, err)
	}
	return waitGone(ctx, timeout, "volume "+v.ID, func(ctx context.Context) error {
		return volumes.Get(ctx, c.volume, v.ID).Err
	})
}

// waitGone polls get until it answers 404, failing at the bound with a message
// that names what is still there.
func waitGone(ctx context.Context, timeout time.Duration, what string, get func(context.Context) error) error {
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		err := get(wctx)
		if gophercloud.ResponseCodeIs(err, 404) {
			return nil
		}
		if wctx.Err() != nil {
			return fmt.Errorf("%s is still there after %s", what, timeout)
		}
		if err != nil {
			return fmt.Errorf("waiting for %s to be gone: %w", what, err)
		}
		select {
		case <-wctx.Done():
			return fmt.Errorf("%s is still there after %s", what, timeout)
		case <-time.After(deletePoll):
		}
	}
}
