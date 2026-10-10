package main

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

// fixtures are govulncheck v1.8.0 output RECORDED, not written by hand: a
// module calling golang.org/x/net/http2 (Server).ServeConn at x/net v0.59.0,
// trimmed to the messages the judge reads; the same run with every
// symbol-level finding removed; and a module whose `replace => ../sib` has no
// directory, exactly as govulncheck printed it.
//
//go:embed fixtures/called.json fixtures/imported.json fixtures/loadfail.json fixtures/loadfail.stderr
var fixtures embed.FS

func fixture(name string) []byte {
	b, err := fixtures.ReadFile("fixtures/" + name)
	if err != nil {
		panic(err)
	}
	return b
}

// selfCase is one known answer. The judge is only worth running over a fleet
// if it gets all three of these right: a tool that never says CALLED, or that
// says CLEAN for a load failure, reports a quiet fleet whatever the fleet is.
type selfCase struct {
	name   string
	stdout []byte
	stderr []byte
	err    error
	want   string
}

func selfCases() []selfCase {
	return []selfCase{
		{"known positive (x/net http2 ServeConn reached)", fixture("called.json"), nil, nil, Called},
		{"imported but not called", fixture("imported.json"), nil, nil, Clean},
		{"packages failed to load", fixture("loadfail.json"), fixture("loadfail.stderr"), errors.New("exit status 1"), Unread},
	}
}

func selftest(w io.Writer) int {
	bad := 0
	for _, c := range selfCases() {
		got := judge(c.stdout, c.stderr, c.err, "")
		mark := "ok  "
		if got.Status != c.want {
			mark = "FAIL"
			bad++
		}
		fmt.Fprintf(w, "%s %-48s want %-6s got %-6s called %d, imported only %d\n",
			mark, c.name, c.want, got.Status, len(got.Called), got.Imported)
	}
	if bad > 0 {
		fmt.Fprintf(w, "%d of %d known answers wrong: do not trust a sweep by this binary\n", bad, len(selfCases()))
		return 1
	}
	return 0
}

// preflight checks the scanner exists before a sweep clones anything: without
// it, every repository would come back UNREAD for the same reason.
var preflight = func(path string) error {
	_, err := exec.LookPath(path)
	return err
}
