package main

import (
	"path"
	"strings"
	"testing"
)

// FuzzResolve holds the two properties that make this tool safe to run over
// ~1300 checkouts it does not control.
//
// ⛔ THE BASE IS UNTRUSTED INPUT. It is read from each repository's own
// hugo.yaml, so it is attacker-controlled for any repository somebody can push
// to, and the page path is a file name from that same tree. The properties:
//
//  1. a path this tool will STAT must stay inside the checkout. `path.Join`
//     cleans `..` rather than refusing it, so a base ending in `..` used to
//     yield InRepo "../passwd" and Inspect stat-ed outside the tree. Existence
//     only, never a read -- and still an existence oracle over the file system,
//     handed to a documentation checker.
//  2. Resolve must answer, never panic, on any bytes at all.
//
// It is a property and not a list of three examples because the three examples
// were what I could think of, and one of them did not even escape -- the two
// that did were the ones I had not predicted.
func FuzzResolve(f *testing.F) {
	f.Add("https://github.com/go-desktop/docs/edit/main/content", "en/finding.md")
	f.Add("https://github.com/go-desktop/docs/edit/main/content/en", "en/finding.md")
	f.Add("https://github.com/go-desktop/edit/main/content/en", "en/finding.md")
	f.Add("https://github.com/go-desktop/docs/edit/main/..", "passwd")
	f.Add("https://github.com/go-desktop/docs/edit/main/content/..%2F..%2F..", "passwd")
	f.Add("https://github.com/o/r/edit/b/c", "../../../../etc/passwd")
	f.Add("https://github.com/o/r/edit/b/c", "")
	f.Add("", "")
	f.Add("://", "x")
	f.Add("https://github.com/o/r/edit/b/\x00c", "\x00")

	f.Fuzz(func(t *testing.T, base, rel string) {
		l := Resolve(base, rel, "go-desktop", "docs", "main")

		// (1) Nothing that will be stat-ed may leave the checkout.
		if l.InRepo != "" {
			if strings.HasPrefix(l.InRepo, "/") {
				t.Fatalf("absolute InRepo %q from base %q rel %q", l.InRepo, base, rel)
			}
			if l.InRepo == ".." || strings.HasPrefix(l.InRepo, "../") {
				t.Fatalf("escaping InRepo %q from base %q rel %q", l.InRepo, base, rel)
			}
			// path.Clean is idempotent on anything Resolve should hand out:
			// a path that cleans to something else would stat a different file
			// from the one reported to the reader.
			if c := path.Clean(l.InRepo); c != l.InRepo {
				t.Fatalf("unclean InRepo %q (cleans to %q) from base %q rel %q", l.InRepo, c, base, rel)
			}
		}

		// (2) A verdict is always one of the known reasons, and a refusal
		// always carries a sentence: a Reason nobody can print is a finding a
		// reader cannot act on.
		switch l.Reason {
		case ReasonNone:
			if l.Why != "" {
				t.Fatalf("ReasonNone with a sentence %q", l.Why)
			}
		case ReasonNotAnEditURL, ReasonOtherRepo, ReasonOtherBranch, ReasonNoSuchPath, ReasonEscapesRepo:
			if l.Why == "" {
				t.Fatalf("reason %d with no sentence, base %q rel %q", l.Reason, base, rel)
			}
		default:
			t.Fatalf("unknown reason %d, base %q rel %q", l.Reason, base, rel)
		}
	})
}

// FuzzReadConfigDoesNotPanic feeds arbitrary bytes to the configuration reader
// through a real file, which is how it is used.
//
// ⚠ A site's configuration is as untrusted as its base: the same push that
// writes one writes the other. The property is deliberately weak -- answer or
// refuse, never panic -- because what the YAML means is the YAML library's
// business, and asserting more here would be asserting a second parser.
func FuzzReadConfigDoesNotPanic(f *testing.F) {
	f.Add("params:\n  editURL:\n    enable: true\n    base: https://github.com/o/r/edit/main/content\n")
	f.Add("params:\n  editURL:\n    enable: true\n    base:\n      - a\n      - b\n")
	f.Add("contentDir: \x00\nparams: {editURL: {base: 1}}\n")
	f.Add("params: [1, 2, 3]\n")
	f.Add("{{{{{{\n")
	f.Add("")

	f.Fuzz(func(t *testing.T, body string) {
		dir := t.TempDir()
		writeFile(t, dir, "hugo.yaml", body)
		cfg, found, unreadable := ReadConfig(dir)
		if found && cfg.Base == "" {
			t.Fatalf("found a configuration with no base: %+v", cfg)
		}
		if found && unreadable != "" {
			t.Fatalf("found AND unreadable (%q): the two are exclusive", unreadable)
		}
		if found && cfg.Content == "" {
			t.Fatalf("found a configuration with no content directory: %+v", cfg)
		}
	})
}
