package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAgent(t *testing.T, root, file, content string) {
	t.Helper()
	dir := filepath.Join(root, ".claude", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func def(name, model string) string {
	s := "---\nname: " + name + "\ndescription: x\n"
	if model != "" {
		s += "model: " + model + "\n"
	}
	return s + "---\nbody\n"
}

// Typed agents are resolved from their definition, driven through the hook.
func TestTypedAgentModel(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	sub := filepath.Join(proj, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	old := homeDir
	homeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDir = old })

	writeAgent(t, home, "checker.md", def("checker", "haiku"))
	writeAgent(t, home, "implementer.md", def("implementer", "sonnet"))
	writeAgent(t, home, "reviewer.md", def("reviewer", "opus"))
	writeAgent(t, home, "nomodel.md", def("nomodel", ""))
	writeAgent(t, home, "inherit.md", def("inherit", "inherit"))
	writeAgent(t, home, "broken.md", "no frontmatter at all\nmodel: haiku\n")
	writeAgent(t, home, "unclosed.md", "---\nname: unclosed\nmodel: haiku\n")
	writeAgent(t, home, "file-name.md", "---\ndescription: x\nmodel: haiku\n---\n")
	writeAgent(t, home, "renamed.md", def("other-name", "haiku"))
	writeAgent(t, home, "quoted.md", "---\nname: quoted\nmodel: \"haiku\"\n---\n")
	writeAgent(t, home, "huge.md", "---\nname: huge\n"+strings.Repeat("#"+strings.Repeat("x", 100)+"\n", 200)+"model: haiku\n---\n")
	writeAgent(t, proj, "checker.md", def("checker", "opus")) // project overrides user
	writeAgent(t, proj, "projonly.md", def("projonly", "sonnet"))

	writeAgent(t, home, "locked.md", def("locked", "haiku"))
	if err := os.Chmod(filepath.Join(home, ".claude", "agents", "locked.md"), 0); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "evil.md")
	if err := os.WriteFile(outside, []byte(def("evil", "haiku")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".claude", "agents", "evil.md")); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, desc, subtype, model, cwd string
		deny                            bool
		contains                        []string
	}{
		{"checker, no model, accepted", "Check the lanes", "checker", "", home, false, nil},
		{"implementer on implement", "Fix the parser", "implementer", "", home, false, nil},
		{"reviewer on review", "Review PR 5", "reviewer", "", home, false, nil},
		{"reviewer-class task on a haiku agent is refused", "Review PR 5", "checker", "", home, true, []string{"class review", `"opus"`, `(from agent checker)`}},
		{"implement task on a haiku agent is refused", "Fix the parser", "checker", "", home, true, []string{"class implement", `"sonnet"`}},
		{"unclassified task accepts any resolved model", "turnd: host handover", "checker", "", home, false, nil},
		{"explicit model wins and is checked", "Check the lanes", "checker", "opus", home, true, []string{"class check", `"haiku"`}},
		{"explicit right model beats a wrong agent", "Review PR 5", "checker", "opus", home, false, nil},
		{"project overrides user", "Review PR 5", "checker", "", sub, false, nil},
		{"project only, from a subdirectory", "Fix the parser", "projonly", "", sub, false, nil},
		{"user agent still found from a project cwd", "Fix the parser", "implementer", "", sub, false, nil},
		{"built-in type has no file", "Check the lanes", "general-purpose", "", home, true, []string{"no model given", "no agent definition named"}},
		{"Explore has no file", "Check the lanes", "Explore", "", home, true, []string{"no model given"}},
		{"no type, no model", "Check the lanes", "", "", home, true, []string{"no model given"}},
		{"agent without model", "Check the lanes", "nomodel", "", home, true, []string{"no model given", "sets no haiku"}},
		{"model inherit is no model", "Check the lanes", "inherit", "", home, true, []string{"sets no haiku"}},
		{"no frontmatter", "Check the lanes", "broken", "", home, true, []string{"no well-formed frontmatter"}},
		{"unclosed frontmatter", "Check the lanes", "unclosed", "", home, true, []string{"no well-formed frontmatter"}},
		{"name falls back to the file basename", "Check the lanes", "file-name", "", home, false, nil},
		{"frontmatter name beats the file name", "Check the lanes", "other-name", "", home, false, nil},
		{"file name does not match a renamed agent", "Check the lanes", "renamed", "", home, true, []string{"no agent definition"}},
		{"quoted value", "Check the lanes", "quoted", "", home, false, nil},
		{"bounded read: frontmatter cut off at the limit", "Check the lanes", "huge", "", home, true, []string{"no well-formed frontmatter"}},
		{"unreadable definition", "Check the lanes", "locked", "", home, true, []string{"is unreadable"}},
		{"symlink is not followed", "Check the lanes", "evil", "", home, true, []string{"no agent definition"}},
		{"path-like type refused", "Check the lanes", "../checker", "", home, true, []string{"no usable subagent_type"}},
		{"relative cwd is ignored", "Check the lanes", "projonly", "", "rel/dir", true, []string{"no agent definition"}},
		{"fork still ignores all", "Review PR 5", "fork", "", home, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := agent(tc.desc, "", tc.subtype, tc.model)
			in, _ := json.Marshal(map[string]any{"tool_name": "Agent", "cwd": tc.cwd, "tool_input": p.ToolInput})
			out, _, code := hook(t, string(in))
			if code != 0 {
				t.Fatalf("exit %d", code)
			}
			dec, reason := "allow", ""
			if strings.TrimSpace(out) != "" { // an allowance prints nothing
				dec, reason = decision(t, out)
			}
			if (dec == "deny") != tc.deny {
				t.Fatalf("decision %q (%s), want deny=%v", dec, reason, tc.deny)
			}
			for _, s := range tc.contains {
				if !strings.Contains(reason, s) {
					t.Errorf("reason %q lacks %q", reason, s)
				}
			}
		})
	}
}
