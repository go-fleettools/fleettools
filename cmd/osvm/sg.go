package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/groups"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/rules"
	"github.com/spf13/cobra"
)

// groupCmd creates a security group with no ingress rule at all: Neutron gives
// a new group its default egress rules and nothing else, so the group is
// egress-only until a rule names it as a source (allow --from-group).
func groupCmd(cloud *string) *cobra.Command {
	var name, desc string
	c := &cobra.Command{
		Use:   "group",
		Short: "create an egress-only security group (no ingress rule); a repeat changes nothing",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if *cloud == "" {
				return errors.New("--cloud is required")
			}
			if name == "" {
				return errors.New("--name is required")
			}
			return createGroup(cmd.Context(), *cloud, name, desc, cmd.OutOrStdout())
		},
	}
	c.Flags().StringVar(&name, "name", "", "security group name")
	c.Flags().StringVar(&desc, "description", "egress only (osvm group)", "description")
	return c
}

func createGroup(ctx context.Context, cloud, name, desc string, w io.Writer) error {
	c, err := connect(ctx, cloud)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "project:", projectName(c.provider))
	gs, err := collect(ctx, groups.List(c.network, groups.ListOpts{Name: name}), groups.ExtractGroups)
	if err != nil {
		return fmt.Errorf("security group %q: %w", name, err)
	}
	switch len(gs) {
	case 1:
		for _, r := range gs[0].Rules {
			if r.Direction == string(rules.DirIngress) {
				return fmt.Errorf("security group %q exists with an ingress rule (id=%s): not egress-only", name, r.ID)
			}
		}
		fmt.Fprintf(w, "group exists: %s id=%s\n", gs[0].Name, gs[0].ID)
		return nil
	case 0:
	default:
		return fmt.Errorf("security group %q: want at most one, got %d", name, len(gs))
	}
	g, err := groups.Create(ctx, c.network, groups.CreateOpts{Name: name, Description: desc}).Extract()
	if err != nil {
		return fmt.Errorf("create security group %q: %w", name, err)
	}
	fmt.Fprintf(w, "group created: %s id=%s\n", g.Name, g.ID)
	return nil
}

// allowFromGroup adds one ingress rule per port and ether type (IPv4, IPv6)
// whose source is the members of the group a.FromGroup, skipping a rule the
// target already has.
func allowFromGroup(ctx context.Context, c *clients, target groups.SecGroup, a allowOpts, w io.Writer) error {
	src, err := oneGroup(ctx, c, a.FromGroup)
	if err != nil {
		return err
	}
	for _, et := range []rules.RuleEtherType{rules.EtherType4, rules.EtherType6} {
		for _, p := range a.Ports {
			id := ""
			for _, r := range target.Rules {
				if r.Direction == string(rules.DirIngress) && r.Protocol == string(a.proto()) && r.RemoteGroupID == src.ID &&
					r.EtherType == string(et) && r.PortRangeMin == p && r.PortRangeMax == p {
					id = r.ID
				}
			}
			if id != "" {
				fmt.Fprintf(w, "rule exists: %s/%d from group %s (%s) in %s id=%s\n", a.proto(), p, src.Name, et, target.Name, id)
				continue
			}
			r, err := rules.Create(ctx, c.network, rules.CreateOpts{
				SecGroupID: target.ID, Direction: rules.DirIngress, EtherType: et,
				Protocol: a.proto(), PortRangeMin: p, PortRangeMax: p, RemoteGroupID: src.ID,
			}).Extract()
			if err != nil {
				return fmt.Errorf("rule %s/%d from group %s: %w", a.proto(), p, src.Name, err)
			}
			fmt.Fprintf(w, "rule added: %s/%d from group %s (%s) in %s id=%s\n", a.proto(), p, src.Name, et, target.Name, r.ID)
		}
	}
	return nil
}
