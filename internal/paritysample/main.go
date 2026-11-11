// Command paritysample is how paritycheck's text mode was MEASURED: it reads the
// JSON that `gh api` prints (an issue list, an issue-comment list, or a search
// result {"items": [...]}, any number of pages concatenated), runs the gate over
// every title+body, and prints one line per text — REFUSE, pass, or pass
// (mentions bit identity) — so that the false positives can be read, not
// counted. It reads files; it never calls the network.
//
//	go run ./internal/paritysample -sample 30 -seed 1 issues-a.json comments-a.json
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"sort"

	"github.com/go-fleettools/fleettools/internal/paritytext"
)

// Item is the part of an issue, pull request or comment the gate reads.
type Item struct {
	URL   string `json:"html_url"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Load reads a stream of JSON values, each an array of items or {"items": [...]}.
func Load(r io.Reader) ([]Item, error) {
	dec := json.NewDecoder(r)
	var all []Item
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err == io.EOF {
			return all, nil
		} else if err != nil {
			return nil, err
		}
		var arr []Item
		if err := json.Unmarshal(raw, &arr); err != nil {
			var wrap struct {
				Items []Item `json:"items"`
			}
			if err2 := json.Unmarshal(raw, &wrap); err2 != nil {
				return nil, err
			}
			arr = wrap.Items
		}
		all = append(all, arr...)
	}
}

// Verdict is the gate's answer to one item.
type Verdict struct {
	Item     Item
	Refused  bool
	Mentions bool
	Findings []paritytext.Finding
}

// Judge runs the gate: the same call a publisher makes, without a waiver.
func Judge(it Item) Verdict {
	text := it.Title + "\n\n" + it.Body
	_, err := paritytext.Gate(text, "", "--parity-waiver")
	return Verdict{Item: it, Refused: err != nil, Mentions: paritytext.HasBitIdentity(text), Findings: paritytext.Scan(text)}
}

// Sample picks n items without replacement, deterministically for a seed;
// n <= 0 or n >= len keeps them all, ordered by URL.
func Sample(items []Item, n int, seed int64) []Item {
	sort.Slice(items, func(i, j int) bool { return items[i].URL < items[j].URL })
	if n <= 0 || n >= len(items) {
		return items
	}
	rng := rand.New(rand.NewSource(seed))
	idx := rng.Perm(len(items))[:n]
	sort.Ints(idx)
	out := make([]Item, n)
	for i, k := range idx {
		out[i] = items[k]
	}
	return out
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, out, errw io.Writer) int {
	n, seed := 0, int64(1)
	show := false
	var files []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-show":
			show = true
		case "-sample", "-seed":
			if i+1 >= len(args) {
				fmt.Fprintln(errw, "paritysample:", args[i], "wants a number")
				return 2
			}
			var v int64
			if _, err := fmt.Sscan(args[i+1], &v); err != nil {
				fmt.Fprintln(errw, "paritysample:", args[i], err)
				return 2
			}
			if args[i] == "-sample" {
				n = int(v)
			} else {
				seed = v
			}
			i++
		default:
			files = append(files, args[i])
		}
	}
	if len(files) == 0 {
		fmt.Fprintln(errw, "paritysample: give JSON files from `gh api`")
		return 2
	}
	var items []Item
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			fmt.Fprintln(errw, "paritysample:", err)
			return 2
		}
		got, err := Load(fh)
		fh.Close()
		if err != nil {
			fmt.Fprintf(errw, "paritysample: %s: %v\n", f, err)
			return 2
		}
		items = append(items, got...)
	}
	items = Sample(items, n, seed)
	refused, mention := 0, 0
	for _, it := range items {
		v := Judge(it)
		switch {
		case v.Refused:
			refused++
			fmt.Fprintf(out, "REFUSE %s\n", it.URL)
			for _, f := range v.Findings {
				fmt.Fprintf(out, "    %s\n", f)
			}
		case v.Mentions:
			mention++
			fmt.Fprintf(out, "pass (mentions bit identity) %s\n", it.URL)
			if show {
				for _, u := range paritytext.Units(it.Title + "\n\n" + it.Body) {
					if paritytext.HasBitIdentity(u.Text) {
						fmt.Fprintf(out, "    | %s\n", u.Text)
					}
				}
			}
		default:
			fmt.Fprintf(out, "pass %s\n", it.URL)
		}
	}
	fmt.Fprintf(out, "%d text(s): %d refused, %d pass while mentioning bit identity, %d other\n", len(items), refused, mention, len(items)-refused-mention)
	return 0
}
