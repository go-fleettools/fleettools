package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/spf13/cobra"
)

const (
	hostKeysBegin = "-----BEGIN SSH HOST KEY KEYS-----"
	hostKeysEnd   = "-----END SSH HOST KEY KEYS-----"
)

// hostKeyTypes are the key types a known_hosts line may carry; any other first
// field inside the block is not a key and is refused.
var hostKeyTypes = map[string]bool{
	"ssh-ed25519": true, "ssh-rsa": true,
	"ecdsa-sha2-nistp256": true, "ecdsa-sha2-nistp384": true, "ecdsa-sha2-nistp521": true,
}

// parseHostKeys returns the keys cloud-init printed on the serial console, as
// "type base64" pairs. It reads the LAST complete block (a rebooted guest
// prints it again) and refuses a console with none, a block never closed, or a
// line inside it that is not a key.
func parseHostKeys(console string) ([]string, error) {
	lines := strings.Split(strings.ReplaceAll(console, "\r", ""), "\n")
	begin, end := -1, -1
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case hostKeysBegin:
			begin, end = i, -1
		case hostKeysEnd:
			if begin >= 0 && end < 0 {
				end = i
			}
		}
	}
	if begin < 0 {
		return nil, errors.New("console log has no SSH HOST KEY KEYS block (not booted yet, or the log is truncated)")
	}
	if end < 0 {
		return nil, errors.New("console log's SSH HOST KEY KEYS block is never closed")
	}
	var keys []string
	for _, l := range lines[begin+1 : end] {
		f := strings.Fields(l)
		if len(f) == 0 {
			continue
		}
		if len(f) < 2 || !hostKeyTypes[f[0]] {
			return nil, fmt.Errorf("console log: %q is not a host key line", l)
		}
		keys = append(keys, f[0]+" "+f[1])
	}
	if len(keys) == 0 {
		return nil, errors.New("console log's SSH HOST KEY KEYS block is empty")
	}
	return keys, nil
}

func hostKeysCmd(cloud *string) *cobra.Command {
	var server, host string
	c := &cobra.Command{
		Use:   "hostkeys",
		Short: "print a server's SSH host keys from its Nova console log, as known_hosts lines (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if *cloud == "" {
				return errors.New("--cloud is required")
			}
			if server == "" || host == "" {
				return errors.New("--server and --host are required")
			}
			if strings.ContainsAny(host, " \t\n,#") {
				return errors.New("--host must be one address or name")
			}
			return hostKeys(cmd.Context(), *cloud, server, host, cmd.OutOrStdout())
		},
	}
	c.Flags().StringVar(&server, "server", "", "server name (exact)")
	c.Flags().StringVar(&host, "host", "", "address the known_hosts lines are written for")
	return c
}

func hostKeys(ctx context.Context, cloud, server, host string, w io.Writer) error {
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
		if s.Name == server {
			hit = append(hit, s)
		}
	}
	if len(hit) != 1 {
		return fmt.Errorf("server %q: want exactly one, got %d", server, len(hit))
	}
	out, err := servers.ShowConsoleOutput(ctx, c.compute, hit[0].ID, servers.ShowConsoleOutputOpts{}).Extract()
	if err != nil {
		return fmt.Errorf("console log of %s: %w", server, err)
	}
	keys, err := parseHostKeys(out)
	if err != nil {
		return err
	}
	for _, k := range keys {
		fmt.Fprintf(w, "%s %s\n", host, k)
	}
	return nil
}
