package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// fake answers the listing and the trees of one organisation, "acme".
func fake(t *testing.T, listing string, trees map[string]string) func(...string) ([]byte, error) {
	t.Helper()
	return func(args ...string) ([]byte, error) {
		p := args[1]
		if strings.HasPrefix(p, "orgs/") {
			if strings.HasPrefix(p, "orgs/broken/") {
				return nil, errors.New("HTTP 404: Not Found")
			}
			return []byte(listing), nil
		}
		for name, body := range trees {
			if strings.HasPrefix(p, "repos/acme/"+name+"/git/trees/") {
				if strings.HasPrefix(body, "ERR ") {
					return nil, errors.New(strings.TrimPrefix(body, "ERR "))
				}
				return []byte(body), nil
			}
		}
		t.Fatalf("unexpected call %v", args)
		return nil, nil
	}
}

func runCmd(t *testing.T, args ...string) (string, int) {
	t.Helper()
	c := RootCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs(args)
	code := execute(c)
	return out.String(), code
}

const listing = `[
 {"full_name":"acme/pure","default_branch":"main","license":{"spdx_id":"BSD-3-Clause"}},
 {"full_name":"acme/bound","default_branch":"main","license":{"spdx_id":"BSD-3-Clause"}},
 {"full_name":"acme/mit","default_branch":"main","license":{"spdx_id":"MIT"}},
 {"full_name":"acme/bare","default_branch":"main","license":null},
 {"full_name":"acme/big","default_branch":"main","license":{"spdx_id":"BSD-3-Clause"}},
 {"full_name":"acme/fixtures","default_branch":"main","license":{"spdx_id":"BSD-3-Clause"}},
 {"full_name":"acme/empty","default_branch":"main","license":{"spdx_id":"BSD-3-Clause"}},
 {"full_name":"acme/.github","default_branch":"main","license":null},
 {"full_name":"acme/old","default_branch":"main","archived":true,"license":{"spdx_id":"GPL-3.0"}},
 {"full_name":"acme/theirs","default_branch":"main","fork":true,"license":{"spdx_id":"Apache-2.0"}}
][]`

var trees = map[string]string{
	"pure":     `{"tree":[{"path":"go.mod","type":"blob"},{"path":"a.go","type":"blob"},{"path":"x_amd64.s","type":"blob"}]}`,
	"bound":    `{"tree":[{"path":"go.mod","type":"blob"},{"path":"bind.go","type":"blob"},{"path":"native/shim.m","type":"blob"},{"path":"native/shim.h","type":"blob"},{"path":"zz_cgo.go","type":"blob"}]}`,
	"mit":      `{"tree":[{"path":"a.go","type":"blob"}]}`,
	"bare":     `{"tree":[{"path":"a.go","type":"blob"}]}`,
	"big":      `{"truncated":true,"tree":[{"path":"a.go","type":"blob"}]}`,
	"fixtures": `{"tree":[{"path":"parse.go","type":"blob"},{"path":"testdata/hello.c","type":"blob"},{"path":"x/testdata/y.h","type":"blob"},{"path":"vendor/z/z.c","type":"blob"},{"path":"cdir","type":"tree"}]}`,
	"empty":    `ERR HTTP 409: Git Repository is empty.`,
	".github":  `{"tree":[{"path":"profile/README.md","type":"blob"},{"path":"default.json","type":"blob"}]}`,
}

func TestAnOrganisationThatDoesNotHold(t *testing.T) {
	gh = fake(t, listing, trees)
	out, code := runCmd(t, "acme")
	if code != 1 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{
		"acme                         DOES NOT HOLD          8 repos, 2 with code not BSD-3-Clause, 1 with cgo signals, 1 unread",
		"licence  acme/mit",
		"licence  acme/bare                                        none",
		"no Go code and no BSD-3-Clause licence (no claim made about them): .github",
		"cgo?     acme/bound                                       native/shim.h, native/shim.m, zz_cgo.go",
		"UNREAD   acme/big                                         GitHub truncated the tree",
		"0 of 1 organisation(s) bear the mantra out",
		"not borne out by acme",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q\n%s", want, out)
		}
	}
	// archived repositories and forks are not ours to judge; a C fixture in
	// testdata or vendor is no cgo; an empty repository has nothing to link;
	// Go assembly is not cgo
	for _, not := range []string{"acme/old", "acme/theirs", "acme/fixtures", "acme/empty", "acme/pure"} {
		if strings.Contains(out, not) {
			t.Errorf("reports %s\n%s", not, out)
		}
	}
}

func TestAnOrganisationThatHolds(t *testing.T) {
	gh = fake(t, `[{"full_name":"acme/pure","default_branch":"main","license":{"spdx_id":"BSD-3-Clause"}}]`, trees)
	out, code := runCmd(t, "acme")
	if code != 0 || !strings.Contains(out, "acme                         HOLDS") || !strings.Contains(out, "1 of 1 organisation(s)") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	// nothing public at all is not a clean organisation
	gh = fake(t, `[]`, trees)
	if out, code := runCmd(t, "acme"); code != 1 || !strings.Contains(out, "NO PUBLIC REPOSITORY") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestVerboseAndErrors(t *testing.T) {
	many := `{"tree":[{"path":"a.c","type":"blob"},{"path":"b.c","type":"blob"},{"path":"c.c","type":"blob"},{"path":"d.cpp","type":"blob"},{"path":"e.swift","type":"blob"}]}`
	gh = fake(t, `[{"full_name":"acme/bound","default_branch":"main","license":{"spdx_id":"BSD-3-Clause"}}]`, map[string]string{"bound": many})
	if out, _ := runCmd(t, "acme"); !strings.Contains(out, "… 2 more") {
		t.Fatal(out)
	}
	if out, _ := runCmd(t, "acme", "-v"); !strings.Contains(out, "e.swift") {
		t.Fatal(out)
	}
	gh = func(args ...string) ([]byte, error) {
		if strings.HasPrefix(args[1], "orgs/") {
			return []byte(`[{"full_name":"acme/pure","default_branch":"main","license":{"spdx_id":"BSD-3-Clause"}}]`), nil
		}
		if strings.Contains(args[1], "trees") {
			return []byte(`not json`), nil
		}
		return nil, nil
	}
	if out, code := runCmd(t, "acme"); code != 1 || !strings.Contains(out, "the tree did not decode") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	gh = func(args ...string) ([]byte, error) {
		if strings.HasPrefix(args[1], "orgs/") {
			return []byte(`[{"full_name":"acme/pure","default_branch":"main","license":{"spdx_id":"BSD-3-Clause"}}]`), nil
		}
		return nil, errors.New("HTTP 403: rate limit")
	}
	if out, code := runCmd(t, "acme"); code != 1 || !strings.Contains(out, "UNREAD") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	// an unreadable organisation is said, and does not hide the next one
	gh = fake(t, `[{"full_name":"acme/pure","default_branch":"main","license":{"spdx_id":"BSD-3-Clause"}}]`, trees)
	if out, code := runCmd(t, "broken", "acme"); code != 1 || !strings.Contains(out, "UNREAD               HTTP 404") ||
		!strings.Contains(out, "acme                         HOLDS") || !strings.Contains(out, "1 of 2 organisation(s)") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if _, code := runCmd(t); code != 2 {
		t.Fatalf("no organisation: exit %d", code)
	}
}

// Data under another licence is listed, not failed: the code's licence
// holds, and the files are for a person to read before a strip names them.
func TestDataLicences(t *testing.T) {
	fonts := `{"tree":[
	 {"path":"LICENSE","type":"blob"},{"path":"go.mod","type":"blob"},{"path":"a.go","type":"blob"},
	 {"path":"inter/OFL.txt","type":"blob"},{"path":"vera/COPYRIGHT.TXT","type":"blob"},
	 {"path":"themes/LICENSE-MIT","type":"blob"},{"path":"twemoji/Graphics.LICENSE","type":"blob"},
	 {"path":"license.go","type":"blob"},{"path":"vendor/x/LICENSE","type":"blob"},
	 {"path":"web/node_modules/y/LICENSE","type":"blob"},{"path":"docs","type":"tree"},
	 {"path":"licensed.md","type":"blob"},{"path":"NOTICE","type":"blob"}]}`
	gh = fake(t, `[{"full_name":"acme/fonts","default_branch":"main","license":{"spdx_id":"BSD-3-Clause"}}]`, map[string]string{"fonts": fonts})
	out, code := runCmd(t, "acme", "-v")
	if code != 0 {
		t.Fatalf("data licences failed the mantra: exit %d\n%s", code, out)
	}
	want := "data     acme/fonts                                       NOTICE, inter/OFL.txt, themes/LICENSE-MIT, twemoji/Graphics.LICENSE, vera/COPYRIGHT.TXT"
	if !strings.Contains(out, want) {
		t.Fatalf("lacks %q\n%s", want, out)
	}
	for _, not := range []string{"vendor/", "node_modules", "license.go", "licensed.md", ", LICENSE"} {
		if strings.Contains(out, not) {
			t.Errorf("lists %s\n%s", not, out)
		}
	}
	// more than five are cut short unless -v
	six := strings.Replace(fonts, `{"path":"NOTICE","type":"blob"}`, `{"path":"NOTICE","type":"blob"},{"path":"z/LICENCE.txt","type":"blob"}`, 1)
	gh = fake(t, `[{"full_name":"acme/fonts","default_branch":"main","license":{"spdx_id":"BSD-3-Clause"}}]`, map[string]string{"fonts": six})
	if out, _ := runCmd(t, "acme"); !strings.Contains(out, "vera/COPYRIGHT.TXT, … 1 more") {
		t.Fatal(out)
	}
	if out, _ := runCmd(t, "acme", "-v"); !strings.Contains(out, "z/LICENCE.txt") {
		t.Fatal(out)
	}
}

func TestMain(t *testing.T) {
	gh = func(args ...string) ([]byte, error) {
		if strings.HasPrefix(args[1], "orgs/") {
			return []byte(`[]`), nil
		}
		return nil, nil
	}
	code := -1
	old := osExit
	defer func() { osExit = old }()
	osExit = func(c int) { code = c }
	main() // os.Args are the test binary's: no organisation, a usage error
	if code != 2 {
		t.Fatalf("main exited %d", code)
	}
}
