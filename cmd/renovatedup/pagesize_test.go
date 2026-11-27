package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// A 502 on the first page: the same (empty) cursor is asked again with half the
// page, the organisation is then read, and the smaller size is kept for the
// next page.
func TestGatewayTimeoutHalvesThePageAndKeepsTheCursor(t *testing.T) {
	var calls []string
	fetch := func(org, cursor string, first int) ([]byte, error) {
		calls = append(calls, fmt.Sprintf("%q/%d", cursor, first))
		switch {
		case first > 25:
			return nil, errors.New("gh: HTTP 502")
		case cursor == "":
			return pageJSON(str(cfgOn), true, "C1", repo{name: "a"}), nil
		}
		return pageJSON(str(cfgOn), false, "", repo{name: "b", files: map[string]string{"renovate.json": `{"extends":"github>acme/.github"}`}}), nil
	}
	code, out, _ := drive(t, fetch, "--orgs", "acme")
	want := `""/100,""/50,""/25,"C1"/25`
	if got := strings.Join(calls, ","); got != want {
		t.Fatalf("calls %s, want %s\n%s", got, want, out)
	}
	if code != 1 || !strings.Contains(out, "acme  inheritConfig on") || !strings.Contains(out, "2 repo(s) read") ||
		!strings.Contains(out, "acme/b  STUB") || !strings.Contains(out, "1 org(s) read, 0 unread") {
		t.Fatalf("code %d\n%s", code, out)
	}
	for _, n := range []string{"50", "25"} {
		if !strings.Contains(out, "acme  page size halved to "+n+" after: gh: HTTP 502") {
			t.Errorf("halving to %s not printed\n%s", n, out)
		}
	}
}

// A 504 counts as the gateway giving up too.
func TestGatewayTimeout504AlsoHalves(t *testing.T) {
	var sizes []int
	fetch := func(org, cursor string, first int) ([]byte, error) {
		sizes = append(sizes, first)
		if first == 100 {
			return nil, errors.New("gh: HTTP 504")
		}
		return pageJSON(str(cfgOn), false, "", repo{name: "a"}), nil
	}
	if code, out, _ := drive(t, fetch, "--orgs", "acme"); code != 0 || fmt.Sprint(sizes) != "[100 50]" {
		t.Fatalf("code %d sizes %v\n%s", code, sizes, out)
	}
}

// A page of one that still times out is the answer: UNREAD, no endless loop.
func TestGatewayTimeoutDownToOneIsUnread(t *testing.T) {
	var sizes []int
	fetch := func(org, cursor string, first int) ([]byte, error) {
		if sizes = append(sizes, first); len(sizes) > 20 {
			t.Fatal("kept retrying")
		}
		return nil, errors.New("gh: HTTP 502")
	}
	code, out, _ := drive(t, fetch, "--orgs", "acme")
	if fmt.Sprint(sizes) != "[100 50 25 12 6 3 1]" {
		t.Fatalf("sizes %v", sizes)
	}
	if code != 2 || !strings.Contains(out, "acme  UNREAD  gh: HTTP 502") || !strings.Contains(out, "0 org(s) read, 1 unread") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

// Any other failure is not retried: a 500 or a quota is not a slow query.
func TestOtherFailuresAreNotHalved(t *testing.T) {
	for _, msg := range []string{"gh: HTTP 500", "API rate limit exceeded", "HTTP 403"} {
		calls := 0
		fetch := func(org, cursor string, first int) ([]byte, error) {
			calls++
			return nil, errors.New(msg)
		}
		if code, _, _ := drive(t, fetch, "--orgs", "acme"); code != 2 || calls != 1 {
			t.Errorf("%s: code %d calls %d", msg, code, calls)
		}
	}
}

func TestGatewayGaveUp(t *testing.T) {
	for msg, want := range map[string]bool{
		"gh: HTTP 502": true, "HTTP 504 Gateway Timeout": true,
		"gh: HTTP 500": false, "HTTP 503": false, "": false,
	} {
		if got := gatewayGaveUp(errors.New(msg)); got != want {
			t.Errorf("%q: %v, want %v", msg, got, want)
		}
	}
}
