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
//	osvm --cloud NAME allow --group G --from H/32 --port P... [--create-group] [--allow-shared]
//	osvm --cloud NAME attach --group G --server NAME
//	osvm --cloud NAME revoke --group G --from H/32 --port P...
//	osvm --cloud NAME rules --group G
//
// create works on a shared public network as on a private one (the network is
// found by name; no floating IP is needed). --sg-name names its dedicated
// group and --volume-gb creates and attaches a data volume. allow takes its
// sources from one --from /32, or from --from-json-url/--json-key (for GitHub
// webhooks: https://api.github.com/meta and hooks), refusing a world-open prefix.
//
// allow refuses a group that more than one server carries unless --allow-shared
// is given: a rule there opens every one of them.
//
// References (read 2026-11-22):
//
//	https://pkg.go.dev/github.com/gophercloud/utils/v2/openstack/clientconfig  clouds.yaml loading
//	https://docs.openstack.org/api-ref/compute/                                 servers, flavors, keypairs
//	https://docs.openstack.org/api-ref/network/v2/                              networks, security groups, floating IPs
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/blockstorage/v3/volumes"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/keypairs"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/volumeattach"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/tokens"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/layer3/floatingips"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/groups"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/rules"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/subnets"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"github.com/gophercloud/utils/v2/openstack/clientconfig"
	"github.com/spf13/cobra"
)

var osExit = os.Exit

func main() {
	fleet.WarnIfStale(os.Stderr)
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

	var a allowOpts
	allow := &cobra.Command{
		Use:   "allow",
		Short: "add TCP ingress rules to an existing security group, from one host (/32) only",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cloud == "" {
				return errors.New("--cloud is required")
			}
			if err := a.validate(); err != nil {
				return err
			}
			return allowRules(cmd.Context(), cloud, a, cmd.OutOrStdout())
		},
	}
	allow.Flags().StringVar(&a.Group, "group", "", "security group name (must match exactly one)")
	allow.Flags().StringVar(&a.From, "from", "", "source host as a /32 (required)")
	allow.Flags().StringVar(&a.FromURL, "from-json-url", "", "https URL of a JSON document whose --json-key array lists the source CIDRs (instead of --from)")
	allow.Flags().StringVar(&a.JSONKey, "json-key", "", "key of the CIDR array in the --from-json-url document, e.g. hooks")
	allow.Flags().IntSliceVar(&a.Ports, "port", nil, "port (repeatable)")
	allow.Flags().StringVar(&a.Proto, "proto", "tcp", "protocol of the rule: tcp or udp")
	allow.Flags().BoolVar(&a.CreateGroup, "create-group", false, "create the group when it does not exist")
	allow.Flags().BoolVar(&a.AllowShared, "allow-shared", false, "allow a group that more than one server carries")
	root.AddCommand(allow)

	var r allowOpts
	revoke := &cobra.Command{
		Use:   "revoke",
		Short: "delete the TCP ingress rules of one host (/32) and port(s) from a security group",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cloud == "" {
				return errors.New("--cloud is required")
			}
			if err := r.validate(); err != nil {
				return err
			}
			return revokeRules(cmd.Context(), cloud, r, cmd.OutOrStdout())
		},
	}
	revoke.Flags().StringVar(&r.Group, "group", "", "security group name (must match exactly one)")
	revoke.Flags().StringVar(&r.From, "from", "", "source host as a /32 (required)")
	revoke.Flags().IntSliceVar(&r.Ports, "port", nil, "port (repeatable)")
	revoke.Flags().StringVar(&r.Proto, "proto", "tcp", "protocol of the rules to delete: tcp or udp")
	root.AddCommand(revoke)

	var group, server string
	attachCmd := &cobra.Command{
		Use:   "attach",
		Short: "add an existing security group to every port of one server, keeping its other groups",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cloud == "" {
				return errors.New("--cloud is required")
			}
			if group == "" || server == "" {
				return errors.New("--group and --server are required")
			}
			return attach(cmd.Context(), cloud, group, server, cmd.OutOrStdout())
		},
	}
	attachCmd.Flags().StringVar(&group, "group", "", "security group name")
	attachCmd.Flags().StringVar(&server, "server", "", "server name (exact)")
	root.AddCommand(attachCmd)

	var rg string
	rulesCmd := &cobra.Command{
		Use:   "rules",
		Short: "list a security group's rules and the servers that carry it (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cloud == "" {
				return errors.New("--cloud is required")
			}
			if rg == "" {
				return errors.New("--group is required")
			}
			return listRules(cmd.Context(), cloud, rg, cmd.OutOrStdout())
		},
	}
	rulesCmd.Flags().StringVar(&rg, "group", "", "security group name")
	root.AddCommand(rulesCmd)

	var o createOpts
	create := &cobra.Command{
		Use:   "create",
		Short: "boot a VM on a private or shared network with an SSH-only security group (optional data volume)",
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
	f.StringVar(&o.SGName, "sg-name", "", "name of the dedicated security group to create (default <name>-ssh)")
	f.IntVar(&o.VolumeGB, "volume-gb", 0, "create and attach a data volume of this many GB (0: none)")
	f.BoolVar(&o.DryRun, "dry-run", false, "resolve everything, create nothing")
	root.AddCommand(create)
	root.AddCommand(imageCmd(&cloud), quotaCmd(&cloud))
	return root
}

type createOpts struct {
	Name, Network, ImageMatch, Flavor, PubKeyFile, Keypair, SSHFrom string
	SGName                                                          string
	MinDisk, MinRAM, VolumeGB                                       int
	DryRun                                                          bool
}

func (o createOpts) sgName() string {
	if o.SGName != "" {
		return o.SGName
	}
	return o.Name + "-ssh"
}

func (o createOpts) validate() error {
	if o.Name == "" || o.Network == "" {
		return errors.New("--name and --network are required")
	}
	if (o.PubKeyFile == "") == (o.Keypair == "") {
		return errors.New("exactly one of --pubkey-file and --keypair is required")
	}
	if o.VolumeGB < 0 || o.VolumeGB > 16000 {
		return fmt.Errorf("--volume-gb %d out of range", o.VolumeGB)
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

type allowOpts struct {
	Group, From string
	// FromURL and JSONKey read the sources from a JSON document (for GitHub,
	// https://api.github.com/meta and "hooks") instead of one --from host.
	FromURL, JSONKey string
	// Proto is "tcp" (also when empty) or "udp".
	Proto string
	Ports []int
	// CreateGroup makes the group when absent; AllowShared lets a rule go into
	// a group that more than one server carries.
	CreateGroup, AllowShared bool
}

func (a allowOpts) validate() error {
	if a.Group == "" || len(a.Ports) == 0 {
		return errors.New("--group and at least one --port are required")
	}
	for _, p := range a.Ports {
		if p < 1 || p > 65535 {
			return fmt.Errorf("--port %d out of range", p)
		}
	}
	if a.proto() == "" {
		return fmt.Errorf("--proto %q: tcp or udp", a.Proto)
	}
	if a.FromURL != "" {
		if a.From != "" {
			return errors.New("--from and --from-json-url are exclusive")
		}
		if a.JSONKey == "" {
			return errors.New("--from-json-url needs --json-key")
		}
		return nil
	}
	pre, err := netip.ParsePrefix(a.From)
	if err != nil {
		return fmt.Errorf("--from: %w", err)
	}
	if !pre.Addr().Is4() || pre.Bits() != 32 {
		return errors.New("--from: a single IPv4 host (/32) is required")
	}
	return nil
}

// proto is the rule protocol, "" when it is neither tcp nor udp.
func (a allowOpts) proto() rules.RuleProtocol {
	switch a.Proto {
	case "", "tcp":
		return rules.ProtocolTCP
	case "udp":
		return rules.ProtocolUDP
	}
	return ""
}

// allowRules adds one ingress rule (TCP, or UDP with --proto udp) per port from a single host, skipping a
// rule the group already has, so a repeat changes nothing.
func allowRules(ctx context.Context, cloud string, a allowOpts, w io.Writer) error {
	sources := []string{a.From}
	if a.FromURL != "" {
		var err error
		if sources, err = fetchCIDRs(ctx, a.FromURL, a.JSONKey); err != nil {
			return err
		}
	}
	c, err := connect(ctx, cloud)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "project:", projectName(c.provider))
	gs, err := collect(ctx, groups.List(c.network, groups.ListOpts{Name: a.Group}), groups.ExtractGroups)
	if err != nil {
		return fmt.Errorf("security group %q: %w", a.Group, err)
	}
	if len(gs) == 0 && a.CreateGroup {
		g, err := groups.Create(ctx, c.network, groups.CreateOpts{Name: a.Group, Description: "created by osvm allow --create-group"}).Extract()
		if err != nil {
			return fmt.Errorf("create security group %q: %w", a.Group, err)
		}
		fmt.Fprintf(w, "group created: %s id=%s\n", g.Name, g.ID)
		gs = []groups.SecGroup{*g}
	}
	if len(gs) != 1 {
		return fmt.Errorf("security group %q: want exactly one, got %d", a.Group, len(gs))
	}
	users, err := attachedServers(ctx, c, a.Group)
	if err != nil {
		return err
	}
	if len(users) > 1 && !a.AllowShared {
		return fmt.Errorf("security group %q is attached to %d servers (%s): a rule here opens all of them; pass --allow-shared to do it anyway, or use a group of its own", a.Group, len(users), strings.Join(users, ", "))
	}
	for _, src := range sources {
		have := map[int]string{}
		for _, r := range gs[0].Rules {
			if isHostRule(r, src, a.proto()) && r.PortRangeMin == r.PortRangeMax {
				have[r.PortRangeMin] = r.ID
			}
		}
		et := rules.EtherType4
		if pre, _ := netip.ParsePrefix(src); pre.Addr().Is6() {
			et = rules.EtherType6
		}
		for _, p := range a.Ports {
			if id := have[p]; id != "" {
				fmt.Fprintf(w, "rule exists: %s/%d from %s in %s id=%s\n", a.proto(), p, src, gs[0].Name, id)
				continue
			}
			r, err := rules.Create(ctx, c.network, rules.CreateOpts{
				SecGroupID: gs[0].ID, Direction: rules.DirIngress, EtherType: et,
				Protocol: a.proto(), PortRangeMin: p, PortRangeMax: p, RemoteIPPrefix: src,
			}).Extract()
			if err != nil {
				return fmt.Errorf("rule %s/%d from %s: %w", a.proto(), p, src, err)
			}
			fmt.Fprintf(w, "rule added: %s/%d from %s in %s id=%s\n", a.proto(), p, src, gs[0].Name, r.ID)
		}
	}
	return nil
}

// httpClient fetches --from-json-url; tests replace it.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// maxSources bounds how many CIDRs one source may open in a group.
const maxSources = 200

// fetchCIDRs reads the array under key of the JSON object at url and returns
// its CIDRs, refusing anything that is not an https URL, a well-formed prefix
// or that would open the world. It is read at run time so the list is never
// copied into a script.
func fetchCIDRs(ctx context.Context, url, key string) ([]string, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, errors.New("--from-json-url: https only")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("--from-json-url: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("--from-json-url: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("--from-json-url: %s answered %d", url, resp.StatusCode)
	}
	var doc map[string]json.RawMessage
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("--from-json-url: %w", err)
	}
	var list []string
	if raw, ok := doc[key]; !ok {
		return nil, fmt.Errorf("--from-json-url: no key %q in the reply", key)
	} else if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("--from-json-url: key %q is not a list of strings: %w", key, err)
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("--from-json-url: key %q is empty", key)
	}
	if len(list) > maxSources {
		return nil, fmt.Errorf("--from-json-url: %d prefixes, more than %d", len(list), maxSources)
	}
	var out []string
	seen := map[string]bool{}
	for _, s := range list {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("--from-json-url: %w", err)
		}
		if p.Bits() == 0 {
			return nil, fmt.Errorf("--from-json-url: refusing a world-open prefix %s", s)
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out, nil
}

func isHostRule(r rules.SecGroupRule, from string, proto rules.RuleProtocol) bool {
	return r.Direction == "ingress" && r.Protocol == string(proto) && r.RemoteIPPrefix == from
}

// attachedServers names the servers that carry the group.
func attachedServers(ctx context.Context, c *clients, group string) ([]string, error) {
	ss, err := collect(ctx, servers.List(c.compute, servers.ListOpts{}), servers.ExtractServers)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, s := range ss {
		for _, g := range s.SecurityGroups {
			if g["name"] == group {
				names = append(names, s.Name)
			}
		}
	}
	return names, nil
}

// oneGroup finds exactly one security group by name.
func oneGroup(ctx context.Context, c *clients, name string) (groups.SecGroup, error) {
	gs, err := collect(ctx, groups.List(c.network, groups.ListOpts{Name: name}), groups.ExtractGroups)
	if err != nil {
		return groups.SecGroup{}, fmt.Errorf("security group %q: %w", name, err)
	}
	if len(gs) != 1 {
		return groups.SecGroup{}, fmt.Errorf("security group %q: want exactly one, got %d", name, len(gs))
	}
	return gs[0], nil
}

// attach adds the group to every port of the named server, keeping the ports'
// other groups.
func attach(ctx context.Context, cloud, group, server string, w io.Writer) error {
	c, err := connect(ctx, cloud)
	if err != nil {
		return err
	}
	g, err := oneGroup(ctx, c, group)
	if err != nil {
		return err
	}
	ss, err := collect(ctx, servers.List(c.compute, servers.ListOpts{}), servers.ExtractServers)
	if err != nil {
		return err
	}
	var hit []servers.Server
	for _, s := range ss {
		if s.Name == server {
			hit = append(hit, s)
		}
	}
	if len(hit) != 1 {
		return fmt.Errorf("server %q: want exactly one, got %d", server, len(hit))
	}
	ps, err := collect(ctx, ports.List(c.network, ports.ListOpts{DeviceID: hit[0].ID}), ports.ExtractPorts)
	if err != nil {
		return err
	}
	if len(ps) == 0 {
		return fmt.Errorf("server %q has no port", server)
	}
	for _, p := range ps {
		ids := append([]string{}, p.SecurityGroups...)
		if !slices.Contains(ids, g.ID) {
			ids = append(ids, g.ID)
			if _, err := ports.Update(ctx, c.network, p.ID, ports.UpdateOpts{SecurityGroups: &ids}).Extract(); err != nil {
				return fmt.Errorf("port %s: %w", p.ID, err)
			}
		}
		fmt.Fprintf(w, "attached: %s to %s port=%s groups=%d\n", g.Name, server, p.ID, len(ids))
	}
	return nil
}

// revokeRules deletes the ingress TCP rules of one host and port from a group.
func revokeRules(ctx context.Context, cloud string, a allowOpts, w io.Writer) error {
	c, err := connect(ctx, cloud)
	if err != nil {
		return err
	}
	g, err := oneGroup(ctx, c, a.Group)
	if err != nil {
		return err
	}
	for _, p := range a.Ports {
		for _, r := range g.Rules {
			if isHostRule(r, a.From, a.proto()) && r.PortRangeMin == p && r.PortRangeMax == p {
				if err := rules.Delete(ctx, c.network, r.ID).ExtractErr(); err != nil {
					return fmt.Errorf("delete rule %s: %w", r.ID, err)
				}
				fmt.Fprintf(w, "rule deleted: %s/%d from %s in %s id=%s\n", a.proto(), p, a.From, g.Name, r.ID)
			}
		}
	}
	return nil
}

// listRules prints a group's rules and the servers that carry it (read-only).
func listRules(ctx context.Context, cloud, group string, w io.Writer) error {
	c, err := connect(ctx, cloud)
	if err != nil {
		return err
	}
	g, err := oneGroup(ctx, c, group)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "group %s id=%s rules=%d\n", g.Name, g.ID, len(g.Rules))
	for _, r := range g.Rules {
		fmt.Fprintf(w, "rule id=%s %s %s %d-%d from=%s ethertype=%s\n", r.ID, r.Direction, r.Protocol, r.PortRangeMin, r.PortRangeMax, r.RemoteIPPrefix, r.EtherType)
	}
	users, err := attachedServers(ctx, c, group)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "attached to: %s\n", strings.Join(users, ", "))
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
	// volume is nil-safe to leave unresolved: only create --volume-gb needs it.
	volume *gophercloud.ServiceClient
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
	// Block storage is optional: a catalog without it only refuses --volume-gb.
	if v, err := openstack.NewBlockStorageV3(pc, eo); err == nil {
		c.volume = v
	}
	return c, nil
}

func projectID(pc *gophercloud.ProviderClient) string {
	if r, ok := pc.GetAuthResult().(tokens.CreateResult); ok {
		if p, err := r.ExtractProject(); err == nil && p != nil {
			return p.ID
		}
	}
	return ""
}

func projectName(pc *gophercloud.ProviderClient) string {
	if r, ok := pc.GetAuthResult().(tokens.CreateResult); ok {
		if p, err := r.ExtractProject(); err == nil && p != nil {
			return p.Name + " (" + p.ID + ")"
		}
	}
	return "unknown"
}

// collect reads every page of a listing and decodes it.
func collect[T any](ctx context.Context, p pagination.Pager, extract func(pagination.Page) ([]T, error)) ([]T, error) {
	pg, err := p.AllPages(ctx)
	if err != nil {
		return nil, err
	}
	return extract(pg)
}

func inventory(ctx context.Context, cloud string, w io.Writer) error {
	c, err := connect(ctx, cloud)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "project:", projectName(c.provider))

	ss, err := collect(ctx, servers.List(c.compute, servers.ListOpts{}), servers.ExtractServers)
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
		var sgs []string
		for _, g := range s.SecurityGroups {
			sgs = append(sgs, fmt.Sprint(g["name"]))
		}
		fmt.Fprintf(w, "server %s id=%s status=%s keypair=%s secgroups=%s addrs=%s\n", s.Name, s.ID, s.Status, s.KeyName, strings.Join(sgs, ","), strings.Join(addrs, ","))
	}

	ns, err := collect(ctx, networks.List(c.network, networks.ListOpts{}), networks.ExtractNetworks)
	if err != nil {
		return err
	}
	for _, n := range ns {
		fmt.Fprintf(w, "network %s id=%s external=%v shared=%v subnets=%d\n", n.Name, n.ID, false, n.Shared, len(n.Subnets))
	}
	sbs, err := collect(ctx, subnets.List(c.network, subnets.ListOpts{}), subnets.ExtractSubnets)
	if err != nil {
		return err
	}
	for _, s := range sbs {
		fmt.Fprintf(w, "subnet %s cidr=%s net=%s dhcp=%v\n", s.Name, s.CIDR, s.NetworkID, s.EnableDHCP)
	}
	fs, err := collect(ctx, floatingips.List(c.network, floatingips.ListOpts{}), floatingips.ExtractFloatingIPs)
	if err != nil {
		return err
	}
	for _, f := range fs {
		fmt.Fprintf(w, "floatingip %s fixed=%s status=%s\n", f.FloatingIP, f.FixedIP, f.Status)
	}
	ks, err := collect(ctx, keypairs.List(c.compute, nil), keypairs.ExtractKeyPairs)
	if err != nil {
		return err
	}
	for _, k := range ks {
		fmt.Fprintf(w, "keypair %s fingerprint=%s\n", k.Name, k.Fingerprint)
	}
	gs, err := collect(ctx, groups.List(c.network, groups.ListOpts{}), groups.ExtractGroups)
	if err != nil {
		return err
	}
	for _, g := range gs {
		fmt.Fprintf(w, "secgroup %s id=%s rules=%d\n", g.Name, g.ID, len(g.Rules))
	}
	fl, err := collect(ctx, flavors.ListDetail(c.compute, nil), flavors.ExtractFlavors)
	if err != nil {
		return err
	}
	for _, f := range fl {
		fmt.Fprintf(w, "flavor %s vcpus=%d ram=%d disk=%d\n", f.Name, f.VCPUs, f.RAM, f.Disk)
	}
	is, err := collect(ctx, images.List(c.image, images.ListOpts{}), images.ExtractImages)
	if err != nil {
		return err
	}
	for _, im := range is {
		fmt.Fprintf(w, "image %s id=%s status=%s created=%s\n", im.Name, im.ID, im.Status, im.CreatedAt.Format(time.DateOnly))
	}
	return nil
}

// attachVolume creates a blank volume, waits until it is available and
// attaches it to the server (it shows as a new block device in the guest).
func attachVolume(ctx context.Context, c *clients, serverID, name string, gb int, w io.Writer) error {
	v, err := volumes.Create(ctx, c.volume, volumes.CreateOpts{Name: name, Size: gb}, nil).Extract()
	if err != nil {
		return fmt.Errorf("volume: %w", err)
	}
	fmt.Fprintf(w, "volume created: %s id=%s size=%dGB\n", name, v.ID, gb)
	vctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := volumes.WaitForStatus(vctx, c.volume, v.ID, "available"); err != nil {
		return fmt.Errorf("volume: %w", err)
	}
	if _, err := volumeattach.Create(ctx, c.compute, serverID, volumeattach.CreateOpts{VolumeID: v.ID}).Extract(); err != nil {
		return fmt.Errorf("attach volume: %w", err)
	}
	fmt.Fprintf(w, "volume attached: %s to %s\n", v.ID, serverID)
	return nil
}

func create(ctx context.Context, cloud string, o createOpts, w io.Writer) error {
	c, err := connect(ctx, cloud)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "project:", projectName(c.provider))
	if o.VolumeGB > 0 && c.volume == nil {
		return errors.New("--volume-gb: the catalog has no block storage service")
	}

	ns, err := collect(ctx, networks.List(c.network, networks.ListOpts{Name: o.Network}), networks.ExtractNetworks)
	if err != nil || len(ns) != 1 {
		return fmt.Errorf("network %q: want exactly one, got %d (%v)", o.Network, len(ns), err)
	}
	fl, err := collect(ctx, flavors.ListDetail(c.compute, nil), flavors.ExtractFlavors)
	if err != nil {
		return err
	}
	flavor, err := pickFlavor(fl, o.Flavor, o.MinRAM, o.MinDisk)
	if err != nil {
		return err
	}
	is, err := collect(ctx, images.List(c.image, images.ListOpts{}), images.ExtractImages)
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

	g, err := groups.Create(ctx, c.network, groups.CreateOpts{Name: o.sgName(), Description: "SSH only, from " + o.SSHFrom}).Extract()
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
	if o.VolumeGB > 0 {
		if err := attachVolume(ctx, c, srv.ID, o.Name+"-data", o.VolumeGB, w); err != nil {
			return err
		}
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
