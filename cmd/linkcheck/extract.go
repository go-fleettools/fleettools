package main

import (
	"bytes"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// urlRe is the extractor the documentation workflows used (grep -oE). The
// class matters: an extractor stopping only at ) and " carries a closing > or <
// into the request.
var urlRe = regexp.MustCompile("https?://[^)\"<>\\s]+")

// Why a URL was not checked.
const (
	reasonUser        = "--skip"
	reasonBuiltin     = "built-in"
	reasonNoHost      = "no host"
	reasonPlaceholder = "placeholder host"
)

// placeholderDomains are names reserved for documentation, testing and local
// use: the host itself and everything under it is skipped. RFC 2606 (example,
// test, invalid, example.com/.org/.net, localhost) and RFC 6761 (adds local
// through mDNS, RFC 6762) say no answer from them proves anything.
var placeholderDomains = []string{
	"example", "example.com", "example.org", "example.net",
	"invalid", "test", "local", "localhost",
}

// Skip is one URL that was found and deliberately not checked, with the reason.
type Skip struct {
	URL    string
	Reason string
}

// skipReason returns "" when the URL is to be checked, else the reason it is not.
// User --skip substrings come first, so an explicit choice is counted as such.
func skipReason(u string, skips []string) string {
	for _, s := range skips {
		if s != "" && strings.Contains(u, s) {
			return reasonUser
		}
	}
	if strings.HasPrefix(u, "http://web/") || strings.Contains(u, "img.shields.io") {
		// `http://web/` is a container name only an engine's DNS resolves;
		// a shields badge renders whatever it is asked for.
		return reasonBuiltin
	}
	host, ok := hostOf(u)
	if !ok {
		return reasonNoHost
	}
	if placeholderHost(host) {
		return reasonPlaceholder
	}
	return ""
}

// hostOf returns the host of an extracted URL, and false when there is none
// that could be dialled: a bare scheme (`https://` cut off at a "<" in
// `https://<host>/`), a scheme glued to prose or a backtick, a shell variable.
func hostOf(u string) (string, bool) {
	p, err := url.Parse(u)
	if err != nil {
		return "", false
	}
	h := p.Hostname()
	if h == "" {
		return "", false
	}
	if net.ParseIP(h) != nil {
		return h, true
	}
	for i, r := range h {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
		case i > 0 && (r == '.' || r == '-' || r == '_'):
		default:
			return "", false
		}
	}
	return h, true
}

func placeholderHost(h string) bool {
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	for _, d := range placeholderDomains {
		if h == d || strings.HasSuffix(h, "."+d) {
			return true
		}
	}
	return false
}

// Scan returns the distinct URLs of text to check, sorted, trailing
// punctuation removed, and the distinct URLs it found but did not check, with
// the reason for each.
func Scan(text []byte, skips []string) (urls []string, skipped []Skip) {
	seen := map[string]bool{}
	for _, m := range urlRe.FindAll(text, -1) {
		u := strings.TrimRight(string(m), ".,`")
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		if r := skipReason(u, skips); r != "" {
			skipped = append(skipped, Skip{u, r})
			continue
		}
		urls = append(urls, u)
	}
	sort.Strings(urls)
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].URL < skipped[j].URL })
	return urls, skipped
}

// Extract returns the URLs of text to check; see Scan.
func Extract(text []byte, skips []string) []string {
	urls, _ := Scan(text, skips)
	return urls
}

// ReadURLs extracts the URLs of every regular file under the given files and
// directories, and the distinct URLs it skipped. Binary files (a NUL in the
// first 512 bytes) are not read. It also returns how many files it read, so a
// caller can tell "no links" from "no pages".
func ReadURLs(paths, skips []string) (urls []string, skipped []Skip, files int, err error) {
	seen := map[string]bool{}
	seenSkip := map[string]bool{}
	read := func(p string) error {
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		head := b
		if len(head) > 512 {
			head = head[:512]
		}
		if bytes.IndexByte(head, 0) >= 0 {
			return nil
		}
		files++
		us, sk := Scan(b, skips)
		for _, u := range us {
			seen[u] = true
		}
		for _, s := range sk {
			if !seenSkip[s.URL] {
				seenSkip[s.URL] = true
				skipped = append(skipped, s)
			}
		}
		return nil
	}
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, nil, files, err
		}
		if !fi.IsDir() {
			if err := read(p); err != nil {
				return nil, nil, files, err
			}
			continue
		}
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			return read(path)
		})
		if err != nil {
			return nil, nil, files, err
		}
	}
	for u := range seen {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].URL < skipped[j].URL })
	return urls, skipped, files, nil
}
