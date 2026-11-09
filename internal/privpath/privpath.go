// Package privpath finds, and redacts, the paths, account names and document
// names of a person's own file system in text that is about to be published.
//
// ⛔⛔ WHY IT EXISTS. On 2026-11-09 three issues were filed on go-tex/engine
// whose bodies each carried `/Users/<account>/Documents/<project>/<file>.tex` —
// the reporter's home directory, their account name, their directory layout and
// the name of a document they had not published. Nobody noticed until the user
// asked "did you leak my name". Editing the bodies does not undo it: GitHub
// keeps the original under "edited" for anyone who opens the history, so the
// only real remedy is to delete and re-create.
//
// The prose was accurate and useful; the path was decoration. That is the shape
// to catch — a citation of WHERE a measurement was taken, on a machine nobody
// else can reach, where the measurement itself is the content.
//
// ⛔ The rule it enforces, in the user's words: "pas de leak de nom ni de
// chemin/nom de fichier sur un file system utilisateur". Three things, not one —
// the account name, the path, AND the file name. Redacting only the home prefix
// leaves `<path elided>/Documents/fairshare-b300/fairshare-b300.tex`, which
// still publishes the project and the document. Each of the three also has to be
// caught ON ITS OWN, away from the path that revealed it: the leak had the path
// in the measurement section and the bare account in a session label below.
//
// ⛔ It is a GATE and not a lint, so its default is refusal. Every program that
// sends a body to a forge asks this package first, before the call, because the
// cost of a false negative is unrecallable and the cost of a false positive is
// one flag.
package privpath

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Finding is one place a text names a person's own file system.
type Finding struct {
	Line  int    // 1-indexed
	Col   int    // 1-indexed byte column, so an author can find it without being shown it
	Match string // the exact text matched
	Kind  string // "home path", "unc path", "account name", "document name"
	Why   string // what a reader of the refusal needs to know
}

// String describes the finding WITHOUT reprinting what it found.
//
// ⛔ A refusal is printed where the body was going to go, and that is sometimes
// a CI log on a public repository. A gate against publishing a path must not
// publish the path in the course of refusing it. The line and column locate it
// in a file the author already has; Reveal is for a local run that asks.
func (f Finding) String() string {
	return fmt.Sprintf("line %d, col %d: %s — %s", f.Line, f.Col, f.Kind, f.Why)
}

// Reveal is String with the matched text, for a local run on the author's own
// machine (privscan --show). Never for a log.
func (f Finding) Reveal() string {
	return fmt.Sprintf("line %d, col %d: %s (%s) — %s", f.Line, f.Col, f.Match, f.Kind, f.Why)
}

// homePath matches a home directory with the account name inside it AND the rest
// of the path after it: the account is the first capture because it identifies a
// person, the tail is the second because it names their projects and documents.
//
// ⛔ The account is required. `/Users/` or `/home/` alone is a convention, not a
// disclosure — "`/Users/runner` is the GitHub runner's home directory" is a
// sentence a report may legitimately need, and refusing it would teach the
// reader to pass the waiver by reflex, which is how a gate stops being one.
// ⛔ The account may not begin with a dot. Measured on 2026-11-09 over 60
// repositories: a package manager's own prefix — `/home/.zpkg/<domain>/<pkg>/
// v<version>/bin/<tool>`, and the `.pkgx` spelling its older issue bodies still
// carry — was read as the home directory of a person by that name, and one body
// alone produced 38 findings: the tool's prefix taken for an account, and every
// package name along the path hunted as that person's document. No account name
// starts with a dot; a dotted first component is a tool's directory.
// ⛔ CASE MATTERS, and a blanket (?i) cost five more false positives. Measured
// 2026-11-09 in go-widgets/toolkit#495 and #421: `Home/End/Space` — a list of
// KEYBOARD KEYS — matched as the home directory of a person called `End`, and
// `End` was then hunted bare through those bodies. `/home` is lowercase on every
// Unix and `/Users` is capitalised on macOS; only the Windows spelling is
// genuinely case-insensitive, so only it is folded. The loss is `/HOME/x` and
// macOS's case-insensitive `/users/x`, which nobody writes.
var homePath = regexp.MustCompile(`(?:/Users|/home|(?:[A-Za-z]:)?\\(?i:Users))[/\\]([A-Za-z0-9][A-Za-z0-9._-]*)((?:[/\\][A-Za-z0-9._-]+)*)`)

// uncPath matches a Windows share, which names a machine as well as a person.
//
// ⛔⛔ IT USED TO MATCH TeX. `\\` is a line break in LaTeX and an escaped
// backslash in every Go string literal, so in this fleet's TeX engine tracker
// the pattern `\\ + name + \ + name` matched things like `\\hbox\scriptstyle`
// and captured `s` as a share. That one-letter capture was then hunted as a name
// through all 60 repositories of a sweep, which is where 276 of its 509
// findings came from — a measurement of my own regex, reported as a population.
// Length alone does not separate them — `\\hbox\scriptstyle` has a four-letter
// "host" and an eleven-letter "share". What separates them is that this package
// is about a PATH ON A FILE SYSTEM, and a path has a path in it: a third
// component is required. `\\par\noindent` and `\\hbox\scriptstyle` have two;
// `\\FILESERVER\alicia$\thesis\main.tex` has four. A bare share root is given
// up deliberately, and a chain of three backslash-separated control words is not
// how TeX is written, braces being what groups its arguments.
var uncPath = regexp.MustCompile(`\\\\[A-Za-z0-9._-]{3,}\\([A-Za-z0-9._$-]{2,})((?:\\[A-Za-z0-9._-]+)+)`)

// genericAccounts are the home-directory names that identify a ROLE rather than
// a person: a runner, a container, a documentation example. They are the reason
// the account is captured rather than the prefix matched.
//
// ⛔ THE TEST IS IDENTIFIABILITY, NOT EXISTENCE. An earlier version of this
// comment refused to list "user" and "admin" on the grounds that they are real
// account names on real machines — which is true and measures the wrong thing.
// The harm this package exists to prevent is a text naming WHO someone is;
// `/home/user` and `/home/admin` name nobody, so publishing one discloses
// nothing, whether or not a machine somewhere has that account. What the list
// may not hold is a name that merely looks conventional while still pointing at
// a person.
//
// ⛔ Its cost, stated: a generic account's path is skipped WHOLE, so a document
// name in the tail (`/home/user/Documents/thesis.tex`) goes unreported too. That
// is the deliberate trade — reporting the tail of every such path is what made
// one sweep produce 38 findings on a single body of package names.
var genericAccounts = map[string]bool{
	// CI images and containers.
	"runner": true, "root": true, "ubuntu": true, "vagrant": true,
	"docker": true, "circleci": true, "travis": true, "jenkins": true,
	"ec2-user": true, "azureuser": true, "pi": true, "nobody": true,
	// Placeholders documentation uses.
	"USERNAME": true, "username": true, "you": true, "me": true,
	"<user>": true, "<name>": true, "example": true, "someone": true,
	"user": true, "admin": true, "Administrator": true,
	"test": true, "demo": true,
	// Shared, not personal. Measured 2026-11-09: `/Users/Shared/axc/work`, in
	// go-tex/engine#715, is macOS's directory for everyone on the machine.
	"Shared": true, "Guest": true, "linuxbrew": true, "homebrew": true,
}

// genericComponents are path components that say nothing about a person: the
// directories every account has, and the file names every repository has. A
// component on this list is still redacted inside a path — the path as a whole
// is the disclosure — but it is not hunted for on its own, because "Documents"
// or "README.md" in a sentence is prose.
var genericComponents = map[string]bool{
	"Documents": true, "Desktop": true, "Downloads": true, "Library": true,
	"Projects": true, "Developer": true, "Applications": true, "Public": true,
	"go": true, "src": true, "bin": true, "lib": true, "pkg": true, "tmp": true,
	"build": true, "dist": true, "target": true, "vendor": true, "node_modules": true,
	"localhost": true, "repos": true, "code": true, "work": true, "git": true,
	"README.md": true, "LICENSE": true, "go.mod": true, "go.sum": true,
	"main.tex": true, "main.go": true, "Makefile": true, "index.html": true,
}

// minBareName is the shortest learned component this package will hunt for on
// its own.
//
// ⛔ It exists so the bare-name pass cannot fire on prose. A path teaches it
// tokens; short ones like "a", "doc" or "out" occur in English sentences, and a
// gate that refuses those is one its author switches off. Anything shorter than
// this is still redacted INSIDE its path — only the standalone hunt is skipped.
const minBareName = 5

// minBareAccount is the shortest ACCOUNT this package will hunt on its own. It
// is lower than minBareName because the account is the person's name and the
// rule is centrally about it, but it is not 1.
//
// ⛔ Measured on 2026-11-09: a one-character name learned from a false path was
// hunted through 60 repositories and produced 276 of a sweep's 509 findings.
// Whatever the source, a name of one or two characters matches prose everywhere,
// and a gate that fires everywhere is one its author turns off. A shorter
// account is still reported INSIDE its path, where it is unambiguous.
const minBareAccount = 3

// Scan reports every place text names a person's own file system.
func Scan(text string) []Finding { return ScanKnown(text, nil) }

// Names returns the account, project and document names text discloses through
// a path, in the form the bare-name pass hunts for.
//
// ⛔⛔ IT EXISTS BECAUSE Scan CANNOT BE ITS OWN ORACLE. Scan learns that a word
// is a name BY READING IT IN A PATH, so once a redaction has removed the path,
// a surviving bare name is invisible to Scan — re-scanning redacted output
// reports clean no matter what survived. Measured on 2026-11-09: with the
// bare-name replacement deliberately disabled, a "nothing left for Scan to find"
// test stayed GREEN on every body. Whatever checks a redaction must hold the
// names from BEFORE it.
func Names(text string) []string {
	seen := map[string]bool{}
	var out []string
	keep := func(n string) {
		if n == "" || genericAccounts[n] || seen[n] || len(n) < minBareAccount {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	// ⛔ An ACCOUNT is hunted at any length, a component only from minBareName
	// up. The asymmetry is the rule's: the account IS the person's name, so
	// every occurrence of it is the disclosure the user asked to stop, however
	// short it is — while a 3-letter directory is a word in English sentences.
	add := func(n string) {
		if genericComponents[n] || len(n) < minBareName {
			return
		}
		keep(n)
	}
	for _, m := range homePath.FindAllStringSubmatch(text, -1) {
		if genericAccounts[m[1]] {
			continue
		}
		keep(m[1])
		for _, c := range strings.Split(m[2], "/") {
			for _, d := range strings.Split(c, `\`) {
				add(d)
			}
		}
	}
	for _, m := range uncPath.FindAllStringSubmatch(text, -1) {
		keep(strings.TrimSuffix(m[1], "$"))
		for _, c := range strings.Split(m[2], `\`) {
			add(c)
		}
	}
	sort.Strings(out)
	return out
}

// SelfNames returns the name of the account running the program, as a list to
// be handed to ScanKnown or RedactKnown.
//
// ⛔ Every gate hunts it whether or not a path in the text revealed it, because
// the sweep's and the gate's hardest case is a text that names the account with
// NO path in it: a session label, a signature, a sentence. A scanner that can
// only learn names from paths calls such a text clean, and that is most of what
// there is to read. The 2026-11-09 leak was exactly this shape — the path in the
// measurement section, the bare account in a label further down.
//
// ⛔ A generic account returns nothing. On a CI runner the home directory is
// /home/runner, and hunting "runner" would refuse every sentence about the
// runner — the false positive that teaches a reader to waive by reflex.
func SelfNames() []string {
	h, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	a := filepath.Base(h)
	if a == "" || a == "." || a == string(filepath.Separator) || genericAccounts[a] {
		return nil
	}
	return []string{a}
}

// Accounts returns only the account names, for a caller that wants to say whose
// machine a text named.
func Accounts(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range homePath.FindAllStringSubmatch(text, -1) {
		if a := m[1]; !genericAccounts[a] && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}

// ScanKnown is Scan with names supplied from outside.
//
// ⛔ The fleet sweep needs it: an issue body can name the account or the
// document with no path anywhere in it — the 2026-11-09 leak had the path in one
// section and the bare account in a session label further down, and a body
// holding only the label is exactly as disclosed. A scanner that can only infer
// names from paths finds nothing in such a body and reports it clean.
func ScanKnown(text string, known []string) []Finding {
	var out []Finding

	// name → what the scanner can honestly call it.
	//
	// ⛔ A name supplied from outside is labelled "name", not "account name":
	// the caller said to look for it, not what it is. Guessing the kind from the
	// text being scanned is what the first version did, and on redacted text —
	// where every path is already gone — it called every account a "document
	// name". A refusal that misnames what it holds sends the author looking for
	// the wrong thing.
	names := map[string]string{}
	for _, n := range known {
		if n != "" && !genericAccounts[n] && len(n) >= minBareAccount {
			names[n] = "name"
		}
	}

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		for _, loc := range homePath.FindAllStringSubmatchIndex(line, -1) {
			m := line[loc[0]:loc[1]]
			account := line[loc[2]:loc[3]]
			if genericAccounts[account] {
				continue
			}
			for _, n := range Names(m) {
				if n == account {
					names[n] = "account name"
				} else if names[n] == "" {
					names[n] = "document name"
				}
			}
			out = append(out, Finding{
				Line: i + 1, Col: loc[0] + 1, Match: m, Kind: "home path",
				Why: "a home directory names its account holder, their layout and their documents; the measurement is the content, the path is not",
			})
		}
		for _, loc := range uncPath.FindAllStringIndex(line, -1) {
			m := line[loc[0]:loc[1]]
			for _, n := range Names(m) {
				if names[n] == "" {
					names[n] = "name"
				}
			}
			out = append(out, Finding{
				Line: i + 1, Col: loc[0] + 1, Match: m, Kind: "unc path",
				Why: "a Windows share names a machine as well as a person",
			})
		}
	}

	// Second pass: the bare names, now that the paths have said which they are.
	// A name is only hunted once a path has shown it, so this never guesses at
	// which words are people or projects.
	for i, line := range lines {
		// No length or generic filter here: Names already applied it, and a name
		// a caller supplied explicitly is one it means. One filter, one place —
		// two copies of a predicate is how a gate and its fix drift apart.
		for name, kind := range names {
			for idx := 0; ; {
				j := strings.Index(line[idx:], name)
				if j < 0 {
					break
				}
				at := idx + j
				idx = at + len(name)
				// Skip occurrences that sit inside a path already reported on
				// this line, so one disclosure is not counted twice.
				if inPath(line, at) {
					continue
				}
				out = append(out, Finding{
					Line: i + 1, Col: at + 1, Match: name, Kind: kind,
					Why: "a name from a path, standing on its own",
				})
			}
		}
	}

	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Line != out[b].Line {
			return out[a].Line < out[b].Line
		}
		return out[a].Col < out[b].Col
	})
	return out
}

// inPath reports whether the byte at index sits inside a path match.
func inPath(line string, index int) bool {
	for _, re := range []*regexp.Regexp{homePath, uncPath} {
		for _, loc := range re.FindAllStringIndex(line, -1) {
			if index >= loc[0] && index < loc[1] {
				return true
			}
		}
	}
	return false
}

// Placeholder is what a redaction leaves where a path was.
//
// ⛔ It is deliberately not an empty string: a sentence that said "measured on
// /Users/alice/thesis/main.tex" must still read as a sentence afterwards, or the
// redaction produces prose nobody can follow and the next person restores the
// path to make it legible again.
const Placeholder = "<path elided>"

// NamePlaceholder replaces a bare name, and is distinct from Placeholder
// because the two read differently in a sentence: one stood where a location
// was, the other where a person or a document was.
const NamePlaceholder = "<name elided>"

// Redact rewrites text with every finding replaced, and reports how many.
//
// ⛔ It is a SEPARATE act from Scan, and both exist because neither is enough:
// scanning without redacting leaves the author to edit prose by hand at the
// moment they are most impatient, and redacting without scanning hides what was
// there. The count is returned so a caller can say what it changed rather than
// claim it was clean.
//
// ⛔⛔ IT MUST REMOVE EVERYTHING ScanKnown REFUSES, bare names included. The
// first version replaced the paths only, which made the gate and its fix
// disagree: `privscan --fix` would rewrite the body, the gate would refuse the
// result over the account name still in a session label, and the author — who
// had just run the fix — would reach for the waiver believing the gate broken.
// TestRedactLeavesNothingToFind is that contract, and it is checked against the
// names from before the redaction, because Scan goes blind once the path is
// gone.
func Redact(text string) (string, int) { return RedactKnown(text, nil) }

// RedactKnown is Redact with names supplied from outside, so that the fix
// covers exactly what ScanKnown refuses.
func RedactKnown(text string, known []string) (string, int) {
	n := 0
	names := map[string]bool{}
	for _, k := range known {
		if k != "" && !genericAccounts[k] && len(k) >= minBareAccount {
			names[k] = true
		}
	}

	out := homePath.ReplaceAllStringFunc(text, func(m string) string {
		if sub := homePath.FindStringSubmatch(m); len(sub) > 1 && genericAccounts[sub[1]] {
			return m
		}
		for _, k := range Names(m) {
			names[k] = true
		}
		n++
		return Placeholder
	})
	out = uncPath.ReplaceAllStringFunc(out, func(m string) string {
		for _, k := range Names(m) {
			names[k] = true
		}
		n++
		return Placeholder
	})

	// The bare names, now that the paths have said which they are. Longest
	// first, so a name that contains another is not half-replaced — the leak's
	// own path held both `fairshare-b300` and `fairshare-b300.tex`.
	ordered := make([]string, 0, len(names))
	for k := range names {
		ordered = append(ordered, k)
	}
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, k := range ordered {
		if c := strings.Count(out, k); c > 0 {
			out = strings.ReplaceAll(out, k, NamePlaceholder)
			n += c
		}
	}
	return out, n
}
