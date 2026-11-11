package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const issues = `[{"html_url":"https://x/issues/2","title":"perf","body":"We skip the SIMD kernel because it would break bit-parity."},
{"html_url":"https://x/issues/1","title":"ok","body":"Output unchanged, bit-identical."}]
{"items":[{"html_url":"https://x/issues/3","title":"plain","body":"Nothing."}]}`

func TestLoadShapes(t *testing.T) {
	got, err := Load(strings.NewReader(issues))
	if err != nil || len(got) != 3 {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := Load(strings.NewReader("")); err != nil || len(got) != 0 {
		t.Errorf("empty: %v %v", got, err)
	}
	if _, err := Load(strings.NewReader("{")); err == nil {
		t.Error("truncated JSON")
	}
	if _, err := Load(strings.NewReader("3")); err == nil {
		t.Error("a number is neither shape")
	}
}

func TestJudge(t *testing.T) {
	v := Judge(Item{Title: "perf", Body: "We skip the SIMD kernel because it would break bit-parity."})
	if !v.Refused || !v.Mentions || len(v.Findings) != 1 {
		t.Errorf("%+v", v)
	}
	v = Judge(Item{Body: "Output unchanged, bit-identical."})
	if v.Refused || !v.Mentions {
		t.Errorf("%+v", v)
	}
}

func TestSampleIsDeterministic(t *testing.T) {
	items, _ := Load(strings.NewReader(issues))
	a := Sample(append([]Item(nil), items...), 2, 7)
	b := Sample(append([]Item(nil), items...), 2, 7)
	if len(a) != 2 || a[0] != b[0] || a[1] != b[1] || a[0].URL >= a[1].URL {
		t.Errorf("%v %v", a, b)
	}
	if got := Sample(items, 0, 1); len(got) != 3 || got[0].URL != "https://x/issues/1" {
		t.Errorf("all, sorted: %v", got)
	}
	if got := Sample(items, 9, 1); len(got) != 3 {
		t.Errorf("n over the size keeps all: %v", got)
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.json")
	os.WriteFile(f, []byte(issues), 0o644)
	var out, errb bytes.Buffer
	if code := run([]string{"-show", f}, &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	for _, want := range []string{"REFUSE https://x/issues/2", "[declined-because-parity]", "pass (mentions bit identity) https://x/issues/1", "    | Output unchanged, bit-identical.", "pass https://x/issues/3", "3 text(s): 1 refused, 1 pass while mentioning bit identity, 1 other"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("lacks %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if code := run([]string{"-sample", "1", "-seed", "3", f}, &out, &errb); code != 0 || !strings.Contains(out.String(), "1 text(s)") || strings.Contains(out.String(), "    |") {
		t.Errorf("%d %s", code, out.String())
	}
}

func TestRunErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{"), 0o644)
	for name, args := range map[string][]string{
		"no files":       {},
		"absent":         {filepath.Join(dir, "absent")},
		"bad json":       {bad},
		"sample no num":  {"-sample"},
		"seed not num":   {"-seed", "x", bad},
		"sample not num": {"-sample", "x", bad},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 || errb.Len() == 0 {
			t.Errorf("%s: %d %q", name, code, errb.String())
		}
	}
}
