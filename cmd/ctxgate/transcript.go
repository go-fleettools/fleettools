// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
)

// maxLine bounds one transcript line. Transcripts reach 100+ MB and a single
// tool result can be megabytes; a line above this is counted and skipped, never
// buffered whole.
const maxLine = 16 << 20

// call is one API call as the transcript records it.
type call struct {
	id        string
	model     string
	ctx       int64 // input + cache read + cache creation: the context the call carried
	cacheRead int64
	at        time.Time // zero when the entry has no usable timestamp
}

type entry struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			In int64 `json:"input_tokens"`
			CR int64 `json:"cache_read_input_tokens"`
			CC int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

var usageKey = []byte(`"usage"`)

// decodeLine reads one transcript line. It answers false for anything that is
// not an assistant entry with a non-zero usage: a malformed line, a user entry,
// a synthetic error message with all-zero counters. Never panics, never
// allocates in proportion to a line it rejects early.
func decodeLine(line []byte) (call, bool) {
	if !bytes.Contains(line, usageKey) {
		return call{}, false
	}
	var e entry
	if json.Unmarshal(line, &e) != nil || e.Type != "assistant" || e.Message.Usage == nil {
		return call{}, false
	}
	u := e.Message.Usage
	if u.In < 0 || u.CR < 0 || u.CC < 0 {
		return call{}, false
	}
	c := call{id: e.Message.ID, model: e.Message.Model, cacheRead: u.CR}
	c.ctx = u.In + u.CR + u.CC
	if c.ctx <= 0 {
		return call{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil {
		c.at = t
	}
	return c, true
}

// eachLine calls fn with every line of r not longer than max, and returns how
// many longer lines it skipped. The slice is valid only during fn.
func eachLine(r io.Reader, max int, fn func([]byte)) (oversize int, err error) {
	br := bufio.NewReaderSize(r, 64<<10)
	var buf []byte
	skipping := false
	for {
		chunk, rerr := br.ReadSlice('\n')
		if len(chunk) > 0 && !skipping {
			if len(buf)+len(chunk) > max {
				skipping = true
				buf = buf[:0]
			} else {
				buf = append(buf, chunk...)
			}
		}
		if rerr == bufio.ErrBufferFull {
			continue
		}
		// end of line or end of input
		if skipping {
			oversize++
		} else if len(buf) > 0 {
			fn(bytes.TrimRight(buf, "\r\n"))
		}
		buf, skipping = buf[:0], false
		if rerr != nil {
			if rerr == io.EOF {
				return oversize, nil
			}
			return oversize, rerr
		}
	}
}

// readCalls returns the calls of one transcript in file order, one per
// message.id: an API call is written as several entries (one per content
// block) that share an id and a usage, and the LAST one wins.
func readCalls(path string) (calls []call, oversize int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	idx := map[string]int{}
	oversize, err = eachLine(f, maxLine, func(line []byte) {
		c, ok := decodeLine(line)
		if !ok {
			return
		}
		if c.id != "" {
			if i, seen := idx[c.id]; seen {
				calls[i] = c
				return
			}
			idx[c.id] = len(calls)
		}
		calls = append(calls, c)
	})
	return calls, oversize, err
}

var errNoUsage = errors.New("no assistant usage found in the tail of the transcript")

// lastContext returns the context size of the LAST API call of a transcript,
// reading only its tail: the window starts at 1 MiB and doubles up to limit.
func lastContext(path string, limit int64) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	size := st.Size()
	for win := int64(1 << 20); ; win *= 2 {
		if win > limit {
			win = limit
		}
		start := size - win
		if start < 0 {
			start = 0
		}
		b := make([]byte, size-start)
		if _, err := f.ReadAt(b, start); err != nil && err != io.EOF {
			return 0, err
		}
		if start > 0 { // the first line is cut: drop it
			if i := bytes.IndexByte(b, '\n'); i >= 0 {
				b = b[i+1:]
			} else {
				b = nil
			}
		}
		for len(b) > 0 {
			b = bytes.TrimRight(b, "\r\n")
			i := bytes.LastIndexByte(b, '\n')
			if c, ok := decodeLine(b[i+1:]); ok {
				return c.ctx, nil
			}
			if i < 0 {
				break
			}
			b = b[:i]
		}
		if start == 0 || win >= limit {
			return 0, errNoUsage
		}
	}
}
