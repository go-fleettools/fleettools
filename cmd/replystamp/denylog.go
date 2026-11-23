package main

// The deny log: what replystamp READ when it refused.
//
// ⛔ A REFUSAL NOBODY CAN REPLAY IS A REFUSAL ONE CAN ONLY BELIEVE OR DISBELIEVE.
// On 2026-11-22 the model twice called a deny false. A replay of the transcript
// (a throwaway dump program) proved both correct -- the turn had opened with a
// tool_use and no text -- but the program had to be written AFTER the dispute,
// from a transcript that keeps growing. So every deny now leaves one JSON line
// naming the anchor it judged from and every entry between that anchor and the
// call it refused, and `replystamp why` prints them.
//
// What it stores, and what it does not. Per entry: line number, type, uuid and
// the KIND of each content block (text / thinking / tool_use / tool_result ...);
// for a text block only its first 40 runes. Never a full text, never a tool
// input or a tool result (those can hold secrets). The file is 0600.
//
// The bound: the log rotates at 1 MiB (denials.jsonl -> denials.jsonl.1,
// replacing the older one), so it never holds more than about 2 MiB plus one
// record; one record keeps at most 200 entries (the first 100 and the last 100,
// with the count omitted) of at most 16 blocks each, so a record stays under
// roughly 60 KiB. Sessions share the file and each record is one O_APPEND write;
// a rotation racing an append can lose a line, which is a diagnostic's price.
//
// A failure to log NEVER changes the verdict: the deny is emitted either way,
// and the path is added to the deny message only when the record was written.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/go-fleettools/fleettools/internal/cli"
)

const (
	denyLogName      = "denials.jsonl"
	maxLogBytes      = 1 << 20
	maxTraceEntries  = 200
	maxBlocksPerItem = 16
	textPrefixRunes  = 40
)

// traceItem is one transcript entry between the anchor and the judged call.
type traceItem struct {
	Line   int      `json:"line"`
	Type   string   `json:"type"`
	UUID   string   `json:"uuid,omitempty"`
	Blocks []string `json:"blocks,omitempty"`
	// Texts holds the first 40 runes of each text block, in order.
	Texts  []string `json:"texts,omitempty"`
	Judged bool     `json:"judged,omitempty"`

	ids []string // tool_use ids, to find the judged call; never written
}

type traceAnchor struct {
	Line int    `json:"line"`
	UUID string `json:"uuid"`
	Kind string `json:"kind"` // user, compaction or notification
}

// denyRecord is one line of the log.
type denyRecord struct {
	Time       string      `json:"time"`
	Hook       string      `json:"hook"` // pretool or stop
	Why        string      `json:"why"`  // no-text, missing, twelve-hour ...
	Session    string      `json:"session"`
	Transcript string      `json:"transcript"`
	ToolUseID  string      `json:"tool_use_id,omitempty"`
	Anchor     traceAnchor `json:"anchor"`
	Omitted    int         `json:"omitted,omitempty"`
	Entries    []traceItem `json:"entries"`
	TraceError string      `json:"trace_error,omitempty"`
}

var kindLabels = map[kind]string{
	missing:        "missing",
	twelveHour:     "twelve-hour",
	impossibleHour: "impossible-hour",
	stale:          "stale",
}

func denyLogPath() (string, bool) {
	dir, err := stateDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(dir, denyLogName), true
}

// anchorKind says what opened the turn.
func anchorKind(e entry) string {
	switch {
	case e.IsCompactSummary:
		return "compaction"
	case e.TurnOrigin == "task_notification" || e.PromptSource == "system":
		return "notification"
	}
	return "user"
}

// prefix returns the first textPrefixRunes runes of s.
func prefix(s string) string {
	n := 0
	for i := range s {
		if n == textPrefixRunes {
			return s[:i]
		}
		n++
	}
	return s
}

// summarise lists the kinds of the content blocks and the text prefixes.
func summarise(raw json.RawMessage) (blocks, texts []string) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{"text"}, []string{prefix(s)}
	}
	var bs []block
	if json.Unmarshal(raw, &bs) != nil {
		return nil, nil
	}
	for i, b := range bs {
		if i == maxBlocksPerItem {
			blocks = append(blocks, fmt.Sprintf("+%d more", len(bs)-i))
			break
		}
		blocks = append(blocks, prefixKind(b.Type))
		if b.Type == "text" {
			texts = append(texts, prefix(b.Text))
		}
	}
	return blocks, texts
}

// prefixKind bounds a block type read from the transcript.
func prefixKind(t string) string {
	if len(t) > 24 {
		return t[:24]
	}
	return t
}

// traceFound is what the window walk returns.
type traceFound struct {
	anchor    traceAnchor
	anchorOff int64
	items     []traceItem
}

// traceNewest walks buf from the end to the newest genuine user turn, as
// newestTurn does, keeping every entry on the way. Line numbers are relative to
// the anchor here (anchor = 0); the caller adds the anchor's absolute line.
func traceNewest(buf []byte, base int64) (traceFound, bool) {
	type got struct {
		start int
		item  traceItem
		e     entry
	}
	var rev []got
	reached := false
	for end := len(buf); end > 0; {
		start := bytes.LastIndexByte(buf[:end], '\n') + 1
		line := buf[start:end]
		end = start - 1
		e, m, ok := decodeEntry(line)
		if !ok {
			continue
		}
		blocks, texts := summarise(m.Content)
		rev = append(rev, got{start, traceItem{Type: e.Type, UUID: e.UUID, Blocks: blocks, Texts: texts, ids: toolUseIDs(m.Content)}, e})
		if e.Type == "user" && isUserTurn(e, m) {
			reached = true
			break
		}
	}
	if !reached {
		// The window ended before a user turn: let the scan widen it.
		return traceFound{}, false
	}
	a := rev[len(rev)-1]
	f := traceFound{
		anchor:    traceAnchor{UUID: a.e.UUID, Kind: anchorKind(a.e)},
		anchorOff: base + int64(a.start),
	}
	prev := a.start
	delta := 0
	for i := len(rev) - 1; i >= 0; i-- {
		delta += bytes.Count(buf[prev:rev[i].start], []byte{'\n'})
		prev = rev[i].start
		it := rev[i].item
		it.Line = delta
		f.items = append(f.items, it)
	}
	return f, true
}

// traceTurn reads the newest turn of the transcript at path, up to the call
// toolUseID when it is present.
func traceTurn(path, toolUseID string) (traceAnchor, []traceItem, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return traceAnchor{}, nil, 0, err
	}
	defer f.Close()
	return traceOf(f, toolUseID)
}

// traceOf is traceTurn over anything statReaderAt describes.
func traceOf(f statReaderAt, toolUseID string) (traceAnchor, []traceItem, int, error) {
	st, err := f.Stat()
	if err != nil {
		return traceAnchor{}, nil, 0, err
	}
	found, _, err := tailScan(f, st.Size(), traceNewest)
	if err != nil {
		return traceAnchor{}, nil, 0, err
	}
	if len(found.items) == 0 {
		return traceAnchor{}, nil, 0, fmt.Errorf("no user turn located in the transcript")
	}
	base, err := lineAt(f, found.anchorOff)
	if err != nil {
		return traceAnchor{}, nil, 0, err
	}
	found.anchor.Line = base
	items := found.items
	for i := range items {
		items[i].Line += base
	}
	if toolUseID != "" {
	judged:
		for i, it := range items {
			for _, id := range it.ids {
				if id == toolUseID {
					items[i].Judged = true
					items = items[:i+1]
					break judged
				}
			}
		}
	}
	omitted := 0
	if len(items) > maxTraceEntries {
		half := maxTraceEntries / 2
		omitted = len(items) - maxTraceEntries
		items = append(append([]traceItem{}, items[:half]...), items[len(items)-half:]...)
	}
	return found.anchor, items, omitted, nil
}

// lineAt returns the 1-based number of the line that starts at byte offset off.
func lineAt(r io.ReaderAt, off int64) (int, error) {
	buf := make([]byte, 64<<10)
	n := 1
	for pos := int64(0); pos < off; {
		want := int64(len(buf))
		if off-pos < want {
			want = off - pos
		}
		k, err := r.ReadAt(buf[:want], pos)
		n += bytes.Count(buf[:k], []byte{'\n'})
		pos += int64(k)
		if err != nil && int64(k) < want {
			return 0, err
		}
	}
	return n, nil
}

// recordDeny appends the record of a deny to the log and returns the log's
// path, or "" when it could not write one. It never fails the caller.
func recordDeny(hook, why, session, transcript, toolUseID string) string {
	path, ok := denyLogPath()
	if !ok {
		return ""
	}
	rec := denyRecord{Time: now().Format(time.RFC3339), Hook: hook, Why: why,
		Session: session, Transcript: transcript, ToolUseID: toolUseID}
	anchor, items, omitted, err := traceTurn(transcript, toolUseID)
	if err != nil {
		rec.TraceError = err.Error()
	}
	rec.Anchor, rec.Entries, rec.Omitted = anchor, items, omitted
	// A struct of strings, ints and slices of them always encodes.
	b, _ := json.Marshal(rec)
	b = append(b, '\n')
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return ""
	}
	if st, err := os.Stat(path); err == nil && st.Size() >= maxLogBytes {
		_ = os.Rename(path, path+".1")
	}
	f, err := openLog(path)
	if err != nil {
		return ""
	}
	if appendTo(f, b) {
		return path
	}
	return ""
}

// openLog is the seam a test replaces to make the write fail.
var openLog = func(path string) (io.WriteCloser, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
}

// appendTo writes b in ONE write and closes w; it reports whether both held.
func appendTo(w io.WriteCloser, b []byte) bool {
	_, werr := w.Write(b)
	cerr := w.Close()
	return werr == nil && cerr == nil
}

// logged adds the log's path to a deny message.
func logged(reason, path string) string {
	if path == "" {
		return reason
	}
	return reason + "\nWhat this deny judged is recorded in " + path + " -- read it with `replystamp why`."
}

// readDenials returns the last n records, oldest first, and how many lines
// could not be decoded.
func readDenials(n int) (recs []denyRecord, bad int, path string, err error) {
	path, ok := denyLogPath()
	if !ok {
		return nil, 0, "", fmt.Errorf("no state directory to read the deny log from")
	}
	for _, p := range []string{path + ".1", path} {
		// A half that is missing or unreadable reads as empty: ReadFile
		// returns no bytes with its error.
		b, _ := os.ReadFile(p)
		for _, l := range bytes.Split(b, []byte{'\n'}) {
			if len(bytes.TrimSpace(l)) == 0 {
				continue
			}
			var r denyRecord
			if json.Unmarshal(l, &r) != nil {
				bad++
				continue
			}
			recs = append(recs, r)
		}
	}
	if len(recs) > n {
		recs = recs[len(recs)-n:]
	}
	return recs, bad, path, nil
}

func (r denyRecord) print(w io.Writer) {
	fmt.Fprintf(w, "%s  %s  %s  session %s\n", r.Time, r.Hook, r.Why, r.Session)
	fmt.Fprintf(w, "  transcript %s\n", r.Transcript)
	if r.TraceError != "" {
		fmt.Fprintf(w, "  could not trace the turn: %s\n", r.TraceError)
		return
	}
	fmt.Fprintf(w, "  anchor: line %d  %s  uuid %s\n", r.Anchor.Line, r.Anchor.Kind, r.Anchor.UUID)
	for i, it := range r.Entries {
		if r.Omitted > 0 && i == len(r.Entries)/2 {
			fmt.Fprintf(w, "  ... %d entries omitted ...\n", r.Omitted)
		}
		mark := ""
		if it.Judged {
			mark = "   <- the refused call"
		}
		fmt.Fprintf(w, "  line %d  %-9s [%s]%s\n", it.Line, it.Type, strings.Join(it.Blocks, " "), mark)
		for _, t := range it.Texts {
			fmt.Fprintf(w, "      text: %q\n", t)
		}
	}
}

func whyCmd() *cobra.Command {
	var last int
	c := &cobra.Command{
		Use:   "why [--last N]",
		Short: "print what the latest denials judged",
		Long: `Prints the latest records of the deny log: for each refusal, the anchor entry
it judged from (line, uuid, user / compaction / notification) and every entry
from the anchor to the refused call, with block kinds and the first 40
characters of text blocks. A disputed refusal is settled by reading this.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if last < 1 {
				return cli.Unanswerable("--last must be at least 1")
			}
			recs, bad, path, err := readDenials(last)
			if err != nil {
				return cli.Unanswerable("%v", err)
			}
			out := cli.Out(cmd)
			if len(recs) == 0 {
				fmt.Fprintf(out, "no denial recorded in %s\n", path)
			}
			for _, r := range recs {
				r.print(out)
			}
			if bad > 0 {
				fmt.Fprintf(cli.Err(cmd), "replystamp: %d unreadable line(s) skipped in %s\n", bad, path)
			}
			return nil
		},
	}
	c.Flags().IntVar(&last, "last", 5, "how many of the latest records to print")
	return c
}
