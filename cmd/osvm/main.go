// Command osvm inspects an OpenStack project and boots a small test VM on its
// private network, from a clouds.yaml entry. It never prints a credential: the
// entry is read by gophercloud's clientconfig and only the project name and
// resource names reach the output.
//
//	osvm --cloud NAME inventory
//	osvm --cloud NAME create --name N --network NET --pubkey-file F.pub [--image-match debian-13] [--ssh-from CIDR]
//
// create refuses a world-open CIDR and never opens Kerberos (88) or SMB (445):
// the only rule it adds is TCP 22 from --ssh-from.
//
// References (read 2026-11-22):
//
//	https://pkg.go.dev/github.com/gophercloud/utils/v2/openstack/clientconfig  clouds.yaml loading
//	https://docs.openstack.org/api-ref/compute/                                 servers, flavors, keypairs
//	https://docs.openstack.org/api-ref/network/v2/                              networks, security groups, floating IPs
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/keypairs"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/tokens"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/layer3/floatingips"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/groups"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/rules"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/subnets"
	"github.com/gophercloud/utils/v2/openstack/clientconfig"
	"github.com/spf13/cobra"
)

var osExit = os.Exit

func main() {
	if err := RootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "osvm:", err)
		osExit(1)
	}
}

// RootCmd builds the command tree.
func RootCmd() *cobra.Command {
	var cloud string
	root := &cobra.Command{
		Use:           "osvm",
		Short:         "inspect an OpenStack project and boot a private-network test VM",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&cloud, "cloud", "", "clouds.yaml entry (required)")

	root.AddCommand(&cobra.Command{
		Use:   "inventory",
		Short: "list project, servers, networks, floating IPs, keypairs, flavors, images (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cloud == "" {
				return errors.New("--cloud is required")
			}
			return inventory(cmd.Context(), cloud, cmd.OutOrStdout())
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "clouds",
		Short: "list the clouds.yaml entry names (names only, no values)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := clientconfig.LoadCloudsYAML()
			if err != nil {
				return err
			}
			names := make([]string, 0, len(m))
			for n := range m {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				fmt.Fprintln(cmd.OutOrStdout(), n)
			}
			return nil
		},
	})

	var o createOpts
	create := &cobra.Command{
		Use:   "create",
		Short: "boot a VM on a private network with an SSH-only security group",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cloud == "" {
				return errors.New("--cloud is required")
			}
			if err := o.validate(); err != nil {
				return err
			}
			return create(cmd.Context(), cloud, o, cmd.OutOrStdout())
		},
	}
	f := create.Flags()
	f.StringVar(&o.Name, "name", "", "server name")
	f.StringVar(&o.Network, "network", "", "private network name")
	f.StringVar(&o.ImageMatch, "image-match", "debian-13", "case-insensitive substring of the image name")
	f.StringVar(&o.Flavor, "flavor", "", "flavor name (default: smallest that fits)")
	f.StringVar(&o.PubKeyFile, "pubkey-file", "", "public key file to register as a keypair")
	f.StringVar(&o.Keypair, "keypair", "", "existing keypair name (instead of --pubkey-file)")
	f.StringVar(&o.SSHFrom, "ssh-from", "", "CIDR allowed to reach TCP 22 (required, never 0.0.0.0/0)")
	f.IntVar(&o.MinDisk, "min-disk", 8, "smallest root disk, GB")
	f.IntVar(&o.MinRAM, "min-ram", 1024, "smallest RAM, MB")
	f.BoolVar(&o.DryRun, "dry-run", false, "resolve everything, create nothing")
	root.AddCommand(create)
	return root
}

type createOpts struct {
	Name, Network, ImageMatch, Flavor, PubKeyFile, Keypair, SSHFrom string
	MinDisk, MinRAM                                                 int
	DryRun                                                          bool
}

func (o createOpts) validate() error {
	if o.Name == "" || o.Network == "" {
		return errors.New("--name and --network are required")
	}
	if (o.PubKeyFile == "") == (o.Keypair == "") {
		return errors.New("exactly one of --pubkey-file and --keypair is required")
	}
	return checkCIDR(o.SSHFrom)
}

// checkCIDR refuses an absent, malformed or world-open source.
func checkCIDR(s string) error {
	if s == "" {
		return errors.New("--ssh-from is required")
	}
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return fmt.Errorf("--ssh-from: %w", err)
	}
	if p.Bits() == 0 {
		return errors.New("--ssh-from: refusing a world-open prefix")
	}
	return nil
}

// pickFlavor returns the smallest flavor (RAM, then vCPUs, then disk) that has
// at least minRAM MB and minDisk GB, or the named one.
func pickFlavor(all []flavors.Flavor, name string, minRAM, minDisk int) (flavors.Flavor, error) {
	if name != "" {
		for _, f := range all {
			if f.Name == name {
				return f, nil
			}
		}
		return flavors.Flavor{}, fmt.Errorf("flavor %q not found", name)
	}
	var fit []flavors.Flavor
	for _, f := range all {
		if f.RAM >= minRAM && f.Disk >= minDisk {
			fit = append(fit, f)
		}
	}
	if len(fit) == 0 {
		return flavors.Flavor{}, errors.New("no flavor fits")
	}
	sort.Slice(fit, func(i, j int) bool {
		a, b := fit[i], fit[j]
		if a.RAM != b.RAM {
			return a.RAM < b.RAM
		}
		if a.VCPUs != b.VCPUs {
			return a.VCPUs < b.VCPUs
		}
		return a.Disk < b.Disk
	})
	return fit[0], nil
}

// pickImage returns the newest active image whose name contains match.
func pickImage(all []images.Image, match string) (images.Image, error) {
	var hit []images.Image
	for _, im := range all {
		if im.Status == "active" && strings.Contains(strings.ToLower(im.Name), strings.ToLower(match)) {
			hit = append(hit, im)
		}
	}
	if len(hit) == 0 {
		return images.Image{}, fmt.Errorf("no active image matches %q", match)
	}
	sort.Slice(hit, func(i, j int) bool { return hit[i].CreatedAt.After(hit[j].CreatedAt) })
	return hit[0], nil
}

// userData is the first-boot cloud-config: security updates only.
const userData = `#cloud-config
package_update: true
package_upgrade: true
package_reboot_if_required: true
`

type clients struct {
	provider *gophercloud.ProviderClient
	compute  *gophercloud.ServiceClient
	network  *gophercloud.ServiceClient
	image    *gophercloud.ServiceClient
}

func connect(ctx context.Context, cloud string) (*clients, error) {
	pc, err := clientconfig.AuthenticatedClient(ctx, &clientconfig.ClientOpts{Cloud: cloud})
	if err != nil {
		return nil, fmt.Errorf("authenticate %q: %w", cloud, err)
	}
	c := &clients{provider: pc}
	eo := gophercloud.EndpointOpts{}
	if c.compute, err = openstack.NewComputeV2(pc, eo); err != nil {
		return nil, err
	}
	if c.network, err = openstack.NewNetworkV2(pc, eo); err != nil {
		return nil, err
	}
	if c.image, err = openstack.NewImageV2(pc, eo); err != nil {
		return nil, err
	}
	return c, nil
}

func projectName(pc *gophercloud.ProviderClient) string {
	if r, ok := pc.GetAuthResult().(tokens.CreateResult); ok {
		if p, err := r.ExtractProject(); err == nil && p != nil {
			return p.Name + " (" + p.ID + ")"
		}
	}
	return "unknown"
}

func inventory(ctx context.Context, cloud string, w io.Writer) error {
	c, err := connect(ctx, cloud)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "project:", projectName(c.provider))

	sp, err := servers.List(c.compute, servers.ListOpts{}).AllPages(ctx)
	if err != nil {
		return err
	}
	ss, err := servers.ExtractServers(sp)
	if err != nil {
		return err
	}
	for _, s := range ss {
		var addrs []string
		for net, v := range s.Addresses {
			list, _ := v.([]any)
			for _, e := range list {
				if m, ok := e.(map[string]any); ok {
					addrs = append(addrs, fmt.Sprintf("%s:%v(%v)", net, m["addr"], m["OS-EXT-IPS:type"]))
				}
			}
		}
		sort.Strings(addrs)
		fmt.Fprintf(w, "server %s id=%s status=%s keypair=%s addrs=%s\n", s.Name, s.ID, s.Status, s.KeyName, strings.Join(addrs, ","))
	}

	np, err := networks.List(c.network, networks.ListOpts{}).AllPages(ctx)
	if err != nil {
		return err
	}
	ns, err := networks.ExtractNetworks(np)
	if err != nil {
		return err
	}
	for _, n := range ns {
		fmt.Fprintf(w, "network %s id=%s external=%v shared=%v subnets=%d\n", n.Name, n.ID, false, n.Shared, len(n.Subnets))
	}
	sbp, err := subnets.List(c.network, subnets.ListOpts{}).AllPages(ctx)
	if err != nil {
		return err
	}
	sbs, err := subnets.ExtractSubnets(sbp)
	if err != nil {
		return err
	}
	for _, s := range sbs {
		fmt.Fprintf(w, "subnet %s cidr=%s net=%s dhcp=%v\n", s.Name, s.CIDR, s.NetworkID, s.EnableDHCP)
	}
	fp, err := floatingips.List(c.network, floatingips.ListOpts{}).AllPages(ctx)
	if err != nil {
		return err
	}
	fs, err := floatingips.ExtractFloatingIPs(fp)
	if err != nil {
		return err
	}
	for _, f := range fs {
		fmt.Fprintf(w, "floatingip %s fixed=%s status=%s\n", f.FloatingIP, f.FixedIP, f.Status)
	}
	kp, err := keypairs.List(c.compute, nil).AllPages(ctx)
	if err != nil {
		return err
	}
	ks, err := keypairs.ExtractKeyPairs(kp)
	if err != nil {
		return err
	}
	for _, k := range ks {
		fmt.Fprintf(w, "keypair %s fingerprint=%s\n", k.Name, k.Fingerprint)
	}
	gp, err := groups.List(c.network, groups.ListOpts{}).AllPages(ctx)
	if err != nil {
		return err
	}
	gs, err := groups.ExtractGroups(gp)
	if err != nil {
		return err
	}
	for _, g := range gs {
		fmt.Fprintf(w, "secgroup %s id=%s rules=%d\n", g.Name, g.ID, len(g.Rules))
	}
	flp, err := flavors.ListDetail(c.compute, nil).AllPages(ctx)
	if err != nil {
		return err
	}
	fl, err := flavors.ExtractFlavors(flp)
	if err != nil {
		return err
	}
	for _, f := range fl {
		fmt.Fprintf(w, "flavor %s vcpus=%d ram=%d disk=%d\n", f.Name, f.VCPUs, f.RAM, f.Disk)
	}
	ip, err := images.List(c.image, images.ListOpts{}).AllPages(ctx)
	if err != nil {
		return err
	}
	is, err := images.ExtractImages(ip)
	if err != nil {
		return err
	}
	for _, im := range is {
		fmt.Fprintf(w, "image %s id=%s status=%s created=%s\n", im.Name, im.ID, im.Status, im.CreatedAt.Format(time.DateOnly))
	}
	return nil
}

func create(ctx context.Context, cloud string, o createOpts, w io.Writer) error {
	c, err := connect(ctx, cloud)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "project:", projectName(c.provider))

	np, err := networks.List(c.network, networks.ListOpts{Name: o.Network}).AllPages(ctx)
	if err != nil {
		return err
	}
	ns, err := networks.ExtractNetworks(np)
	if err != nil || len(ns) != 1 {
		return fmt.Errorf("network %q: want exactly one, got %d (%v)", o.Network, len(ns), err)
	}
	flp, err := flavors.ListDetail(c.compute, nil).AllPages(ctx)
	if err != nil {
		return err
	}
	fl, err := flavors.ExtractFlavors(flp)
	if err != nil {
		return err
	}
	flavor, err := pickFlavor(fl, o.Flavor, o.MinRAM, o.MinDisk)
	if err != nil {
		return err
	}
	ip, err := images.List(c.image, images.ListOpts{}).AllPages(ctx)
	if err != nil {
		return err
	}
	is, err := images.ExtractImages(ip)
	if err != nil {
		return err
	}
	image, err := pickImage(is, o.ImageMatch)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "plan: flavor=%s image=%s network=%s ssh-from=%s\n", flavor.Name, image.Name, ns[0].Name, o.SSHFrom)
	if o.DryRun {
		return nil
	}

	key := o.Keypair
	if o.PubKeyFile != "" {
		pub, err := os.ReadFile(o.PubKeyFile)
		if err != nil {
			return err
		}
		key = o.Name + "-key"
		if _, err := keypairs.Create(ctx, c.compute, keypairs.CreateOpts{Name: key, PublicKey: strings.TrimSpace(string(pub))}).Extract(); err != nil {
			return fmt.Errorf("keypair: %w", err)
		}
		fmt.Fprintln(w, "keypair created:", key)
	}

	g, err := groups.Create(ctx, c.network, groups.CreateOpts{Name: o.Name + "-ssh", Description: "SSH only, from " + o.SSHFrom}).Extract()
	if err != nil {
		return fmt.Errorf("secgroup: %w", err)
	}
	// A new group carries default egress rules; only ingress 22 is added.
	if _, err := rules.Create(ctx, c.network, rules.CreateOpts{
		SecGroupID: g.ID, Direction: rules.DirIngress, EtherType: rules.EtherType4,
		Protocol: rules.ProtocolTCP, PortRangeMin: 22, PortRangeMax: 22, RemoteIPPrefix: o.SSHFrom,
	}).Extract(); err != nil {
		return fmt.Errorf("secgroup rule: %w", err)
	}
	fmt.Fprintln(w, "secgroup created:", g.Name, g.ID)

	copts := servers.CreateOpts{
		Name: o.Name, FlavorRef: flavor.ID, ImageRef: image.ID,
		Networks:       []servers.Network{{UUID: ns[0].ID}},
		SecurityGroups: []string{g.Name},
		UserData:       []byte(userData),
	}
	srv, err := servers.Create(ctx, c.compute, keypairs.CreateOptsExt{CreateOptsBuilder: copts, KeyName: key}, nil).Extract()
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	fmt.Fprintln(w, "server created:", srv.ID)
	dctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := servers.WaitForStatus(dctx, c.compute, srv.ID, "ACTIVE"); err != nil {
		return err
	}
	got, err := servers.Get(ctx, c.compute, srv.ID).Extract()
	if err != nil {
		return err
	}
	for net, v := range got.Addresses {
		list, _ := v.([]any)
		for _, e := range list {
			if m, ok := e.(map[string]any); ok {
				fmt.Fprintf(w, "address %s %v\n", net, m["addr"])
			}
		}
	}
	fmt.Fprintf(w, "server %s id=%s status=%s\n", got.Name, got.ID, got.Status)
	return nil
}
