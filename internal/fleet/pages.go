package fleet

import (
	"bytes"
	"encoding/json"
	"io"
)

// DecodeArrays reads what `gh --paginate` writes, which is a STREAM of JSON
// arrays rather than one array: the pages are concatenated, not merged.
//
// ⛔ A plain json.Unmarshal into []T reads the FIRST page and stops, with no
// error — the trailing pages are simply not there. That is a scan silently
// answering about the first hundred items of a thousand, which is the shape of
// a reading that cannot be told from a small repository.
func DecodeArrays[T any](b []byte) ([]T, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	var out []T
	for {
		var page []T
		if err := dec.Decode(&page); err != nil {
			if err == io.EOF {
				return out, nil
			}
			return nil, err
		}
		out = append(out, page...)
	}
}
