package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// ⛔ THE FRAME IS RUN, NOT READ (fleettools#243). A static reading of a script
// cannot say whether its origin check works: the first version looked for the
// word `.origin`, and a frame with no origin check, with the guard removed, or
// with no framing check still verified. So the published shell is loaded in a
// headless browser, framed from origins that are not the declared parent and in
// ways that are not a direct frame, and sent well-formed commands. Each of those
// must get NO message back; the declared parent must be answered.
//
// The contract a second-origin shell keeps (secondorigin init writes it):
//
//	parent -> frame   {t:"hello", nonce}          the parent announces itself; the
//	                                              frame believes it only if the
//	                                              message's origin is allowed, its
//	                                              source is the framing window and
//	                                              the nonce is the one in its URL
//	                                              fragment; only then does it send
//	                                              {t:"ready"} to that origin
//	parent -> frame   {t:<command>, id, nonce}    answered by a message with the id
//
// There is deliberately no use of location.ancestorOrigins (Firefox has none) or
// of the referrer (the parent sends none).
//
// The declared parent, and the foreign origins, are played by request
// interception in the browser: no network, and the real shell served from its
// real URL. So the browser really is on https://<declared parent> when it
// frames the shell.

const harnessPath = "/__secondorigin"

const (
	foreignOrigin = "https://foreign.invalid"
	nesterOrigin  = "https://nester.invalid"
)

// harnessHTML is served at every played origin. Its role is in the URL fragment.
const harnessHTML = `<!doctype html><meta charset="utf-8"><body><script>
"use strict";
const q = new URLSearchParams(location.hash.slice(1));
const role = q.get("role"), shell = q.get("shell"), nonce = q.get("nonce");
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const until = async (fn, ms) => { const end = Date.now() + ms; while (!fn()) { if (Date.now() > end) return false; await sleep(25); } return true; };
window.log = [];   // every message this window received
window.loaded = false;
addEventListener("message", (e) => window.log.push({ origin: e.origin, data: JSON.stringify(e.data) }));
if (role === "nest") {
  const f = document.createElement("iframe");
  f.id = "n"; f.src = q.get("inner"); document.body.appendChild(f);
} else {
  const f = document.createElement("iframe");
  f.id = "f"; f.onload = () => { window.loaded = true; }; f.src = shell + "#" + nonce; document.body.appendChild(f);
  if (role === "sibling") {
    const s = document.createElement("iframe");
    s.id = "s"; s.srcdoc = "<script>window.slog=[];addEventListener('message',e=>slog.push({origin:e.origin,data:JSON.stringify(e.data)}));window.go=(o,ms)=>{for(const m of ms)parent.frames[0].postMessage(m,o)}</scr" + "ipt>";
    document.body.appendChild(s);
  }
}
// run sends what c says to the shell and returns what came back from its origin.
window.run = async (c) => {
  if (!(await until(() => window.loaded, 30000))) return { error: "the shell did not load" };
  const f = document.getElementById("f");
  const post = (m) => f.contentWindow.postMessage(m, c.shellOrigin);
  const ms = [];
  if (c.hello) ms.push({ t: "hello", nonce: c.helloNonce });
  if (c.sibling) {
    const s = document.getElementById("s");
    await sleep(200);
    s.contentWindow.go(c.shellOrigin, [{ t: "hello", nonce: nonce }, { t: c.cmd, id: "p1", nonce: nonce }]);
  } else {
    for (const m of ms) post(m);
    if (c.waitReady) await until(() => window.log.some((m) => m.origin === c.shellOrigin && m.data.indexOf('"ready"') >= 0), c.readyMs);
    if (c.cmd) post({ t: c.cmd, id: "p1", nonce: c.cmdNonce });
  }
  await sleep(c.quietMs);
  const got = window.log.filter((m) => m.origin === c.shellOrigin);
  const s = document.getElementById("s");
  if (s) for (const m of s.contentWindow.slog || []) if (m.origin === c.shellOrigin) got.push(m);
  return { got: got };
};
</script>`

// runBehaviour loads shellURL in each browser and plays the scenarios.
func runBehaviour(d Decl, shellURL string, o options) []finding {
	name := "shell run " + shellURL
	pw, err := playwright.Run()
	if err != nil {
		return []finding{{unverified, name, "no browser driver: " + err.Error() + " (install: go run github.com/mxschmitt/playwright-go/cmd/playwright install chromium)"}}
	}
	defer pw.Stop()
	su, err := url.Parse(shellURL)
	if err != nil {
		return []finding{{unverified, name, err.Error()}}
	}
	shellOrigin := su.Scheme + "://" + su.Host
	var fs []finding
	for _, bn := range o.browsers {
		var bt playwright.BrowserType
		switch bn {
		case "chromium":
			bt = pw.Chromium
		case "firefox":
			bt = pw.Firefox
		case "webkit":
			bt = pw.WebKit
		default:
			return append(fs, finding{unverified, name, "unknown browser " + bn})
		}
		opts := playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true), Timeout: playwright.Float(60000)}
		if bn == "chromium" && (su.Hostname() == "127.0.0.1" || su.Hostname() == "localhost") {
			// a shell served from loopback (a local build or preview, --live-url)
			// is a "private network" address to the played public parent:
			// Chromium would refuse to frame it. A published shell is not affected.
			opts.Args = []string{"--disable-features=PrivateNetworkAccessForNavigations,PrivateNetworkAccessForWorkers,PrivateNetworkAccessSendPreflights,PrivateNetworkAccessRespectPreflightResults,BlockInsecurePrivateNetworkRequests,LocalNetworkAccessChecks"}
		}
		b, err := bt.Launch(opts)
		if err != nil {
			fs = append(fs, finding{unverified, name + " [" + bn + "]", "the browser did not start: " + err.Error()})
			continue
		}
		for _, sc := range scenarios(d, shellURL, shellOrigin) {
			fs = append(fs, play(b, bn, name, sc, shellURL, d))
		}
		_ = b.Close()
	}
	return fs
}

type scenario struct {
	name     string
	top      string // the origin of the top page
	role     string // direct, nest, sibling
	inner    string // nest: the origin of the page that frames the shell
	cfg      map[string]any
	positive bool // the shell must answer; otherwise it must say nothing
	// silentID: only a reply carrying the probe's id is a violation (the shell
	// may legitimately have said ready)
	silentID bool
}

func nonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func scenarios(d Decl, shellURL, shellOrigin string) []scenario {
	probe := d.Probe
	if probe == "" {
		probe = "ping"
	}
	n := nonce()
	base := func(over map[string]any) map[string]any {
		m := map[string]any{"shellOrigin": shellOrigin, "hello": true, "helloNonce": n, "cmd": probe, "cmdNonce": n, "waitReady": false, "readyMs": 0, "quietMs": 2500}
		for k, v := range over {
			m[k] = v
		}
		return m
	}
	var sc []scenario
	for _, p := range d.Parents {
		sc = append(sc, scenario{name: "the declared parent " + p + " is answered", top: p, role: "direct", positive: true,
			cfg: base(map[string]any{"waitReady": true, "readyMs": 60000, "quietMs": 1500})})
	}
	p := d.Parents[0]
	sc = append(sc,
		scenario{name: "a foreign origin framing the shell gets nothing", top: foreignOrigin, role: "direct", cfg: base(nil)},
		scenario{name: "a foreign frame nested under the declared parent gets nothing", top: p, role: "nest", inner: foreignOrigin, cfg: base(nil)},
		scenario{name: "a hello with the wrong nonce gets nothing", top: p, role: "direct", cfg: base(map[string]any{"helloNonce": "wrong" + n})},
		scenario{name: "a command with the wrong nonce gets no reply", top: p, role: "direct", silentID: true,
			cfg: base(map[string]any{"cmdNonce": "wrong" + n})},
		scenario{name: "a command with no hello gets no reply", top: p, role: "direct", cfg: base(map[string]any{"hello": false})},
		scenario{name: "a sibling frame of the declared parent's origin gets nothing", top: p, role: "sibling", cfg: base(map[string]any{"hello": false, "sibling": true})},
	)
	return sc
}

func harnessURL(origin, role, shellURL, nonce, inner string) string {
	q := url.Values{"role": {role}, "shell": {shellURL}, "nonce": {nonce}}
	if inner != "" {
		q.Set("inner", inner)
	}
	return origin + harnessPath + "#" + q.Encode()
}

// play runs one scenario in one browser and judges it.
func play(b playwright.Browser, bn, name string, sc scenario, shellURL string, d Decl) finding {
	check := name + " [" + bn + "] " + sc.name
	ctx, err := b.NewContext()
	if err != nil {
		return finding{unverified, check, err.Error()}
	}
	defer ctx.Close()
	played := map[string]bool{foreignOrigin: true, nesterOrigin: true}
	for _, p := range d.Parents {
		played[p] = true
	}
	err = ctx.Route("**/*", func(r playwright.Route) {
		u, perr := url.Parse(r.Request().URL())
		if perr == nil && played[u.Scheme+"://"+u.Host] {
			if u.Path == harnessPath {
				_ = r.Fulfill(playwright.RouteFulfillOptions{Status: playwright.Int(200), ContentType: playwright.String("text/html"), Body: harnessHTML})
			} else {
				_ = r.Fulfill(playwright.RouteFulfillOptions{Status: playwright.Int(404), Body: ""})
			}
			return
		}
		_ = r.Continue()
	})
	if err != nil {
		return finding{unverified, check, err.Error()}
	}
	page, err := ctx.NewPage()
	if err != nil {
		return finding{unverified, check, err.Error()}
	}
	n, _ := sc.cfg["helloNonce"].(string)
	// the nonce in the shell's URL is the right one; the scenario may send another
	shellNonce := strings.TrimPrefix(n, "wrong")
	var topURL, hostOrigin string
	switch sc.role {
	case "nest":
		inner := harnessURL(sc.inner, "direct", shellURL, shellNonce, "")
		topURL = harnessURL(sc.top, "nest", "", "", inner)
		hostOrigin = sc.inner
	default:
		topURL = harnessURL(sc.top, sc.role, shellURL, shellNonce, "")
		hostOrigin = sc.top
	}
	if _, err := page.Goto(topURL, playwright.PageGotoOptions{Timeout: playwright.Float(30000)}); err != nil {
		return finding{unverified, check, "the harness page did not load: " + err.Error()}
	}
	var runner playwright.Frame
	deadline := time.Now().Add(30 * time.Second)
	for runner == nil && time.Now().Before(deadline) {
		for _, f := range page.Frames() {
			if strings.HasPrefix(f.URL(), hostOrigin+harnessPath) {
				if ok, _ := f.Evaluate("typeof window.run === 'function' && (document.getElementById('f') !== null)"); ok == true {
					runner = f
				}
			}
		}
		if runner == nil {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if runner == nil {
		return finding{unverified, check, "the harness frame never appeared"}
	}
	res, err := runner.Evaluate("(c) => window.run(c)", sc.cfg)
	if err != nil {
		return finding{unverified, check, "the run failed: " + err.Error()}
	}
	m, _ := res.(map[string]any)
	if e, ok := m["error"].(string); ok {
		return finding{unverified, check, e}
	}
	got, _ := m["got"].([]any)
	var texts []string
	hasReady, hasReply := false, false
	for _, g := range got {
		gm, _ := g.(map[string]any)
		data, _ := gm["data"].(string)
		texts = append(texts, data)
		if strings.Contains(data, `"ready"`) {
			hasReady = true
		}
		if strings.Contains(data, `"id":"p1"`) {
			hasReply = true
		}
	}
	switch {
	case sc.positive && hasReady && hasReply:
		return finding{pass, check, "ready, and the probe answered"}
	case sc.positive:
		seen := "no frame of the shell was found; frames:"
		for _, f := range page.Frames() {
			seen += " " + f.URL()
			if strings.HasPrefix(f.URL(), shellURL) {
				txt, _ := f.Evaluate("document.body ? document.body.textContent.slice(0, 120) : ''")
				seen = fmt.Sprintf("the shell's frame says %q", txt)
			}
		}
		return finding{fail, check, fmt.Sprintf("the declared parent was not served (ready=%t, reply=%t; %s): the frame does not work for its own parent, or does not follow the hello contract", hasReady, hasReply, seen)}
	case sc.silentID && !hasReply:
		return finding{pass, check, "no reply"}
	case sc.silentID:
		return finding{fail, check, "answered a command with the wrong nonce: " + oneLine(strings.Join(texts, " "))}
	case len(got) == 0:
		return finding{pass, check, "no message at all"}
	}
	return finding{fail, check, "the frame spoke to a page it must not: " + oneLine(strings.Join(texts, " "))}
}
