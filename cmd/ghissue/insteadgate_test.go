package main

import (
	"strings"
	"testing"
)

func TestInsteadOfPRRefusesTextWithoutTheFlag(t *testing.T) {
	s := newServer(t)
	rs := repoServer(t, true)
	cases := []struct {
		name string
		args []string
	}{
		{"comment", []string{"o/r", "7", "--base", s.URL, "--token-file", tokenFile(t, "k"), "--body-file", bodyFile(t, "hi")}},
		{"new", []string{"o/r", "--new", "--title", "t", "--base", rs.URL, "--token-file", tokenFile(t, "k"), "--body-file", bodyFile(t, "hi")}},
		{"comment with close", []string{"o/r", "7", "--close", "--base", s.URL, "--token-file", tokenFile(t, "k"), "--body-file", bodyFile(t, "hi")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := execBare(t, c.args...)
			if err == nil {
				t.Fatal("text was accepted without --instead-of-pr")
			}
			for _, want := range []string{"PULL REQUEST", "regression test", "--instead-of-pr \"<why"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal lacks %q:\n%v", want, err)
				}
			}
		})
	}
	if len(s.methods) != 0 || len(rs.methods) != 0 {
		t.Errorf("the refusal reached the network: %v %v", s.methods, rs.methods)
	}
}

func TestInsteadOfPRRefusesAnEmptyOrBlankReason(t *testing.T) {
	s := newServer(t)
	for _, reason := range []string{"", "   ", "\t \n"} {
		for _, args := range [][]string{
			{"o/r", "7", "--base", s.URL, "--token-file", tokenFile(t, "k"), "--body-file", bodyFile(t, "hi"), "--instead-of-pr", reason},
			{"o/r", "--new", "--title", "t", "--base", s.URL, "--token-file", tokenFile(t, "k"), "--body-file", bodyFile(t, "hi"), "--instead-of-pr", reason},
		} {
			if _, err := execBare(t, args...); err == nil || !strings.Contains(err.Error(), "--instead-of-pr") {
				t.Errorf("reason %q: err = %v", reason, err)
			}
		}
	}
	if len(s.methods) != 0 {
		t.Errorf("a refusal reached the network: %v", s.methods)
	}
}

func TestInsteadOfPRRefusesAMultiLineReason(t *testing.T) {
	if _, err := gateInsteadOfPR("a\nb"); err == nil {
		t.Error("a two-line reason was accepted")
	}
	if _, err := gateInsteadOfPR(strings.Repeat("x", maxInsteadText+1)); err == nil {
		t.Error("an over-long reason was accepted")
	}
	if r, err := gateInsteadOfPR("  upstream has no CI to run a test  "); err != nil || r != "upstream has no CI to run a test" {
		t.Errorf("got %q, %v", r, err)
	}
}

func TestInsteadOfPRAcceptsAReasonAndKeepsItOffGitHub(t *testing.T) {
	const why = "maintainers asked for a question first"
	s := newServer(t)
	out, err := execBare(t, "o/r", "7", "--base", s.URL, "--token-file", tokenFile(t, "k"),
		"--body-file", bodyFile(t, "hello"), "--instead-of-pr", why)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "issuecomment-1") || !strings.Contains(out, why) {
		t.Errorf("output lacks the URL or the reason:\n%s", out)
	}
	if got := s.bodies[len(s.bodies)-1]; got != `{"body":"hello"}` || strings.Contains(got, why) {
		t.Errorf("sent %q: the reason must not reach GitHub", got)
	}

	rs := repoServer(t, true)
	out, err = execBare(t, "o/r", "--new", "--title", "t", "--base", rs.URL, "--token-file", tokenFile(t, "k"),
		"--body-file", bodyFile(t, "why"), "--instead-of-pr", why)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "created:") || !strings.Contains(out, why) {
		t.Errorf("--new output lacks the URL or the reason:\n%s", out)
	}
	for _, b := range rs.bodies {
		if strings.Contains(b, why) {
			t.Errorf("the reason reached GitHub: %s", b)
		}
	}
}

func TestInsteadOfPRDoesNotCoverStateChangesWithoutText(t *testing.T) {
	for _, flag := range []string{"--close", "--reopen"} {
		s := newServer(t)
		if _, err := execBare(t, "o/r", "7", flag, "--base", s.URL, "--token-file", tokenFile(t, "k")); err != nil {
			t.Errorf("%s without text was refused: %v", flag, err)
		}
	}
}

func TestInsteadOfPRDoesNotCoverEditingExistingText(t *testing.T) {
	s := editServer(t)
	if _, err := execBare(t, "o/r", "7", "--edit", "--base", s.URL, "--token-file", tokenFile(t, "k"),
		"--body-file", bodyFile(t, "fresh body")); err != nil {
		t.Errorf("--edit was refused: %v", err)
	}
}
