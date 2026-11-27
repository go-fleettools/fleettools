package transcript

import (
	"strings"
	"testing"
)

func TestScanAndTurns(t *testing.T) {
	long := strings.Repeat("a", 200<<10) // a line past bufio's default 64 KiB
	in := `{"type":"user","message":{"role":"user","content":"hi"}}` + "\n" +
		`{"type":"assistant","isSidechain":true,"message":{"role":"assistant","content":"sub"}}` + "\n" +
		"not json\n" +
		`{"type":"user","isMeta":true,"message":{"role":"user","content":"meta"}}` + "\n" +
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":[{"type":"text","text":"out"}]}]}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + long + `"}]}}` + "\n"
	var types []string
	var turns int
	err := Scan(strings.NewReader(in), func(e Entry, m Message) {
		types = append(types, e.Type)
		if e.Type == "user" && IsUserTurn(e, m) {
			turns++
		}
		for _, b := range Blocks(m.Content) {
			if b.Type == "tool_result" && ResultText(b) != "out\n" {
				t.Errorf("ResultText = %q", ResultText(b))
			}
			if b.Type == "text" && e.Type == "assistant" && len(b.Text) != len(long) {
				t.Errorf("a long line was cut: %d", len(b.Text))
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(types, ","); got != "user,user,user,assistant" {
		t.Errorf("entries = %s (sidechain and junk must be skipped)", got)
	}
	if turns != 1 {
		t.Errorf("turns = %d, want 1 (meta and tool_result are not turns)", turns)
	}
}

func TestResultTextString(t *testing.T) {
	if got := ResultText(Block{Content: []byte(`"plain"`)}); got != "plain" {
		t.Errorf("got %q", got)
	}
	if got := ResultText(Block{Content: []byte(`17`)}); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestBlocksShapes(t *testing.T) {
	if got := Blocks([]byte(`"plain"`)); len(got) != 1 || got[0].Type != "text" || got[0].Text != "plain" {
		t.Errorf("string content: %+v", got)
	}
	if got := Blocks([]byte(`[{"type":"text","text":"a"},{"type":"tool_use","name":"Bash"}]`)); len(got) != 2 || got[1].Name != "Bash" {
		t.Errorf("array content: %+v", got)
	}
	for _, bad := range []string{`42`, `{"type":"text"}`, ``} {
		if got := Blocks([]byte(bad)); len(got) != 0 {
			t.Errorf("Blocks(%q) = %+v, want none", bad, got)
		}
	}
}
