// landingrollsum reads landingrollout's state file and prints, per status, how
// many organisations ended there, then one line per organisation that did not
// merge, with its cause. The LAST record of an organisation is its state.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

type result struct {
	Org    string `json:"org"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	PR     int    `json:"pr"`
}

func last(lines []string) map[string]result {
	m := map[string]result{}
	for _, l := range lines {
		var r result
		if json.Unmarshal([]byte(l), &r) == nil && r.Org != "" {
			m[r.Org] = r
		}
	}
	return m
}

func RootCmd() *cobra.Command {
	c := cli.New("landingrollsum STATE.jsonl", "Where each landing of a rollout ended, and why the others did not merge", "")
	c.Args = cobra.ExactArgs(1)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		f, err := os.Open(args[0])
		if err != nil {
			return cli.Wrap(err)
		}
		defer f.Close()
		var lines []string
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			lines = append(lines, sc.Text())
		}
		if err := sc.Err(); err != nil {
			return cli.Wrap(err)
		}
		m := last(lines)
		if len(m) == 0 {
			return cli.Unanswerable("%s holds no readable record: zero of zero is not a finished rollout", args[0])
		}
		count := map[string]int{}
		var orgs []string
		for o, r := range m {
			count[r.Status]++
			orgs = append(orgs, o)
		}
		sort.Strings(orgs)
		var sts []string
		for s := range count {
			sts = append(sts, s)
		}
		sort.Strings(sts)
		out := cli.Out(cmd)
		for _, s := range sts {
			fmt.Fprintf(out, "%-8s %d\n", s, count[s])
		}
		fmt.Fprintf(out, "total    %d\n", len(m))
		for _, o := range orgs {
			r := m[o]
			if r.Status != "merged" {
				d := r.Detail
				if len(d) > 160 {
					d = d[:160]
				}
				fmt.Fprintf(out, "%s\t%s\t#%d\t%s\n", r.Status, o, r.PR, strings.TrimSpace(d))
			}
		}
		return nil
	}
	return c
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}
