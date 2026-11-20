// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxAgentFile bounds what is read of an agent definition: the frontmatter is
// a few lines, the body is none of our business.
const maxAgentFile = 8 << 10

// homeDir is replaceable so tests never read the real ~/.claude/agents.
var homeDir = os.UserHomeDir

// agentDirs lists the directories that can define a sub-agent, the project's
// first (the nearest .claude/agents above cwd) so that it overrides the user's.
func agentDirs(cwd string) []string {
	var dirs []string
	if cwd != "" && filepath.IsAbs(cwd) {
		for d := filepath.Clean(cwd); ; d = filepath.Dir(d) {
			cand := filepath.Join(d, ".claude", "agents")
			if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
				dirs = append(dirs, cand)
				break
			}
			if filepath.Dir(d) == d {
				break
			}
		}
	}
	if h, err := homeDir(); err == nil && h != "" {
		u := filepath.Join(h, ".claude", "agents")
		if len(dirs) == 0 || dirs[0] != u {
			dirs = append(dirs, u)
		}
	}
	return dirs
}

// frontmatter returns the key: value pairs between the opening and closing
// "---" lines. ok is false when there is no well-formed block.
func frontmatter(b []byte) (map[string]string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return nil, false
	}
	kv := map[string]string{}
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			return kv, true
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		kv[strings.TrimSpace(k)] = v
	}
	return nil, false // never closed
}

// readBounded reads a regular file that is not a symlink, at most maxAgentFile.
func readBounded(path string) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("not a regular file (symlinks are not followed)")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxAgentFile))
}

// agentModel resolves the model an agent type sets in its definition. When it
// cannot, reason says why; it never guesses.
func agentModel(cwd, typ string) (model, reason string) {
	typ = strings.TrimSpace(typ)
	if typ == "" || strings.ContainsAny(typ, `/\`) {
		return "", "no usable subagent_type"
	}
	var problem string
	for _, dir := range agentDirs(cwd) {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !strings.HasSuffix(e.Name(), ".md") || e.Type()&os.ModeSymlink != 0 || e.IsDir() {
				continue
			}
			path := filepath.Join(dir, e.Name())
			b, err := readBounded(path)
			if err != nil {
				if strings.TrimSuffix(e.Name(), ".md") == typ {
					problem = fmt.Sprintf("%s is unreadable (%v)", path, err)
				}
				continue
			}
			kv, ok := frontmatter(b)
			name := strings.TrimSuffix(e.Name(), ".md")
			if ok && kv["name"] != "" {
				name = kv["name"]
			}
			if name != typ {
				continue
			}
			if !ok {
				return "", fmt.Sprintf("%s has no well-formed frontmatter", path)
			}
			if family(kv["model"]) == "" {
				return "", fmt.Sprintf("%s sets no haiku, sonnet or opus model (model: %q)", path, kv["model"])
			}
			return kv["model"], ""
		}
	}
	if problem != "" {
		return "", problem
	}
	return "", fmt.Sprintf("no agent definition named %q in .claude/agents (project or user)", typ)
}
