package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/spf13/cobra"
)

const statusVerifyResize = "VERIFY_RESIZE"

// resizeWait bounds each wait for a status; a variable so a test whose guard is
// removed ends in a stated failure in seconds, not after twenty minutes.
var resizeWait = 20 * time.Minute

type resizeOpts struct {
	Server, Flavor  string
	Confirm, DryRun bool
}

func (o resizeOpts) validate() error {
	if o.Server == "" || o.Flavor == "" {
		return errors.New("--server and --flavor are required")
	}
	return nil
}

func resizeCmd(cloud *string) *cobra.Command {
	var o resizeOpts
	c := &cobra.Command{
		Use:   "resize",
		Short: "resize one server to another flavor; without --confirm it stops at VERIFY_RESIZE (--confirm also confirms it)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if *cloud == "" {
				return errors.New("--cloud is required")
			}
			if err := o.validate(); err != nil {
				return err
			}
			return resize(cmd.Context(), *cloud, o, cmd.OutOrStdout())
		},
	}
	c.Flags().StringVar(&o.Server, "server", "", "server name (exact)")
	c.Flags().StringVar(&o.Flavor, "flavor", "", "target flavor name (exact)")
	c.Flags().BoolVar(&o.Confirm, "confirm", false, "confirm the resize once the server reaches VERIFY_RESIZE (a server already there is confirmed too)")
	c.Flags().BoolVar(&o.DryRun, "dry-run", false, "print the plan and change nothing")
	return c
}

// flavorID is the flavor id Nova reports for a server ("" when it reports none).
func flavorID(s servers.Server) string {
	id, _ := s.Flavor["id"].(string)
	return id
}

func resize(ctx context.Context, cloud string, o resizeOpts, w io.Writer) error {
	c, err := connect(ctx, cloud)
	if err != nil {
		return err
	}
	ss, err := collect(ctx, servers.List(c.compute, servers.ListOpts{}), servers.ExtractServers)
	if err != nil {
		return err
	}
	var hit []servers.Server
	for _, s := range ss {
		if s.Name == o.Server {
			hit = append(hit, s)
		}
	}
	if len(hit) != 1 {
		return fmt.Errorf("server %q: want exactly one, got %d", o.Server, len(hit))
	}
	srv := hit[0]
	fl, err := collect(ctx, flavors.ListDetail(c.compute, nil), flavors.ExtractFlavors)
	if err != nil {
		return err
	}
	want, err := pickFlavor(fl, o.Flavor, 0, 0)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "plan: server %s id=%s status=%s flavor=%s -> %s vcpus=%d ram=%d disk=%d\n",
		srv.Name, srv.ID, srv.Status, flavorID(srv), want.Name, want.VCPUs, want.RAM, want.Disk)
	if srv.Status != statusVerifyResize {
		if flavorID(srv) == want.ID {
			fmt.Fprintf(w, "server %s already has flavor %s\n", srv.Name, want.Name)
			return nil
		}
		if srv.Status != "ACTIVE" && srv.Status != "SHUTOFF" {
			return fmt.Errorf("server %s is %s: only an ACTIVE or SHUTOFF server is resized", srv.Name, srv.Status)
		}
	}
	if o.DryRun {
		return nil
	}
	dctx, cancel := context.WithTimeout(ctx, resizeWait)
	defer cancel()
	if srv.Status != statusVerifyResize {
		if err := servers.Resize(dctx, c.compute, srv.ID, servers.ResizeOpts{FlavorRef: want.ID}).ExtractErr(); err != nil {
			return fmt.Errorf("resize %s: %w", srv.Name, err)
		}
		if err := servers.WaitForStatus(dctx, c.compute, srv.ID, statusVerifyResize); err != nil {
			return fmt.Errorf("wait for %s: %w", statusVerifyResize, err)
		}
		fmt.Fprintf(w, "resized: %s awaits confirmation (%s)\n", srv.Name, statusVerifyResize)
	}
	if !o.Confirm {
		return nil
	}
	if err := servers.ConfirmResize(dctx, c.compute, srv.ID).ExtractErr(); err != nil {
		return fmt.Errorf("confirm resize of %s: %w", srv.Name, err)
	}
	if err := servers.WaitForStatus(dctx, c.compute, srv.ID, "ACTIVE"); err != nil {
		return fmt.Errorf("wait for ACTIVE: %w", err)
	}
	fmt.Fprintf(w, "confirmed: %s is ACTIVE\n", srv.Name)
	return nil
}
