package fleet

import (
	"encoding/json"
	"testing"
)

type item struct {
	Name string `json:"name"`
}

// ⛔ The defect this exists to prevent: `gh --paginate` concatenates pages, so
// the obvious spelling reads page one and reports success.
func TestDecodeArraysReadsEveryPageAndNotJustTheFirst(t *testing.T) {
	stream := []byte(`[{"name":"a"},{"name":"b"}]` + "\n" + `[{"name":"c"}]`)

	var viaUnmarshal []item
	if err := json.Unmarshal(stream, &viaUnmarshal); err == nil && len(viaUnmarshal) == 3 {
		t.Fatal("the control is wrong: plain Unmarshal is supposed to miss the later pages")
	}

	got, err := DecodeArrays[item](stream)
	if err != nil {
		t.Fatalf("DecodeArrays: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("read %d item(s), want 3 across the two pages: %+v", len(got), got)
	}
	for i, want := range []string{"a", "b", "c"} {
		if got[i].Name != want {
			t.Errorf("item %d = %q, want %q", i, got[i].Name, want)
		}
	}
}

func TestDecodeArraysReadsASinglePage(t *testing.T) {
	got, err := DecodeArrays[item]([]byte(`[{"name":"only"}]`))
	if err != nil {
		t.Fatalf("DecodeArrays: %v", err)
	}
	if len(got) != 1 || got[0].Name != "only" {
		t.Errorf("got %+v, want one item named only", got)
	}
}

// An empty stream is zero items and NOT an error: a repository with no open
// pull requests is a legitimate answer.
func TestDecodeArraysReadsAnEmptyStreamAsNoItems(t *testing.T) {
	got, err := DecodeArrays[item](nil)
	if err != nil {
		t.Fatalf("DecodeArrays: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want nothing", got)
	}
}

// ⛔ Malformed input is an ERROR and not an empty result. A reader that
// answered "no items" for a body it could not parse would turn every broken
// call into a clean scan.
func TestDecodeArraysRefusesWhatItCannotRead(t *testing.T) {
	if _, err := DecodeArrays[item]([]byte(`{"not":"an array"}`)); err == nil {
		t.Error("an object where an array was expected came back as success")
	}
	if _, err := DecodeArrays[item]([]byte(`[{"name":"a"}] [truncated`)); err == nil {
		t.Error("a truncated later page came back as success")
	}
}
