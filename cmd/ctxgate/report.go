// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/spf13/cobra"
)

// bucketEdges are the lower bounds, in tokens, of the context-size buckets.
var bucketEdges = []int64{0, 100_000, 200_000, 400_000, 700_000}

var bucketNames = []string{"<100k", "100-200k", "200-400k", "400-700k", ">=700k"}

func bucketOf(ctx int64) int {
	b := 0
	for i, e := range bucketEdges {
		if ctx >= e {
			b = i
		}
	}
	return b
}

type cell struct{ calls, cacheRead int64 }

type group struct {
	name    string
	buckets [5]cell
}

func (g *group) total() cell {
	var t cell
	for _, c := range g.buckets {
		t.calls += c.calls
		t.cacheRead += c.cacheRead
	}
	return t
}

type census struct {
	groups   map[string]*group
	files    int
	oversize int
}

func newCensus() *census { return &census{groups: map[string]*group{}} }

func (c *census) add(name string, ca call) {
	g := c.groups[name]
	if g == nil {
		g = &group{name: name}
		c.groups[name] = g
	}
	b := &g.buckets[bucketOf(ca.ctx)]
	b.calls++
	b.cacheRead += ca.cacheRead
}

func isSub(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/subagents/")
}

// scan reads every transcript under root modified since cutoff and counts the
// calls stamped at or after it.
func scan(root string, cutoff time.Time) (*census, error) {
	c := newCensus()
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("%s is not a readable directory", root)
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil // an unreadable subtree is skipped, not fatal
		}
		if info, ierr := d.Info(); ierr != nil || info.ModTime().Before(cutoff) {
			return nil
		}
		calls, over, rerr := readCalls(path)
		if rerr != nil {
			return nil
		}
		c.files++
		c.oversize += over
		kind := "main"
		if isSub(path) {
			kind = "sub"
		}
		for _, ca := range calls {
			if !ca.at.IsZero() && ca.at.Before(cutoff) {
				continue
			}
			c.add("all", ca)
			c.add(kind, ca)
			m := ca.model
			if m == "" {
				m = "unknown"
			}
			c.add(kind+" "+m, ca)
		}
		return nil
	})
	return c, err
}

func pct(a, b int64) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

func human(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.2fG", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

// render prints the table: one header, then each group's five buckets. "all",
// "main" and "sub" come first, then "<kind> <model>" by cache-read, largest first.
func (c *census) render(w io.Writer, since time.Duration) {
	all := c.groups["all"]
	if all == nil {
		fmt.Fprintf(w, "ctxgate report --since %v: no API call found (%d files read)\n", since, c.files)
		return
	}
	t := all.total()
	fmt.Fprintf(w, "ctxgate report --since %v: %d calls, %s cache-read tokens (%d files, %d oversize lines skipped)\n",
		since, t.calls, human(t.cacheRead), c.files, c.oversize)
	fmt.Fprintf(w, "%-34s %-9s %8s %7s %10s %7s\n", "group", "context", "calls", "%calls", "cache-read", "%cache")
	var rest []*group
	for n, g := range c.groups {
		if n != "all" && n != "main" && n != "sub" {
			rest = append(rest, g)
		}
	}
	sort.Slice(rest, func(i, j int) bool {
		ti, tj := rest[i].total(), rest[j].total()
		if ti.cacheRead != tj.cacheRead {
			return ti.cacheRead > tj.cacheRead
		}
		return rest[i].name < rest[j].name
	})
	order := []*group{all}
	for _, n := range []string{"main", "sub"} {
		if g := c.groups[n]; g != nil {
			order = append(order, g)
		}
	}
	order = append(order, rest...)
	for _, g := range order {
		gt := g.total()
		for i, b := range g.buckets {
			fmt.Fprintf(w, "%-34s %-9s %8d %6.1f%% %10s %6.1f%%\n",
				g.name, bucketNames[i], b.calls, pct(b.calls, gt.calls), human(b.cacheRead), pct(b.cacheRead, gt.cacheRead))
		}
	}
}

// defaultRoot is where Claude Code keeps its transcripts.
func defaultRoot() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".claude", "projects")
}

func reportCmd() *cobra.Command {
	since := 168 * time.Hour
	root := defaultRoot()
	c := &cobra.Command{
		Use:           "report [--since 168h] [--projects DIR]",
		Short:         "context-size distribution of the API calls",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if since <= 0 {
				return cli.Unanswerable("--since must be positive")
			}
			cen, err := scan(root, clock().Add(-since))
			if err != nil {
				return cli.Unanswerable("%v", err)
			}
			if cen.files == 0 {
				return cli.Unanswerable("no transcript under %s modified in the last %v", root, since)
			}
			cen.render(cli.Out(cmd), since)
			return nil
		},
	}
	c.Flags().DurationVar(&since, "since", since, "only calls newer than this")
	c.Flags().StringVar(&root, "projects", root, "directory holding the transcripts")
	return c
}

var clock = time.Now
