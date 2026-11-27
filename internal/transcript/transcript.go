// Package transcript reads a Claude Code session transcript (JSONL): the ONE
// parser the Stop and PreToolUse hooks of this repository share.
//
// It was moved out of cmd/replystamp when a second gate (cmd/mergedcheck) needed
// the same three decisions, each of which had been a defect once:
//
//   - a line that overruns bufio's default 64 KiB must not be dropped (the
//     anchor would move), so Scan raises the limit;
//   - a subagent's entries are written into the same file with isSidechain set,
//     and are not this session's: Decode skips them;
//   - most `type: "user"` entries are not turns (tool results, injected meta),
//     and a usage-limit message (isApiErrorMessage) is not a reply: IsUserTurn
//     and the Entry field carry both facts.
package transcript

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// Entry is one transcript line.
type Entry struct {
	Type    string          `json:"type"`
	IsMeta  bool            `json:"isMeta"`
	UUID    string          `json:"uuid"`
	Message json.RawMessage `json:"message"`
	// ⛔⛔ A USAGE-LIMIT MESSAGE IS NOT A REPLY, AND IT IS THE MOST COMMON LAST
	// TEXT IN A TRANSCRIPT. Measured over the 50 transcripts on this machine:
	// 14 of them end on `You've hit your 5-hour limit · resets 8:50pm`, text
	// the model never wrote, recorded as an ordinary assistant text entry.
	//
	// The entry says so itself: isApiErrorMessage, beside apiError
	// ("usage_limit_reached") and apiErrorStatus (429). A FIELD, not a text
	// pattern: matching on the words would break when the message is reworded
	// and would refuse a real reply that quoted it.
	IsAPIErrorMessage bool `json:"isApiErrorMessage"`
	// A subagent's entries are written into the same transcript with this set.
	IsSidechain bool `json:"isSidechain"`
	// Timestamp is when the entry was recorded, RFC 3339.
	Timestamp string `json:"timestamp"`
	// What opened a turn: the resume after a compaction, or a background task
	// finishing.
	IsCompactSummary bool   `json:"isCompactSummary"`
	TurnOrigin       string `json:"turnOrigin"`
	PromptSource     string `json:"promptSource"`
}

// Message is an entry's message.
type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// Block is one element of a message's content: text, tool_use or tool_result.
type Block struct {
	Type string `json:"type"`
	Text string `json:"text"`
	ID   string `json:"id"` // a tool_use block's id
	// A tool_use block names the tool and carries its input.
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	// A tool_result block names the tool_use it answers; its content is a
	// string or a list of text blocks.
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// Scan calls fn for each decodable, non-sidechain entry of a transcript.
func Scan(r io.Reader, fn func(e Entry, m Message)) error {
	sc := bufio.NewScanner(r)
	// A transcript line holds a whole message and the default 64 KiB limit is
	// nowhere near enough.
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		if e, m, ok := Decode(sc.Bytes()); ok {
			fn(e, m)
		}
	}
	return sc.Err()
}

// Decode decodes one transcript line; ok is false for a line that is empty,
// undecodable or a sidechain entry.
func Decode(line []byte) (Entry, Message, bool) {
	var e Entry
	if err := json.Unmarshal(line, &e); err != nil {
		// A transcript is append-only and a half-written last line is ordinary.
		return Entry{}, Message{}, false
	}
	var m Message
	if len(e.Message) > 0 {
		_ = json.Unmarshal(e.Message, &m)
	}
	if e.IsSidechain {
		return Entry{}, Message{}, false
	}
	return e, m, true
}

// Blocks decodes a message's content as blocks; a plain string content is one
// text block.
func Blocks(raw json.RawMessage) []Block {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []Block{{Type: "text", Text: s}}
	}
	var bs []Block
	if json.Unmarshal(raw, &bs) != nil {
		return nil
	}
	return bs
}

// IsUserTurn reports whether a user entry is a genuine turn: not meta, not a
// tool result, with some text in it.
func IsUserTurn(e Entry, m Message) bool {
	if e.IsMeta || m.Role != "user" {
		return false
	}
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return strings.TrimSpace(s) != ""
	}
	var bs []Block
	if json.Unmarshal(m.Content, &bs) != nil {
		return false
	}
	for _, b := range bs {
		if b.Type == "tool_result" {
			return false
		}
	}
	for _, b := range bs {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			return true
		}
	}
	return false
}

// ResultText returns the text of a tool_result block's content.
func ResultText(b Block) string {
	var s string
	if json.Unmarshal(b.Content, &s) == nil {
		return s
	}
	var bs []Block
	if json.Unmarshal(b.Content, &bs) != nil {
		return ""
	}
	var sb strings.Builder
	for _, x := range bs {
		if x.Type == "text" {
			sb.WriteString(x.Text)
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}
