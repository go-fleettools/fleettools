// Package secretscan finds the places where a daemon takes a secret as a
// plaintext value (a config field, a flag, an environment variable, a unit
// file) instead of by reference, and reports them with the denominator that
// makes the answer worth reading.
//
// # PRECISION OVER RECALL
//
// A name heuristic that cries wolf on `max_tokens` gets switched off, and a
// gate that is switched off protects nothing. So a name is judged by its WORDS
// (never by a substring), the last word must be the secret word, and a
// trailing `file`, `path`, `from` or `env` changes the CLASS instead of
// hiding the finding: `token_file` is a path to a secret, `password_from` is a
// secretsource reference, and neither is the secret itself. The committed
// corpus testdata/names.tsv holds the true positives and the false positives
// the heuristic was written against; adding a name there is how it is amended.
package secretscan

import (
	"strings"
	"unicode"
)

// Class says how a secret input receives its secret.
type Class string

const (
	// None: the name does not denote a secret.
	None Class = ""
	// Plaintext: the value IS the secret. The class a gate refuses.
	Plaintext Class = "plaintext"
	// Path: a `*_file` style path to a file holding the secret.
	Path Class = "path"
	// Ref: a `*_from` secretsource reference (`systemd-creds:name`, ...).
	Ref Class = "ref"
	// Env: the NAME of an environment variable that holds the secret.
	Env Class = "env"
)

var suffixClass = map[string]Class{
	"file": Path, "path": Path, "filename": Path,
	"from": Ref,
	"env":  Env,
}

var secretWords = map[string]bool{
	"password": true, "passwd": true, "passphrase": true, "secret": true,
	"credential": true, "credentials": true, "apikey": true,
	"token": true, "tokens": true,
	// SMTP_PASS (weft-loom-server) and the one-word AWS spellings.
	"pass": true, "accesskey": true, "secretkey": true,
}

// keyPrefix: a bare `key` is a map key, a cache key, a sort key. It denotes a
// secret only behind one of these words.
var keyPrefix = map[string]bool{
	"api": true, "private": true, "secret": true, "signing": true,
	"encryption": true, "master": true,
	// weft-ha-irods: --negotiation-key, --control-plane-key, --zone-key hold
	// key material typed on the command line.
	"negotiation": true, "plane": true, "zone": true,
	"hmac": true, "jwt": true, "shared": true,
	// WEFT_CUBEFS_ACCESS_KEY, AWS_SECRET_ACCESS_KEY, WEBUI_SESSION_KEY (a
	// cookie HMAC key), AUTH_KEY.
	"access": true, "session": true, "auth": true,
}

// quantifier words turn `token` into a COUNT (LLM tokens, a rate limit), which
// is the false positive `max_tokens` stands for.
var quantifier = map[string]bool{
	"max": true, "min": true, "num": true, "number": true, "count": true,
	"total": true, "prompt": true, "completion": true, "limit": true,
	"budget": true, "context": true, "input": true, "output": true,
}

// Tokens splits a name into lower-case words at separators and at case
// changes: `APIKey` is api, key; `client_secret_file` is client, secret, file.
func Tokens(name string) []string {
	var toks []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			toks = append(toks, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	rs := []rune(name)
	for i, r := range rs {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && len(cur) > 0 &&
			(!unicode.IsUpper(rs[i-1]) || (i+1 < len(rs) && unicode.IsLower(rs[i+1]))):
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return toks
}

// Classify says whether name denotes a secret input and how it is received.
func Classify(name string) Class {
	toks := Tokens(name)
	if awsKeyID(toks) {
		return Plaintext
	}
	class := Plaintext
	if n := len(toks); n > 0 {
		if c, ok := suffixClass[toks[n-1]]; ok {
			class, toks = c, toks[:n-1]
		}
	}
	if !denotes(toks) {
		return None
	}
	// GOOGLE_APPLICATION_CREDENTIALS names a path, and is the one widely
	// used spelling whose last word lies about it.
	if class == Plaintext && toks[len(toks)-1] == "credentials" {
		for _, t := range toks {
			if t == "application" {
				return Path
			}
		}
	}
	return class
}

// awsKeyID: AWS_ACCESS_KEY_ID ends in `id`, so no secret word is last, yet the
// SDK reads it together with the secret key and it names the account.
func awsKeyID(t []string) bool {
	n := len(t)
	return n >= 4 && t[0] == "aws" && t[n-3] == "access" && t[n-2] == "key" && t[n-1] == "id"
}

func denotes(t []string) bool {
	n := len(t)
	if n == 0 {
		return false
	}
	head := t[:n-1]
	switch last := t[n-1]; {
	case last == "key":
		if n < 2 || !keyPrefix[t[n-2]] {
			return false
		}
		head = t[:n-2]
	case !secretWords[last]:
		return false
	}
	for _, h := range head {
		if quantifier[h] {
			return false
		}
	}
	return true
}
