// landingkitdist reads a `landingmigrate inventory` file and prints how the
// landings on the kit are spread over kit versions, then writes two files: the
// organisations whose pin is BELOW the target (the rollout's list), and the ones
// not on the kit at all with the inventory's reason (the list to hand a person).
//
// ⛔ Versions compare NUMERICALLY: v0.9.0 is below v0.10.0, which a lexical sort
// gets wrong.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/spf13/cobra"
)

type entry struct {
	Org   string `json:"org"`
	State string `json:"state"`
	Kit   string `json:"kit"`
	Note  string `json:"note"`
}

// less reports whether semver a < b (vX.Y.Z, numeric).
func less(a, b string) bool {
	pa, pb := parts(a), parts(b)
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func parts(v string) [3]int {
	var p [3]int
	for i, s := range strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3) {
		p[i], _ = strconv.Atoi(s)
	}
	return p
}

func summarise(es []entry, target string) (dist map[string]int, behind, other []string) {
	dist = map[string]int{}
	for _, e := range es {
		if e.State != "kit" {
			other = append(other, e.Org+" "+e.State+" "+e.Note)
			continue
		}
		dist[e.Kit]++
		if less(e.Kit, target) {
			behind = append(behind, e.Org)
		}
	}
	sort.Strings(behind)
	sort.Strings(other)
	return
}

func RootCmd() *cobra.Command {
	c := cli.New("landingkitdist INVENTORY.json TARGET BEHIND-OUT.txt OTHER-OUT.txt",
		"How the landings are spread over kit versions, and who is behind", "")
	c.Args = cobra.ExactArgs(4)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		b, err := os.ReadFile(args[0])
		if err != nil {
			return cli.Wrap(err)
		}
		var inv struct {
			Orgs []entry `json:"orgs"`
		}
		if err := json.Unmarshal(b, &inv); err != nil || len(inv.Orgs) == 0 {
			return cli.Unanswerable("unreadable or empty inventory: %v", err)
		}
		dist, behind, other := summarise(inv.Orgs, args[1])
		vs := make([]string, 0, len(dist))
		for v := range dist {
			vs = append(vs, v)
		}
		sort.Slice(vs, func(i, j int) bool { return less(vs[i], vs[j]) })
		total := 0
		out := cli.Out(cmd)
		for _, v := range vs {
			fmt.Fprintf(out, "%-10s %d\n", v, dist[v])
			total += dist[v]
		}
		fmt.Fprintf(out, "on the kit: %d, below %s: %d, not on the kit: %d (of %d)\n", total, args[1], len(behind), len(other), len(inv.Orgs))
		if err := os.WriteFile(args[2], []byte(strings.Join(behind, "\n")+"\n"), 0o644); err != nil {
			return cli.Wrap(err)
		}
		return cli.Wrap(os.WriteFile(args[3], []byte(strings.Join(other, "\n")+"\n"), 0o644))
	}
	return c
}

func main() { cli.Run(RootCmd()) }
