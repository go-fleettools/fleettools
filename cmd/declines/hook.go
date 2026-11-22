// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// payload is the PostToolUse envelope, read leniently: a field this does not
// know about is ignored, and a field it knows about but cannot read leaves the
// zero value, which every decision below treats as "could not tell".
type payload struct {
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
	Cwd           string `json:"cwd"`
	ToolInput     struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
}

// response is the part of a Bash tool_response this reads. Only the ERROR
// markers matter: the narrowing rule is "the command succeeded and its output
// says it declined", and a command that visibly failed is already being read.
type response struct {
	Interrupted bool   `json:"interrupted"`
	IsError     bool   `json:"is_error"`
	Error       string `json:"error"`
	Stderr      string `json:"stderr"`
}

// maxPayloadBytes bounds the payload. A hook reads whatever the harness hands
// it, so it must not be the thing that runs the machine out of memory.
var maxPayloadBytes int64 = 8 << 20

// failed reports whether the response definitely shows the command failing.
//
// ⛔ The two uncertain answers are NOT merged: a payload this cannot parse
// returns false, so the file is still read and the declines still surface.
// Silence is the failure this tool was written about, so silence is only ever
// chosen on a DEFINITE error.
func failed(raw json.RawMessage) bool {
	var r response
	if err := json.Unmarshal(raw, &r); err != nil {
		// ⛔ Not `return r.…`. A SYNTAX error populates nothing (encoding/json
		// validates the whole value first), but a TYPE error does not stop the
		// decode: `{"interrupted":"x","is_error":true}` leaves IsError set and
		// returns an error, and without this line a response nobody could
		// fully read would buy silence. Measured by mutation: the guard
		// survived every syntax-error test and died on the type-error one.
		// There is no `len(raw) == 0` guard above: an empty message fails to
		// unmarshal and lands here, which is the same answer by one line.
		return false
	}
	return r.Interrupted || r.IsError || strings.TrimSpace(r.Error) != ""
}

// hookRun reads a PostToolUse payload on stdin and prints, as
// additionalContext, every declining line in the files the command redirected
// its output to. It always returns nil: a PostToolUse hook cannot undo the
// command it is reading, and a hook that errors out reports nothing at all.
func hookRun(cmd *cobra.Command) error {
	var p payload
	if err := readPayload(stdin, &p); err != nil {
		// ⛔ Said out loud, never swallowed: a hook that could not read its own
		// payload has judged nothing, and the whole lesson behind this tool is
		// that a silent non-event reads as a clean one.
		fmt.Fprintf(cmd.ErrOrStderr(), "declines: could not read the payload (%v) — NOT read, so this command is not counted as clean\n", err)
		return nil
	}
	if p.ToolName != "" && p.ToolName != "Bash" {
		return nil
	}
	if failed(p.ToolResponse) {
		return nil
	}
	// No `len(targets) == 0` fast path: report() of nothing returns nothing and
	// the check below already holds it — see redirect.go on that shape.
	lines := report(p.Cwd, redirectTargets(p.ToolInput.Command))
	if len(lines) == 0 {
		return nil
	}
	writeContext(cmd.OutOrStdout(), lines)
	return nil
}

// readPayload decodes the envelope under the size bound.
func readPayload(r io.Reader, p *payload) error {
	data, err := io.ReadAll(io.LimitReader(r, maxPayloadBytes+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > maxPayloadBytes {
		return fmt.Errorf("payload longer than the %d bytes this reads", maxPayloadBytes)
	}
	return json.Unmarshal(data, p)
}

// report scans each target and returns the lines to surface. An empty result
// means every target was read and none declined — the only silence this tool
// allows itself.
func report(cwd string, targets []string) []string {
	var out []string
	for _, t := range targets {
		if unresolved(t) {
			out = append(out, fmt.Sprintf("%s: NOT read (the name holds shell syntax this does not expand), so not counted as clean", t))
			continue
		}
		path := t
		if !filepath.IsAbs(path) && cwd != "" {
			path = filepath.Join(cwd, path)
		}
		hits, n, err := scanFile(path, declineWords)
		switch {
		case err != nil && os.IsNotExist(err):
			// A target the command did not create is not this hook's business:
			// the command may have written it elsewhere, or not run at all,
			// and the harness already showed that failure.
			continue
		case err != nil:
			out = append(out, fmt.Sprintf("%s: NOT read (%v), so not counted as clean", t, err))
		case n == 0:
			out = append(out, fmt.Sprintf("%s: EMPTY — nothing was read, which is not the same as a clean run", t))
		default:
			for _, h := range hits {
				out = append(out, fmt.Sprintf("%s:%d: %s", t, h.Line, h.Text))
			}
		}
	}
	return out
}

// maxContextLines bounds what one hook adds to a session's context.
var maxContextLines = 40

// writeContext prints the PostToolUse hookSpecificOutput envelope.
func writeContext(w io.Writer, lines []string) {
	shown := lines
	suffix := ""
	if len(shown) > maxContextLines {
		suffix = fmt.Sprintf("\n… and %d more declining line(s); run `declines <file>` to read them all.", len(shown)-maxContextLines)
		shown = shown[:maxContextLines]
	}
	text := "⛔ declines: that command exited without an error, and the output it saved SAYS IT DID NOT DO SOMETHING. " +
		"The tail of a log holds what a run FOUND; a refusal is printed where the decision was taken, near the top. " +
		"Read these before writing what the tool did or did not say:\n\n" +
		strings.Join(shown, "\n") + suffix
	out := map[string]map[string]string{"hookSpecificOutput": {
		"hookEventName":     "PostToolUse",
		"additionalContext": text,
	}}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(out)
}
