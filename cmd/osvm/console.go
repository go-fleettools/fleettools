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

// consoleCmd prints the tail of a server's Nova console log (read-only): the
// only way to see a guest that has no cloud-init and answers nothing on the
// network, such as a freshly built OpenBSD image.
func consoleCmd(cloud *string) *cobra.Command {
	var server string
	var lines int
	c := &cobra.Command{
		Use:   "console",
		Short: "print the last lines of a server's Nova console log (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if *cloud == "" {
				return errors.New("--cloud is required")
			}
			if server == "" {
				return errors.New("--server is required")
			}
			if lines < 1 || lines > 10000 {
				return errors.New("--lines must be 1..10000")
			}
			return consoleLog(cmd.Context(), *cloud, server, lines, cmd.OutOrStdout())
		},
	}
	c.Flags().StringVar(&server, "server", "", "server name (exact)")
	c.Flags().IntVar(&lines, "lines", 100, "how many trailing lines to print")
	return c
}

func consoleLog(ctx context.Context, cloud, server string, lines int, w io.Writer) error {
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
	out, err := servers.ShowConsoleOutput(ctx, c.compute, hit[0].ID, servers.ShowConsoleOutputOpts{Length: lines}).Extract()
	if err != nil {
		return fmt.Errorf("console log of %s: %w", server, err)
	}
	fmt.Fprint(w, strings.TrimRight(out, "\n")+"\n")
	return nil
}
