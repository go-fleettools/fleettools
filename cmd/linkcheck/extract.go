package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// urlRe is the extractor the documentation workflows used (grep -oE). The
// class matters: an extractor stopping only at ) and " carries a closing > or <
// into the request.
var urlRe = regexp.MustCompile("https?://[^)\"<>\\s]+")

// builtinSkips are hosts and shapes that prove nothing or are not hosts.
var builtinSkips = []string{
	"img.shields.io", // renders whatever it is asked for
	"example.com", "example.org", "example.net",
	"localhost", "127.0.0.1", ".invalid",
}

// Extract returns the distinct URLs of text, sorted, trailing punctuation
// removed, with the built-in exclusions and the caller's skips applied. A URL
// is skipped when it contains one of the skip substrings, or is under
// `http://web/` (the Networking page's by-name example).
func Extract(text []byte, skips []string) []string {
	seen := map[string]bool{}
	for _, m := range urlRe.FindAll(text, -1) {
		u := strings.TrimRight(string(m), ".,`")
		if u == "" || skipped(u, skips) {
			continue
		}
		seen[u] = true
	}
	out := make([]string, 0, len(seen))
	for u := range seen {
		out = append(out, u)
	}
	sort.Strings(out)
	return out
}

func skipped(u string, skips []string) bool {
	if strings.HasPrefix(u, "http://web/") {
		return true
	}
	for _, s := range builtinSkips {
		if strings.Contains(u, s) {
			return true
		}
	}
	for _, s := range skips {
		if s != "" && strings.Contains(u, s) {
			return true
		}
	}
	return false
}

// ReadURLs extracts the URLs of every regular file under the given files and
// directories. Binary files (a NUL in the first 512 bytes) are not read. It
// also returns how many files it read, so a caller can tell "no links" from
// "no pages".
func ReadURLs(paths, skips []string) (urls []string, files int, err error) {
	seen := map[string]bool{}
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
		for _, u := range Extract(b, skips) {
			seen[u] = true
		}
		return nil
	}
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, files, err
		}
		if !fi.IsDir() {
			if err := read(p); err != nil {
				return nil, files, err
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
			return nil, files, err
		}
	}
	for u := range seen {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	return urls, files, nil
}
