// i18ncheck reports where a translated Hugo content tree has drifted from the
// language it was translated from, in the four ways a translation must not
// change a page:
//
//   - a page missing in a language, or present in one language only;
//
//   - a fenced code block that is not byte-identical, in the same order: a
//     transcript is what a program printed, and a translated transcript is
//     one the program never printed;
//
//   - a heading whose anchor differs: the translation keeps the source
//     language's anchor as an explicit {#id}, so that every link into the
//     page keeps its fragment in every language;
//
//   - a relref target, or a weight, that differs: the same links, the same
//     place in the sidebar.
//
//     i18ncheck [-base en] <content-dir>
//
// <content-dir> holds one directory per language (content/en, content/fr...).
// It exits 1 when it finds a difference, 2 on a usage error, 0 otherwise, and
// says how many pages and blocks it compared, because a check that compared
// nothing passes everything.
//
// It was written after a night of translating go-authn's documentation into
// three languages with the same checks redone by hand -- an awk loop per run,
// one helper script per agent -- until three agents' helpers, sharing one
// name in one scratch directory, overwrote each other. A check that matters
// gets a home, a name nobody else uses, and tests.
//
// Heading anchors are computed as Hugo's default (autoHeadingIDType "github")
// computes them: lower case, letters, digits, "-" and "_" kept, a space
// becoming "-", everything else dropped, and "-1", "-2"... for repeats on one
// page. Inline code and emphasis markers and link targets are removed first,
// as rendering would.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

func main() {
	fleet.WarnIfStale(os.Stderr)
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = "usage: i18ncheck [-base en] <content-dir>"

// run is the whole program, so a test can drive it and read what it says.
func run(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("i18ncheck", flag.ContinueOnError)
	fl.SetOutput(stderr)
	base := fl.String("base", "en", "the language the others were translated from")
	comments := fl.Bool("comments", false, "let comments differ inside blocks whose language has them (hcl, sh, yaml, toml, ini, go...): a translated comment in a configuration example; output blocks stay byte-exact")
	if code, ok := fleet.ParseFlags(fl, args); !ok {
		return code
	}
	if fl.NArg() != 1 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	root := fl.Arg(0)
	langs, err := languages(root, *base)
	if err != nil {
		fmt.Fprintln(stderr, "i18ncheck:", err)
		return 2
	}
	rep, err := check(root, *base, langs, *comments)
	if err != nil {
		fmt.Fprintln(stderr, "i18ncheck:", err)
		return 2
	}
	for _, p := range rep.problems {
		fmt.Fprintln(stdout, p)
	}
	fmt.Fprintf(stdout, "%d pages in %s, compared with %s: %d code blocks, %d headings; %d problems\n",
		rep.pages, *base, strings.Join(langs, ", "), rep.blocks, rep.headings, len(rep.problems))
	if rep.pages == 0 {
		fmt.Fprintf(stderr, "i18ncheck: no page in %s: nothing was compared\n", filepath.Join(root, *base))
		return 2
	}
	if len(rep.problems) > 0 {
		return 1
	}
	return 0
}

// languages are the directories of root other than base.
func languages(root, base string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var langs []string
	found := false
	for _, e := range entries {
		switch {
		case !e.IsDir():
		case e.Name() == base:
			found = true
		default:
			langs = append(langs, e.Name())
		}
	}
	if !found {
		return nil, fmt.Errorf("%s has no %s directory", root, base)
	}
	if len(langs) == 0 {
		return nil, fmt.Errorf("%s has no language besides %s: nothing to compare", root, base)
	}
	sort.Strings(langs)
	return langs, nil
}

type report struct {
	pages, blocks, headings int
	problems                []string
}

func pagesOf(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

func check(root, base string, langs []string, comments bool) (report, error) {
	var rep report
	basePages, err := pagesOf(filepath.Join(root, base))
	if err != nil {
		return rep, err
	}
	rep.pages = len(basePages)
	for _, lang := range langs {
		pages, err := pagesOf(filepath.Join(root, lang))
		if err != nil {
			return rep, err
		}
		for _, p := range basePages {
			if !slices.Contains(pages, p) {
				rep.problems = append(rep.problems, fmt.Sprintf("%s/%s: missing (it is in %s)", lang, p, base))
			}
		}
		for _, p := range pages {
			if !slices.Contains(basePages, p) {
				rep.problems = append(rep.problems, fmt.Sprintf("%s/%s: not in %s", lang, p, base))
			}
		}
	}
	for _, p := range basePages {
		src, err := parseFile(filepath.Join(root, base, p))
		if err != nil {
			return rep, err
		}
		rep.blocks += len(src.blocks) * len(langs)
		rep.headings += len(src.headings) * len(langs)
		for _, lang := range langs {
			tr, err := parseFile(filepath.Join(root, lang, p))
			if errors.Is(err, fs.ErrNotExist) {
				continue // reported above
			}
			if err != nil {
				return rep, err
			}
			for _, d := range compare(src, tr, comments) {
				rep.problems = append(rep.problems, fmt.Sprintf("%s/%s: %s", lang, p, d))
			}
		}
	}
	return rep, nil
}

// page is what a translation must keep from its source.
type page struct {
	weight   string
	blocks   []string
	langs    []string // each block's fence info, e.g. "hcl"; "" for output
	headings []heading
	relrefs  []string
}

type heading struct {
	level int
	id    string // explicit {#id}, or computed as Hugo does
	line  int
}

var (
	fenceRE   = regexp.MustCompile("^[ \t]*(```|~~~)[ \t]*([A-Za-z0-9_+-]*)")
	headingRE = regexp.MustCompile(`^(#{1,6})[ \t]+(.*?)[ \t]*$`)
	explicit  = regexp.MustCompile(`[ \t]*\{#([^}\s]+)\}$`)
	relrefRE  = regexp.MustCompile(`relref "([^"]+)"`)
	weightRE  = regexp.MustCompile(`^weight:\s*(\S+)`)
)

func parseFile(path string) (page, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return page{}, err
	}
	return parse(b), nil
}

// parse reads a page: its front matter's weight, its fenced blocks verbatim
// (fence lines included), its headings outside fences, and its relrefs.
func parse(b []byte) page {
	var p page
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	n := 0
	inFront, inFence := false, false
	var fence, block strings.Builder
	var lang string
	seen := map[string]int{}
	for sc.Scan() {
		line := sc.Text()
		n++
		if n == 1 && line == "---" {
			inFront = true
			continue
		}
		if inFront {
			if line == "---" {
				inFront = false
			} else if m := weightRE.FindStringSubmatch(line); m != nil {
				p.weight = m[1]
			}
			continue
		}
		if m := fenceRE.FindStringSubmatch(line); m != nil {
			if !inFence {
				inFence = true
				fence.Reset()
				fence.WriteString(m[1])
				lang = m[2]
				block.Reset()
			} else if strings.TrimSpace(line)[:3] == fence.String() {
				inFence = false
				block.WriteString(line)
				p.blocks = append(p.blocks, block.String())
				p.langs = append(p.langs, lang)
				continue
			}
		}
		if inFence {
			block.WriteString(line)
			block.WriteByte('\n')
			continue
		}
		for _, m := range relrefRE.FindAllStringSubmatch(line, -1) {
			p.relrefs = append(p.relrefs, m[1])
		}
		if m := headingRE.FindStringSubmatch(line); m != nil {
			text := m[2]
			var id string
			if e := explicit.FindStringSubmatch(text); e != nil {
				id = e[1]
			} else {
				id = anchor(text)
				// Hugo numbers a repeated anchor on one page.
				if k := seen[id]; k > 0 {
					seen[id] = k + 1
					id = fmt.Sprintf("%s-%d", id, k)
				} else {
					seen[id] = 1
				}
			}
			p.headings = append(p.headings, heading{level: len(m[1]), id: id, line: n})
		}
	}
	return p
}

var (
	linkRE = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	// Shortcodes in a heading render to something else entirely; dropped.
	shortcodeRE = regexp.MustCompile(`\{\{[<%].*?[%>]\}\}`)
)

// anchor is a heading's id as Hugo's "github" autoHeadingIDType makes it.
func anchor(text string) string {
	text = shortcodeRE.ReplaceAllString(text, "")
	text = linkRE.ReplaceAllString(text, "$1")
	text = strings.NewReplacer("`", "", "**", "", "*", "").Replace(text)
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// compare lists what tr changed of what it must keep from src.
func compare(src, tr page, comments bool) []string {
	var d []string
	if src.weight != tr.weight {
		d = append(d, fmt.Sprintf("weight %q, the source says %q", tr.weight, src.weight))
	}
	if len(src.blocks) != len(tr.blocks) {
		d = append(d, fmt.Sprintf("%d code blocks, the source has %d", len(tr.blocks), len(src.blocks)))
	} else {
		for i := range src.blocks {
			a, b := src.blocks[i], tr.blocks[i]
			if comments {
				a, b = stripComments(src.langs[i], a), stripComments(src.langs[i], b)
			}
			if a != b {
				d = append(d, fmt.Sprintf("code block %d differs from the source's: %s", i+1, firstDifference(src.blocks[i], tr.blocks[i])))
			}
		}
	}
	if len(src.headings) != len(tr.headings) {
		d = append(d, fmt.Sprintf("%d headings, the source has %d", len(tr.headings), len(src.headings)))
	} else {
		for i, h := range src.headings {
			t := tr.headings[i]
			switch {
			case h.level != t.level:
				d = append(d, fmt.Sprintf("line %d: heading level %d, the source's is %d", t.line, t.level, h.level))
			case h.id != t.id:
				d = append(d, fmt.Sprintf("line %d: anchor #%s, the source's is #%s (keep it with {#%s})", t.line, t.id, h.id, h.id))
			}
		}
	}
	if !slices.Equal(sortedCopy(src.relrefs), sortedCopy(tr.relrefs)) {
		d = append(d, fmt.Sprintf("relref targets %v, the source has %v", tr.relrefs, src.relrefs))
	}
	return d
}

func sortedCopy(s []string) []string {
	c := slices.Clone(s)
	sort.Strings(c)
	return c
}

// firstDifference names the first line two blocks disagree on.
func firstDifference(a, b string) string {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(la) && i < len(lb); i++ {
		if la[i] != lb[i] {
			return fmt.Sprintf("line %d, %q instead of %q", i+1, lb[i], la[i])
		}
	}
	return fmt.Sprintf("%d lines instead of %d", len(lb), len(la))
}

// commentMarkers are what starts a comment in a block of each language that
// has comments. A block with no language, or one not here -- the output of a
// program -- has none: it is compared byte for byte whatever -comments says.
var commentMarkers = map[string][]string{
	"hcl": {"#", "//"}, "terraform": {"#", "//"},
	"sh": {"#"}, "bash": {"#"}, "shell": {"#"}, "zsh": {"#"},
	"yaml": {"#"}, "yml": {"#"}, "toml": {"#"}, "ini": {"#", ";"}, "conf": {"#"},
	"go": {"//"}, "nginx": {"#"}, "sshconfig": {"#"},
}

// stripComments drops the comments of a block in lang -- a marker outside a
// quoted string, to the end of its line -- and the spaces before them, so a
// configuration example whose comments were translated compares equal to
// its source and one whose VALUES changed does not. "...#login.example.org"
// inside quotes is a value, not a comment.
func stripComments(lang, block string) string {
	markers := commentMarkers[strings.ToLower(lang)]
	if len(markers) == 0 {
		return block
	}
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		if i == 0 || i == len(lines)-1 && fenceRE.MatchString(line) {
			continue // the fence lines themselves
		}
		lines[i] = strings.TrimRight(cutComment(line, markers), " \t")
	}
	return strings.Join(lines, "\n")
}

func cutComment(line string, markers []string) string {
	var quote rune
	escaped := false
	for i, r := range line {
		switch {
		case escaped:
			escaped = false
		case quote != 0:
			if r == '\\' {
				escaped = true
			} else if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		default:
			for _, m := range markers {
				// A marker starts a comment at the line's start or after a
				// space: "http://x" and "a#b" are not comments.
				if strings.HasPrefix(line[i:], m) && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
					return line[:i]
				}
			}
		}
	}
	return line
}
