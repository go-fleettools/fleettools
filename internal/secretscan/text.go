package secretscan

import (
	"path/filepath"
	"strings"
)

var refSchemes = []string{
	"systemd-creds", "keychain", "keepassxc", "file", "env",
	"vault", "op", "pass", "secretsource", "sops",
}

var placeholderWords = []string{
	"change", "your", "example", "xxx", "redacted", "todo", "dummy",
	"placeholder", "secret", "password", "token", "hunter2", "foo", "test",
}

// usable says whether value is something that could be a secret written out:
// not empty, not an interpolation, not a reference, not a YAML block marker.
func usable(value string) bool {
	switch strings.ToLower(value) {
	case "", "true", "false", "null", "nil", "none", "~", "|", ">", "|-", ">-":
		return false
	}
	for _, p := range []string{"$", "%", "<", "{{", "@"} {
		if strings.HasPrefix(value, p) {
			return false
		}
	}
	lower := strings.ToLower(value)
	for _, s := range refSchemes {
		if strings.HasPrefix(lower, s+":") {
			return false
		}
	}
	return true
}

// placeholder says whether a value reads as a stand-in (`changeme`, `<...>`
// shapes are already refused by usable). A sample showing one still teaches
// the plaintext habit, so it is reported, but marked.
func placeholder(value string) bool {
	lower := strings.ToLower(value)
	for _, w := range placeholderWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return strings.Trim(lower, "*x.-_") == ""
}

func validName(name string) bool {
	for _, r := range name {
		if !(r == '_' || r == '-' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return name != ""
}

// cleanValue reads the value after a separator: a quoted string whole, or an
// unquoted word up to a comment. An unquoted run of words is prose, not a
// value, and comes back empty.
func cleanValue(v string) string {
	v = strings.TrimSpace(v)
	if v != "" && strings.ContainsRune("\"'`", rune(v[0])) {
		if end := strings.IndexByte(v[1:], v[0]); end >= 0 {
			return v[1 : 1+end]
		}
		return v[1:]
	}
	v, _, _ = strings.Cut(v, " #")
	v = strings.TrimSuffix(strings.TrimSpace(v), ",")
	if strings.ContainsAny(v, " \t") {
		return ""
	}
	return v
}

// InlineSecret reads one line of a sample (toml, yaml, env file, a shell
// command, a JSON object) and returns the name and the value when it sets a
// plaintext secret to something. Comments and prose are not read.
func InlineSecret(line string) (name, value string, ok bool) {
	l := strings.TrimSpace(line)
	// A comment is not read because its first word is no name: validName
	// refuses `# password` and `// token` below.
	l = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(l, "- "), "export "))
	var raw string
	if strings.HasPrefix(l, "--") {
		i := strings.IndexAny(l, "= ")
		if i < 0 {
			return "", "", false
		}
		name, raw = l[2:i], l[i+1:]
	} else {
		i := strings.IndexAny(l, "=:")
		if i < 0 {
			return "", "", false
		}
		name, raw = strings.Trim(strings.TrimSpace(l[:i]), `"'`), l[i+1:]
	}
	value = cleanValue(raw)
	if !validName(name) || Classify(name) != Plaintext || !usable(value) {
		return "", "", false
	}
	return name, value, true
}

// ScanText reads a file that is not Go: a systemd unit and a launchd plist
// are INPUTS (what the daemon is started with), everything else is a SAMPLE
// (what a reader is shown). Markdown is read inside code fences only.
func ScanText(rel string, content []byte) []Finding {
	lines := strings.Split(string(content), "\n")
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".service":
		return scanUnit(rel, lines)
	case ".plist":
		return scanPlist(rel, lines)
	}
	md := strings.EqualFold(filepath.Ext(rel), ".md")
	source := "sample"
	if md {
		source = "doc"
	}
	var out []Finding
	fenced := false
	for i, l := range lines {
		if md && strings.HasPrefix(strings.TrimSpace(l), "```") {
			fenced = !fenced
			continue
		}
		if md && !fenced {
			continue
		}
		if name, value, ok := InlineSecret(l); ok {
			f := newFinding("sample", source, Plaintext, name, rel, i+1)
			f.Placeholder = placeholder(value)
			out = append(out, f)
		}
	}
	return out
}

func scanUnit(rel string, lines []string) []Finding {
	var out []Finding
	add := func(source, name string, i int) {
		out = append(out, newFinding("input", source, Plaintext, name, rel, i+1))
	}
	for i, l := range lines {
		key, val, ok := strings.Cut(strings.TrimSpace(l), "=")
		if !ok {
			continue
		}
		switch key {
		case "Environment":
			for _, tok := range strings.Fields(val) {
				if n, v, ok := strings.Cut(strings.Trim(tok, `"'`), "="); ok && Classify(n) == Plaintext && usable(v) {
					add("unit-env", n, i)
				}
			}
		case "ExecStart", "ExecStartPre", "ExecStartPost", "ExecReload":
			fields := strings.Fields(val)
			for j, tok := range fields {
				if !strings.HasPrefix(tok, "--") {
					continue
				}
				n, v, eq := strings.Cut(tok[2:], "=")
				if !eq && j+1 < len(fields) && !strings.HasPrefix(fields[j+1], "-") {
					v = fields[j+1]
				}
				if Classify(n) == Plaintext && usable(strings.Trim(v, `"'`)) {
					add("unit-flag", n, i)
				}
			}
		}
	}
	return out
}

func tagText(line, tag string) string {
	open, closing := "<"+tag+">", "</"+tag+">"
	i := strings.Index(line, open)
	if i < 0 {
		return ""
	}
	rest := line[i+len(open):]
	j := strings.Index(rest, closing)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func scanPlist(rel string, lines []string) []Finding {
	var out []Finding
	add := func(name string, i int) {
		out = append(out, newFinding("input", "unit-env", Plaintext, name, rel, i+1))
	}
	lastKey, prevFlag := "", ""
	for i, l := range lines {
		if k := tagText(l, "key"); k != "" {
			lastKey = k
		}
		s := tagText(l, "string")
		if s == "" {
			continue
		}
		switch {
		case strings.HasPrefix(s, "--"):
			n, v, eq := strings.Cut(s[2:], "=")
			prevFlag = ""
			if !eq {
				prevFlag = n
			} else if Classify(n) == Plaintext && usable(v) {
				add(n, i)
			}
		case prevFlag != "":
			if Classify(prevFlag) == Plaintext && usable(s) {
				add(prevFlag, i)
			}
			prevFlag = ""
		case Classify(lastKey) == Plaintext && usable(s):
			add(lastKey, i)
		}
	}
	return out
}
