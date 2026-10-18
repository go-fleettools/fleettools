// langcheck reports where this fleet's own prose on GitHub is not in English.
//
// The rule it measures is a standing one: everything we author on a public
// repository — doc comments, code comments, test case names, README and
// documentation prose — is in English, because a public repository is a
// multi-lingual surface and French-only content excludes the people who have to
// read it. The session where the work is decided happens in French; the artefact
// does not.
//
//	langcheck [-min 2] [-v] <path>...
//
// It exits 1 when it finds something, 2 on a usage error, 0 otherwise, and it
// always says how many files and blocks it compared — a check that read nothing
// passes everything.
//
// # Why a parser and not a grep
//
// A grep over `.go` files for French words answers the wrong question. Measured
// on 2026-10-18 across 987 local checkouts, a word-list grep named 855 files in
// 25 repositories — and `go-tex/engine` alone accounted for 317 of them, because
// a TeX engine's test fixtures are full of French DOCUMENTS. Those are data
// under test, not prose we wrote, and a tool that cannot tell them apart names
// everybody and is therefore ignored.
//
// So for Go files this reads the COMMENTS, through go/parser. A string literal,
// a testdata document, a fixture holding a French sentence on purpose: none of
// them is a comment, so none of them is reported. That removes the entire
// false-positive class rather than filtering it afterwards.
//
// For Markdown it reads the prose, minus fenced code blocks — a transcript is
// what a program printed, and what a program printed is not ours to translate.
//
// # What it looks for, and what it cannot do
//
// It looks for FRENCH specifically, by a list of function words that are not
// also English words, and it needs -min of them in one block before it says
// anything. It is not a language identifier: it would not notice German, and it
// is not meant to. The risk being measured is the one this fleet actually has,
// which is that the person writing it thinks in French.
//
// A block of English that quotes a French phrase in passing can reach the
// threshold. That is why the threshold exists, why -v prints the words that
// matched, and why the exit status is a report rather than a gate in CI until
// somebody decides it should be.
//
// Measured, so the rate is a number and not a worry: on go-fileshare/docs —
// 200 files, 5243 blocks, a site deliberately translated into three languages —
// it reports exactly ONE block, and that block is this case:
//
//  1. `content/<lang>/`: a translation of every page … A translated heading
//     keeps the English one's anchor as an explicit id
//     (`## Certificats destinés à cet hôte …`)
//
// English documentation quoting a French heading as its example. `-v` shows
// `cet ·accent`, which is enough for a reader to dismiss it in a second. One in
// 5243 is the residue after the comment unit, the worktree skip and the
// translation skip have each removed a whole class.
package main

import (
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

// french holds function words that are French and are NOT English words.
//
// ⛔ EVERY ENTRY HERE IS A FALSE-POSITIVE RISK, so the ambiguous ones are left
// out on purpose, however frequent they are in French: "a", "on", "me", "son",
// "plus", "pas", "est", "par", "sur", "note", "mode", "point", "page", "image",
// "format", "signal", "patient" and "important" all mean something in English
// too. A list that named them would report an English comment about a signal.
var french = map[string]bool{
	"qui": true, "que": true, "quoi": true, "dont": true, "donc": true,
	"dans": true, "pour": true, "avec": true, "sans": true, "sous": true,
	"entre": true, "vers": true, "chez": true, "depuis": true,
	"cette": true, "cet": true, "ces": true, "celui": true, "celle": true,
	"ceux": true, "celles": true,
	"une": true, "des": true, "aux": true, "les": true, "lui": true,
	"leur": true, "leurs": true, "elle": true, "elles": true, "ils": true,
	"nous": true, "vous": true, "eux": true,
	"alors": true, "ainsi": true, "aussi": true, "encore": true,
	"ensuite": true, "enfin": true, "toujours": true, "jamais": true,
	"déjà": true, "ici": true, "parfois": true, "souvent": true,
	"mais": true, "parce": true, "lorsque": true, "puisque": true,
	"afin": true, "comme": true, "quand": true, "tandis": true,
	"tout": true, "tous": true, "toute": true, "toutes": true,
	"chaque": true, "même": true, "mêmes": true, "aucun": true, "aucune": true,
	"rien": true, "quelque": true, "quelques": true, "plusieurs": true,
	"sont": true, "ont": true, "était": true, "étaient": true, "étant": true,
	"être": true, "avoir": true, "avait": true, "avaient": true,
	"faire": true, "fait": true, "faut": true, "peut": true, "peuvent": true,
	"doit": true, "doivent": true, "veut": true, "sait": true,
	"c'est": true, "n'est": true, "qu'il": true, "qu'elle": true,
	"d'une": true, "d'un": true, "l'on": true, "s'il": true, "jusqu'à": true,
	// ⭐ The short, very frequent ones. They were added after the first version
	// missed a TEST CASE NAME entirely — `{"après go build | tail (tel quel)"}`
	// contained not one word of the list above, because a case name is six
	// words long and the list was built for sentences.
	"le": true, "du": true, "au": true, "ne": true, "pas": true, "ou": true,
	"après": true, "très": true, "où": true, "quel": true, "quelle": true,
	"haut": true, "deux": true, "trois": true,
}

// accented reports whether text carries a Latin letter with a diacritic.
//
// ⭐ IT COUNTS AS ONE WORD, which is what makes a short block decidable: a case
// name has no room for two function words, and `séparés` or `après` is a strong
// signal on its own. English prose reaches for one only in a loanword — café,
// naïve, résumé — and one point is not a report.
//
// It deliberately ignores anything after "://" so a percent-escaped URL or a
// host name cannot score.
func accented(text string) bool {
	if i := strings.Index(text, "://"); i >= 0 {
		text = text[:i]
	}
	for _, r := range text {
		if r < 128 {
			continue
		}
		switch {
		case r >= 0x00C0 && r <= 0x00FF && r != 0x00D7 && r != 0x00F7:
			return true
		case r >= 0x0100 && r <= 0x017F:
			return true
		}
	}
	return false
}

// Finding is one block of prose that reads as French.
type Finding struct {
	File  string
	Line  int
	Words []string
	Text  string
}

func main() {
	// ⛔ FIRST, because a binary built last week answers for last week's rules
	// and nothing in its output would say so.
	fleet.WarnIfStale(os.Stderr)
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("langcheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	min := flags.Int("min", 2, "how many French signals in one block before it is reported")
	verbose := flags.Bool("v", false, "print the signals that matched and the first line of the block")
	// ⛔ Asking for help is not a usage error, which is why this goes through
	// fleet.ParseFlags: a command that answered 2 to -h would be reported as
	// broken by whatever checks exit statuses, and a fleet tool that RAN on -h
	// has already cost a full sweep here.
	if code, ok := fleet.ParseFlags(flags, args); !ok {
		return code
	}
	if *min < 1 {
		fmt.Fprintln(stderr, "langcheck: -min must be at least 1; a threshold of zero reports every block")
		return 2
	}
	roots := flags.Args()
	if len(roots) == 0 {
		roots = []string{"."}
	}

	var found []Finding
	files, blocks := 0, 0
	for _, root := range roots {
		f, b, got, err := walk(root, *min)
		if err != nil {
			fmt.Fprintf(stderr, "langcheck: %v\n", err)
			return 2
		}
		files += f
		blocks += b
		found = append(found, got...)
	}

	// ⛔ Say what was read, always. A check that walked nothing passes
	// everything, and the number is the only thing that distinguishes the two.
	fmt.Fprintf(stdout, "read %d file(s), %d block(s) of prose\n", files, blocks)
	if len(found) == 0 {
		fmt.Fprintln(stdout, "every block reads as English")
		return 0
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].File != found[j].File {
			return found[i].File < found[j].File
		}
		return found[i].Line < found[j].Line
	})
	for _, f := range found {
		if *verbose {
			fmt.Fprintf(stdout, "%s:%d: %s — %s\n", f.File, f.Line,
				strings.Join(f.Words, " "), firstLine(f.Text))
		} else {
			fmt.Fprintf(stdout, "%s:%d: %s\n", f.File, f.Line, strings.Join(f.Words, " "))
		}
	}
	fmt.Fprintf(stdout, "%d block(s) read as French\n", len(found))
	return 1
}

// skipDir names the directories whose contents are not ours to translate, or
// are the same prose counted again.
//
// `testdata` is the important one for correctness: a French document under test
// is the subject, not the prose. `translations` holds deliberate translations.
//
// ⛔ `worktrees` AND `.claude` ARE NOT TIDINESS, THEY ARE THE COUNT. The first
// reading of go-tex/engine said 2098 French blocks; sampling ten of them —
// instead of trusting the headline — showed the SAME file at the SAME line ten
// times over, once per per-session agent worktree under `.claude/worktrees/`.
// `*-worktrees` was already skipped, which is the sibling spelling and is what
// made the gap invisible. A per-repository count doubles its mirrors exactly as
// it doubles its worktrees, and a number nobody sampled is a number nobody has
// checked.
// ⛔ AND A TRANSLATION IS NOT A DEFECT. Measured on go-fileshare/docs before
// this clause existed: 884 blocks reported, of which **510** were the
// deliberate French translation under `content/fr`. A tool that is 58% wrong on
// a multilingual site is a tool nobody runs twice.
//
// The rule is structural rather than a list of directory names: a language code
// sitting directly under `content`, `i18n` or `locales` is a translation, which
// is the only place Hugo puts one. A directory called `fr` anywhere else — say
// `pkg/fr` — is code, and is read.
func skipDir(path, name string) bool {
	switch name {
	case ".git", ".claude", "vendor", "node_modules", "worktrees",
		"testdata", "translations", "fixtures", "corpus":
		return true
	}
	if strings.HasSuffix(name, "-worktrees") {
		return true
	}
	switch filepath.Base(filepath.Dir(path)) {
	case "content", "i18n", "locales", "lang":
		return isTranslationCode(name)
	}
	return false
}

// isTranslationCode reports whether name is a language tag other than English:
// `fr`, `pt-br`, `zh-Hans`. English is NOT one, because `content/en` is the
// prose this tool exists to read.
func isTranslationCode(name string) bool {
	base, _, _ := strings.Cut(name, "-")
	if len(base) != 2 {
		return false
	}
	for i := 0; i < len(base); i++ {
		if base[i] < 'a' || base[i] > 'z' {
			return false
		}
	}
	return base != "en"
}

func walk(root string, min int) (files, blocks int, found []Finding, err error) {
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable directory is not an opinion
		}
		if d.IsDir() {
			if skipDir(path, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		var got []Finding
		var n int
		switch filepath.Ext(path) {
		case ".go":
			got, n, err = scanGo(path, min)
		case ".md":
			got, n, err = scanMarkdown(path, min)
		default:
			return nil
		}
		if err != nil {
			return nil // a file that will not parse is not an opinion either
		}
		files++
		blocks += n
		found = append(found, got...)
		return nil
	})
	return files, blocks, found, err
}

// scanGo reads a Go file's COMMENTS, which is the whole point: a string literal
// holding a French sentence is data, and data is not prose we wrote.
func scanGo(path string, min int) ([]Finding, int, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, 0, err
	}
	var found []Finding
	blocks := 0
	for _, g := range f.Comments {
		blocks++
		text := g.Text()
		if w := frenchWords(text); len(w) >= min {
			found = append(found, Finding{
				File:  path,
				Line:  fset.Position(g.Pos()).Line,
				Words: w,
				Text:  text,
			})
		}
	}
	// ⭐ A test's CASE NAMES are prose too, and they are the half a comment scan
	// misses: `{"après go build | tail", …}` is French on a public repository
	// and lives in a string. They are read from the literals of composite
	// values only in _test.go files, where a string is overwhelmingly a name or
	// a message rather than data.
	if strings.HasSuffix(path, "_test.go") {
		for i, lit := range stringLiterals(src) {
			_ = i
			if w := frenchWords(lit.text); len(w) >= min {
				blocks++
				found = append(found, Finding{
					File:  path,
					Line:  lineOf(src, lit.off),
					Words: w,
					Text:  lit.text,
				})
			}
		}
	}
	return found, blocks, nil
}

// scanMarkdown reads prose, minus fenced code blocks: a transcript is what a
// program printed and is not ours to translate.
func scanMarkdown(path string, min int) ([]Finding, int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	lines := strings.Split(string(b), "\n")
	var found []Finding
	blocks := 0
	fenced := false
	var para []string
	start := 0
	flush := func(end int) {
		if len(para) == 0 {
			return
		}
		blocks++
		text := strings.Join(para, " ")
		if w := frenchWords(text); len(w) >= min {
			found = append(found, Finding{File: path, Line: start + 1, Words: w, Text: text})
		}
		para = nil
	}
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			flush(i)
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if t == "" {
			flush(i)
			continue
		}
		if len(para) == 0 {
			start = i
		}
		para = append(para, t)
	}
	flush(len(lines))
	return found, blocks, nil
}

// frenchWords returns the distinct French signals in text: the function words
// it found, plus the pseudo-word "·accent" when it carries a diacritic.
//
// The accent is reported in the list rather than hidden in a score, so a reader
// of the output can see WHY a block was named and disagree with it.
func frenchWords(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range tokens(text) {
		if french[w] && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	sort.Strings(out)
	if accented(text) {
		out = append(out, "·accent")
	}
	return out
}

// tokens lower-cases and splits on everything that is not a letter or an
// apostrophe, because the elided forms — c'est, n'est, qu'il — are the clearest
// markers there are and splitting on the apostrophe would destroy them.
func tokens(text string) []string {
	var out []string
	cur := strings.Builder{}
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || r == '\'' || r == '’' {
			if r == '’' {
				r = '\''
			}
			cur.WriteRune(r)
			continue
		}
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

type strLit struct {
	text string
	off  int
}

// stringLiterals finds double-quoted and back-quoted literals, crudely but
// without a parser: this runs only on _test.go files, where the cost of missing
// one is a name left untranslated rather than a wrong verdict.
func stringLiterals(src []byte) []strLit {
	var out []strLit
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '/':
			// Skip comments, which scanGo has already read.
			if i+1 < len(src) && src[i+1] == '/' {
				for i < len(src) && src[i] != '\n' {
					i++
				}
			} else if i+1 < len(src) && src[i+1] == '*' {
				i += 2
				for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
					i++
				}
				i++
			}
		case '"', '`':
			q := src[i]
			start := i + 1
			i++
			for i < len(src) && src[i] != q {
				if q == '"' && src[i] == '\\' {
					i++
				}
				if q == '"' && src[i] == '\n' {
					break // an unterminated literal: give up on it
				}
				i++
			}
			if end := min2(i, len(src)); start <= end {
				out = append(out, strLit{text: string(src[start:end]), off: start})
			}
		}
	}
	return out
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func lineOf(src []byte, off int) int {
	n := 1
	for i := 0; i < off && i < len(src); i++ {
		if src[i] == '\n' {
			n++
		}
	}
	return n
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 90 {
		s = s[:90] + "…"
	}
	return s
}
